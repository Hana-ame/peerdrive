# downloader layer (back/internal/downloader/)

> Layer belonging: AOP ④ business core (see doc/LAYERS.md §1) — the multi-protocol fallback download chain
> (doc/archive/LEGACY.md line 51: "core download chain, retained").
> Protocol order (universal_downloader.go:3-8 header comment): `local → ipfsgw → btdht → http`,
> cached to local storage on success, so the next request hits directly via LocalFetcher.

**One-line responsibility**: turn "get file content by sha256" into a multi-protocol fallback pipeline — one
`ProtocolFetcher` per protocol, tried in configured order, hash-verified, and cached to disk on a hit; and provides the
admin endpoints with per-protocol availability checks and local cache clearing.

## Responsibilities

### What problem does it solve

A file may exist at the same time in: local CAS storage, an IPFS public gateway (by CID), BT DHT (peer-provided),
and some registered http URL. The historical implementation (libp2p era) was a hand-written chain of if-else fallbacks in the
controller, and every new protocol required changing the download path. This layer turns "fallback" itself into a configurable data structure:

- the `ProtocolFetcher` interface (Name/Fetch/IsAvailable) — one implementation per protocol, unaware of each other
- `buildFetchers(order)` — a comma-separated string declares priority; unknown protocol names are skipped with a LogWarn
- `Download` — the unified entry point: hash validation → try in order → sha256 verification → cache →
  return `(data, protocol, err)`

### Division of labor with the source layer (REFACTOR.md §3.8)

The source layer is "multi-source routing" (streaming, Range, runtime priority adjustment), while this layer is "multi-protocol fallback"
(full fetch, cache writes, DB provider participation). The two evolve independently:
- the controller's `/download/:hash` goes through this layer; `GET /sources` goes through the source layer
- this layer's LocalFetcher DB provider readback is a legacy capability (the source layer's LocalSource does not read the DB)

## Module inventory

| File | One-line responsibility | Key exports |
|---|---|---|
| universal_downloader.go | Multi-protocol fallback download pipeline (interface + 4 fetchers + downloader + cache + source checks) | the `ProtocolFetcher` interface, `FetcherMetric`, `LocalFetcher`, `BTDHTFetcher` (`NewBTDHTFetcher`), `HTTPURLFetcher`, `IPFSGatewayFetcher`, `UniversalDownloader` (`NewUniversalDownloader`, `Download`, `LastMetrics`, `CheckSources`, `ClearLocalCache`, `Fetchers`); `maxURLFetchSize = 8GB` |

## Key mechanisms

### 1. The Download pipeline (universal_downloader.go:305-367)

```
Download(ctx, hash):
  1. Defense: IsStrictSHA256 validation (the hash comes from remote input in an anon collection entry,
      via the sync/serve path; an unvalidated hash reaching LocalFetcher would hash[:2] out-of-bounds panic — the last line of defense,
      since the local download endpoint already validates beforehand)
  2. Iterate fetchers in order: IsAvailable()==false → record "not available" and skip
  3. Wrap Fetch with a per-fetcher context.WithTimeout(d.timeout) (M5 double safety, see pitfall 2)
  4. On success → recompute sha256 and compare; on mismatch record "hash mismatch" and continue to the next protocol
  5. On a hit → cacheToLocal writes and registers → return (data, protocol, nil)
  6. All fail → "file not found on any protocol"
```

Each step records a `FetcherMetric{Name, Duration, Success, Error}`, and `LastMetrics()` is available for
`/download/:hash/sources` admin-troubleshooting.

**Hash validation semantics**: the bytes returned by a hit protocol must pass sha256 recomputation — content addressing is the
trust floor of the whole chain (local has already been validated when written to disk, so it must pass; the content from
gateways/URLs/BT is **untrusted**, and validation is the defense against poisoning).

**Per-fetcher timeout semantics**: each protocol times out independently (`context.WithTimeout(ctx, d.timeout)`),
so a slow protocol does not drag down the whole thing — the total time is Σ of the protocols' timeouts, not a single global
timeout (this is a deliberate tradeoff of "fallback" vs "time limit": rather than interrupting the fallback chain, wait for
all protocols to finish).

### 2. The four ProtocolFetchers

| fetcher | Name | Availability | Fetching |
|---|---|---|---|
| `LocalFetcher` | "local" | storageDir != "" | ① standard CAS `{dir}/{h[:2]}/{h}`; ② the `p2p/` subdirectory variant (the BT download landing directory); ③ an "local" provider marked Available in the DB (relative path joined with storageDir) |
| `IPFSGatewayFetcher` | "ipfsgw" | provider non-empty and has a gateway | `hashutil.SHA256ToCID(hash)` → `provider.FetchByCID` (HTTP public gateways only; the libp2p DHT/Bitswap stack was deleted in batch 2, universal_downloader.go:10-11) |
| `BTDHTFetcher` | "btdht" | `dhtSvc.Server != nil` (BT DHT enabled) | `p2p_bt.NewBTBridge(dhtSvc, storageDir).FetchFile` (the HTTP bridge of the standalone go-peerdrive-bt library) |
| `HTTPURLFetcher` | "http" | always true | "http" providers marked Available in the DB, GET each; `LimitReader(maxURLFetchSize+1)` skips on overflow as abnormal |

