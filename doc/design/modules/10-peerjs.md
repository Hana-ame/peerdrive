# Module 10: peerjs PeerJS Protocol Library

- **Code location**: `back/peerjs` (independent go.mod, module path `github.com/Hana-ame/go-peerjs`, see `back/peerjs/go.mod:1`; main repository requires it at `back/go.mod:7` and replaces with `replace github.com/Hana-ame/go-peerjs => ./peerjs` pointing to local directory at `back/go.mod:160`)
- **One-line function**: PeerJS compatible signaling client + WebRTC DataChannel transport primitives (connection / messaging / signaling / flow control), pure in-memory connection state, does not persist any state.
- **Dependencies**: `gorilla/websocket v1.5.3` (signaling WS client, `back/peerjs/go.mod:6`), `pion/webrtc/v4 v4.1.2` (PeerConnection / DataChannel / ICE, `back/peerjs/go.mod:7`); tests use `stretchr/testify` (`back/peerjs/go.mod:8`); standard library `crypto/rand` (token/connectionId generation, `back/peerjs/peer.go:6,319-329`), `net/http` (fetch random ID, `back/peerjs/peer.go:11,543-578`), `sync`/`time`. Does not depend on any upper-level framework (no gin / sqlite).
- **Depended upon by**: Main consumer is `back/internal/transport/peerjs_service.go` (`PeerJSService` holds `*peerjs.Peer`, `back/internal/transport/peerjs_service.go:26,38-41`); `back/internal/transport/rtc_session.go:4,11-39` adapts `*peerjs.Connection` to unified `Session`; `back/internal/transport/ws_session.go:9,15-29`'s `Session` interface signature directly uses this library's `peerjs.Frame` type (WS sessions reuse same frame protocol); `back/internal/transport/conn.go:36` frame protocol core references; independent consumer `back/cmd/media-node/main.go:46` (media node), `back/cmd/echclient/main.go:15` (verification client); test references in `back/test/integration/{live,selfhosted}_test.go`, `back/internal/source/peer_test.go`, `back/internal/transport/{conn,psk,peerjs_service,forward,stream}_test.go`. Also has independent repo mirror `github.com/Hana-ame/go-peerjs` (tag=v0.1.0 synced, see `AGENTS.md:24-27`).

## 1. Logic

**Module positioning**: Transport primitives (signaling + data plane), business frame protocol (verb) defined by upper layer — consistent with "format-agnostic" principle (`back/peerjs/message.go:1-4`, `back/peerjs/README.md:6-7`).

**Three-layer structure + one transport abstraction** (layering rationale: each can be independently replaced/tested, see `back/peerjs/README.md:91-98`):

| Layer | Responsibility | Code |
|---|---|---|
| `Signaller` | Signaling channel abstraction: register node, send/receive signaling messages; current implementation is PeerJS protocol (`peerJSSignaller`) | `back/peerjs/signaller.go:12-30`, `back/peerjs/peer.go:352-369` |
| `Peer` | Node role: signaling routing (OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE etc. dispatch), connection registry (one-to-many), lifecycle; proactive initiation (`Connect`) and passive reception (`OnConnection`) | `back/peerjs/peer.go:35-44,168-215` |
| `Connection` | One WebRTC DataConnection: SDP exchange, ICE candidate forwarding, frame sending (text/binary/atomic header+body), built-in write buffer flow control, open/message/close events | `back/peerjs/connection.go:24-57` |
| `DataChannel` interface | Data plane transport abstraction, `Connection` only depends on this interface (not bound to pion concrete type), current implementation is `pionChannel` (pion/webrtc DataChannel adapter) | `back/peerjs/transport.go:15-26,29-56` |

**Signaling protocol (messages and routing)**:

- Message types consistent with peerjs-server enum: `OPEN/LEAVE/CANDIDATE/OFFER/ANSWER/EXPIRE/HEARTBEAT/ID-TAKEN/ERROR` (`back/peerjs/message.go:18-28`); `Message{Type,Src,Dst,Payload}`, Payload is arbitrary JSON (`back/peerjs/message.go:38-43`).
- Connection payloads: `OfferPayload` (SDP + type + connectionId + label + reliable + serialization="raw", `back/peerjs/connection.go:339-346`), `AnswerPayload`, `CandidatePayload` (`back/peerjs/message.go:57-80`).
- Registration flow: When ID specified, directly connect WS; when not specified, first `GET /id?ts=…&version=…` to get server-assigned random ID (validated by `validID` before use), then establish WS (`back/peerjs/peer.go:396-408,543-578`).
- WS address format: `wss://host:port/peerjs?key=&id=&token=&version=1.5.4` (`back/peerjs/peer.go:410-427`; version constant see `peer.go:22`); `src` overridden by server (`back/peerjs/peer.go:502` comments).
- Keepalive: Client sends `HEARTBEAT` every `PingInterval` (default 5s); receiving server `HEARTBEAT` does not respond, no side effects (`back/peerjs/peer.go:483-500,173-175`).
- Routing dispatch: `ANSWER/CANDIDATE` finds existing connection by `connectionId` in payload then passes to `conn.handleMessage`; `OFFER` creates new connection (answerer); `EXPIRE` closes corresponding connection (let upper layer reconnect); `LEAVE` closes all connections for that remote; `ERROR/ID-TAKEN` logs (`back/peerjs/peer.go:168-215`).

**Two connection establishment paths** (symmetric, no direction distinction, `back/internal/transport/peerjs_service.go:13-14`):

