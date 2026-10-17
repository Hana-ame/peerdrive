# source layer (back/internal/source/)

> Layer belonging: AOP ④ business core (see doc/LAYERS.md §1) — the unified file acquisition abstraction (the landing of the provider concept,
> REFACTOR.md §3.8).
> Core idea (header comment at source.go:1-18): **anything that can provide a "content-addressed byte stream" is a Source**
> — the local disk, a p2p peer (pass-through), and URL/HTTP templates. The upper layers only ask "give me the content of this hash"
> and do not care about the origin or the network path.

**One-line responsibility**: abstract multi-backend (local / peer pass-through / URL template) file acquisition into the `Source` interface +
`SourceManager` unified routing (priority/capability/statistics/runtime adjustment); large files go streaming only (CapStream),
and full acquisition goes through OpenAny downgrade.

## Responsibilities

### What problem does it solve

Before the refactor, the "local lookup" logic in service was copied 6 times (the motivation for the provider layer design in REFACTOR.md §7).
And once node interconnection (PeerJS) matured, a new requirement appeared: **a file not on this node can be pulled from an online peer**, and if that fails
from a URL template — this is "multi-source fallback". The source layer converges these three paths into:

- **A unified interface**: `Source` (Name/Type/Capabilities/Priority/SetPriority/Available/Open/Fetch/Info)
- **Unified routing**: `SourceManager` (trying each source in ascending priority, capability routing, hit statistics, aggregated error on all-fail)
- **A unified admin surface**: `Snapshot()` → `GET /sources` (status + statistics + runtime priority adjustment)

### Position in AOP ④

```
controller (the /sources admin endpoint goes through router/source_routes.go, not through the controller)
router (source_routes.go: GET /sources, POST /sources/:name/priority)
  ↑
source (this layer)
  ├→ transport.FileIndexService (LocalSource's index mapping + IsPathAllowed)
  ├→ transport.PeerJSService (PeerSource's connection enumeration + OpenStream)
  └→ pkg/hashutil (IsStrictSHA256)
```

Assembly lives in `internal/serverapp/app.go` (comment at source.go:17: transport does not depend back on this package).

## Module inventory

| File | One-line responsibility | Key exports |
|---|---|---|
| source.go | Interface/type definitions: Source, Capability, FileMeta, Stats, SourceStatus | the `Source` interface, `Capability` (`CapFile=1`/`CapStream=2`), `IsStream`/`IsFile`, `validHash` |
| manager.go | SourceManager: register/unregister/priority/unified acquisition entry/statistics snapshot | `Manager`: `New`, `Register`, `Unregister`, `SetPriority`, `Open`, `OpenRange`, `OpenAny`, `Info`, `Snapshot` |
| local.go | The local disk source: file_index mapping preferred + CAS fallback, CapStream | `LocalSource`: `NewLocalSource`, `Open`, `Fetch`, `Info`, `Available`, `resolvePath` |
| peer.go | The p2p pass-through source: enumerating online peers and trying serially, per-peer single slot | `PeerSource`: `NewPeerSource`, `Open`, `Available`, `Fetch`; `peerReadCloser` |
| url.go | The URL template source: %s/%d templates + Range slicing + sha256 verification | `URLSource`: `NewURLSource`, `Open`, `Fetch`, `buildURL`; `verifyReadCloser` |

## Key mechanisms

### 1. Capability flags (source.go:29-39)

```go
const (
    CapFile   Capability = 1 << iota  // whole acquisition (Fetch → []byte)
    CapStream                         // streaming/slicing (Open(ctx, hash, offset, size))
)
```

Capability determines the routing mode (header comment at manager.go:11-13):

- **Large files must go through CapStream** — an 8GB full buffer would OOM (the lesson from the transport streaming refactor, source.go:9)
- `OpenRange` **only tries CapStream sources**: a CapFile source has no slicing capability, and downgrading = the full buffer path
- `OpenAny` allows downgrading to a CapFile whole fetch (small files/metadata scenarios)

Each source's capability declaration:

| Source | Capability | Reason |
|---|---|---|
| local | CapStream | os.File Seek/ReadAt is natively sliceable |
| peer | CapStream | the req frame protocol supports offset/size ranges |
| url | a template containing `%d` → CapStream; otherwise CapFile | Range parameters in the template are what make it streaming (url.go:52-57) |

