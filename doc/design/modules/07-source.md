# Module 07: source File Source Management

- **Code location**: `back/internal/source`
- **One-line function**: Unified "content-addressed byte stream" retrieval abstraction — local disk (file_index mapping + content-addressed storage fallback), p2p peer (transparent racing), URL/HTTP template — three source types registered in `Manager` registry, routed by priority per source; also provides capability-split control plane (local add/write file, BT torrent/magnet download, IPFS pin/gateway status), and implements transport's `FileRouter` interface for peer req multi-source fallback.
- **Dependencies**: `internal/transport` (`FileIndexService`, `PeerJSService`, direction source→transport, transport does not reverse-depend on this package, see `back/internal/source/source.go:16-17`); `internal/provider` (IPFSProvider, `back/internal/source/ipfs_control.go:25`); `internal/repository` (InsertPin/InsertFileMeta/InsertFileProvider etc., `back/internal/source/ipfs_control.go:56-70`); `github.com/Hana-ame/go-peerdrive-bt` (BTClient, `back/internal/source/bt_control.go:7-13`); `internal/pathutil` (SafeOpen, `back/internal/source/local.go:104`); `internal/log`, `internal/model`, `pkg/hashutil`.
- **Depended upon by**: `internal/serverapp/app.go` (assembly: Register three source types + SetSourceManager + SetFileRouter, `back/internal/serverapp/app.go:214-233`); `internal/router` (`/sources` management endpoints + inject BT/IPFS control plane, `back/internal/router/source_routes.go`, `back/internal/router/router.go:150-196`); `internal/transport` (uses Manager via `FileRouter` interface, `back/internal/transport/inbound.go:42-51`, `back/internal/transport/peerjs_service.go:535-537`).

## 1. Logic

### 1.1 Core Abstraction: Source Interface and Capability Flags

Unified file retrieval abstraction (`back/internal/source/source.go:1-18`): Anything that can provide "content-addressed byte streams" is a Source — local disk, p2p peer (transparent), URL/HTTP (can go through ech-proxy etc. exit), IPFS gateway. Upper layer only asks "give me content for this hash", doesn't care about source and network path.

- `Source` interface (`back/internal/source/source.go:51-72`): `Name()` (registry key) / `Type()` (local/peer/url/ipfs classification) / `Capabilities()` / `Priority()` / `SetPriority()` / `Available()` (soft health check, false means router skips) / `Open(ctx, hash, offset, size)` (streaming, requires CapStream) / `Fetch(ctx, hash)` (whole, requires CapFile) / `Info(ctx, hash)` (optional metadata; returns `nil, nil` if unsupported).
- `Capability` bit flags (`back/internal/source/source.go:30-39`): `CapFile` (whole fetch) and `CapStream` (streaming/chunked read) can be combined. Large files must use CapStream — 8GB full buffer would OOM (same file comments, `source.go:8-10`, also see `manager.go:150-152`).
- Utility functions `IsStream`/`IsFile` (`source.go:75-78`), `FileMeta` (`Hash/Size/Name/Path`, `Path` only meaningful for local, `source.go:42-48`), unified entry defense `validHash` (must be 64-char lowercase hex, `source.go:100-106`, `back/pkg/hashutil/hashutil.go:60-62`).
- Unified stats `Stats` (Success/Fail/Bytes/LastErr/LastAt, `source.go:81-87`) and management snapshot `SourceStatus` (JSON-ified, `source.go:90-98`).

### 1.2 SourceManager Registry and Routing

`Manager` (`back/internal/source/manager.go:27-39`) holds:

- `sources []Source`: Sorted by priority ascending (re-sort on register, priority change via `sortLocked`, `manager.go:55-58,118-136`);
- `stats map[string]*Stats`: Cumulative stats per source name;
- `ipfsControl IPFSControl / btControl BTControl`: Optional control plane instances (nil means disabled), **held per instance** — historically misused package-level global var causing multi-instance sharing, changed to field so each Manager is independent (`manager.go:33-39` comments + `source_test.go:410-427` regression test).

Registry operations: `Register` (reject duplicate name, `manager.go:47-59`), `Unregister` (`manager.go:62-73`), `Get` (`manager.go:106-115`), `SetPriority` (runtime adjustment and re-sort, `manager.go:118-129`).

Unified file retrieval entry (routing semantics, `manager.go:9-14`):

