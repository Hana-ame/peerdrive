# provider —— IPFS gateway provider

> One-line responsibility: Fetch file content by CID through public IPFS gateways
> (`back/internal/provider/ipfs.go`) —— concurrent race across multiple gateways
> taking the first success, retry on failure + exponential backoff, an injectable
> BitswapFetcher callback as first priority; this is the only remaining HTTP gateway
> form of the IPFS ecosystem slot in the "external capability aspect"
> (libp2p DHT/Bitswap stack was deleted in batch 2 on 2026-08-16).

- Layer membership: AOP ⑦ External capability aspect (`doc/LAYERS.md` §1)
- History: The "provider layer 1 file fetch abstraction" from REFACTOR.md §7 target package structure ——
  converged 6 duplicated local lookups in service into one (that vision was inherited by
  `SourceManager` in `internal/source/`, see L4-core/source.md); this package's current
  responsibility is narrowed to the IPFS gateway provider
- Config: `PEERDRIVE_IPFS_GATEWAY_ENABLE` (default true) + `PEERDRIVE_IPFS_GATEWAYS`
  (default `https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link`)

---

## Responsibilities

1. **Fetch by CID**: two entry points `GetReader(cid)` (streaming response body) and
   `FetchByCID(ctx, cid)` (full bytes).
2. **Multi-gateway race**: all gateways request concurrently; first success returns;
   other requests cancelled, late response bodies closed (prevents goroutine leaks + connection leaks).
3. **Retry on failure**: each gateway tries up to 3 times (`defaultMaxRetries`), exponential backoff +
   jitter (`sleepBackoff`).
4. **Bitswap priority**: after `SetBitswapFetcher` injects a callback, Bitswap network is tried first,
   then falls back to HTTP gateways (after libp2p stack removal, this callback is injected by an external
   provider; this package has no built-in implementation).
5. **Filename hint**: `GetFilenameHint(cid, originalFilename)`.

## Module inventory (per file: filename + one-line responsibility + key exports)

### `ipfs.go` —— IPFS gateway provider core

| Key export | Description |
|---|---|
| `BitswapFetcher` type | `func(ctx, cid) ([]byte, error)` —— returning `nil, nil` on failure causes the caller to fall back |
| `IPFSProvider` struct | `Gateways`([]string) / `bitswapFetcher` / `client`(http.Client, 30s timeout) |
| `NewIPFSProvider(gateways)` | Create; `http.Client{Timeout: 30s}` |
| `SetBitswapFetcher(f)` | Inject Bitswap callback (takes priority when set) |
| `GetReader(cid) (io.ReadCloser, error)` | Bitswap first → gateway race for first successful response body; failures close body, success triggers a drain goroutine to finish up the remaining results |
| `FetchByCID(ctx, cid) ([]byte, error)` | Same as above but returns full bytes (caller supplies ctx; cancellation propagates to all gateway requests) |
| `GetFilenameHint(cid, originalFilename)` | Original name takes priority, otherwise CID as fallback |
| `fetchBody(ctx, gw, cid)` | Single gateway request + up to 3 retries (retry after backoff) |
| `fetchBytes(ctx, gw, cid)` | `fetchBody` + `io.ReadAll` |
| `doRequest(ctx, gw, cid)` | `GET {gw}/ipfs/{cid}` (`strings.TrimRight(gw, "/")` prevents double slash); non-200 closes body and reports `HTTP %d` |
| `sleepBackoff(attempt)` | Exponential backoff `500ms·2^(n-1)` capped at 5s + 75%~125% jitter (`math/rand`) |

Constants: `defaultMaxRetries=3`, `defaultBaseInterval=500ms`, `defaultMaxInterval=5s`,
`defaultHTTPTimeout=30s`.

### `ipfs_test.go` —— Tests (see "Tests" section)

## Key mechanisms

### 1. Race + cancellation + cleanup (the core difficulty of GetReader)

