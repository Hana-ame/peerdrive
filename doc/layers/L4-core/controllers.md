# controller layer (back/internal/controller/)

> Layer belonging: AOP ④ business core (see doc/LAYERS.md §1).
> The "endpoint group" organizing layer for HTTP semantic business: each file is a group of
> related endpoints, doing only parameter binding/validation/response assembly —
> all business rules are delegated to service. It is unaware whether it is called directly by HTTP
> or forwarded internally by an admin verb
> (router.go injects the gin engine into transport via `SetAdminHandler`, REFACTOR.md §3.10).
> Actual route registration sites: `back/internal/router/router.go` (LEGACY HTTP route section) + `collection_dispatch.go`
> (/collections dispatcher) + `peerjs_routes.go` + `source_routes.go`.

**One-line responsibility**: translate an HTTP request into service calls — bind the body, validate
inputs, map error types to HTTP status codes, and assemble JSON/file responses; it writes no business logic and does not connect to repository directly (after the M2 layer collapse, only
injected dependencies such as `fileSvc` in download.go remain, see pitfall §7).

## Responsibilities

### What problem does it solve

The controller is the outermost layer of the business core (AOP ④): the router only handles the static path→handler mapping,
and the controller handles "request semantics" — what the request looks like (fields, validation rules) and what the response looks like (status
code, error body, headers). Business rules (hash validation, path defense, multi-source routing, transactions) live in service/source/
downloader, and the controller only orchestrates.

### Assembly pattern (two coexist)

Two dependency injection styles exist in the package; new code follows the style of the same file:

1. **Package-level variables + Init\* functions** (the majority): the `var anonSvc *service.AnonService` in
   `controller/anon.go:15` is injected by `InitAnonController(...)` at router.go:117. Same pattern: `InitFileController` (file.go:33),
   `InitCollectionController` (collection.go:42), `InitShareController` (share.go:17),
   `InitTaskController` (task.go:24), `InitPinController` (p2p.go:44),
   `InitUniversalDownloader` (download.go:35), `InitIPFSProvider` (download.go:41),
   `InitForwardController` (p2p.go:48), `InitBTController` (p2p.go:56), `InitBTClient` (p2p.go:66).
2. **Structures + New\* constructors**: `AuthController` (auth.go:13), `SyncController` (sync.go:12)
   are created directly by the router with `NewAuthController(...)` / `NewSyncController(...)`.

### Dual entry points (design intent, not a violation)

The same set of handlers serves two entry points (explicit in the comment section at router.go:176-184):

- **Direct HTTP**: curl / old frontend / integration tests
- **admin verb internal forwarding**: the browser sends a `{"type":"admin",...}` frame over `/ws/peer` →
  transport/admin.go constructs an *http.Request → injects it into this gin engine's ServeHTTP
  (router.go:357 `SetAdminHandler`) → all controllers are reused with zero duplicate implementation

The business layer only knows *http.Request and does not care about the origin (LAYERS.md §6). New frontend code is forbidden from directly fetching these
endpoints (the forbidden zone table in LAYERS.md §5).

### Authentication strategy

`authRequired` (router.go:92, auth_middleware.go) is attached to all mutating/admin routes:
- when no registration server is configured (RegistrationServer empty), it is internally allowed (local single-machine mode)
- once configured, it requires `Authorization: Bearer <authkey>`
- read interfaces (GET/download, anon GET, /files GET, /s/:token, /ws/peer) stay public

## Module inventory (endpoint groups)

### 1. auth.go — authentication (user system)

| File | One-line responsibility | Key exports |
|---|---|---|
| auth.go | Register/login/logout/current user | `AuthController` (`Register`/`Login`/`Logout`/`Me`), `NewAuthController` |

Routes (registered by router.go; note: **this group has no routes registered in router.go** — the registration server is an external
service (RegistrationServer) and authkeys are issued externally; these handlers are called directly by integration tests/external assembly,
see the "no route registration" note at doc/archive/LEGACY.md line 61):

