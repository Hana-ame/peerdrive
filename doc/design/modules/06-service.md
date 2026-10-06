# Module 06: service Business Logic Layer

- **Code location**: `back/internal/service/`
- **One-line function**: Business use case orchestration layer — combines repository (persistence), transport/source/downloader (data plane) and pathutil (persistence), only exposed to controller/router, shielding downstream details.
- **Dependencies**: repository (`back/internal/repository/`), model (`back/internal/model/`), transport (`back/internal/transport/`), source (`back/internal/source/`), downloader (`back/internal/downloader/`), pathutil (`back/internal/pathutil/`), config, nodestate, log.
- **Depended upon by**: `back/internal/controller/` all controllers (file/collection/share/sync/anon/p2p/node_market/node_share/peer_pull etc.), `back/internal/serverapp/app.go` (assembles NewNodeDirectory/NewNodeShare/NewPeerPuller/NewAnonService).

## 1. Logic

This layer is the product of "M2 convergence": controller previously called repository directly (60+ scattered call sites), now all reads/writes go through service — `controller only depends on service, repository only referenced by service` ([collection_service.go:1-4](collection_service.go)). Method naming corresponds one-to-one with repository, most are transparent forwarding; transaction/cache/cross-module orchestration land at this layer.

Nine service components (one file each, by responsibility):

| Component | File | Responsibility |
|------|------|------|
| FileService | [file_service.go](file_service.go) | File upload, URL registration, local file registration/batch registration, validate/delete, directory traversal, metadata query (M2 convergence) |
| CollectionService | [collection_service.go](collection_service.go) | Collection domain use cases (collection CRUD, search, fork/merge related transparent forwarding) |
| ShareService | [share_service.go](share_service.go) | 30-day valid share link use cases |
| SyncService | [sync_service.go](sync_service.go) | Sync collection files to local disk, with include/exclude filtering and sync state tracking |
| PinService | [pin_service.go](pin_service.go) | IPFS pin use cases (M2 convergence, independent feature from legacy p2p controller) |
| AnonService | [anon_service.go](anon_service.go) | Anonymous collections (visibility + AccessList), stateless read service (only holds cfg) |
| NodeDirectory | [node_directory.go](node_directory.go) | Node marketplace directory (NETDISK M1): discovery server online ∪ local connections ∪ joined list |
| NodeShare | [nodeshare.go](nodeshare.go) | Node sharing scope (NETDISK M2): environment variable initial values + runtime selection synthesis; share frame data source + announce summary |
| PeerPuller | [peerpull.go](peerpull.go) | Cross-node pull save (NETDISK M3): peer content → local persistence + registration |

**Key flow highlights**:

- **NodeShare assembly** (main.go:134-162): `NewNodeShare(cfg, storageDir)` → `SetDirHook` (runtime new shared directory supplements `FileIndex().AddReadRoot`) → `SetAnonAccess` (anonymous collection reads) → `SetFileLister/SetFileInfoReader` → fed to transport as `SetShareProvider(share.SnapshotFor)` and `SetShareGate(share)`.
- **PeerPuller assembly** (main.go:167-182): `SetSource(peerjsSvc)`; `SetFileAccess(hasher, creator)` uses it to determine "content already local then skip", after persistence `file_index.Create` registers.
- **FileService upload** (file_service.go:32-45): Constructs holding only `storageDir/storageEnable/cfg`; persistence path `storageDir/<hash first 2 chars>/<hash>` (content-addressed, see [03-storage.md](03-storage.md) and [10-controller-storage.md](../connections/10-controller-storage.md)).

## 2. How It Stores

This layer **does not do general persistence** — it is the orchestrator, persistence delegated by responsibility to downstream, only retains two small files:

1. **Delegated to repository (SQLite)**: Collections, share links, pins, sync state, anonymous collections visibility/AccessList → `back/internal/repository/` (see [02-repository.md](02-repository.md), [04-service-repository.md](../connections/04-service-repository.md)).
2. **Delegated to storage**: Uploaded content → `storageDir/<hash first 2 chars>/<hash>` (content-addressed); IPFS gateway data `ImportGatewayData` also persists + `InsertFileMeta` + `InsertFileProvider` triple (file_service.go:57-70).

**This layer's own persistence files (only three, all under storageDir)**:

