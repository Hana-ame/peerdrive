# Backend Feature Documentation

> Last updated: 2026-04-27

---

## Architecture Overview

```
internal/serverapp/app.go          — Entry point: load config → initialize components → start HTTP
  │
  ├── config/config.go      — Environment variable configuration
  ├── router/router.go      — Route registration (22+ routes)
  ├── controller/           — HTTP layer: parameter validation, call service, return JSON
  │   ├── file.go, collection.go, p2p.go, anon.go, sync.go, fork.go, merge.go, auth.go
  ├── service/              — Business logic layer
  │   ├── file_service.go   — File upload/registration/verification/deletion
  │   ├── p2p.go            — libp2p node, Exchange/Announce/Request protocol
  │   ├── p2p_connection.go — Connection management (heartbeat/reconnect)
  │   ├── p2p_transfer.go   — Chunked transfer (256KB chunk/8 concurrent)
  │   ├── p2p_ws.go         — WebSocket transfer
  │   ├── downloader.go     — Content-addressed download (local → P2P fallback)
  │   ├── anon_service.go   — Anonymous collection CRUD
  │   ├── sync_service.go   — Local sync
  │   └── auth_service.go   — User authentication
  ├── provider/             — Storage backend abstraction
  │   ├── local.go          — Local files
  │   └── http.go           — Remote HTTP
  └── repository/           — SQLite data access
      ├── file_repo.go, collection_repo.go, anon_repo.go, sync_repo.go, task_repo.go, user_repo.go
```

---

## 🔴 P1 - User Repeated Complaints

### 🔴 P1.1 File Browse `/files/browse` 301 Redirect Issue

**Source of complaint**: FileSystemBrowse.txt

```
[GIN-debug] redirecting request 301: /files/browse/ → /files/browse/?path=%2F
```

Route: `GET /files/browse?path=<dir>`
Handler: `controller.BrowseDir` → `FileService.BrowseDir(dirPath)`

**Issue**: Some requests with trailing `/` trigger Gin 301 redirect. Already set in `router.go`:
```go
r.RedirectTrailingSlash = false
r.RedirectFixedPath = false
```
**Need to verify if this is effective**. If issues persist, need to add `/files/browse/` route explicitly.

**Test**:
```bash
curl -v http://127.0.0.1:3000/files/browse/?path=/
# Should return 200 + JSON, not 301
```

### 🔴 P1.2 Windows Path Compatibility

**Source of complaint**: FileSystemBrowse.txt ("not necessarily running on Linux")

- `BrowseDir` uses `filepath.Join` for path handling (cross-platform safe)
- `DefaultRootPath()` already returns `C:\` on Windows
- `storageDir` should also use `filepath` handling

**Current status**: Already uses `filepath.IsAbs`, `filepath.Join`. **Needs verification in Windows environment**.

---

## API Endpoint Details

### System

| Method | Path | Handler | Description | Test Point |
|--------|------|---------|-------------|------------|
| GET | `/ping` | `controller.Ping` | Health check → `"pong"` | `curl /ping` → 200 |

### File Management

#### GET `/files?sort=time`

Lists all registered files. sort parameter: `time`, `name`, `size`, `type`, `path`.

**Response**: `[{hash, filename, size, mime_type, created_at, provider_type, provider_path}]`

**Test**: `curl /files` → 200 + JSON array

#### POST `/files/upload`

Multipart file upload.

**Request**: `multipart/form-data`, field `file`
**Response**: `{hash, filename, size}`
**Flow**: Receive file stream → Calculate SHA256 → Write to storage → Register to DB

**Test**:
```bash
echo "test" > /tmp/t.txt
curl -X POST http://127.0.0.1:3000/files/upload -F "file=@/tmp/t.txt"
# → {"hash":"...","filename":"t.txt","size":5}
```

#### POST `/files/register_local`

Register a local file path.

**Request**: `{path: "/abs/path/to/file", filename: "optional"}`
**Flow**: Read local file → Calculate SHA256 → Register to DB
**Test**: Point to an existing file → 200 + `{hash, filename}`

#### POST `/files/register_folder`

Recursively register a folder.

**Request**: `{folder_path: "/abs/path/"}`
**Response**: `{registered: [{hash, filename, path}], count: N}`
**Security**: Requires path traversal detection (reject `../`)

**Test**:
```bash
curl -X POST /files/register_folder -H 'Content-Type: application/json' \
  -d '{"folder_path":"/tmp/testdir"}'
