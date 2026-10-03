# `file_meta` 表为什么难改

> 2026-05-03T23:50:00

---

## 核心问题

`file_meta` 不是一张独立的表，而是**存储系统的核心索引**。它记录了每个 SHA256 哈希对应的文件元数据（大小、MIME、文件名、是否 gzip、CID）。删除它意味着没有地方存这些信息。

按新理念"文件=单 entry 的 collection"，需要把这些元数据**嵌入到 collection 的 entry 中**。但这带来连锁问题：

## 改动波及面

### 1. 引用方（13 处）

| 文件 | 使用方式 | 为什么依赖 |
|------|----------|-----------|
| `controller/download.go` (4 处) | `GetFileMeta(hash)` 获取 filename、gzip、mime_type | 下载时需要这些元数据来设 HTTP 头 |
| `controller/file.go` | `ListAllFiles()` 列出文件、"InsertFileMeta" 存储上传结果 | 上传/注册后记录文件元数据 |
| `controller/anon.go` | `file_meta WHERE type='anon_collection'` 列出合集 | 合集列表直接依赖 `file_meta` 表 |
| `controller/p2p.go` | `InsertFileMeta` 在 BT 下载完成时注册 | 跨协议文件注册 |
| `repository/anon_repo.go` (2 处) | `InsertFileMeta` 保存合集 JSON；按 type 查询合集 | 合集存储依赖于 file_meta 的 type 字段 |
| `repository/file_repo.go` (全部) | 4 个 SQL 操作（Get/Insert/List/GetByCID） | 整个文件仓库层 |
| `model/file.go` | `FileMeta` struct 定义 | 模型定义 |

### 2. 数据关联

```
file_meta (hash PK)
  ├── file_providers (hash → storage path)
  ├── anon_repo (type='anon_collection' 的 file_meta 行被视为合集)
  ├── download handler (需要 filename/mime/gzip 信息)
  └── BT download complete callback (注册文件)
```

### 3. 功能依赖

| 功能 | 依赖 file_meta 的原因 |
|------|---------------------|
| **文件下载** | 需要 filename 设 Content-Disposition，需要 gzip 标志设 Content-Encoding |
| **合集列表** | `ListAnonCollections` 直接查 `file_meta WHERE type='anon_collection'` |
| **文件列表** | `ListAllFiles` 查 `file_meta` 联表 `file_providers` |
| **文件上传** | 上传完成后 `InsertFileMeta` 记录结果 |
| **BT 下载完成** | 回调中 `InsertFileMeta` 注册已下载文件 |
| **IPFS CID 查找** | `GetFileMetaByCID` 通过 CID 反查 hash |

## 如果非要改，路径

### 方案 A：元数据嵌入 collection JSON

```
collection.json (content-addressed):
{
  entries: [{
    path: "file.txt",
    providers: [{ type: "sha256", value: "<hash>" }],
    meta: { filename: "file.txt", mime_type: "text/plain", size: 1234, gziped: false }
  }]
}
```

**问题**：
- 下载 `/sha256sum/:hash` 时传入的是 hash，不是 collection hash，查不到 metadat
- 需要另一层 hash → collection 的映射
- 合集列表仍需可查询（`WHERE type='collection'`）
- 文件上传的瞬时操作需要立即返回元数据

### 方案 B：file_meta 简化为 file_entry 表（推荐折中）

不删 `file_meta`，而是改名 `file_entry`，作为 collection entry 的缓存层：

```
file_entry (hash PK)
  ├── filename, mime_type, size, gziped  ← 纯元数据缓存
  ├── cid                                ← IPFS 兼容
  └── provider_path                      ← 存储路径

collection_entries (path, hash)
  └── 引用 file_entry.hash

collections (id, name, ...)
  └── version → collection_entries
```

这样 `file_entry` 就是纯元数据缓存，不再同时承担"文件列表"和"合集列表"的双重角色。真正的数据归属在 collection。

### 方案 C：直接放弃（当前建议）

当前架构虽然冗余，但功能正常。`file_meta` 的删除需要重写：
1. 存储层（repository/file_repo.go）
2. 下载层（download.go 的 Content-Disposition/Content-Encoding）
3. 文件列表 API
4. 合集列表 API
5. BT 完成回调
6. 前端文件列表页面（已删除，少一个依赖）

**共 6 个模块，13 处代码**，且有数据迁移风险。建议等技术债务积累到自然重写时一并解决。
