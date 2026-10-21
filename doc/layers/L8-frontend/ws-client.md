# WS client module (front/src/platform/transport-ws/ + front/src/ws.js shim)

> Layer membership: AOP ⑧ Frontend aspect (doc/LAYERS.md §1). The frame protocol client
> between the browser and the local node `/ws/peer` session (implementation located in
> `front/src/platform/transport-ws/`, re-exported via `front/src/ws.js` shim) —— management-plane admin
> verb, binary upload, req verb file fetch all happen on this one WS connection;
> `api.js`'s `request()` all goes through it. Authoritative protocol definition is in
> REFACTOR.md §3.10/§4 and NODE-API.md §2.4; this doc covers the frontend
> implementation and pitfalls.

## Responsibilities

- **Management plane**: `admin(method, path, body)` (ws.js:218) → backend admin verb
  internally forwards to gin engine → `admin-resp` (JSON) or `admin-bin` (binary file
  stream, e.g. collection files/.torrent). Covers all HTTP semantic endpoints:
  collections/auth/BT/IPFS/tasks/file management, etc.
- **Binary upload**: `upload(file, fileName, field, path)` (ws.js:237) → admin
  declaration frame + continuous binary chunks (file chunked upload, BT torrent upload);
  backend re-wraps into multipart after receiving all.
- **Data-plane fetch**: `download(hash, offset, size)` (ws.js:308) → `req` verb, same
  frame protocol as WebRTC DataChannel (64KB blocks + data headers + done terminator).
- **Connection lifecycle**: single connection reuse, disconnect rejects all pending
  and clears, waiting for reconnect, token injection.
- **Download to disk**: `downloadToFile(hash, filename)` (ws.js:322) → Blob + `<a download>`
  simulated save.

**Why it exists**: the frame protocol (REFACTOR.md §4) only covers the file data plane
(req fetch + create/upload/list/info/delete/sync index + fwd-* forwarding), not the
management plane for collections/auth/BT/IPFS/tasks. The backend adds admin verb on
local WS sessions (`back/internal/transport/admin.go`), internally forwarding to gin
engine to reuse all HTTP controllers (zero duplicate implementation) —— the browser
completes all management operations through this channel, **no more direct HTTP fetch**
(HTTP routes kept as legacy, see router.go LEGACY comment section).

## Key mechanisms

### Connection lifecycle

**wsUrl transformation** (ws.js:34): `http://` → `ws://`, `https://` → `wss://`,
same source as api.js `getApiBase()`. `getWsBase()` (ws.js:63) reads
`localStorage['peerdrive_api_base']`, default `https://wsl-3000.moonchan.xyz`.

**Idempotent connect** (ws.js:69):

```js
function connect() {
  if (!sock) sock = new WebSocket(wsUrl(getWsBase()) + '/ws/peer')
  if (sock._wsHandlers) return      // handlers idempotently mounted
  sock._wsHandlers = true
  ...
}
```

- Existing `sock` is directly reused; handlers are marked with `sock._wsHandlers`
  for idempotent mounting (test-injected mock sockets go through the same init path).
- **Single connection reuse, no connection pool**: the browser and local node have
  only one session; all requests concurrently route via reqId.

**Disconnect handling** (ws.js:84): `onclose` → reject all pending
(`Error('ws: connection closed')`), clear pending, `binaryExpect = null`, `sock = null`;
auto-reconnects before the next request. `onerror` → actively `close()` goes through
the same cleanup path.

**Why**: when the connection drops, hanging requests will never receive a response
frame; they must be actively rejected so callers can handle them as network errors;
clearing sock ensures reconnect doesn't reuse a zombie connection.

### reqId routing (pending Map)

`nextReqId()` (ws.js:96) = `'w' + Date.now().toString(36) + '-' + seq.toString(36)`;
`pending` Map stores resolve/reject (download types also have `chunks`/`total`
collection state). Response frames echo reqId for pairing, **out-of-order safe**
(unit test covers: second request returns first).

### Text frame dispatch (handleText, ws.js:103)

After JSON.parse, dispatch by `msg.type` (parse failure/no type dropped directly):

