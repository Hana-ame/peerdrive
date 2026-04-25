# SHA256 下载层分析

## 涉及文件

```
internal/controller/download.go    — HTTP 入口 /sha256sum/:sha256
internal/service/downloader.go     — 业务逻辑（查询元数据 + provider 读取 + 多位置重试）
internal/provider/                 — 内容寻址读取（local/http）
internal/model/file.go             — FileMetadata（含 metadata JSON + available）
internal/repository/file_repo.go   — files 表查询（优先 local 可用记录）
internal/repository/db.go          — files 表 schema（metadata TEXT + available + type）
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
        ┌──[循环]──────────────────────────────────────┐
        │  repository.GetFileByHash(hash)               │
        │  └─ SELECT ... WHERE hash=? AND available=1   │
        │     ORDER BY local优先                        │
        │                                               │
        │  ├─ meta != nil                               │
        │  │   ├─ provider.GetReader → OK → return       │
        │  │   └─ provider.GetReader → FAIL              │
        │  │       └─ MarkFileUnavailable(id) → 重试     │
        │  │                                             │
        │  └─ meta == nil → 跳出循环                     │
        └───────────────────────────────────────────────┘
        │
        ├─ P2P 回退（已实现占位）
        │   ├─ FetchFile(hash) → OK → 缓存到 storage/p2p/{...} → INSERT files → return
        │   └─ FetchFile(hash) → FAIL → 404
        │
        return (io.ReadCloser, filename, metadataJSON)
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
上传:  multipart → SHA256 → storage/{prefix}/{hash} → files INSERT (hash可能已存在)
                                                         │
下载:  files SELECT(available=1) ← hash → 循环重试 → HTTP stream
                                          ↑       ↑
                                    失败标记不可用  Content-Encoding/Disposition
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
- `InitDB` 执行三条 `ALTER TABLE` 迁移：metadata / type / available
- 旧数据 `metadata` 为 `'{}'`，type 为 `'blob'`，available 为 1
- 旧数据库 files 表 hash 列从 `UNIQUE` 改为非唯一（多条迁移语句需手动处理 UNIQUE 约束）

## 设计决策

| 决策 | 方案 | 原因 |
|------|------|------|
| metadata 格式 | JSON 文本列 | 可扩展，任何属性无需改表 |
| gzip 检测方式 | 读文件前 2 字节 | 不需要解压，零成本 |
| 返回类型 | GetFileStream 返回原始 metadata string | controller 层自行解析，灵活 |
| 版本隔离 | GetFileStream 不关心 metadata 含义 | downloader 只负责"拿到文件流 + 附带的元数据" |
| 多副本重试 | 读取失败 → MarkFileUnavailable → 尝试下一记录 | 自动容错，无需手动修复 |

