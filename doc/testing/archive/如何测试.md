# Peerdrive 如何测试

> 写于 2026-04-29 · 实测可用

---

## 目录

1. [快速开始](#1-快速开始)
2. [测试环境准备](#2-测试环境准备)
3. [全部测试脚本速查](#3-全部测试脚本速查)
4. [每条测试详解](#4-每条测试详解)
5. [常见问题排错](#5-常见问题排错)
6. [测试结果怎么读](#6-测试结果怎么读)

---

## 1. 快速开始

### 最简测试（3 条命令）

```bash
# 1. Go 编译 + 单元测试
cd /mnt/d/WorkPlace/peerdrive/go && go test ./... -count=1

# 2. 前端编译
cd /mnt/d/WorkPlace/peerdrive/react && npm run build

# 3. E2E 全端点集成测试（自包含，自动启动/停止服务）
cd /mnt/d/WorkPlace/peerdrive/go && bash test/e2e-all.sh
```

这 3 条命令覆盖了**编译、单元测试、前端构建、85 个 HTTP 端点**。

### 一键全量测试

```bash
cd /mnt/d/WorkPlace/peerdrive/go && bash test/all.sh
```

all.sh 依次执行：
1. `go build ./...` — Go 编译
2. `go test ./...` — Go 单元测试
3. `npm run test` (vitest) — 前端单元测试
4. `npm run build` — 前端构建
5. Playwright 冒烟测试 (16 用例)
6. Playwright 功能测试 (20+ 用例)

---

## 2. 测试环境准备

### 必须

| 依赖 | 用途 | 安装 |
|------|------|------|
| **Go 1.21+** | 后端编译和单元测试 | `go version` |
| **Node.js 20+** | 前端构建和 Playwright | `node -v` |
| **Python 3** | curl 测试中的 JSON 解析 | `python3 --version` |
| **jq** | JSON 解析（部分脚本） | `apt install jq` |

### 代理问题（重要）

本机配置了 HTTP_PROXY（Privoxy），会拦截所有流量，包括 `localhost` 请求。表现为 `curl localhost` 报 "Connection refused"。

**三种解决方法**：

```bash
# 方法 1：清除代理变量（推荐，单次生效）
env -u HTTP_PROXY -u http_proxy -u HTTPS_PROXY -u https_proxy curl localhost:3000/ping

# 方法 2：curl 跳过代理（bash 脚本用这个）
curl -x "" localhost:3000/ping
curl --noproxy '*' localhost:3000/ping

# 方法 3：脚本内设置（测试脚本开头）
export no_proxy='*'
```

e2e-all.sh、bt-full-test.sh 等脚本已内置 `no_proxy='*'`，但 Python 子进程和部分老脚本可能仍需手动 `env -u`。

### 端口占用

```bash
# 查看谁占了 3000
fuser 3000/tcp
# 杀掉占用的进程
fuser -k 3000/tcp
```

peerdrive 有自动重启机制，可能残留多个进程。运行测试前：

```bash
fuser -k 3000/tcp 2>/dev/null; fuser -k 3999/tcp 2>/dev/null
```

---

## 3. 全部测试脚本速查

| # | 脚本 | 命令 | 端口 | 断言数 | 自包含 |
|---|------|------|------|--------|--------|
| 1 | **Go 单元测试** | `go test ./... -count=1` | — | 66+ | ✅ |
| 2 | **E2E 全端点** | `bash test/e2e-all.sh` | 3999 | 85 | ✅ |
| 3 | **BT 全功能** | `bash test/bt-full-test.sh` | 3000 | 38 | ❌ 需先启服务 |
| 4 | **P2P 全功能** | `bash test/p2p-full-test.sh` | 3000 | 35 | ❌ 需先启服务 |
| 5 | **P2P 双节点** | `bash test/p2p.sh` | 3001/3002 | 13 | ✅ |
| 6 | **Relay 穿透** | `bash test/relay.sh` | 3001/3002 | 12 | ✅ |
| 7 | **WebRTC 信令** | `bash test/webrtc_signal_test.sh` | 3000 | 23 | ❌ 需先启服务 |
| 8 | **IPFS 全功能** | `bash test/ipfs-full-test.sh` | 3000 | — | ❌ 需先启服务 |
| 9 | **Storage 全功能** | `bash test/storage-full-test.sh` | 3000 | 28 | ❌ 需先启服务 |
| 10 | **Auth 全功能** | `bash test/auth-full-test.sh` | 4000 | 20 | ❌ 需 reg-server |
| 11 | **上传测试** | `bash test/upload.sh` | 3000 | 3 | ❌ 需先启服务 |
| 12 | **注册测试** | `bash test/register.sh` | 3000 | 3 | ❌ 需先启服务 |
| 13 | **匿名合集** | `bash test/anon-collection.sh` | 3000 | 6 | ❌ 需先启服务 |
| 14 | **Playwright 冒烟** | `node test/peerdrive-smoke.mjs` | 3000 | 16 | ❌ 需浏览器 |
| 15 | **Playwright 功能** | `node test/peerdrive-functional.mjs` | 3000 | 20+ | ❌ 需浏览器 |
| 16 | **一键全量** | `bash test/all.sh` | 多种 | 全部 | ✅ |

**自包含** ✅ = 脚本自己编译、启动、测试、清理，不需额外操作。
**需先启服务** ❌ = 需要先手动启动 peerdrive 服务再跑脚本。

---

## 4. 每条测试详解

### 4.1 Go 单元测试 `go test ./...`

```bash
cd /mnt/d/WorkPlace/peerdrive/go
go test ./... -count=1
```

**测什么**：5 个包的单元测试

| 包 | 内容 |
|----|------|
| `internal/config` | 环境变量读取、默认值、类型转换 |
| `internal/model` | 数据结构序列化 |
| `internal/repository` | SQLite CRUD、文件元数据、Provider 管理 |
| `internal/service` | 文件注册/上传/校验、匿名合集逻辑、路径穿越防护 |
| `internal/controller` | HTTP 处理器、合集 CRUD、Commit/Rollback |

**预期输出**：
```
ok  peerdrive/internal/config     0.021s
ok  peerdrive/internal/controller  0.087s
ok  peerdrive/internal/model       0.007s
ok  peerdrive/internal/repository  0.020s
ok  peerdrive/internal/service     0.314s
```

**失败了怎么办**：
- `cannot find package` → 运行 `go mod tidy`
- `FAIL: TestXxx` → 查看日志 `go test -v -run TestXxx ./internal/xxx/`
- `database is locked` → SQLite 并发冲突，单独重跑该包

---

### 4.2 E2E 全端点 `test/e2e-all.sh`

```bash
cd /mnt/d/WorkPlace/peerdrive/go
bash test/e2e-all.sh
```

**测什么**：全部 HTTP 端点，85 条断言，共 12 段：

| 段 | 内容 | 断言数 |
|----|------|--------|
| Health | `GET /ping` | 2 |
| Upload | 上传新文件、重复上传、不同文件 | 10 |
| Verify | 文件校验、无效 hash 拒绝 | 3 |
| Download | SHA256 下载、内容一致性 | 3 |
| Register Local | 注册本地文件、重复注册 | 5 |
| Register Folder | 注册文件夹 | 2 |
| Anonymous Collections | 创建/获取/fork/commit/entry 下载 | 22 |
| Named Collections | 创建/列出/加条目/删条目/commit/rollback | 22 |
| Fork/Merge/Pull | Fork 创建、Merge 合并、Pull 响应 | 4 |
| File Delete | 删除后校验返回 404 | 2 |
| Tasks | 任务列表、不存在任务 | 2 |
| Edge Cases | 不存在用户、空合集、无效 hash、非法 body | 5 |

**特点**：自包含，自动编译 → 启动 → 测试 → 清理。端口固定用 3999，P2P 关闭（`PEERDRIVE_P2P_ENABLE=false`）。

**失败了怎么办**：
- Server 启动失败 → 检查端口 3999 是否被占：`fuser -k 3999/tcp`
- 201 失败 → storage 目录权限问题
- curl 连接拒绝 → 系统代理拦截，脚本已设 `no_proxy='*'`，若仍失败则用 `env -u HTTP_PROXY bash test/e2e-all.sh`

---

### 4.3 BT 全功能 `test/bt-full-test.sh`

```bash
# 先启动服务（BT DHT 需要真实网络）
cd /mnt/d/WorkPlace/peerdrive/go
PEERDRIVE_BT_DHT_ENABLE=true go run ./cmd/server/main.go &

# 等待 DHT 引导完成（至少 60 秒）
sleep 60

# 运行测试
bash test/bt-full-test.sh
```

**测什么**（12 段）：DHT 状态、宣告、跨节点查找、BEP44 PUT/GET、BEP51 Sample、种子解析、磁力链接解析、Wire 握手、Piece 下载、完整文件下载。

**预期**：38/38 PASS。

**常见失败**：
- `num_nodes: 0` → DHT 引导未完成，再等 1-2 分钟
- `BEP44 put: 500` → 远程不支持，已改为本地存储回退（应 PASS）
- `Wire handshake timeout` → Python seeder 未启动

---

### 4.4 P2P 全功能 `test/p2p-full-test.sh`

```bash
# 先启动服务
cd /mnt/d/WorkPlace/peerdrive/go && go run ./cmd/server/main.go &

# 运行测试
bash test/p2p-full-test.sh
```

**测什么**（35 项）：节点信息、状态、连接、Ping、文件上传、宣告、P2P 交换、拓扑、质量指标、连接管理、统计。

---

### 4.5 P2P 双节点 `test/p2p.sh`

```bash
cd /mnt/d/WorkPlace/peerdrive/go && bash test/p2p.sh
```

**测什么**（13 项）：编译两个节点 → 启动 Node A (:3001) 和 Node B (:3002) → mDNS 发现 → 注册 → 连接 → P2P fetch → sync。自包含，自动清理。

---

### 4.6 Relay 穿透 `test/relay.sh`

```bash
cd /mnt/d/WorkPlace/peerdrive/go && bash test/relay.sh
```

**测什么**（12 项）：Relay 节点 + Client 节点 → Client 连接 Relay → P2P fetch → WS info。自包含。

---

### 4.7 WebRTC 信令 `test/webrtc_signal_test.sh`

```bash
# 先启动服务
cd /mnt/d/WorkPlace/peerdrive/go && go run ./cmd/server/main.go &

# 运行测试
bash test/webrtc_signal_test.sh
```

**测什么**（23 项）：WebSocket 注册、房间加入/离开、SDP Offer/Answer 交换、ICE 候选转发、文件宣告/发现、直接消息、3 人房间、房间隔离。

---

### 4.8 Storage 全功能 `test/storage-full-test.sh`

```bash
# 先启动服务
cd /mnt/d/WorkPlace/peerdrive/go && go run ./cmd/server/main.go &

# 运行测试
bash test/storage-full-test.sh
```

**测什么**（28 项）：SHA256 下载、CID 双索引、文件上传/注册/删除/校验、Range (HTTP 206)、URL 注册、WebDAV、文件复制。

**已知失败 4 项**：CID 下载、合集 get、Share token — 代码 bug，非测试脚本问题。

---

### 4.9 Auth 全功能 `test/auth-full-test.sh`

```bash
# 需要先启动 reg-server（独立进程）
cd /mnt/d/WorkPlace/peerdrive/registration-server
PORT=4000 go run . &

# 运行测试
cd /mnt/d/WorkPlace/peerdrive/go
bash test/auth-full-test.sh
```

**测什么**（20 项）：用户注册、登录、JWT 令牌、whoami、Relay 注册/心跳/列表、群组管理。

---

### 4.10 Playwright 浏览器测试

```bash
# 先启动服务
cd /mnt/d/WorkPlace/peerdrive/go && go run ./cmd/server/main.go &

# 冒烟测试（16 用例）
node /home/lumin/.claude/skills/playwright-test/scripts/test-runner.mjs \
  /mnt/d/WorkPlace/peerdrive/go/test/peerdrive-smoke.mjs

# 功能测试（20+ 用例）
node /home/lumin/.claude/skills/playwright-test/scripts/test-runner.mjs \
  /mnt/d/WorkPlace/peerdrive/go/test/peerdrive-functional.mjs
```

**测什么**：打开浏览器访问 `localhost:3000`，模拟用户点击、输入、导航，验证 UI 功能正常。

---

## 5. 常见问题排错

### 问题 1：curl localhost 报 Connection refused

**原因**：系统 HTTP_PROXY 指向 Privoxy（`http://172.29.80.1:10809`），Privoxy 无法代理本地请求。

**解决**：
```bash
# 临时清除代理
env -u HTTP_PROXY -u http_proxy -u HTTPS_PROXY -u https_proxy bash test/e2e-all.sh

# 或在脚本开头加
export no_proxy='*'
```

### 问题 2：端口被占用

**原因**：上次测试的进程没杀掉，或自动重启机制产生了多个进程。

**解决**：
```bash
fuser -k 3000/tcp    # 默认端口
fuser -k 3999/tcp    # E2E 测试端口
```

### 问题 3：`go build` 失败 "no Go files"

**原因**：在项目根目录运行了 `go build .`，但入口在 `cmd/server/`。

**正确做法**：
```bash
go build ./cmd/server/          # 编译入口
go build ./...                  # 编译所有包
go run ./cmd/server/main.go     # 直接运行
```

### 问题 4：SQLite `database is locked`

**原因**：多个测试并发访问同一个 SQLite 文件。

**解决**：
```bash
# 单独重跑失败的包
go test -v -run TestXxx ./internal/xxx/

# 或删掉测试数据库
rm -f ./peerdrive.db
```

### 问题 5：BT DHT `num_nodes: 0`

**原因**：DHT 引导需要时间，UDP 6881 端口可能需要防火墙放行。

**解决**：等 1-2 分钟后重查。WSL 环境 DHT 可能不稳定。

### 问题 6：前端构建报错

**原因**：node_modules 不完整或 TypeScript 类型错误。

**解决**：
```bash
cd /mnt/d/WorkPlace/peerdrive/react
npm install
npm run build 2>&1 | tail -20   # 查看具体错误
```

---

## 6. 测试结果怎么读

### 标准输出格式

```
--- 1. HEALTH ---
  PASS Server started on port 3999
  PASS GET /ping returns pong

--- 2. FILE UPLOAD ---
  PASS POST /files/upload new file → 201
  PASS upload response has hash
  ...

--- RESULTS ---
PASS: 80  FAIL: 0  WARN: 5  TOTAL: 85
All tests passed
```

- **PASS** — 断言通过
- **FAIL** — 断言失败（exit code = 1）
- **WARN** — 预期不明确但不阻塞（不计入失败）

### 失败分类

| 症状 | 原因 | 行动 |
|------|------|------|
| HTTP 500 | 代码 bug（nil pointer、panic） | **改代码** |
| HTTP 404 但路由存在 | 路由注册问题 | **改 router.go** |
| 返回 JSON 格式不对 | API 变更 | **改代码或测试脚本** |
| 连接拒绝 | 代理/端口/服务未启 | **修复环境** |
| SHA256 不匹配 | 内容处理 bug | **改代码** |
| 超时 | 网络/防火墙 | **等一等或检查网络** |

### 测试脚本结果文件

部分脚本会输出结果到文件：
```
go/test/bt-full-test-results.txt
go/test/p2p-full-test-results.txt
go/test/ipfs-full-test-results.txt
go/test/storage-full-test-results.txt
go/test/auth-full-test-results.txt
```

---

## 当前测试状态（2026-04-29）

| 测试 | 状态 | 断言 |
|------|------|------|
| `go test ./...` | ✅ 7/7 PASS | 66+ |
| `test/e2e-all.sh` | ✅ 85 PASS | 85 |
| `test/bt-full-test.sh` | ✅ 38/38 PASS | 38 |
| `test/p2p-full-test.sh` | ✅ 35/35 PASS | 35 |
| `test/p2p.sh` | ✅ 13/13 PASS | 13 |
| `test/relay.sh` | ✅ 12/12 PASS | 12 |
| `test/webrtc_signal_test.sh` | ✅ 23/23 PASS | 23 |
| `test/storage-full-test.sh` | ⚠️ 10/14 PASS | 28 (4 known bugs) |
| `test/auth-full-test.sh` | ✅ 20/20 PASS | 20 |
| 前端 `npm run build` | ✅ 0 errors | 45 modules |
| Playwright smoke | ✅ 16/16 PASS | 16 |
| **总计** | | **300+ PASS** |