```
ctx, cancel := context.WithCancel(ctx)
for each gateway: go fetchBody(ctx, gw, cid) → ch
result loop (len(Gateways) iterations):
  r := <-ch
  r success → start drain goroutine to consume remaining ch (close late bodies) → return r.body
  failure   → close body, record firstErr
all failed → aggregate error
```

- **First success returns immediately**; `defer cancel()` cancels the HTTP connections of
  in-flight requests (propagated by `http.NewRequestWithContext`);
- **drain goroutine** (ipfs.go:94-101) continues consuming late results from the channel and
  closes bodies —— otherwise slow gateways' response bodies leak (protected by
  `TestIPFSProvider_GetReader_NoBodyLeak`); starting from `i+1` skips already-consumed successes;
- `fetchBody`'s backoff retries stack with the race: each gateway can retry 3 times, but **the
  overall race is still governed by the first success** —— failed gateways' subsequent retries
  are cancelled along with ctx after success returns.

### 2. Bitswap first + HTTP fallback

`GetReader`/`FetchByCID` share the same first-stage logic: when `bitswapFetcher != nil`,
Bitswap is tried first (GetReader uses 30s timeout ctx), returns on success; otherwise
enters gateway race. The `BitswapFetcher` contract is "returning nil instead of error on
failure is also acceptable" —— the convention is that failure falls back. After the libp2p
stack was removed (batch 2), this package has no built-in Bitswap implementation; the
callback is entirely externally injected (currently the main service does not inject
one —— Bitswap capability is fully offline; the ipfsgw fetcher always goes through the
gateway path).

### 3. Trade-offs in race semantics

- Gateway race is **first-wins** (not shortest-latency-wins): the i-th success returns
  immediately; a slower gateway with better content is not selected (under content
  addressing content is identical, no impact).
- `math/rand` (not crypto/rand) is used for backoff jitter —— only statistical distribution
  is needed, no cryptographic randomness.
- No gateways configured and no Bitswap → explicit error `"ipfs: no gateways configured and
  Bitswap unavailable"` (not silent).

### 4. Behavior parameters quick reference

| Constant | Value | Purpose |
|---|---|---|
| `defaultMaxRetries` | 3 | Number of retry attempts per failed gateway (503/timeout and other transient errors) |
| `defaultBaseInterval` | 500ms | Backoff base (doubling exponent) |
| `defaultMaxInterval` | 5s | Backoff ceiling |
| `defaultHTTPTimeout` | 30s | Overall timeout per request (http.Client) |
| Jitter | 75%~125% | `delay * (0.75 + rand.Float64()*0.5)` —— prevents synchronous retry storms across multiple clients |
| GetReader Bitswap ctx | 30s | Timeout window for Bitswap priority attempt |

Note that `defaultHTTPTimeout` is **per-request** timeout: the worst-case duration of a
single gateway retrying 3 times is about `30s×3 + backoff`, but the overall race is not
constrained by it —— first success returns immediately, others are cancelled via ctx.

### 5. Evolution history (why this is a "gateway" form)

1. **M4 landing** (REFACTOR.md §7): `internal/provider/` was established, targeting
   convergence of 6 duplicated local lookups in service —— original design included multiple
   implementations (local/http/manager etc.) (doc/FILE-REFERENCE.md:115-121 has the old
   inventory).
