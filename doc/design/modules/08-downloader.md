# Module 08: downloader Multi-Protocol Downloader

- **Code location**: `back/internal/downloader` (`universal_downloader.go`, tests `universal_downloader_test.go`)
- **One-line function**: For the same 64-char SHA-256 hash, tries protocols in configured priority order: local (local content-addressed storage) → ipfsgw (IPFS HTTP gateway) → btdht (BitTorrent Mainline DHT HTTP bridge) → http (URL registered in `file_providers`); the first one that passes byte-level verification wins; on success caches data to local storage and registers metadata/source, finally returns `(data []byte, protocol string, err error)` to caller (`back/internal/downloader/universal_downloader.go:1-14, 303-367`).
- **Dependencies**:
  - `peerdrive/internal/repository` (direct, bypasses service convergence): `GetFileProviders` / `InsertFileMeta` / `InsertFileProvider` / `MarkProviderUnavailable` (`back/internal/repository/file_repo.go:48-93`).
  - `peerdrive/internal/model`: `FileMeta` / `FileProvider` structs and `FileTypeBlob` constant (`back/internal/model/file.go:10-32`).
  - `peerdrive/internal/provider`: `IPFSProvider` (ipfsgw fetch; `back/internal/provider/ipfs.go:34-48, 124-166`).
  - `github.com/Hana-ame/go-peerdrive-bt` (local module `back/p2p_bt`): `BTDHTService` / `BTBridge` (`back/p2p_bt/bt_bridge.go:19-33, 57-115`).
  - `peerdrive/pkg/hashutil`: `IsStrictSHA256` (download entry validation), `SHA256ToCID` (ipfsgw hash→CID conversion, auto-compute CID on registration) (`back/pkg/hashutil/hashutil.go:27-41, 62-73`).
  - Standard library: `crypto/sha256`+`encoding/hex` (verification), `net/http` (http fetcher), `os`/`path/filepath` (CAS read/write), `sync` (metrics mutex), `time`.
- **Depended upon by**:
  - `back/internal/controller/download.go`: Package-level global `universalDownloader` (31-37), `DownloadBySHA256Internal` (60), `UniversalDownload` (219), `UniversalDownloadSources` (244), `UniversalDownloadRefresh` (261, 265) call `Download` / `CheckSources` / `ClearLocalCache`.
  - `back/internal/controller/anon.go:186`: Anonymous collection file download (`DownloadAnonFile`) calls `Download`.
  - `back/internal/service/sync_service.go:16-19, 132`: `SyncService` holds same instance, `saveFile` uses it to fetch collection files.
  - `back/internal/router/router.go:199-213, 233-236`: Sole construction point + inject into controller/SyncService + register `/download/*` routes.
  - Tests: `back/internal/downloader/universal_downloader_test.go`.

## 1. Logic

**Core responsibility**: Multi-protocol fallback download pipeline. Abstracted as `ProtocolFetcher` interface — `Name() / Fetch(ctx, hash) ([]byte, error) / IsAvailable()` (`universal_downloader.go:55-61`), currently 4 implementations:

| Protocol | Implementation Type | Fetch Behavior | IsAvailable | Code Location |
|---|---|---|---|---|
| `local` | `LocalFetcher` | ① Read disk by two content-addressed paths: `<storageDir>/<hash[:2]>/<hash>`, `<storageDir>/p2p/<hash[:2]>/<hash>`; ② If both miss, query DB `file_providers` for `available` `local` entries, read disk by their `Path` (relative path joined with `storageDir`) | `storageDir != ""` | `universal_downloader.go:68-107` |
| `ipfsgw` | `IPFSGatewayFetcher` | `hashutil.SHA256ToCID(hash)` converts to CIDv1 → `IPFSProvider.FetchByCID` (Bitswap callback preferred, else multi-gateway concurrent race, each gateway exponential backoff retry ≤3 times; client timeout 30s) | `provider != nil && len(Gateways) > 0` | `universal_downloader.go:205-221`; `back/internal/provider/ipfs.go:41-48, 124-166, 170-187` |
| `btdht` | `BTDHTFetcher` | `BTBridge.FetchFile`: DHT `FindProviders(hash)` finds peer addresses then GET `http://<peerAddr>/files/<hash>` for each (client timeout 15s) | `dhtSvc != nil && dhtSvc.Server != nil` | `universal_downloader.go:115-136`; `back/p2p_bt/bt_bridge.go:57-115` |
| `http` | `HTTPURLFetcher` | Query DB `file_providers` for `http` entries, `GET p.Path` (with ctx; client timeout = `d.timeout`; `io.LimitReader` size limit) | Always `true` | `universal_downloader.go:144-197` |