| Method/path | handler | Description |
|---|---|---|
| POST /auth/register | `Register` | body `RegisterRequest` (username/password) → `AuthResponse` |
| POST /auth/login | `Login` | bcrypt verification; on failure 401 `ErrInvalidCredentials` |
| POST /auth/logout | `Logout` | clear the session by Bearer authkey |
| GET /auth/me | `Me` | `ValidateKey` returns user info |

Call relationships: all delegated to `service.AuthService` (Register/Login/Logout/ValidateKey).

### 2. anon.go — anonymous collections (content-addressed)

| File | One-line responsibility | Key exports |
|---|---|---|
| anon.go | Anonymous collection CRUD/download/fork/commit | `CreateAnonCollection`, `ListAnonCollections`, `GetAnonCollection`, `DownloadAnonFile`, `ForkAnonCollection`, `CommitAnonCollection`, `InitAnonController` |

Routes (router.go:271-279, the /anon group; write operations under authRequired):

| Method/path | handler | Description |
|---|---|---|
| POST /anon/collections | `CreateAnonCollection` | body `{friendly_name, entries:[{path,hash,providers}], tags}` → 201 `{hash}`; illegal path/hash/provider → 400 |
| GET /anon/collections | `ListAnonCollections` | Summary list of anonymous collections known to this node |
| POST /anon/collections/commit | `CommitAnonCollection` | Commit a new version based on source_hash (an entry with an empty hash = delete the entry) → 201 `{hash}` |
| GET /anon/collections/:hash | `GetAnonCollection` | Collection metadata + entries |
| GET /anon/collections/:hash/*filepath | `DownloadAnonFile` | Download in provider order: sha256 (via universalDownloader) → url 302 redirect |
| POST /anon/collections/fork | `ForkAnonCollection` | Source collection ± add/remove entries → a new collection hash |

Download semantics (anon.go:125-155): entry `GetPrimaryHash()` first → `universalDownloader.Download`,
on success set the `X-Protocol` header (`?inline=1` controls Content-Disposition); on failure, degrade to the url provider's 302.

Call relationships: `service.AnonService` (CreateCollection/GetCollectionByHash/ListCollections/
CommitCollection) + `downloader.UniversalDownloader` (package-level `universalDownloader`).

### 3. collection.go — user collections (CRUD + versions)

| File | One-line responsibility | Key exports |
|---|---|---|
| collection.go | User collection CRUD, entry add/remove, version commit/log/rollback, visibility, tags | `CreateCollection`, `ListCollections`, `SearchCollections`, `GetCollection`, `AddEntry`, `RemoveEntry`, `DownloadCollectionFile`, `CommitCollection`, `GetVersionLog`, `RollbackCollection`, `SetCollectionVisibility`, `ListPublicCollections`, `UpdateCollectionTags`, `InitCollectionController` |

Routes (router.go:296-307 admin group + collection_dispatch.go dispatch group + router.go:327 wildcard download):

| Method/path | handler | Description |
|---|---|---|
| GET /collections/public | `ListPublicCollections` | visibility='public', `?q=` filter |
| GET /collections/search | `SearchCollections` | Fuzzy search |
| POST /collections | `dispatchCreateCollection` | Dispatcher: body contains username → `CreateCollection` (user system); otherwise `CreateAnonCollection` (anonymous) |
| POST /collections/fork\|merge\|pull | `ForkAnonCollection`/`MergeFromSource`/`PullCollection` | See fork.go/merge.go |
| POST /collections/upload, /register-local, /register-url, /register-folder | Reuse file.go handlers | The frontend uses the unified /collections path prefix |
| GET /collections/:id | `dispatchGetCollection` | 64hex → `GetAnonCollection`; otherwise `ListCollections` by username |
| GET /collections/:id/*filepath | `dispatchGetTree` | 64hex → anonymous file download; otherwise dispatch by segment count to `ListCollections`/`GetCollection`/`GetVersionLog` (gin does not allow :param and *wildcard to coexist, so they are unified into one; see the pitfall comment at collection_dispatch.go:56-58) |
| POST /collections/:id/:collection_name/entries | `AddEntry` | body `{path, hash \ | providers}`; the collection is auto-created if it does not exist (GetOrCreate) |
| DELETE /collections/:id/:collection_name/entries/*path | `RemoveEntry` | TrimPrefix "/" on path |
| POST /collections/:id/:collection_name/commit | `CommitCollection` | 5 steps: ListEntries → build AnonCollection → SaveAnon generates the CID (current_hash) → CreateVersion+SnapshotEntries snapshot |
| GET /collections/:id/:collection_name/log | via `dispatchGetTree` | Version history (newest first) |
| POST /collections/:id/:collection_name/rollback/:version_id | `RollbackCollection` | RestoreVersion + regenerate the CID to update current_hash |
| POST /collections/:id/:collection_name/visibility | `SetCollectionVisibility` | One of public/unlisted/private |
| POST /collections/:id/:collection_name/tags | `UpdateCollectionTags` | Full tag replacement |
| GET /:username/:collection_name/*filepath | `DownloadCollectionFile` | Look up the entry → sha256 provider → `DownloadBySHA256Internal`; a url provider does 302 or returns `{url, follow_redirects:false}` JSON depending on `col.FollowRedirects` |

CID pointer mechanism (collection.go:6-11 header comment): at Commit, besides the version snapshot, the entries are built as
an AnonCollection JSON and stored in content-addressed storage with `collections.current_hash` recorded;
`GetCollection` prefers `collSvc.GetAnonByHash` to read the CID JSON and return entries (full providers),
falling back to the `collection_entries` table only on failure.

Call relationships: `service.CollectionService` (a pure forwarding layer) — unrelated to `service.AnonService`:
`GetAnonByHash`/`SaveAnon` are forwarded through CollectionService to the repository.

### 4. download.go — content-addressed download (multi-protocol)

| File | One-line responsibility | Key exports |
|---|---|---|
| download.go | sha256/CID download, Range requests, the generic multi-protocol download endpoint | `DownloadBySHA256`, `DownloadBySHA256Internal`, `DownloadBySHA256Local`, `DownloadByCID`, `UniversalDownload`, `UniversalDownloadSources`, `UniversalDownloadRefresh`, `InitUniversalDownloader`, `InitIPFSProvider` |

Routes (router.go:186-194):

| Method/path | handler | Description |
|---|---|---|
| GET /sha256sum/:sha256 | `DownloadBySHA256Local` | Local storage direct read only (no multi-protocol fallback), `X-Content-Encoding: gzip` supported |
| GET /sha256sum/:sha256/:filename | Same as above | Variant with a filename (same handler) |
| GET /ipfs/:cid | `DownloadByCID` | `GetMetaByCID` local lookup → miss with an IPFS gateway configured → `ipfsGatewayProvider.FetchByCID` → `fileSvc.ImportGatewayData` writes and registers → then `DownloadBySHA256Internal`; a hit sets the `X-CID` header |
| GET /download/:hash | `UniversalDownload` | `universalDownloader.Download` → `X-Protocol` header |
| GET /download/:hash/sources | `UniversalDownloadSources` | `CheckSources` (30s timeout) |
| POST /download/:hash/refresh | `UniversalDownloadRefresh` | authRequired; reruns the pipeline after `ClearLocalCache` |

Range support (download.go:164-201 `handleRangeRequest` + 275-326 `parseRangeHeader`):
standard `bytes=N-M`, suffix `bytes=-N`, open-ended `bytes=N-`, out-of-range 416 `Content-Range: bytes */total`,
and end-out-of-range clamped to the file tail. `parseRangeHeader` origin note: migrated from legacy/relay.go's ParseRange in batch 2.

Call relationships: `downloader.UniversalDownloader` + `service.FileService` (GetMeta/GetMetaByCID/
ImportGatewayData) + `provider.IPFSProvider`.

### 5. file.go — file management (upload/register/verify/delete/copy/browse)

| File | One-line responsibility | Key exports |
|---|---|---|
| file.go | File upload and local registration, metadata verification, deletion, copy, directory browsing, version diff, listing | `UploadFile`, `RegisterURL`, `RegisterLocalFile`, `RegisterFolder`, `VerifyFile`, `DeleteFile`, `CopyFile`, `DiffVersions`, `ListFiles`, `BrowseDir`, `InitFileController` |

Routes (router.go:282-294, the /files group; write operations under authRequired):

| Method/path | handler | Description |
|---|---|---|
| GET /files | `ListFiles` | `?sort=time\|name\|path\|type\|size`, `fileSvc.ListAll` |
| POST /files/upload | `UploadFile` | multipart `file` field; `MaxBytesReader` rate-limits by authentication state (`MaxUploadBytes`/`MaxUploadBytesAnon`); 409 semantics: an existing file returns 200 + `already_exists:true`; storage disabled → 403 |
| POST /files/register_local | `RegisterLocalFile` | body `{path, filename}` |
| POST /files/register_url | `RegisterURL` | body `{url, filename}`, auto-follows redirects |
| POST /files/register_folder | `RegisterFolder` | body `{folder_path}`, recursively registers all files |
| GET /files/verify/:hash | `VerifyFile` | Metadata lookup (400 illegal hash / 404 not found) |
| GET /files/browse | `BrowseDir` | `?path=`; both empty and "/" map to the storage root (the frontend file manager defaults to "/") |
| DELETE /files/:hash | `DeleteFile` | Delete the local file + file_meta + file_providers |
| POST /files/copy | `CopyFile` | body `{hash, dest_path}`, reads the source via `ReadFile` then writes the destination |
| POST /files/diff | `DiffVersions` | body `{version_a, version_b}`, returns three groups: added/removed/modified |

Call relationships: `service.FileService` (Upload/RegisterLocal/RegisterFolder/RegisterURL/Verify/
Delete/CopyFile/ReadFile/MaxUploadBytes/ListAll/BrowseDir) + `service.CollectionService`
(DiffVersions' VersionEntries).

### 6. fork.go — fork / pull

| File | One-line responsibility | Key exports |
|---|---|---|
| fork.go | Collection fork (Fork), upstream pull (Pull placeholder) | `ForkCollection`, `PullCollection` |

Routes (router.go:320-324, the /actions group; authRequired):

| Method/path | handler | Description |
|---|---|---|
| POST /actions/fork | `ForkCollection` | Look up the source collection → `CreatePlain` the target → copy entry by entry via `AddProviderEntry`/`AddEntry`; 409 if the target already exists |
| POST /actions/pull | `PullCollection` | Placeholder: returns "not implemented", then creates a transfer_tasks record marked completed (a no-op marked deletable in doc/archive/LEGACY.md) |

Call relationships: `service.CollectionService` + `service.TaskService` (PullCollection's fake task).

### 7. merge.go — merge (three strategies)

| File | One-line responsibility | Key exports |
|---|---|---|
| merge.go | Merge a source collection into a local one, with ours/theirs/manual strategies | `Conflict`, `MergeFromSource` |

Routes (router.go:262,321: /collections/merge and /actions/merge share the same handler):

| Method/path | handler | Description |
|---|---|---|
| POST /collections/merge, POST /actions/merge | `MergeFromSource` | body `{username, collection_name, source_username, source_coll_name, strategy}` |

Conflict semantics (merge.go:93-123): compare the providers' sha256 primary hash by path;
`manual` with conflicts → 409 + `{conflicts:[{path, local_hash, source_hash, local_providers, source_providers}]}`;
newly added entries are adopted directly at merge time, and conflicting entries follow the strategy (theirs takes the source / ours keeps the local by default).

Call relationships: `service.CollectionService`.

### 8. p2p.go — P2P/BT/IPFS/pin/port forwarding (1092 lines, the largest endpoint group)

| File | One-line responsibility | Key exports |
|---|---|---|
| p2p.go | A collection of several sub-endpoint groups: BT DHT (announce/find/bep44/bep51), BT downloads (torrent/magnet/progress/seeding), BT collection seeding, port forwarding v2 (the HTTP surface), auth status, IPFS pin, gateway health | `InitBTController`/`InitBTClient`/`InitPinController`/`InitForwardController` + about 30 handlers |

Sub-group routes (router.go:196-244):

**/p2p** (after batch-2 slimming, only auth status/WebRTC info/port forwarding remain):

| Method/path | handler | Description |
|---|---|---|
| GET /p2p/auth/status | `AuthStatus` | Reads authenticated/username/role from the gin context |
| GET /p2p/webrtc/info | `WebRTCInfoHandler(cfg)` | STUN/TURN configuration (see webrtc.go) |
| POST /p2p/forward/create | `CreateForwardSession` | authRequired; `{key, port}` → `AddForwardRule` (runtime registration, not persisted) |
| POST /p2p/forward/connect | `ConnectForwardSession` | authRequired; `{key, target_peer, local_port, port?}` → starts a listener on 127.0.0.1, idempotent (an existing listener with the same key returns directly) |
| GET /p2p/forward/list | `ListForwardSessions` | `{listeners, tunnels}` (tunnels come from `ListForwardStreams`) |
| POST /p2p/forward/close | `CloseForwardSession` | authRequired; closes the listener by key, drops the tunnel by peer_id |

Forwarding mechanism (p2p.go:713-847): the package-level map `fwdListeners` manages listeners; `acceptForwardTunnels`
does, for each local TCP, `forwardPeer.OpenForward(ctx, targetPeer, key, targetPort)` (30s timeout) →
`pipeTCPForward` for bidirectional passthrough, closing both directions on either side's EOF.

**/bt** (depends on the standalone p2p_bt library; the controller is a thin wrapper):

| Method/path | handler | Description |
|---|---|---|
| GET /bt/status | `BTDHTStatus` | `{enabled, listen_addr, num_nodes, node_id}`; if disabled, 200 `{enabled:false}` |
| POST /bt/announce | `BTAnnounce` | `btSvc.Announce(hash)` |
| POST /bt/find | `BTFindProviders` | `btSvc.FindProviders(hash)` |
| POST /bt/bep44/put | `BEP44Put` | base64 data; mutable is rejected with 501 (the API does no key management); immutable → `PutImmutable` |
| POST /bt/bep44/get | `BEP44Get` | 40hex target → `GetImmutable` |
| GET /bt/bep51/sample | `BEP51Sample` | `DiscoverInfohashes(200)` |
| POST /bt/torrent | `BTTorrentUpload` | multipart `torrent` → `AddTorrentBytes` |
| POST /bt/magnet | `BTMagnetResolve` | `AddMagnetURI` |
| GET /bt/downloads | `BTDownloadList` | `ListDownloads` |
| GET /bt/download/:infohash | `BTDownloadProgress` | `GetDownload` |
| GET /bt/download/:infohash/torrent | `BTDownloadTorrent` | .torrent file, with the filename cleaned of illegal characters (p2p.go:563-570) |
| GET /bt/download/:infohash/magnet | `BTDownloadMagnet` | magnet URI |
| POST /bt/download/:infohash/pause\|resume\|seed\|unseed | `BTPauseDownload` etc. | Task control |
| DELETE /bt/download/:infohash | `BTRemoveDownload` | Delete the task and its files |
| GET /bt/stats | `BTGlobalStats` | `GetGlobalStats` + `ListSeeders` |
| POST /bt/seed-collection | `BTSeedCollection` | Collection → assemble files in a temp directory → `BuildFromFilePath` to build the torrent → write data to the BT download directory → `AddTorrentBytes` + `SetAutoSeed` |

**/ipfs** (HTTP gateway pin):

| Method/path | handler | Description |
|---|---|---|
| POST /ipfs/pin/:cid | `PinCID` | Return directly if already pinned; otherwise `FetchByCID` (60s timeout) → write to CAS → `pinSvc.InsertMeta`+`Insert` |
| DELETE /ipfs/pin/:cid | `UnpinCID` | `pinSvc.Get`/`Remove` |
| GET /ipfs/pins | `ListPins` | `pinSvc.List` |
| GET /ipfs/gateways | `IPFSGatewayStatus` | HEAD probe each gateway (5s timeout), `/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn` is a permanent test node |

Call relationships: `p2p_bt.BTDHTService` / `p2p_bt.BTClient` (external capability aspect ⑦),
`service.PinService`, `transport.PeerJSService` (forward), `service.CollectionService`
(BTSeedCollection's GetAnonByHash), `provider.IPFSProvider` (package-level ipfsGatewayProvider).

### 9. ping.go — health check

| File | One-line responsibility | Key exports |
|---|---|---|
| ping.go | Health check | `Ping` |

Route: GET /ping → 200 "pong" (router.go:186). No dependencies.

### 10. share.go — share links

| File | One-line responsibility | Key exports |
|---|---|---|
| share.go | Create/access/list share links | `CreateShare`, `AccessShare`, `ListShares`, `InitShareController` |

Routes (router.go:337-342; create/list under authRequired, access by token is public):

| Method/path | handler | Description |
|---|---|---|
| POST /shares | `CreateShare` | body `{hash, type: file\|collection, filename}` → `{token, hash, type, filename, url:"/s/"+token, expires}` (30 days) |
| GET /s/:token | `AccessShare` | collection → 302 `/anon/collections/{hash}`; file → 302 `/sha256sum/{hash}` |
| GET /shares | `ListShares` | List of non-expired shares (up to 100) |

Call relationships: `service.ShareService`.

### 11. sync.go — local sync

| File | One-line responsibility | Key exports |
|---|---|---|
| sync.go | Sync collection files to local disk + status query | `SyncController` (`SaveLocal`/`GetStatus`), `NewSyncController` |

Routes (router.go:310-314, the /local group):

| Method/path | handler | Description |
|---|---|---|
| POST /local/save | `SaveLocal` | body `SaveLocalRequest` (collection_hash/local_path/include/exclude) → `syncSvc.SaveToDisk` |
| GET /local/status/:hash | `GetStatus` | `syncSvc.GetStatus` → list of saved/missing files |

Call relationships: `service.SyncService` (including `downloader.UniversalDownloader` injection, see services.md).

### 12. task.go — asynchronous tasks (placeholder)

| File | One-line responsibility | Key exports |
|---|---|---|
| task.go | Task status query (placeholder) | `GetTaskStatus`, `ListTasks`, `InitTaskController` |

Routes (router.go:330-334, the /tasks group):

| Method/path | handler | Description |
|---|---|---|
| GET /tasks | `ListTasks` | Always returns an empty array (marked deletable in doc/archive/LEGACY.md) |
| GET /tasks/:id | `GetTaskStatus` | `taskSvc.Get` (transfer_tasks table) |

Call relationships: `service.TaskService`.

### 13. webrtc.go — WebRTC configuration

| File | One-line responsibility | Key exports |
|---|---|---|
| webrtc.go | STUN/TURN configuration delivery | `WebRTCInfoHandler(cfg)` (a factory function returning a handler) |

Route: GET /p2p/webrtc/info → `{stun_server, turn_server?}`. Depends on `config.Config`.

## Key mechanisms

### 1. Init\* package-level variable injection

Historical baggage: the controller package uses package-level mutable globals (`anonSvc`/`fileSvc`/`collSvc`/`universalDownloader`/
`btSvc`/`forwardPeer`...) + `Init*` injection, assembled uniformly in SetupRouter at router.go:94-174.
New code should prefer the `NewXxxController` struct style (auth.go/sync.go) to avoid global state.

### 2. Error→status-code mapping (no unified error middleware)

Each handler maps status codes itself based on the error returned by the service; conventions:

| Case | Status code |
|---|---|
| Binding failure / illegal parameter | 400 |
| Authentication failure | 401 / 403 (storage disabled) |
| Resource not found | 404 |
| Duplicate create / merge conflict | 409 (merge's manual conflict also goes to 409 + a conflicts list) |
| Dependency not assembled (universalDownloader nil, etc.) | 503 |
| Other | 500 |

### 3. Unified download entry point `DownloadBySHA256Internal`

`DownloadCollectionFile` in collection.go and `DownloadByCID` in download.go both converge on
`DownloadBySHA256Internal` (download.go:51) — one function unifies the X-Protocol header, filename,
inline/attachment, gzip Content-Encoding, and Range handling.

## Relationships with other modules

```
router (route registration + Init* assembly + the admin internal forwarding entry)
  ↓ calls
