# SHA256 Download Layer Analysis

## Related Files

```
internal/controller/download.go    — HTTP entry point /sha256sum/:sha256
internal/service/downloader.go     — Business logic (metadata query + provider read + multi-location retry)
internal/provider/                 — Content-addressed read (local/http)
internal/model/file.go             — FileMeta + FileProvider
internal/repository/file_repo.go   — file_meta + file_providers queries
internal/repository/db.go          — Schema (file_meta + file_providers)
internal/controller/file.go        — File upload/registration (writes to both tables)
```

## Request Lifecycle

```
GET /sha256sum/a1b2c3d4...
         │
         ▼
controller.DownloadBySHA256
         │  Validate hash format (64 hex chars)
         ▼
service.Downloader.GetFileStream(hash)
         │
         ┌──[loop]──────────────────────────────────────┐
         │  repository.GetFileByHash(hash)               │
         │  └─ SELECT ... WHERE hash=? AND available=1   │
         │     ORDER BY local first                      │
         │                                               │
         │  ├─ meta != nil                               │
         │  │   ├─ provider.GetReader → OK → return      │
         │  │   └─ provider.GetReader → FAIL             │
         │  │       └─ MarkFileUnavailable(id) → retry   │
         │  │                                             │
         │  └─ meta == nil → break loop                  │
         └───────────────────────────────────────────────┘
         │
         ├─ P2P fallback (placeholder implemented)
         │   ├─ FetchFile(hash) → OK → cache to storage/p2p/{...} → INSERT files → return
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

## Data Flow Direction

```
Upload:  multipart → SHA256 → storage/{prefix}/{hash} → files INSERT (hash may already exist)
                                                          │
Download:  files SELECT(available=1) ← hash → loop retry → HTTP stream
                                           ↑       ↑
                                   mark unavailable  Content-Encoding/Disposition
```

## Metadata Design

The `files.metadata` column is TEXT type, storing JSON. Currently only one field is used:

```json
{"is_gzip": true}
```

### Detection Timing

| Operation | Detection Method | Write Location |
|-----------|------------------|----------------|
| `POST /files/upload` | `os.Open` reads first 2 bytes after file is written to disk | metadata JSON |
| `POST /files/register_local` | Same as upload | metadata JSON |
| `POST /files/register_folder` | Same as upload | metadata JSON |

Detection logic is in `controller/file.go:isGzipFile(path)` — magic number `0x1f 0x8b`.

### Consumption Timing

| Operation | Read Method | Consumption Method |
|-----------|-------------|-------------------|
| `GET /sha256sum/:sha256` | `downloader.GetFileStream` returns metadata JSON | Parse `is_gzip` → `Content-Encoding: gzip` |
| `GET /files/verify/:hash` | `repository.GetFileByHash` returns full struct | Deserialize metadata JSON into map then return |

## Metadata Extension Pattern

Adding new properties (e.g. `mime_type`) only requires changing two places:

1. **Write side** (`controller/file.go` three functions):
```go
metaData, _ := json.Marshal(map[string]any{
    "is_gzip":   isGzipFile(fullPath),
    "mime_type": mimeDetect(fullPath),  // newly added
})
```

2. **Consumption side** (`controller/download.go`):
```go
var meta map[string]any
json.Unmarshal([]byte(metaJSON), &meta)
if mime, _ := meta["mime_type"].(string); mime != "" {
    c.Header("Content-Type", mime)
}
```

**No table schema changes, no model changes, no repo layer changes needed.**

## Backward Compatibility

- Old tables with `is_gzip INTEGER` column: kept as-is, new code ignores this column
- `InitDB` executes three `ALTER TABLE` migrations: metadata / type / available
- Old data: `metadata` is `'{}'`, type is `'blob'`, available is 1
- Old databases need manual migration of `files` table data to `file_meta` + `file_providers` tables

## Design Decisions

| Decision | Approach | Reason |
|----------|----------|--------|
| Metadata format | JSON text column | Extensible, any property doesn't require schema changes |
| Gzip detection method | Read first 2 bytes of file | No decompression needed, zero cost |
| Return type | GetFileStream returns raw metadata string | Controller layer parses it itself, flexible |
| Version isolation | GetFileStream doesn't care about metadata semantics | Downloader only handles "get file stream + accompanying metadata" |
| Multi-replica retry | Read failure → MarkFileUnavailable → try next record | Automatic fault tolerance, no manual repair needed |