| File | Writer | Medium/Format | Description |
|------|--------|-----------|------|
| `joined_nodes.json` | NodeDirectory | JSON (`{peers:[{peer_id, joined_at}]}`) | Operator joined nodes, retained offline; **atomic write** (temp file + rename), prevents half-written file if process killed (node_directory.go:14-40) |
| `share_scope.json` | NodeShare | JSON | Runtime sharing scope (dirs/files/collections + levels), persists across restart (nodeshare.go:10-60) |
| `<DownloadDir>/pulled/…` | PeerPuller | File (`.part` temp → rename to final name) | Cross-node pull persistence, single write not CAS replica (peerpull.go:15-45) |

**Why not put these in SQLite**: joined_nodes is a very small operator preference (peerId + time), needs to work without DB scenarios (pure client mode/unit tests/CI); share_scope is similarly a "casual decision", unrelated to file_index registration.

## 3. When It Stores

| Trigger | Behavior | Code Reference |
|--------|------|----------|
| Process startup (main) | `NewNodeDirectory` loads `joined_nodes.json`; `NewNodeShare` reads `share_scope.json` (environment variables `PEERDRIVE_SHARE_*` only seed on **first startup** then persist) | main.go:120-162; nodeshare.go:10-60 |
| Marketplace join/leave operation (API) | `NodeDirectory.Join/Leave` → update `joined_nodes.json` (atomic write) | node_directory.go |
| Admin console checkbox / PUT `/peerjs/share` | NodeShare updates `share_scope.json` (runtime persistence; environment variable changes don't backfill) | nodeshare.go |
| File upload / gateway import (request) | FileService writes content-addressed file + repository.InsertFileMeta + InsertFileProvider | file_service.go:57-70 |
| Sync save (request) | SyncService `SaveToDisk`: path traversal defense → download → persist (downloader) | sync_service.go:15-40 |
| Cross-node pull (request) | PeerPuller: stream write `.part` → verify sha256 → rename → `file_index.Create` register | peerpull.go |

## 4. What It Stores

- **Data delegated to repository** (details in [02-repository.md](02-repository.md)): collections (username/collection name/visibility/follow_redirects/tags), share_links (token, hash, type, filename, 30-day expiry), pins (cid/hash/filename/size), sync state, anon collections (visibility/access_list).
- **joined_nodes.json**: `{peers: [{peer_id, joined_at}]}` — marketplace "joined" persistent list.
- **share_scope.json**: `{dirs: [], files: [], collections: [], friends: []}` + each declaration's level (public/unlisted/private).
- **Pulled persistence**: `<DownloadDir>/pulled/<relative_path>.part→<relative_path>`; deduplication relies on file_index pre-check (same hash already registered means no download needed).

## 5. Boundaries and Pitfalls

- **NodeShare disabled by default**: `PEERDRIVE_SHARE_ENABLE=false`, without explicit enabling no manifest is exposed externally (nodeshare.go header comments).
- **Security boundaries (two, code comments explicitly say don't relax)**:
  1. Share frames have no verifiable identity → private judgment only meaningful on connections that passed PSK admission (without PSK, friend list degrades to "person claiming that id");
  2. Collection visibility non-public means **not included in external manifest**, treated as private (AccessList is account list, no identity to verify).
- **Path traversal defense**: SyncService `SaveToDisk` start rejects paths containing `..`; PeerPuller persistence paths similarly restricted.
- **M2 convergence discipline**: Controller must not call repository directly (convergence target); PinService's controller p2p.go is still entirely legacy (pending M1 migration), but pin endpoints are converged independent features.

## 6. External Connections

- [04-service-repository.md](../connections/04-service-repository.md): This layer delegates SQLite persistence read/write and transaction boundaries.
- [03-controller-service.md](../connections/03-controller-service.md): Controller calls this layer's business orchestration entry points.
- [06-service-transport.md](../connections/06-service-transport.md): NodeShare/NodeDirectory/PeerPuller wiring with transport (share frames, announce, pull streams).
- [05-router-source.md](../connections/05-router-source.md): SyncService fetches data via downloader, FileService persists via pathutil (adjacent faces).
- [10-controller-storage.md](../connections/10-controller-storage.md): Upload/persistence path security (this layer's FileService and controller share pathutil conventions).
