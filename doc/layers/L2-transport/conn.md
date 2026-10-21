# Shared Connection Core (back/internal/transport/conn.go)

> Layer belonging: AOP ② frame protocol layer (see doc/LAYERS.md §1)
> Frame protocol verb definitions are in doc/REFACTOR.md §4; this document only describes the implementation mechanisms and does not redefine the protocol.

**One-line responsibility**: the "shared core" of a connection — frame type definitions, the reqId state machine, binary chunk routing, and connection lifecycle cleanup; the mechanisms shared by the inbound/outbound roles exist here in a single copy and must not be duplicated.

## Responsibilities

### What problem does it solve

A WebRTC DataChannel (and local WS) is a full-duplex symmetric connection: the same Session must both **respond** to verbs sent by the peer (inbound role) and **initiate** its own verbs while collecting responses (outbound role). If the two roles each maintained their own "how frames are parsed, whose binary chunk is it" logic, two mutually drifting protocol implementations would result — any change to frame semantics on one side immediately breaks protocol consistency on the other.

conn.go collects all **connection-level** mechanisms into a single point (the header comment at conn.go:3-11 explicitly states "no duplication"):

- Frame types (`dcReq`/`dcResp`) — the request/response roles share the same structure
- Connection state machine (`connState`) — fetches/expect belong to outbound, pendingUpload/binCh to inbound, adminUp/fwd to their own subsystems, **flattened and shared, not split in two**
- Dispatch pump (`bindConn`'s OnMessage) — text frames are routed by verb/reqId, binary chunks are attached by the "connection-level expect" state machine to the most recent declared header's request
- Unified cleanup on connection close (release fetches, exit the upload worker, close the forward tunnel's out)

### Position in AOP ②

```
① peerjs (transport primitives: signaling/DataChannel/flow control/atomic frames)
       ↑  Session interface (ws_session.go / rtc_session.go)
② transport
    ├── conn.go        ← this document: shared connection core (dispatch pump + state machine + cleanup)
    ├── inbound.go     inbound role (serveFile / serve* index verbs / uploadWorker)
    ├── outbound.go    outbound role (OpenStream / FetchFromPeer / routeResponse)
    ├── forward.go     port forwarding v2 (fwd-* verbs, single-slot fwd/fwdHs/fwdCh)
    ├── admin.go       admin-plane verbs (single-slot adminUp, local session only)
    └── file_index.go  SQLite persistence (shared by both roles)
       ↓
③ admin aspect (internal gin engine forwarding)  /  ④ controller → service → repository
```

Upstream and downstream:

- **Upstream**: `*peerjs.Connection` (WebRTC) from ① peerjs and gorilla's `*websocket.Conn`
  (local WS) enter this module after being adapted through the `Session` interface; the atomicity
  of the peerjs layer's `SendFrame` (sendMu + low-water flow control) is the physical basis for
  this module's "header+chunk atomically contiguous" assumption (REFACTOR.md §4
  constraint 2, §5 concurrency flow-control deadlock pitfall).
- **Downstream**: inbound.go's `serveFile`/`serve*` and outbound.go's `routeResponse` are the
  leaf nodes of the dispatch pump; admin.go / forward.go slots (`adminUp`/`fwd`) are written by
  the pump according to frame order. The business layer (④) is unaware of conn.go and only uses
  the data plane via the semantic APIs `FetchFromPeer`/`OpenStream`
  (LAYERS.md §5 forbidden zone: the business core does not manipulate connections directly).

## Key mechanisms

### 1. Frame types (conn.go dcReq/dcResp)

```go
type dcReq struct {   // request frame: outbound initiates, inbound responds
    Type/ Hash / Offset / Size / ReqID
    Trace []string  // optional (omitempty): source-routing node chain (loop prevention for multi-source serveFile routing)
}
type dcResp struct {  // generic response frame: fetch response + index verb response + forward handshake
    Type/ Hash / Total / Offset / Size / Msg / ReqID
    Path / Name / Seq / Files / LastSeq
    Nonce / Hmac / Port   // fwd-challenge / fwd-auth / fwd-open
}
```

Key points:

- **One dcResp carries all responses**: meta/data/done/err (fetch) and created/uploaded/ack/
  list-resp/info-resp/deleted/sync-resp (index) share it, and the fwd handshake fields
  (Nonce/Hmac/Port) are merged in too — avoiding creating a structure per verb; dispatch only
  distinguishes by `Type`.
- The requester's reqId is mandatory on the Go side (UUID v4); the browser may omit it (backward
  compatible, REFACTOR.md §4 constraint 3).