| type | Behavior |
|---|---|
| `admin-resp` | Look up pending by reqId; `status>=400` → `reject(Error(body.error\|body.message \|\| 'HTTP '+status))`, error carries `err.status`/`err.data` (structured bodies like the 409 conflict list are available); otherwise `resolve(msg.body)` |
| `admin-bin` | Declares "next binary frame belongs to this admin download": `binaryExpect = {type:'admin', reqId, size, got:0, chunks:[]}`; size==0 immediately finish |
| `data` | Download data block header: only `p.kind==='download'` takes over; `binaryExpect = {type:'download',...}`; size==0 directly clears expectation (empty blocks rare) |
| `meta` / `done` | Only for download-type pending; `done` → delete pending, clear binaryExpect, `resolve(assemble(p))` |
| `err` | Reject by reqId (`msg.msg` or 'peer fetch failed') |

### binaryExpect single slot (ws.js:51, 176)

"Recent binary declaration header" single slot: a binary frame must belong to the
most recent admin-bin or data header.

**Why single slot (protocol correctness depends on it)**: backend `SendFrame` guarantees
data/admin-bin headers and binary blocks are **atomically continuous** (sendMu,
REFACTOR.md §4 constraint 2) —— at any given time at most one "declaration header
awaiting its blocks" window; the frontend single slot matches the backend connection-
level expect state machine semantics. Switching to multi-slot (buffering blocks by
reqId) would break the implicit "blocks belong to most recent declaration header"
ordering, and the protocol can't distinguish "whose blocks these are".

handleBinary (ws.js:176):
- No `binaryExpect` → drop (dirty block).
- Accumulate `got`; when `got >= size`:
  - admin type → `finishBinaryExpect` (assemble Uint8Array and resolve)
  - download type → merge block into `p.chunks`, **don't resolve** —— completeness is
    guaranteed by the done frame (data headers can appear multiple times; after each
    block is complete, wait for the next data header or done frame)

### Token injection (readToken, ws.js:54)

`peerdrive_auth_token` (URL fragment `#token` import, see api.js `setApiBase`) takes
priority; otherwise when `peerdrive_auth_header_enabled==='true'` (settings toggle)
use `peerdrive_auth_key`; otherwise empty string. Admin request frames carry a `token`
field; backend injects `Authorization: Bearer` when forwarding (same as HTTP behavior,
NODE-API.md §2.4).

### Admin verb frame format examples

Ordinary JSON request (backend agreement, ws.js:10-16):

```jsonc
// Browser → Backend (send, ws.js:224)
{"type":"admin","method":"GET","path":"/files?sort=time","body":null,
 "token":"<optional>","reqId":"w-m4f3a-1"}

// Backend → Browser (admin-resp, body is the controller's original JSON)
{"type":"admin-resp","status":200,"body":{"files":[...]},"reqId":"w-m4f3a-1"}

// 4xx/5xx also go through admin-resp: body is a structured error body (409 has a conflicts list)
{"type":"admin-resp","status":409,
 "body":{"error":"merge conflict","conflicts":[{"path":"a.txt","local_hash":"...","source_hash":"..."}]},
 "reqId":"w-m4f3a-1"}
```

Binary response (file stream, e.g. collection files/`.torrent`, ≤64MB `adminBinMax`):

```jsonc
{"type":"admin-bin","status":200,"size":N,"reqId":"w-m4f3a-1"}
<following N-byte binary frame (single block, backend SendFrame atomically continuous)>
```

### Binary upload frame sequence (upload/pumpBinary, ws.js:237)

The complete frame sequence of `upload(file, fileName, field='file', path='/files/upload')`:

```
Browser → Backend:
  1. admin declaration frame (text, sent synchronously):
     {"type":"admin","method":"POST","path":"/files/upload","binary":true,
      "filename":"a.bin","field":"file","size":N,"token":"<optional>","reqId":"w-m4f3a-2"}
  2. Continuous binary chunks (≤64KB slices, Streams API reads and sends per block):
     [chunk1][chunk2]...[chunkN]   ← no other frame may be inserted between declaration and blocks
Backend → Browser (after full receive, re-wraps into multipart and forwards to controller):
  3. {"type":"admin-resp","status":201,"body":{"hash":"<64hex>",...},"reqId":"w-m4f3a-2"}
```

