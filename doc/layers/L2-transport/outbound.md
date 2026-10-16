# Outbound Role (outbound.go)

> One-line responsibility: the full set of verbs initiated by this end that collect responses
> ("I ask others") — initiating `req` fetches (OpenStream/FetchFromPeer), routing responses by reqId
> (meta/data/done/err), and the streaming reader with integrity checking.

## Responsibilities

`outbound.go` is the frame protocol layer's "outbound role" implementation, paired against `inbound.go` (answering the peer).
The same connection is reused full-duplex and concurrently: this end initiates fetches through this file while serving the
verbs sent by the peer.

This file is responsible for three blocks:

1. **Initiating fetches**: `OpenStream` (streaming, the peerSource adaptation entry for the source system) /
   `FetchFromPeer` ([]byte compatibility wrapper) → `openStream` sends the `req` frame and creates
   the `fetchState` routing table entry.
2. **Streaming reads**: `fetchReader` consumes data chunks from a bounded chunk queue, handling done/err/timeout/cancel,
   and performs the sha256 check at EOF for full requests (content-addressed fallback).
3. **Response routing**: `routeResponse` delivers the peer's reply frames (meta/data/done/err) to
   the corresponding fetch state by reqId — containing all defensive checks (H6: size caps, done integrity).

Cleanup on connection close (errCh delivery + close(closed)) is done uniformly in `conn.go`'s `bindConn`
OnClose; this file only handles initiation and collection (`openStream`'s cleanup cooperates idempotently with OnClose).

**Does not do**: does not handle admin frames (admin.go), does not handle forward handshake responses
(forward.go's `routeForwardResponse`, conn.go splits by frame type), and does not write to disk
(upload is the inbound role's responsibility).

### Outbound API overview

| API | Signature | Purpose |
|---|---|---|
| `OpenStream` | `(peerID, hash, offset, size) → io.ReadCloser` | Streaming fetch (peerSource adaptation entry); Close can cancel early |
| `FetchFromPeer` | `(peerID, hash, offset, size) → ([]byte, error)` | Whole-fetch into []byte (compat with old callers / integration tests) |
| `routeResponse` | `(st *connState, r dcResp)` | Called from the pump's default branch, routing response frames by reqId |
| `stateFor` | `(c Session) → *connState` | Connection state lookup (the pending table) |
| `hashMatchesSHA256` | `(hash string, data []byte) → bool` | Content-addressed verification (old path / defensive) |

## Key mechanisms

### Initiation (OpenStream → openStream, outbound.go:32,58)

```
OpenStream(peerID, hash, offset, size) → io.ReadCloser
  1. look up conns[peerID] (none → error)
  2. openStream:
      - reqId = uuid.NewString() (UUID v4, unique across connections — randHex8 is only 32bit and would collide)
      - fetchState{q: chan []byte(8), done, errCh(1), closed} registered into st.fetches[reqID]
      - SendJSON(req frame) → return the fetchReader
  3. cleanup (sync.Once): delete the routing table + close(f.closed) (idempotent against double-close panic)
```

`FetchFromPeer` keeps the []byte signature for compatibility with old callers/integration tests; internally it is `OpenStream` +
`io.ReadAll` — under streaming semantics, the sha256 check for a full request is completed by the reader at EOF,
matching the old implementation's semantics.

### fetchState (conn.go:133, the state core of the outbound role)

```
fetchState{
  reqID    string         // routing key (UUID v4)
  size     int64          // the expected data chunk size (set by routeResponse; see maxPeerFetchSize for the cap check)
  received int64          // bytes already delivered to the queue (used for the done integrity check)
  q        chan []byte    // data chunk queue (bounded 8: the message pump delivers / fetchReader consumes, backpressure does not occupy the pump)
  done     chan struct{}  // close → the peer's done frame (transfer complete; chunks remaining in q are still consumable)
  errCh    chan error     // errors (including connection close; buffered 1 so it does not block the pump)
  closed   chan struct{}  // local cancellation (reader.Close): the pump stops delivering, chunks are dropped
}
```

Data chunks **do not reside in state** (streaming: delivered into a bounded queue for the reader to consume); `received` is only a
byte counter — used for the done frame's integrity check.

### A complete frame exchange for one fetch (including a range request)

```
this node (outbound role)                      peer (inbound role serveFile)
  │ {type:"req",hash:"<64hex>",offset:0,size:-1,reqId:"<uuid-v4>"} →│
  │← {type:"meta",hash,total:1024,reqId}
  │← {type:"data",hash,offset:0,size:700,reqId} + 700B binary chunk
  │← {type:"data",hash,offset:700,size:324,reqId} + 324B binary chunk
  │← {type:"done",hash,offset:0,size:1024,reqId}
  → reader EOF: full request (offset==0 && size<0) → sha256 check
```

Corresponding flow:
1. `openStream` sends the req frame and registers fetchState into `st.fetches[reqID]`.
2. Pump receives `meta` → routeResponse validates the total cap → no expect.
3. Pump receives a `data` header → routeResponse validates size → `st.expect = f`.
4. Pump receives a binary chunk → `st.expect` hits → deliver to `f.q` (bounded 8, backpressure does not occupy the pump) +
   `f.received += len`; when `received >= f.size`, clear expect.
5. Pump receives `done` → routeResponse validates `done.Size == f.received` → close(f.done).
6. The reader consumes q; after done, q is empty → before EOF, non-blocking check of errCh → finish: for a full request
   check sha256, reporting "content hash mismatch" on mismatch.

Range request (offset>0 or size>=0): verify is not enabled (no complete content to compare against), and `done.Size`
must still equal the bytes actually received — the integrity check and the content check are decoupled (H6 vs H5).

### Streaming reads (fetchReader.Read, outbound.go:130)

```
loop:
  1. buf has remaining → copy and return
  2. select four ways:
      - q ← chunk: feed sha256 when verifying, buf = chunk
      - done ← the peer's done frame: q may still have chunks (pump order: chunks are enqueued first, done closes later)
        only take one chunk at a time — after buf is consumed, the outer for returns to select (done stays ready and takes the next chunk),
        until q is empty to EOF; before EOF, a non-blocking errCh check (preventing an err frame from being masked by done)
      - errCh ← error: finish(err) returns
      - fetchIdleTimeout (5-minute chunk-interval timeout) / ctx.Done: finish reports an error
```

- **Chunk-interval timeout**: the old implementation used a 5-minute total timeout — a total timeout is meaningless under streaming
  for large files (8GB, many chunks), so it changed to "no data chunks from the peer for 5 minutes counts as stalled" (the original total-timeout
  semantics are covered by the peer connection's keepalive).
- **verify**: only enabled for full requests (offset==0 && size<0) — at EOF, `finish` compares the
  sha256 and reports "content hash mismatch" on mismatch (H5 content-addressed fallback, against man-in-the-middle / peer
  corrupting data silently entering storage).
- **finish is idempotent** (outbound.go:189): the eof flag prevents duplicates; a full request verifies
  sha256 at EOF; cleanup runs only once.
- **Close for early cancellation**: finish(io.ErrClosedPipe) — locally drop subsequent chunks (the pump stops
  delivering via f.closed) without disconnecting.

### Response routing (routeResponse, outbound.go:231)

The peer's reply frames (reqId matched against fetches[reqID]) are handled by type; all defensive logic is here:

| Frame | Handling |
|---|---|
| `meta` | `Total > maxPeerFetchSize(8GB)` → reject (total is the full size; for a range request it is ≠ the amount received this time, so only a cap check is done) |
| `data` | `Size <= 0 || Size > maxPeerFetchSize` → reject (H6 unbounded allocation); otherwise `f.size = r.Size`, `st.expect = f` (awaiting the next binary chunk; the pump routes by this) |
| `done` | Idempotent (a duplicate done is not processed, preventing a duplicate close(done) panic); **integrity check**: `done.Size` (the bytes the peer declares as actually sent) must equal `f.received` (bytes delivered), otherwise reject (H6 truncation counted as success) |
| `err` | reject (a late err frame: if done is already closed, ignore it; errCh is buffered so it does not block) |

- `f.size` is "the expected data chunk size" — in the pump, expect is cleared when `received >= size`.
- `reject` delivers one error to errCh; frames arriving after done are ignored uniformly (finish is idempotent).

## Relationships with other modules

| Module | Relationship |
|---|---|
| `conn.go` (shared core) | The pump delivers binary chunks to `fetchState.q` per `st.expect`; OnClose does unified cleanup (errCh delivery + close(f.closed)); `routeResponse` is called from the pump's default branch |
| `inbound.go` (inbound role) | Full-duplex on the same connection: the `req` this file sends is answered by the peer's serveFile; this file's routeResponse collects it. `connState.fetches/expect` belong to outbound, `pendingUpload/binCh` to inbound, flattened and shared |
| `source/peer.go` (PeerSource) | `OpenStream` is peerSource's adaptation entry (source system: a local hit returns immediately, a miss degrades to peer) |
| `service/peerjs_service.go` | `FetchFromPeer` is called directly by endpoints such as /peerjs/fetch (REFACTOR §3.8 boundary: semantics preserved, not switched to SourceManager) |
| `file_index.go` | Path resolution for fetching is on the inbound side (serveFile prefers the index + CAS fallback); this file only fetches by hash |
| `forward.go` | `routeResponse` and `routeForwardResponse` route in the same slot (conn.go splits by frame type: fwd-* goes to forward, the rest to this file) |

## Pitfalls and design decisions

1. **H6 unbounded allocation OOM** (maxPeerFetchSize, outbound.go:27). The chunk size /
   file size declared by data frames had no upper bound → a malicious peer declaring 1<<62 and continuously sending data frames →
   `f.got` grows unboundedly. Fix: an 8GB cap for both `data.Size` and `meta.Total` (consistent with the upload cap).
2. **H6 silent data corruption** (routeResponse's done branch, outbound.go:277). The old implementation's done
   did not check the bytes actually received — the peer could just send meta+done and return truncated/empty data as success. Fix:
   `done.Size` must equal `f.received` (compared against the done frame's Size rather than meta.total —
   for a range request, total is the full size). Combined with the reader-side pre-EOF errCh check, an err frame
   is never masked by done.
3. **H5 content-addressed fallback** (fetchReader.finish, outbound.go:194). The byte count matches but
   the content does not (peer corruption / man-in-the-middle tampering) → the sha256 check fails and reports an error at EOF. Only
   enabled for full requests (a range request has no complete content to compare).
4. **reqId uses UUID v4** (outbound.go:60). reqId is the response routing key; randHex8 is only
   32bit and may collide under high concurrency — UUID v4 guarantees uniqueness across connections.
5. **q still has chunks after done** (outbound.go:149-173). Pump order guarantees chunks are enqueued first and done closes
   later. Read's done branch **takes only one chunk at a time** — it was once a loop taking chunks, and the loop's buf
   assignment overwrote an unconsumed chunk (the chunk-1-lost bug, reproduced by the streaming test).
6. **Idempotent cleanup** (cleanup sync.Once + idempotent close, outbound.go:75-89). The scenario where the connection disconnects first:
   OnClose may already have closed(f.closed) — a duplicate close would panic; check with select, then
   close. finish itself is also idempotent (the eof flag).
7. **An empty file is fetchable** (hashMatchesSHA256, outbound.go:216). `len(data)==0` should
   not be rejected outright: sha256(empty) is a legal content-addressed value. hash is user input and must first be ensured to be a legal 64-char
   hex (otherwise the comparison always fails).
8. **Error collection does not block** (reject's select+default, outbound.go:248-251). errCh
   is buffered 1; duplicate/late errors are dropped directly, so the message pump does not hang.
9. **Leftover semantics from the streaming refactor** (FetchFromPeer keeps []byte). When the source system was made streaming,
   requestFile moved to a chunk queue + pump delivery, but FetchFromPeer's signature was preserved for caller compatibility;
   the old implementation's 8GB full-buffer OOM risk is eliminated by streaming.
10. **Connection close → a reader error, not EOF** (conn.go OnClose). OnClose delivers "connection closed"
    to each fetch's errCh, so the reader returns an error rather than hanging; close(closed) is checked idempotently (the reader may have already
    cleaned up itself). A test's error must not be io.EOF — ReadAll treats EOF as a normal end.

### Collaboration with the source system (peerSource adaptation)

`OpenStream` is the streaming adaptation entry for `internal/source/peer.go` (PeerSource), with the chain:

```
source system routing (local miss) → PeerSource.Open(...) → PeerJSService.OpenStream
  → conns[peerID] → openStream sends req → fetchReader
  → the reader gives the Source layer chunk-by-chunk reads (CapStream semantics)
```

- Large files only go through CapStream (OpenRange rejects CapFile sources — against 8GB full-buffer OOM,
  REFACTOR §3.8).
- `FetchFromPeer` ([]byte compat signature) is called directly by the `/peerjs/fetch` endpoint — semantics
  preserved, not switched to SourceManager (REFACTOR §3.8 boundary note).
- Connection-level expect single-slot constraint: at most one fetch flow per peer at a time (per-peer
  Mutex.TryLock serial attempt, see PeerSource) — this is the business-layer embodiment of the fetchState single expect slot.

### The defensive matrix for peer response frames (all routeResponse branches)

| Frame | Legal scenario | Reject scenario | Reject action |
|---|---|---|---|
| `meta` | total ≤ 8GB | total > 8GB (malicious declaration) | errCh ← reject |
| `data` | 0 < size ≤ 8GB | size ≤ 0 or > 8GB (H6 unbounded allocation) | errCh ← reject, no expect set |
| `done` | size == received (matches actual receipt) | size != received (H6 truncation as success) | errCh ← reject, no close(done) |
| `err` | any msg | late (done already closed) | ignore (select done inside reject) |
| unknown reqId | — | reqId not in the fetches table | silently ignore (return) |
| empty reqId | — | no routing key | silently ignore (return) |

Defense essentials: **reject does not panic** (done's idempotent close check), **reject does not block**
(errCh buffered 1 + select default), **reject does not duplicate** (late frames ignored). Combined with
the reader-side pre-EOF non-blocking errCh check — an err frame is never masked by done.

### fetchReader lifecycle states

| State | Entry condition | Exit condition | Exit action |
|---|---|---|---|
| In progress | openStream registration succeeded | received done / err / timeout / ctx cancel / Close / connection close | — |
| Complete (EOF) | done frame + q empty | — | finish: for a full request, verify sha256; the error path returns err |
| Failed | errCh has an error | — | finish(err) returns the error |
| Cancelled | reader.Close() | — | finish(io.ErrClosedPipe); the pump stops delivering |

finish is the only exit and is idempotent (the eof flag): duplicate calls return directly. cleanup (delete
from fetches + close(closed)) runs only once via sync.Once — cooperating with bindConn's OnClose
cleanup (when the connection disconnects first, OnClose already did it; cleanup skips idempotently).

## Tests

| Test | Background of discovery |
|---|---|
| `TestOpenStream_StreamingRead` (stream_test.go:57) | **Streaming refactor (source system)**: the old requestFile held everything in memory with a full buffer, an 8GB file had OOM risk; after switching to a chunk-queue stream, verifies the chunk delivery→consumption→verification chain (including that reqId must be carried and the sha256 check) |
| `TestOpenStream_CloseCancel` (stream_test.go:131) | **Defensive**: after an early Close, pump delivery does not block (the closed channel lets it through), the routing table is cleaned, and repeated Close is idempotent |
| `TestOpenStream_ConnClosed` (stream_test.go:149) | **Defensive**: after connection close the reader returns an error rather than hanging (errCh delivery; the error must not be io.EOF — ReadAll treats EOF as a normal end) |
| `TestRouteResponse_DataSizeCap` (peerjs_service_test.go:174) | **H6 unbounded allocation OOM**: a malicious data frame declares 1<<62 → must report an error on errCh (≤8GB cap) |
| `TestRouteResponse_DoneSizeMismatch` (peerjs_service_test.go:192) | **H6 silent data corruption**: the peer only sends meta+done (actual 0 bytes vs declared 100), returning truncated data as success → must report an error and must not close(done) |
| `TestRouteResponse_DoneSizeMatch` (peerjs_service_test.go:214) | A normal done (Size matching actual receipt) → success (done already closed = transfer complete; streaming semantics: data is consumed via f.q) |
| `TestHashMatchesSHA256_AllowsEmptyFile` (peerjs_service_test.go:316) | **Code review**: the original implementation returned false directly on `len(data)==0`, so a legal sha256(empty) file could never be fetched from the peer. Fix: removed the empty-data special case |
| `TestServeFile_InvalidHashNoPanic` / `TestServeFile_IndexPathOutsideRoot` (peerjs_service_test.go:130,145) | H1/H2 are inbound-side vulnerabilities, but H1's trigger surface is the peer's req — the same protocol as the frames outbound initiates; listed as a cross-reference |

## File inventory

- `back/internal/transport/outbound.go` (285 lines) — this module
- `back/internal/transport/conn.go` — fetchState definition, in-pump expect routing, OnClose unified cleanup
- `back/internal/transport/stream_test.go` — OpenStream/fetchReader full-chain tests
- `back/internal/transport/peerjs_service_test.go` — routeResponse defensive tests + fakeSession infrastructure
- `back/internal/source/peer.go` — PeerSource (the business consumer of OpenStream)
- `doc/REFACTOR.md` §3.8 (unified source system, requestFile streaming), §4 (frame protocol)
- `doc/archive/TRANSPORT-REVIEW-2026-08-15.md` — original H5/H6 findings and fix records