- `Open/OpenRange`: Try each source by priority ascending; `Available()==false` skip and log (`manager.go:166-168,196-198`); **only try CapStream sources**, CapFile sources without chunk capability are skipped (`manager.go:153-182`); all fail returns aggregated error (with per-source failure reason, `manager.go:181`).
- `OpenAny`: Prefer CapStream streaming, all fail falls back to CapFile whole fetch (in-memory, suitable for small files/metadata, `manager.go:186-224`).
- `Info`: Try by priority for sources supporting Info, first hit returns (`manager.go:227-252`).
- `InfoSize`: Converges `FileMeta` to scalar size for transport adaptation (`manager.go:254-263`, avoids import cycle issue).
- `Snapshot`: Status + stats for each source, data source for `GET /sources` (`manager.go:266-287`).
- `record`: Logs stats for each attempt (success/fail/bytes/time, `manager.go:290-309`).

### 1.3 Four Source Implementations

**LocalSource** (`back/internal/source/local.go`) — local disk source, semantics completely consistent with transport.serveFile path decisions (same logic converged to one place, `local.go:3-8`):
- Path decision `resolvePath` (`local.go:76-87`): file_index hit and path readable → read mapped path; otherwise → content-addressed storage `storageDir/<hash[:2]>/<hash>`.
- Open via `pathutil.SafeOpen` (os.Root anchored, prevents TOCTOU where registered path gets replaced by symlink, `local.go:90-105`).
- `Open` streaming chunks: offset<0→0, out-of-bounds clamp, `io.LimitReader` length limit (`local.go:109-140`); local file already SHA-256 verified on write (upload Complete), Open does not re-verify (`local.go:7-8`).
- `Info`: Reads file size via `os.Stat`, `FileMeta.Path` set to resolved path (`local.go:143-155`).
- `Available()`: `storageDir != ""` (`local.go:157-159`).
- `Priority`: Fixed at 0 (highest, local preferred, `local.go:161-163`).

**PeerSource** (`back/internal/source/peer.go`) — p2p peer source (transparent pass-through):
- `Capabilities`: `CapStream | CapFile` (both supported, `peer.go:22-24`).
- `Open`: Calls `FileRouter.OpenStream` for peer req streaming (`peer.go:26-38`);
- `Fetch`: Calls `FileRouter.Fetch` for whole fetch (`peer.go:40-48`);
- `Info`: Returns nil (no metadata available from peer, `peer.go:50-53`);
- `Available()`: `router != nil` (`peer.go:55-57`);
- `Priority`: Default 100 (lower than local, adjustable at runtime, `peer.go:59-67`).

**URLSource** (`back/internal/source/url.go`) — URL/HTTP template source:
- Template format: URL template with `{hash}` placeholder (e.g. `https://cdn.example.com/files/{hash}`), `url.go:4-7`.
- `Capabilities`: `CapStream | CapFile` (both supported, `url.go:25-27`).
- `Open`: HTTP GET with Range header, streams response body (`url.go:39-63`); supports custom Transport for ech-proxy exit (`url.go:44-47`).
- `Fetch`: HTTP GET whole body (`url.go:65-84`);
- `Info`: HTTP HEAD for Content-Length (`url.go:86-101`);
- `Available()`: Template non-empty (`url.go:103-105`).
- `Priority`: Default 200 (below local and peer, `url.go:107-115`).
- Supports `SetTemplate` for runtime template change (`url.go:117-120`).

**IPFSControl** (`back/internal/source/ipfs_control.go`) — IPFS control plane:
- `PinCID(ctx, cid, filename)`: Fetches gateway data via `IPFSProvider.FetchByCID`, writes to pin cache file, registers in repository (`ipfs_control.go:34-73`).
- `UnpinCID(ctx, cid)`: Deletes pin from repository (`ipfs_control.go:75-82`).
- `ListPins()`: Returns all pinned CIDs from repository (`ipfs_control.go:84-93`).
- `GatewayStatus()`: Returns gateway configuration and reachability (`ipfs_control.go:95-103`).

**BTControl** (`back/internal/source/bt_control.go`) — BT control plane:
- `DownloadTorrent(ctx, torrentData)`: Creates BT download task via `BTClient` (`bt_control.go:15-42`).
- `DownloadMagnet(ctx, magnetURI)`: Creates magnet link download task (`bt_control.go:44-67`).
- `ListDownloads()`: Returns active BT downloads (`bt_control.go:69-78`).
- `Available()`: `btClient != nil` (`bt_control.go:80-82`).

### 1.4 FileRouter Interface

`FileRouter` (`back/internal/source/manager.go:14-25`) is the interface implemented by `Manager` and injected into transport:

- `OpenStream(ctx, hash, offset, size)`: Streaming read with offset/size
- `Fetch(ctx, hash)`: Whole content fetch
- `Info(ctx, hash)`: Metadata query
- `Size(ctx, hash)`: Size query (scalar)

