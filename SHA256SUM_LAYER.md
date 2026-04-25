# SHA256 下载层分析

## 涉及文件

```
internal/controller/download.go    — HTTP 入口 /sha256sum/:sha256
internal/service/downloader.go     — 业务逻辑（查询元数据 + provider 读取）
internal/provider/                 — 内容寻址读取（local/http）
internal/model/file.go             — FileMetadata（含 metadata JSON）
internal/repository/file_repo.go   — files 表查询
internal/repository/db.go          — files 表 schema（metadata TEXT）
internal/controller/file.go        — 文件上传/注册（写入元数据）
```

## 请求生命周期

```
GET /sha256sum/a1b2c3d4...
        │
        ▼
controller.DownloadBySHA256
        │  校验 hash 格式 (64 hex chars)
        ▼
service.Downloader.GetFileStream(hash)
        │
        ├─ repository.GetFileByHash(hash)
        │   └─ SELECT id, hash, provider_type, path, filename, metadata
        │      FROM files WHERE hash = ?
        │
        ├─ meta == nil → P2P 回退（未实现）→ 404
        │
        └─ meta != nil
                │
                ├─ provider.Manager.GetReader(meta.ProviderType, meta.Path)
                │   ├─ "local" → 读取 storage/{hash[:2]}/{hash}
                │   └─ "http"  → 远程 HTTP 流
                │
                └─ return (io.ReadCloser, filename, metadataJSON)
                        │
                        ▼
                controller.DownloadBySHA256Internal
                        │
                        ├─ set Content-Disposition: attachment; filename=xxx
                        ├─ parse metadata JSON → if is_gzip → set Content-Encoding: gzip
                        └─ c.DataFromReader(200, -1, "application/octet-stream", reader, nil)
```

## 数据流方向

```
上传:  multipart → SHA256 → storage/{prefix}/{hash} → files 表 INSERT (含 metadata)
                                                          │
下载:  files 表 SELECT ← hash → provider.GetReader  → HTTP stream
                           ↑       ↑
                      Content-Encoding  Content-Disposition
```

## 元数据设计

`files.metadata` 列是 TEXT 类型，存 JSON，当前只用一个字段：

```json
{"is_gzip": true}
```

### 检测时机

| 操作 | 检测方式 | 写入位置 |
|------|----------|----------|
| `POST /files/upload` | 文件写入磁盘后 `os.Open` 读前 2 字节 | metadata JSON |
| `POST /files/register_local` | 同上传 | metadata JSON |
| `POST /files/register_folder` | 同上传 | metadata JSON |

检测逻辑在 `controller/file.go:isGzipFile(path)` — 魔数 `0x1f 0x8b`。

### 消费时机

| 操作 | 读取方式 | 消费方式 |
|------|----------|----------|
| `GET /sha256sum/:sha256` | `downloader.GetFileStream` 返回 metadata JSON | 解析 `is_gzip` → `Content-Encoding: gzip` |
| `GET /files/verify/:hash` | `repository.GetFileByHash` 返回完整 struct | 将 metadata JSON 反序列化为 map 后返回 |

## 元数据扩展方式

新增属性（如 `mime_type`）只需改两处：

1. **写入侧**（`controller/file.go` 三个函数）：
```go
metaData, _ := json.Marshal(map[string]any{
    "is_gzip":   isGzipFile(fullPath),
    "mime_type": mimeDetect(fullPath),  // 新加
})
```

2. **消费侧**（`controller/download.go`）：
```go
var meta map[string]any
json.Unmarshal([]byte(metaJSON), &meta)
if mime, _ := meta["mime_type"].(string); mime != "" {
    c.Header("Content-Type", mime)
}
```

**无需改表结构、无需改 model、无需改 repo 层。**

## 向后兼容性

- 旧表有 `is_gzip INTEGER` 列：保留不动，新代码忽略该列
- `InitDB` 执行 `ALTER TABLE files ADD COLUMN metadata TEXT DEFAULT '{}'`，列已有时 SQLite 返回错误，忽略即可
- 旧数据 `metadata` 为 `'{}'`，is_gzip 默认为 false，不设置 gzip 编码
- 所有新写入的文件都有完整的 metadata JSON

## 设计决策

| 决策 | 方案 | 原因 |
|------|------|------|
| metadata 格式 | JSON 文本列 | 可扩展，任何属性无需改表 |
| gzip 检测方式 | 读文件前 2 字节 | 不需要解压，零成本 |
| 返回类型 | GetFileStream 返回原始 metadata string | controller 层自行解析，灵活 |
| 版本隔离 | GetFileStream 不关心 metadata 含义 | downloader 只负责"拿到文件流 + 附带的元数据" |
