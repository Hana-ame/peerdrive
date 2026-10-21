# Admin aspect (AOP ③) — transport/admin.go + router assembly

> One-line responsibility: the browser manages this node by sending admin/admin-bin frames over the **local WS session** (/ws/peer) —— internally forwarding to the gin engine, reusing all HTTP controllers with zero duplicate implementation; WebRTC connections do not implement the admin verbs, preventing the privilege surface from being exposed.

## Responsibilities

- Defines the admin frame protocol: `admin` (request declaration, JSON body or binary upload), `admin-resp` (JSON response), `admin-bin` (binary response header), `err` (failure)
- Translates an admin frame into an internal `*http.Request` → injects it into the gin engine's `ServeHTTP` (httptest recorder) → restores the HTTP response into frames
- Binary upload: the declaration frame + subsequent binary frames are collected into a temp file → forwarded as multipart/form-data (the controller's `FormFile` read is unaware of this)
- Binary response (file stream): the `admin-bin` header + a single binary frame
- Authentication pass-through: the frontend admin frame carries a token → on forwarding it injects `Authorization: Bearer <token>` → the gin `AuthRequired` middleware behaves exactly like over HTTP

## Key mechanisms

### 1. The frame protocol (comment at admin.go:14-27 + structs)

```
browser → local session (request):
  {"type":"admin","method":"GET|POST|...","path":"/files/upload?x=1","body":{...},"token":"...","reqId":"..."}
  binary upload declaration: {"type":"admin","method":"POST","path":"/files/upload","binary":true,
                    "filename":"a.bin","field":"file","size":12345,"reqId":"..."}
  after the declaration: subsequent binary frames are the request body (once collected → forward as multipart)

local session → browser (response):
  JSON response   → {"type":"admin-resp","status":200,"body":<original JSON>,"reqId"}
  text response   → {"type":"admin-resp","status":200,"body":"pong","reqId"}
  binary stream   → {"type":"admin-bin","status":200,"size":N,"reqId"} + immediately followed by one binary frame
  failure       → {"type":"err","msg":"...","reqId"}
```

### 2. Session isolation (serveAdmin, admin.go:112-117)

```go
if c.ID() != "local" {  // WebRTC/PeerJS connections are always rejected
    SendJSON(err: "admin verb is only allowed on the local session")
}
```

**Why**: a WebRTC connection may come from any node on the public signaling —— if the admin verbs were implemented there too, this node's admin port (auth tokens, file read/write, collection mutation) would be opened to unknown peers. The admin plane is fixed to the local WS session (`id="local"`, Origin allowlist check, see `registerPeerJSRoutes` in peerjs_routes.go).

### 3. Internal forwarding (dispatchAdmin, admin.go:284-317)

```
admin frame → buildAdminRequest(method, path, body, contentType, token)
        → s.adminHandler(req)   // the gin engine wrapper injected by router.go:357
        → response classification:
             json.Valid(respBody)           → admin-resp (body deserialized)
             status>=400 / text/*           → admin-resp (string body)
             the rest (file stream)         → admin-bin header + SendFrame binary body
```

**Response classification pitfall (admin.go:290-292, background of discovery: E2E smoke /ping returned a Buffer)**: the first version judged by whether respBody started with `{` —— a `text/plain` response (such as /ping's "pong") was misclassified as binary → the frontend received a Uint8Array instead of a string, and admin-bin occupied the binaryExpect slot. Fix: `json.Valid` first, then classify by status/content-type for non-JSON.

**Binary cap (admin.go:43-45)**: `adminBinMax = 64MB` —— internal forwarding already keeps the whole response resident in memory (the httptest recorder), so large files should be fetched in chunks via the req verb (the frontend ws.js download path). A response over the cap returns 413 suggesting the req verb.

### 4. Binary upload (admin.go:130-172, 186-262)

- The declaration frame synchronously occupies the slot (`st.adminUp`, **it must be set synchronously inside the message pump** —— otherwise if the pump has already routed later binary frames while adminUp is still empty → data chunks are lost. Background of discovery: the first version made case "admin" fully async, upload chunks arrived before adminUp, uploads never completed collecting, admin.go:109-111)
- Chunk writing goes through a worker goroutine (`adminUploadChunk`, moving IO out of the message pump, consistent with H5's fix for fileIndex uploads); the last chunk triggers `serveAdminUploadComplete`
- **Upload forwarding path pitfall (admin.go:158-167)**: the declaration frame's path is the forwarding target —— a BT torrent upload passes `path=/bt/torrent` (field=torrent); the old implementation hardcoded /files/upload, sending torrent uploads to the wrong endpoint (background of discovery: verifying the admin frame against the backend forwarding path while migrating the frontend btTorrentUpload)
- **Seek(0) pitfall (admin.go:227-228)**: au.f's write offset is already at the file end, so it must `Seek(0)` to read from the start, otherwise io.Copy reads 0 bytes (background of discovery: the admin upload test got 0 bytes)
- Timeout protection (admin.go:47-49): `adminUploadTimeout = 30s` —— a browser that declares and then never sends chunks would occupy the slot forever (the same fix as M6's for the upload verb)
- **Slot replacement pitfall (admin.go:140-153, background of discovery: code review 2026-08-18)**: a repeated upload declaration replaces the old slot (cleaning the old temp file with `os.Remove`), and **it must return an err frame for the old reqId** —— otherwise the browser Promise of the old upload hangs forever (the pending entry is only cleared when the connection drops), and late chunks of the old upload would pollute the new au's got count, causing the new upload to be wrongly judged as over-size and aborted with an err pointing at the new reqId (misleading to debug)
- The temp file is always cleaned: `defer os.Remove(au.path)` (covering both the normal and exceptional paths); the write-failure/abort paths clean up too (all three exits of adminUploadChunk unified)

### 5. Assembly (router.go:350-362)

```go
peerjsService.SetAdminHandler(func(req *http.Request) (int, []byte, string, error) {
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)          // reuses the whole gin engine (all middleware/controllers)
    return rec.Code, rec.Body.Bytes(), rec.Header().Get("Content-Type"), nil
})
```

- `SetAdminHandler` is locked (adminMu): set during assembly, read-only afterwards (serveAdmin is called concurrently)
- When adminHandler is nil it returns "admin handler not configured" (defending against use before assembly)

## Relationships with other modules

```
frontend ws.js admin()/upload()
  └─ admin frame → WSSession(id="local") → transport dispatch (bindConn case "admin")
       └─ serveAdmin → buildAdminRequest → gin engine (router.go)
            └─ all HTTP controllers (AOP ④): files/collections/shares/tasks/BT/IPFS
                 └─ service → repository / source / downloader
```

- **Mutually exclusive and independent** from the old upload verb (file index/download directory sessions): admin uploads go through storageDir content-addressed storage (the /files/upload semantics)
- The frontend-side protocol client is in `doc/layers/L8-frontend/ws-client.md` (including frame format examples)
- The global frame protocol definition is per `doc/REFACTOR.md` §3.10 / §4

## Pitfalls and design decisions

| # | Pitfall | Fix |
|---|---|---|
| — | Exposing the admin plane to WebRTC = the whole privilege surface open | Only `c.ID()=="local"` sessions may send admin |
| — | The first version was fully async for admin → upload chunks arrived before adminUp | The declaration frame occupies the slot synchronously (inside the pump) |
| — | Response judged by a leading `{` → /ping "pong" misclassified as binary | json.Valid + content-type three-level classification |
| — | multipart forwarding did not Seek(0) → 0-byte upload | Seek(0) to read from the start |
| — | BT torrent upload hardcoded to /files/upload | The declaration frame's path is the forwarding target |
| — | Declaring then never sending chunks → occupying the slot forever | A 30s timeout + slot replacement cleanup |
| — | Slot replacement silently cleared the old file → the old reqId Promise hangs forever + late chunks pollute the new upload's count | Return an err frame for the old reqId on replacement (2026-08-18) |
| — | Large file responses resident in memory | adminBinMax 64MB + 413 suggesting the req verb |

## Tests (10 unit tests, the L3 section of `scripts/test-layers.sh`)

- `admin_test.go` (`back/internal/transport/`): unit tests for the admin frame protocol —— JSON response classification,
  binary upload collection/write-failure cleanup/abort cleanup/**slot replacement notifying the old reqId**, token injected into Authorization,
  non-local session rejection, text response classification
  (`go test -tags nosqlite ./internal/transport/ -count=1 -run "^TestAdmin"`)
- The backgrounds of discovery are all annotated in the test doc comments (a hard requirement of the global AGENTS.md)

## File inventory

| File | Description |
|---|---|
| `back/internal/transport/admin.go` | This module (~325 lines: protocol structs + serveAdmin + upload collection + forwarding + response classification) |
| `back/internal/transport/admin_test.go` | Unit tests |
| `back/internal/router/router.go:350-362` | The SetAdminHandler assembly (httptest recorder wrapping the gin engine) |
| `back/internal/router/peerjs_routes.go` | The /ws/peer local session route (Origin allowlist) |
| `front/src/ws.js` | The frontend admin()/upload() client (see L8-frontend/ws-client.md) |
