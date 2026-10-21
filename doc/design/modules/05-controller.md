# Module 05: controller HTTP Handlers

- **Code location**: `back/internal/controller` (15 Go files; route registration and auth middleware in `back/internal/router/`, strongly related to this module, see §6)
- **One-line function**: Translates HTTP requests into service / downloader / transport calls — responsible for parameter parsing, format and auth validation, error mapping to status codes, **almost no persistence itself**, delegates all read/write to downstream modules.
- **Dependencies**: `back/internal/service` (File/Collection/Share/Anon/Pin/NodeDirectory/NodeShare/PeerPuller/Sync services), `back/internal/downloader` (UniversalDownloader), `back/internal/provider` (IPFSProvider), `back/internal/transport` (PeerJSService port forwarding/sharing manifest query), `back/internal/nodestate` (Operator), `back/pkg/hashutil` (SHA256 validation), `back/internal/config` (WebRTCInfo), `github.com/Hana-ame/go-peerdrive-bt` (BT DHT/BT client).
- **Depended upon by**: `back/internal/router/router.go` assembles and routes to all handlers; `transport/admin.go` admin frame **internally forwards** to this gin engine reusing the same controllers (`back/internal/router/router.go:402-414`); frontend/curl/integration tests directly request these HTTP endpoints (`back/internal/router/router.go:215-224` comments).

## 1. Logic

### 1.1 Responsibility Division

Controller is the "HTTP handler face" in the request handling chain: **parameter parsing / auth validation / calling service and downloader**, does not directly do business persistence. Three typical patterns:

1. **Parsing**: `c.ShouldBindJSON(&req)` (e.g. `back/internal/controller/file.go:99`, `collection.go:80`), `c.Request.FormFile("file")` (`file.go:46`), `c.Param`/`c.Query`/`c.DefaultQuery` (`file.go:176/328/345`), `c.GetHeader` (`download.go:84`).
2. **Validation**: Path hash always first passes `hashutil.IsValidSHA256` (64-char lowercase hex, `back/pkg/hashutil/hashutil.go:16-23`), parameter field non-empty checks (e.g. `file.go:104-108`, `share.go:32-35`). Visibility/permission validation in service layer (e.g. `anonSvc.GetCollectionVisibleTo`, `anon.go:141`).
3. **Delegation**: Call service methods, map errors to status codes (`ErrStorageDisabled`→403, `ErrFileAlreadyExists`→200 with `already_exists`, `file.go:57-72`; binding failure→400, not found→404, others→500).

### 1.2 Dependency Injection: Package-level Variables + Init* Functions

Controller has no constructors; all dependencies stored as package-level variables, injected by router at process startup via `Init*` (`file.go:30-36`, `collection.go:38-44`, `p2p.go:45-64` etc.). Inventory (each handler file header `var`):

| Package Variable | Type | Injection Function | Purpose |
|---|---|---|---|
| `fileSvc` | `*service.FileService` | `InitFileController` | File upload/register/delete/list/browse |
| `collSvc` | `*service.CollectionService` | `InitCollectionController` | Collection CRUD/entries/versions/CID |
| `shareSvc` | `*service.ShareService` | `InitShareController` | Share links |
| `anonSvc` | `*service.AnonService` | `InitAnonController` | Anonymous (content-addressed) collections |
| `pinSvc` | `*service.PinService` | `InitPinController` | IPFS pin |
| `peerPuller` | `*service.PeerPuller` | `InitPeerPuller` | Cross-node pull tasks |
| `nodeDir` | `*service.NodeDirectory` | `InitNodeDirectory` | Node marketplace (main forwards via `router.SetNodeDirectory`, `peerjs_routes.go:33-36`) |
| `nodeShareSvc` | `*service.NodeShare` | `InitNodeShareController` | This node's sharing scope |
| `peerShareSvc` | `*transport.PeerJSService` | `InitPeerShareController` | "Ask peer for sharing manifest" (share frame requester) |
| `forwardPeer` | `*transport.PeerJSService` | `InitForwardController` | Port forwarding (forward v2) |
| `btSvc` / `btClient` | `*p2p_bt.BTDHTService` / `*p2p_bt.BTClient` | `InitBTController` / `InitBTClient` | BT DHT / BT download |
| `ipfsGatewayProvider` | `*provider.IPFSProvider` | `InitIPFSProvider` | IPFS gateway fallback/fetch |
| `universalDownloader` | `*downloader.UniversalDownloader` | `InitUniversalDownloader` | Multi-protocol download pipeline |

The only struct-based controller is `SyncController` (`sync.go:12-18`, `NewSyncController` constructs and holds `syncSvc`); the rest are all package-level function handlers.

### 1.3 Main Flows