2. **Batch 2 stack removal** (2026-08-16): libp2p host + DHT + Bitswap were removed with
   the old interconnect layer —— `ipfs_service.go`/`ipfs_compat.go` could not survive
   independently (they reused P2PService's host/DHT). **IPFS capability narrowed to only
   HTTP gateway fetching** (doc/archive/LEGACY.md section C).
3. **source layer takeover** (2026-08-16): the full vision of "file fetch abstraction" was
   inherited by `SourceManager` in `internal/source/` (Capability/priority routing/stats,
   see L4-core/source.md); this package retains a single responsibility: IPFS gateway.
4. **Current state**: `BitswapFetcher` injection point is kept (contract compatible with future
   boxo Bitswap injection), but the main service no longer provides a built-in implementation ——
   ipfsgw fetcher always goes through the gateway race path.

## Boundary with source layer (why there are two "providers")

| Dimension | `internal/provider` (this package) | `internal/source` (L4-core) |
|---|---|---|
| Semantics | A specific external capability: IPFS gateway fetch | **Unified routing** for this node's file fetching (Local/Peer/URL sources) |
| Interface | `GetReader/FetchByCID` (CID dimension) | `Source` interface + Capability (hash dimension) |
| Consumer | downloader (ipfsgw fetcher), controller (pin/gateway status) | FileService/serveFile semantics (local-first degradation) |
| Evolution | Legacy abstraction residue, narrowed to a single capability | Final form of M4 vision |

Lesson from rename/migration: when converging abstractions, **do not keep splitting** —— the
historical files of the provider package (local/http/manager) have been replaced by the
source package; this package only keeps the still-referenced IPFS parts (ipfs.go + ipfs_test.go),
avoiding "empty interface shells + dead implementations".

## Relationships with other modules

| Consumer | Purpose |
|---|---|
| `internal/router/router.go:148-158` | Assembly: `PEERDRIVE_IPFS_GATEWAY_ENABLE` + comma-separated gateway list → `provider.NewIPFSProvider` → `controller.InitIPFSProvider` |
| `internal/controller/p2p.go` | Package-level `ipfsGatewayProvider`: `POST /ipfs/pin/:cid` (PinCID, download and cache), `GET /ipfs/gateways` (health check, probes each gateway), `GET /ipfs` status |
| `internal/controller/download.go` | `InitIPFSProvider` + download fallback path (`FetchByCID`) |
| `internal/downloader/universal_downloader.go` | `IPFSGatewayFetcher`: `hashutil.SHA256ToCID(hash)` → `provider.FetchByCID`, priority "ipfsgw" (IsAvailable = provider non-empty and has gateways) |
| `internal/service/file_service.go` (via controller) | Gateway data import such as `ImportGatewayData` |

Reverse: this package has zero dependencies (stdlib only), one of the cleanest leaf packages.
Note **it is a separate system from SourceManager in L4-core/source.md**: source layer manages
"this node's file fetch routing", this package manages the specific external capability
"IPFS gateway fetch"; downloader and controller consume them independently, without bypassing
each other.

## Usage example: PinCID full chain (controller/p2p.go:938-999)

```
POST /ipfs/pin/:cid
  ├─ ipfsGatewayProvider nil / no gateways → 503 (explicit error)
  ├─ already pinned → 200 already_pinned (pinSvc.Get idempotent)
  ├─ FetchByCID(ctx(60s timeout), cid)  ← this package's core entry
  │    ├─ Bitswap (if injected) → fall back on failure
  │    └─ gateway race: GET {gw}/ipfs/{cid}, first success returns
  ├─ sha256(data) → hashStr
  ├─ write CAS layout {storageDir}/{h[:2]}/{h} (matches repository's anon/file layout)
  ├─ pinSvc.InsertMeta(hash, cid, size, relPath)   ← file_meta registration
  └─ pinSvc.Insert(cid, hash, cid, size)           ← ipfs_pins registration (repository/pin_repo.go)
```

`GET /ipfs/gateways` health check (p2p.go:1046-1067): probes each gateway via `checkGateway`,
returns `[{url, healthy, latency}]` —— used by the frontend BT/IPFS panel. When no gateways
are configured it returns an empty array instead of an error (frontend doesn't need to be aware
of config differences).

## Coordination with downloader (IPFSGatewayFetcher)

```go
// universal_downloader.go:205-214
type IPFSGatewayFetcher struct { provider *provider.IPFSProvider }
IsAvailable() = provider != nil && len(provider.Gateways) > 0
// Fetch: hashutil.SHA256ToCID(hash) → provider.FetchByCID(ctx, cid)
```

Download priority chain `local → ipfsgw → btdht → http` (see downloader.md): when local
misses, convert sha256 to CID and try public gateways. **CID conversion**:
`hashutil.SHA256ToCID` encodes sha256's 32 raw bytes into a CIDv1 (raw codec + sha2-256
multihash) —— the same file content has consistent addressing on both the IPFS side and
the peerdrive side, maximizing gateway cache hit rate.

