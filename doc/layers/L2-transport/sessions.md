# Session Abstraction (back/internal/transport/ws_session.go + rtc_session.go)

> Layer belonging: AOP ② frame protocol layer (see doc/LAYERS.md §1)
> Frame protocol definitions are in doc/REFACTOR.md §4; this module is the two implementations of the
> "protocol carrier channel".

**One-line responsibility**: unify two physical channels (local WebSocket / WebRTC DataChannel) into the
`Session` interface — the same frame protocol (text frame = JSON control header, binary frame = data chunk), the same
reqId state machine, so the layer above (conn.go's dispatch pump) reuses them with zero branching.

## Responsibilities

### What problem does it solve

There are two intercommunication paths between the browser and a node (REFACTOR.md §3.5):

```
browser ──WS(/ws/peer)──────→ local node: admin/metadata/small files (millisecond, no hole-punching)
browser ──WebRTC(public cloud signaling)─→ any node (including remote): large files, cross-node (hole-punch direct connection)
```

The two paths are physically completely different (gorilla/websocket's `*websocket.Conn` vs
`*peerjs.Connection`), but the frame protocol must be exactly the same — otherwise the frontend has to maintain two sets of codecs
and the backend would have to write two copies of every verb handler. The `Session` interface contains the difference in the adaptation layer:

- **WSSession** (ws_session.go): gorilla WebSocket adaptation + keepalive (read timeout / ping-pong)
- **rtcSession** (rtc_session.go): `*peerjs.Connection` adaptation (the peer node via public cloud signaling)

conn.go's `bindConn` only knows `Session`: fetchReader's frame receiving, serveFile's frame sending,
and OnClose cleanup are all transport-agnostic.

### Position in AOP ②

```
① peerjs: *peerjs.Connection (DataChannel + SendFrame atomic frames + flow control)
         ↑ adapted by rtc_session.go
② transport
    ├── ws_session.go / rtc_session.go  ← this document (Session abstraction, direction-neutral)
    ├── conn.go  bindConn (consumes only Session)
    ├── inbound.go / outbound.go (consume only Session)
    └── peerjs_service.go assembly: connectLoop → newRTCSession; BindLocal → NewWSSession
```

- Upstream: the ① peerjs module (the `peerjs.Frame`, `peerjs.DataChannel` types are borrowed by the
  interface); gorilla/websocket (the direct dependency for local WS).
- Downstream: conn.go's dispatch pump, the inbound/outbound roles, admin.go (serveAdmin distinguishes
  local sessions via `c.ID()=="local"`).

## Key mechanisms

### 1. The Session interface (ws_session.go:22-29)

```go
type Session interface {
    ID() string                          // session identifier: "local" or a remote peer id
    SendJSON(v any) error                // text frame (JSON control header)
    SendFrame(header any, body []byte) error // atomic send of "header + binary body" (REFACTOR.md §4 constraint 2)
    OnMessage(f func(peerjs.Frame))      // register the frame callback (IsText distinguishes text/binary)
    OnClose(f func())
    Close()
}
```

Design points:

- `peerjs.Frame{IsText, Data}` as the unified frame type — WSSession maps WS's
  `TextMessage/BinaryMessage` to `IsText` (ws_session.go:146), so the two implementations have
  exactly the same frame semantics.
- `SendFrame`'s atomicity is a precondition for protocol correctness: no other frame may be
  inserted between the data/admin-bin header and the data chunk (REFACTOR.md §4 constraint 2; ws-client.md's
  binaryExpect single slot depends on it).

### 2. WSSession (ws_session.go:34-149)

```
structure: id + conn + sendMu + onMessage/onClose + closeOnce
threads: readLoop (read frames → dispatch) + heartbeatLoop (30s ping)
```

- **NewWSSession** (ws_session.go:49-62):
  - `SetReadLimit(3 * 64 * 1024)`: data chunks ≤64KB + JSON control header headroom (double the margin)
  - `SetReadDeadline(nowPlus(90))` + refreshed by PongHandler: a 90s read timeout with no activity
  - starts readLoop + heartbeatLoop
