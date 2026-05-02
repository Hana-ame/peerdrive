# Peerdrive Storage Module -- API Design Document

> Base URL: `http://localhost:3000`
> Last updated: 2026-04-28

## Overview

The Storage module provides content-addressed file storage with SHA256-based
addressing, dual CID indexing (IPFS compatible), multi-protocol download
(local -> IPFS -> BT DHT -> HTTP), collection management, share links, and
WebDAV mount. All file data is stored as `storage/<hash[:2]>/<hash>`.

---

## 1. File Endpoints

### 1.1 List Files

```http
GET /files
Query: ?sort=time|name|path|type|size  (default: time)
```

**Response 200:**
```json
[
  {
    "hash": "abc...",
    "filename": "report.pdf",
    "size": 1024000,
    "mime_type": "application/pdf",
    "created_at": "2026-04-28T10:00:00Z",
    "type": "blob",
    "provider_type": "local",
    "provider_path": "ab/abc..."
  }
]
```

**curl:**
```bash
curl -s http://localhost:3000/files | jq .
curl -s 'http://localhost:3000/files?sort=size' | jq .
```

---

### 1.2 Upload File

```http
POST /files/upload
Content-Type: multipart/form-data
Field: file
```

Stores file at `storage/<hash[:2]>/<hash>`, inserts `file_meta` and
`file_providers` rows. Duplicate content returns HTTP 200 with
`already_exists: true`.

**Response 201 (new):**
```json
{
  "hash": "abc...",
  "size": 1024,
  "mime": "text/plain",
  "filename": "hello.txt",
  "already_exists": false
}
```

**Response 200 (duplicate):**
```json
{
  "hash": "abc...",
  "size": 1024,
  "mime": "text/plain",
  "filename": "hello.txt",
  "already_exists": true
}
```

**Response 403 (storage disabled):**
```json
{"error": "storage is disabled"}
```

**curl:**
```bash
echo "Hello World" > /tmp/test.txt
curl -s -X POST http://localhost:3000/files/upload \
  -F "file=@/tmp/test.txt" | jq .
```

---

### 1.3 Register Local File (zero-copy)

```http
POST /files/register_local
Content-Type: application/json
```

Computes SHA256 of a local file path and registers it in the database
without copying to the storage directory.

**Request:**
```json
{
  "path": "/absolute/path/to/file.txt",
  "filename": "file.txt"
}
```

**Response 200:**
```json
{
  "hash": "abc...",
  "filename": "file.txt"
}
```

**curl:**
```bash
echo "Test content" > /tmp/myfile.txt
curl -s -X POST http://localhost:3000/files/register_local \
  -H "Content-Type: application/json" \
  -d '{"path": "/tmp/myfile.txt", "filename": "myfile.txt"}' | jq .
```

---

### 1.4 Register Folder (recursive)

```http
POST /files/register_folder
Content-Type: application/json
```

Recursively walks a directory and registers every file under it.

**Request:**
```json
{
  "folder_path": "/absolute/path/to/folder"
}
```

**Response 200:**
```json
{
  "registered": [
    {"filename": "a.txt", "hash": "abc..."},
    {"filename": "b.txt", "hash": "def..."}
  ]
}
```

**curl:**
```bash
mkdir -p /tmp/mydir
echo "content1" > /tmp/mydir/f1.txt
echo "content2" > /tmp/mydir/f2.txt
curl -s -X POST http://localhost:3000/files/register_folder \
  -H "Content-Type: application/json" \
  -d '{"folder_path": "/tmp/mydir"}' | jq .
```

---

### 1.5 Register URL

```http
POST /files/register_url
Content-Type: application/json
```

Fetches a file from a URL, computes SHA256, stores a local copy and
registers with `provider_type = "http"`. Auto-follows 301/302 redirects.

**Request:**
```json
{
  "url": "https://example.com/file.pdf",
  "filename": "download.pdf"
}
```

**Response 201:**
```json
{
  "hash": "abc...",
  "size": 1024000,
  "mime": "application/pdf",
  "filename": "download.pdf"
}
```

**curl:**
```bash
curl -s -X POST http://localhost:3000/files/register_url \
  -H "Content-Type: application/json" \
  -d '{"url": "https://httpbin.org/bytes/1024", "filename": "random.bin"}' | jq .
```

---

### 1.6 Verify File by Hash

```http
GET /files/verify/:hash
```

Looks up file metadata by SHA256 hash. Returns 404 if not found.

**Response 200:**
```json
{
  "hash": "abc...",
  "filename": "file.txt",
  "size": 1024,
  "mime": "text/plain"
}
```

**Response 404:**
```json
{"error": "file not found"}
```

**curl:**
```bash
curl -s http://localhost:3000/files/verify/abc123... | jq .
```

---

### 1.7 Browse Directory