controller (this layer)
  ├→ service (FileService/AnonService/CollectionService/ShareService/TaskService/
  │          PinService/SyncService/AuthService)
  ├→ downloader.UniversalDownloader (download endpoints + anon file download)
  ├→ provider.IPFSProvider (gateway fallback / pin / health checks)
  ├→ transport.PeerJSService (the HTTP surface for port forwarding)
  └→ p2p_bt (BTDHTService/BTClient, external capability aspect)
```

- **No reverse dependency**: controller does not import repository (the M2 layer collapse is achieved, REFACTOR.md §7 M2).
- **Dual entry points**: admin frames → gin engine ServeHTTP (router.go:357) reuse all handlers in this layer.
- **Boundary with ②**: controller does forwarding through `transport.PeerJSService`'s semantic APIs (OpenForward/
  AddForwardRule/ListForwardStreams/CloseForwardStream) and does not manipulate connections directly.

## Pitfalls and design decisions

1. **The race surface of package-level global injection**: Init\* is called serially inside SetupRouter and does not change at runtime; but tests must
   Init before calling a handler, otherwise there is a nil dereference (the setupFileTestRouter pattern in file_test.go).
2. **gin does not allow :param and *wildcard to coexist** (collection_dispatch.go:56-58): all deep
   GETs in the user system are merged into one `/:id/*filepath` wildcard route with internal dispatch; `withParams` uses
   `c.Copy()` + appended Params to supply parameter names, with zero changes to the controller (REFACTOR.md §3.1).
3. **/collections dual-semantics dispatch** (dispatchCreateCollection/dispatchGetCollection): the same path
   dispatches the anonymous (hash-addressed) and user (username-addressed) systems by body/path shape — the
   fix for a route registration conflict (REFACTOR.md §3.1, the startup panic before deleting the legacy redirect).