### 2. Routing semantics (manager.go:9-14)

```
OpenRange(ctx, hash, offset, size):
  1. validHash (IsStrictSHA256, unified defense — all source entry points)
  2. snapshot sources (an RLock copy; no lock held while routing — Available/Open are slow operations)
  3. In ascending priority: skip if Available()==false (recording "unavailable");
     try each IsStream source's Open in turn; the first success returns
  4. All fail → "all sources failed: <the last source's error>" (lastErr keeps only the last one;
     the detailed failure reason per source is in Stats.LastErr)
```

Assembly priority (internal/serverapp/app.go): `local → peer → url (optional, PEERDRIVE_URL_SOURCE_TEMPLATE)`.
"Return immediately on a local hit" is the local-authoritative semantics of content addressing; a miss downgrades to peer; URL is the last fallback.

### 3. Statistics and the admin surface (manager.go:205-248)

Every attempt (including an unavailable skip) is `record`ed:

```
Stats{ Success, Fail, Bytes, LastErr, LastAt }
```

`Snapshot()` outputs `SourceStatus` (Name/Type/Priority/Capabilities/Stream/Available/Stats)
→ `GET /sources`; `POST /sources/:name/priority` adjusts priority at runtime (after SetPriority, resorted with
sort.SliceStable). This is the admin capability of "runtime routing adjustment" — a failing source can be temporarily deprioritized
without a restart.

### 4. LocalSource: file_index preferred + CAS fallback (local.go:69-82)

```go
resolvePath(hash):
  CAS: storageDir/{hash[:2]}/{hash}
  if fileIndex.Info(hash) hits and IsPathAllowed (the path is inside the allowed root) → return the index path
  else → fall back to CAS (historical dirty data / malicious registrations do not serve out-of-root files, H2 semantics)
```

Exactly the same path decision as `transport.serveFile` (the same logic converged in one place, comment at local.go:3-8).
The sha256 check is already done when a local file is written (upload Complete), so `Open` does not re-verify
(same behavior as serveFile); `Available` = the storage directory is readable.

### 5. PeerSource: per-peer single-slot serialization (comment at peer.go:5-15)

```
Open:
  enumerate PeerJSService.Connections() (excluding its own ID)
  for each peer: peerLocks.LoadOrStore gets a *sync.Mutex → TryLock()
    - cannot get it = this peer already has a stream in progress → skip to the next (do NOT wait! a large file stream would block the whole routing)
    - got it → svc.OpenStream(pid, hash, offset, size); on success return a wrapper reader
      (unlocking on Close releases the slot); on failure, unlock and continue
```

- **Where the single-slot constraint comes from**: the connection-level expect state machine (transport/conn.go bindConn) — two concurrent
  fetch streams on the same connection would interleave data. TryLock rather than Lock is the "skip if busy" semantics.
- Currently **serial attempts**; it can be upgraded to multi-peer concurrent racing in the future (concurrent across different connections is safe, the same connection still needs mutual exclusion,
  comment at peer.go:13-14).
- `Info` is not supported (the peer info verb is not implemented on the fetching side, peer.go:114-117).

### 6. URLSource: template + Range + content-addressed backstop (url.go)

- **Template**: `fmt.Sprintf`, `%s`=hash; containing `%d` (twice) = offset,size → declares CapStream
  (url.go:40-58). Example: `https://example.com/f/%s?off=%d&size=%d`
- **Range semantics** (Open): `bytes=start-end` (size<0 to the file tail); server 206 → use the body directly;
  a 200 full response → `io.CopyN` to discard the offset portion + `LimitReader` to truncate to the size portion (bandwidth wasted but correct,
  comment at url.go:121-134); 416/4xx → error
- **sha256 verification** (verifyReadCloser, url.go:177-214): **a full request (offset==0 && size<0) is verified while reading** — URL source content can be tampered with, and verification is the
  floor of content-addressed semantics; compared at EOF, returning a hash mismatch on failure (ReadAll gets it). Fetch verifies the same way (url.go:164-168)
- `Available` is always true (ping would waste requests, comment at url.go:79-81); failures are exposed by routing statistics (LastErr)
- An http.Client Transport can be injected to point at an ech-proxy or similar egress (wintools cmd/ech-proxy),
  without creating a separate source type (comment at url.go:5-8)

