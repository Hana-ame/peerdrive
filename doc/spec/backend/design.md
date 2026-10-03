# Peerdrive System Design Document

## 1. Overview
Peerdrive is a content-addressable storage (CAS) based P2P file sharing and distribution system. It allows users to uniquely identify content via its SHA256 hash and provides a mechanism for organizing content into "Collections." The system supports multi-replica redundancy, automatic P2P fallback download, and snapshot-based version control.

## 2. Core Architecture

### 2.1 Content-Addressable Storage (CAS)
- **Unique identification**: All files use their content's SHA256 hash as a unique ID.
- **Decoupled storage**: The metadata layer decouples "content identifier (Hash)" from "physical location (Path/URL)."
- **Detailed analysis** → [sha256-download.md](./sha256-download.md)

### 2.2 Multi-replica metadata model
Uses a layered storage model:
- **`file_meta` table**: Stores intrinsic content properties (size, MIME type, gzip compression, file type). Only one record per unique content.
- **`file_providers` table**: Stores multiple physical replica locations for the content. Records include provider type (`local` or `http`) and availability status (`available`).
- **Replica strategy**: During download, all available replicas are tried in sequence; if a replica becomes invalid it is marked as unavailable.
- **Database definition** → [database.md](./database.md)

### 2.3 P2P download fallback mechanism
The system implements a tiered download strategy:
1. **Local storage** → 2. **Remote HTTP replica** → 3. **P2P Bitswap network**
- When all known replicas are unavailable, the system broadcasts a request over the network via the libp2p protocol.
- Successfully fetched data is automatically cached locally and metadata records are updated.
- **Implementation details** → [sha256-download.md](./sha256-download.md)

## 3. Collection System

### 3.1 Anonymous Collection
- **Nature**: A JSON file containing `path` → `hash` mappings.
- **Characteristics**: Immutable. Once generated, its content determines its SHA256 hash.
- **Storage**: The collection JSON is stored at `storage/{hash[:2]}/{hash}`, with `type` set to `anon_collection` in `file_meta`.
- **Validation**: Paths must be relative and contain no `..`; hashes must be 64-character hexadecimal.
- **Detailed analysis** → [anon-collection.md](./anon-collection.md)
- **Pointer mechanism**: User collections in the database are mutable records that point to anonymous collection snapshots via the `current_hash` field.
- **Version control**:
  - **Commit**: Serializes the current workspace state into an anonymous collection → stores it → updates `current_hash` → records version history.
  - **Rollback**: Points `current_hash` to a historical version snapshot.
  - **Fork/Merge**: Based on the immutability of anonymous snapshots, enables collection branching and merging.
- **Implementation logic** → [anon-collection.md](./anon-collection.md) | **Data structures** → [database.md](./database.md)

## 4. File Import and Upload

### 4.1 File Upload
- Uploads file streams via HTTP Multipart.
- Uses a temporary file to compute SHA256 hash, detect MIME type, and record file size.
- Files are stored at `storage/{hash[:2]}/{hash}`.
- Duplicate file detection: returns `already_exists` flag (HTTP 200) without writing to disk again.
- Write operations can be disabled via the `PEERDRIVE_STORAGE_ENABLE` environment variable (returns 403).
- **Detailed analysis** → [upload.md](./upload.md)

### 4.2 File Registration
- Directly references local files already on disk without copying.
- Supports both absolute paths (any filesystem location) and relative paths.
- Automatically computes and stores `Size`, `MimeType`, and SHA256 Hash.
- Idempotency guarantee: registering the same file repeatedly returns the same hash.
- **Registration analysis** → [register.md](./register.md)

## 5. Configuration System

