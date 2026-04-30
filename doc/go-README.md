# Peerdrive - P2P Content-Addressed File Sharing

基于 libp2p + SQLite + Gin 的内容寻址 P2P 文件分享系统，支持 Git 风格合集版本管理与跨节点同步。

## 本次变更 (Stage 2 E2E — feat/stage2-e2e-test)

### Bug 修复 (4 项)

| Bug | 文件 | 修复 |
|-----|------|------|
| P2P 禁用时 close nil channel panic | `internal/service/p2p.go` | nil 检查后再 close |
| P2P 禁用时打印空 PeerID | `cmd/server/main.go` | `if id != ""` 守卫 |
| storageDir middleware 未生效 | `internal/router/router.go` | middleware 移到路由注册前 |
| RemoveEntry 路径前缀 `/` 导致删除失败 | `internal/controller/collection.go` | `TrimPrefix(path, "/")` |

### 新功能

- **`POST /anon/collections/commit`** — 匿名合集版本化提交，支持条目增/改/删，生成新 hash 版本
- **版本支持** — `GetCollectionByHash` 接受 `version >= 1`（原为严格 `== 1`）

### 测试

- **66 个 Go 单元测试** 覆盖 5 个包（config/model/repository/service/controller），全部通过
- **E2E 测试** (`test/e2e-all.sh`)：85 条断言，12 个测试段，自包含（build + 启动 + 清理）
- React 前端构建通过（`npm run build`），31 模块零错误

### 前端 (React)

- **App 架构重写**：从 Tab-based 改为 BrowserRouter，支持 Plaza → Explorer → AnonExplorer 导航
- **修复所有损坏页面**：`res.data` → 直接返回、`err.response` → `err.message`、缺少的 API 函数
- **新增 Commit UI**：AnonExplorer 页面支持提交新版本
- **AppContext**：全局用户名状态 + localStorage 持久化

---

## 完整功能

### 文件存储

| 端点 | 方法 | 说明 |
|------|------|------|
| `/files/upload` | POST | Multipart 上传，SHA256 内容寻址，自动去重 |
| `/files/register_local` | POST | 本地文件零拷贝注册（path→hash 映射） |
| `/files/register_folder` | POST | 递归注册整个文件夹 |
| `/files/verify/:hash` | GET | 查询文件元数据（size, mime, gziped） |
| `/files/:hash` | DELETE | 删除文件记录 |
| `/sha256sum/:hash` | GET | 通过 SHA256 下载文件（本地 → P2P 回退） |
| `/files/diff` | POST | 文件版本差异 |

### 匿名合集 (Anon Collections)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/anon/collections` | POST | 创建不可变合集（content-addressed JSON） |
| `/anon/collections/:hash` | GET | 根据 hash 获取合集 JSON |
| `/anon/collections/:hash/entries/*filepath` | GET | 下载合集内指定文件 |
| `/anon/collections/fork` | POST | Fork 合集（增/删条目，生成新 hash） |
| `/anon/collections/commit` | POST | **NEW** 版本化提交（增/改/删条目，version++） |

### 用户合集 (Named Collections)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/collections` | POST | 创建用户合集 |
| `/collections/:username` | GET | 列出用户所有合集 |
| `/collections/:username/:coll` | GET | 获取合集详情 + 条目 |
| `/collections/:username/:coll/entries` | POST | 添加条目 |
| `/collections/:username/:coll/entries/*path` | DELETE | 移除条目 |
| `/collections/:username/:coll/commit` | POST | 提交工作区变更，生成快照 |
| `/collections/:username/:coll/log` | GET | 查看版本历史 |
| `/collections/:username/:coll/rollback/:vid` | POST | 回滚到指定版本 |
| `/collections/search` | GET | 搜索合集 |

### 协作操作

| 端点 | 方法 | 说明 |
|------|------|------|
| `/actions/fork` | POST | Fork 远程合集到本地 |
| `/actions/merge` | POST | 三路合并（ours/theirs/manual） |
| `/actions/pull` | POST | 拉取上游更新 |