# → 200 + registered array
```

#### GET `/files/verify/:hash`

Verify file integrity.

**Response**: `{hash, filename, size, mime_type, exists: true/false, consistent: true/false}`
**Test**: Use a known hash → verify returns exists:true, consistent:true

#### DELETE `/files/:hash`

Delete file metadata and provider records. **Does not delete physical files**.

**Test**: `curl -X DELETE /files/<hash>` → 200

#### GET `/files/browse?path=/`

Browse server filesystem (not limited to registered files).

**Response**: `[{name, path, is_dir, size, mod_time}]`
**Security**: Only returns directory contents, does not traverse symlinks
**Default path**: Linux `/`, Windows `C:\`

---

### Anonymous Collections

#### POST `/anon/collections`

Create anonymous collection (content-addressed, immutable).

**Request**: `{entries: [{path, hash}], friendly_name: "name", tags: ["tag1"]}`
**Flow**: Validate entries → JSON serialize → SHA256 hash → Write to storage → Return hash
**Security**: 
- Reject empty path, paths containing `../`, absolute paths
- Hash must be 64 hex characters

**Test**:
```bash
curl -X POST /anon/collections -H 'Content-Type: application/json' -d '{
  "entries":[{"path":"f.txt","hash":"<real_hash>"}],
  "friendly_name":"test collection"
}'
# → {"hash":"<64-hex>"}
```

#### GET `/anon/collections`

List all anonymous collections.

**Response**: `[{hash, friendly_name, name_preview, version, entry_count, tags, created_at}]`

#### GET `/anon/collections/:hash`

Get full contents of a single anonymous collection.

**Response**: `{hash, friendly_name, name_preview, entries: [{path, hash}], tags, created_at}`

#### GET `/anon/collections/:hash/*filepath`

Download a single file within a collection.

**Flow**: Load JSON by collection hash → Find entry → Get file content by hash → Stream return

#### POST `/anon/collections/fork`

Create a branch based on an existing collection.

**Request**: `{source_hash, add_entries: [], remove_paths: [], friendly_name: ""}`
**Flow**: Load source collection → Add new entries → Remove specified paths → Serialize as new collection

#### POST `/anon/collections/commit`

Submit a new version of a collection.

**Request**: `{source_hash, entries: [], commit_message: ""}`
**Flow**: Create new AnonCollection → New hash

---

### User Collections

#### POST `/collections`

Create a user collection.

**Request**: `{username, collection_name, visibility: "public"|"unlisted"|"private", tags: []}`
**Response**: `{id, username, collection_name, visibility}`

#### GET `/collections/:username`

List all collections of a user.

#### GET `/collections/:username/:collection_name`

Get collection details (with entry list).

**Response**: `{id, username, collection_name, entries: [{path, file_hash}], current_hash, visibility, tags}`

#### POST `/collections/:username/:collection_name/entries`

Add an entry to a collection.

**Request**: `{path: "dir/file.txt", hash: "<sha256>"}`
**Note**: Duplicate paths are allowed (different hashes); deduplication is at the SHA256 file level

#### DELETE `/collections/:username/:collection_name/entries/*path`

Delete a collection entry.

#### POST `/collections/:username/:collection_name/commit`

Submit a new version.

**Request**: `{commit_message: ""}`
**Flow**: Freeze current workspace as snapshot → Generate version record

#### GET `/collections/:username/:collection_name/log`

Version history.

#### POST `/collections/:username/:collection_name/rollback/:version_id`

Roll back to a specified version. **Overwrites the current workspace**.

#### GET `/collections/public` & `/collections/search`

List public collections / search collections.

---

### P2P

#### GET `/p2p/status`

P2P status summary.

**Response**:
```json
{
  "enabled": true,
  "peer_id": "12D3...",
  "addrs": ["/ip4/..."],
  "connected_count": 3,
  "discovered_count": 5,
  "relay_mode": "client",
  "hole_punch": true,
  "ws_connections": 1,
  "conn_stats": {"known_peers": 5, "successful_conns": 3, "failed_conns": 1},
  "active_transfers": [{"hash": "...", "progress": 45.2, "done": false}]
}
```

#### GET `/p2p/node`

Local node Peer ID and addresses.

#### GET `/p2p/peers` / `/p2p/discovered`

Connected/discovered node lists.

#### POST `/p2p/connect`

Manually connect to a node.

**Request**: `{addr: "/ip4/1.2.3.4/tcp/4001/p2p/12D3..."}`

#### POST `/p2p/announce`

Announce to DHT that a file is owned.

**Request**: `{hash: "<sha256>"}`

#### POST `/p2p/fetch`

Fetch a collection from the P2P network.

**Request**: `{hash: "<sha256>"}`
**Flow**: DHT lookup provider → exchange protocol to get data → Parse JSON → Return collection

#### POST `/p2p/sync`

Sync files from a specified node to local.

**Request**: `{peer_id, hash, target_dir: "/path/"}`
**Flow**: Get collection → Download all files → Write to local storage

#### POST `/p2p/push` / POST `/p2p/request-file`

Push collection / Broadcast file request.

#### GET `/ws/transfer`

WebSocket upgrade endpoint (for browser node file transfer).

**Protocol**: JSON messages + binary frames
- Client → Server: `{"type":"request","hash":"..."}`
- Server → Client: `{"type":"response","hash":"...","size":N}` + binary data

---

### Collaboration Operations

#### POST `/actions/fork`

Fork a collection to local.

**Request**: `{username, source_username, collection_name, source_coll_name}`

#### POST `/actions/merge`

Merge two collections.

**Request**: `{username, source_username, collection_name, source_coll_name, strategy: "ours"|"theirs"|"manual"}`

**Strategies**:
- `ours`: On conflict, keep local → optimistic merge
- `theirs`: On conflict, adopt remote → overwrite merge
- `manual`: On conflict, error → force manual handling

#### POST `/actions/pull`

Pull updates from upstream.

---

### Local Sync

#### POST `/local/save`

Save collection files to local disk.

**Request**: `{collection_hash, local_path, include: ["*.go"], exclude: ["node_modules"]}`

#### GET `/local/status/:hash`

Query sync status.

---

### Tasks

#### GET `/tasks` / GET `/tasks/:id`

Query asynchronous transfer task (Fork/Pull) status.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | 3000 | HTTP port |
| `PEERDRIVE_STORAGE` | ./storage | Storage directory |
| `PEERDRIVE_STORAGE_ENABLE` | true | Whether to enable storage |
| `PEERDRIVE_ALLOWED_ORIGINS` | localhost:5173,... | CORS whitelist |
| `PEERDRIVE_PUBLIC_DOMAIN` | "" | Public domain |
| `PEERDRIVE_P2P_ENABLE` | true | P2P toggle |
| `PEERDRIVE_P2P_LISTEN` | /ip4/0.0.0.0/tcp/0 | P2P listen address |
| `PEERDRIVE_BOOTSTRAP_PEER` | "" | Bootstrap node |
| `PEERDRIVE_MDNS_ENABLE` | true | mDNS |
| `PEERDRIVE_RELAY_ENABLE` | false | Relay |
| `PEERDRIVE_RELAY_MODE` | client | Relay mode |
| `PEERDRIVE_STATIC_RELAYS` | "" | Static relays |
| `PEERDRIVE_HOLE_PUNCH` | true | NAT hole punching |
| `PEERDRIVE_AUTO_NAT` | true | AutoNAT |
| `PEERDRIVE_NAT_PORTMAP` | false | NAT-PMP |

---

## Database Tables

| Table | Key Fields | Purpose |
|-------|-----------|---------|
| file_meta | hash(PK), size, filename, mime_type | File metadata |
| file_providers | hash(FK), provider_type, path, available | File storage locations |
| collections | username, collection_name, current_hash | User collections |
| collection_entries | collection_id(FK), path, file_hash | Collection workspace |
| collection_versions | id, collection_id(FK), snapshot_data, message | Version snapshots |
| version_entries | version_id(FK), path, file_hash | Version entries |
| transfer_tasks | id, type, status, params | Async tasks |
| users | username, password_hash, authkey | User authentication |
| local_collection_sync | collection_hash, local_path | Local sync |
| local_sync_files | collection hashes, file_path, is_saved | Sync file status |