```http
GET /files/browse
Query: ?path=/some/dir
```

Lists files and subdirectories in a local path (relative to storage dir
or absolute).

**Response 200:**
```json
[
  {
    "name": "ab",
    "path": "/mnt/storage/ab",
    "is_dir": true,
    "size": 4096,
    "mod_time": "2026-04-28T10:00:00Z"
  },
  {
    "name": "abc...",
    "path": "/mnt/storage/ab/abc...",
    "is_dir": false,
    "size": 1024,
    "mod_time": "2026-04-28T10:00:00Z"
  }
]
```

**curl:**
```bash
curl -s 'http://localhost:3000/files/browse?path=.' | jq .
```

---

### 1.8 Delete File

```http
DELETE /files/:hash
```

Removes the file from disk (local providers), deletes `file_providers`
and `file_meta` rows.

**Response 200:**
```json
{"message": "deleted"}
```

**curl:**
```bash
curl -s -X DELETE http://localhost:3000/files/abc123... | jq .
```

---

### 1.9 Copy File

```http
POST /files/copy
Content-Type: application/json
```

Copies a file within storage by hash to a destination path and registers
the copy as a new local provider.

**Request:**
```json
{
  "hash": "abc...",
  "dest_path": "/target/path/file.txt"
}
```

**Response 201:**
```json
{
  "hash": "abc...",
  "dest_path": "/full/path/to/target"
}
```

**curl:**
```bash
curl -s -X POST http://localhost:3000/files/copy \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc...", "dest_path": "/tmp/copy.txt"}' | jq .
```

---

## 2. Download Endpoints

### 2.1 SHA256 Download

```http
GET /sha256sum/:hash
Query: ?inline=1
```

Downloads a file by its SHA256 hash. Uses the universal multi-protocol
downloader (local -> IPFS -> BT DHT -> HTTP). Supports Range requests
for partial content (HTTP 206).

**Response 200:** binary file stream
- `Content-Disposition: attachment; filename=xxx`
- `Content-Encoding: gzip` (if gziped)
- `X-Peerdrive-Collection: true` (if collection)
- `X-Protocol: local|ipfs|btdht|http`

**Response 206 (Range):**
- `Content-Range: bytes 0-99/1024`
- `Content-Length: 100`

**curl:**
```bash
# Full download
curl -s -o /tmp/download http://localhost:3000/sha256sum/abc...

# Range request (first 100 bytes)
curl -s -o /tmp/partial -H "Range: bytes=0-99" \
  http://localhost:3000/sha256sum/abc...

# Inline display
curl -s -o /tmp/view http://localhost:3000/sha256sum/abc...?inline=1
```

### 2.2 CID Download

```http
GET /ipfs/:cid
```

Downloads a file by its IPFS CID. Falls back to public IPFS gateways
when not found locally.

**Response 200:** binary file stream
- `Content-Disposition: attachment; filename=xxx`
- `X-CID: Qm...`

**curl:**
```bash
curl -s -o /tmp/cid-download http://localhost:3000/ipfs/Qm...
```

### 2.3 Universal Download

```http
GET /download/:hash
```

Multi-protocol download (local -> IPFS -> BT DHT -> HTTP gateway).

**Response 200:** binary
- `X-Protocol: local|ipfs|btdht|http|ipfsgw`

```bash
curl -s -o /tmp/uni http://localhost:3000/download/abc...
```

### 2.4 Download Sources

```http
GET /download/:hash/sources
```

Returns availability per protocol.

**Response 200:**
```json
{
  "local": true,
  "ipfs": false,
  "btdht": true,
  "http": false,
  "ipfsgw": true
}
```

### 2.5 Download Refresh

```http
POST /download/:hash/refresh
```

Clears local cache and re-runs the multi-protocol download pipeline.

**Response 200:** binary
- `X-Protocol: ...`

---

## 3. Anonymous Collection Endpoints

### 3.1 Create Anonymous Collection

```http
POST /anon/collections
Content-Type: application/json
```

Creates a content-addressed immutable collection. Paths must be relative
and must not contain `..`. Hashes must be 64-char hex SHA256.

**Request:**
```json
{
  "friendly_name": "my-collection",
  "entries": [
    {"path": "docs/readme.txt", "hash": "abc..."},
    {"path": "images/logo.png", "hash": "def..."}
  ],
  "tags": ["important", "work"]
}
```

**Response 201:**
```json
{"hash": "sha256-of-collection-json"}
```

**curl:**
```bash
curl -s -X POST http://localhost:3000/anon/collections \
  -H "Content-Type: application/json" \
  -d '{
    "friendly_name": "test-coll",
    "entries": [
      {"path": "file1.txt", "hash": "abc..."},
      {"path": "file2.txt", "hash": "def..."}
    ]
  }' | jq .
```

### 3.2 List Anonymous Collections