### P2P 网络 (3 Stage)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/p2p/status` | GET | P2P 运行状态（enabled, relay_mode, hole_punch, nat） |
| `/p2p/node` | GET | 本节点 PeerID + 多地址 |
| `/p2p/peers` | GET | 已连接对等节点 |
| `/p2p/discovered` | GET | 已发现节点（mDNS/DHT） |
| `/p2p/ping/:peer_id` | GET | Ping 对等节点 |
| `/p2p/connect` | POST | 手动连接到指定地址 |
| `/p2p/announce` | POST | 宣告合集 hash 到网络 |
| `/p2p/fetch` | POST | 从 P2P 获取匿名合集 |
| `/p2p/sync` | POST | 从对等节点同步文件 |
| `/p2p/push` | POST | 推送合集到对等节点 |
| `/p2p/request-file` | POST | 广播文件请求到全网 |
| `/p2p/ws/info` | GET | WebSocket 传输连接信息 |
| `/ws/transfer` | GET | WebSocket 文件传输通道 |

P2P 协议：
- `/peerdrive/exchange/1.0.0` — DHT 发现 + 自定义文件交换
- `/peerdrive/announce/1.0.0` — 合集 hash 宣告
- `/peerdrive/request/1.0.0` — 文件请求广播

NAT 穿透：Relay 服务器模式 / Hole Punching / AutoNAT / mDNS 局域网发现

### 本地同步 + 任务 + 其他

| 端点 | 方法 | 说明 |
|------|------|------|
| `/local/save` | POST | 将合集保存到本地路径 |
| `/local/status/:hash` | GET | 查询本地同步状态 |
| `/tasks` | GET | 列出异步任务 |
| `/tasks/:id` | GET | 查询任务进度 |
| `/ping` | GET | 健康检查 |
| `/swagger/*any` | GET | Swagger API 文档 |

---

## 技术架构

```
back/cmd/server/main.go
  ├── internal/repository/db.go          → SQLite (peerdrive.db)
  ├── internal/service/p2p.go            → libp2p (DHT + Relay + WS)
  ├── internal/service/downloader.go     → 本地 → P2P 回退
  ├── internal/service/anon_service.go   → 匿名合集 CRUD + Commit + Fork
  ├── internal/service/file_service.go   → 文件注册/上传/删除
  ├── internal/service/sync_service.go   → 本地同步 + 过滤器
  ├── internal/provider/manager.go       → 存储后端抽象
  └── internal/router/router.go          → Gin HTTP 路由
```

数据库：SQLite（`peerdrive.db`），表结构 `file_meta` + `file_providers` + `collections` + `collection_entries` + `collection_versions`

存储：`PEERDRIVE_STORAGE`（默认 `./storage`），文件按 `<hash[:2]>/<hash>` 布局

---

## 运行

```bash
# 后端
cd back
go build -o peerdrive-server ./cmd/server/
PORT=3000 PEERDRIVE_STORAGE=./storage PEERDRIVE_P2P_ENABLE=false ./peerdrive-server

# 前端
cd front
npm run dev      # Vite dev server → http://localhost:5173

# E2E 测试
bash back/test/e2e-all.sh

# 单元测试
cd back && go test ./... -count=1
cd front && npm test
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `3000` | HTTP 端口 |
| `PEERDRIVE_STORAGE` | `./storage` | 文件存储目录 |
| `PEERDRIVE_STORAGE_ENABLE` | `true` | 是否启用本地存储 |
| `PEERDRIVE_P2P_ENABLE` | `true` | 是否启动 P2P 节点 |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | P2P 监听地址 |
| `PEERDRIVE_MDNS_ENABLE` | `true` | 局域网 mDNS 发现 |
| `PEERDRIVE_RELAY_ENABLE` | `false` | 是否启用 Relay |
| `PEERDRIVE_RELAY_MODE` | `client` | relay 模式 (server/client) |
| `PEERDRIVE_HOLE_PUNCH` | `true` | NAT 穿透打洞 |
| `PEERDRIVE_AUTO_NAT` | `true` | 自动 NAT 检测 |
