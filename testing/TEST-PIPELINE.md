# Peerdrive 测试管线文档

> 每个测试脚本的流程、预期行为、错误分析、是否需要修改代码
> 更新: 2026-04-29

---

## 测试脚本总览

| 脚本 | 类型 | 断言数 | 端口 | 自包含 |
|------|------|--------|------|--------|
| `go test ./...` | 单元测试 | 66 | — | ✅ |
| `test/e2e-all.sh` | E2E 集成 | 85 | 3999 | ✅ |
| `test/bt-full-test.sh` | BT 专项 | 38 | 3000 | ❌ |
| `test/p2p-full-test.sh` | P2P 专项 | 35 | 3000 | ❌ |
| `test/p2p.sh` | P2P 双节点 | 13 | 3001/3002 | ✅ |
| `test/relay.sh` | Relay 穿透 | 12 | 3001/3002 | ✅ |
| `test/webrtc_signal_test.sh` | WebRTC 信令 | 23 | 3000 | ❌ |
| `test/ipfs-full-test.sh` | IPFS 专项 | — | 3000 | ❌ |
| `test/storage-full-test.sh` | Storage 专项 | 28 | 3000 | ❌ |
| `test/auth-full-test.sh` | Auth 专项 | 20 | 4000 | ❌ |
| `test/upload.sh` | 上传测试 | 3 | 3000 | ❌ |
| `test/register.sh` | 注册测试 | 3 | 3000 | ❌ |
| `test/anon-collection.sh` | 匿名合集 | 6 | 3000 | ❌ |
| `test/all.sh` | 一键全模块 | — | 3000 | ❌ |

---

## 1. go test ./... — Go 单元测试

### 运行
```bash
cd go && go test ./... -count=1
```

### 测试段
| 包 | 测试数 | 覆盖 |
|----|--------|------|
| config | 10 | Load()、getEnv、getEnvBool、parseRelayMode |
| model | 6 | Collection/AnonCollection 结构体 |
| repository | 5 | FileMeta CRUD、Provider 管理 |
| service | 29 | 文件注册/上传/校验、匿名合集、路径穿越 |
| controller | 17 | Ping、Collection CRUD、Commit/Rollback |

### 预期输出
```
ok  peerdrive/internal/config     0.021s
ok  peerdrive/internal/controller  0.087s
ok  peerdrive/internal/model       0.007s
ok  peerdrive/internal/repository  0.020s
ok  peerdrive/internal/service     0.314s
```

### 常见错误

| 错误 | 可能原因 | 是否需改代码 |
|------|----------|-------------|
| `cannot find package` | go.mod 依赖未下载 | 否，运行 `go mod tidy` |
| `undefined: xxx` | 代码引用了已删除的函数 | 是，检查 import 和函数名 |
| `FAIL: TestXxx` | 测试逻辑与当前实现不匹配 | 是，检查测试用例是否符合最新 API |
| `database is locked` | SQLite 并发访问冲突 | 否，单独运行该测试 |
| `no test files` | 测试文件在非测试目录 | 否，确认 `_test.go` 文件存在 |

### 排错步骤
1. 检查 `go.mod` 是否完整：`go mod tidy`
2. 查看具体失败：`go test -v ./internal/... 2>&1 | grep FAIL`
3. 单独运行失败包：`go test -v -run TestXxx ./internal/xxx/`
4. 检查代码是否被 linter 修改（查看 git diff）

---

## 2. test/e2e-all.sh — E2E 全端点测试

### 运行
```bash
cd go && bash test/e2e-all.sh
```

### 测试流程

```
Phase 1: 编译 peerdrive-server → 启动于 :3999 → 等待 ready
Phase 2-12: 按序执行 85 条 curl 断言
Phase 13: kill 进程，清理临时文件
```

### 12 个测试段详情

#### 段 1 — Health (2 断言)
- `GET /ping` → 200 "pong"
- **如果失败**: 进程未启动或端口冲突
- **排错**: `fuser 3999/tcp` 检查占用，查看编译错误

#### 段 2 — File Upload (10 断言)
- 上传新文件 → 201 + hash + on-disk 验证
- 重复上传 → 200 + already_exists + 相同 hash
- 第二个文件 → 不同的 hash
- **如果失败**: 
  - 201 失败 → storage 目录权限问题
  - hash 不匹配 → `hashutil.SHA256` 或 `io.Copy` 问题（需改代码）
  - on-disk 验证失败 → `c.SaveUploadedFile` 路径问题（需改代码）

#### 段 3 — File Verify (3 断言)
- 合法 hash → 200 + 元数据
- 无效 hash → 400
- **如果失败**: `/files/verify/:hash` 路由或 `GetFileMeta` 查询问题（需改代码）

#### 段 4 — SHA256 Download (3 断言)
- 合法 hash → 200 + 内容一致
- 无效 hash → 404
- **如果失败**: provider 未找到文件路径或文件被删除