**Main flow `Download(ctx, hash) (data []byte, protocol string, err error)`** (`universal_downloader.go:305-367`):

1. Entry defense: `IsStrictSHA256(hash)` strict validation (only 64-char lowercase hex); invalid returns error directly. Comment explicitly says this is defense against anon collection entry remote input — bypassing validation in `LocalFetcher` triggers `hash[:2]` out-of-bounds panic (306-310).
2. Initialize this round's `metrics`, `defer` write back to `lastMetrics` (311-316).
3. Try each in `fetchers` order:
   - Unavailable → log `{success:false, error:"not available"}` and skip (319-327);
   - Available → `context.WithTimeout(ctx, d.timeout)` limits single attempt total duration (330) → `Fetch`;
   - Fail → log `{success:false, error:err}` and continue to next protocol (358-364);
   - Success → recompute SHA-256 and compare with input, mismatch logs `"hash mismatch"` and treats as failure continue (337-347); match → `cacheToLocal` persists to cache (349) → log success metric → `return data, fetcher.Name(), nil` (351-356).
4. All protocols fail → return error `"file not found"` (366).

**`cacheToLocal` (368-400)**: On successful fetch from remote protocol, persists to local storage:
- Writes to `<storageDir>/<hash[:2]>/<hash>` via `os.WriteFile` (with `MkdirAll` for directory);
- `repository.InsertFileMeta` (type=blob, size, sha256);
- `repository.InsertFileProvider` (provider_type=origin protocol, path=written path, available=true);
- Logs success.
This cache allows subsequent requests to hit `local` protocol directly, avoiding remote fetches.

**`CheckSources` (405-436)**: Checks availability of all protocols for a given hash, returns per-protocol status:
- `local`: Checks file existence in CAS paths + DB `file_providers` local entries;
- `ipfsgw`: Checks `provider != nil && len(Gateways) > 0`;
- `btdht`: Checks `dhtSvc != nil && dhtSvc.Server != nil`;
- `http`: Checks DB `file_providers` http entries exist.

**`ClearLocalCache` (440-447)**: Deletes local cache for a hash:
- Removes file from CAS path;
- `repository.DeleteFileProvider` (provider_type=local);
- Does NOT delete `file_meta` (may be referenced by collections).

## 2. How It Stores

**Cache writes to local content-addressed storage + SQLite registration**:

| Aspect | Details |
|--------|---------|
| Storage medium | Local filesystem (CAS: `<storageDir>/<hash[:2]>/<hash>`) + SQLite (`file_meta`, `file_providers`) |
| Trigger | Only on successful remote fetch (ipfsgw/btdht/http) — not on local hits |
| Write path | `cacheToLocal` writes file + inserts metadata + inserts provider record |
| De-duplication | If local hit occurs first, no cache write needed |
| Cleanup | `ClearLocalCache` removes file + provider record, keeps metadata |

**Not persisted**:
- Metrics (`lastMetrics`) are in-memory only
- Protocol availability checks are computed on the fly
- Download timeout config is runtime, not persisted

## 3. When It Stores

