# Inbound Role (inbound.go)

> One-line responsibility: the full set of verbs that respond to what the peer sends ("someone asks me, I answer") — `req` file fetch responses,
> the file index's six verbs (create/upload/list/info/delete/sync) server side, and the upload chunk-to-disk worker.

## Responsibilities

`inbound.go` is the frame protocol layer's "inbound role" implementation, paired against `outbound.go` (this end initiates).
**A role is a "frame role", not a connection direction**: WebRTC connections are full-duplex symmetric, so the same Session
carries both roles simultaneously — one side responds to the peer's `req`, while the other collects responses to requests it initiated (dispatched in
`conn.go`'s `bindConn` by frame type + reqId).

This file is responsible for three blocks:

1. **File serving** (`serveFile` / `openFile`): when the peer sends a `req` frame, return this node's file
   in chunks (`meta` → `data×N` → `done`).
2. **File index verb server side** (`serveCreate` / `serveUploadBegin` / `serveList` /
   `serveInfo` / `serveDelete` / `serveSync`): the peer registers external files, streams chunked uploads,
   queries, logically deletes, and incrementally syncs. Request fields are uniformly carried by the `dcResp` generic structure
   (Hash/Offset/Size/ReqID/Path/Name/Seq).
3. **Upload-to-disk worker** (`uploadWorker`): a connection-level IO worker that consumes the binary chunks the
   message pump delivers (upload persistence, admin upload collection, forwarding chunk writes to the tunnel), moving slow IO out of the message pump.

Note: this **does not include** server-side handling of forwarding handshakes — `serveForwardOpen`/`serveForwardAuth`/
`serveForwardClose` live in `forward.go` (forward v2 is one leg of inbound, carried in its own file).

### The full set of inbound verbs (dispatch in conn.go bindConn's switch)

| Frame type | Handler | Response | Description |
|---|---|---|---|
| `req` | `serveFile` | `meta` / `data×N` / `done` / `err` | File fetch (range-capable) |
| `create` | `serveCreate` | `created {hash,size,name,path,seq}` / `err` | Register an external file, no copy |
| `upload` | `serveUploadBegin` | `meta {total,offset}` → `uploaded`/`ack` / `err` | Streaming chunked upload (multi-source concurrency) |
| `list` | `serveList` | `list-resp {files,total}` / `err` | Paginated listing |
| `info` | `serveInfo` | `info-resp {hash,size,name,path,seq}` / `err` | Look up metadata by hash |
| `delete` | `serveDelete` | `deleted {hash,seq}` / `err` | Logical delete (tombstone) |
| `sync` | `serveSync` | `sync-resp {files,lastSeq}` / `err` | Incremental sync by seq cursor |