**Why the declaration frame is sent synchronously first**: the backend message pump
synchronously occupies the `st.adminUp` slot (NODE-API.md §2.4); no other frame may
be inserted between declaration and blocks, otherwise multipart collection is broken
(e2e smoke has the same comment "declaration frame cannot await" —— response comes back
after blocks are fully received).

pumpBinary (ws.js:257):
- **Streams API first**: `file.stream().getReader()` reads block-by-block via
  `reader.read()` → `sock.send(value)`. Zero-copy for large files, backpressure-friendly,
  doesn't read entirely into memory.
- **FileReader fallback** (file objects without stream): `BIN_CHUNK` slices `file.slice(off, off+CH)`
  → `readAsArrayBuffer` → send block by block.
- Disconnection mid-way: `send` throws `Error('ws: closed during upload')` → `fail()`
  deletes pending and rejects.
- `field`/`path` can be swapped: BT torrent upload uses `field='torrent'` +
  `path='/bt/torrent'`.

### Download frame sequence (download/downloadStream/stat, ws.js)

`download(hash, offset=0, size=-1)` sends `{type:'req', hash, offset, size, reqId}`,
pending records `kind:'download'`; the server responds in 64KB blocks (same set as
WebRTC DataChannel):

```
Browser → Backend:
  {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"w-m4f3a-3"}
Backend → Browser:
  {"type":"meta","hash","total","reqId":"w-m4f3a-3"}
  {"type":"data","offset":0,"size":65536,"reqId":"w-m4f3a-3"} + 64KB binary block
  {"type":"data","offset":65536,"size":65536,"reqId":"w-m4f3a-3"} + 64KB binary block
  ... (multiple blocks)
  {"type":"done","hash","offset","size","reqId":"w-m4f3a-3"}   ← completeness signal, triggers resolve
  or {"type":"err","msg":"file not found","reqId":"w-m4f3a-3"}
```

- The frontend collects by the size declared in data headers (handleBinary), **only
  done frame resolves** as a Uint8Array.