4. **Anonymous download is a "downloader + redirect" dual chain** (anon.go:125-155): the sha256 main chain (universalDownloader)
   degrades to a url 302 on failure — when collection.go's `FollowRedirects` is false, it instead returns JSON to let the frontend
   decide (against SSRF-style following).
5. **The 64MB cap on /peerjs/fetch** (peerjs_routes.go:69-92, H4): the whole response buffer resides in memory,
   so it hangs authentication + limits size; oversized files go over /ws/peer chunks. The frontend does not use this endpoint.
6. **The 409 semantics of /files/upload**: an existing file returns 200 + `already_exists:true` rather than 409
   (idempotent upload, so the frontend can keep using the hash).
7. **BTSeedCollection's full in-memory/on-disk copy** (p2p.go:615-709): collection entries are each ReadFile +
   WriteFile twice (the temp directory + the BT data directory) — the memory peak for large collections is high, a known limitation (TODO:
   could be streamed).
8. **p2p.go carries legacy endpoints** (doc/archive/LEGACY.md section A): libp2p-era endpoints were deleted (REFACTOR.md §8
   batch 2, 2026-08-16); p2p.go now keeps the three BT/IPFS/forward sub-groups, where forward is the new implementation
   (REFACTOR.md §3.9) and BT/IPFS depend on the external capability aspect ⑦.
