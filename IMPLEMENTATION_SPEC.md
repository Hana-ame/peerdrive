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

