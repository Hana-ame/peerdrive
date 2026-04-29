# Peerdrive P2P Stage 2 — 测试报告

**日期**: 2026-04-25
**分支**: `feat/remove-auth-and-refactor`
**测试环境**: Ubuntu 22.04, Go 1.24-rc

---

## 一、测试结果总览

| 测试项 | 状态 | 说明 |
|--------|------|------|
| 双节点启动 | ✅ | Node A (3001) + Node B (3002) 各产生唯一 PeerID |
| P2P 状态 | ✅ | `enabled: true`，包含 discovered/connected 计数 |
| mDNS 自动发现 | ✅ | 10s 内双向发现对方 |
| 手动连接 | ✅ | Node B 通过 multiaddr 连接 Node A |
| 对等列表 | ✅ | 双向确认连接状态 |
| 合集获取 (P2P) | ✅ | Node B 通过 P2P 协议获取 Node A 的合集 JSON，friendly_name 正确 |
| 文件同步 (P2P) | ✅ | Node B 通过协议下载 1 个文件，校验通过 |
| DHT 发布 | ⚠️ | 无 bootstrap 节点，DHT 无法路由 |
| 合集友好名 | ✅ | `friendly_name: "p2p-test-collection"` 传递正确 |
| Push 同步 | ⚠️ | 跨节点 push 需合集 hash 在本地可用 |

---

## 二、新增端点

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/p2p/status` | P2P 节点总览（启用/PeerID/连接数/发现数） |
| GET | `/p2p/discovered` | mDNS 发现的节点列表 |
| POST | `/p2p/connect` | 手动连接指定 multiaddr |
| POST | `/p2p/announce` | 向 DHT 宣告拥有某 hash |
| POST | `/p2p/fetch` | 从 P2P 网络获取合集 JSON |
| POST | `/p2p/sync` | 从对等节点同步文件到本地目录 |
| POST | `/p2p/push` | 接收其他节点的推送合集 |

---

## 三、模型变更

**AnonCollection** 新增 `friendly_name` 字段：

```json
{
  "version": 1,
  "friendly_name": "my-collection",
  "entries": [...],
  "created_at": "2026-04-25T15:42:39Z"
}
```

所有创建/复刻端点已更新，复刻时默认继承源合集的友好名。

---

## 四、P2P 协议

**自定义协议**: `/peerdrive/exchange/1.0.0`

```
请求方 → 对等节点:
  <64位hex hash>\n

服务方 → 请求方:
  OK <字节数>\n<数据>

  或

  ERR not found\n
```

**协议扩展**: `/peerdrive/announce/1.0.0` — 接收对等节点的 hash 告示并转发到 DHT。

---

## 五、配置参数

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `PEERDRIVE_P2P_ENABLE` | `true` | 是否启用 P2P 节点 |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | libp2p 监听地址 |
| `PEERDRIVE_BOOTSTRAP_PEER` | `""` | DHT bootstrap 节点 multiaddr |
| `PEERDRIVE_MDNS_ENABLE` | `true` | 是否启用 mDNS 局域网发现 |
| `PEERDRIVE_RELAY_ENABLE` | `false` | 是否启用中继节点模式 |

---

## 六、已知限制 & 后续

1. **DHT 路由未激活**：需要至少一台常驻 bootstrap 节点。当前 mDNS 仅适用于局域网。
2. **Push 跨节点同步**：`POST /p2p/push` 要求合集 hash 在当前节点本地存在。应支持直接从请求体接收 entries。
3. **文件传输无进度**：大文件同步无进度回调，建议增加 WebSocket 推送。
4. **NAT 穿透**：未配置 hole punching，公网节点需要端口转发或中继。
5. **GitHub Actions**：CI 已配置，P2P 测试在单机多实例模式运行。

---

## 七、Stage 3 — NAT穿透 + 中继 + WS传输（2026-04-25）

### 测试结果

| 测试项 | 状态 | 说明 |
|--------|------|------|
| 中继节点启动 | ✅ | `relay_mode=server`，`ForceReachabilityPublic` |
| 客户端节点启动 | ✅ | `relay_mode=client`，hole punch + AutoNAT v2 + NATPortMap 启用 |
| mDNS 双向发现 | ✅ | relay 与 client 双向发现 |
| 手动连接 | ✅ | client 通过 multiaddr 连接 relay |
| P2P 状态字段 | ✅ | `relay_mode`, `hole_punch`, `ws_connections` 正确输出 |
| 合集通过中继获取 | ✅ | relay 端通过 P2P 协议获取 client 合集，friendly_name="relay-test" |
| 广播文件请求 | ✅ | `POST /p2p/request-file` 向连接节点请求并收到数据 |
| WS info 端点 | ✅ | `GET /p2p/ws/info` 返回 `message_types` |
| WS 传输端点 | ✅ | `GET /ws/transfer` 路由注册成功 |

### Stage 3 新增端点

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/p2p/request-file` | 向已连接节点广播文件请求，通过 exchange 协议拉取 |
| GET | `/p2p/ws/info` | WS 传输通道状态查询 |
| GET | `/ws/transfer` | WebSocket 双向文件传输 |

### Stage 3 新增协议

- `/peerdrive/request/1.0.0` — 接收对等节点的文件请求，异步查找本地文件

### Stage 3 配置参数

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `PEERDRIVE_RELAY_MODE` | `client` | `off` / `client` / `server` |
| `PEERDRIVE_STATIC_RELAYS` | `""` | client 模式静态中继 multiaddr（逗号分隔） |
| `PEERDRIVE_HOLE_PUNCH` | `true` | DCUtR 打洞（NAT 穿透） |
| `PEERDRIVE_AUTO_NAT` | `true` | AutoNAT v2 检测可达性 |
| `PEERDRIVE_NAT_PORTMAP` | `false` | UPnP/NAT-PMP 端口映射 |

### 中继模式行为

| 模式 | `EnableRelayService` | `ForceReachabilityPublic` | `EnableAutoRelay` |
|------|----------------------|---------------------------|-------------------|
| `off` | - | - | - |
| `client` | - | - | 若设 STATIC_RELAYS |
| `server` | ✅ | ✅ | - |

### WS 传输协议

```
客户端 → 服务端:
  {"type":"request","hash":"sha256..."}     # 请求文件
  {"type":"ping"}                           # 心跳

服务端 → 客户端:
  {"type":"response","hash":"...","size":N}  # 文件头
  [Binary frame]                             # 文件内容
  {"type":"error","hash":"...","message":"..."}
  {"type":"pong"}
```