- **Upload flow** (`file.go:1-14` header comments): `multipart` → `MaxBytesReader` length limit (`file.go:42-44`, limit by `fileSvc.MaxUploadBytes(c)` based on auth status, see §4.4) → `fileSvc.Upload` (write CAS `storage/{h[:2]}/{h}` → INSERT `file_meta` → INSERT `file_providers`) → 201 + hash/size/mime/filename.
- **Download flow** (`download.go:1-8` header comments): Validate hash → `universalDownloader.Download(ctx, hash)` (pipeline `local → ipfsgw → btdht → http`, `back/internal/downloader/universal_downloader.go:3-14`; success writes back to local cache) → Set `X-Protocol`/`Content-Disposition` (toggle via `inline=1` parameter)/`Content-Encoding: gzip`/`X-Peerdrive-Collection` (`download.go:65-89`) → Handle `Range` (`handleRangeRequest`, supports standard/suffix/open-ended, `download.go:171-201`).
- **Collection CID pointer mechanism** (`collection.go:6-`): Collection's `current_hash` is a CID-like pointer to anonymous collection content; commit creates a new snapshot version; rollback points to previous version.

## 2. How It Stores

**Almost no persistence.** Controller is a pure request handler — it reads request parameters, validates them, delegates to services, and returns responses. The only "storage" is:

| Aspect | Details |
|--------|---------|
| State | Stateless (no persistent state between requests) |
| In-memory | Package-level service/downloader/transport references (set at startup, never changed) |
| Request-scoped | Gin context values (storageDir, request ID, auth token) |
| Persistence | Delegated to service layer → repository (SQLite) + storage (CAS files) |

### 2.2 Shared Scope and Joined Nodes JSON

Two JSON files are stored in `storageDir`:
- `joined_nodes.json`: List of joined marketplace nodes (peer IDs + timestamps)
- `share_scope.json`: Runtime sharing scope configuration (directories/files/collections/friends + levels)

Both are written by service layer (NodeDirectory/NodeShare), not directly by controller.

## 3. When It Stores

**Never directly.** All persistence is delegated:

| Request Type | Persistence Path |
|--------------|-----------------|
| File upload | Controller → FileService → repository (InsertFileMeta + InsertFileProvider) + storage (CAS write) |
| File delete | Controller → FileService → repository (delete meta + providers) + storage (delete blob) |
| Collection commit | Controller → CollectionService → repository (SaveAnon + UpdateCurrentHash + CreateVersion) + storage (CAS write) |
| Share link create | Controller → ShareService → repository (InsertShareLink) |
| Sync save | Controller → SyncService → repository (sync state) + downloader (file fetch) + storage (file write) |
| Pull task | Controller → PeerPuller → repository (file_index) + storage (file write) |
| IPFS pin | Controller → PinService → repository (InsertPin + InsertFileMeta + InsertFileProvider) + storage (file write) |
| BT download | Controller → BTClient → repository (register file) + storage (file write) |

## 4. What It Stores

**Nothing directly.** Controller is a pure translation layer between HTTP and internal services. It handles:

- **Request parsing**: Extracting path params, query params, headers, JSON bodies, multipart files
- **Validation**: Hash format, required fields, file size limits, auth tokens
- **Response formatting**: JSON responses, file downloads, error responses with proper HTTP status codes
- **Error mapping**: Converting service errors to HTTP status codes (400/401/403/404/500)

## 5. Boundaries and Pitfalls

- **No business logic**: Controller must not contain business logic. All logic belongs in service layer. Controller only does HTTP translation.
- **Package-level variable injection**: All dependencies are package-level vars set via `Init*` functions. This is not concurrency-safe if called multiple times, but is called exactly once at startup.
- **Auth via middleware**: Authentication is handled by router middleware (`AuthRequired`/`AuthOptional`), not by controllers. Controllers assume auth is already validated.
- **Error mapping is manual**: Each handler maps service errors to HTTP status codes manually. This is error-prone — a missed error type defaults to 500.
- **SyncController is the only struct**: It has `NewSyncController` constructor and holds `syncSvc`. All other controllers use package-level variables.
- **Range request handling**: `handleRangeRequest` supports standard RFC 7233 ranges, suffix ranges (`bytes=-500`), and open-ended ranges (`bytes=500-`). Must handle concurrent range requests correctly.
- **CORS is handled by middleware**: Controllers do not set CORS headers — the middleware chain handles it.

## 6. External Connections

- [../connections/02-router-controller.md](../connections/02-router-controller.md): Router registers all controller handlers and mounts auth middleware.
- [../connections/03-controller-service.md](../connections/03-controller-service.md): Controller delegates to service layer for all business operations.
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md): Download handlers call `universalDownloader.Download`/`CheckSources`/`ClearLocalCache`, multi-protocol pipeline caching results back to local storage.
- [../connections/10-controller-storage.md](../connections/10-controller-storage.md): Controller→storage. Controller gets `storageDir` from gin context (injected at `router.go:75-79`; consumed at `collection.go:171/382/494`, `p2p.go:575`), CAS path rule `storageDir/<hash[:2]>/<hash>`; sharing scope and joined nodes JSON also stored in storageDir (§2.2).
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Controller↔signaling REST. `/peerjs/node`, `/peerjs/nodes*` marketplace lists depend on signaling/discovery server's online node info (`peerjs_routes.go:74-105`, `node_market.go:99-126`).
- [../connections/06-service-transport.md](../connections/06-service-transport.md): Layering exception annotation. Controller has two **bypass-service direct `transport.PeerJSService`** injections: `forwardPeer` (port forwarding, `p2p.go:49-52`) and `peerShareSvc` (ask peer for sharing manifest, `node_market.go:44-47`); these are existing exceptions to the "controller only sees service" convention, with related data transfer semantics on the transport side.