### 3. Caching and invalidation (cacheToLocal / ClearLocalCache)

- **cacheToLocal** (378-398): writes `{dir}/{h[:2]}/{h}` (idempotent: InsertFileMeta ignores conflicts)
  → registers FileTypeBlob meta + a "local" provider. A failed cache write only Warns (the download still counts as successful,
  it just will not hit next time).
- **ClearLocalCache** (428-447): deletes both the standard CAS and `p2p/` paths, and marks all local
  providers in the DB `MarkProviderUnavailable` — otherwise the file is deleted but the provider remains, and LocalFetcher's
  DB readback path would still hit the old file. Used by `/download/:hash/refresh`.
- **The p2p/ subdirectory**: files completed by a BT download land at `{dir}/p2p/{h[:2]}/{h}`, and LocalFetcher looks in both
  locations — so the download endpoint can directly consume BT's results (TestLocalFetcher_FileInP2PSubdir).

### 4. Source availability check (CheckSources, 405-425)

For `/download/:hash/sources`: local/http do an actual `Fetch` trial; network protocols (btdht/ipfsgw)
only report capability (true) — no real request is made (it would really download, wasting time). The Controller side wraps a 30s timeout.

### 5. Assembly and configuration (NewUniversalDownloader, 242-256)

```
NewUniversalDownloader(btSvc, storageDir, order, timeout, ipfsProvider):
  storageDir   the cache/readback root directory (assembled from the same source as service.FileService)
  order        comma-separated protocol order (any permutation/subset of "local,ipfsgw,btdht,http")
  timeout      per-protocol single timeout (also injected into HTTPURLFetcher's httpClient)
  ipfsProvider the gateway provider (if nil, the ipfsgw fetcher is always unavailable)
```

- An empty order / all-unknown protocol names → fall back to the default full order (buildFetchers:271-273)
- `Fetchers()` exposes the internal slice only for test assertions of order (Download_FetchersMatchOrder)
- The controller's `InitUniversalDownloader` and service.SyncService's injection share
  the same construction parameters (two independent instances, each holding its own metrics)

### 6. Data-flow example (the full path of one /download/:hash request)

```
GET /download/{hash}
  └─ controller.DownloadBySHA256 → UniversalDownloader.Download(ctx, hash)
        ├─ local     miss (the file is not on this node)
        ├─ ipfsgw    CID gateway 404 (the content was never on IPFS)
        ├─ btdht     bridge hit → FetchFile returns bytes
        │            └─ sha256 comparison passes
        └─ cacheToLocal writes {storageDir}/{h[:2]}/{h} + registers meta/provider
  → 200 (data, X-Protocol: btdht)
Second time with the same hash:
  ├─ local hits directly (CAS path) → no network traversal
```

This also explains the point of the cache: the fallback chain is the "first impression" cost, and the cache converges high-frequency hashes back to local.
`/download/:hash/refresh` does the opposite: after ClearLocalCache it forces the full chain to run again (verifying
whether the remote content is still consistent).

## Relationships with other modules

```
controller/download.go (/download/:hash, /sources, /refresh, anon file download, sync fetching)
  ↓
downloader (this layer)
  ├→ repository (GetFileProviders/InsertFileMeta/InsertFileProvider/MarkProviderUnavailable)
  ├→ provider.IPFSProvider (gateway fetching; the same assembly as the controller's ipfsGatewayProvider)
  ├→ p2p_bt (BTDHTService + BTBridge, external capability aspect ⑦)
  ├→ pkg/hashutil (IsStrictSHA256/SHA256ToCID)
  └→ model (FileMeta)
```

- **Consumers**: the controller's package-level `universalDownloader` variable (injected by InitUniversalDownloader),
  `service.SyncService` (saveFile fetching), and anonymous file download in controller/anon.go.
- **Does not depend on transport**: the fetching paths are local disk / external gateways / BT / URL, orthogonal to node interconnection (PeerJS);
  peer fetching is handled by source.PeerSource (REFACTOR.md §3.8 division of labor).
- Assembly: `NewUniversalDownloader(btSvc, storageDir, order, timeout, ipfsProvider)`, with order from
  configuration (such as PEERDRIVE_DOWNLOAD_ORDER), defaulting to `local,ipfsgw,btdht,http` when empty.

