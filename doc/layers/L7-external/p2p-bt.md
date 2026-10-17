# p2p_bt —— BT DHT Bridge (go-peerdrive-bt standalone library)

> One-line responsibility: BitTorrent ecosystem capability bridge —— announce/lookup
> on the Mainline DHT,
> BEP 44 (mutable/immutable data storage), BEP 51 (infohash sampling), torrent/magnet
> download and seeding, as a **standalone go.mod library** `github.com/Hana-ame/go-peerdrive-bt`
> (`back/p2p_bt/`, referenced from the main go.mod via `replace`).

- Layer membership: AOP ⑦ External capability aspect (`doc/LAYERS.md` §1)
- Standalone library fact: `back/p2p_bt/go.mod` declares `module github.com/Hana-ame/go-peerdrive-bt`
  (go 1.26.2), depending only on `anacrolix/dht/v2 v2.23.0` and `anacrolix/torrent v1.61.0`
  (+ transitive dependencies); the main module uses `replace github.com/Hana-ame/go-peerdrive-bt => ./p2p_bt`
- Boundary: **new code must not import `internal/p2p_bt`** (old path has been removed); the main module may only
  reference it via `github.com/Hana-ame/go-peerdrive-bt` (REFACTOR.md §8 rule 1)

---

## Responsibilities

1. **BT Mainline DHT node** (`bt_dht.go`): UDP server + bootstrapping (router.bittorrent
   .com / dht.transmissionbt.com) —— announce file hash, find providers.
2. **BEP 44 data storage** (`bep44.go`): immutable items (≤1000 bytes, target=sha1(v))
   and mutable items (Ed25519 signature + seq + salt, target=sha1(pubkey‖salt)) put/get.
3. **BEP 51 sampling** (`bep51.go`): send `sample_infohashes` queries to DHT nodes to collect
   infohash samples, supports `DiscoverInfohashes` crawling.
4. **torrent download client** (`client.go`): .torrent byte / magnet URI download,
   pause/resume, seeding, progress query, completion callback (sha256 per file).
5. **File bridge** (`bt_bridge.go`): bridges peerdrive's content-addressed storage with DHT ——
   `ShareFile` (announce), `FetchFile` (FindProviders → HTTP pull from peer).

## Module inventory (per file: filename + one-line responsibility + key exports)

### `bt_dht.go` —— DHT node + announce/lookup

| Key export | Description |
|---|---|
| `PeerdriveDHTNodePrefix = [2]byte{0x70, 0x64}` | Node ID prefix "pd" (0x70='p', 0x64='d') —— marks our own nodes, for `IsPeerdriveNodeID` to recognize |
| `IsPeerdriveNodeID(id krpc.ID) bool` | Checks whether a 20-byte DHT node ID belongs to a peerdrive node |
| `BTDHTService` struct | `Server`(anacrolix dht.Server) / `listenAddr` / `NodeID` / `localBEP44Store`(sync.Map) |
| `NewBTDHT(listenAddr) (*BTDHTService, error)` | Starts UDP DHT + bootstrapping + waits 1s for routing table to fill; can inject an already-bound `net.Conn` |
| `Announce(hash) error` | Truncate sha256 to first 20 bytes as infohash → `Server.Announce(ih, port, false)` |
| `FindProviders(hash) ([]string, error)` | `AnnounceTraversal` collects deduplicated `ip:port` list, 15s timeout |
| `NumNodes()` / `Close()` | Routing table node count / close server |
| `infoHashFromHex(hash)` | 40 hex (already an infohash) or 64 hex (SHA256 truncated to 20) → 20 bytes |
| `generatePeerdriveNodeID()` | First 2 bytes "pd" + 18 random bytes |

### `bep44.go` —— BEP 44 data storage