Except for `req`, all are synchronous small operations — they `go` escape in `bindConn` (async goroutine) and do not block
the message pump. `admin` frames are the exception: they run synchronously (adminUp's slot occupation must complete inside the pump, see the conn.go comment).

## Key mechanisms

### File fetch response (serveFile, inbound.go:35)

Peer `req {hash, offset, size, reqId}` → this node returns by range:

```
1. hash must be 64 hex chars (IsStrictSHA256), otherwise reply an err frame (H1)
2. Path resolution priority:
   ① file_index hit and IsPathAllowed(fi.Path) → use the index path
   ② index hit but the path is out of bounds (historical dirty data / malicious registration) → fall back to content-addressed storage (H2)
   ③ index miss → filepath.Join(storageDir, hash[:2], hash)
3. os.Open + Stat → total
4. offset/size clamping (offset<0→0, offset>total→total, limit over-bounds→truncate)
5. send meta{total} → loop ReadAt in chunks (chunkSize=64KB), sending data header+chunk → done
```

- Chunk size constant `chunkSize = 64 * 1024` (pion SCTP's single-message limit is about 256KB; 64KB balances flow-control
  granularity); write buffer flow control has sunk into `peerjs.Connection.SendFrame` (a connection-level global callback).
- A failed `meta` send returns immediately (the connection is already dead; continuing would be futile).
- `openFile` (inbound.go:105) is a frame-free wrapper of the same content-addressed open logic (for the local/admin plane to
  read files directly), with the hash check similarly upfront.
- One `req`'s response frame order is `meta → data×N → done`; any failure point replies with an `err` frame to terminate.
  The peer's outbound role (routeResponse) pairs by reqId, and the consistency check between `done.Size` and bytes actually received
  is the peer's responsibility (see outbound.md).

### File index verb server side

- **serveCreate** (inbound.go:145): register an external file (sha256 → absolute path, no file
  copy). Empty `path` → err; `fileIndex.Create` internally does the root directory check (H2). Response
  `created {hash,size,name,path,seq}`.
- **serveUploadBegin** (inbound.go:161): start receiving a chunked upload.
  - Security: reject `size < 0 || size > 8GB`; `offset` must be chunk-aligned (64KB);
    reject `offset >= size`.
  - **Special case for an empty file**: when `size==0` there are no data frames to send, so directly `sess.Complete()` and reply
    `uploaded` — otherwise the connection-level `pendingUpload` would stall until the 30s stale cleanup (and the old implementation
    rejected size<=0 outright, so the two ends were asymmetric).
  - Chunk length: `min(size-offset, uploadChunkSize)`, a single data chunk ≤64KB.
  - Connection-level single slot `st.pendingUpload`: if occupied and not expired → `err "upload already in
    progress"` (multiple sources go over multiple connections); **occupancy over 30s is auto-cleared** (M6).
  - Response `meta {total, offset: ContiguousOffset()}` — offset is the contiguous written offset
    (the resume starting point), computed by the UploadSession bitmap.
- **serveList** (inbound.go:227): `list {offset?, size?}` → `list-resp {files,
  total}`. Each item is redacted via `redactDisallowedPath`; when files is nil it is set to `[]` (JSON
  serialization is not null).
- **serveInfo** (inbound.go:245): `info {hash}` → `info-resp` (look up before download).
- **serveDelete** (inbound.go:258): `delete {hash}` → `deleted {hash, seq}`.
  seq is the sync cursor; the peer's sync can then track the delete (L6).
- **serveSync** (inbound.go:269): `sync {seq}` → `sync-resp {files, lastSeq}`
  metadata incremental sync (including tombstones).
- **redactDisallowedPath** (inbound.go:136): path redaction for externally reported file information — create already
  enforces inside the root directory, but historical databases/old versions may retain out-of-root Paths; serveFile has already fallen back to CAS,
  but list/info/sync must also not leak such absolute paths.

### Upload-to-disk worker (uploadWorker, inbound.go:292)

The message pump (conn.go) only does "routing decisions" (cheap); all persistence IO moves here (H5):

```
select {
  binCh ← chunk:
    - ch.au != nil → admin-plane upload collection (admin.go reuses the same worker architecture)
    - otherwise ch.up.sess.WriteAt(offset, data) → on failure reply an err frame
      - ch.last (this chunk fully received) → sess.Complete():
        done=true → reply uploaded{hash,path}; bitmap not full → reply ack{offset} so the next chunk can continue
  fwdCh ← forwarding chunks: ch.fw.out.Write (best effort; on failure silently drop + close)
  binDone ← connection close: exit (unconsumed chunks are dropped directly; leftover sessions are cleaned up by file_index's 10-minute reap)
}
```

- Single worker preserves order: chunks are written to disk in arrival order, consistent with the frame protocol order.
- Why a single worker must be serial: multiple goroutines concurrently doing WriteAt would disorder them (chunk order is
  determined by frame order), and the "all bitmap bits set" judgment in Complete requires ordered visibility of writes.
- Forwarding chunks also reuse this worker (the same idea as H5), so the write direction does not stall the message pump.
- Exit semantics: `binDone` is closed on connection close (conn.go OnClose) — the worker exits from select,
  and unconsumed chunks are dropped directly (the connection is already dead); leftover UploadSessions are cleaned up by file_index's
  10-minute reap as a backstop.

### Connection-level slots (connState, the parts this role occupies)

The inbound role occupies two slots on `connState` (flattened and shared; it does not interfere with outbound's
fetches/expect):

| Slot | Purpose | Lifecycle |
|---|---|---|
| `pendingUpload` | the streaming upload currently being received (single stream) | created at serveUploadBegin, cleared when fully received (got>=size in the pump) or by the 30s stale cleanup |
| `binCh`/`binDone` | binary chunk delivery channel + worker exit signal | created by bindConn, OnClose does close(binDone) |

`uploadState` (conn.go:121) records this chunk's reqID/offset/size/got/sess/created —
`got` is counted in the pump (`up.got += len(msg.Data)`); once fully received, `pendingUpload` is cleared and a
chunk with `last=true` is delivered to trigger Complete.

### A complete frame exchange for one chunked upload (multi-source semantics)

```
peer (source 1)                    this node (inbound role)
  │ {type:"upload",name:"a.bin",size:131072,offset:0,reqId:"u1"} →│
  │← {type:"meta",total:131072,offset:0,reqId:"u1"}
  │ {type:"data",...} + 64KB binary chunk (chunk 0)───────────────→│
  │← {type:"ack",offset:65536,reqId:"u1"}        ← bitmap not full, resume starting point
peer (source 2, another connection)
  │ {type:"upload",name:"a.bin",size:131072,offset:65536,reqId:"u2"} →│
  │← {type:"meta",total:131072,offset:0,reqId:"u2"}   ← is the contiguous written still 0? No:
  │                                                    (this diagram is a sequential example; with multiple sources
  │                                                     and out-of-order writes, offset=ContiguousOffset())
  │ {type:"data",...} + 64KB binary chunk (chunk 1)───────────────→│
  │← {type:"uploaded",hash:"<sha256>",path:"...",reqId:"u2"}  ← bitmap full; the requester of the last chunk receives it
```

Key points:
- Reopening a session with the same name = idempotent reuse (`BeginUpload` looks up the session by the sanitized
  name; size must match, M7).
- Every chunk goes through `serveUploadBegin` → in-pump binary routing (the pendingUpload slot) →
  `binCh` → worker `WriteAt`; the `last` chunk triggers `Complete`.
- Bitmap not full → reply `ack{offset}` rather than `uploaded` — the client can use this to continue the next chunk
  (the offset is suggested to resume from `ContiguousOffset`).
- An empty file (size=0) has no data frames: serveUploadBegin directly Completes and replies uploaded.

### Frame protocol constraint cross-reference (this file must comply, REFACTOR §4)

1. **Text frame = JSON control header, binary frame = data chunk**: a data header must use `SendJSON`,
   and a data chunk must use `SendFrame` (both this file's serveFile and in-pump routing comply; if reversed, the peer
   swallows the control header as a data chunk).
2. **Header-chunk atomically contiguous**: guaranteed by `SendFrame`'s sendMu — the receiver attaches binary chunks
   to the request owning the most recent data header via the connection-level expect state machine.
3. **reqId**: always carried on the Go side (UUID v4); the browser side may omit it (backward compatible). The
   inbound role echoes reqId to preserve pairing.

## Relationships with other modules

| Module | Relationship |
|---|---|
| `conn.go` (shared core) | `bindConn` dispatches verbs → this file's serve*; the `connState` slots `pendingUpload`/`binCh`/`binDone` are exclusively held by the inbound role |
| `outbound.go` (outbound role) | Full-duplex concurrency on the same connection; `connState` slots are flattened and shared without interfering (fetches/expect belong to outbound, pendingUpload/binCh to inbound) |
| `file_index.go` | This file is the "wire side" of the index verbs: the protocol layer only does validation/dispatch/reply frames; real state is in `UploadSession` (bitmap, resume, reap) |
| `admin.go` | admin binary upload chunks are delivered via `binCh` to this worker (the `ch.au != nil` branch), reusing the H5 architecture |
| `forward.go` | Forwarding chunk writes to the tunnel also go through this worker's `fwdCh` branch; the forwarding handshake server side is outside this file (forward.go) |
| `peerjs_service.go` | `serveFile` is a method of `PeerJSService` — storageDir/fileIndex/conns and other state live on the service |
| `ws_session.go` / `rtc_session.go` | The `Session` interface abstraction (SendJSON/SendFrame/OnMessage/OnClose); the inbound role depends only on the interface, not on the specific channel |

## Pitfalls and design decisions

1. **H1 remote crash: out-of-bounds panic on `req.Hash[:2]`** (serveFile:36). The old implementation sliced directly; the peer sending an empty/short hash caused a panic — serveFile runs in a goroutine, so the panic kills the whole
   process (any node on the public signaling network could crash all nodes with one line of JSON). Fix: `hashutil.IsStrictSHA256` check first; illegal replies an err frame.
2. **H2 remote arbitrary file read** (serveFile:41-49 + serveCreate). The old implementation accepted any absolute
   path: a peer could `create /etc/shadow`, get the hash, then read it with `req`, and the info verb would also leak the
   path. Fix: a path hit in file_index must pass `IsPathAllowed` (judged whether it is inside the root directory after Abs + EvalSymlinks resolution); out of bounds falls back to content-addressed storage. **Defense against historical dirty data**: out-of-root paths already
   in the index (written by an old version) are also refused from being returned; there is no fallback trust.
3. **H5 head-of-line blocking**: WriteAt/Complete (fsync + full-file hashFile is a slow operation)
   previously ran synchronously on the pion message pump — the instant an 8GB upload completed, all other frames on the connection froze.
   Fix: routing decisions stay in the pump (cheap), IO is handed to a connection-level worker (this file's uploadWorker).
   Also: concurrent serveFile once each registered OnBufferedAmountLow (the replacement callback was overwritten → wait forever),
   now handled uniformly by the peerjs layer's sendMu + lowWater broadcast.
4. **M6 pendingUpload had no timeout → connection-level DoS** (serveUploadBegin:205-219). If the peer sends
   an upload header but no data chunks, the slot is occupied forever, after which all uploads on that connection return "already in
   progress" (only a reconnect recovers). Fix: `uploadState.created` + 30s expiry auto-clear.
   Note: what is cleared is the connection-level slot; the session itself is left to file_index's 10-minute reap and does not affect multiple sources.
5. **An empty file is a legal content-addressed value** (serveUploadBegin:162,177-195). sha256(empty) =
   e3b0c442...; the fetch side already supports empty-file verification; the upload side must be able to complete `size==0` (no data frames
   to send → Complete directly), and `offset` must be 0 ("offset beyond size").
6. **The upload response is `meta`, not a dedicated header**: it shares the meta frame type with file fetch (the offset field
   returns the contiguous written offset, for resume), so reusing the frame protocol needs no new verb.
7. **err frame semantics**: all failures uniformly reply `err {msg, reqId}` (echoing reqId preserves request-response
   pairing); the peer's routeResponse receives errors by reqId.
8. **Separating routing decisions from IO is a hard constraint**: whose "binary chunk it is" (fwd/upload/admin/fetch)
   must be decided in the message pump (preserving order consistency with text frames); persistence is done by the worker (order preserved by the single
   worker). Any change that moves IO back into the pump would resurrect H5.
9. **All serve* escape to goroutines**: except admin, all inbound handling `go`-escapes (conn.go
   bindConn) — slow operations (reading large files in chunks and sending) must not block the message pump. The cost is that the response frame order is no longer
   strictly guaranteed (file chunks and index responses may interleave), and the protocol tolerates this via reqId pairing.
10. **Chunk length semantics**: one upload request = one chunk (≤64KB), with `length = size -
    offset` clamped to chunkSize — if the peer declares an oversized chunk (>64KB), only 64KB is received; do the excess bytes
    remain for the next data frame? No — the pump judges fully received by `up.got >= up.size`, and over-sent bytes are counted into
    the next chunk's attribution, and the protocol routes by "the data header declares size" (conn.go's expect mechanism).
    This is a direct embodiment of the frame protocol's "header+chunk atomically contiguous" constraint (REFACTOR §4 constraint 2).

### Summary of exception paths (all failure surfaces the peer can trigger)

| Peer behavior | This node's response | Defense line |
|---|---|---|
| req with empty/short/non-hex hash | `err "invalid hash"` | H1 (IsStrictSHA256) |
| req for a nonexistent hash | `err "not found"` (CAS miss) | normal path |
| req with an out-of-bounds index path | Fall back to CAS; if that misses → `err "not found"` | H2 (IsPathAllowed) |
| create a path outside the root | `err "path outside allowed root"` | H2 |
| create a directory | `err "<path> is a directory"` | file-semantics check |
| upload size<0 / >8GB | `err "invalid upload size"` | cap (symmetric with fetch) |
| upload offset not aligned | `err "offset must be chunk-aligned"` | bitmap granularity constraint |
| upload over-send (write out of bounds) | `err "write out of range"` / `"upload write failed"` | WriteAt bounds check |
| upload header with no data | slot auto-cleared after 30s | M6 |
| upload single-stream re-entry | `err "upload already in progress"` | connection-level single slot |
| info/delete with an illegal hash | `err "invalid hash"` | IsStrictSHA256 |
| list with an oversized limit | internally clamped to 1000 | against whole-table materialization DoS |

All failures uniformly use the `err {msg, reqId}` frame, and the peer's routeResponse receives errors by reqId — the error surface
is auditable, does not panic, and does not hang (this is the security floor of the frame protocol layer, where "any node on the public signaling server can send frames").

## Tests

Tests covering the inbound role are spread across `peerjs_service_test.go` (direct serve* tests) and
`file_index_test.go` (the UploadSession layer, see file-index.md). They share `fakeSession`
(an in-memory Session: records sent frames + manually injects peer frames).

| Test | Background of discovery |
|---|---|
| `TestServeFile_InvalidHashNoPanic` (peerjs_service_test.go:130) | **H1 remote crash vulnerability**: serveFile directly did `req.Hash[:2]`; the peer sending `{"type":"req","hash":""}` or a short hash caused an out-of-bounds panic that killed the process (any node on the public signaling server could crash all nodes with one line of JSON). Fix: IsStrictSHA256 check first; asserts each bad hash replies exactly one err frame |
| `TestServeFile_IndexPathOutsideRoot` (peerjs_service_test.go:145) | **H2 arbitrary file read vulnerability**: a peer creates an arbitrary absolute path and then reads it with req. Defensive test: simulates historical dirty data (an out-of-root path already upserted into the index); serveFile must refuse to return it |
| `TestServeUploadBegin_StalePendingCleared` (peerjs_service_test.go:245) | **M6 connection-level DoS**: the peer sends an upload header but no data chunks; pendingUpload is occupied forever and all subsequent uploads on that connection return "already in progress" (only a reconnect recovers). Fix: created + 30s expiry clear; asserts the expired placeholder is cleared and a new upload starts normally |
| `TestUploadWorker_WriteThenComplete` (peerjs_service_test.go:269) | **H5 head-of-line blocking**: binary frame WriteAt/Complete moved out of the message pump into a connection-level worker. This test directly drives the worker to verify the full chunk delivery → persistence → uploaded reply-frame chain (pitfall: you must not immediately close(binDone) — select's random path choice would drop unprocessed chunks, so poll the reply frame first) |
| `TestFileIndex_UploadEmpty` (file_index_test.go:123) | **Asymmetry between the two ends**: the fetch side's hashMatchesSHA256 already supports empty files, but the upload side's old implementation rejected size<=0 outright. Fix: size==0 Completes directly (serveUploadBegin's direct path) |
| `TestFileIndex_UploadMultiSource` (file_index_test.go:242) | **Functional requirement**: multiple nodes upload different chunks of the same file in parallel — out-of-order WriteAt merges, and a full bitmap triggers completion (the source of serveUploadBegin's multi-source semantics) |
| `TestFileIndex_UploadResume` (file_index_test.go:286) | **Functional requirement**: resume from the received position after an upload interruption (the source of meta.offset's contiguous written offset) |
| `TestFileIndex_UploadPartialNotComplete` (file_index_test.go:316) | **Defensive test**: when chunks are missing, Complete does not register — the semantic basis for serveUploadBegin replying ack so the client can resume |

## File inventory

- `back/internal/transport/inbound.go` (330 lines) — this module
- `back/internal/transport/conn.go` — dispatch (bindConn), connState slots, in-pump binary routing
- `back/internal/transport/peerjs_service_test.go` — direct serve* tests + fakeSession infrastructure
- `back/internal/transport/stream_test.go` — streaming fetch tests (the outbound role, but using the same fakeSession)
- `back/internal/transport/file_index_test.go` — UploadSession layer tests
- `doc/REFACTOR.md` §3.7 (role split), §4 (frame protocol), §5 (E2E pitfalls)
- `doc/archive/TRANSPORT-REVIEW-2026-08-15.md` — all original H1/H2/H5/H6/M6/M7 findings and fix records
