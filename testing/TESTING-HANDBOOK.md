# Peerdrive 测试手册

> 面向测试人员和开发者的完整测试指南
> 更新: 2026-04-29

---

## 目录

1. [环境准备](#1-环境准备)
2. [测试架构概览](#2-测试架构概览)
3. [快速开始](#3-快速开始)
4. [测试套件详解](#4-测试套件详解)
5. [手动测试流程](#5-手动测试流程)
6. [故障排查](#6-故障排查)
7. [测试矩阵](#7-测试矩阵)
8. [混沌测试](#8-混沌测试)

---

## 1. 环境准备

### 1.1 依赖安装

```bash
# Go 1.24+
go version

# Python 3 (用于测试脚本 JSON 解析)
python3 --version

# curl
curl --version

# jq (可选，用于命令行 JSON 处理)
sudo apt-get install jq
```

### 1.2 系统代理配置

如果系统配置了 HTTP 代理（如 Privoxy），测试脚本需绕过代理：

```bash
# 方法 1: 使用 no_proxy 环境变量
export no_proxy='*'

# 方法 2: curl 命令使用 -x "" 参数
curl -x "" http://localhost:3000/ping

# 方法 3: 测试脚本自动处理（大部分脚本已内置）
```

### 1.3 端口规划

| 组件 | 默认端口 | 说明 |
|------|----------|------|
| Peerdrive 主服务 | 3000 | Go Gin HTTP API |
| Peerdrive 节点 B | 3001 | 双节点测试用 |
| Peerdrive 节点 C | 3002 | 三节点测试用 |
| Registration Server | 4000 | JWT 认证服务 |
| React Dev Server | 5173 | Vite 前端开发 |
| BT Tracker (测试) | 6969 | Python tracker 模拟 |
| BT Seeder (测试) | 6890 | Python seeder |
| IPFS 模拟节点 | 9001 | IPFS 测试节点 |
| E2E 测试 | 3999 | 自包含测试端口 |

### 1.4 环境变量速查

```bash
# 核心配置
PEERDRIVE_STORAGE=./storage        # 文件存储目录
PEERDRIVE_STORAGE_ENABLE=true      # 存储开关
PORT=3000                          # HTTP 端口

# P2P 配置
PEERDRIVE_P2P_ENABLE=true          # P2P 开关
PEERDRIVE_MDNS_ENABLE=true         # mDNS 局域网发现
PEERDRIVE_RELAY_ENABLE=true        # Relay 中继开关
PEERDRIVE_RELAY_MODE=client        # client / server / off
PEERDRIVE_HOLE_PUNCH=true          # NAT 打洞
PEERDRIVE_AUTO_NAT=true            # AutoNAT 检测
PEERDRIVE_PUBLIC_REACHABLE=true    # 公网可达（relay server）
PEERDRIVE_BOOTSTRAP_PEER=<multiaddr>  # Bootstrap 节点

# BT 配置
PEERDRIVE_BT_DHT_ENABLE=true       # BT DHT 开关

# 安全（重要！）
PEERDRIVE_JWT_SECRET=<random>      # JWT 密钥（生产环境必须设置）
```

---

## 2. 测试架构概览

```
测试金字塔
┌─────────────────────────┐
│     E2E 测试             │  ← e2e-all.sh (85 断言, 12 段)
│     真实网络测试          │  ← BT DHT / IPFS 公网
├─────────────────────────┤
│    集成测试              │  ← p2p.sh, relay.sh, bt-full-test.sh
│    多节点/模块间         │
├─────────────────────────┤
│    API 测试              │  ← upload.sh, register.sh, anon-collection.sh
│    单节点 HTTP           │
├─────────────────────────┤
│    单元测试              │  ← go test ./... (66 tests)
│    Go 包级别             │
└─────────────────────────┘
```

### 测试文件位置

```
go/
├── *_test.go                    # Go 单元测试（源码内嵌）
├── test/
│   ├── e2e-all.sh               # E2E 全端点测试（自包含）
│   ├── test.sh                  # 完整集成测试（需要运行中的 server）
│   ├── upload.sh                # 上传功能测试
│   ├── register.sh              # 文件注册测试
│   ├── anon-collection.sh       # 匿名合集测试
│   ├── p2p.sh                   # P2P 双节点测试（自包含）
│   ├── relay.sh                 # Relay 穿透测试（自包含）
│   ├── bt-full-test.sh          # BT 全功能测试
│   ├── ipfs-full-test.sh        # IPFS 全功能测试
│   ├── p2p-full-test.sh         # P2P 全功能测试
│   ├── storage-full-test.sh     # Storage 全功能测试
│   ├── auth-full-test.sh        # Auth 全功能测试
│   ├── webrtc_signal_test.sh    # WebRTC 信令测试
│   └── *-full-test-results.txt  # 测试结果文件
```

---

## 3. 快速开始

### 3.1 一键运行全部测试

```bash
cd /mnt/d/WorkPlace/peerdrive/go

# 1. 单元测试 — 最快，无需启动服务
go test ./... -count=1

# 2. E2E 测试 — 完整 HTTP 端点覆盖
bash test/e2e-all.sh

# 3. 模块专项测试（需要服务在 :3000 运行）
bash test/all.sh
```

### 3.2 最小验证（30 秒）

```bash
# 启动服务
cd /mnt/d/WorkPlace/peerdrive/go
PORT=3999 PEERDRIVE_STORAGE=/tmp/pd-test go run ./cmd/server/main.go &

# 等待启动
sleep 3

# 验证
curl -x "" http://localhost:3999/ping
# → pong

# 停止
kill %1
```

---

## 4. 测试套件详解

### 4.1 Go 单元测试 (`go test ./...`)

**无需启动服务**，直接运行。

```bash
cd go
go test ./... -count=1
```

| 包 | 测试数 | 覆盖内容 |
|----|--------|----------|
| `internal/config` | 10 | Load() 默认值、env 解析、bool 处理、relay mode |
| `internal/model` | 6 | Collection/AnonCollection 结构体 |
| `internal/repository` | 5 | FileMeta CRUD、Provider 管理 |
| `internal/service` | 29 | 文件注册/上传/校验、匿名合集、路径过滤 |
| `internal/controller` | 17 | Ping、Collection CRUD、Commit/Rollback |

预期输出：
```
ok  peerdrive/internal/config     0.021s
ok  peerdrive/internal/controller  0.087s
ok  peerdrive/internal/model       0.007s
ok  peerdrive/internal/repository  0.020s
ok  peerdrive/internal/service     0.314s
```

### 4.2 E2E 全端点测试 (`test/e2e-all.sh`)

**最重要的一体化测试**，自包含（自己编译、启动、测试、清理）。

```bash
cd go
bash test/e2e-all.sh
```

**12 个测试段**：

| # | 段名 | 断言 | 测试内容 |
|---|------|------|----------|
| 1 | Health | 2 | 服务启动、返回 pong |
| 2 | File Upload | 10 | 新文件/重复/已存在/存储路径 |
| 3 | File Verify | 3 | 合法 hash / 无效 hash |
| 4 | SHA256 Download | 3 | 下载/比对/404 |
| 5 | Register Local | 5 | 单文件/重复/文件夹注册 |
| 6 | Register Folder | 2 | 递归注册/文件数 |
| 7 | Anonymous Collections | 22 | CRUD/路径穿越/commit/fork/download |
| 8 | Named Collections | 22 | 创建/条目/版本/rollback |
| 9 | Fork/Merge/Pull | 4 | Fork/Merge/Pull 操作 |
| 10 | File Delete | 2 | 删除/后续访问 404 |
| 11 | Tasks | 2 | 任务列表/不存在任务 |
| 12 | Edge Cases | 5 | 不存在用户/无效 hash/空 body |

预期输出：`PASS: 84  FAIL: 0  WARN: 1  TOTAL: 85`

> WARN: 空 body `{}` 创建 collection 返回 200（边界行为），不影响功能。

### 4.3 模块专项测试

这些测试假设服务器已在端口 3000 运行。

#### Upload 测试
```bash
# 先启动服务
PORT=3000 PEERDRIVE_STORAGE=./storage go run ./cmd/server/main.go &

# 运行测试
bash test/upload.sh
```
测试：新文件上传(201)、重复上传(200 already_exists)、第二个文件(不同hash)

#### Register 测试
```bash
bash test/register.sh
```
测试：单文件注册→验证→下载、文件夹递归注册、重复注册幂等性

#### Anonymous Collection 测试
```bash
bash test/anon-collection.sh
```
测试：创建合集、路径穿越拒绝、获取JSON、下载文件、Fork

### 4.4 P2P 双节点测试 (`test/p2p.sh`)

**自包含**（编译两个节点，分别启动在 3001/3002）。

```bash
cd go
bash test/p2p.sh
```

测试流程：
1. Node A (3001) ping
2. Node B (3002) ping
3. P2P status 验证
4. mDNS 发现（软断言，10 秒）
5. A 注册文件 + 创建合集
6. A 宣告 hash
7. B 手动 connect 到 A
8. Peer list 验证
9. B P2P fetch 获取合集
10. P2P sync 同步
11. P2P push 推送

环境变量：`PEERDRIVE_P2P_ENABLE=true PEERDRIVE_MDNS_ENABLE=true PEERDRIVE_RELAY_ENABLE=false`

### 4.5 Relay 穿透测试 (`test/relay.sh`)

**自包含**，测试 relay 模式下的节点通信。

```bash
cd go
bash test/relay.sh
```

测试流程：
1. Relay 节点 (3001, relay_mode=server) ping
2. Client 节点 (3002, hole_punch=true) ping
3. mDNS 发现（软断言）
4. Client 手动 connect 到 Relay
5. Peer list 验证
6. Client 注册文件 + 合集 + 宣告
7. Relay P2P fetch 获取合集
8. WS info 端点验证
9. Request-file 广播

### 4.6 BT 全功能测试 (`test/bt-full-test.sh`)

```bash
cd go
bash test/bt-full-test.sh
# 预期: 38/38 PASS, 0 FAIL
```

覆盖：
- BT DHT 启动/状态/节点数
- BT Announce / Find
- BEP 44 Put/Get（不可变数据存储）
- BEP 51 Sample（DHT 采样）
- Torrent 解析 / Magnet 解析
- Wire Protocol handshake / piece 下载
- BT 下载管理（pause/resume/seed/unseed/delete）

### 4.7 WebRTC 信令测试 (`test/webrtc_signal_test.sh`)

```bash
cd go
bash test/webrtc_signal_test.sh
# 预期: 23/23 PASS
```

覆盖：注册、房间加入/离开、SDP 交换、ICE 候选、文件宣告/发现、直接消息、3 人房间、房间隔离。

### 4.8 IPFS 全功能测试 (`test/ipfs-full-test.sh`)

```bash
cd go
bash test/ipfs-full-test.sh
```

覆盖：IPFS CID 索引、Pin/Unpin、网关健康检查、IPFS 兼容模式切换。

### 4.9 Storage 全功能测试 (`test/storage-full-test.sh`)

```bash
cd go
bash test/storage-full-test.sh
```

覆盖：SHA256 下载、CID 双索引、文件上传/注册/删除/校验、Range 下载、URL 注册、WebDAV、文件复制。

### 4.10 Auth 全功能测试 (`test/auth-full-test.sh`)

```bash
cd go
bash test/auth-full-test.sh
# 预期: 20/20 PASS
```

覆盖：用户注册/登录、JWT 验证、Relay 注册/心跳/列表、Group 管理、Comment 系统。

### 4.11 前端测试

```bash
cd react

# Playwright E2E 测试
npx playwright test

# Smoke test (16 tests) — 页面加载、元素渲染
# Functional test (18 tests) — 用户交互流程
```

---

## 5. 手动测试流程

### 5.1 基础健康检查

```bash
# 1. 确认服务在线
curl -x "" http://localhost:3000/ping
# → pong

# 2. 确认 P2P 运行
curl -x "" http://localhost:3000/p2p/status | jq
# → {"enabled":true, "peer_id":"12D3KooW...", "relay_mode":"..."}

# 3. 确认 BT DHT 运行
curl -x "" http://localhost:3000/p2p/bt/status | jq
# → {"enabled":true, "listen_addr":"0.0.0.0:6881", "num_nodes":127}
```

### 5.2 文件上传→下载验证

```bash
# 1. 创建测试文件
echo "Hello Peerdrive Test $(date)" > /tmp/pd-test.txt
ORIGINAL_SHA256=$(sha256sum /tmp/pd-test.txt | cut -d' ' -f1)

# 2. 上传
RESULT=$(curl -s -x "" -F "file=@/tmp/pd-test.txt" http://localhost:3000/files/upload)
HASH=$(echo "$RESULT" | jq -r .hash)
echo "Hash: $HASH"

# 3. 验证 hash 一致
test "$HASH" = "$ORIGINAL_SHA256" && echo "✓ Hash 匹配"

# 4. 下载
curl -s -x "" -o /tmp/pd-downloaded http://localhost:3000/sha256sum/$HASH

# 5. 内容对比
diff /tmp/pd-test.txt /tmp/pd-downloaded && echo "✓ 内容一致"

# 6. 删除
curl -s -x "" -X DELETE http://localhost:3000/files/$HASH
# → 200

# 7. 确认已删除
curl -s -x "" -o /dev/null -w "%{http_code}" http://localhost:3000/sha256sum/$HASH
# → 404
```

### 5.3 匿名合集完整流程

```bash
# 1. 准备两个文件
echo "file1" > /tmp/f1.txt
echo "file2" > /tmp/f2.txt
H1=$(curl -s -x "" -F "file=@/tmp/f1.txt" http://localhost:3000/files/upload | jq -r .hash)
H2=$(curl -s -x "" -F "file=@/tmp/f2.txt" http://localhost:3000/files/upload | jq -r .hash)

# 2. 创建合集
COLL=$(curl -s -x "" -H "Content-Type: application/json" \
  -d "{\"entries\":[{\"path\":\"a.txt\",\"hash\":\"$H1\"},{\"path\":\"b.txt\",\"hash\":\"$H2\"}],\"friendly_name\":\"Test Coll\"}" \
  -X POST http://localhost:3000/anon/collections)
COLL_HASH=$(echo "$COLL" | jq -r .hash)

# 3. 获取合集 JSON
curl -s -x "" http://localhost:3000/anon/collections/$COLL_HASH | jq

# 4. 下载合集内文件
curl -s -x "" -o /tmp/result http://localhost:3000/anon/collections/$COLL_HASH/entries/a.txt
diff /tmp/f1.txt /tmp/result && echo "✓ 条目 a.txt 匹配"

# 5. Fork 合集
FORK=$(curl -s -x "" -H "Content-Type: application/json" \
  -d "{\"source_hash\":\"$COLL_HASH\",\"friendly_name\":\"Forked\",\"remove_paths\":[\"b.txt\"]}" \
  -X POST http://localhost:3000/anon/collections/fork)
FORK_HASH=$(echo "$FORK" | jq -r .hash)

# 6. 验证 Fork（只剩 a.txt）
curl -s -x "" http://localhost:3000/anon/collections/$FORK_HASH | jq '.entries | length'
# → 1
```

### 5.4 路径穿越防护验证

```bash
# 应被拒绝（400）
curl -s -x "" -H "Content-Type: application/json" \
  -d '{"entries":[{"path":"../etc/passwd","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}' \
  -X POST http://localhost:3000/anon/collections | jq
# → {"error":"path traversal detected: ../etc/passwd"}

# 应被拒绝（400）
curl -s -x "" -H "Content-Type: application/json" \
  -d '{"path":"/etc/passwd"}' \
  -X POST http://localhost:3000/files/register_local | jq
```

### 5.5 P2P 双节点手动测试

```bash
# 终端 1 — 启动节点 A (relay)
cd go
PORT=3001 PEERDRIVE_P2P_ENABLE=true PEERDRIVE_MDNS_ENABLE=true \
  PEERDRIVE_RELAY_ENABLE=true PEERDRIVE_RELAY_MODE=server \
  PEERDRIVE_STORAGE=/tmp/pd-a go run ./cmd/server/main.go

# 终端 2 — 启动节点 B (client)
PORT=3002 PEERDRIVE_P2P_ENABLE=true PEERDRIVE_MDNS_ENABLE=true \
  PEERDRIVE_RELAY_ENABLE=true PEERDRIVE_RELAY_MODE=client \
  PEERDRIVE_HOLE_PUNCH=true PEERDRIVE_STORAGE=/tmp/pd-b \
  PEERDRIVE_BOOTSTRAP_PEER="/ip4/127.0.0.1/tcp/<A_P2P_PORT>/p2p/<A_PEER_ID>" \
  go run ./cmd/server/main.go

# 终端 3 — 测试
# 获取节点信息
curl -s -x "" http://localhost:3001/p2p/node | jq
A_ID=$(curl -s -x "" http://localhost:3001/p2p/node | jq -r .peer_id)

# 查看发现的节点
curl -s -x "" http://localhost:3001/p2p/discovered | jq

# 节点 B 连接 A
curl -s -x "" -H "Content-Type: application/json" \
  -d "{\"peer_id\":\"$A_ID\",\"addrs\":[\"/ip4/127.0.0.1/tcp/<A_P2P_PORT>\"]}" \
  -X POST http://localhost:3002/p2p/connect | jq

# 验证连接
curl -s -x "" http://localhost:3002/p2p/peers | jq
# → ["<A_PEER_ID>"]

# Ping
curl -s -x "" http://localhost:3002/p2p/ping/$A_ID | jq
# → {"peer":"...","rtt":"1.5ms"}

# 上传文件到 A → 宣告 → B 请求
echo "P2P test" > /tmp/p2p-test.txt
HASH=$(curl -s -x "" -F "file=@/tmp/p2p-test.txt" http://localhost:3001/files/upload | jq -r .hash)
curl -s -x "" -X POST http://localhost:3001/p2p/announce -d "{\"hash\":\"$HASH\"}" | jq
curl -s -x "" -X POST http://localhost:3002/p2p/request-file \
  -d "{\"hash\":\"$HASH\",\"peer_ids\":[\"$A_ID\"]}" | jq
```

### 5.6 BT 功能手动测试

```bash
# 1. 查看 BT DHT 状态
curl -s -x "" http://localhost:3000/p2p/bt/status | jq

# 2. 宣告一个 hash
HASH="eafb6f737b516be4c8899299b4732f3d54ea5d119ce0571ce6f5cd2d55735275"
curl -s -x "" -X POST http://localhost:3000/p2p/bt/announce \
  -H "Content-Type: application/json" \
  -d "{\"hash\":\"$HASH\"}" | jq

# 3. BEP 44 Put
curl -s -x "" -X POST http://localhost:3000/p2p/bt/bep44/put \
  -H "Content-Type: application/json" \
  -d '{"v":"SGVsbG8gV29ybGQ="}' | jq
# → {"target":"...","status":"stored locally"}

# 4. BEP 44 Get (用上面返回的 target)
TARGET=$(curl -s -x "" -X POST http://localhost:3000/p2p/bt/bep44/put \
  -H "Content-Type: application/json" \
  -d '{"v":"SGVsbG8gV29ybGQ="}' | jq -r .target)
curl -s -x "" -X POST http://localhost:3000/p2p/bt/bep44/get \
  -H "Content-Type: application/json" \
  -d "{\"target\":\"$TARGET\"}" | jq
# → {"v":"SGVsbG8gV29ybGQ="}

# 5. BEP 51 Sample
curl -s -x "" http://localhost:3000/p2p/bt/bep51/sample | jq
```

### 5.7 WebRTC 信令手动测试

```bash
# 1. 获取 WebRTC 配置
curl -s -x "" http://localhost:3000/p2p/webrtc/info | jq

# 2. 获取 WebSocket 信息
curl -s -x "" http://localhost:3000/p2p/ws/info | jq

# 3. 使用 wscat 测试信令（需要安装 wscat）
# npm install -g wscat
# wscat -c ws://localhost:3000/ws/signal
```

---

## 6. 故障排查

### 6.1 服务无法启动

**症状**: `go run ./cmd/server/main.go` 报错或立即退出

```bash
# 检查端口占用
fuser 3000/tcp
ss -tlnp | grep 3000

# 杀死占用进程
fuser -k 3000/tcp

# 检查环境变量
echo $PORT
echo $PEERDRIVE_STORAGE

# 检查数据库是否损坏
rm -f go/peerdrive.db  # 警告：会丢失所有注册数据

# 检查 storage 目录权限
ls -la go/storage/
```

### 6.2 P2P 节点无法互联

**症状**: `GET /p2p/peers` 返回 `[]`

```bash
# 1. 确认 P2P 已启用
curl -x "" http://localhost:3000/p2p/status | jq .enabled
# → true

# 2. 确认节点信息正常
curl -x "" http://localhost:3000/p2p/node | jq

# 3. 检查 mDNS 发现
curl -x "" http://localhost:3000/p2p/discovered | jq

# 4. 检查防火墙
# 确保 P2P 端口（通常 40000+）未被防火墙阻止
sudo ufw status

# 5. 手动连接（使用正确的 multiaddr）
# 先从目标节点获取 P2P 地址
curl -x "" http://localhost:<OTHER_PORT>/p2p/node | jq '.addrs'
# 然后用实际地址连接
curl -x "" -X POST http://localhost:3000/p2p/connect \
  -H "Content-Type: application/json" \
  -d '{"addr":"/ip4/127.0.0.1/tcp/<PORT>/p2p/<PEER_ID>"}'
```

### 6.3 BT DHT 无节点

**症状**: `num_nodes: 0`

```bash
# 1. 确认 BT DHT 已启用
curl -x "" http://localhost:3000/p2p/bt/status | jq .enabled

# 2. 检查网络连接
# BT DHT 使用 UDP 6881，确保出站 UDP 未被阻止

# 3. 等待引导（DHT 加入网络需要 1-2 分钟）
sleep 60
curl -x "" http://localhost:3000/p2p/bt/status | jq .num_nodes

# 4. 如果仍然 0，检查系统代理是否拦截了 UDP
# Privoxy 只代理 HTTP，一般不影响 UDP
```

### 6.4 测试脚本报错

**症状**: `curl: (7) Failed to connect`

```bash
# 确认服务在运行
curl -x "" http://localhost:3000/ping

# 确认端口正确（有些测试用 3001/3002/3999）
curl -x "" http://localhost:3999/ping

# 如果使用了代理，添加 -x "" 或 no_proxy='*'
export no_proxy='*'
```

**症状**: `jq: parse error`

```bash
# 检查响应是否为有效 JSON
curl -s -x "" http://localhost:3000/p2p/status

# 如果返回空或 HTML，说明端点不存在或路由未注册
# 检查路由注册
grep -n "GET\|POST" go/internal/router/router.go
```

### 6.5 编译错误

```bash
# 清理 Go 缓存
go clean -cache -modcache

# 重新下载依赖
cd go
go mod tidy
go mod download

# 检查 Go 版本
go version  # 需要 >= 1.24
```

### 6.6 常见 HTTP 状态码

| 状态码 | 含义 | 常见原因 |
|--------|------|----------|
| 200 | 成功 | — |
| 201 | 创建成功 | — |
| 206 | 部分内容 | Range 请求 |
| 400 | 请求错误 | 无效参数、路径穿越 |
| 401 | 未认证 | 缺少/无效 token |
| 404 | 不存在 | hash 未找到 |
| 409 | 冲突 | 合集名重复 |
| 413 | 内容过大 | 文件超限 |
| 500 | 服务器错误 | 查看服务日志 |

### 6.7 日志查看

```bash
# 查看服务日志（如果有重定向）
tail -f /root/p2p.log

# Go 程序的标准输出/错误
# 如果前台运行，直接看终端输出

# 搜索特定关键词
grep -i "error\|panic\|fatal" /root/p2p.log

# 查看最近 100 行
tail -n 100 /root/p2p.log
```

---

## 7. 测试矩阵

完整的测试矩阵请参见 [TEST-MATRIX.md](TEST-MATRIX.md)，覆盖：

| 类别 | 测试ID范围 | 数量 | 说明 |
|------|-----------|------|------|
| 文件系统 | F-01 ~ F-25 | 25 | 上传/注册/下载/删除/验证 |
| 合集系统 | C-01 ~ C-18 | 18 | 匿名合集/用户合集/版本管理 |
| P2P 网络 | P-01 ~ P-12 | 12 | libp2p/DHT/Exchange |
| BitTorrent | B-01 ~ B-16 | 16 | BT DHT/Wire/BEP 标准 |
| Dual-Stack | D-01 ~ D-03 | 3 | 双栈宣告/查找 |
| 认证 | R-01 ~ R-08 | 8 | 注册/登录/JWT/Relay |
| 前端 | UI-01 ~ UI-16 | 16 | 页面加载/交互/错误处理 |
| 部署运维 | O-01 ~ O-07 | 7 | 编译/部署/Docker/内存 |
| 真实网络 | I-01 ~ I-04 | 4 | IPFS/BT 公网连通性 |

---

## 8. 混沌测试

在恶劣网络条件下验证 P2P 协议鲁棒性。

### 8.1 前置条件

```bash
# 加载内核模块
sudo modprobe sch_netem sch_tbf sch_htb cls_u32

# 安装依赖
sudo apt-get install iproute2 iptables
```

### 8.2 快速使用

```bash
cd /mnt/d/WorkPlace/peerdrive/go

# 模拟 30% 丢包 + 500ms 延迟 + 1Mbps 带宽
sudo bash test/chaos-net.sh start --loss 30% --latency 500ms --bandwidth 1Mbps

# 在混沌条件下运行测试
bash test/test-under-chaos.sh --loss 30% --test bt-full-test.sh

# 运行完整混沌矩阵（所有组合）
bash test/test-under-chaos.sh --matrix

# 停止混沌
sudo bash test/chaos-net.sh stop
```

### 8.3 混沌参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--loss X%` | 0% | 丢包率 |
| `--latency Xms` | 0ms | 额外延迟 |
| `--jitter Xms` | 0ms | 延迟抖动 |
| `--bandwidth X` | unlimited | 带宽限制 |

### 8.4 混沌矩阵

矩阵模式自动测试以下组合：
- 丢包: 0%, 10%, 30%, 50%
- 延迟: 0ms, 100ms, 500ms
- 带宽: unlimited, 1Mbps, 100Kbps
- 组合: 30% loss + 500ms latency + 100Kbps

详细文档: [CHAOS_TESTING.md](CHAOS_TESTING.md)

---

## 附录 A: 一键启动测试环境

```bash
#!/bin/bash
# 保存为 start-test-env.sh
cd /mnt/d/WorkPlace/peerdrive/go
export no_proxy='*'

# 清理旧数据库
rm -f peerdrive.db

# 启动服务
PORT=3000 PEERDRIVE_STORAGE=./storage PEERDRIVE_P2P_ENABLE=true \
  go run ./cmd/server/main.go &

sleep 5
echo "服务已启动: http://localhost:3000"
echo "Swagger: http://localhost:3000/swagger/index.html"
echo "健康检查: $(curl -s -x "" http://localhost:3000/ping)"
```

## 附录 B: VPS 测试环境

```bash
# SSH 到 VPS
ssh -p26275 root@bwh.moonchan.xyz

# 检查服务状态
systemctl status peerdrive-relay
curl http://127.0.0.1:3000/ping
curl http://127.0.0.1:3000/p2p/status | python3 -m json.tool
curl http://127.0.0.1:3000/p2p/bt/status | python3 -m json.tool

# 查看日志
journalctl -u peerdrive-relay -n 50 --no-pager

# 重启服务
systemctl restart peerdrive-relay
```

## 附录 C: 测试结果模板

```markdown
## 测试报告 — YYYY-MM-DD

### 环境
- 服务版本: <commit hash>
- 端口: 3000
- P2P: enabled / disabled
- BT DHT: enabled / disabled

### 结果
| 测试套件 | 通过 | 失败 | 总计 |
|----------|------|------|------|
| go test | | | |
| e2e-all.sh | | | |
| bt-full-test.sh | | | |
| p2p.sh | | | |

### 发现的问题
1. ...
2. ...

### 备注
...
```
