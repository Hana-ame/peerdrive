# Peerdrive Backend Documentation

## 1. Project Overview

| Attribute | Value |
|-----------|-------|
| Language | Go 1.23 |
| HTTP Framework | [Gin](https://github.com/gin-gonic/gin) v1.12.0 |
| Database | SQLite3 via [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) v1.14.42 |
| P2P | [libp2p](https://github.com/libp2p/go-libp2p) v0.48.0 with DHT, mDNS, AutoNAT, DCUtR hole-punching |
| WebSocket | [gorilla/websocket](https://github.com/gorilla/websocket) v1.5.3 |
| Swagger | [swaggo/gin-swagger](https://github.com/swaggo/gin-swagger) v1.6.1 |
| Module path | `peerdrive` |
| DB path | `./peerdrive.db` (hardcoded in `cmd/server/main.go:42`) |
| Default storage | `./storage/` (configurable via `PEERDRIVE_STORAGE`) |
| Default port | `3000` |

**Entry point**: `cmd/server/main.go:34` — `main()` initializes DB → P2P → provider manager → downloader → anon storage → router → Gin engine.

**Key packages**:

| Package | Path | Role |
|---------|------|------|
| `cmd/server` | `cmd/server/main.go` | Entry point, wires all dependencies |
| `router` | `internal/router/router.go` | Gin route registration, CORS middleware, context injection |
| `controller` | `internal/controller/*.go` | HTTP handler functions for each route group |
| `service` | `internal/service/*.go` | Business logic layer (file ops, anon collections, P2P, sync, download) |
| `repository` | `internal/repository/*.go` | SQLite data access layer, anon collection JSON storage |
| `model` | `internal/model/*.go` | Data structures (struct tags for `db` and `json`) |
| `config` | `internal/config/config.go` | Environment variable loading and defaults |
| `provider` | `internal/provider/*.go` | File storage providers (local, HTTP, manager) |
| `pkg/hashutil` | `pkg/hashutil/hashutil.go` | SHA256 validation utility |

---

## 2. API Endpoints

### 2.1 Health & File Download

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `GET` | `/ping` | `controller.Ping` | `internal/controller/ping.go:24` | — | `200 "pong"` (text/plain) |
| `GET` | `/sha256sum/:sha256` | `controller.DownloadBySHA256` | `internal/controller/download.go:25` | — | `200` binary octet-stream with `Content-Disposition` and optional `Content-Encoding: gzip`. `404` if hash not found. `400` for invalid SHA256. Also sets `X-Peerdrive-Collection: true` for anon collection type files. |

### 2.2 File Management (`/files`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `GET` | `/files` | `controller.ListFiles` | `internal/controller/file.go:215` | Query: `?sort=time\|name\|size\|type` (default: `time`). Filters `file_meta.type = 'blob'`. | `200` `[]model.FileListItem` — array of `{hash, filename, size, mime_type, created_at, type, provider_type, provider_path}` |
| `POST` | `/files/upload` | `controller.UploadFile` | `internal/controller/file.go:38` | Multipart form field `file`. Temp-file → SHA256 → storage at `{storageDir}/{hash[:2]}/{hash}`. | `201` `{hash, size, mime, filename, already_exists: false}`. `200` `{...already_exists: true}` if duplicate content. `403` if storage disabled. |
| `POST` | `/files/register_local` | `controller.RegisterLocalFile` | `internal/controller/file.go:77` | JSON: `{"path": "...", "filename": "..."}`. Path resolved against storage dir if not absolute. | `200` `{hash, filename}` |
| `POST` | `/files/register_folder` | `controller.RegisterFolder` | `internal/controller/file.go:97` | JSON: `{"folder_path": "..."}`. Recursively walks directory using `filepath.Walk`, skipping directories. | `200` `{registered: [{filename, hash}, ...]}` |
| `GET` | `/files/verify/:hash` | `controller.VerifyFile` | `internal/controller/file.go:116` | — | `200` `{hash, filename, size, mime}` or `404` `{error: "file not found"}` |
| `GET` | `/files/browse` | `controller.BrowseDir` | `internal/controller/file.go:229` | Query: `?path=...` (default: `C:\` on Windows, `/` on Linux). Uses `os.ReadDir`. | `200` `[]model.DirEntry` — array of `{name, path, is_dir, size, mod_time}` |
| `DELETE` | `/files/:hash` | `controller.DeleteFile` | `internal/controller/file.go:142` | — | `200` `{message: "deleted"}`. Removes local file + `file_providers` + `file_meta` rows. |
| `POST` | `/files/diff` | `controller.DiffVersions` | `internal/controller/file.go:158` | JSON: `{"version_a": int, "version_b": int}` | `200` `{added: [{path, hash}], removed: [{path, hash}], modified: [{path, old_hash, new_hash}]}` |

### 2.3 Anonymous Collections (`/anon`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `POST` | `/anon/collections` | `controller.CreateAnonCollection` | `internal/controller/anon.go:32` | JSON: `{"friendly_name": "...", "entries": [{"path": "...", "hash": "..."}], "tags": ["..."]}`. Validates paths for no `..` and hashes for 64-char hex. | `201` `{hash: "<sha256>"}` |
| `GET` | `/anon/collections` | `controller.ListAnonCollections` | `internal/controller/anon.go:63` | — | `200` `[]model.AnonCollectionSummary` — array of `{hash, friendly_name, name_preview, version, tags, entry_count, created_at}` |
| `POST` | `/anon/collections/commit` | `controller.CommitAnonCollection` | `internal/controller/anon.go:218` | JSON: `{"source_hash": "...", "entries": [{"path": "...", "hash": "..."}], "commit_message": "..."}`. Empty hash on an entry removes it. | `201` `{hash: "<new sha256>"}` |
| `GET` | `/anon/collections/:hash` | `controller.GetAnonCollection` | `internal/controller/anon.go:81` | — | `200` `model.AnonCollection` — full JSON with `{version, friendly_name, entries, tags, created_at}`. `404` if not found. |
| `GET` | `/anon/collections/:hash/*filepath` | `controller.DownloadAnonFile` | `internal/controller/anon.go:101` | — | `200` binary `application/octet-stream` with `Content-Disposition`. `404` if collection or entry not found. |
| `POST` | `/anon/collections/fork` | `controller.ForkAnonCollection` | `internal/controller/anon.go:153` | JSON: `{"source_hash": "...", "friendly_name": "...", "add_entries": [...], "remove_paths": [...]}` | `201` `{hash: "<new sha256>"}` |

> **Note on `:hash.json`**: The route `/anon/collections/:hash` matches path segments including `:hash.json` — Gin's `:hash` captures the full segment. The `.json` suffix variant is documented for Cloudflare cache compatibility; the handler treats the parameter identically.

### 2.4 User Collections (`/collections`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `POST` | `/collections` | `controller.CreateCollection` | `internal/controller/collection.go:49` | JSON: `{"username": "...", "collection_name": "...", "visibility": "public\|unlisted\|private", "tags": [...]}` | `200` `{id, username, collection_name, visibility}`. `409` if `UNIQUE(username, collection_name)` conflict. |
| `GET` | `/collections/public` | `controller.ListPublicCollections` | `internal/controller/collection.go:490` | Query: `?q=...` (optional). Filters `visibility = 'public'`. | `200` `{data: [...]}` — array of `model.Collection` |
| `GET` | `/collections/search` | `controller.SearchCollections` | `internal/controller/collection.go:103` | Query: `?q=...` (required). Searches by `username LIKE` or `collection_name LIKE` on public collections. | `200` `{data: [...]}` — array of `model.Collection` |
| `GET` | `/collections/:username` | `controller.ListCollections` | `internal/controller/collection.go:82` | — | `200` `{data: [...]}` — array of `model.Collection` |
| `GET` | `/collections/:username/:collection_name` | `controller.GetCollection` | `internal/controller/collection.go:130` | — | `200` `{collection: Collection, entries: [...]}`. If `current_hash` is set, entries are sourced from anon collection JSON; falls back to `collection_entries` table. `404` if not found. |
| `POST` | `/collections/:username/:collection_name/entries` | `controller.AddEntry` | `internal/controller/collection.go:182` | JSON: `{"path": "...", "hash": "..."}`. Creates collection if it doesn't exist. | `200` `{message: "entry added"}` |
| `DELETE` | `/collections/:username/:collection_name/entries/*path` | `controller.RemoveEntry` | `internal/controller/collection.go:216` | — | `200` `{message: "entry removed"}`. `404` if collection not found. |
| `POST` | `/collections/:username/:collection_name/commit` | `controller.CommitCollection` | `internal/controller/collection.go:285` | JSON: `{"commit_message": "..."}` | `200` `{message: "committed", version_number: int, snapshot_hash: "<sha256>"}`. Generates anon collection JSON, updates `current_hash`, creates version snapshot. |
| `GET` | `/collections/:username/:collection_name/log` | `controller.GetVersionLog` | `internal/controller/collection.go:368` | — | `200` `{data: [...]}` — array of `model.CollectionVersion` (newest first) |
| `POST` | `/collections/:username/:collection_name/rollback/:version_id` | `controller.RollbackCollection` | `internal/controller/collection.go:403` | — | `200` `{message: "rolled back"}`. Restores entries in a transaction, regenerates `current_hash`. |
| `POST` | `/collections/:username/:collection_name/visibility` | `controller.SetCollectionVisibility` | `internal/controller/collection.go:456` | JSON: `{"visibility": "public\|unlisted\|private"}` | `200` `{message: "ok"}` |
| `POST` | `/collections/:username/:collection_name/tags` | `controller.UpdateCollectionTags` | `internal/controller/collection.go:530` | JSON: `{"tags": ["..."]}` | `200` `{message: "ok"}` |

### 2.5 Collection File Download (public)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `GET` | `/:username/:collection_name/*filepath` | `controller.DownloadCollectionFile` | `internal/controller/collection.go:247` | — | `200` binary octet-stream via `DownloadBySHA256Internal`. Looks up path in collection entries → downloads by SHA256 hash. `404` if collection or entry not found. |

### 2.6 Actions (`/actions`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `POST` | `/actions/merge` | `controller.MergeFromSource` | `internal/controller/merge.go:38` | JSON: `{"username": "...", "collection_name": "...", "source_username": "...", "source_coll_name": "...", "strategy": "ours\|theirs\|manual"}` | `200` `{message: "merge complete", conflicts_found: int, total_entries: int}`. Strategy `manual` returns `409` with `{conflicts: [{path, local_hash, source_hash}], message}`. |
| `POST` | `/actions/fork` | `controller.ForkCollection` | `internal/controller/fork.go:32` | JSON: `{"username": "...", "collection_name": "...", "source_username": "...", "source_coll_name": "..."}` | `200` `{message: "forked", id, username, collection_name, entries_count}`. Copies all entries from source to new collection. `409` if target already exists. |
| `POST` | `/actions/pull` | `controller.PullCollection` | `internal/controller/fork.go:90` | JSON: `{"username": "...", "collection_name": "..."}` | `200` `{message: "pull not implemented (upstream sync coming in v2)"}`. Creates a no-op transfer task. |

### 2.7 P2P Endpoints (`/p2p`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `GET` | `/p2p/status` | `controller.P2PStatus` | `internal/controller/p2p.go:180` | — | `200` `{enabled, peer_id, addrs, connected_count, discovered_count, relay_mode, hole_punch, ws_connections}` |
| `GET` | `/p2p/node` | `controller.GetNodeInfo` | `internal/controller/p2p.go:23` | — | `200` `{peer_id, addrs}` |
| `GET` | `/p2p/peers` | `controller.GetPeers` | `internal/controller/p2p.go:31` | — | `200` `{peers: ["peerId1", ...]}` |
| `GET` | `/p2p/discovered` | `controller.GetDiscoveredPeers` | `internal/controller/p2p.go:40` | — | `200` `{peers: [{peer_id, addrs}, ...]}` |
| `GET` | `/p2p/ping/:peer_id` | `controller.PingPeer` | `internal/controller/p2p.go:56` | — | `200` `{peer: "<id>", rtt: "<duration>"}`. `400` if invalid peer ID. |
| `POST` | `/p2p/connect` | `controller.ConnectPeer` | `internal/controller/p2p.go:71` | JSON: `{"addr": "<multiaddr>"}` | `200` `{status: "connected"}` |
| `POST` | `/p2p/announce` | `controller.AnnounceHash` | `internal/controller/p2p.go:86` | JSON: `{"hash": "<sha256>"}` | `200` `{status: "announced"}` |
| `POST` | `/p2p/fetch` | `controller.FetchCollection` | `internal/controller/p2p.go:101` | JSON: `{"hash": "<sha256>"}` | `200` `model.AnonCollection`. `404` if not found on P2P network. 60s timeout. |
| `POST` | `/p2p/sync` | `controller.SyncFromPeer` | `internal/controller/p2p.go:122` | JSON: `{"peer_id": "...", "hash": "...", "file_hashes": ["..."], "target_dir": "..."}` | `200` `{synced: [...], count: int, saved_to: "..."}`. Fetches collection from peer if hash provided, then downloads each file. 120s timeout. |
| `POST` | `/p2p/push` | `controller.PushSync` | `internal/controller/p2p.go:198` | JSON: `{"hash": "...", "entries": [...], "target_dir": "..."}` | `200` `{entries, target_dir, message}`. Resolves entries from hash if provided. |
| `POST` | `/p2p/request-file` | `controller.RequestFile` | `internal/controller/p2p.go:249` | JSON: `{"hash": "<sha256>", "peer_ids": ["..."]}` | `200` `{hash, requested: int, responses: int, details: [{hash, error\|size}]}`. Broadcasts file request to specified peer IDs. |
| `GET` | `/p2p/ws/info` | `controller.WSInfo` | `internal/controller/p2p.go:294` | — | `200` `{ws_connections: int, ws_endpoint: "/ws/transfer", message_types: ["request","response","ping","pong"]}` |
| `GET` | `/ws/transfer` | (inline in router) | `internal/router/router.go:169` | WebSocket upgrade | WebSocket connection for bidirectional file transfer via P2P |

### 2.8 Local Sync (`/local`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `POST` | `/local/save` | `syncCtrl.SaveLocal` | `internal/controller/sync.go:19` | JSON: `{"collection_hash": "...", "local_path": "...", "include": [...], "exclude": [...]}` | `200` `{message: "collection sync started/completed successfully"}` |
| `GET` | `/local/status/:hash` | `syncCtrl.GetStatus` | `internal/controller/sync.go:39` | — | `200` `SyncStatusResponse` — `{collection_hash, local_path, total_files, saved_files, missing_files, last_synced}`. `404` if not found. |

### 2.9 Tasks (`/tasks`)

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `GET` | `/tasks` | `controller.ListTasks` | `internal/controller/task.go:56` | — | `200` `{tasks: []}` (placeholder) |
| `GET` | `/tasks/:id` | `controller.GetTaskStatus` | `internal/controller/task.go:30` | — | `200` `{task: TransferTask}`. `404` if not found. |

### 2.10 Swagger

| Method | Path | Handler | File | Request / Body | Response |
|--------|------|---------|------|----------------|----------|
| `GET` | `/swagger/*any` | gin-swagger handler | `internal/router/router.go:165` | — | Swagger UI page. Spec at `docs/swagger.json`, `docs/swagger.yaml`. |

### 2.11 Auth Endpoints

Controller file exists at `internal/controller/auth.go` with service at `internal/service/auth_service.go` and user model at `internal/model/user.go`. These are **not registered in the router** (`internal/router/router.go`) — the auth routes are currently disabled/not wired.

---

## 3. Database Schema

All tables defined in `internal/repository/db.go:32-123`. Seven core tables plus two sync tables:

### Schema DDL

```sql
-- Users (auth, currently not wired in router)
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    authkey TEXT UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Sync state tracking
CREATE TABLE IF NOT EXISTS local_collection_sync (
    collection_hash TEXT PRIMARY KEY,
    local_path TEXT NOT NULL,
    include_filter TEXT,
    exclude_filter TEXT,
    synced_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS local_sync_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collection_hash TEXT NOT NULL REFERENCES local_collection_sync(collection_hash) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    is_saved INTEGER DEFAULT 0,
    last_modified DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(collection_hash, file_path)
);

-- File metadata: one row per unique content hash
CREATE TABLE IF NOT EXISTS file_meta (
    hash TEXT PRIMARY KEY,
    size INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    mime_type TEXT DEFAULT '',
    gziped INTEGER DEFAULT 0,
    filename TEXT,
    type TEXT DEFAULT 'blob'
);

-- File storage locations: multiple providers per hash
CREATE TABLE IF NOT EXISTS file_providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL REFERENCES file_meta(hash),
    provider_type TEXT NOT NULL,
    path TEXT NOT NULL,
    available INTEGER DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_provider_hash ON file_providers(hash);

-- User collections: namespaced by username
CREATE TABLE IF NOT EXISTS collections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    current_hash TEXT DEFAULT NULL,
    tags TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(username, collection_name)
);

-- Working-copy entries for collections
CREATE TABLE IF NOT EXISTS collection_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collection_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    file_hash TEXT NOT NULL,
    FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE,
    UNIQUE(collection_id, path)
);

-- Version history: linked list via parent_version_id
CREATE TABLE IF NOT EXISTS collection_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    collection_id INTEGER NOT NULL,
    version_number INTEGER NOT NULL,
    commit_message TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    parent_version_id INTEGER,
    FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE
);

-- Snapshot of entries at a given version
CREATE TABLE IF NOT EXISTS version_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    file_hash TEXT NOT NULL,
    FOREIGN KEY (version_id) REFERENCES collection_versions(id) ON DELETE CASCADE
);

-- Async transfer tasks
CREATE TABLE IF NOT EXISTS transfer_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    params TEXT DEFAULT '',
    result TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Column Reference

#### `file_meta`

| Column | Type | Description |
|--------|------|-------------|
| `hash` | TEXT PK | SHA256 hex string (64 chars) |
| `size` | INTEGER | File size in bytes (default 0) |
| `created_at` | DATETIME | Auto-set on insert |
| `mime_type` | TEXT | MIME type (default `''`) |
| `gziped` | INTEGER | 1 if gzip-compressed (default 0) |
| `filename` | TEXT | Original or descriptive filename |
| `type` | TEXT | `'blob'` for regular files, `'anon_collection'` for collection JSON |

#### `file_providers`

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment |
| `hash` | TEXT FK | References `file_meta.hash` |
| `provider_type` | TEXT | `'local'` or other provider identifier |
| `path` | TEXT | Relative path within storage (e.g., `ab/ab123...`) or absolute path for registered files |
| `available` | INTEGER | 1 = available, 0 = unavailable |

#### `collections`

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment |
| `username` | TEXT NOT NULL | Namespace key (part of UNIQUE constraint) |
| `collection_name` | TEXT NOT NULL | Collection name (part of UNIQUE constraint) |
| `current_hash` | TEXT | SHA256 of latest commit's anon collection JSON |
| `visibility` | TEXT | `'public'`, `'unlisted'`, or `'private'` (migrated column, default `'public'`) |
| `tags` | TEXT | JSON array of tag strings (default `''`) |
| `created_at` | DATETIME | Auto-set on insert |

#### `collection_entries`

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment |
| `collection_id` | INTEGER FK | References `collections.id` (CASCADE delete) |
| `path` | TEXT | Logical file path within the collection |
| `file_hash` | TEXT | SHA256 hash of the file content |
| UNIQUE | — | `(collection_id, path)` |

#### `collection_versions`

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment |
| `collection_id` | INTEGER FK | References `collections.id` (CASCADE delete) |
| `version_number` | INTEGER | Monotonically increasing per collection |
| `commit_message` | TEXT | User-provided commit description |
| `created_at` | DATETIME | Auto-set on insert |
| `parent_version_id` | INTEGER | Links to previous version (NULL for first commit) |

#### `version_entries`

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment |
| `version_id` | INTEGER FK | References `collection_versions.id` (CASCADE delete) |
| `path` | TEXT | File path as of this version |
| `file_hash` | TEXT | SHA256 hash as of this version |

#### `transfer_tasks`

| Column | Type | Description |
|--------|------|-------------|
| `id` | INTEGER PK | Auto-increment |
| `type` | TEXT | Task type (e.g., `'pull'`) |
| `status` | TEXT | `'pending'`, `'completed'`, `'failed'` |
| `params` | TEXT | JSON parameters string |
| `result` | TEXT | JSON result string |
| `created_at` | DATETIME | Auto-set on insert |
| `updated_at` | DATETIME | Auto-updated on status change |

### Storage Layout (Anon Collections)

Anonymous collections are serialized as JSON files on disk at `{storageDir}/{hash[:2]}/{hash}`. The SHA256 hash of the JSON content is the collection identifier. Each collection is also registered as `file_meta.type = 'anon_collection'` with a `file_providers` entry pointing to the relative path. This design enables:
- Content-addressable collection identity
- P2P distribution via the same mechanisms as regular files
- `current_hash` in collections table to point to the latest version's anon collection hash

---

## 4. Models

### `FileMeta` (`internal/model/file.go:8`)

| Field | Type | DB Column | JSON | Description |
|-------|------|-----------|------|-------------|
| `Hash` | `string` | `hash` | — | SHA256 hex |
| `Size` | `int64` | `size` | — | File size in bytes |
| `CreatedAt` | `string` | `created_at` | — | ISO 8601 timestamp |
| `MimeType` | `string` | `mime_type` | — | Detected MIME type |
| `Gziped` | `bool` | `gziped` | — | Whether content is gzip-compressed |
| `Filename` | `string` | `filename` | — | Original filename |
| `Type` | `string` | `type` | — | `"blob"` or `"anon_collection"` |

### `FileProvider` (`internal/model/file.go:18`)

| Field | Type | DB Column | JSON | Description |
|-------|------|-----------|------|-------------|
| `ID` | `int` | `id` | — | Row ID |
| `Hash` | `string` | `hash` | — | SHA256 of file content |
| `ProviderType` | `string` | `provider_type` | — | `"local"` or other |
| `Path` | `string` | `path` | — | Path relative to storage or absolute |
| `Available` | `bool` | `available` | — | 1 = available |

### `FileListItem` (`internal/model/file.go:26`)

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Hash` | `string` | `hash` | SHA256 hex |
| `Filename` | `string` | `filename` | Original filename |
| `Size` | `int64` | `size` | File size |
| `MimeType` | `string` | `mime_type` | MIME type |
| `CreatedAt` | `string` | `created_at` | Timestamp |
| `Type` | `string` | `type` | `"blob"` or `"anon_collection"` |
| `ProviderType` | `string` | `provider_type` | Storage provider |
| `ProviderPath` | `string` | `provider_path` | Storage path |

### `DirEntry` (`internal/model/file.go:37`)

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Name` | `string` | `name` | Entry name |
| `Path` | `string` | `path` | Full absolute path |
| `IsDir` | `bool` | `is_dir` | Whether it's a directory |
| `Size` | `int64` | `size` | File size in bytes |
| `ModTime` | `string` | `mod_time` | ISO 8601 modification time |

### `Collection` (`internal/model/collection.go:18`)

| Field | Type | DB Column | JSON | Description |
|-------|------|-----------|------|-------------|
| `ID` | `int` | `id` | `id` | Row ID |
| `Username` | `string` | `username` | `username` | Owner namespace |
| `CollectionName` | `string` | `collection_name` | `collection_name` | Collection name |
| `CurrentHash` | `*string` | `current_hash` | `current_hash` | SHA256 of latest anon collection |
| `Visibility` | `string` | `visibility` | `visibility` | `public`/`unlisted`/`private` |
| `Tags` | `[]string` | `tags` (JSON) | `tags` | Tag array |
| `CreatedAt` | `string` | `created_at` | `created_at` | Timestamp |

### `CollectionEntry` (`internal/model/collection.go:77`)

| Field | Type | DB Column | JSON | Description |
|-------|------|-----------|------|-------------|
| `ID` | `int` | `id` | `id` | Row ID |
| `CollectionID` | `int` | `collection_id` | `collection_id` | FK to collections |
| `Path` | `string` | `path` | `path` | Logical file path |
| `FileHash` | `string` | `file_hash` | `file_hash` | SHA256 of file content |

### `CollectionVersion` (`internal/model/collection.go:84`)

| Field | Type | DB Column | JSON | Description |
|-------|------|-----------|------|-------------|
| `ID` | `int` | `id` | `id` | Row ID |
| `CollectionID` | `int` | `collection_id` | `collection_id` | FK to collections |
| `VersionNumber` | `int` | `version_number` | `version_number` | Monotonic version number |
| `CommitMessage` | `string` | `commit_message` | `commit_message` | Commit description |
| `CreatedAt` | `string` | `created_at` | `created_at` | Timestamp |
| `ParentVersionID` | `*int` | `parent_version_id` | `parent_version_id` | Links to parent version |

### `VersionEntry` (`internal/model/collection.go:93`)

| Field | Type | DB Column | JSON | Description |
|-------|------|-----------|------|-------------|
| `ID` | `int` | `id` | `id` | Row ID |
| `VersionID` | `int` | `version_id` | `version_id` | FK to collection_versions |
| `Path` | `string` | `path` | `path` | File path at this version |
| `FileHash` | `string` | `file_hash` | `file_hash` | SHA256 at this version |

### `AnonCollection` (`internal/model/anon.go:10`)

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Version` | `int` | `version` | Schema version (>= 1) |
| `FriendlyName` | `string` | `friendly_name` | Human-readable name (omitempty) |
| `Entries` | `[]AnonCollectionEntry` | `entries` | Array of path→hash mappings |
| `Tags` | `[]string` | `tags` | Tag array (omitempty) |
| `CreatedAt` | `string` | `created_at` | RFC 3339 timestamp |

### `AnonCollectionEntry` (`internal/model/anon.go:5`)

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Path` | `string` | `path` | Relative file path |
| `Hash` | `string` | `hash` | SHA256 of file content |

### `AnonCollectionSummary` (`internal/model/anon.go:41`)

| Field | Type | JSON | Description |
|-------|------|------|-------------|
| `Hash` | `string` | `hash` | SHA256 identifier |
| `FriendlyName` | `string` | `friendly_name` | Human-readable name |
| `NamePreview` | `string` | `name_preview` | First 3 entry paths |
| `Version` | `int` | `version` | Schema version |
| `Tags` | `[]string` | `tags` | Tags |
| `EntryCount` | `int` | `entry_count` | Number of entries |
| `CreatedAt` | `string` | `created_at` | Timestamp |

### Legacy / Compatibility

| Model | File | Notes |
|-------|------|-------|
| `AnonEntry` | `internal/model/anon.go:35` | Legacy type with optional `URL` field; still used by sync/collection modules |
| `TransferTask` | `internal/model/transfer_task.go:7` | `db` tags: `id, type, status, params, result, created_at, updated_at` |
| `LocalCollectionSync` | `internal/model/sync.go:5` | JSON-only model, no direct DB scan |
| `LocalSyncFile` | `internal/model/sync.go:13` | JSON-only model |
| `SaveLocalRequest` | `internal/model/sync.go:21` | Request body for `/local/save` |
| `SyncStatusResponse` | `internal/model/sync.go:28` | Response for `/local/status/:hash` |
| `User` | `internal/model/user.go` | Auth user model (not wired in router) |

---

## 5. Known Issues

### 5.1 DirEntry shows `bin` and `lib` as files (`is_dir=false`) when they are symlinks

- **Cause**: `BrowseDir` in `internal/service/file_service.go:208` uses `os.ReadDir` which returns `DirEntry.IsDir()` based on file type at the entry level. For symlinks to directories, `e.IsDir()` returns `false` because the symlink itself is not a directory, even though its target is.
- **Mitigation**: Needs `os.Lstat` or explicit symlink following to report `is_dir=true` for symlinks-to-directories.

### 5.2 CORS with AllowedOrigins

- **Where**: `internal/config/config.go:38` (`IsOriginAllowed`) and `internal/router/router.go:50-66` (CORS middleware).
- **Issue 1**: `IsOriginAllowed` uses simple string matching (`strings.EqualFold`). When `AllowedOrigins` is set to specific origins (e.g., `"http://localhost:5173"`), a request with `Origin: https://localhost:5173` (different protocol) will fail matching since there's no protocol-agnostic comparison.
- **Issue 2**: The CORS middleware in the router ignores the `IsOriginAllowed` check entirely — it always sets `Access-Control-Allow-Origin` to the request's `Origin` header or `*`. The `AllowedOrigins` configuration is not enforced at the HTTP response level.
- **Default behavior**: `AllowedOrigins` defaults to `"*"` (`PEERDRIVE_ALLOWED_ORIGINS` env var), so the issue only manifests when a specific origin list is configured.

### 5.3 BrowseDir default path

- **Where**: `internal/config/config.go:57` (`DefaultRootPath`) and `internal/controller/file.go:231`.
- **Issue**: `DefaultRootPath()` returns `"C:\\"` on Windows and `"/"` on Linux. However, `os.ReadDir("/")` on Linux lists hundreds of system entries which is rarely desired for a file-sharing app. There's no configurable default browse path — it always falls back to the OS root.

### 5.4 Gin RedirectTrailingSlash disabled

- **Where**: `internal/router/router.go:40` — `r.RedirectTrailingSlash = false`.
- **Reason**: Previously enabled, Gin would 301 redirect `/files/browse/` → `/files/browse`, causing issues with clients that don't follow redirects properly. Disabling it fixed the 301 redirect problem.

### 5.5 AnonCollection JSON must be present for listing

- **Where**: `internal/repository/anon_repo.go:79` (`ListAnonCollections`).
- **Issue**: `ListAnonCollections` queries `file_meta` for type `anon_collection`, then tries to `os.ReadFile` the JSON file from `{storageDir}/{hash[:2]}/{hash}`. If the JSON file was deleted from disk but the `file_meta` row still exists, `friendly_name`, `name_preview`, `tags`, `version`, and `entry_count` will be empty/zero in the summary response. The collection will still appear in the list but with minimal info.

### 5.6 SQLite ALTER TABLE ADD COLUMN migration

- **Where**: `internal/repository/db.go:131-133`.
- **Issue**: The migration runs `ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL` etc. every startup. While this is idempotent in standard SQLite (columns already existing cause a silent error), some SQLite versions or build configurations may fail silently or log confusing errors. The code uses `DB.Exec()` (ignoring the return value) as a workaround, but this masks other potential migration errors.

### 5.7 No authentication enforcement

- **Where**: All `/collections/` and `/actions/` endpoints.
- **Issue**: There is no authentication layer. Any HTTP client can read/write any user's collections, fork, merge, or commit. The `username` parameter is trusted from the request without verification. The `users` table and auth service (`internal/controller/auth.go`, `internal/service/auth_service.go`) exist but are **not wired** into the router. Security depends entirely on network isolation.

### 5.8 Pull not implemented

- **Where**: `internal/controller/fork.go:90` (`PullCollection`).
- **Issue**: The `/actions/pull` endpoint returns `"pull not implemented (upstream sync coming in v2)"` and creates a no-op transfer task. There is no actual upstream sync functionality.

### 5.9 ListTasks returns empty array

- **Where**: `internal/controller/task.go:56` (`ListTasks`).
- **Issue**: The `/tasks` endpoint always returns `{tasks: []}` regardless of how many tasks exist in the database. Individual task queries via `/tasks/:id` work correctly.

### 5.10 Sync repository uses `int` for `is_saved` but model uses `bool`

- **Where**: `internal/repository/sync_repo.go:48` vs `internal/model/sync.go:17`.
- **Issue**: `UpsertFileSyncState` takes `isSaved bool` and passes it directly to `DB.Exec` with `?` placeholder. SQLite accepts `bool` but stores it as `0`/`1`. The reading path correctly converts `int` to `bool`. This works but is fragile and implicit.

### 5.11 `DB.Exec` ignoring deprecated return values

- **Where**: Multiple locations in `internal/repository/db.go`, `internal/service/anon_service.go`, `internal/service/file_service.go`.
- **Issue**: Calls like `DB.Exec(`ALTER TABLE ...`)` (db.go:131-133) and `_ = repository.InsertFileMeta(...)` ignore the `sql.Result` return value, making it impossible to verify whether a migration or metadata insertion actually succeeded.

---

## 6. Configuration

All configuration is loaded from environment variables in `internal/config/config.go:64` (`Load()`).

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PORT` | `string` | `"3000"` | HTTP server listen port |
| `PEERDRIVE_STORAGE` | `string` | `"./storage"` | File storage directory for uploaded/registered files and anon collection JSON files |
| `PEERDRIVE_STORAGE_ENABLE` | `bool` | `true` | Whether file storage operations are allowed (`false` returns 403 for upload/register/delete) |
| `PEERDRIVE_ALLOWED_ORIGINS` | `string` | `"*"` | Comma-separated allowed CORS origins; `"*"` or empty = allow all |
| `PEERDRIVE_P2P_ENABLE` | `bool` | `true` | Enable libp2p node (required for P2P features) |
| `PEERDRIVE_P2P_LISTEN` | `string` | `"/ip4/0.0.0.0/tcp/0"` | P2P listen multiaddr (port 0 = random) |
| `PEERDRIVE_BOOTSTRAP_PEER` | `string` | `""` | Bootstrap peer multiaddr for DHT join |
| `PEERDRIVE_MDNS_ENABLE` | `bool` | `true` | Enable mDNS local peer discovery |
| `PEERDRIVE_RELAY_ENABLE` | `bool` | `false` | Enable circuit relay (requires relay mode set) |
| `PEERDRIVE_RELAY_MODE` | `string` | `"client"` | Relay mode: `"off"`, `"client"`, `"server"` |
| `PEERDRIVE_STATIC_RELAYS` | `string` | `""` | Comma-separated static relay multiaddrs |
| `PEERDRIVE_HOLE_PUNCH` | `bool` | `true` | Enable DCUtR hole punching for NAT traversal |
| `PEERDRIVE_PUBLIC_REACHABLE` | `bool` | `false` | Mark the node as publicly reachable (disables AutoNAT probes) |
| `PEERDRIVE_AUTO_NAT` | `bool` | `true` | Enable AutoNAT v2 for reachability detection |
| `PEERDRIVE_NAT_PORTMAP` | `bool` | `false` | Enable UPnP/NAT-PMP port mapping |

### Config struct

`internal/config/config.go:18`:

```go
type Config struct {
    Port                 string
    StorageDir           string
    StorageEnable        bool
    AllowedOrigins       string
    P2PEnable            bool
    P2PListenAddr        string
    P2PBootstrapPeer     string
    P2PMDNSEnable        bool
    P2PRelayEnable       bool
    P2PRelayMode         RelayMode
    P2PStaticRelays      string
    P2PHolePunch         bool
    P2PPublicReachable   bool
    P2PAutoNAT           bool
    P2PNATPortMap        bool
}
```

### Context Injection

`internal/router/router.go:44-48` injects the following into `gin.Context`:
- `"storageDir"` → `cfg.StorageDir` (string) — used by collection commit, rollback, anon file operations
- `"downloader"` → `*service.Downloader` — used by anon file download, collection file download

---

## 7. Build & Test

### Build

```bash
# Build server binary
go build -o peerdrive-server ./cmd/server/main.go

# Check all packages compile
go build ./...

# Run server
PORT=3000 PEERDRIVE_STORAGE=./storage go run ./cmd/server/main.go
```

### Unit Tests

```bash
# Run all unit tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific package
go test -v ./internal/config/
go test -v ./internal/service/
go test -v ./internal/repository/
go test -v ./internal/model/
go test -v ./internal/controller/
```

### Test Files

| File | Package | Description |
|------|---------|-------------|
| `internal/config/config_test.go` | `config` | Tests for env var loading, defaults, relay mode parsing, `IsOriginAllowed` |
| `internal/model/anon_test.go` | `model` | Tests for `AnonCollection` and `AnonCollectionSummary` |
| `internal/model/collection_test.go` | `model` | Tests for `Collection` scanning, `MarshalTags` |
| `internal/service/file_service_test.go` | `service` | Unit tests for file service operations |
| `internal/service/anon_service_test.go` | `service` | Unit tests for anon collection service |
| `internal/service/sync_service_test.go` | `service` | Unit tests for sync service |
| `internal/repository/file_repo_test.go` | `repository` | Unit tests for file repository |
| `internal/controller/ping_test.go` | `controller` | Tests for `/ping` handler |
| `internal/controller/collection_test.go` | `controller` | Tests for collection controller |

### Integration Tests (Shell Scripts)

| Script | Description | CI Step |
|--------|-------------|---------|
| `test.sh` | Comprehensive integration test: health, upload, register, collections, versioning, fork, merge, pull, delete, tasks | Not in CI directly |
| `test/p2p.sh` | Multi-instance P2P discovery, connection, collection exchange, file sync | CI step `Run P2P integration test` |
| `test/anon-collection.sh` | Anonymous collection CRUD, fork, download, path validation | CI step `Run anon collection test` |
| `test/upload.sh` | File upload with duplicate detection, storage enable/disable | CI step `Run file upload test` |
| `test/register.sh` | Local file/folder registration, verify, delete | CI step `Run register test` |
| `test/relay.sh` | Relay server + client, hole punching, WS transfer, request broadcast | CI step `Run relay + NAT traversal test` |
| `test/e2e-all.sh` | End-to-end test runner for all test suites | Manual execution |

### CI Pipeline

Defined in `.github/workflows/ci.yml`. Runs on `ubuntu-latest` with Go 1.23:
1. `go mod download`
2. `go build -o peerdrive-server ./cmd/server/main.go`
3. `bash test/p2p.sh` (P2P integration)
4. Start server → `bash test/anon-collection.sh` → kill server
5. Start server → `bash test/upload.sh` → kill server
6. Start server → `bash test/register.sh` → kill server
7. `bash test/relay.sh` (relay + NAT traversal)
8. Generate test report artifact

---

## 8. Directory Structure

```
go/
├── cmd/server/main.go              # Entry point
├── internal/
│   ├── config/config.go            # Configuration loading
│   ├── config/config_test.go       # Config unit tests
│   ├── controller/
│   │   ├── anon.go                 # Anonymous collection handlers
│   │   ├── auth.go                 # Auth handlers (not wired)
│   │   ├── collection.go           # User collection handlers
│   │   ├── collection_test.go      # Collection controller tests
│   │   ├── download.go             # SHA256 download handler
│   │   ├── file.go                 # File upload/register/verify/delete/browse
│   │   ├── fork.go                 # Fork and pull handlers
│   │   ├── merge.go                # Merge handler
│   │   ├── p2p.go                  # P2P handlers
│   │   ├── ping.go                 # Health check handler
│   │   ├── ping_test.go            # Ping handler test
│   │   ├── sync.go                 # Local sync handlers
│   │   └── task.go                 # Task status handlers
│   ├── model/
│   │   ├── anon.go                 # AnonCollection, AnonCollectionEntry, AnonCollectionSummary
│   │   ├── anon_test.go            # Anon model tests
│   │   ├── collection.go           # Collection, CollectionEntry, CollectionVersion, VersionEntry
│   │   ├── collection_test.go      # Collection model tests
│   │   ├── file.go                 # FileMeta, FileProvider, FileListItem, DirEntry
│   │   ├── sync.go                 # LocalCollectionSync, LocalSyncFile, SaveLocalRequest, SyncStatusResponse
│   │   ├── transfer_task.go        # TransferTask
│   │   └── user.go                 # User (auth, not wired)
│   ├── provider/
│   │   ├── http.go                 # HTTP file provider
│   │   ├── local.go                # Local filesystem provider
│   │   ├── manager.go              # Provider manager (orchestrates multiple providers)
│   │   └── provider.go             # Provider interface
│   ├── repository/
│   │   ├── anon_repo.go            # Anon collection JSON storage + file_meta registration
│   │   ├── collection_repo.go      # Collections CRUD + versions + version entries
│   │   ├── db.go                   # InitDB, schema, migrations, global DB var
│   │   ├── file_repo.go            # file_meta + file_providers CRUD, ListAllFiles
│   │   ├── file_repo_test.go       # File repo tests
│   │   ├── sync_repo.go            # Local sync state persistence
│   │   ├── task_repo.go            # Transfer task CRUD
│   │   └── user_repo.go            # User CRUD (auth, not wired in router)
│   ├── router/
│   │   └── router.go               # Route registration, CORS middleware, context injection
│   └── service/
│       ├── anon_service.go         # Anon collection business logic
│       ├── anon_service_test.go    # Anon service tests
│       ├── auth_service.go         # Auth business logic (not wired)
│       ├── downloader.go           # Content-addressed file stream resolver
│       ├── file_service.go         # File upload/register/verify/delete/browse logic
│       ├── file_service_test.go    # File service tests
│       ├── p2p.go                  # P2P service (libp2p host, DHT, protocols)
│       ├── p2p_helpers.go          # P2P helper functions
│       ├── p2p_ws.go               # WebSocket transfer handler
│       ├── sync_service.go         # Sync service
│       └── sync_service_test.go    # Sync service tests
├── pkg/hashutil/hashutil.go        # SHA256 validation utility
├── docs/
│   ├── swagger.json                # Swagger spec (JSON)
│   ├── swagger.yaml                # Swagger spec (YAML)
│   └── docs.go                     # Auto-generated Swagger docs
├── test/
│   ├── test.sh                     # Main integration test
│   ├── p2p.sh                      # P2P integration test
│   ├── anon-collection.sh          # Anon collection test
│   ├── upload.sh                   # File upload test
│   ├── register.sh                 # File registration test
│   ├── relay.sh                    # Relay/NAT traversal test
│   └── e2e-all.sh                  # E2E runner
├── go.mod                          # Module definition (module peerdrive, go 1.26.2 toolchain)
├── go.sum                          # Dependency checksums
├── .github/workflows/ci.yml        # GitHub Actions CI
└── peerdrive.db                    # SQLite database (runtime artifact)
```