```http
GET /anon/collections
```

**Response 200:**
```json
[
  {
    "hash": "abc...",
    "friendly_name": "my-collection",
    "name_preview": "docs/readme.txt, images/logo.png",
    "version": 1,
    "tags": ["important"],
    "entry_count": 2,
    "created_at": "2026-04-28T10:00:00Z"
  }
]
```

**curl:**
```bash
curl -s http://localhost:3000/anon/collections | jq .
```

### 3.3 Get Anonymous Collection

```http
GET /anon/collections/:hash
```

**Response 200:**
```json
{
  "version": 1,
  "friendly_name": "my-collection",
  "entries": [
    {"path": "docs/readme.txt", "hash": "abc..."},
    {"path": "images/logo.png", "hash": "def..."}
  ],
  "tags": ["important"],
  "created_at": "2026-04-28T10:00:00Z"
}
```

**curl:**
```bash
curl -s http://localhost:3000/anon/collections/abc... | jq .
```

### 3.4 Download File from Collection

```http
GET /anon/collections/:hash/*filepath
```

**curl:**
```bash
curl -s -o /tmp/readme.txt \
  http://localhost:3000/anon/collections/abc.../docs/readme.txt
```

### 3.5 Commit Collection (versioned)

```http
POST /anon/collections/commit
Content-Type: application/json
```

Commits modifications to an existing anonymous collection. Entries with
empty hash are removed. Version is auto-incremented.

**Request:**
```json
{
  "source_hash": "abc...",
  "entries": [
    {"path": "docs/new.txt", "hash": "def..."},
    {"path": "docs/readme.txt", "hash": ""}
  ],
  "commit_message": "added new.txt, removed readme"
}
```

**Response 201:**
```json
{"hash": "new-collection-hash"}
```

### 3.6 Fork Collection

```http
POST /anon/collections/fork
Content-Type: application/json
```

Creates a variant by adding and/or removing entries.

**Request:**
```json
{
  "source_hash": "abc...",
  "friendly_name": "forked-collection",
  "add_entries": [{"path": "new.txt", "hash": "def..."}],
  "remove_paths": ["old.txt"]
}
```

**Response 201:**
```json
{"hash": "fork-collection-hash"}
```

---

## 4. Share Endpoints

### 4.1 Create Share

```http
POST /shares
Content-Type: application/json
```

Creates a share link for a file or collection. Token is 32-char hex.
Expires in 30 days.

**Request:**
```json
{
  "hash": "abc...",
  "type": "file",
  "filename": "share-name.txt"
}
```

**Response 200:**
```json
{
  "token": "aabbccdd...",
  "hash": "abc...",
  "type": "file",
  "filename": "share-name.txt",
  "url": "/s/aabbccdd...",
  "expires": "2026-05-28T10:00:00Z"
}
```

**curl:**
```bash
curl -s -X POST http://localhost:3000/shares \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc...", "type": "file"}' | jq .
```

### 4.2 List Shares

```http
GET /shares
```

**Response 200:**
```json
{
  "shares": [
    {
      "id": 1,
      "token": "aabbccdd...",
      "hash": "abc...",
      "type": "file",
      "filename": "share-name.txt",
      "created_at": "2026-04-28T10:00:00Z",
      "expires_at": "2026-05-28T10:00:00Z"
    }
  ]
}
```

**curl:**
```bash
curl -s http://localhost:3000/shares | jq .
```

### 4.3 Access Share

```http
GET /s/:token
```

Redirects to the shared resource:
- `type=file` -> `307 /sha256sum/<hash>`
- `type=collection` -> `307 /anon/collections/<hash>`

**curl:**
```bash
curl -s -L http://localhost:3000/s/aabbccdd... -o /tmp/shared
```

---

## 5. WebDAV

### 5.1 WebDAV Mount

```http
ALL /webdav/*
```

Full WebDAV protocol support using `golang.org/x/net/webdav`:
- PROPFIND, MKCOL, GET, HEAD, PUT, DELETE, COPY, MOVE
- LOCK, UNLOCK, OPTIONS

Enabled when `PEERDRIVE_WEBDAV_ENABLE=true` (default).

**curl (PROPFIND):**
```bash
curl -s -X PROPFIND http://localhost:3000/webdav/ \
  -H "Depth: 1" | xmllint --format -
```

**Mount examples:**
```bash
# Linux (davfs2)
sudo mount -t davfs http://localhost:3000/webdav /mnt/peerdrive

# macOS (Finder)
# Cmd+K -> http://localhost:3000/webdav

# Windows
# net use Z: http://localhost:3000/webdav
```

---

## 6. Health Check

### 6.1 Ping

```http
GET /ping
```

**Response 200:**
```json
{"message": "pong"}
```

**curl:**
```bash
curl -s http://localhost:3000/ping
```