| Trigger | Action | Code Reference |
|---------|--------|----------------|
| Remote protocol fetch succeeds | `cacheToLocal` writes file + registers metadata + provider | `universal_downloader.go:368-400` |
| Clear cache request | `ClearLocalCache` removes file + provider record | `universal_downloader.go:440-447` |
| Check sources request | No storage — only reads DB and filesystem | `universal_downloader.go:405-436` |

**Note**: The downloader does NOT store on `local` protocol hits (file already exists in CAS). Cache writes only happen for data fetched from remote protocols.

## 4. What It Stores

**Cached file data**:
- Binary file content (any type — documents, images, video, archives, code)
- SHA-256 verified before caching (byte-level integrity check)
- Stored at `<storageDir>/<hash[:2]>/<hash>`

**Metadata in SQLite**:
- `file_meta` entry: sha256 (PK), filename (from protocol response if available), size, mime, type=blob
- `file_providers` entry: provider_type (ipfsgw/btdht/http), path (CAS file path), available=true

**Not stored**:
- Download metrics (in-memory only, lost on restart)
- Provider availability status (computed on demand)
- Download history (no tracking of past download attempts)

## 5. Boundaries and Pitfalls

- **SHA-256 verification is mandatory**: Every fetched byte is verified against the expected hash. Hash mismatch is treated as failure, not success — the data is discarded and the next protocol is tried.
- **Cache is best-effort**: `cacheToLocal` failures (disk full, permission denied) do not fail the download — the data is returned to the caller regardless. The cache is an optimization, not a requirement.
- **No cache eviction**: Once cached, files are never automatically evicted. Disk space is the only limit. Manual cleanup requires `ClearLocalCache`.
- **Local hit does not trigger cache**: If `local` protocol hits, no cache write happens (file already exists). Cache is only for remote-fetched data.
- **Bypasses service layer**: Downloader directly reads/writes repository (bypasses service convergence). This is a deliberate architectural decision — the downloader is a low-level data fetcher, not a business logic component.
- **Protocol timeout is shared**: All protocols use the same `d.timeout` value. Individual protocol-specific timeouts are not supported.
- **HTTP provider fetches are not authenticated**: HTTP fetches use plain GET with no authentication. If a provider URL requires auth, it must be embedded in the URL.
- **BTDHT fetch is HTTP-based**: BT fetch uses HTTP GET to peer addresses found via DHT, not BitTorrent protocol. This is a bridge implementation, not full BitTorrent client.
- **IPFS gateway race is concurrent**: Multiple gateways are tried concurrently with exponential backoff. This can cause high bandwidth usage during retries.

## 6. External Connections

- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md): Controller→downloader. Download handlers (`GET /sha256sum/:sha256`, `/download/:hash`, `/download/sources/:hash`, `/download/refresh/:hash`) drive `Download`/`CheckSources`/`ClearLocalCache` respectively, writing the matched protocol name to `X-Protocol` response header (`download.go:60-65, 219-226, 244, 261-265`).
- [../connections/02-router-controller.md](../connections/02-router-controller.md): Router assembly layer. `SetupRouter` constructs `UniversalDownloader` per config and injects into controller (`router.go:199-208`), then registers `/download/*` routes (233-236).
- [../connections/03-controller-service.md](../connections/03-controller-service.md): Service layer reuses same instance. `SyncController → SyncService.saveFile → downloader.Download` fetches collection files (`sync_service.go:16-19, 130-135`).
- [../connections/04-service-repository.md](../connections/04-service-repository.md): Persistence delegation. Downloader bypasses service layer and **directly connects to repository** for `file_meta`/`file_providers` read/write (`file_repo.go:48-93`; downloader side 378-398, 441-445).
- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md): Frontend normal file download goes through `req` verb (transport `serveFile`/FileRouter, not downloader), does not enter this module; downloader is only indirectly reachable from frontend via local WS admin frame forwarding to anon/sha256 endpoints (`router.go:215-223` legacy HTTP route section; `front/src/api.js:290-293` anon download is `ws.admin('GET', '/anon/...')`, backend lands at `anon.go:186` `Download`).