9. **auth.go/task.go's ListTasks has no route registration / is always empty** (doc/archive/LEGACY.md lines 61-62): kept as placeholders.

## Tests (94 unit tests, including controller/service/source/downloader; the L4 section of `scripts/test-layers.sh`)

> Command: `go test -tags nosqlite ./internal/controller/... ./internal/service/... ./internal/source/... ./internal/downloader/...`

> Controller tests are all legacy-marked except `TestBrowseDir_SlashMeansStorageRoot`
> (file header comment: "This file belongs to legacy code… backgrounds of discovery are not annotated individually").

| File | Test | Background of discovery |
|---|---|---|
| collection_test.go | `TestCreateCollection_Valid/Duplicate/InvalidBody`, `TestListCollections`, `TestGetCollection(_NotFound)`, `TestAddEntry(_CreatesCollectionIfNotExists)`, `TestRemoveEntry(_CollectionNotFound)`, `TestCommitCollection`, `TestGetVersionLog`, `TestSearchCollections`, `TestForkCollection(_SourceNotFound)`, `TestRollbackCollection`, `TestCreateCollectionWithVisibility(_WithPrivateVisibility)`, `TestSetCollectionVisibility`, `TestListPublicCollections`, `TestListCollectionsForUserReturnsAllVisibilities`, `TestInvalidVisibilityRejected` | legacy (gin calls handlers directly, covering the CRUD/version/visibility main chain and the 404/409/400 branches) |
| download_test.go | `TestHandleRangeRequest_StandardRange/MidRange/SuffixRange/OpenEndedRange/ZeroByteFile/RangeBeyondFile/EndBeyondFile/SuffixLargerThanFile` | legacy (regression for the pure function parseRangeHeader migrated from legacy/relay.go, covering the 206/416/clamp boundaries) |
| file_test.go | `TestListFiles_Empty(_WithSort)`, `TestVerifyFile_NotFound(_InvalidHash)`, `TestBrowseDir_DefaultRoot(_SpecificPath)`, `TestUploadFile_NoFile`, `TestCopyFile_MissingParams(_InvalidHash)` | legacy |
| file_test.go | `TestBrowseDir_SlashMeansStorageRoot` | **After the security boundary was tightened, BrowseDir rejects out-of-root paths, but the frontend file manager defaults to passing "/", causing a persistent 400**; fix: both the empty path and "/" map to the storage root (comment at file.go:346-348) |
| ping_test.go | `TestPing` | legacy (health check smoke) |

## File inventory

```
back/internal/controller/
├── anon.go             anonymous collections (Create/List/Get/Download/Fork/Commit)
├── auth.go             AuthController (Register/Login/Logout/Me, no route registration)
├── collection.go       user collection CRUD + entries + versions + visibility + tags
├── collection_test.go  collection CRUD/version/visibility tests (legacy mark)
├── download.go         sha256/CID download + Range + the generic multi-protocol download endpoint
├── download_test.go    Range handling tests (legacy mark)
├── file.go             upload/register/verify/delete/copy/browse/list/diff
├── file_test.go        file management tests (includes 1 background-of-discovery entry)
├── fork.go             fork (Fork) + pull (Pull placeholder)
├── merge.go            merge (ours/theirs/manual strategies)
├── p2p.go              BT DHT/BT downloads/port forwarding v2/pin/gateway health (1092 lines)
├── ping.go             health check
├── ping_test.go        Ping test
├── share.go            share links (Create/Access/List)
├── sync.go             SyncController (SaveLocal/GetStatus)
├── task.go             task query (ListTasks placeholder)
└── webrtc.go           STUN/TURN configuration delivery
```