#### 段 5 — Register Local (5 断言)
- 注册本地文件 → hash + verify + download
- 重复注册 → 相同 hash
- **如果失败**: 路径权限问题或 `RegisterLocal` 逻辑（需改代码）

#### 段 6 — Register Folder (2 断言)
- 注册文件夹 → 返回文件列表
- **如果失败**: 目录遍历逻辑或权限问题

#### 段 7 — Anonymous Collections (22 断言)
- 创建合集 → 路径穿越拒绝 → friendly_name
- GET 合集 JSON → sha256sum 下载 → entries 下载
- Fork → commit
- **如果失败**: 
  - 路径穿越未拒绝 → 安全漏洞（需改代码）
  - commit 无 version → collection_versions 表问题（需改代码）

#### 段 8 — Named Collections (22 断言)
- 创建/重复拒/列出/获取/加条目/删条目/下载
- Commit → version → rollback
- **如果失败**: DB 表或 repository 逻辑问题（需改代码）

#### 段 9 — Fork/Merge/Pull (4 断言)
- Fork 创建新合集 → Merge 合并 → Pull 响应
- **如果失败**: `actions/` 路由或 fork/merge 逻辑（需改代码）

#### 段 10 — File Delete (2 断言)
- 删除成功 → verify 返回 404
- **如果失败**: 删除逻辑或 DB 事务问题（需改代码）

#### 段 11 — Tasks (2 断言)
- 任务列表有效 → 不存在任务 404
- **如果失败**: transfer_tasks 表或 task handler 问题（需改代码）

#### 段 12 — Edge Cases (5 断言)
- 不存在用户、空合集、无效 hash、非法 body
- **如果失败**: 边界条件处理不当（需改代码）

### 全局排错
- 脚本需要 `python3` 用于 JSON 解析
- 端口 3999 必须空闲
- 系统代理可能干扰 curl，脚本自动设置 `no_proxy='*'`

---

## 3. test/bt-full-test.sh — BitTorrent 全功能测试

### 运行
```bash
# 先启动服务
PEERDRIVE_BT_DHT_ENABLE=true go run ./cmd/server/main.go &
# 再测试
bash test/bt-full-test.sh
```

### 测试段

| # | 测试 | 预期 |
|---|------|------|
| 1 | BT DHT 状态 | enabled:true, num_nodes > 0 |
| 2 | BT Announce | status:"announced on BT DHT" |
| 3 | BT Find (self) | 找到自己的宣告 |
| 4 | BT Find (cross) | A 宣告 → B find → count >= 1 |
| 5 | BEP 44 Put | 返回 target hash |
| 6 | BEP 44 Get | 数据往返一致 (base64) |
| 7 | BEP 51 Sample | 返回 sample 数组 |
| 8 | Torrent Parse | name/pieces/size/infohash |
| 9 | Magnet Parse | infohash/name/trackers |
| 10 | Wire Handshake | 协议握手成功 |
| 11 | Piece Download | SHA1 验证通过 |
| 12 | Full Download | SHA256 最终匹配 |

### 常见错误

| 错误 | 可能原因 | 是否需改代码 |
|------|----------|-------------|
| `num_nodes: 0` | UDP 6881 被防火墙阻止 / DHT 引导需要 1-2 分钟 | 否，等 2 分钟后重试 |
| `BEP44 put: 500` | DHT 远程节点不支持 BEP44 | 否，已修复为本地回退 |
| `BEP44 get: not found` | 数据未存入 DHT（孤立节点） | 否，检查 BEP44 localBEP44Store |
| `Wire handshake timeout` | Seeder 未启动或端口错误 | 否，检查 Python seeder 进程 |
| `SHA1 mismatch` | Piece 下载损坏 | 是，检查 piece.go 验证逻辑 |

### 排错步骤
1. 确认服务运行: `curl localhost:3000/bt/status`
2. 确认 DHT 有节点: 等 60 秒后重查 `num_nodes`
3. BEP44 问题: 检查 `internal/p2p_bt/bep44.go` 的 `localBEP44Store`
4. Wire 问题: 启动 Python seeder: `python3 test/bt-integration/bt-listener.py`

---

## 4. test/p2p.sh — P2P 双节点测试

### 运行
```bash
cd go && bash test/p2p.sh
```

### 测试流程
1. 编译两个节点
2. 启动 Node A (:3001) 和 Node B (:3002)
3. 等待 mDNS 发现 (10s)
4. Node A 注册文件 + 创建合集
5. Node B 连接 A + P2P fetch + sync
6. 清理进程

### 常见错误

