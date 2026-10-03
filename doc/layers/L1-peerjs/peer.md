# Peer Module (back/peerjs/peer.go + signaller.go)

> One-line responsibility: PeerJS-compatible signaling client — registers with signaling server, sends/receives OFFER/ANSWER/CANDIDATE messages, maintains connection registry, supports active (Connect) and passive (OnConnection) WebRTC data connection.

## Responsibilities

- Register node with signaling server (specified ID or server-assigned random ID), maintain WS connection and heartbeat
- Route signaling messages: OFFER → establish answerer connection; ANSWER/CANDIDATE → deliver to existing connection by connectionId; LEAVE/EXPIRE → clean up connection
- Maintain connection registry (`conns map[string]*Connection`), responsible for connection lifecycle cleanup
- Three extension points: `Signaller` interface (swap signaling), `DataChannel` interface (swap transport), open string `MessageType` (add messages)
- **Contains no business logic**: Data plane handled by Connection + upper callbacks

## Key Mechanisms

### 1. Signaling Connection and Heartbeat (peerJSSignaller)

- WS URL: `wss://host:port/{path}peerjs?key=&id=&token=&version=1.5.4` (following peerjs-client's version parameter)
- When ID not specified, first HTTP `GET /id?ts=...&version=...` to get server-assigned ID (`retrieveID`, validate `validID`: alphanumeric start/end, middle allows `- _ space`, length 1-256)
- `heartbeatLoop` sends HEARTBEAT every `PingInterval` (default 5s) to keep alive — public cloud signaling idle timeout depends on this heartbeat
- **M7**: `conn.SetReadLimit(1 << 20)` — signaling messages (SDP/ICE text) are very small, 1MB limit covers legitimate payloads; if cloud signaling is compromised and returns oversized frames, no limit means direct OOM

### 2. Message Routing (route, peer.go:150-197)

| Message | Handling |
|---|---|
| OFFER | `handleOffer`: Establish answerer Connection and reply ANSWER |
| ANSWER/CANDIDATE | Deliver to existing connection in `conns` by `connectionId` (payloadConnectionID) |
| LEAVE | Close all connections with `PeerID == m.Src` (collect then unlock then Close, prevent deadlock) |
| EXPIRE | OFFER expired in signaling server queue (peer didn't come online in time) → close connection to let upper connectLoop reconnect |
| HEARTBEAT | Ignore (client actively pings for keep-alive) |
| ID-TAKEN/ERROR | Log (low-severity 7: previously silently ignored, same-ID dual nodes both lose contact with no trace) |

### 3. H7: Signaling Disconnection Notification and Reconnection

**Pitfall**: Previously readLoop silently exited on error, `connected=false`, but no signal was sent to upper layer — startLoop only selected on ctx/closed signals that never triggered, **node permanently deaf after a single public network WS drop until restart**.

**Fix**: `Done() <-chan struct{}` — when readLoop exits due to network error/EOF, close the `done` channel (doneOnce ensures single close). Upper layer (peerjs_service's H7 reconnection loop) relies on it to trigger full reconnection round.

**Detail**: Active `Close()` first sets `s.conn=nil` then closes conn, in readLoop defer `s.conn == conn` doesn't match → done doesn't close, finished by ctx/closed branch (avoids false reconnection loop trigger after Close).

### 4. Concurrency Model

- `p.mu`: Protects conns map and onConn/iceServers
- **writeMu (peer.go:349-351)**: Serializes `conn.WriteJSON` — gorilla/websocket doesn't allow concurrent writes, multi-goroutine (heartbeat/ICE candidates/ANSWER) concurrent sending panics (**actually triggered by 3-node mutual integration test**)
- `Send` has 15s write deadline
- **Deadlock defense (multiple places)**: `Close → forgetConnection` needs `p.mu` — all "collect first, clean up later" paths (handleOffer's old connections, handleLeave) must call `Close` after `p.mu` unlock (Go mutex is non-reentrant)

### 5. validID and ID Lifecycle

- `validID` validates server-returned ID legitimacy (must validate after retrieveID, prevent signaling server returning garbage ID)
- ID taken (ID-TAKEN) is a configuration error, reconnection won't fix — only log

## Relationships with Other Modules

```
Upper (internal/service/peerjs_service.go)
  ├─ NewPeer / NewPeerWithSignaller → Peer
  ├─ OnConnection(handler)      ← Passive connection callback
  ├─ Connect(ctx, dst, label)   → *Connection (active)
  ├─ Dial(ctx) / Done() / Close()
  └─ Send(m)                    ← Custom message type extension point
```

- `Connection` managed via `p.conns` registry, messages routed by connectionId (see connection.md)
- `Signaller` interface (signaller.go): `Dial/ID/Send/OnMessage/Done/Close`. `NewPeerWithSignaller` injects custom implementation; `OnMessage` internally injected with `p.route` by framework, implementor only needs to call injected callback when receiving message
- `Options` (message.go:83-92): Host/Port/Secure/Path/Key/ID/Token/PingInterval/ICEServers — `SetICEServers` only effective for custom signaling paths (NewPeer uses Options.ICEServers); **custom signaling must call SetICEServers, otherwise WebRTC only has LAN host candidates**

## Pitfalls and Design Decisions

| # | Pitfall | Fix |
|---|---|---|
| H7 | readLoop silently exits on error → node permanently deaf | Done() channel + upper reconnection loop |
| — | gorilla WS concurrent write panic | writeMu serialization |
| — | Duplicate OFFER old connection leak | Full Close outside lock (see connection.md) |
| — | Close and readLoop exit race triggers false reconnection | conn identity check (`s.conn == conn`) + doneOnce |
| Low-7 | ID-TAKEN silent → same-ID no-trace loss | Logging |
| M7 | Signaling oversized frame OOM | 1MB ReadLimit |
| — | Custom signaling has no ICE servers | SetICEServers mandatory note |

## Tests

- `peer_test.go` (450 lines): Tests registration/routing/connection establishment/cleanup under in-memory signaling stub (testutil_test.go), no public network dependency
- Integration tests (`back/test/integration/`, `-tags integration`): Real public signaling 0.peerjs.com + public broker, `-p 1` serial (multiple parallel sets interfere with each other); H7 reconnection and writeMu concurrency both exposed by 3-node mutual integration test

## File List

| File | Description |
|---|---|
| `peer.go` | This module (560 lines: Peer + peerJSSignaller implementation) |
| `signaller.go` | Signaller interface + MessageHandler/SignallerFactory (39 lines) |
| `message.go` | Message/message types/payload/Options (93 lines, see transport.md) |
| `peer_test.go` / `testutil_test.go` | Tests and in-memory signaling stub |