- **Trace (added 2026-08-18, source-routing loop prevention)**: after serveFile multi-source
  routing, a peer's request may route back to another node (when A↔B are interconnected, if B
  requests a file A does not have → A→B→A infinite recursion). A req frame carries trace (the
  chain of nodes it passed through); a forwarding-path node that finds itself in the chain
  immediately returns `err "loop detected"` (refused). The root request does not carry this field
  (omitempty, compatible with older peers); `serveFile` propagates it via context
  (transport.TraceKey, carried by OpenStreamFrom); OpenStreamFrom is the propagation point.

### 2. connState: flattened, shared connection-level state (conn.go:74-99)

```
connState
├── fetches  map[reqId]*fetchState   ← outbound: download requests initiated by this end (written by outbound.go)
├── expect   *fetchState             ← outbound: the download currently awaiting binary chunks (written by routeResponse)
├── pendingUpload *uploadState       ← inbound: the streaming upload currently being received (single slot, written by serveUploadBegin)
├── adminUp  *adminUploadState       ← admin: admin-plane upload collection (single slot, written by serveAdmin)
├── fwd / fwdHs / fwdCh              ← forward: forwarding tunnel (single slot) + handshake placeholder + chunk queue
└── binCh / binDone                  ← connection-level IO worker's submission queue / exit signal
```

Design points:

- **State is not split in two**: the two roles' states are flattened in the same structure, naturally separated by different fields (comment at conn.go:71-73). Splitting into inboundState/outboundState would introduce cross-structure handshakes and lock-ordering problems.
- **Single-slot semantics**: `pendingUpload`, `adminUp`, and `fwd` each have only one active at a time per connection — this is a corollary of the protocol's "header+chunk atomically contiguous" rule (at most one "declared header awaiting its binary chunks" window at any moment); concurrent uploads from multiple sources run **in parallel across connections** (uploadState comment, conn.go:119-120).
- **All slot operations are under `st.mu`**, but only cheap routing decisions are made there — no IO (see below).

### 3. bindConn: the dispatch pump (conn.go:150-293)

`bindConn` is the core entry point of a connection, shared by three paths:

1. **Registration** (conn.go:151-163): `conns[ID]` (look up a connection by peer id / "local") +
   `pending[Session]` stores state, and starts the `uploadWorker` goroutine. After that all frames
   enter only via the OnMessage callback.