- Offerer: `Peer.Connect(ctx, dst, label)` → `newConnection` creates PC, `CreateDataChannel(label, Ordered=true)`, `makeOffer` sends `OFFER` (`back/peerjs/peer.go:136-141`, `back/peerjs/connection.go:225-286,330-348`).
- Answerer: After receiving `OFFER`, `handleOffer` uses **offerer's connectionId** to create connection, `SetRemoteDescription`, returns `ANSWER`: `back/peerjs/peer.go:217-254`, `back/peerjs/connection.go:350-364`.
- ICE: Local candidates `OnICECandidate` → manually wrap `CANDIDATE` and forward via signaling (pion does not auto-send, `back/peerjs/connection.go:252-265`); remote candidates injected via `conn.handleMessage`'s `AddICECandidate` (`back/peerjs/connection.go:203-218`).

**DataChannel frame protocol** (`back/peerjs/connection.go:345,349-364`):

- `SendFrame(header, body)`: Atomic send — text header followed immediately by binary body (if any). `serialization="raw"` tells peerjs to treat body as raw bytes.
- `SendText(text)`: Text-only frame (no binary body).
- `SendBinary(data)`: Binary-only frame (no text header — not recommended for structured data).
- Write buffer: DataChannel has built-in send buffer; `SendFrame` blocks when buffer is full (backpressure).
- Events: `OnOpen` (connection ready), `OnMessage` (received frame), `OnClose` (connection closed).

## 2. How It Stores

**No persistence whatsoever.** This is a pure in-memory transport library:

| Aspect | Details |
|--------|---------|
| Storage | None — no files, no database, no network writes |
| Connection state | In-memory (`Peer.connections` map, `Connection` objects) |
| Signaling state | In-memory (WS connection, registered ID) |
| Message queue | In-memory (per-connection send buffer) |
| ICE candidates | In-memory (transient, not persisted) |
| SDP | In-memory (transient, not persisted) |

**Memory structure**:
- `Peer`: Holds signaling connection, registered ID, connection registry, event handlers
- `Connection`: Holds DataChannel, SDP, ICE state, frame send buffer, event handlers
- `Signaller`: Holds WS connection, server URL, auth token

## 3. When It Stores

**Never.** All operations are transient:

- Signaling messages are sent/received over WebSocket (not stored)
- SDP offers/answers are exchanged and discarded
- ICE candidates are forwarded and discarded
- Data frames are sent/received and not persisted
- Heartbeats are sent periodically with no storage

## 4. What It Stores

**Nothing persistent.** In-memory only:

- Peer identity (ID, token)
- Active connections (map of connectionId → Connection)
- Event handler registrations (callbacks for connection events)
- Send buffers (per-connection, bounded by DataChannel buffer size)
- ICE candidate lists (transient, until connection established)

## 5. Boundaries and Pitfalls

- **No persistence**: This library does NOT persist any state. If the process restarts, all connections must be re-established. There is no session resumption.
- **Pure memory transport**: All state is in memory. This is intentional — transport libraries should not have persistent side effects.
- **PeerJS protocol compatibility**: The signaling protocol is a subset of peerjs-server protocol. Not all peerjs-server features are supported (e.g. custom messages beyond the defined set).
- **serialization="raw"**: Binary data is sent with `serialization="raw"` to ensure raw byte transmission without JSON encoding. This is critical for binary frame protocol compatibility.
- **Heartbeat is fire-and-forget**: Client sends heartbeats but does not expect responses. Server heartbeat responses are ignored. Connection health is determined by WS connection state, not heartbeats.
- **No built-in reconnection**: The library does not auto-reconnect on WS disconnect. Upper layers (PeerJSService) handle reconnection logic.
- **Connection ID is server-assigned or self-specified**: When no ID is specified, the server assigns a random ID via `GET /id`. Self-specified IDs must be valid (alphanumeric + dashes, max 256 chars).
- **ICE trickle is manual**: ICE candidates are manually wrapped in `CANDIDATE` messages and forwarded via signaling. Pion does not auto-send ICE candidates through the signaling channel.
- **DataChannel flow control**: The built-in send buffer provides backpressure. When the buffer is full, `SendFrame` blocks. This is intentional for flow control but can cause head-of-line blocking for multiple frames.

## 6. External Connections

- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md): This library provides the signaling client and DataChannel transport primitives used by the transport module. The transport module defines the frame protocol (verb) on top of this library's transport primitives.
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md): This library connects to signaling server via WS (`/peerjs?key=&id=&token=&version=`) as signaling client; OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE/HEARTBEAT flow bidirectionally; signaling only forwards SDP/ICE, does not touch data plane.
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Browser-side peerjs connects to same signaling with same protocol, is the peer in `OFFER → ANSWER → CANDIDATE` negotiation flow (browser client implementation); this library must interop with its `serialization: "raw"` (`back/peerjs/connection.go:345`).
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md): `back/cmd/media-node` (`back/cmd/media-node/main.go:46`) uses this library to register signaling and carry browser media DataChannel; frame protocol (`url/meta/binary chunks/done/err` + keepalive) is same family but independent from peerdrive main frame protocol (`back/cmd/media-node/main.go:20-28`).

> Note: Frontend↔backend local WS sessions ([01-frontend-backend.md](../connections/01-frontend-backend.md)) are **not directly related** to this module — `BindLocal` uses local `WSSession` (`back/internal/transport/ws_session.go:31-34`), does not go through this library's signaling, only reuses the same `Session` interface and frame protocol.
