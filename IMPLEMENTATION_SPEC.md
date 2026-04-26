# Peerdrive 后端修改规格书 (已完成)

## 总体目标 (已实现)
1. **匿名 Collection**：实现了基于内容寻址 JSON 的不可变匿名合集。
2. **注册用户 Collection 改为 hash 指针**：`collections` 表增加 `current_hash`，每次 Commit 生成匿名快照 hash。
3. **P2P 下载回退**：`/sha256sum/:hash` 本地找不到时自动从 P2P 拉取（集成 libp2p Bitswap 逻辑）。
4. **多副本文件存储**：同一 hash 允许多条 `file_providers` 记录，每份独立 `available` 标记。
5. **集中式认证**：实现了用户注册、登录、AuthKey 会话管理及路由权限控制。

## 修改记录
(此处保留原表作为实现参考)

| # | 文件 | 操作 | 目的 | 状态 |
|---|------|------|------|---|
| 1 | `internal/model/anon.go` | 新建 | 匿名合集 JSON 结构体 + 文件类型常量 | ✅ |
| 2 | `internal/controller/anon.go` | 新建 | 匿名合集 CRUD 控制器 | ✅ |
| 3 | `internal/repository/anon_repo.go` | 新建 | 匿名合集 JSON 持久化 | ✅ |
| 4 | `internal/model/collection.go` | 修改 | Collection 增加 CurrentHash 字段 | ✅ |
| 5 | `internal/model/file.go` | 修改 | FileMetadata 增加 Available 字段 | ✅ |
| 6 | `internal/repository/db.go` | 修改 | 数据库 Schema 更新 (users, file_meta, file_providers) | ✅ |
| 7 | `internal/repository/collection_repo.go` | 修改 | 适配 current_hash；新增搜索功能 | ✅ |
| 8 | `internal/repository/file_repo.go` | 修改 | GetFileByHash 副本循环重试逻辑 | ✅ |
| 9 | `internal/controller/collection.go` | 修改 | Commit 生成快照；GetCollection 优先走快照 | ✅ |
| 10 | `internal/controller/file.go` | 修改 | 上传/注册不再检查 hash 重复 | ✅ |
| 11 | `internal/router/router.go` | 修改 | 增加 /auth, /anon 路由及 Auth 中间件 | ✅ |
| 12 | `internal/service/downloader.go` | 修改 | GetFileStream 循环可用位置 + P2P 回退 | ✅ |
| 13 | `internal/service/p2p.go` | 修改 | 增加 FetchFile 实现 | ✅ |
| 14 | `cmd/server/main.go` | 修改 | 初始化 AuthService 并注入路由 | ✅ |

## 约束验证
- [x] 所有改动对现有 API 向后兼容
- [x] 匿名合集 hash = 规范 JSON 的 SHA256
- [x] JSON entries 序列化前按 path 排序
- [x] 数据库通过 `ALTER TABLE` 或 `CREATE TABLE IF NOT EXISTS` 兼容迁移

---

## Stage 2: P2P DHT + mDNS + Custom Exchange Protocol (已完成)

| # | 文件 | 操作 | 目的 | 状态 |
|---|------|------|------|---|
| 15 | `internal/config/config.go` | 修改 | P2P 环境变量 (ENABLE/LISTEN/BOOTSTRAP/MDNS/RELAY) | ✅ |
| 16 | `internal/service/p2p.go` | 重写 | libp2p + DHT + mDNS + Exchange/Announce 协议 | ✅ |
| 17 | `internal/service/p2p_helpers.go` | 新建 | cidFromSha256 / parsePeerAddr 工具函数 | ✅ |
| 18 | `internal/controller/p2p.go` | 重写 | status/node/peers/discovered/ping/connect/announce/fetch/sync/push | ✅ |
| 19 | `internal/model/anon.go` | 修改 | AnonCollection 增加 FriendlyName | ✅ |
| 20 | `internal/service/anon_service.go` | 修改 | CreateCollection(name, entries) 支持命名 | ✅ |
| 21 | `internal/router/router.go` | 修改 | 注册所有 P2P 和新 anon 路由 | ✅ |
| 22 | `cmd/server/main.go` | 修改 | Pass cfg to NewP2PService | ✅ |
| 23 | `test/p2p.sh` | 新建 | 13 步双节点集成测试 | ✅ |
| 24 | `.github/workflows/ci.yml` | 新建 | CI: build + 4 测试套件 | ✅ |
| 25 | `docs/specs/api-reference.md` | 修改 | P2P Stage 2 端点文档 | ✅ |
| 26 | `docs/testing/p2p-stage2-report.md` | 新建 | Stage 2 测试报告 | ✅ |

## Stage 3: NAT Traversal + Relay + WS Transfer (已完成)

| # | 文件 | 操作 | 目的 | 状态 |
|---|------|------|------|---|
| 27 | `internal/config/config.go` | 修改 | RelayMode / StaticRelays / HolePunch / AutoNAT / NATPortMap / PublicReachable | ✅ |
| 28 | `internal/service/p2p.go` | 修改 | EnableRelay / EnableHolePunching / AutoNAT / BroadcastRequest / ProtocolRequest | ✅ |
| 29 | `internal/service/p2p_ws.go` | 新建 | WebSocket hub (wsHub) + WSHandler for /ws/transfer | ✅ |
| 30 | `internal/service/p2p_helpers.go` | 修改 | parseStaticRelays() | ✅ |
| 31 | `internal/controller/p2p.go` | 修改 | RequestFile / WSInfo 端点; P2PStatus 扩展 | ✅ |
| 32 | `internal/router/router.go` | 修改 | /p2p/request-file / /p2p/ws/info / /ws/transfer 路由 | ✅ |
| 33 | `test/relay.sh` | 新建 | 12 步 relay + WS 集成测试 | ✅ |
| 34 | `docs/specs/api-reference.md` | 修改 | Stage 3 端点文档 (request-file / ws/info / ws/transfer) | ✅ |
| 35 | `docs/testing/p2p-stage2-report.md` | 修改 | Stage 3 测试结果章节 | ✅ |
| 36 | `.github/workflows/ci.yml` | 修改 | 新增 relay.sh 测试步骤 | ✅ |

