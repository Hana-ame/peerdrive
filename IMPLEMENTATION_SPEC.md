# Peerdrive 后端修改规格书（目的版）

## 总体目标

1. **匿名 Collection**：基于内容寻址 JSON 的不可变匿名合集。
2. **注册用户 Collection 改为 CID 指针**：`collections` 表增加 `current_cid`，每次 Commit 生成匿名 CID。
3. **P2P 下载回退**：`/sha256sum/:hash` 本地找不到时自动从 P2P 拉取。

## 修改文件一览

| # | 文件 | 操作 | 目的 |
|---|------|------|------|
| 1 | `internal/model/anon.go` | **新建** | 匿名合集 JSON 结构体 |
| 2 | `internal/controller/anon.go` | **新建** | 匿名合集 CRUD 控制器 |
| 3 | `internal/repository/anon_repo.go` | **新建** | 匿名合集 JSON 持久化（写入 storage + files 表） |
| 4 | `internal/model/collection.go` | **修改** | Collection 增加 CurrentCID 字段 |
| 5 | `internal/repository/db.go` | **修改** | collections 表增加 current_cid 列 |
| 6 | `internal/repository/collection_repo.go` | **修改** | 全部查询适配 current_cid；新增 UpdateCurrentCID |
| 7 | `internal/controller/collection.go` | **修改** | Commit 同时生成 CID；GetCollection 优先返回 CID 内容；Rollback 更新 CID |
| 8 | `internal/router/router.go` | **修改** | 增加 /anon/* 路由 |
| 9 | `internal/service/downloader.go` | **修改** | GetFileStream 增加 P2P 回退 |
| 10 | `internal/service/p2p.go` | **修改** | 增加 FetchFile 方法（Bitswap 集成） |
| 11 | `cmd/server/main.go` | **修改** | NewDownloader 传递 p2pSvc 和 storageDir |

## 约束

- 所有改动必须对现有 API 向后兼容
- 匿名 CID = 规范 JSON 的 SHA256
- JSON 的 `entries` 序列化前必须按 `path` 字典序排序
- 旧数据库用 `ALTER TABLE` 迁移