## Relationships with other modules

```
router/source_routes.go (admin endpoints: the /sources snapshot + priority adjustment)
  ↑ Snapshot/SetPriority
source (this layer)
  ├→ transport.FileIndexService (the LocalSource index; Info/IsPathAllowed)
  ├→ transport.PeerJSService (PeerSource connection enumeration; Connections/OpenStream/ID)
  └→ pkg/hashutil (IsStrictSHA256 unified hash defense)
internal/serverapp/app.go (assembly: local → peer → url)
```

- **Boundary** (REFACTOR.md §3.8): `serveFile` keeps its local semantics and does **not** use the manager (avoiding an inbound→outbound
  pass-through recursion loop); `/peerjs/fetch` still calls FetchFromPeer directly (keeping its semantics).
- **Consumers**: currently mainly the transport layer file index's `LocalSource` assembly; in the future the downloader/
  controller's acquisition paths can be switched to the manager (reserved, not switched).

## Pitfalls and design decisions

1. **OpenRange rejects CapFile sources** (manager.go:102-103): a CapFile source has no slicing capability, and downgrading in OpenRange
   would go through the full buffer (the OOM path); callers needing a whole fetch explicitly use OpenAny — the API semantics force
   the caller to declare its memory budget.
2. **All-fail returns only the last error** (manager.go:126-132): `all sources failed: <lastErr>`,
   with the detailed reason per source in Stats.LastErr (for admin troubleshooting) — the error body does not balloon but the information is queryable.
3. **No lock held while routing** (manager.go:108-110): the snapshot is copied and the RLock released — Available/Open
   are network/disk slow operations, and holding the lock would block SetPriority/Register entirely.
4. **per-peer TryLock rather than a global lock**: the same-peer single slot is a hard frame protocol constraint; streams on different peers are
   naturally concurrently safe (different connections) — lock granularity is exact to the peer, avoiding one slow peer blocking all routing.
5. **URLSource's 200 full truncation**: when the server does not support Range, correctness comes first (bandwidth waste is acceptable),
   and no HEAD probing is done in the first version (comment at url.go:122-124).
6. **verifyReadCloser only verifies at EOF**: an early Close cancel does not verify (consistent with the transport
   fetchReader semantics; the H5 backstop is on the transport side).
7. **The soft-state semantics of Available**: local=directory readable, peer=there are online connections (excluding itself), url=always true
   — all are "possibly available" rather than "definitely has this file", and a real hit is decided by Open (comment at source.go:63-64).
8. **Duplicate name registration is rejected** (manager.go:40-52): Name is the registry key, and a duplicate registration returns an error — preventing assembly
   from accidentally registering a source with the same name and silently overwriting.

## Tests

| File | Test | Background of discovery |
|---|---|---|
| source_test.go | `TestLocalSource_OpenCAS` | A new feature of the source system (local CAS slicing/whole reading + illegal hash rejection + not-found error) — no prior bug, defensive |
| source_test.go | `TestLocalSource_IndexPriority` | file_index mapping takes priority over CAS (when the same hash exists in both places, the index path is read); the test uses `repository.InitDB(":memory:")` + a real `transport.NewFileIndexService` (depends on SQLite persistence) |
| source_test.go | `TestManager_RoutePriority` | Routing semantics regression: a local hit returns immediately (peer is not called), the miss downgrade chain, OpenRange skips CapFile sources, OpenAny backstop, SetPriority runtime adjustment, Snapshot statistics, duplicate name rejection — verified with a programmable stubSource, covering 5 routing branches |

> Note: this layer's tests are all marked with a "source system" background of discovery (header comment at source_test.go:3-5: routing priority/
> capability flags/statistics admin/error downgrade chain); they are defensive tests for new code.

## File inventory

```
back/internal/source/
├── source.go         Source interface + Capability + FileMeta/Stats/SourceStatus + validHash
├── manager.go        SourceManager (registration/routing/statistics/snapshot)
├── local.go          LocalSource (file_index preferred + CAS fallback, CapStream)
├── peer.go           PeerSource (connection enumeration + per-peer single-slot serialization)
├── url.go            URLSource (template + Range + sha256 verification)
└── source_test.go    Local/Manager tests (a stub source drives 5 routing branches)
```
