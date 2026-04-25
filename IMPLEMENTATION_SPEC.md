# Peerdrive 后端修改规格书（目的版）

## 总体目标

1. **匿名 Collection**：基于内容寻址 JSON 的不可变匿名合集。
2. **注册用户 Collection 改为 hash 指针**：`collections` 表增加 `current_hash`，每次 Commit 生成匿名快照 hash。
3. **P2P 下载回退**：`/sha256sum/:hash` 本地找不到时自动从 P2P 拉取。
4. **多副本文件存储**：同一 hash 允许多条 `files` 表记录，每份独立 `available` 标记。

## 修改文件一览

| # | 文件 | 操作 | 目的 |
|---|------|------|------|
| 1 | `internal/model/anon.go` | **新建** | 匿名合集 JSON 结构体 + 文件类型常量 |
| 2 | `internal/controller/anon.go` | **新建** | 匿名合集 CRUD 控制器 |
| 3 | `internal/repository/anon_repo.go` | **新建** | 匿名合集 JSON 持久化（写入 storage + files 表） |
| 4 | `internal/model/collection.go` | **修改** | Collection 增加 CurrentHash 字段 |
| 5 | `internal/model/file.go` | **修改** | FileMetadata 增加 Available 字段 |
| 6 | `internal/repository/db.go` | **修改** | collections 增加 current_hash；files 移除 UNIQUE(hash)、增加 type/metadata/available |
| 7 | `internal/repository/collection_repo.go` | **修改** | 全部查询适配 current_hash；新增 UpdateCurrentHash |
| 8 | `internal/repository/file_repo.go` | **修改** | GetFileByHash 返回第一条可用（优先 local）；新增 MarkFileUnavailable |
| 9 | `internal/controller/collection.go` | **修改** | Commit 同时生成快照 hash；GetCollection 优先走快照；Rollback 更新 hash |
| 10 | `internal/controller/file.go` | **修改** | 上传/注册不再检查 hash 重复，直接 INSERT |
| 11 | `internal/router/router.go` | **修改** | 增加 /anon/* 路由 |
| 12 | `internal/service/downloader.go` | **修改** | GetFileStream 循环可用位置 + P2P 回退 |
| 13 | `internal/service/p2p.go` | **修改** | 增加 FetchFile 占位方法 |
| 14 | `cmd/server/main.go` | **修改** | NewDownloader 传递 p2pSvc 和 storageDir |

## 约束

- 所有改动必须对现有 API 向后兼容
- 匿名合集 hash = 规范 JSON 的 SHA256
- JSON 的 `entries` 序列化前必须按 `path` 字典序排序
- 旧数据库用 `ALTER TABLE` 迁移