- **`downloadStream(hash, offset, size)` (2026-08-18, optimization #1)**: returns a
  ReadableStream (pending records `kind:'stream'`) —— data blocks are `controller.enqueue`'d
  as they arrive (delivered upon each block completion, no waiting for done); done →
  `controller.close()`, err → `controller.error()` (exception if incomplete); cancel →
  clear pending + clear binaryExpect. Large files with **zero full memory**: old
  download() assembled all blocks into a single Uint8Array (8GB file OOM); the
  streaming API is the downloadToFile's disk-write channel.
- **`stat(hash)` (2026-08-18)**: sends `req` (offset=0, size=0) → the meta frame's
  `total` is the file size (size=0 semantics = only meta returned, no data sent;
  server-side convention). Used for size probing before preview.
- **`downloadToFile(hash, filename)` (2026-08-18 rewritten)**: prefers File System
  Access API (`showSaveFilePicker` + `createWritable` + `for await (downloadStream)`
  streaming write, writing to disk as it downloads); without API (non-Chromium)
  falls back to old Blob + `<a download>` (revoke after 5s). Frontend preview
  (api.js getBlobUrl) > 200MB throws `TOO_LARGE` and no longer reads fully into
  memory.

### __test test hooks (ws.js:337)

Not exported in production; used only by vitest unit tests:
- `connect` / `readToken` / `getWsBase` / `handleText` / `handleBinary` / `pending`
- `_setSock(s)`: inject mock socket (when `_wsHandlers:false`, connect idempotently
  mounts handlers)
- `_reset()`: clears sock + clears pending + clears binaryExpect

## Relationships with other modules

```
Page components (pages/*)
   │  import * as api
   ▼
api.js ──request()──▶ ws.admin()         Management plane JSON/binary
api.js ──downloadFile──▶ ws.download()   Data plane req verb
api.js ──getBlobUrl──▶ ws.download()     Preview objectURL
   │
   ▼
ws.js ──WebSocket──▶ back/internal/transport/ws_session.go (/ws/peer, WSSession)
                        └─ serveAdmin (only sessions with ID()=='local' accepted)
                              └─ construct *http.Request → gin engine ServeHTTP
                                    └─ controller (business layer unaware)
```

- **Upper layer (api.js)**: `request()` is `ws.admin()` (api.js:148-150);
  `downloadFile`, `getBlobUrl`, `downloadFileToDisk` use `ws.download`/`ws.downloadToFile`;
  `uploadFile`/`btTorrentUpload` use `ws.upload`; `downloadAnonFile`/`downloadUserFile`/
  `downloadTorrentFile` use `ws.admin('GET', ...)` (binary response → admin-bin).
- **Backend protocol side**: `admin.go` (admin verb handling + `adminUp` upload single
  slot + multipart re-wrap + `adminBinMax` 64MB limit), `ws_session.go` (WSSession,
  frame protocol matches DataChannel), `router.go` `SetAdminHandler` assembly.
  Authoritative protocol definition: REFACTOR.md §3.10/§4, NODE-API.md §2.4.
- **Non-relationship (important)**: peerjs/WebRTC connections **do not implement admin
  verb** (prevents privilege boundary exposure to unknown nodes on the public signaling
  channel; user decision); the frontend also has no peerjs/mqtt dependencies —— the
  peerjs stack only exists in the backend; old frontend P2P client code was removed
  (doc/archive/LEGACY.md section F).

## Caveats and design decisions

1. **data/admin-bin headers and binary blocks must be atomically continuous** (backend
   SendFrame guarantees) —— the frontend single-slot binaryExpect pairs with it; don't
   change to multi-slot (ws.js:23, 51).
2. **admin response status>=400 semantics**: the rejected Error carries `err.status`/
   `err.data`, consistent with api.js's old fetch version; structured error bodies like
   the 409 conflict list depend on it (ws.js:26; Explorer's merge conflict modal
   directly consumes `e.data.conflicts`).
3. **Disconnect must reject all pending**: otherwise hanging Promises never settle,
   callers can't distinguish "slow" from "dead" (ws.js:84).
4. **Management plane only goes through local WS**: peerjs/WebRTC doesn't implement admin
   verb (prevents privilege boundary leaks; backend serveAdmin rejects non-local
   connections by session ID, ws.js:30).
5. **Upload declaration frame must be sent continuously with binary blocks**: inserting
   any frame in between will scramble backend multipart collection; also **cannot await
   the declaration frame's response before sending blocks** —— the response comes back
   after blocks are fully received (ws.js:237, e2e-admin-smoke.mjs comments).
6. **Download resolve timing**: blocks fully received doesn't mean done; must wait for
   the done frame —— data headers can appear multiple times; done is the completeness
   signal (ws.js:175).
7. **Missing BIN_CHUNK (fixed, 382b74c)**: pumpBinary's FileReader fallback branch
   previously referenced an undefined `BIN_CHUNK` —— modern browsers with `.stream()`
   go through Streams API and don't trigger; encountering a file object without stream
   throws ReferenceError and pending never settles. Fix: define
   `const BIN_CHUNK = 64 * 1024` (matches backend uploadChunkSize/chunkSize, safely
   above WS read limit 3×64KB). Discovery context: code review 2026-08-18 (grep of
   all front/src found only one reference, no definition); regression test:
   ws.test.js "upload FileReader fallback" 150KB split into 3 blocks.
8. **upload() missing readyState guard (fixed, 1992406)**: admin()/download() both
   check `sock.readyState !== OPEN` first and then reject; upload() didn't have it
   originally —— in CONNECTING state (uploading immediately after page load),
   `sock.send` synchronously throws InvalidStateError; the throw in the executor rejects
   but the pending entry leaks until onclose clears it, and the error type is
   inconsistent with other paths. Fix: unified guard on all three entry points.
   Discovery context: code review 2026-08-18 (three entry point guards were uneven).
9. **Token dual source**: URL fragment token (`peerdrive_auth_token`) is always
   effective; settings-page legacy token (`peerdrive_auth_key`) requires the toggle
   `peerdrive_auth_header_enabled` —— when both coexist fragment wins (api.js
   getAuthToken has same semantics).
10. **downloadStream cancel must clean pending + binaryExpect** (2026-08-18): when
    reader.cancel() is called the server is still sending subsequent blocks —— not
    cleaning causes late frames to hang on the next request's header (single-slot
    binaryExpect occupied by the old request). Test: ws.test.js "stream cancel
    cleanup".