| Key export | Description |
|---|---|
| `ErrBEP44NotFound` / `ErrBEP44DHTDisabled` | Package-level sentinel errors |
| `PutImmutable(data) (target [20]byte, err)` | ≤1000 bytes after bencode; three writes: `localBEP44Store` (memory, keeps Get round-trip) → `putLocal` (own server) → remote close nodes best-effort (must first get to obtain a write token) |
| `GetImmutable(target)` | First checks local store (anything you put will definitely be get-able), then iterates Kademlia lookup |
| `PutMutable(privKey, salt, data, seq) (target, err)` | Ed25519 signature, salt ≤64 bytes, seq monotonically increasing |
| `GetMutable(pubKey, salt) (data, seq, err)` | target=sha1(pubkey‖salt), any seq query |
| `putLocal(put, target)` | **`Server.Put` with a cancelled context** —— anacrolix writes to local store before sending network queries; cancelling ctx makes the network query return immediately (relies on library internal behavior, see caveats) |
| `lookupValue(target, seq)` | 8 rounds of iterative Kademlia (alpha=3 parallelism), first response with v is returned |
| `closestNodes(target, count)` | The `count` nodes closest in routing-table distance to target |
| `MakeBEP44Key()` / `MakeBEP44Target(pubKey, salt)` | Key pair generation / target computation utilities |

### `bep51.go` —— infohash sampling

| Key export | Description |
|---|---|
| `ErrBEP51DHTDisabled` / `ErrBEP51NoSamples` | Package-level sentinel errors |
| `SampleInfohashes(target) ([][20]byte, error)` | Queries 8 closest nodes in parallel, deduplicated collection; on total failure falls back to local server query |
| `queryNodeForSamples(addr, target)` | Single-node `sample_infohashes` query (10s timeout) |
| `DiscoverInfohashes(maxResults)` | Crawls using 3 random targets covering different buckets, `maxResults` upper bound |

### `client.go` —— torrent download client

| Key export | Description |
|---|---|
| `BTClient` struct | `cl`(anacrolix torrent.Client) / `dataDir` / `onComplete` / `downloads` / `customPeers` / `torrentData` / `autoSeed` |
| `NewBTClient(dataDir)` / `newBTClient(dataDir, listenAddr)` | Config: `Seed=false, NoUpload=true, DisableUTP=true` |
| `SetOnComplete(fn)` | Download completion callback (infohash + list of completed files) |
| `AddTorrentBytes(data)` / `AddMagnetURI(uri)` | Start download from .torrent bytes / magnet (returns `TorrentMeta`) |
| `AddTorrent(meta)` / `AddMagnet(magnet)` | Backward-compatible entry points (rebuild magnet then go through AddMagnetURI) |
| `PauseDownload` / `ResumeDownload` / `RemoveDownload` | Pause/resume/remove (remove includes data directory cleanup) |
| `GetDownload` / `ListDownloads` / `GetGlobalStats` | Progress/status query |
| `StartSeed` / `StopSeed` / `IsSeeding` / `ListSeeders` / `SetAutoSeed` | Seeding management |
| `GetTorrentBytes` / `GetMagnetURI` | Export .torrent / magnet (magnet metadata exported from library once ready) |
| `AddPeer` / `GetCustomPeers` | Manual peer injection |
| `globalDHT` / `SetGlobalDHT(dht)` | Global DHT reference (used by GetGlobalStats().DHTNodes) |
| `torrentMetaFromLibrary` / `metaFromTorrent` | Library type → this library's `TorrentMeta` (for HTTP API responses) |

Internal mechanism: `downloadState{status: downloading/paused/completed/error/seeding}` +
`watchDownload` (2s polling for completion detection) → `finalizeDownload` (per-file sha256 → close
`doneCh` → trigger onComplete / autoSeed).

### `bt_bridge.go` —— File bridge

| Key export | Description |
|---|---|
| `BTBridge` struct | `DHT`(BTDHTService) / `storageDir` / `shared`(set of shared hashes) |
| `NewBTBridge(dhtSvc, storageDir)` | Create |
| `ShareFile(hash) error` | DHT announce + record into `shared` (idempotency semantics maintained by caller) |
| `FetchFile(ctx, hash) ([]byte, error)` | FindProviders → sequentially try `http://{peerAddr}/files/{hash}` (15s timeout) → return first 200 |
| `ListShared()` / `FilePath(hash)` / `EnsureFileWritten(hash, data)` | Shared list / standard CAS path / write into peerdrive storage layout |

### `bt_types.go` —— Shared external types

`TorrentFile`, `TorrentMeta`, `MagnetInfo`, `DownloadStatus`, `CompletedFile`,
`OnTorrentComplete` (callback signature), `GlobalStats` —— all with JSON tags, directly
usable as HTTP API response bodies.

### `log.go` —— In-library logging

Five package-level functions `LogDebug/LogInfo/LogWarn/LogError/LogDuration`; level controlled
by `PEERDRIVE_LOG_LEVEL` environment variable (default INFO). **Caveat: after splitting into a standalone
library, cannot import main module's `internal/log` (Go internal rule); the original delegation
`p2p_bt.LogDebug -> log.LogDebug` is now self-implemented with matching signatures/behavior**
(log.go:1-3 comments).