## Caveats and design decisions

| No. | Caveat | Fix |
|---|---|---|
| Race leak | After the first gateway succeeds and returns, slow gateways' response bodies go un-closed → connection/memory leak | drain goroutine consumes remaining channel and closes all late bodies (ipfs.go:94-101; protected by `TestIPFSProvider_GetReader_NoBodyLeak`) |
| No timeout | `http.DefaultClient` has no timeout → slow gateways hang forever | `http.Client{Timeout: 30s}` (`defaultHTTPTimeout`) |
| URL concatenation | Trailing `/` on gateway address combined with path produces double slash | `strings.TrimRight(gw, "/")` then append `/ipfs/{cid}` |
| Retry storm | No backoff during gateway transient outages causes frantic retries | `sleepBackoff`: exponential 500ms→5s + jitter |
| Non-200 semantics | Body leak for 404/500 | `doRequest` first calls `resp.Body.Close()` on non-200 before reporting error |
| Architecture boundary | Historical problem of 6 duplicated "local lookups" in service | M4 landed `internal/provider/` (this package) and later `internal/source/` (SourceManager routing); this package narrowed to a single capability: IPFS gateway |
| Feature reduction | libp2p DHT+Bitswap stack removed with the old interconnect layer (batch 2, universal_downloader.go:10-11) | IPFS compatibility API/Bitswap no longer provided, only HTTP gateway fetch kept; `BitswapFetcher` callback injection point retained but main service no longer provides built-in implementation |

## Tests (`ipfs_test.go`)

> Note: legacy code tests (declared at file header); discovery context not marked case-by-case; "discovery context"
> spec applies to new code. All tests use `httptest.Server` to simulate gateways, no external
> network dependencies.

| Test | Coverage |
|---|---|
| `TestIPFSProvider_GetReader_Success` / `_NoGateways` / `_NotFound` | Basic paths: success / no-gateways explicit error / 404 error |
| `TestIPFSProvider_GetReader_RaceWinner` | **Race semantics**: fast gateway wins, duration <1s (slow gateway cancelled), content from fast gateway |
| `TestIPFSProvider_GetReader_AllFail` | All gateways fail → aggregated error |
| `TestIPFSProvider_FetchByCID_Success` / `_NoGateways` / `_NotFound` | FetchByCID basic paths |
| `TestIPFSProvider_FetchByCID_ContextCancel` / `_ParentContextCancel` | ctx cancellation propagation: 50ms timeout / parent ctx cancelled → error return |
| `TestIPFSProvider_FetchByCID_RaceWinner` | FetchByCID race (same as GetReader) |
| `TestIPFSProvider_GetFilenameHint` | Original name priority / CID fallback |
| `TestIPFSProvider_SetBitswapFetcher` | Bitswap callback priority: no gateways + has callback → returns Bitswap data |
| `TestIPFSProvider_GetReader_NoBodyLeak` | Race cleanup: after fast wins, slow gateway bodies are closed by drain goroutine (leak regression guard) |
| `TestIPFSProvider_GetReader_ManyGateways` | 10-gateway stress (different latencies) |
| `TestIPFSProvider_RetryOnError` / `_RetryExhausted` | Retries: 503 on first 2 attempts then success on 3rd / always fails exactly `defaultMaxRetries` times |

## File inventory

| File | Responsibility |
|---|---|
| `back/internal/provider/ipfs.go` | IPFS gateway provider (race + retry + Bitswap injection point) |
| `back/internal/provider/ipfs_test.go` | Tests (14, httptest simulated gateways) |
