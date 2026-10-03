# File Registration Layer (Register Layer)

## Involved Files

```
internal/controller/file.go      — HTTP entry points RegisterLocalFile, RegisterFolder
internal/service/file_service.go — Business logic: hash computation, metadata extraction, database writes
internal/model/file.go           — FileMeta + FileProvider
internal/repository/file_repo.go — file_meta + file_providers INSERT / GET
internal/repository/db.go        — schema
internal/provider/local.go       — File reading (absolute/relative path adaptive)
internal/config/config.go        — PEERDRIVE_STORAGE_ENABLE switch
```

## API

### POST /files/register_local
Registers a single local file.

**Request body:**
```json
{"path": "/absolute/path/to/file", "filename": "display_name.txt"}
```

**Processing flow:**
1. Check `StorageEnable`; return 403 if disabled.
2. Open the file specified by `path`.
3. Compute SHA256 hash.
4. Get file `Size` (via `os.Stat`).
5. Detect MIME type (`http.DetectContentType` reads first 512 bytes + extension fallback).
6. Write to `file_meta` (idempotent: skip if hash already exists).
7. Write to `file_providers`, with `path` being the provided absolute path.
8. Return `{"hash": "...", "filename": "..."}`.

### POST /files/register_folder
Batch-registers all files within a folder (recursively).

**Request body:**
```json
{"folder_path": "/absolute/path/to/folder"}
```

**Processing flow:**
1. Check `StorageEnable`; return 403 if disabled.
2. Recursively traverse the directory using `filepath.Walk`.
3. Call `RegisterLocal` for each non-directory file (passing the absolute path).
4. Return `{"registered": [{"filename":"...","hash":"..."}, ...]}`.

## Registration vs Upload

| Feature | Upload | Register |
|---------|--------|----------|
| File source | HTTP multipart stream | File already on disk |
| Copy or not | Copies to `storage/{h[:2]}/{h}` | No copy; directly references the original path |
| Hash already exists | Returns `already_exists: true` (200) | Idempotent; returns the same hash |
| Path storage | Relative path `{h[:2]}/{h}` | Absolute path |

## Metadata Writing

The following metadata is automatically computed and written during registration:
- `Size`: file size (bytes)
- `MimeType`: HTTP content type detection
- `Gziped`: fixed at `false`
- `Type`: fixed at `blob`

## Configuration Switch

The environment variable `PEERDRIVE_STORAGE_ENABLE` controls write operation availability:
- `true` (default): allows registration and upload
- `false`: all write operations return 403 Forbidden

## Design Notes

| Decision | Approach | Reason |
|----------|----------|--------|
| Do not copy files | Only write to DB, do not write files | Avoids duplicate storage; suitable for pre-import scenarios |
| Idempotent insert | Skip if hash exists | Repeated registration does not corrupt existing metadata |
| Absolute path storage | Write the full path | Supports file registration from any location |
| Recursive traversal | Uses filepath.Walk | Supports deep nested directory registration |
