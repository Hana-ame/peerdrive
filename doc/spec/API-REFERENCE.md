# Peerdrive API Reference

**Base URL**: `http://127.0.0.1:3000`

Peerdrive is a P2P file-sharing server with content-addressable storage, BitTorrent DHT (32 nodes), IPFS compatibility, collection management, versioning, and collaboration (merge/fork/pull).

---

## Table of Contents

1. [System](#1-system)
2. [File Management](#2-file-management)
3. [Downloads](#3-downloads)
4. [Anonymous Collections](#4-anonymous-collections)
5. [User Collections](#5-user-collections)
6. [Collaboration Actions](#6-collaboration-actions)
7. [P2P Network](#7-p2p-network)
8. [BitTorrent DHT](#8-bitTorrent-dht)
9. [Dual-Stack P2P](#9-dual-stack-p2p)
10. [Port Forwarding](#10-port-forwarding)
11. [IPFS Compat](#11-ipfs-compat)
12. [Authentication](#12-authentication)
13. [Share Links](#13-share-links)
14. [Tasks](#14-tasks)
15. [Local Sync](#15-local-sync)
16. [WebDAV](#16-webdav)
17. [WebRTC & Signaling](#17-webrtc--signaling)
18. [Relay Proxy](#18-relay-proxy)
19. [Swagger UI](#19-swagger-ui)

---

## 1. System

### `GET /ping`
Health check. Returns `"pong"` when the server is running.

```bash
curl http://127.0.0.1:3000/ping
```

**Response** (text/plain):
```
pong
```

---

## 2. File Management

### `GET /files`
List all registered files.

```bash
curl 'http://127.0.0.1:3000/files?sort=time'
```

**Query Parameters**:
- `sort` (optional): Sort field — `time` | `name` | `path` | `type` | `size`. Default: `time`.

**Response** (JSON array):
```json
[
  {
    "hash": "abc123...",
    "filename": "photo.jpg",
    "size": 1048576,
    "mime_type": "image/jpeg",
    "created_at": "2026-04-28T12:00:00Z",
    "type": "blob",
    "provider_type": "local",
    "provider_path": "ab/abc123..."
  }
]
```

**Fields**:
- `hash`: 64-character SHA256 hash, unique file identifier
- `filename`: Original filename at upload time
- `size`: File size in bytes
- `mime_type`: Auto-detected MIME type
- `created_at`: ISO 8601 timestamp
- `type`: File type (`blob`, `anon_collection`, etc.)
- `provider_type`: Storage provider (`local`, `http`, `p2p`)
- `provider_path`: Provider-specific path (relative storage path, URL, etc.)

---

### `POST /files/upload`
Upload a file. Returns the SHA256 hash. Duplicate uploads return the existing hash with `already_exists: true`.

**Request**: `multipart/form-data`, field `file`

```bash
curl -X POST http://127.0.0.1:3000/files/upload -F "file=@document.pdf"
```

**Response** (201 Created):
```json
{
  "hash": "abc123...",
  "filename": "document.pdf",
  "size": 204800,
  "mime": "application/pdf",
  "already_exists": false
}
```

**Fields**:
- `hash`: 64-character SHA256 hash, the file's unique content address
- `filename`: Original filename
- `size`: File size in bytes
- `mime`: Auto-detected MIME type
- `already_exists`: `true` if the file content was already stored (duplicate upload)

---

### `POST /files/register_local`
Register a local file by path. Computes SHA256 and records metadata without uploading.

```bash
curl -X POST http://127.0.0.1:3000/files/register_local \
  -H "Content-Type: application/json" \
  -d '{"path": "/home/user/data.csv", "filename": "data.csv"}'
```

**Request Body**:
- `path` (required): Absolute or relative path to the file
- `filename` (optional): Override display name

**Response**:
```json
{
  "hash": "abc123...",
  "filename": "data.csv"
}
```

**Fields**:
- `hash`: 64-character SHA256 hash of the file
- `filename`: Display filename

---

### `POST /files/register_folder`
Recursively register all files in a folder. Returns an array of `{filename, hash}` for each file.

```bash
curl -X POST http://127.0.0.1:3000/files/register_folder \
  -H "Content-Type: application/json" \
  -d '{"folder_path": "/home/user/photos"}'
```

**Request Body**:
- `folder_path` (required): Path to the folder

**Response**:
```json
{
  "registered": [
    {"filename": "img001.jpg", "hash": "abc..."},
    {"filename": "img002.jpg", "hash": "def..."}
  ]
}
```

**Fields**:
- `registered`: Array of objects, each with `filename` and `hash`

---

### `POST /files/register_url`
Register a file from a URL. Fetches the remote file, computes SHA256, and stores it. Follows HTTP redirects.

```bash
curl -X POST http://127.0.0.1:3000/files/register_url \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com/image.png", "filename": "remote-image.png"}'
```

**Request Body**:
- `url` (required): Source URL
- `filename` (optional): Override display name (auto-derived from Content-Disposition or URL if omitted)

**Response** (201 Created):
```json
{
  "hash": "abc123...",
  "size": 65536,
  "mime": "image/png",
  "filename": "remote-image.png"
}
```

**Fields**:
- `hash`: 64-character SHA256 hash
- `size`: File size in bytes
- `mime`: Detected MIME type
- `filename`: Display filename

---

### `GET /files/verify/:hash`
Verify a file exists by its SHA256 hash. Returns metadata if found.

```bash
curl http://127.0.0.1:3000/files/verify/abc123...
```

**Response** (200 OK):
```json
{
  "hash": "abc123...",
  "filename": "document.pdf",
  "size": 204800,
  "mime": "application/pdf"
}
```

**Fields**:
- `hash`: 64-character SHA256 hash
- `filename`: Registered filename
- `size`: File size in bytes
- `mime`: MIME type

**404 Not Found**:
```json
{"error": "file not found"}
```

---

### `GET /files/browse`
Browse the server's file system (relative to storage directory).

```bash
curl 'http://127.0.0.1:3000/files/browse?path=/'
```

**Query Parameters**:
- `path` (optional): Directory path. Default: root (`/`).

**Response**:
```json
[
  {
    "name": "ab",
    "path": "/mnt/d/WorkPlace/peerdrive/storage/ab",
    "is_dir": true,
    "size": 4096,
    "mod_time": "2026-04-28T12:00:00Z"
  },
  {
    "name": "abc123...",
    "path": "/mnt/d/WorkPlace/peerdrive/storage/ab/abc123...",
    "is_dir": false,
    "size": 1048576,
    "mod_time": "2026-04-28T12:00:00Z"
  }
]
```

**Fields** (per entry):
- `name`: Entry name (filename or directory name)
- `path`: Absolute path on the server
- `is_dir`: Whether this entry is a directory
- `size`: Size in bytes (4096 for directories)
- `mod_time`: Last modification time in ISO 8601

---

### `DELETE /files/:hash`
Delete a file by its SHA256 hash. Removes local storage, metadata, and provider records.

```bash
curl -X DELETE http://127.0.0.1:3000/files/abc123...
```

**Response**:
```json
{"message": "deleted"}
```

---

### `POST /files/copy`
Copy a file within storage (similar to a WebDAV MOVE equivalent). Creates a duplicate at `dest_path`.

```bash
curl -X POST http://127.0.0.1:3000/files/copy \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123...", "dest_path": "backup/copy_of_document.pdf"}'
```

**Request Body**:
- `hash` (required): SHA256 hash of source file
- `dest_path` (required): Destination path within storage

**Response** (201 Created):
```json
{
  "hash": "abc123...",
  "dest_path": "backup/copy_of_document.pdf"
}
```

**Fields**:
- `hash`: SHA256 hash of the copied file (same as source)
- `dest_path`: The destination path that was created

---

### `POST /files/diff`
Compare two committed versions and return added, removed, and modified entries.

```bash
curl -X POST http://127.0.0.1:3000/files/diff \
  -H "Content-Type: application/json" \
  -d '{"version_a": 1, "version_b": 2}'
```

**Request Body**:
- `version_a` (required): Source version ID
- `version_b` (required): Target version ID

**Response**:
```json
{
  "added": [
    {"path": "new_file.txt", "hash": "abc..."}
  ],
  "removed": [
    {"path": "old_file.txt", "hash": "def..."}
  ],
  "modified": [
    {"path": "changed_file.txt", "old_hash": "ghi...", "new_hash": "jkl..."}
  ]
}
```

**Fields**:
- `added`: Array of `{path, hash}` for entries present in B but not A
- `removed`: Array of `{path, hash}` for entries present in A but not B
- `modified`: Array of `{path, old_hash, new_hash}` for entries whose hash changed between A and B

---

## 3. Downloads

### `GET /sha256sum/:hash`
Download a file by SHA256 hash. Uses multi-protocol resolution (local, P2P, IPFS gateways) automatically.

```bash
curl -o document.pdf http://127.0.0.1:3000/sha256sum/abc123...
```

**Headers**:
- `X-Protocol`: Indicates which protocol served the data (`local`, `p2p`, `ipfs`, `http`, `bt`)
- `X-Peerdrive-Collection`: `true` if the file is an anonymous collection JSON
- `Content-Encoding`: `gzip` if the stored data is gzip-compressed
- `Content-Disposition`: `attachment; filename=<filename>` (use `?inline=1` for inline disposition)

**Query Parameters**:
- `inline=1`: Serve with `inline` Content-Disposition instead of `attachment`

**Response**: Binary file stream (application/octet-stream).

**Range Requests**: Supports `Range` header for partial content downloads (HTTP 206).

---

### `GET /ipfs/:cid`
Download a file by IPFS CID. Tries local storage first, then falls back to public IPFS gateways. On successful gateway fetch, the file is cached locally.

```bash
curl -o photo.jpg http://127.0.0.1:3000/ipfs/QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco
```

**Response Headers**:
- `X-CID`: The requested CID

**Response**: Binary file stream (application/octet-stream).

---

### `GET /download/:hash`
Universal multi-protocol download. Similar to `/sha256sum/:hash` but without Content-Disposition headers.

```bash
curl -o data.bin http://127.0.0.1:3000/download/abc123...
```

**Response Headers**:
- `X-Protocol`: The protocol that served the data

**Response**: Binary file stream.

---

### `GET /download/:hash/sources`
List all available protocol sources for a given hash.

```bash
curl http://127.0.0.1:3000/download/abc123.../sources
```

**Response**:
```json
[
  {"network": "local", "available": true},
  {"network": "p2p", "available": true, "peers": 3},
  {"network": "bt", "available": false},
  {"network": "ipfs", "available": true, "cid": "Qm..."}
]
```

**Fields** (per source):
- `network`: Protocol name (`local`, `p2p`, `bt`, `ipfs`, `http`)
- `available`: Whether the source is currently reachable
- `peers` / `cid` / extra fields: Protocol-specific details

---

### `POST /download/:hash/refresh`
Clear local cache for a hash and re-run the download pipeline from scratch.

```bash
curl -X POST http://127.0.0.1:3000/download/abc123.../refresh
```

**Response**: Binary file stream with `X-Protocol` header.

---

### `POST /p2p/download/resume`
Start or resume a download. Supports pause/resume via SQLite-backed progress tracking.

```bash
curl -X POST http://127.0.0.1:3000/p2p/download/resume \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123...", "peer_id": "12D3KooW..."}'
```

**Request Body** (JSON):
```json
{
  "hash": "abc123...",
  "peer_id": "12D3KooW..."
}
```

**Response** (JSON):
```json
{
  "hash": "abc123...",
  "status": "resumed",
  "progress": 45.2,
  "bytes_downloaded": 473920,
  "total_bytes": 1048576,
  "peer_id": "12D3KooW..."
}
```

| Field | Type | Description |
|-------|------|-------------|
| `hash` | string | 64-character SHA256 hash |
| `status` | string | `resumed` / `new` / `completed` |
| `progress` | float | Download progress percentage (0-100) |
| `bytes_downloaded` | int | Bytes downloaded so far |
| `total_bytes` | int | Total file size in bytes |
| `peer_id` | string | Peer ID providing the file |

---

### `GET /p2p/download/progress/:hash`
Query download progress for a specific hash.

```bash
curl http://127.0.0.1:3000/p2p/download/progress/abc123...
```

**Response** (JSON):
```json
{
  "hash": "abc123...",
  "status": "downloading",
  "progress": 67.8,
  "bytes_downloaded": 710934,
  "total_bytes": 1048576,
  "peers_connected": 2,
  "speed_bps": 524288
}
```

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `queued` / `downloading` / `paused` / `completed` / `failed` |
| `peers_connected` | int | Number of active peer connections |
| `speed_bps` | int | Current download speed in bytes/sec |

---

### `POST /p2p/download/cancel/:hash`
Cancel an active or paused download.

```bash
curl -X POST http://127.0.0.1:3000/p2p/download/cancel/abc123...
```

**Response** (JSON):
```json
{
  "hash": "abc123...",
  "status": "cancelled",
  "message": "download cancelled"
}
```

---

### `POST /p2p/download/multipeer`
Start a multi-peer parallel download for maximum speed.

```bash
curl -X POST http://127.0.0.1:3000/p2p/download/multipeer \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123...", "peer_ids": ["12D3A...", "12D3B..."]}'
```

**Request Body** (JSON):
```json
{
  "hash": "abc123...",
  "peer_ids": ["12D3KooWA...", "12D3KooWB..."]
}
```

**Response** (JSON):
```json
{
  "hash": "abc123...",
  "status": "downloading",
  "active_sources": 3,
  "total_sources": 5,
  "progress": 12.0,
  "bytes_downloaded": 125829,
  "total_bytes": 1048576
}
```

| Field | Type | Description |
|-------|------|-------------|
| `active_sources` | int | Number of peers actively transferring |
| `total_sources` | int | Total available peers for this hash |

---

### `GET /p2p/download/sources/:hash`
List available download sources (peers) for a file hash.

```bash
curl http://127.0.0.1:3000/p2p/download/sources/abc123...
```

**Response** (JSON):
```json
{
  "hash": "abc123...",
  "sources": [
    {
      "peer_id": "12D3KooWA...",
      "addrs": ["/ip4/192.168.1.5/tcp/4001", "/ip4/10.0.0.3/tcp/4001"],
      "protocol": "libp2p",
      "latency_ms": 12,
      "connected": true
    },
    {
      "peer_id": "",
      "addrs": ["https://ipfs.io/ipfs/QmUNLLsP..."],
      "protocol": "ipfs-gateway",
      "latency_ms": 371,
      "connected": false
    }
  ],
  "count": 2
}
```

---

### `GET /p2p/download/multipeer/progress/:hash`
Query multi-peer download progress.

```bash
curl http://127.0.0.1:3000/p2p/download/multipeer/progress/abc123...
```

**Response** (JSON):
```json
{
  "hash": "abc123...",
  "status": "downloading",
  "progress": 88.5,
  "bytes_downloaded": 927989,
  "total_bytes": 1048576,
  "sources": [
    {"peer_id": "12D3A...", "progress": 100, "bytes": 524288, "status": "completed"},
    {"peer_id": "12D3B...", "progress": 77, "bytes": 403701, "status": "downloading"}
  ],
  "speed_bps": 1048576
}
```

---

### `GET /:username/:collection_name/*filepath`
Download a file from within a user collection by username, collection name, and file path.

```bash
curl -o doc.txt http://127.0.0.1:3000/alice/my-collection/docs/readme.txt
```

**Response**: Binary file stream. Internally resolves to `/sha256sum/:hash`.

---

## 4. Anonymous Collections

Anonymous collections are immutable, content-addressed collections of `{path, hash}` entries, identified by their SHA256 hash.

### `POST /anon/collections`
Create an immutable anonymous collection.

```bash
curl -X POST http://127.0.0.1:3000/anon/collections \
  -H "Content-Type: application/json" \
  -d '{
    "friendly_name": "My Photos",
    "entries": [
      {"path": "vacation/beach.jpg", "hash": "abc123..."},
      {"path": "vacation/sunset.jpg", "hash": "def456..."}
    ],
    "tags": ["photos", "vacation"]
  }'
```

**Request Body**:
- `friendly_name` (optional): Human-readable name
- `entries` (required): Array of `{path, hash}` mappings. `path` is the logical file path within the collection, `hash` is the 64-char SHA256.
- `tags` (optional): Array of string tags

**Response** (201 Created):
```json
{"hash": "collection-sha256-hash..."}
```

**Fields**:
- `hash`: 64-character SHA256 hash that uniquely identifies this collection (computed from its content)

---

### `GET /anon/collections`
List all anonymous collections stored on this node.

```bash
curl http://127.0.0.1:3000/anon/collections
```

**Response**:
```json
[
  {
    "hash": "abc...",
    "friendly_name": "My Photos",
    "name_preview": "My Photos",
    "version": 1,
    "tags": ["photos", "vacation"],
    "entry_count": 2,
    "created_at": "2026-04-28T12:00:00Z"
  }
]
```

**Fields** (per entry):
- `hash`: Collection SHA256 hash
- `friendly_name`: Human-readable name
- `name_preview`: Short preview of the name
- `version`: Version number (starts at 1)
- `tags`: Array of tags
- `entry_count`: Number of file entries
- `created_at`: ISO 8601 creation timestamp

---

### `GET /anon/collections/:hash`
Retrieve an anonymous collection's metadata and entries.

```bash
curl http://127.0.0.1:3000/anon/collections/abc123...
```

**Response**:
```json
{
  "version": 1,
  "friendly_name": "My Photos",
  "entries": [
    {"path": "vacation/beach.jpg", "hash": "abc123..."},
    {"path": "vacation/sunset.jpg", "hash": "def456..."}
  ],
  "tags": ["photos", "vacation"],
  "created_at": "2026-04-28T12:00:00Z"
}
```

**Fields**:
- `version`: Version number
- `friendly_name`: Human-readable name
- `entries`: Array of `{path, hash}` mappings
- `tags`: Array of tags
- `created_at`: ISO 8601 creation timestamp

---

### `GET /anon/collections/:hash/*filepath`
Download a specific file from an anonymous collection by collection hash and file path.

```bash
curl -o beach.jpg http://127.0.0.1:3000/anon/collections/abc123.../vacation/beach.jpg
```

**Query Parameters**:
- `inline=1`: Serve with inline Content-Disposition instead of attachment

**Response**: Binary file stream.

---

### `POST /anon/collections/fork`
Create a new anonymous collection as a variant of an existing one, with added and/or removed entries.

```bash
curl -X POST http://127.0.0.1:3000/anon/collections/fork \
  -H "Content-Type: application/json" \
  -d '{
    "source_hash": "abc123...",
    "friendly_name": "Edited Photos",
    "add_entries": [
      {"path": "vacation/edited_beach.jpg", "hash": "ghi789..."}
    ],
    "remove_paths": ["vacation/old_photo.jpg"]
  }'
```

**Request Body**:
- `source_hash` (required): Hash of the source collection to fork
- `friendly_name` (optional): Name for the new collection
- `add_entries` (optional): Array of `{path, hash}` to add or replace
- `remove_paths` (optional): Array of path strings to remove

**Response** (201 Created):
```json
{"hash": "new-collection-hash..."}
```

**Fields**:
- `hash`: SHA256 hash of the newly created fork

---

### `POST /anon/collections/commit`
Commit modifications to an existing anonymous collection. Creates a new version with a content hash.

```bash
curl -X POST http://127.0.0.1:3000/anon/collections/commit \
  -H "Content-Type: application/json" \
  -d '{
    "source_hash": "abc123...",
    "entries": [
      {"path": "readme.md", "hash": "abc123..."},
      {"path": "old_deleted_file.md", "hash": ""}
    ],
    "commit_message": "Updated readme, removed old file"
  }'
```

**Request Body**:
- `source_hash` (required): Hash of the collection to update
- `entries` (required): Array of `{path, hash}`. Entries with empty `hash` are removed.
- `commit_message` (optional): Commit message

**Response** (201 Created):
```json
{"hash": "new-collection-hash..."}
```

---

## 5. User Collections

User collections are named, versioned `path->hash` mappings owned by a username. They support commit/rollback/log, visibility control, and tagging.

### `POST /collections`
Create a new named collection for a user.

```bash
curl -X POST http://127.0.0.1:3000/collections \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice",
    "collection_name": "my-documents",
    "visibility": "public",
    "tags": ["docs", "work"]
  }'
```

**Request Body**:
- `username` (required): Owner username
- `collection_name` (required): Collection name
- `visibility` (optional): `public` | `unlisted` | `private`. Default is `private`.
- `follow_redirects` (optional): Boolean, whether to follow redirects for collection entries
- `tags` (optional): Array of string tags

**Response**:
```json
{
  "id": 1,
  "username": "alice",
  "collection_name": "my-documents",
  "visibility": "public"
}
```

**Fields**:
- `id`: Auto-increment collection ID
- `username`: Owner username
- `collection_name`: Collection name
- `visibility`: Visibility setting

---

### `GET /collections/:username`
List all collections owned by a username.

```bash
curl http://127.0.0.1:3000/collections/alice
```

**Response**:
```json
{
  "data": [
    {
      "id": 1,
      "username": "alice",
      "collection_name": "my-documents",
      "current_hash": "abc123...",
      "visibility": "public",
      "follow_redirects": false,
      "tags": ["docs", "work"],
      "created_at": "2026-04-28T12:00:00Z"
    }
  ]
}
```

**Fields** (per collection):
- `id`: Collection ID
- `username`: Owner username
- `collection_name`: Collection name
- `current_hash`: Hash of the latest committed snapshot
- `visibility`: Visibility setting
- `follow_redirects`: Whether redirects are followed
- `tags`: Array of tags
- `created_at`: ISO 8601 creation timestamp

---

### `GET /collections/:username/:coll`
Get collection details with all entries.

```bash
curl http://127.0.0.1:3000/collections/alice/my-documents
```

**Response**:
```json
{
  "collection": {
    "id": 1,
    "username": "alice",
    "collection_name": "my-documents",
    "current_hash": "abc123...",
    "visibility": "public",
    "follow_redirects": false,
    "tags": ["docs", "work"],
    "created_at": "2026-04-28T12:00:00Z"
  },
  "entries": [
    {"id": 1, "collection_id": 1, "path": "readme.md", "file_hash": "abc123..."},
    {"id": 2, "collection_id": 1, "path": "notes.txt", "file_hash": "def456..."}
  ]
}
```

**Fields**:
- `collection`: Collection metadata
- `entries`: Array of entry `{id, collection_id, path, file_hash}` mappings

---

### `POST /collections/:username/:coll/entries`
Add a `path -> hash` entry to a collection. Creates the collection if it doesn't exist.

```bash
curl -X POST http://127.0.0.1:3000/collections/alice/my-documents/entries \
  -H "Content-Type: application/json" \
  -d '{"path": "docs/readme.md", "hash": "abc123..."}'
```

**Request Body**:
- `path` (required): File path within the collection
- `hash` (required): 64-character SHA256 hash

**Response**:
```json
{"message": "entry added"}
```

---

### `DELETE /collections/:username/:coll/entries/*path`
Remove a `path -> hash` entry from a collection.

```bash
curl -X DELETE http://127.0.0.1:3000/collections/alice/my-documents/entries/docs/readme.md
```

**Response**:
```json
{"message": "entry removed"}
```

---

### `POST /collections/:username/:coll/commit`
Snapshot all current entries as a new version with a commit message. Generates an anonymous collection JSON snapshot and updates `current_hash`.

```bash
curl -X POST http://127.0.0.1:3000/collections/alice/my-documents/commit \
  -H "Content-Type: application/json" \
  -d '{"commit_message": "Initial version"}'
```

**Request Body**:
- `commit_message` (optional): Commit message

**Response**:
```json
{
  "message": "committed",
  "version_number": 1,
  "snapshot_hash": "abc123..."
}
```

**Fields**:
- `version_number`: Auto-incrementing version number
- `snapshot_hash`: SHA256 hash of the anonymous collection JSON snapshot

---

### `GET /collections/:username/:coll/log`
Get version history, newest first.

```bash
curl http://127.0.0.1:3000/collections/alice/my-documents/log
```

**Response**:
```json
{
  "data": [
    {
      "id": 2,
      "collection_id": 1,
      "version_number": 2,
      "commit_message": "Added new files",
      "created_at": "2026-04-28T13:00:00Z",
      "parent_version_id": 1
    },
    {
      "id": 1,
      "collection_id": 1,
      "version_number": 1,
      "commit_message": "Initial version",
      "created_at": "2026-04-28T12:00:00Z",
      "parent_version_id": null
    }
  ]
}
```

**Fields** (per version):
- `id`: Version ID
- `collection_id`: Parent collection ID
- `version_number`: Auto-incrementing version number
- `commit_message`: Commit message
- `created_at`: ISO 8601 commit timestamp
- `parent_version_id`: Previous version ID (null for first version)

---

### `POST /collections/:username/:coll/rollback/:vid`
Rollback collection entries to a specific version. Replaces all current entries with that version's snapshot.

```bash
curl -X POST http://127.0.0.1:3000/collections/alice/my-documents/rollback/1
```

**URL Parameters**:
- `vid`: Version ID to restore

**Response**:
```json
{"message": "rolled back"}
```

---

### `POST /collections/:username/:coll/visibility`
Set a collection's visibility.

```bash
curl -X POST http://127.0.0.1:3000/collections/alice/my-documents/visibility \
  -H "Content-Type: application/json" \
  -d '{"visibility": "public"}'
```

**Request Body**:
- `visibility` (required): One of `public`, `unlisted`, `private`

**Response**:
```json
{"message": "ok"}
```

---

### `POST /collections/:username/:coll/tags`
Replace the tag list for a collection.

```bash
curl -X POST http://127.0.0.1:3000/collections/alice/my-documents/tags \
  -H "Content-Type: application/json" \
  -d '{"tags": ["important", "archive"]}'
```

**Request Body**:
- `tags` (required): Array of string tags

**Response**:
```json
{"message": "ok"}
```

---

### `GET /collections/public`
List all public collections. Optionally filtered by search query.

```bash
curl 'http://127.0.0.1:3000/collections/public?q=documents'
```

**Query Parameters**:
- `q` (optional): Search query matched against `username` and `collection_name`

**Response**:
```json
{
  "data": [
    {
      "id": 1,
      "username": "alice",
      "collection_name": "my-documents",
      "current_hash": "abc...",
      "visibility": "public",
      "follow_redirects": false,
      "tags": ["docs"],
      "created_at": "2026-04-28T12:00:00Z"
    }
  ]
}
```

---

### `GET /collections/search`
Search collections by username or collection name.

```bash
curl 'http://127.0.0.1:3000/collections/search?q=alice'
```

**Query Parameters**:
- `q` (required): Search query

**Response**:
```json
{
  "data": [
    {
      "id": 1,
      "username": "alice",
      "collection_name": "my-documents",
      "current_hash": "abc...",
      "visibility": "public",
      "follow_redirects": false,
      "tags": ["docs"],
      "created_at": "2026-04-28T12:00:00Z"
    }
  ]
}
```

---

## 6. Collaboration Actions

### `POST /actions/merge`
Merge a source collection into the local collection. Supports three conflict resolution strategies.

```bash
curl -X POST http://127.0.0.1:3000/actions/merge \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice",
    "collection_name": "my-docs",
    "source_username": "bob",
    "source_coll_name": "shared-docs",
    "strategy": "theirs"
  }'
```

**Request Body**:
- `username` (required): Local collection owner
- `collection_name` (required): Local collection name
- `source_username` (required): Source collection owner
- `source_coll_name` (required): Source collection name
- `strategy` (required): One of:
  - `"ours"` — Keep local hash on conflict
  - `"theirs"` — Accept source hash on conflict
  - `"manual"` — Return 409 with conflict list; resolve and re-merge

**Response** (200 OK):
```json
{
  "message": "merge complete",
  "conflicts_found": 0,
  "total_entries": 15
}
```

**409 Conflict** (when strategy=manual):
```json
{
  "conflicts": [
    {
      "path": "shared_file.txt",
      "local_hash": "abc...",
      "source_hash": "def..."
    }
  ],
  "message": "resolve conflicts and re-merge with strategy=ours or theirs"
}
```

---

### `POST /actions/fork`
Copy all entries from a source collection into a new collection owned by a different user.

```bash
curl -X POST http://127.0.0.1:3000/actions/fork \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice",
    "collection_name": "forked-docs",
    "source_username": "bob",
    "source_coll_name": "shared-docs"
  }'
```

**Request Body**:
- `username` (required): Owner of the new collection
- `collection_name` (required): Name of the new collection
- `source_username` (required): Owner of the source collection
- `source_coll_name` (required): Name of the source collection

**Response**:
```json
{
  "message": "forked",
  "id": 2,
  "username": "alice",
  "collection_name": "forked-docs",
  "entries_count": 10
}
```

**Fields**:
- `id`: ID of the newly created collection
- `entries_count`: Number of entries copied

---

### `POST /actions/pull`
Placeholder for syncing upstream changes from a forked source. Currently creates a no-op completed task.

```bash
curl -X POST http://127.0.0.1:3000/actions/pull \
  -H "Content-Type: application/json" \
  -d '{"username": "alice", "collection_name": "forked-docs"}'
```

**Request Body**:
- `username` (required): Collection owner
- `collection_name` (required): Collection name

**Response**:
```json
{"message": "pull not implemented (upstream sync coming in v2)"}
```

---

## 7. P2P Network

### `GET /p2p/status`
Get P2P node comprehensive status.

```bash
curl http://127.0.0.1:3000/p2p/status
```

**Response**:
```json
{
  "enabled": true,
  "peer_id": "12D3KooW...",
  "addrs": [
    "/ip4/127.0.0.1/tcp/4001",
    "/ip4/192.168.1.100/tcp/4001",
    "/ip4/127.0.0.1/udp/4001/quic-v1"
  ],
  "connected_count": 5,
  "discovered_count": 3,
  "relay_mode": "enabled",
  "hole_punch": true,
  "ws_connections": 0,
  "signal_peers": 0,
  "conn_stats": {
    "total_connections": 8,
    "inbound": 3,
    "outbound": 5
  },
  "active_transfers": [
    {
      "hash": "abc123...",
      "progress": 0.45,
      "total_mb": 100.0,
      "done": false,
      "peers": 3,
      "elapsed": "1m30s"
    }
  ]
}
```

**Fields**:
- `enabled`: Whether P2P is enabled
- `peer_id`: Local libp2p peer ID
- `addrs`: List of listening multiaddrs
- `connected_count`: Number of currently connected peers
- `discovered_count`: Number of peers discovered via mDNS
- `relay_mode`: Relay status (`enabled`, `disabled`)
- `hole_punch`: Whether hole punching is enabled
- `ws_connections`: Number of WebSocket connections
- `signal_peers`: Number of peers in the signaling hub
- `conn_stats`: Connection manager statistics (total, inbound, outbound)
- `active_transfers`: Array of active file transfer jobs

---

### `GET /p2p/node`
Get local node info (peer ID and addresses).

```bash
curl http://127.0.0.1:3000/p2p/node
```

**Response**:
```json
{
  "peer_id": "12D3KooW...",
  "addrs": [
    "/ip4/127.0.0.1/tcp/4001",
    "/ip4/192.168.1.100/tcp/4001"
  ]
}
```

---

### `GET /p2p/peers`
List connected peer IDs.

```bash
curl http://127.0.0.1:3000/p2p/peers
```

**Response**:
```json
{
  "peers": [
    "12D3KooWAbc...",
    "12D3KooWDef..."
  ]
}
```

---

### `GET /p2p/peers/detail`
Get detailed metadata for all tracked peers.

```bash
curl http://127.0.0.1:3000/p2p/peers/detail
```

**Response**:
```json
[
  {
    "peer_id": "12D3KooW...",
    "addrs": ["/ip4/10.0.0.1/tcp/4001"],
    "first_seen": "2026-04-28T12:00:00Z",
    "last_seen": "2026-04-28T14:00:00Z",
    "bytes_sent": 1048576,
    "bytes_recv": 2097152,
    "server_version": "peerdrive/1.0",
    "transports": ["tcp", "quic"],
    "reg_verified": true,
    "reg_username": "bob",
    "connection_dur": "2h30m",
    "latency": "45ms",
    "user_agent": "peerdrive-client/1.0",
    "direction": "outbound",
    "connected_at": "2026-04-28T12:00:00Z"
  }
]
```

**Fields** (per peer):
- `peer_id`: libp2p peer ID
- `addrs`: Known multiaddrs
- `first_seen` / `last_seen`: Timestamps
- `bytes_sent` / `bytes_recv`: Transfer statistics
- `server_version`: Remote node software version
- `transports`: Supported transports (`tcp`, `quic`, `ws`)
- `reg_verified`: Whether verified by registration server
- `reg_username`: Registration server username (if verified)
- `connection_dur`: Duration the connection has been established
- `latency`: Last measured RTT
- `direction`: `inbound` or `outbound`
- `connected_at`: When the connection was established

---

### `GET /p2p/peers/detail/:peer_id`
Get detailed metadata for a specific peer.

```bash
curl http://127.0.0.1:3000/p2p/peers/detail/12D3KooWAbc...
```

**Response**: Same structure as a single entry from `/p2p/peers/detail`.

---

### `GET /p2p/discovered`
List mDNS-discovered LAN peers.

```bash
curl http://127.0.0.1:3000/p2p/discovered
```

**Response**:
```json
{
  "peers": [
    {
      "peer_id": "12D3KooW...",
      "addrs": ["/ip4/192.168.1.50/tcp/4001"]
    }
  ]
}
```

---

### `GET /p2p/ping/:peer_id`
Ping a remote peer and return RTT.

```bash
curl http://127.0.0.1:3000/p2p/ping/12D3KooWAbc...
```

**Response**:
```json
{
  "peer": "12D3KooWAbc...",
  "rtt": "45ms"
}
```

**Fields**:
- `peer`: The target peer ID
- `rtt`: Round-trip time string

---

### `POST /p2p/connect`
Connect to a remote peer by multiaddr.

```bash
curl -X POST http://127.0.0.1:3000/p2p/connect \
  -H "Content-Type: application/json" \
  -d '{"addr": "/ip4/192.168.1.50/tcp/4001/p2p/12D3KooWAbc..."}'
```

**Request Body**:
- `addr` (required): Full multiaddr including peer ID

**Response**:
```json
{"status": "connected"}
```

---

### `POST /p2p/announce`
Announce that this node holds a specific hash on the IPFS DHT (libp2p provide).

```bash
curl -X POST http://127.0.0.1:3000/p2p/announce \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}'
```

**Request Body**:
- `hash` (required): SHA256 hash to announce

**Response**:
```json
{"status": "announced"}
```

---

### `POST /p2p/fetch`
Fetch an anonymous collection from the P2P network by its hash.

```bash
curl -X POST http://127.0.0.1:3000/p2p/fetch \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}'
```

**Request Body**:
- `hash` (required): Collection SHA256 hash to fetch

**Response**: The full anonymous collection JSON (same structure as `GET /anon/collections/:hash`).

---

### `POST /p2p/sync`
Sync files from a specific peer to a local directory.

```bash
curl -X POST http://127.0.0.1:3000/p2p/sync \
  -H "Content-Type: application/json" \
  -d '{
    "peer_id": "12D3KooWAbc...",
    "file_hashes": ["abc...", "def..."],
    "target_dir": "./downloaded"
  }'
```

**Request Body**:
- `peer_id` (required): Source peer ID
- `hash` (optional): Collection hash to resolve file list from (alternative to `file_hashes`)
- `file_hashes` (optional): Array of specific SHA256 hashes to sync
- `target_dir` (optional): Local target directory (default: `./p2p_sync`)

**Response**:
```json
{
  "synced": ["/path/to/file1", "/path/to/file2"],
  "count": 2,
  "saved_to": "./downloaded"
}
```

---

### `POST /p2p/push`
Push collection entries to a target directory for a peer to fetch.

```bash
curl -X POST http://127.0.0.1:3000/p2p/push \
  -H "Content-Type: application/json" \
  -d '{
    "hash": "abc123...",
    "target_dir": "shared-files"
  }'
```

**Request Body**:
- `hash` (optional): Collection hash to resolve entries from
- `entries` (optional): Array of `{path, hash}` entries (alternative to `hash`)
- `target_dir` (optional): Target directory name

**Response**:
```json
{
  "entries": [{"path": "doc.txt", "hash": "abc..."}],
  "target_dir": "./shared-files",
  "message": "collection received, ready to download"
}
```

---

### `POST /p2p/request-file`
Broadcast a file request to specific peers.

```bash
curl -X POST http://127.0.0.1:3000/p2p/request-file \
  -H "Content-Type: application/json" \
  -d '{
    "hash": "abc123...",
    "peer_ids": ["12D3KooWAbc...", "12D3KooWDef..."]
  }'
```

**Request Body**:
- `hash` (required): Requested file hash
- `peer_ids` (required): Array of target peer IDs

**Response**:
```json
{
  "hash": "abc123...",
  "requested": 2,
  "responses": 2,
  "details": [
    {"hash": "abc123...", "size": 1048576},
    {"hash": "abc123...", "error": "not found"}
  ]
}
```

---

### `GET /p2p/connections`
Get connection counts and scanner status.

```bash
curl http://127.0.0.1:3000/p2p/connections
```

**Response**:
```json
{
  "inbound": 3,
  "outbound": 5,
  "total": 8,
  "active_scanners": ["mdns", "relay-list"],
  "last_scan_times": {
    "mdns": "2026-04-28T14:00:00Z",
    "relay-list": "2026-04-28T13:55:00Z"
  }
}
```

---

### `GET /p2p/stats`
Get global P2P statistics.

```bash
curl http://127.0.0.1:3000/p2p/stats
```

**Response**:
```json
{
  "total_peers_seen": 25,
  "total_bytes_sent": 52428800,
  "total_bytes_recv": 104857600,
  "active_connections": 8,
  "total_transfers": 12,
  "uptime_seconds": 3600
}
```

---

### `GET /p2p/topology`
Get the P2P connection topology graph.

```bash
curl http://127.0.0.1:3000/p2p/topology
```

**Response**:
```json
{
  "local_peer_id": "12D3KooW...",
  "edges": [
    {
      "peer_id": "12D3KooWAbc...",
      "addrs": ["/ip4/10.0.0.1/tcp/4001"],
      "direction": "outbound",
      "latency": "45ms",
      "transport": "tcp"
    }
  ]
}
```

**Fields**:
- `local_peer_id`: This node's peer ID
- `edges`: Array of direct connections with peer ID, addresses, direction, latency, and transport

---

### `GET /p2p/quality`
Get connection quality metrics for all peers, or a specific peer.

```bash
curl http://127.0.0.1:3000/p2p/quality
curl http://127.0.0.1:3000/p2p/quality?peer_id=12D3KooWAbc...
```

**Query Parameters**:
- `peer_id` (optional): Filter to a specific peer

**Response** (array or single object):
```json
{
  "peer_id": "12D3KooWAbc...",
  "latency": "45ms",
  "bandwidth": 1048576,
  "reliability": 0.98,
  "connection_stability": "high"
}
```

**Fields**:
- `latency`: Last measured round-trip time
- `bandwidth`: Estimated bandwidth in bytes/s
- `reliability`: Float 0.0–1.0 reliability score
- `connection_stability`: Qualitative assessment (`high`, `medium`, `low`)

---

### `GET /p2p/ws/info`
Get WebSocket transport information.

```bash
curl http://127.0.0.1:3000/p2p/ws/info
```

**Response**:
```json
{
  "ws_connections": 0,
  "ws_endpoint": "/ws/transfer",
  "message_types": ["request", "response", "ping", "pong"]
}
```

---

### `GET /p2p/webrtc/info`
Get WebRTC STUN/TURN configuration.

```bash
curl http://127.0.0.1:3000/p2p/webrtc/info
```

**Response**:
```json
{
  "stun_server": "stun:stun.l.google.com:19302",
  "turn_server": "turn:turn.example.com:3478"
}
```

---

## 8. BitTorrent DHT

### `GET /bt/status`
Get BitTorrent DHT node status.

```bash
curl http://127.0.0.1:3000/bt/status
```

**Response**:
```json
{
  "enabled": true,
  "listen_addr": "0.0.0.0:4002",
  "num_nodes": 32
}
```

**Fields**:
- `enabled`: Whether BT DHT is enabled
- `listen_addr`: DHT listen address
- `num_nodes`: Number of nodes in the DHT routing table

---

### `POST /bt/announce`
Announce a hash on the BitTorrent DHT network.

```bash
curl -X POST http://127.0.0.1:3000/bt/announce \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}'
```

**Request Body**:
- `hash` (required): SHA256 hash to announce

**Response**:
```json
{"status": "announced on BT DHT"}
```

---

### `POST /bt/find`
Find providers for a hash on the BitTorrent DHT.

```bash
curl -X POST http://127.0.0.1:3000/bt/find \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}'
```

**Request Body**:
- `hash` (required): SHA256 hash to search for

**Response**:
```json
{
  "hash": "abc123...",
  "peers": ["peer_id_1", "peer_id_2"],
  "count": 2
}
```

---

### `POST /bt/torrent`
Upload a `.torrent` file and start downloading.

```bash
curl -X POST http://127.0.0.1:3000/bt/torrent \
  -F "torrent=@ubuntu-24.04-desktop-amd64.iso.torrent"
```

**Request**: `multipart/form-data`, field `torrent`

**Response**:
```json
{
  "infohash": "a1b2c3d4e5f6...",
  "name": "ubuntu-24.04-desktop-amd64.iso",
  "files": 1,
  "total": 5830086656,
  "pieces": 5564,
  "status": "downloading"
}
```

**Fields**:
- `infohash`: 40-character hex infohash
- `name`: Torrent display name
- `files`: Number of files in the torrent
- `total`: Total size in bytes
- `pieces`: Number of pieces
- `status`: Current status (`downloading`)

---

### `POST /bt/magnet`
Resolve a magnet URI and start BitTorrent download.

```bash
curl -X POST http://127.0.0.1:3000/bt/magnet \
  -H "Content-Type: application/json" \
  -d '{"uri": "magnet:?xt=urn:btih:a1b2c3d4e5f6...&dn=ubuntu-24.04"}'
```

**Request Body**:
- `uri` (required): Magnet URI

**Response**:
```json
{
  "infohash": "a1b2c3d4e5f6...",
  "display_name": "ubuntu-24.04",
  "trackers": ["udp://tracker.ubuntu.com:6969"],
  "status": "downloading"
}
```

---

### `GET /bt/download/:infohash`
Get download progress for a specific infohash.

```bash
curl http://127.0.0.1:3000/bt/download/a1b2c3d4e5f6...
```

**Response**:
```json
{
  "infohash": "a1b2c3d4e5f6...",
  "name": "ubuntu-24.04-desktop-amd64.iso",
  "status": "downloading",
  "total_size": 5830086656,
  "downloaded": 2097152000,
  "pieces_done": 2000,
  "pieces_total": 5564,
  "progress": 0.36,
  "peers": 12,
  "download_speed": 5242880,
  "upload_speed": 1048576,
  "eta": "12m30s"
}
```

---

### `GET /bt/downloads`
List all BitTorrent downloads (active and completed).

```bash
curl http://127.0.0.1:3000/bt/downloads
```

**Response**:
```json
{
  "downloads": [
    {
      "infohash": "a1b2c3d4e5f6...",
      "name": "ubuntu-24.04-desktop-amd64.iso",
      "status": "downloading",
      "progress": 0.36
    }
  ],
  "count": 1
}
```

---

### `POST /bt/download/:infohash/pause`
Pause a BitTorrent download.

```bash
curl -X POST http://127.0.0.1:3000/bt/download/a1b2c3.../pause
```

**Response**:
```json
{"infohash": "a1b2c3d4e5f6...", "status": "paused"}
```

---

### `POST /bt/download/:infohash/resume`
Resume a paused BitTorrent download.

```bash
curl -X POST http://127.0.0.1:3000/bt/download/a1b2c3.../resume
```

**Response**:
```json
{"infohash": "a1b2c3d4e5f6...", "status": "downloading"}
```

---

### `POST /bt/download/:infohash/seed`
Start seeding a completed download.

```bash
curl -X POST http://127.0.0.1:3000/bt/download/a1b2c3.../seed
```

**Response**:
```json
{"infohash": "a1b2c3d4e5f6...", "status": "seeding"}
```

---

### `POST /bt/download/:infohash/unseed`
Stop seeding a download.

```bash
curl -X POST http://127.0.0.1:3000/bt/download/a1b2c3.../unseed
```

**Response**:
```json
{"infohash": "a1b2c3d4e5f6...", "status": "stopped"}
```

---

### `DELETE /bt/download/:infohash`
Remove a download and its files.

```bash
curl -X DELETE http://127.0.0.1:3000/bt/download/a1b2c3...
```

**Response**:
```json
{"infohash": "a1b2c3d4e5f6...", "status": "removed"}
```

---

### `GET /bt/stats`
Get global BitTorrent client statistics.

```bash
curl http://127.0.0.1:3000/bt/stats
```

**Response**:
```json
{
  "total_up_bytes": 52428800,
  "total_down_bytes": 209715200,
  "active_torrents": 2,
  "paused_torrents": 1,
  "completed": 3,
  "errors": 0,
  "dht_nodes": 32,
  "seeding": 1,
  "seeding_hashes": ["a1b2c3..."]
}
```

**Fields**:
- `total_up_bytes` / `total_down_bytes`: Cumulative transfer statistics
- `active_torrents`: Currently downloading torrents
- `paused_torrents`: Paused torrents
- `completed`: Completed torrents
- `errors`: Failed torrents
- `dht_nodes`: Nodes in the DHT routing table
- `seeding`: Number of torrents being seeded
- `seeding_hashes`: Array of infohashes being seeded

---

### `POST /bt/bep44/put`
Store immutable data on the BitTorrent DHT using BEP 44.

```bash
curl -X POST http://127.0.0.1:3000/bt/bep44/put \
  -H "Content-Type: application/json" \
  -d '{"data": "SGVsbG8gV29ybGQ=", "mutable": false}'
```

**Request Body**:
- `data` (required): Base64-encoded data to store
- `mutable` (optional): Set to `true` for mutable puts (currently returns 501)
- `salt` (optional): Salt for mutable puts
- `key` (optional): Private key for mutable puts

**Response**:
```json
{
  "target": "a1b2c3d4e5f6...",
  "size": 11,
  "mutable": false
}
```

**Fields**:
- `target`: 40-character hex target hash (used as key for BEP44Get)
- `size`: Size of stored data in bytes

---

### `POST /bt/bep44/get`
Read immutable data from the BitTorrent DHT using BEP 44.

```bash
curl -X POST http://127.0.0.1:3000/bt/bep44/get \
  -H "Content-Type: application/json" \
  -d '{"target": "a1b2c3d4e5f6..."}'
```

**Request Body**:
- `target` (required): 40-character hex target hash from BEP44Put

**Response**:
```json
{
  "data": "SGVsbG8gV29ybGQ=",
  "size": 11
}
```

**Fields**:
- `data`: Base64-encoded retrieved data
- `size`: Size in bytes

---

### `GET /bt/bep51/sample`
Sample infohashes from the DHT using BEP 51.

```bash
curl http://127.0.0.1:3000/bt/bep51/sample
```

**Response**:
```json
{
  "samples": [
    "a1b2c3d4e5f6...",
    "fedcba987654..."
  ],
  "count": 200
}
```

**Fields**:
- `samples`: Array of 40-character hex infohashes sampled from the DHT
- `count`: Number of samples collected

---

## 9. Dual-Stack P2P

### `POST /p2p/dual/announce`
Announce a hash simultaneously on both IPFS DHT and BitTorrent DHT.

```bash
curl -X POST http://127.0.0.1:3000/p2p/dual/announce \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}'
```

**Request Body**:
- `hash` (required): SHA256 hash to announce

**Response**:
```json
{"status": "announced on both networks"}
```

---

### `POST /p2p/dual/find`
Find providers for a hash on both IPFS DHT and BitTorrent DHT simultaneously.

```bash
curl -X POST http://127.0.0.1:3000/p2p/dual/find \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}'
```

**Request Body**:
- `hash` (required): SHA256 hash to search for

**Response**:
```json
{
  "hash": "abc123...",
  "ipfs_peers": ["12D3KooW...", "12D3KooW..."],
  "bt_peers": ["peer_id_1", "peer_id_2"]
}
```

**Fields**:
- `ipfs_peers`: Peers found on the IPFS DHT (libp2p peer IDs)
- `bt_peers`: Peers found on the BitTorrent DHT

---

## 10. Port Forwarding

### `POST /p2p/forward/create`
Register a local service port for remote forwarding via P2P.

```bash
curl -X POST http://127.0.0.1:3000/p2p/forward/create \
  -H "Content-Type: application/json" \
  -d '{"key": "my-ssh", "port": 22}'
```

**Request Body**:
- `key` (required): Unique session key / identifier
- `port` (required): Local port to expose (1–65535)

**Response**:
```json
{"status": "listening", "port": 22}
```

---

### `POST /p2p/forward/connect`
Connect to a remote peer's forwarded port.

```bash
curl -X POST http://127.0.0.1:3000/p2p/forward/connect \
  -H "Content-Type: application/json" \
  -d '{
    "key": "my-ssh",
    "target_peer": "12D3KooWAbc...",
    "local_port": 2222
  }'
```

**Request Body**:
- `key` (required): Session key matching the remote's create key
- `target_peer` (required): Remote peer ID
- `local_port` (required): Local port to bind the forwarded connection

**Response**:
```json
{"status": "connected", "local_port": 2222}
```

---

### `GET /p2p/forward/list`
List all active port forwarding sessions.

```bash
curl http://127.0.0.1:3000/p2p/forward/list
```

**Response**:
```json
{
  "sessions": [
    {
      "key": "my-ssh",
      "source_peer": "12D3KooW...",
      "local_port": 2222,
      "created_at": "2026-04-28T12:00:00Z",
      "clients": 1
    }
  ]
}
```

**Fields** (per session):
- `key`: Session identifier
- `source_peer`: Peer ID of the session creator
- `local_port`: Local port number
- `created_at`: ISO 8601 creation timestamp
- `clients`: Number of connected clients

---

### `POST /p2p/forward/close`
Close a port forwarding session by key.

```bash
curl -X POST http://127.0.0.1:3000/p2p/forward/close \
  -H "Content-Type: application/json" \
  -d '{"key": "my-ssh"}'
```

**Request Body**:
- `key` (required): Session key to close

**Response**:
```json
{"status": "closed"}
```

---

## 11. IPFS Compat

### `GET /ipfs`
Get IPFS compatibility layer status.

```bash
curl http://127.0.0.1:3000/ipfs
```

**Response**:
```json
{
  "enabled": false,
  "block_count": 0,
  "blockstore": "/mnt/d/WorkPlace/peerdrive/storage/ipfs-blocks"
}
```

**Fields**:
- `enabled`: Whether the IPFS compat layer is active
- `block_count`: Number of blocks cached in the blockstore
- `blockstore`: Path to the IPFS block storage directory

---

### `POST /ipfs/toggle`
Enable or disable the IPFS compatibility layer.

```bash
curl -X POST http://127.0.0.1:3000/ipfs/toggle \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'
```

**Request Body**:
- `enabled` (required): `true` to enable, `false` to disable

**Response**:
```json
{"enabled": true}
```

---

### `POST /ipfs/pin/:cid`
Pin an IPFS CID. Downloads from public IPFS gateways and caches permanently in local storage.

```bash
curl -X POST http://127.0.0.1:3000/ipfs/pin/QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco
```

**Response**:
```json
{
  "status": "pinned",
  "cid": "QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco",
  "hash": "abc123...",
  "size": 1048576
}
```

**Fields**:
- `status`: `"pinned"` or `"already_pinned"`
- `cid`: The IPFS CID
- `hash`: Corresponding SHA256 hash
- `size`: File size in bytes

---

### `DELETE /ipfs/pin/:cid`
Unpin an IPFS CID. Removes the pin record (local data may remain).

```bash
curl -X DELETE http://127.0.0.1:3000/ipfs/pin/QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco
```

**Response**:
```json
{"status": "unpinned", "cid": "QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco"}
```

---

### `GET /ipfs/pins`
List all pinned CIDs.

```bash
curl http://127.0.0.1:3000/ipfs/pins
```

**Response**:
```json
{
  "pins": [
    {
      "cid": "QmXoypizjW3WknFiJnKLwHCnL72vedxjQkDDP1mXWo6uco",
      "hash": "abc123...",
      "size": 1048576,
      "filename": "QmXoypizj...",
      "pinned_at": "2026-04-28T12:00:00Z"
    }
  ],
  "count": 1
}
```

**Fields** (per pin):
- `cid`: IPFS CID
- `hash`: SHA256 hash of the content
- `size`: File size in bytes
- `filename`: Filename (typically the CID if no explicit filename)
- `pinned_at`: ISO 8601 pin timestamp

---

### `GET /ipfs/gateways`
Check health of all configured IPFS gateways.

```bash
curl http://127.0.0.1:3000/ipfs/gateways
```

**Response**:
```json
{
  "gateways": [
    {"url": "https://ipfs.io", "healthy": true, "latency": "150ms"},
    {"url": "https://cloudflare-ipfs.com", "healthy": true, "latency": "80ms"}
  ]
}
```

**Fields** (per gateway):
- `url`: Gateway URL
- `healthy`: Whether the gateway responded successfully
- `latency`: Response time string

---

## 12. Authentication

### `GET /p2p/auth/status`
Get current authentication status.

```bash
curl http://127.0.0.1:3000/p2p/auth/status
```

**Response**:
```json
{
  "authenticated": false,
  "username": "",
  "role": ""
}
```

**Fields**:
- `authenticated`: Whether the request has a valid Bearer token
- `username`: Authenticated username (empty if not authenticated)
- `role`: User role (empty if not authenticated)

To make authenticated requests, include an `Authorization` header:
```bash
curl -H "Authorization: Bearer <token>" http://127.0.0.1:3000/p2p/auth/status
```

---

## 13. Share Links

### `POST /shares`
Create a share link for a file or collection.

```bash
curl -X POST http://127.0.0.1:3000/shares \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123...", "type": "file", "filename": "document.pdf"}'
```

**Request Body**:
- `hash` (required): SHA256 hash (for file) or collection hash (for collection)
- `type` (required): `"file"` or `"collection"`
- `filename` (optional): Display filename

**Response**:
```json
{
  "token": "a1b2c3d4e5f6...",
  "hash": "abc123...",
  "type": "file",
  "filename": "document.pdf",
  "url": "/s/a1b2c3d4e5f6...",
  "expires": null
}
```

**Fields**:
- `token`: Unique share token
- `hash`: The shared hash
- `type`: Share type (`file` or `collection`)
- `filename`: Display filename
- `url`: Relative URL to access the share
- `expires`: Expiration timestamp (null = never expires)

---

### `GET /shares`
List all active (non-expired) share links.

```bash
curl http://127.0.0.1:3000/shares
```

**Response**:
```json
{
  "shares": [
    {
      "id": 1,
      "token": "a1b2c3d4e5f6...",
      "hash": "abc123...",
      "type": "file",
      "filename": "document.pdf",
      "created_at": "2026-04-28T12:00:00Z",
      "expires_at": null
    }
  ]
}
```

---

### `GET /s/:token`
Access a share link by token. Redirects to the file download or collection view.

```bash
curl -L http://127.0.0.1:3000/s/a1b2c3d4e5f6...
```

**Response**: HTTP 302 redirect to `/sha256sum/:hash` (for files) or `/anon/collections/:hash` (for collections).

---

## 14. Tasks

### `GET /tasks`
List all async tasks (currently returns an empty stub array).

```bash
curl http://127.0.0.1:3000/tasks
```

**Response**:
```json
{"tasks": []}
```

---

### `GET /tasks/:id`
Get the status and result of an async task.

```bash
curl http://127.0.0.1:3000/tasks/1
```

**Response**:
```json
{
  "task": {
    "id": 1,
    "type": "pull",
    "status": "completed",
    "params": "",
    "result": "{\"note\":\"pull no-op\"}",
    "created_at": "2026-04-28T12:00:00Z",
    "updated_at": "2026-04-28T12:00:01Z"
  }
}
```

**Fields**:
- `id`: Task ID
- `type`: Task type (`pull`, `merge`, etc.)
- `status`: `pending` | `completed` | `failed`
- `params`: JSON string of task parameters
- `result`: JSON string of task result
- `created_at` / `updated_at`: ISO 8601 timestamps

---

## 15. Local Sync

### `POST /local/save`
Save collection files to local disk.

```bash
curl -X POST http://127.0.0.1:3000/local/save \
  -H "Content-Type: application/json" \
  -d '{
    "collection_hash": "abc123...",
    "local_path": "./downloaded",
    "include": ["*.txt", "*.md"],
    "exclude": ["*.tmp"]
  }'
```

**Request Body**:
- `collection_hash` (required): Collection SHA256 hash
- `local_path` (required): Local directory path
- `include` (optional): Glob patterns to include
- `exclude` (optional): Glob patterns to exclude

**Response**:
```json
{"message": "collection sync started/completed successfully"}
```

---

### `GET /local/status/:hash`
Get sync status for a collection.

```bash
curl http://127.0.0.1:3000/local/status/abc123...
```

**Response**:
```json
{
  "collection_hash": "abc123...",
  "local_path": "./downloaded",
  "total_files": 10,
  "saved_files": 7,
  "missing_files": [
    {
      "id": 1,
      "collection_hash": "abc123...",
      "file_path": "missing.txt",
      "is_saved": false,
      "last_modified": "2026-04-28T12:00:00Z"
    }
  ],
  "last_synced": "2026-04-28T12:00:00Z"
}
```

**Fields**:
- `total_files`: Total files in the collection
- `saved_files`: Files successfully saved to disk
- `missing_files`: Array of files not yet saved
- `last_synced`: ISO 8601 timestamp of last sync

---

## 16. WebDAV

### `GET/PUT/DELETE/PROPFIND /webdav/*path`
Mount the entire content-addressed storage as a WebDAV network drive.

```bash
# List root directory (via WebDAV PROPFIND)
curl -X PROPFIND http://127.0.0.1:3000/webdav/

# Read a file
curl http://127.0.0.1:3000/webdav/ab/abc123...

# WebDAV is enabled when configured (cfg.WebDAVEnable)
```

WebDAV exposes the storage directory as a standard WebDAV filesystem, compatible with macOS Finder, Windows Explorer, Linux `davfs2`, and `rclone`.

**Note**: This endpoint is only active when `WebDAVEnable` is `true` in the server configuration.

---

## 17. WebRTC & Signaling

### `GET /ws/signal`
WebRTC signaling endpoint (WebSocket). Used for establishing direct browser-to-browser connections.

```bash
# Connect via WebSocket
ws ws://127.0.0.1:3000/ws/signal
```

This is a WebSocket endpoint, not a REST API. Messages are JSON-encoded signaling frames (offer, answer, ICE candidates).

### `GET /ws/transfer`
WebSocket endpoint for P2P file transfers.

```bash
# Connect via WebSocket
ws ws://127.0.0.1:3000/ws/transfer
```

Message types: `request`, `response`, `ping`, `pong`.

---

## 18. Relay Proxy

### `GET /relay/proxy`
P2P relay proxy endpoint for proxied downloads through connected relay nodes.

```bash
curl http://127.0.0.1:3000/relay/proxy
```

**Response**: Proxied data stream (application/octet-stream).

---

## 19. Swagger UI

### `GET /swagger/*any`
Swagger UI documentation page.

```bash
# Open in browser
open http://127.0.0.1:3000/swagger/index.html
```

---

## Error Responses

All endpoints return errors in a consistent JSON format:

```json
{"error": "description of what went wrong"}
```

Common HTTP status codes:
- `200 OK`: Success
- `201 Created`: Resource created
- `400 Bad Request`: Invalid input (missing fields, invalid hash format, etc.)
- `403 Forbidden`: Storage disabled
- `404 Not Found`: Resource not found
- `409 Conflict`: Duplicate resource or merge conflict
- `500 Internal Server Error`: Server-side failure
- `503 Service Unavailable`: Dependent service not available (e.g., BT DHT disabled)

---

## CORS Support

All endpoints support CORS with configurable allowed origins. Preflight `OPTIONS` requests are handled automatically.