Configuration is done via environment variables:
| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `3000` | HTTP listen port |
| `PEERDRIVE_STORAGE` | `./storage` | File storage root directory |
| `PEERDRIVE_STORAGE_ENABLE` | `true` | Write operation switch (returns 403 when set to false) |
| `PEERDRIVE_P2P_ENABLE` | `true` | Enable/disable P2P network |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | libp2p listen address |
| `PEERDRIVE_BOOTSTRAP_PEER` | (empty) | DHT bootstrap node multiaddr |
| `PEERDRIVE_MDNS_ENABLE` | `true` | LAN mDNS node discovery |
| `PEERDRIVE_RELAY_MODE` | `client` | Relay mode: off / client / server |
| `PEERDRIVE_STATIC_RELAYS` | (empty) | Comma-separated static relay addresses |
| `PEERDRIVE_HOLE_PUNCH` | `true` | Enable STUN hole punching |
| `PEERDRIVE_AUTO_NAT` | `true` | Automatic NAT type detection |
| `PEERDRIVE_NAT_PORTMAP` | `false` | UPnP/NATPMP port mapping |
| `PEERDRIVE_PUBLIC_REACHABLE` | `false` | Mark node as publicly reachable |
- **Configuration analysis** → `internal/config/config.go`

## 6. Tech Stack
- **Backend**: Go, libp2p, SQLite3, Gin
- **Frontend**: React (JSX), Tailwind CSS, Vite

## 7. Architecture Layers
```
cmd/server/main.go     — Entry point
internal/router/        — Route registration
internal/controller/    — HTTP request/response handling
internal/service/       — Business logic layer
internal/repository/    — Data access layer
internal/model/         — Data structure definitions
internal/provider/      — Content-addressed reading (local/http)
internal/config/        — Configuration management
```

## 8. API Logic Overview
| Path | Description |
|------|-------------|
| `GET /ping` | Health check |
| `GET /sha256sum/:hash` | CAS-based file download (with P2P fallback) |
| `POST /files/upload` | Upload file |
| `POST /files/register_local` | Register local file |
| `POST /files/register_folder` | Register folder |
| `GET /files/verify/:hash` | Verify file metadata |
| `DELETE /files/:hash` | Delete file |
| `POST /anon/collections` | Create anonymous collection |
| `GET /anon/collections/:hash` | Get anonymous collection JSON |
| `GET /anon/collections/:hash/entries/*path` | Download file from anonymous collection |
| `POST /anon/collections/fork` | Fork anonymous collection |
| `POST /collections` | Create user collection |
| `GET /collections/:username` | List user collections |
| `POST /collections/:username/:coll/entries` | Add entries |
| `POST /collections/:username/:coll/commit` | Commit version |
| `POST /collections/:username/:coll/rollback/:vid` | Rollback version |
| `POST /actions/fork` | Fork collection |
| `POST /actions/merge` | Merge collection |
| `POST /actions/pull` | Pull updates |
| `GET /tasks` | Task list |
| `GET /p2p/status` | P2P status (including relay/hole_punch/ws) |
| `GET /p2p/node` | P2P node info |
| `GET /p2p/peers` | Connected peers |
| `GET /p2p/discovered` | Nodes discovered via mDNS/DHT |
| `GET /p2p/ping/:peer_id` | Ping peer |
| `POST /p2p/connect` | Manually connect to peer |
| `POST /p2p/announce` | Announce ownership of file hash |
| `POST /p2p/fetch` | Fetch anonymous collection from P2P |
| `POST /p2p/sync` | Full-sync collection from peer |
| `POST /p2p/push` | Push collection to peer |
| `POST /p2p/request-file` | Broadcast file request (P2P) |
| `GET /p2p/ws/info` | WebSocket connection info |
| `GET /ws/transfer` | WebSocket file transfer endpoint |

## 9. Testing
- **Register/download tests**: `bash test/register.sh`
- **Upload tests**: `bash test/upload.sh`
- **Anonymous collection tests**: `bash test/anon-collection.sh`
- **P2P Stage 2 tests**: `bash test/p2p.sh`
- **P2P Stage 3 relay tests**: `bash test/relay.sh`
- **Testing documentation** → `docs/testing/index.md`