## Key mechanisms

### 1. Standalone library constraints and dependency direction

```
Main module back/ (go.mod replace) ──► github.com/Hana-ame/go-peerdrive-bt
   ├─ router.go: NewBTDHT / NewBTClient / SetGlobalDHT / onComplete registration
   ├─ controller/p2p.go: BT HTTP endpoints (thin wrapper)
   └─ downloader: BTDHTFetcher (NewBTBridge + FetchFile)
```

- **Zero imports of main module inside the library** (no import of internal/*); the only cross-boundary
  is env `PEERDRIVE_LOG_LEVEL` (log level convention).
- Tests: `cd back/p2p_bt && go test ./... -count=1 -race` (standalone go.mod,
  no main-module build tag needed).

### 2. SHA256 → infohash mapping

peerdrive uses sha256 content addressing (64 hex); BT DHT uses 20-byte infohash.
`infoHashFromHex` is the unified entry point: 40 hex decoded as-is, 64 hex truncated to first 20 bytes.
**Truncation means DHT addressing space is 160bit** —— theoretical collision risk 2^80, engineering
acceptable (bt_dht.go:201-222 comments).

### 3. BEP 44 three-tier write strategy (reliability backstop)

`PutImmutable` writes to three places in one call:

1. `localBEP44Store` (in-memory sync.Map) —— **guarantees that Get of anything you put will round-trip**,
   not relying on remote nodes' BEP 44 support (most DHT nodes don't support arbitrary data storage);
2. `putLocal` (own server's local store) —— able to answer other nodes' gets;
3. Best-effort writes to remote close nodes (first get for a write token, then put; failures don't report errors).

`GetImmutable` symmetrically checks local store first, then does network lookup. `PutMutable` has only
putLocal + remote (mutable items don't consult local cache; relies on lookupValue's seq filter).

### 4. BTBridge HTTP pull convention

`FetchFile` assumes DHT peers "provide `/files/{hash}` on an HTTP port same/near as their
DHT listen port" —— this is peerdrive's inter-node convention protocol (not standard BT wire protocol).
On failure it tries peers sequentially; on total failure reports error. **This assumption is legacy design**:
in the new architecture (PeerJS + WebRTC) file transfers already use a frame protocol, and the BT DHT bridge
is kept only as a "btdht" fallback fetcher for the downloader (see downloader.md).

## Relationships with other modules

| Consumer | Purpose |
|---|---|
| `internal/router/router.go:107-145` | `PEERDRIVE_BT_DHT_ENABLE` (default true) starts DHT; `PEERDRIVE_BT_DHT_LISTEN` (default :6881); BT download complete → `FileService.RegisterBTFile` registers into storage |
| `internal/controller/p2p.go` | BT HTTP endpoints (torrent upload/magnet download/seeding/status) —— thin wrapper, business logic in library |
| `internal/downloader/universal_downloader.go` | `BTDHTFetcher`: `NewBTBridge(dhtSvc, storageDir)` + `FetchFile`, priority "btdht" |
| `internal/config/config.go` | `BTDHTEnabled` / `BTDHTListenAddr` / `DownloadDir` |
| Main module go.mod | `replace github.com/Hana-ame/go-peerdrive-bt => ./p2p_bt` |

Reverse: the library does not depend on anything in the main module (except env conventions).

## HTTP endpoints overview (thin wrapper in controller/p2p.go)

| Endpoint | Function | Library call |
|---|---|---|
| `GET /bt/status` | `BTDHTStatus` | `NumNodes()` etc. |
| `POST /bt/announce` | `BTAnnounce` | `DHT.Announce(hash)` |
| `POST /bt/find` | `BTFindProviders` | `DHT.FindProviders(hash)` |
| `POST /bt/bep44/put` | `BEP44Put` | `PutImmutable` |
| `POST /bt/bep44/get` | `BEP44Get` | `GetImmutable` |
| `GET /bt/bep51/sample` | `BEP51Sample` | `SampleInfohashes` |
| `POST /bt/torrent` | `BTTorrentUpload` | `AddTorrentBytes` (.torrent upload starts download) |
| `POST /bt/magnet` | `BTMagnetResolve` | `AddMagnetURI` |
| `GET /bt/download/:infohash` series | `BTDownloadProgress` / `BTDownloadTorrent` / `BTDownloadMagnet` | `GetDownload` / `GetTorrentBytes` / `GetMagnetURI` |
| `POST /bt/download/:infohash/{pause,resume}` | `BTPauseDownload` / `BTResumeDownload` | `PauseDownload` / `ResumeDownload` |
| `DELETE /bt/download/:infohash` | `BTRemoveDownload` | `RemoveDownload` |
| `GET /bt/downloads` / `GET /bt/stats` | `BTDownloadList` / `BTGlobalStats` | `ListDownloads` / `GetGlobalStats` |
| `POST /bt/seed/:infohash` / `POST /bt/download/:infohash/unseed` | `BTSeedTorrent` / `BTStopSeed` | `StartSeed` / `StopSeed` |
| `POST /bt/seed-collection` | `BTSeedCollection` | Collection → generate torrent → seed (~130 lines, controller-side assembly) |

> The frontend calls these endpoints via admin verb (`path=/bt/...`; the `field:"torrent"`
> declaration in the admin binary upload was designed specifically for this, see REFACTOR.md §3.10); router
> assembly in `InitBTController` / `InitBTClient` (controller/p2p.go:56-73).

## Caveats and design decisions

| No. | Caveat | Fix |
|---|---|---|
| Go internal | After splitting to standalone library, p2p_bt cannot import main module `internal/log` | log.go self-implements logging (matching signatures/behavior), level via `PEERDRIVE_LOG_LEVEL` |
| Standalone library assertion | README's claim that p2p_bt is "usable standalone" is **wrong** (REFACTOR.md §6): it depended on `internal/log`, `PutImmutable`'s local store priority masking network failures, `putLocal` relied on anacrolix internal behavior (server.go:1081 writes store before sending queries) | After splitting into standalone library (commit 5fb1193), the above dependencies were removed; docs follow REFACTOR.md §6 |
| Library internal behavior dependency | `putLocal` uses `Server.Put` with a "cancelled context" to achieve "only write to local store" —— anacrolix internal implementation changes will cause behavior drift | bep44.go:237-253 comments explicitly mark the dependency point |
| Truncation | 64 hex sha256 truncated to 20 bytes for DHT | Addressing space 160bit, collision 2^80, engineering acceptable (bt_dht.go:201-222 comments) |
| Full read | `FetchFile` uses `io.ReadAll` —— memory blows up when file is unlimited | Bridge is legacy fallback path (download semantics are full caching); new pulls use PeerJS streaming; defensive upper bound not implemented (legacy) |
| Test timeout | `TestFullBTDownload` didn't finish in 30s | `t.Skip` skips instead of failing (DHT bootstrap/peer connectivity are environment issues) |

## Tests (7 unit tests, standalone go.mod; `scripts/test-layers.sh` L7 section)

> Command: `cd back/p2p_bt && go test ./... -count=1`

### `bt_test.go`

> Note: legacy code tests (declared at file header); discovery context not marked case-by-case; "discovery context" spec
> applies to new code.

| Test | Coverage |
|---|---|
| `TestParseTorrent` | .torrent generation → `metainfo.Load` parse consistency + `AddTorrentBytes` wrapper |
| `TestParseMagnet` | magnet URI parsing (valid/invalid) + `AddMagnetURI` |
| `TestBTClientPauseResume` | Pause/resume/list/global stats/remove lifecycle |
| `TestFullBTDownload` | In-process seeder + BTClient full download flow, completion callback verifies sha256 (30s timeout skip) |
| `TestAddMagnetBackwardCompat` / `TestAddTorrentBackwardCompat` | Old API entry-point compatibility |
| `TestGlobalStats` | Stat fields non-negative + DHTNodes via globalDHT |

## File inventory

| File | Responsibility |
|---|---|
| `go.mod` / `go.sum` | Standalone module (anacrolix/dht + torrent) |
| `bt_dht.go` | DHT node + announce/FindProviders |
| `bep44.go` | BEP 44 immutable/mutable data storage |
| `bep51.go` | BEP 51 infohash sampling + crawling |
| `client.go` | torrent download/seeding client |
| `bt_bridge.go` | File bridge (Share/Fetch/storage layout) |
| `bt_types.go` | Shared external types |
| `log.go` | In-library logging (standalone library constraint) |
| `bt_test.go` | Library tests |