Transport's `serveFile` uses `FileRouter` for multi-source fallback when handling peer `req` frames (`back/internal/transport/inbound.go:42-51`).

---

## 2. How It Stores

**No persistent storage.** The source module is a pure routing/abstraction layer:

| Aspect | Details |
|--------|---------|
| State | Registry of source instances + stats (in-memory, lost on restart) |
| Persistence | None — all data access delegated to local disk / peer / URL / IPFS |
| Stats | Per-source cumulative success/fail/bytes counters (in-memory) |
| Control plane | BT/IPFS control instances held per-Manager (not persisted) |

The source module does not own any storage. It reads from:
- LocalSource: Local filesystem (content-addressed storage)
- PeerSource: Peer nodes (via WebRTC/WebSocket transport)
- URLSource: HTTP/HTTPS endpoints (via URL template)
- IPFSControl: IPFS gateways (via IPFSProvider)
- BTControl: BitTorrent DHT/network (via BTClient)

## 3. When It Stores

**Never.** No persistence operations. All operations are read/fetch/route. The only state changes are:
- Source registration/unregistration (in-memory registry)
- Priority adjustments (in-memory)
- Stats accumulation (in-memory)
- BT/IPFS control plane actions (delegated to BTClient/IPFSProvider)

## 4. What It Stores

**Nothing persistent.** In-memory state only:

- Source registry (ordered list of Source instances)
- Per-source statistics (success count, fail count, bytes transferred, last error, last attempt time)
- BT/IPFS control plane instances (optional, nil if not configured)
- Runtime priority settings (per-source priority values)

## 5. Boundaries and Pitfalls

- **Source interface is the contract**: All sources must implement `Name()`, `Type()`, `Capabilities()`, `Priority()`, `SetPriority()`, `Available()`, `Open()`, `Fetch()`, `Info()`. Missing methods break routing.
- **Capability flags are critical**: `CapStream` and `CapFile` determine routing behavior. Large files must have `CapStream`; whole fetches require `CapFile`. A source with wrong flags will be skipped or cause OOM.
- **Priority is the only routing criterion**: Sources are tried in priority order (ascending). Lower number = higher priority. Ties broken by registration order.
- **Available() is a soft check**: Returns false to skip a source, not to fail the request. A source returning false should not be retried until its state changes.
- **PeerSource depends on FileRouter**: PeerSource is transparent — it delegates to the FileRouter interface, which routes through all other sources. This creates potential circular routing if not careful.
- **URLSource template injection**: The URL template `{hash}` is not URL-escaped — a hash containing special characters could break the URL. Hashes are always 64-char hex, so this is safe.
- **BTControl is optional**: If `btClient` is nil, BTControl methods return errors. The control plane is only available when BT is enabled.
- **IPFSControl is optional**: Same pattern — `provider` must be non-nil for IPFS operations.

## 6. External Connections

- [../connections/05-router-source.md](../connections/05-router-source.md): Source management endpoints (`/sources`) expose source registry status, allow priority adjustments, and control plane operations (local add, BT download, IPFS pin).
- [../connections/06-service-transport.md](../connections/06-service-transport.md): Transport uses `FileRouter` interface for peer `req` frame multi-source fallback.
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md): PeerSource transparent pass-through uses transport's `FileRouter` for WebRTC/WebSocket data channel routing.
- [../connections/04-service-repository.md](../connections/04-service-repository.md): IPFSControl registers pins in repository (`InsertPin`, `InsertFileMeta`, `InsertFileProvider`).
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md): LocalSource reads from content-addressed storage via file_index mapping or CAS fallback.
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md): Comparison. HTTP download (`GET /sha256sum/:sha256`, `/download/:hash`) goes through `downloader.UniversalDownloader.Download` (`back/internal/controller/download.go:45-94`), a separate multi-protocol download system, does not go through source.Manager; two "source" concepts coexist (downloader's local/http/ipfs/bt sources and source module's local/peer/url sources are different implementations).
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md): Source → exit proxy. URLSource can inject an `http.Client` with ech-proxy exit Transport (`url.go:44-47` comments: wintools' ech-proxy can serve as URL source outbound proxy, no independent source type needed; `url.go:4-7`).

## Appendix: Unverified Items

1. `back/internal/serverapp/app.go:230-232` comment claims "HTTP download root requests already go through mgr", but current `GET /sha256sum` actually goes through `universalDownloader.Download` (`back/internal/controller/download.go:45-94`), downloader package does not import source package — this comment appears to be historical, could not verify corresponding path in current code.
2. Module document cross-references: This repository's `doc/design/modules/` has no other numbered module documents yet, cannot cross-reference by number; download/storage/controller module references use code paths.