11. **getBlobUrl 200MB preview threshold** (2026-08-18, api.js): preview objectURL
    assembles the full content; for large files first `ws.stat` probes the size;
    > 200MB throws `err.code='TOO_LARGE'` (page falls back to a download prompt);
    concurrent preview requests for the same hash are deduplicated in-flight
    (blobUrlInflight Map, share one download). Test: ws.test.js stat size probe +
    api-side threshold.

## Tests (`cd front && npm test`, full 36 items × 4 files)

### Unit tests (front/tests/ws.test.js)

Mock socket directly injected via `ws.__test._setSock`; `feedText` manually feeds text
frames, `onmessage` feeds binary frames, verifying pure frame routing logic.
`beforeEach` calls `_reset()` + `localStorage.clear()` (residual pending will be
rejected en masse on onclose producing unhandled rejections —— isolates between tests).

| Test | Coverage |
|---|---|
| `admin requests route responses by reqId` | Two requests respond out of order and still pair correctly |
| `admin 4xx response → reject Error(err.status/err.data) (409 conflict list semantics)` | Structured error body pass-through |
| `admin request carries token (Authorization semantics)` | Token injection |
| `download: data header + binary blocks collected by binaryExpect, done frame resolves` | Multi-block collection + done triggers resolve |
| `download: err frame rejects` | err frame semantics |
| `admin-bin: binary file stream response collected as Uint8Array` | File stream response |
| `connection close → all pending rejected` | Disconnect cleanup (attach catch before onclose, avoiding unhandled rejection) |
| `upload: declaration frame + binary blocks sliced by BIN_CHUNK (FileReader fallback path)` | 150KB file split into 3 blocks (64+64+22KB), FileReader fallback branch regression (BIN_CHUNK ReferenceError fix) |
| `stat: size=0 request returns meta.total` (2026-08-18) | stat size probe semantics (preview threshold prerequisite) |
| `downloadStream: multi-block enqueue-as-arrive + done close` (2026-08-18) | Streaming download chunk delivery (zero full memory) |
| `downloadStream: err frame → controller.error` (2026-08-18) | Streaming exception path |
| `downloadStream: cancel cleans pending + binaryExpect` (2026-08-18) | After cancel, late frames don't pollute the next request |

**Discovery context** (file header comment): ws.js is the core client of the "frontend
full migration to ws/peerjs"; frame routing correctness directly determines whether pages
work —— the single-slot binaryExpect must be semantically consistent with the backend
connection-level expect. This test suite is part of the migration batch (REFACTOR.md
§3.10).

### E2E smoke (front/tests/e2e-admin-smoke.mjs, 121 lines)

Node 22 native WebSocket connects directly to `ws://localhost:3000/ws/peer` (run after
starting local service, `PEERDRIVE_STORAGE=/tmp/pd-storage PORT=3000 go run ./cmd/server/`),
going through admin verb to verify the full management plane chain: `GET /ping` → binary
upload `/files/upload` → `GET /download/:hash` (admin-bin response) → anonymous collection
POST/GET → `GET /files` → unknown route 404 pass-through. Same frame protocol as ws.js
(independent implementation, binary collected with Buffer).

### Manual verification

- Open a page in the browser, do any list/upload/download operation; DevTools Network
  filters WS frames, verify the continuity of admin declaration frames with binary
  blocks.
- Disconnect network (kill the node process) and observe all page requests report
  "ws: connection closed"; restart the node and the next request auto-reconnects
  successfully.

## File inventory

> References use function names only (line numbers drift easily, see REFACTOR.md §10 convention).

- `front/src/ws.js` —— the main subject of this doc (download/downloadStream/stat/
  downloadToFile)
- `front/tests/ws.test.js` —— frame routing + upload + streaming download unit tests
- `front/tests/e2e-admin-smoke.mjs` —— admin verb E2E smoke (requires local service)
- ~~the deleted HTTP wrapper module~~ —— was the getBlobUrl (200MB threshold + in-flight dedup) consumer; **deleted 2026-10-07** for having zero importers, so the preview path in this doc is now reached by page code calling `ws.stat`/`ws.download` directly
- Related backend (protocol peer, not this module):
  `back/internal/transport/admin.go` (admin verb server side),
  `back/internal/transport/ws_session.go` (WSSession session implementation)