| 错误 | 可能原因 | 是否需改代码 |
|------|----------|-------------|
| 编译失败 | libp2p 依赖缺失 | 否，`go mod tidy` |
| mDNS 未发现 (WARN) | 防火墙/网络隔离 | 否，软断言不 FAIL |
| connect 失败 | 端口错误或 peer_id 不匹配 | 否，检查 multiaddr |
| fetch 失败 | 合集未宣告或 DHT 无记录 | 否，检查 announce 状态 |
| sync 返回 0 | 文件不在目标节点 | 否，确认文件已在 A 上注册 |
| 端口冲突 | 3001/3002 被占用 | 否，`fuser -k 3001/tcp` |

---

## 5. test/relay.sh — Relay 穿透测试

### 运行
```bash
cd go && bash test/relay.sh
```

### 测试流程
1. Relay 节点 (:3001, relay_mode=server)
2. Client 节点 (:3002, hole_punch=true)
3. Client 连接 Relay → P2P fetch → WS info
4. 清理

### 环境变量
```bash
PEERDRIVE_RELAY_ENABLE=true
PEERDRIVE_RELAY_MODE=server  # or client
PEERDRIVE_HOLE_PUNCH=true
PEERDRIVE_AUTO_NAT=true
```

### 常见错误

| 错误 | 可能原因 | 是否需改代码 |
|------|----------|-------------|
| relay_mode 不是 server | 环境变量未正确设置 | 否 |
| Client 无法连接 Relay | 网络不可达或 peer_id 错误 | 否，检查地址 |
| Hole punch 失败 | NAT 类型为对称型 | 否，对称 NAT 无法打洞 |
| WS info 为空 | WebSocket 服务未初始化 | 是，检查 p2p_ws.go |

---

## 6. test/webrtc_signal_test.sh — WebRTC 信令测试

### 运行
```bash
# 服务在 :3000 运行
bash test/webrtc_signal_test.sh
```

### 测试段 (23 项)
- 注册 (echo 消息往返)
- 房间加入/离开
- SDP Offer/Answer 交换
- ICE 候选转发
- 文件宣告/发现
- 直接消息 (unicast)
- 3 人房间
- 房间隔离

### 常见错误

| 错误 | 可能原因 | 是否需改代码 |
|------|----------|-------------|
| 注册失败 | SignalingHub 未初始化 | 是，检查 router.go 中 InitSignalHub |
| 房间消息未到达 | broadcast 逻辑 bug | 是，检查 signaling.go exclude 参数 |
| SDP 交换失败 | WebSocket 消息格式错误 | 否，检查 JSON 格式 |
| panic: slice bounds | hash/room 名称过短 | 是，已修复 — 检查 length >= 16 |

---

## 7. test/storage-full-test.sh — Storage 全功能

### 运行
```bash
# 服务在 :3000 运行
bash test/storage-full-test.sh
```

### 测试覆盖
- SHA256 下载 / CID 双索引
- 文件上传 / 注册 / 删除 / 校验
- Range (HTTP 206) 下载
- URL 文件注册 / WebDAV / 文件复制

### 已知问题 (4 failures)
- Collection get 返回格式不一致
- Share token 创建后无法访问
- CID 查询部分场景失败
- 这些是已知 bug，不改测试脚本，需要改代码

---

## 8. 测试失败分类处理指南

### 不需要改代码（环境/配置问题）
- 端口占用 → `fuser -k PORT/tcp`
- 代理干扰 → `export no_proxy='*'`
- 编译错误 → `go mod tidy`
- 权限问题 → `chmod` / `sudo`
- 数据库锁 → 单独重跑

### 需要改测试脚本
- API 路径变更
- 返回格式变更
- 新增必需参数
- 超时时间不足

### 需要改代码
- HTTP 500 错误
- 返回数据不正确
- 安全漏洞（路径穿越、注入）
- Panic / nil pointer
- 断言失败但 API 返回看起来正确

---

## 9. 添加新测试的规范

1. Shell 脚本放在 `go/test/<name>.sh`
2. 脚本顶部添加注释说明测试目的
3. 使用 `curl -x ""` 绕过系统代理
4. 使用 `jq` 或 `python3 -c` 解析 JSON
5. 使用 `|| echo "FAIL: ..."` 标记失败
6. 清理临时文件和进程
7. 避免依赖特定文件路径（使用 `/tmp/`）
8. 测试结果保存到 `go/test/<name>-results.txt`

### 模板
```bash
#!/bin/bash
# Test: <description>
# Requires: server on PORT (default 3000)
set -e
PORT=${1:-3000}
BASE="http://localhost:$PORT"
no_proxy="*"
PASS=0; FAIL=0
check() {
  if [ "$1" = "$2" ]; then ((PASS++)); echo "PASS: $3"
  else ((FAIL++)); echo "FAIL: $3 (expected '$2', got '$1')"; fi
}
# ... tests ...
echo "PASS=$PASS FAIL=$FAIL TOTAL=$((PASS+FAIL))"
```
