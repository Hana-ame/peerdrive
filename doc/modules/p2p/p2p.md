# P2P 网络架构

> **最后修改**: 2026-04-27 · **版本**: v2.0
> **上一版本**: v1.0 (2026-04-26) — 基础 libp2p 节点、mDNS 发现、Exchange 协议
> **本版新增**: 连接管理器（心跳/重连）、分片传输（256KB chunk/8并发）、STUN 配置、SIZE 协议

## 概述

Peerdrive 基于 libp2p 构建 P2P 对等网络，支持节点发现、文件传输、DHT 路由和中继转发。

## 协议栈

| 层 | 协议/组件 | 说明 |
|---|---|---|
| 传输层 | TCP, QUIC, WebSocket | 底层通信协议 |
| 安全层 | Noise (自动协商) | libp2p 内置加密 |
| 流复用 | Yamux | 单连接多流复用 |
| NAT 穿越 | AutoNAT v2, DCUtR, NAT-PMP | 自动检测网络类型并打洞 |
| 中继 | Circuit Relay v2 | 公网中继转发流量 |
| 发现 | mDNS (局域网), Kademlia DHT | 节点发现和路由 |
| 应用层 | /peerdrive/exchange/1.0.0, /peerdrive/chunk/1.0.0 | 文件传输协议 |

## 应用层协议

### Exchange 协议 (`/peerdrive/exchange/1.0.0`)

用于整文件传输和文件大小查询。

**整文件请求：**
```
请求: <64-char hex SHA256>\n
响应: OK <byte_count>\n<raw binary data>
错误: ERR <message>\n
```

**文件大小查询：**
```
请求: SIZE <64-char hex SHA256>\n
响应: OK <byte_count>\n
错误: ERR <message>\n
```

### Chunk 协议 (`/peerdrive/chunk/1.0.0`)

用于分片传输大文件。

```
请求: CHUNK <64-char hex SHA256> <offset> <size>\n
响应: <raw binary chunk data>
错误: ERR <message>\n
```

限制: 每个 chunk 最大 256KB。

### Announce 协议 (`/peerdrive/announce/1.0.0`)

节点向其他节点宣告自己拥有某个文件。

```
请求: <64-char hex SHA256>\n
响应: OK\n
```

收到 Announce 后，节点自动向 DHT 注册为提供者。

## 连接管理

### 自动连接

- **mDNS 发现**：局域网内自动发现节点并连接
- **Bootstrap 节点**：启动时尝试连接配置的引导节点
- **DHT 发现**：通过 DHT 查找文件提供者时尝试连接

### 心跳检测

每 30 秒检查已连接节点的状态，对断开的节点尝试重连。

### 重连策略

- 初始重连间隔：10 秒
- 退避上限：5 分钟
- 连接超时：15 秒

## 文件传输

### 小文件传输

- 使用 Exchange 协议单次请求获取完整文件
- 超时：30 秒

### 大文件分片传输

- 文件被分为 256KB 的 Chunk
- 最多 8 个并发 Chunk 请求
- 支持从多个提供者并行下载不同分片
- 下载完成后验证 SHA256 哈希
- 支持进度回调，实时显示下载进度
- 传输超时：5 分钟

### 文件查找优先级

1. 本地 storage 目录 (SHA256 内容寻址)
2. file_providers 数据库记录（本地路径）
3. P2P 网络（DHT + 直接请求）

## WebSocket 传输

用于浏览器节点的文件传输。

- 端点：`GET /ws/transfer`
- 消息格式：JSON + 二进制帧
- 支持 ping/pong 心跳
- 文件请求通过 WS 广播到所有连接的浏览器节点

### 消息类型

**客户端 -> 服务端:**
```json
{"type": "request", "hash": "sha256..."}
{"type": "ping"}
```

**服务端 -> 客户端:**
```json
{"type": "response", "hash": "sha256...", "size": 1234}
（后跟二进制帧）
{"type": "pong"}
{"type": "error", "hash": "sha256...", "message": "not found"}
```

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `PEERDRIVE_P2P_ENABLE` | 是否启用 P2P | `true` |
| `PEERDRIVE_P2P_LISTEN` | libp2p 监听地址 | `/ip4/0.0.0.0/tcp/0` |
| `PEERDRIVE_BOOTSTRAP_PEER` | 引导节点地址 | `""` |
| `PEERDRIVE_MDNS_ENABLE` | 是否启用 mDNS | `true` |
| `PEERDRIVE_RELAY_ENABLE` | 是否启用中继 | `false` |
| `PEERDRIVE_RELAY_MODE` | 中继模式 (client/server/off) | `client` |
| `PEERDRIVE_STATIC_RELAYS` | 静态中继地址列表 | `""` |
| `PEERDRIVE_HOLE_PUNCH` | 是否启用打洞 | `true` |
| `PEERDRIVE_PUBLIC_REACHABLE` | 节点是否公网可达 | `false` |
| `PEERDRIVE_AUTO_NAT` | 是否启用 AutoNAT | `true` |
| `PEERDRIVE_NAT_PORTMAP` | 是否启用 NAT-PMP | `false` |
| `PEERDRIVE_PUBLIC_DOMAIN` | 节点公网访问域名 | `""` |

## API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/p2p/status` | P2P 状态（含连接统计和传输任务） |
| GET | `/p2p/node` | 本节点信息 |
| GET | `/p2p/peers` | 已连接节点列表 |
| GET | `/p2p/discovered` | 已发现节点列表 |
| GET | `/p2p/ping/:peer_id` | Ping 节点 |
| POST | `/p2p/connect` | 手动连接节点 |
| POST | `/p2p/announce` | 宣告拥有文件 |
| POST | `/p2p/fetch` | P2P 获取合集 |
| POST | `/p2p/sync` | 从节点同步文件 |
| POST | `/p2p/push` | 推送合集到节点 |
| POST | `/p2p/request-file` | 广播文件请求 |
| GET | `/p2p/ws/info` | WebSocket 连接信息 |
| GET | `/ws/transfer` | WebSocket 传输端点 |

## 运行模式

### 普通节点 (默认)

```bash
PEERDRIVE_P2P_ENABLE=true go run ./cmd/server/main.go
```

### 中继服务端 (公网超级节点)

```bash
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=server \
PEERDRIVE_PUBLIC_REACHABLE=true \
PEERDRIVE_PUBLIC_DOMAIN=relay.example.com \
go run ./cmd/server/main.go
```

### 纯中继节点（不存储文件）

```bash
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=server \
PEERDRIVE_STORAGE_ENABLE=false \
go run ./cmd/server/main.go
```

### 中继客户端（NAT 后节点）

```bash
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=client \
PEERDRIVE_STATIC_RELAYS="/ip4/1.2.3.4/tcp/4001/p2p/12D3..." \
go run ./cmd/server/main.go
```

## 测试

```bash
# 启动两个节点测试 P2P 传输
# 节点 A
PEERDRIVE_PORT=3001 go run ./cmd/server/main.go

# 节点 B (使用不同的端口)
PEERDRIVE_PORT=3002 PEERDRIVE_P2P_LISTEN=/ip4/0.0.0.0/tcp/0 \
go run ./cmd/server/main.go

# 运行 P2P 测试（注意：本文档描述的是 2026-08-16 已删除的 libp2p 栈，
# 当前互联层为 PeerJS/WebRTC，见 doc/REFACTOR.md；test/p2p_transfer.sh 已随栈删除）
```