## Pitfalls and design decisions

1. **M5: HTTPURLFetcher's httpClient timeout** (145-153 comment): the original implementation used `http.DefaultClient`
   with no timeout — a slow URL provider hung Download forever (hanging the download endpoint too); fix:
   NewUniversalDownloader injects `&http.Client{Timeout: d.timeout}`, and Download's
   per-fetcher context is a second backstop. **Double safety** (the client timeout prevents a single-request hang; the context
   prevents an overall time overrun).
2. **M5: lastMetrics race** (236-239 comment): concurrent Download writes vs LastMetrics reads
   (`/download/:hash/sources` is called at high frequency) → protected by the metricsMu mutex. `Fetchers()` exposing the slice
   also means callers must not modify order concurrently.
3. **maxURLFetchSize 8GB** (151-153): consistent with the peerjs upload cap (peerjs_routes.go's
   64MB is specific to the fetch endpoint; 8GB is the service-internal overflow-prevention cap) — `LimitReader(size+1)`
   judges the provider abnormal one byte over, preventing a malicious/outrun URL from returning an infinite stream.
4. **Download's hash validation is "the last line of defense"** (306-310): the hash may come from a remote anon entry
   (the sync/serve path), and the local endpoint already validates beforehand, but the downloader guards once more —
   against a `hash[:2]` out-of-bounds panic (a defensive copy of the H1-class problem at this layer).
5. **A hash mismatch does not break the fallback**: some protocol returned wrong content (a poisoned gateway / a tampered URL) →
   record metrics and continue to the next protocol — content addressing lets a wrong source be discovered, but not trusted.
6. **CheckSources "reports capability, not fact" for network protocols**: to avoid an admin endpoint triggering a real download;
   the cost is that the sources list is always true for btdht/ipfsgw (the semantics is "may be available").
7. **A cacheToLocal failure is not fatal**: the cache is an optimization, not correctness — a failed write only Warns, and the download result
   is returned as normal (the full fallback chain runs again next time).
8. **ClearLocalCache must MarkProviderUnavailable**: deleting the file is not enough — the local
   provider in the DB is LocalFetcher's third lookup path, and leaving it would "delete then hit again".
9. **buildFetchers' order tolerance**: trim spaces after commas, filter empty segments, skip and
   LogWarn on unknown protocol names — a misconfigured value does not panic, it degrades to the default order (falling back to the full default order when filtered is empty).
10. **Filename semantics**: cacheToLocal's meta Filename uses the hash itself (FileTypeBlob),
    and the download endpoint then decides the attachment filename per request context — the cache and display name are decoupled.

## Tests

> All tests in this file are legacy-marked (universal_downloader_test.go:3: "This file belongs to legacy
> code (see doc/archive/LEGACY.md, to be deleted/migrated) tests; backgrounds of discovery are not annotated individually"), of which
> the M5-related fixes (httpClient timeout / metrics lock) have their regression semantics embodied in FetchFromServer and
> the order-related tests.

| Test | Coverage |
|---|---|
| `TestLocalFetcher_FileInStorageDir` | Standard CAS path read |
| `TestLocalFetcher_FileInP2PSubdir` | The `p2p/` subdirectory variant (BT result reuse semantics) |
| `TestLocalFetcher_FileFromDBProvider` | DB local provider readback (including relative path joining) |
| `TestLocalFetcher_NotFound` / `TestLocalFetcher_IsAvailable` | Miss error / availability judgment |
| `TestBTDHTFetcher_NotAvailable` / `_Name` | Skip semantics when BT is disabled + name |
| `TestHTTPURLFetcher_Name` / `_IsAvailable` / `_NoProvider` | Always available + error when there is no provider |
| `TestHTTPURLFetcher_FetchFromServer` | Real GET from an httptest server (including the timeout client semantics) |
| `TestNewUniversalDownloader_DefaultOrder` / `_CustomOrder` | Order parsing and assembly (default order / custom order / empty-segment filtering) |
| `TestDownload_LocalFile` | Local hit + metrics recording |
| `TestDownload_Fallback` | Local miss → fallback to the next protocol (stub fetcher chain) |
| `TestDownload_AllProtocolsFail` | Aggregated error when all fail |
| `TestCacheToLocal` | Write + DB registration idempotency |
| `TestCheckSources` | Availability mapping (local actually tried, network reports capability) |
| `TestDownload_FetchersMatchOrder` | Order consistency |
| `TestBuildFetchers_UnknownProtocol` | Unknown protocol name skipped + Warn |

## File inventory

```
back/internal/downloader/
├── universal_downloader.go        the pipeline main file (interface + 4 fetchers + Download/cache/check, 452 lines)
└── universal_downloader_test.go   full-chain tests (legacy mark, 19 cases)
```