- **readLoop** (ws_session.go:135-148): a `ReadMessage` loop → construct a
  `peerjs.Frame` by message type → call the registered OnMessage; on error, `defer s.Close()` (triggers OnClose cleanup).
- **heartbeatLoop** (ws_session.go:67-78): `WriteControl(PingMessage)` every 30s,
  with a 10s write timeout; a failed ping (connection already closed) exits directly, no leak. The browser auto-replies pong to a ping
  (protocol-layer behavior), and a pong refreshes the read deadline.
- **Sending** (SendJSON ws_session.go:84-89 / SendFrame ws_session.go:92-104):
  serialized by `sendMu` (gorilla does not allow concurrent writes), with a 15s write deadline; SendFrame first does WriteJSON
  for the header, then the BinaryMessage body — both inside the same lock, guaranteeing atomic contiguity.
- **Close** (ws_session.go:121-132): `closeOnce` guarantees idempotency; close the underlying connection first, then take
  the onClose callback and run it outside the lock (preventing a callback deadlock while holding the lock).

### 3. rtcSession (rtc_session.go:11-35)

A thin adaptation of `*peerjs.Connection`: `ID()` returns the cached `PeerID`; the other methods pass through
directly (SendFrame's atomicity and flow control are implemented by the peerjs module). It additionally exposes
`DataChannel() peerjs.DataChannel` (rtc_session.go:35) — used by serveFile's write-buffer flow control
(WSSession has no such capability: TCP has backpressure built in, so no water-mark control is needed; REFACTOR.md §3.5's interface
assertion semantics are exactly "only rtcSession has a DataChannel method").

**Why wrap one layer rather than let Connection implement Session directly** (rtc_session.go:7-10 comment):
`Connection.ID` is a signaling routing key (connectionId), semantically different from the session identifier (the remote peer id),
and the field names conflict; the adapter contains the difference in the service layer, and the peerjs module keeps its
transport-primitive responsibility (LAYERS.md §1: ① does not depend on internal/*).

### 4. Lifecycle cooperation

```
establish: peerjs_service.go
  connectLoop dial success / onIncomingConnection accept → newRTCSession → bindConn
  router /ws/peer upgrade → NewWSSession("local", conn) → BindLocal → bindConn
run: bindConn registers OnMessage/OnClose (conn.go:165, 294)
close: WSSession read timeout/ping failure → readLoop exits → Close → OnClose → bindConn cleanup
       peerjs connection dropped → Connection.OnClose → rtcSession's OnClose wrapper → bindConn cleanup
```

## Relationships with other modules

- **conn.go**: `bindConn` is the only consumer of Session (registers OnMessage/OnClose);
  the session identifier (`ID()`) is both the key of the `conns` map and the basis for admin permission checks
  (`c.ID()!="local"` rejects admin, admin.go:113).
- **peerjs_service.go**: `connectLoop` (peerjs_service.go:318-326) and
  `onIncomingConnection` (peerjs_service.go:352-357) create rtcSessions;
  `BindLocal` (peerjs_service.go:243-246) binds a WSSession — **both paths merely create
  a full-duplex Session and then bindConn**; connections have no notion of direction (peerjs_service.go:13-14 comment).
- **inbound/outbound**: serveFile/routeResponse interact with Session only via SendJSON/SendFrame/OnMessage,
  unaware of the transport type.
- **router (/ws/peer)**: peerjs_routes.go does the WS upgrade + Origin allowlist check and then
  `NewWSSession`; the local session id is fixed as "local".

## Pitfalls and design decisions

| # | Pitfall | Design/fix | Source |
|---|---|---|---|
| 1 | gorilla/websocket does not allow concurrent writes — heartbeat/ICE candidates/ANSWER from multiple goroutines writing concurrently would panic (triggered by the 3-node interop test) | WSSession's `sendMu` serializes all writes (including ping's WriteControl) | ws_session.go:38, 70-73, 85-103; REFACTOR.md §5 |
| 2 | No read limit: a malicious/faulty browser sending oversized frames occupies memory indefinitely | `SetReadLimit(3*64KB)` (M5) | ws_session.go:46-47, 51 |
| 3 | No keepalive: a dead browser tab → readLoop + session persist, the connection map is never cleaned, pending fetches hang for 5 minutes | a 90s read deadline + a 30s ping/pong refresh (M5) | ws_session.go:47-57, 64-78 |
| 4 | pion's Connection.ID is a signaling routing key (connectionId), conflicting semantically with "the remote peer id" | rtcSession wraps one layer; ID() caches PeerID | rtc_session.go:7-14, 17 |
| 5 | WS has no write-buffer water-mark concept (TCP has backpressure built in) | DataChannel() is exposed only on rtcSession — serveFile's flow-control assertion applies only to DataChannel; WSSession has zero flow-control code | rtc_session.go:34-35; REFACTOR.md §3.5 |
| 6 | A close callback run while holding the lock may deadlock | Close takes the callback and runs it outside the lock; closeOnce is idempotent (repeated Close does not re-trigger) | ws_session.go:121-132 |
| 7 | Registration timing race for OnMessage/OnClose (bindConn registers after construction) | registration is protected by sendMu; readLoop also holds the lock to copy the callback when taking it | ws_session.go:107-118, 142-145 |

## Tests

There are no separate ws_session_test.go / rtc_session_test.go inside the transport package — the correctness
of the session adaptation is covered by two kinds of tests:

**Unit tests**: `fakeSession` in `peerjs_service_test.go` (peerjs_service_test.go:24-77)
implements the same `Session` interface (recording sent frames in memory, manually injecting frames), driving the
conn.go pump and the full inbound/outbound chain. Background of discovery (file header comment): the H1/H6/M6 fixes needed a way to
"inject malicious frames without a network", so fakeSession is the foundation of protocol-level testing.

**Integration tests** (`back/test/integration/`, requires outernet + proxy, `-p 1` serial):

| Test | Coverage | Background of discovery |
|---|---|---|
| ws_test.go TestLocalWSSessionFetch | real WS upgrade → NewWSSession("local") → BindLocal → fetch | Architecture decision (§3.5): local goes over WS with no hole-punching |
| ws_test.go TestLocalWSSession_FetchFromPeerReuse | after "local" is registered, FetchFromPeer reuses with zero branching | BindLocal design verification |
| ws_verbs_test.go TestFrameVerbs_* | the full create/upload/list/info/download/sync chain on a WS session | Functional acceptance |
| interop_test.go Test* | rtcSession (real DataChannel) two/three/four-node interop | Functional acceptance + flow-control deadlock regression |
| live_test.go TestLive* | live signaling + the full WS/WebRTC chain | Live verification |

> Note: gorilla's read-timeout/keepalive behavior (pitfalls #2/#3) has no dedicated unit test; it is a
> "protocol-layer behavior" dependency (the browser auto-replies pong). Adding a dedicated disconnect test would require mocking
> websocket.Conn; currently the coverage comes from the liveness judgment in the integration
> ws_test.go.

## File inventory

| File | Description |
|---|---|
| `ws_session.go` (149 lines) | Session interface definition + WSSession (gorilla adaptation: read loop / keepalive / write lock) |
| `rtc_session.go` (35 lines) | rtcSession (*peerjs.Connection adaptation + the DataChannel flow-control entry) |
| Related consumers: `conn.go` (bindConn), `peerjs_service.go` (assembly/binding), `inbound.go`/`outbound.go` (role implementations), `back/internal/router/peerjs_routes.go` (/ws/peer upgrade) | See the corresponding module documents |
| Tests: `peerjs_service_test.go` (fakeSession), `back/test/integration/ws_test.go` + `ws_verbs_test.go` | Session behavior verification (see the table above) |