2. **Text frame dispatch** (conn.go:166-226): JSON parse failures or missing type are silently
   dropped; routed by type:
    - `req`/`create`/`upload`/`list`/`info`/`delete`/`sync` → `go serve*` (inbound role responds,
      async goroutine, no shared state)
    - `admin` → **synchronous** `serveAdmin` (see pitfall #7: async would lose upload chunks)
    - `fwd-open`/`fwd-auth`/`fwd-close` → `go serveForward*`; `fwd-data` → the pump synchronously
      sets `fw.pending` (declaring "the next binary chunk belongs to the forwarding tunnel"; frame
      order guarantees no race)
    - `fwd-challenge`/`fwd-ok`/`fwd-err` → `routeForwardResponse` (routed in the same slot as file fetch responses)
    - others (meta/data/done/err and index responses) → `routeResponse` routes by reqId
3. **Binary chunk routing** (conn.go:228-292): attributed by priority, **the routing decision is in the pump (cheap), IO in the worker (H5)**:
    ```
    fw.pending (forwarding chunk) → st.fwdCh (worker writes the tunnel)
    pendingUpload        → count → binCh (worker WriteAt; on last=true receipt → Complete)
    adminUp              → count → binCh (worker writes the temp file; abort on over-limit/timeout)
    expect               → f.q (bounded queue, consumed by fetchReader; dropped if locally cancelled via closed)
    ```

### 4. Concurrency model (three lines)

```
Message pump (OnMessage callback, single goroutine, frame order guaranteed)
   │  text frame → dispatch (most go async to run serve*)
   │  binary chunk → routing decision (cheap) → submit to channel
   ▼
Connection-level worker (uploadWorker, inbound.go:292) — single worker preserves order
   │  consumes binCh (upload chunk WriteAt/Complete, admin temp-file collection)
   │  consumes fwdCh (forwarding chunks written to the tunnel, backpressure does not block the pump)
   ▼
fetchReader (the caller's goroutine) — consumes f.q, done/errCh signal the end
```

- **Why a worker exists (H5)**: the old implementation ran `WriteAt`/`Complete` (fsync + full-file hashFile) synchronously in the message pump — the instant an 8GB upload completed, every other frame on that connection froze until Complete finished (head-of-line blocking, a slow disk freezing the whole connection). Now routing happens in the pump (cheap) and IO in the worker; ordering is guaranteed by the single worker (comment at conn.go:80-84; REFACTOR.md §5).
- **Bounded backpressure**: `f.q` (8), `binCh`/`fwdCh` (16); if a submission blocks, connection close releases it via `binDone`/`f.closed`, so nothing hangs (conn.go:235-238, 267-269, 275-282).
- **Serialized sending**: `Session.SendFrame`/`SendJSON` have internal write locks (peerjs `sendMu` / WSSession `sendMu`), concurrent serveFile serializes through the lock, and low-water flow control sinks into `peerjs.Connection.SendFrame` (a connection-level global callback, no longer registered per call — replacement callbacks would overwrite each other and cause concurrent deadlock, REFACTOR.md §5 last item).

### 5. Where the three protocol constraints land in conn.go

The three protocol constraints in REFACTOR.md §4 are not just documentation slogans; each has a corresponding enforcement point in the code:

| Constraint | Implementation point | Consequence of breaking it |
|---|---|---|
| 1. JSON control headers must be text frames, data chunks must be binary frames | Sender side: `dcResp{Type:"data"}` goes through `SendFrame` (peerjs/WSSession writes the JSON header + BinaryMessage/binary body); receiver side: `OnMessage` dispatches on `IsText` (conn.go:166, 228) | The peer swallows the control header as a data chunk (REFACTOR.md §5 third row) |
| 2. The data header and the data chunk must be atomically contiguous | peerjs `Connection.SendFrame`'s sendMu (layer ①); on the conn.go receiver side the "connection-level expect" state machine attaches chunks (conn.go:271-283), with at most one expect at a time | Binary chunks misattributed, data from multiple requests interleaved |
| 3. The browser may omit reqId, the Go side always carries it | Outbound openStream uses `uuid.NewString()` (outbound.go:60); inbound serve* echoes the peer's reqId | Responses cannot be paired (the Go side guarantees UUID v4 uniqueness across connections; randHex8 is only 32bit and would collide) |

### 6. Typical flow walkthrough: one req fetch (each end's automatic actions)

Taking node A fetching a file from node B over WebRTC as an example, showing how the two role paths meet on the same connection:

```
A (this end)                         B (the peer)
outbound.go openStream             inbound.go serveFile (started by B's bindConn pump via go)
  ├ reqId=uuid, fetches[reqId]=f     ├ IsStrictSHA256 check → path decision (file_index/CAS)
  ├ SendJSON(req frame) ────────────→   ├ SendJSON(meta{total})  ← outbound routeResponse records total
  └ fetchReader waits on f.q           ├ SendFrame(data header + 64KB chunk) × N (SendFrame is atomic)
                                       └ SendJSON(done)
A's bindConn pump:
  ├ meta text frame → routeResponse (expect not set)
  ├ data text frame → routeResponse: f.size=r.Size, st.expect=f
  ├ binary chunk    → expect hit → f.q <- chunk (received counted, expect cleared when full)
  └ done text frame → routeResponse: received==done.Size check → close(f.done)
fetchReader: consumes q chunks → drains q before EOF (pump order guarantees chunks are enqueued first) → sha256 check → io.EOF
```

If at the same time B sends a req on A's side of the same connection (B also fetching files from A), serveFile and fetchReader do not interfere with each other — text frames are distinguished by verb and reqId, binary chunks by expect/pendingUpload (comment at conn.go:144-149).

### 7. Typical flow walkthrough: one chunked upload (connection-level single slot)

```
B's bindConn pump:
  ├ upload text frame → serveUploadBegin (go async): validates size/offset alignment →
  │   st.pendingUpload = &uploadState{...} (30s stale cap) → replies meta{total,offset}
  ├ binary chunk → up=st.pendingUpload counts → binCh <- binaryChunk{up,...}
  │   (last is set and pendingUpload cleared when got>=size)
  └ next upload frame → serveUploadBegin: pendingUpload non-empty → "already in progress"
B's uploadWorker (single goroutine, preserves order):
  ├ consumes binCh → sess.WriteAt(offset, data) (IO moved out of the pump, H5)
  └ last → sess.Complete() (all bitmap bits set → uploaded{hash,path}; otherwise ack{offset} for resume)
```

Multiple sources in parallel = multiple connections each uploading different chunks (UploadSession bitmap merging); chunks on the same connection are serial (request-response pairing). The fsync+hash at the moment of upload completion runs in the worker, so the message pump does not freeze (comment at conn.go:80-84).

### 8. fetchState: streaming collection state (conn.go:133-141)

```
q        chan []byte   // bounded data chunk queue (8), pump submits / reader consumes
done     chan struct{} // peer's done frame → close; remaining chunks in q are still consumable
errCh    chan error    // errors (including connection close)
closed   chan struct{} // local cancellation (reader.Close) → pump stops submitting
```

- Data chunks **do not reside in state** (streaming); `received` only counts bytes, for the done frame's integrity check (the H6 check in outbound.go routeResponse).
- Channel semantics were carefully chosen: `done` is closed once (preventing panic from duplicate done frames, with an idempotent check inside routeResponse), `errCh` has a buffer of 1 (a late err frame does not block the pump), and `closed` is closed idempotently (in the scenario where the connection disconnects first, double closing by reader cleanup would panic, outbound.go:81-87).

### 6. OnClose: unified cleanup (conn.go:294-340)

Following the principle of "only collect inside the lock, act after unlocking" (calling `Close` while holding the lock is a deadlock pitfall, REFACTOR.md §5):

1. Unregister from `conns`/`pending` (look up `pending[c]` first, then delete; handling happens outside the lock)
2. Inside the lock: abort adminUp (delete the temp file + close the file), deliver errCh + idempotently close(closed) for each fetch, and take the fwd out connection
3. After unlocking: `close(binDone)` lets the worker exit (unconsumed chunks are dropped directly — leftover sessions are cleaned by file_index's 10-minute reap), then `fwdOut.Close()` (the forwarding caller's read side immediately sees EOF)

## Relationships with other modules

```
conn.go (dispatch pump) ──frame──▶ inbound.go  serveFile/serveCreate/serveUploadBegin/...
                              (responds to the peer; security checks H1/H2, uploadWorker persists to disk)
conn.go (initiate+collect) ◀──frame── outbound.go  OpenStream/FetchFromPeer/routeResponse
                              (reqId state machine writes fetches/expect; H6 size cap check)
conn.go (single-slot fwd) ◀──▶  forward.go  fwd-open/challenge/auth handshake + data pass-through
conn.go (single-slot adminUp) ──▶ admin.go  serveAdmin internally forwards the gin engine
conn.go ──fileIndex──▶   file_index.go  SQLite sha256→path (inbound reads/writes, outbound untouched)
conn.go ◀──Session──     ws_session.go / rtc_session.go (the two transport adaptations)
conn.go ◀──binding──     peerjs_service.go  connectLoop/onIncomingConnection/BindLocal
```

- **inbound ↔ outbound**: full-duplex reuse on the same connection, sharing no mutable state with each other (except for their own slots in connState, comment at inbound.go:6-7).
- **file_index**: the serve* index verbs are file_index's frame-protocol exit; `UploadSession` bitmap tracking is on the inbound side, and conn.go only delivers chunks to the worker.
- **forward**: handshake responses (fwd-challenge/ok/err) route in the **same slot** as file fetch responses (routeForwardResponse, conn.go:220-222); only after the tunnel is established does the `fwd` single slot get occupied (REFACTOR.md §3.9: handshakes do not occupy a slot, preventing tunnel floods).
- **Business layer (④)**: reuses the same fetch path via `FetchFromPeer("local", ...)` — local WS sessions and remote nodes have zero branching (comment at peerjs_service.go BindLocal).

## Pitfalls and design decisions

> Each item notes its source: code comments (conn.go/inbound.go/outbound.go), test files, REFACTOR.md.

| # | Pitfall | Design/fix | Source |
|---|---|---|---|
| 1 | The old `serveFile` implementation did `req.Hash[:2]` directly; if the peer sent an empty/short hash it panicked out of bounds, **and a panic inside a goroutine kills the entire process** (any node on the public signaling network could crash all nodes with a single JSON line) | serveFile checks `hashutil.IsStrictSHA256` first (H1); test TestServeFile_InvalidHashNoPanic | at the start of inbound.go serveFile; peerjs_service_test.go TestServeFile_InvalidHashNoPanic |
| 2 | After the peer can create an arbitrary absolute path, it can req-read it (/etc/shadow attack chain) | A path hit in file_index must pass `IsPathAllowed` to fall within the allowed root, otherwise fall back to content-addressed storage (H2); test TestServeFile_IndexPathOutsideRoot | the fallback branch of inbound.go serveFile; peerjs_service_test.go TestServeFile_IndexPathOutsideRoot |
| 2a | Infinite recursion from source routing after multi-source routing: A↔B interconnected, B requests a file A lacks → A routes to B → B routes to A → infinite recursion | The req frame carries the trace node chain; a node that finds itself already in the chain refuses (`err "loop detected"`); the root request carries no trace (omitempty for older-peer compatibility), and OpenStreamFrom is the propagation point; tests TestServeFile_LoopDetected / TestOpenStreamFrom_TracePropagation | at the start of inbound.go serveFile; conn.go dcReq.Trace; outbound.go OpenStreamFrom (2026-08-18) |
| 3 | An 8GB upload's Complete (fsync+hash) ran synchronously in the message pump → the entire connection froze head-of-line | WriteAt/Complete moved out of the pump into a connection-level worker (H5); the pump only does routing decisions; on 2026-08-18 upload and forwarding were split into uploadWorker/fwdWorker dual workers (an 8GB upload no longer blocks forwarding tunnels); test TestUploadWorker_WriteThenComplete + integration TestConcurrentLargeFetches | conn.go bindConn (dual workers); inbound.go uploadWorker/fwdWorker; REFACTOR.md §5 |
| 4 | The peer sends an upload header but no data chunks → pendingUpload occupies the slot forever, after which every upload on that connection returns "already in progress" (a connection-level DoS, only recovered by reconnecting) | Auto-cleared after 30s stale (M6); test TestServeUploadBegin_StalePendingCleared | conn.go connState.pendingUpload; inbound.go serveUploadBegin; peerjs_service_test.go TestServeUploadBegin_StalePendingCleared |
| 5 | A malicious peer declares an oversized data size / sends done early (truncated data as meta+done treated as success) → unbounded allocation OOM / silent data corruption | `maxPeerFetchSize` 8GB cap + comparing done.Size against bytes actually received (H6); tests TestRouteResponse_DataSizeCap / DoneSizeMismatch | outbound.go fetchReader/routeResponse; peerjs_service_test.go TestRouteResponse_* |
| 6 | After `f.size = r.Size` the expect is closed by the done frame — a duplicate done, or an err arriving afterward, would double-close f.done and panic | An idempotent check in routeResponse (ignore if done is already closed) | outbound.go routeResponse |
| 7 | The admin declaration frame's first version used `go` async → when subsequent binary frames reached the pump first, `adminUp` was still empty, **all upload chunks were lost** | `case "admin"` runs serveAdmin **synchronously** in the pump (slot occupation must be completed inside the pump); test admin_test.go | conn.go bindConn; admin.go serveAdmin |
| 8 | Calling `conn.Close()` while holding the lock deadlocks (Go mutexes are not reentrant) | bindConn OnClose only collects inside the lock and Calls Close after unlocking | conn.go bindConn OnClose; REFACTOR.md §5 |
| 9 | Concurrent serveFile each registering `OnBufferedAmountLow` (pion replacement callback) → only the last registrant receives low-water events, the rest deadlock (reproduced with 4 concurrent × 2MB) | Flow control sinks into peerjs.SendFrame (registered once globally + lowWater broadcast); serveFile has zero flow-control code | inbound.go serveFile; REFACTOR.md §5 last item |
| 10 | On connection close, a blocked fetch submission hangs | errCh submission (non-blocking) + idempotent close(f.closed) releases the pump; tests TestOpenStream_ConnClosed / CloseCancel | conn.go bindConn OnClose; stream_test.go TestOpenStream_* |
| 11 | The peer sends only meta+done and does not verify bytes actually received → a truncated file is treated as success | done.Size integrity check (H6) | outbound.go routeResponse |
| 12 | Illegal admin frames / text frames without a type | Silently dropped in the pump (no panic, no reply frame) | conn.go bindConn |

## Tests (full transport package, `scripts/test-layers.sh` L2 section)

### Unit tests (no network, `go test -tags nosqlite ./internal/transport/ -count=1 -skip "^TestAdmin"`)

| Test file | Coverage | Background of discovery |
|---|---|---|
| peerjs_service_test.go | Frame routing and security boundaries | fakeSession injects frames to drive the pump (see each test comment) |
| ├ TestServeFile_InvalidHashNoPanic | Illegal hash → err frame, no panic | **H1 remote crash**: an out-of-bounds slice kills the process |
| ├ TestServeFile_IndexPathOutsideRoot | Index hit but path is out of bounds → refuse to return | **H2 arbitrary file read**: defense against legacy dirty data |
| ├ TestRouteResponse_DataSizeCap | Oversized data frame → errCh | **H6 OOM**: no cap on chunk size |
| ├ TestRouteResponse_DoneSizeMismatch/Match | Rejected truncated done / normal done passes | **H6 silent data corruption** |
| ├ TestServeUploadBegin_StalePendingCleared | Expired upload slot auto-cleared | **M6 connection-level DoS** |
| ├ TestUploadWorker_WriteThenComplete | Worker persists + uploaded reply frame | **H5 path**: regression after moving IO out of the pump |
| └ TestHashMatchesSHA256_AllowsEmptyFile | Empty-file sha256 check passes | **Code review**: an empty-data special case wrongly rejected legitimate empty files |
| stream_test.go | Full streaming OpenStream chain | see each test comment |
| ├ TestOpenStream_StreamingRead | meta/data/data/done chunk reassembly + sha256 check | **source system**: the old full buffer had 8GB OOM risk |
| ├ TestOpenStream_CloseCancel | Early Close does not block the pump, cleans up the routing table, idempotent | Defensive (cleanup path regression) |
| └ TestOpenStream_ConnClosed | On connection close the reader errors rather than hanging | Defensive (close timing) |
| forward_test.go | fwd handshake/overreach/replay/data pass-through (8 unit tests) | REFACTOR.md §3.9 |

### Integration tests (`-tags "nosqlite integration" ./test/integration/ -count=1 -p 1`, offline from the public internet)

> 2026-08-18 (6th optimization): integration test signaling defaults to the **global self-hosted
> signalserver** started by TestMain (an in-memory httptest service); the data plane is real
> same-machine WebRTC (host candidate direct connection, no STUN) — no longer dependent on the
> 0.peerjs.com public cloud (no proxy/outernet needed).
> The MQTT public broker tests require `PEERDRIVE_MQTT_TEST=1`; the full live chain requires `PEERDRIVE_LIVE_TEST=1`.

| Test | Coverage | Background of discovery |
|---|---|---|
| interop_test.go TestTwoNodes/ThreeNodes/FourNodesStar | Two/three/four-node interop, star one-to-many concurrency | Feature acceptance |
| interop_test.go TestConcurrentLargeFetches | 4 concurrent × 2MB large file fetches | **Flow-control deadlock regression**: before the fix it hung until timeout (REFACTOR.md §5) |
| file_lifecycle_test.go TestFileLifecycleEndToEnd | create/req/upload/download/sync closed loop across nodes | Feature acceptance |
| ws_verbs_test.go TestFrameVerbs_* | Chunked upload/resume/sync chains | Feature requirement |
| ws_test.go TestLocalWSSessionFetch etc. | Local WS session + FetchFromPeer("local") reuse | Architecture decision (§3.5) |
| selfhosted_test.go TestSelfHosted* | Self-hosted signaling protocol compatibility + discovery API full chain | Feature requirement (§3.6) |
| live_test.go TestLive* | Live signaling+discovery+fetch full chain (`PEERDRIVE_LIVE_TEST=1`) | Live verification (AGENTS.md deployment section) |

> ⚠️ Integration tests must run with `-p 1` serially: multiple test groups share the global self-hosted
> signaling, and parallelism would interfere with each other.
> The data plane is real WebRTC — a sandbox without UDP (docker's default) cannot connect; use
> `PEERDRIVE_SKIP_RTC=1` to skip interop tests (local/WS tests are unaffected).

## File inventory

> References use function names throughout (line numbers drift easily, see the convention in REFACTOR.md §10).

| File | Description |
|---|---|
| `conn.go` | This document: frame types, connState, bindConn dispatch pump, OnClose cleanup |
| `inbound.go` | Inbound role: serveFile (multi-source routing FileRouter)/serve* index verbs/uploadWorker+fwdWorker (leaf nodes of the pump) |
| `outbound.go` | Outbound role: OpenStream/OpenStreamFrom/FetchFromPeer/routeResponse/fetchReader |
| `ws_session.go` | WSSession: local WS adaptation (see sessions.md) |
| `rtc_session.go` | rtcSession: DataChannel adaptation (see sessions.md) |
| `peerjs_service.go` | Assembly layer: signaling lifecycle, connection establishment, BindLocal, Connect de-duplication, SetFileRouter |
| `admin.go` | admin verbs (conn.go synchronous dispatch + adminUp single-slot collection) |
| `forward.go` | fwd-* verbs (fwd single slot + fwdCh worker writing the tunnel) |
| `file_index.go` | SQLite sha256→path index (inbound reads/writes) |
| Tests: `peerjs_service_test.go` / `stream_test.go` / `forward_test.go` / `file_index_test.go` / `admin_test.go` / `servefile_router_test.go` | Per-module unit tests (discovery background in the table above) |
