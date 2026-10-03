# Transport Abstraction and Message Types (back/peerjs/transport.go + message.go)

> One-line responsibility: data-plane transport abstraction (`DataChannel` interface + `Frame` frame type + pion adaptation) and the signaling message protocol (`Message` + the various payloads + `Options` configuration).

## Responsibilities

- **transport.go**: defines `Frame` (text/binary frames) and the `DataChannel` interface — Connection only depends on this interface, so the transport can later be swapped for WebSocket / TCP direct connections, etc.; provides `pionChannel` to adapt pion/webrtc.DataChannel to the interface
- **message.go**: defines the signaling message structure (compatible with the peerjs-server protocol), the message type enum, connection types, the various payload structures, and the client configuration `Options`

## Key Mechanisms

### 1. Frame (transport.go:5-10)

```go
type Frame struct {
    IsText bool
    Data   []byte
}
```

Library-defined type, independent of the transport implementation. `IsText=true` is a text frame (control header/JSON), false is a binary frame (data block). In the pion `OnMessage` callback, `m.IsString` maps directly (transport.go:50-53).

### 2. DataChannel interface (transport.go:14-26)

```go
type DataChannel interface {
    SendText(string) error
    Send([]byte) error
    OnOpen(func())
    OnMessage(func(Frame))
    OnClose(func())
    Open() bool
    BufferedAmount() uint64
    SetBufferedAmountLowThreshold(uint64)
    OnBufferedAmountLow(func())
    Close()
}
```

The interface covers all data-plane operations needed by the upper layer, **including the flow-control trio** (BufferedAmount / SetBufferedAmountLowThreshold / OnBufferedAmountLow) — the water-level flow control of the upper transport package (sessions.md) depends on them. When swapping transports (e.g. TCP direct) you just implement this interface, with zero changes to the Connection layer.

`pionChannel` is a thin adapter: field forwarding + converting `webrtc.DataChannelMessage` into a `Frame` inside `OnMessage`.

### 3. Signaling message protocol (message.go)

```go
type Message struct {
    Type    MessageType     `json:"type"`               // open string type
    Src     string          `json:"src,omitempty"`      // server overwrites it with the client id
    Dst     string          `json:"dst,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`  // the type determines the structure
}
```

Message types are consistent with the peerjs-server enum: `OPEN/LEAVE/CANDIDATE/OFFER/ANSWER/EXPIRE/HEARTBEAT/ID-TAKEN/ERROR`. Connection types `ConnData="data"` / `ConnMedia="media"`.

Three payload structures (SDP uses `*webrtc.SessionDescription`, interoperating directly with pion):

| Payload | Key fields |
|---|---|
| `OfferPayload` | sdp / type / **connectionId** (defined by the offerer, shared by both sides) / label / reliable / serialization="raw" / metadata |
| `AnswerPayload` | sdp / type / connectionId |
| `CandidatePayload` | candidate (`webrtc.ICECandidateInit`) / type / connectionId |

### 4. Options (message.go:83-92)

| Field | Default | Description |
|---|---|---|
| Host | 0.peerjs.com | Signaling server |
| Port | 443 | — |
| Secure | — | wss/https |
| Path | "/" | Path prefix for a self-hosted server |
| Key | "peerjs" | API key |
| ID | — | Node ID; if empty, assigned by the server |
| Token | randomly generated | Auth token |
| PingInterval | 5s | Heartbeat interval |
| ICEServers | — | WebRTC ICE/TURN servers |

Constraint: adding a configuration item must stay backward compatible (defaults do not change existing behavior).

## Relationships with other modules

- `Connection.Send/SendText/SendFrame` all go through the `DataChannel` interface (connection.md)
- `Peer` uses `Message`/the various payloads to communicate with the signaling server (peer.md); `Options` is consumed by `NewPeer`/`normalizeOptions`
- The upper layer (internal/transport) obtains the interface via `Connection.DataChannel()` for flow control — **WebRTC-specific water-level flow control is exposed only here** (WSSession relies on TCP's built-in backpressure, see sessions.md)

## Pitfalls and design decisions

| Pitfall | Description |
|---|---|
| Text/binary frame distinction is the protocol foundation | SendText=PPID 51 vs Send=PPID 53; the upper-layer "control header vs data block" state machine depends on it |
| Replacement-style flow-control callback | `OnBufferedAmountLow` can only be registered once (detailed in connection.md) |
| Interface breadth | The DataChannel interface deliberately includes flow-control APIs — when swapping transports (e.g. TCP) you can return 0/empty callbacks without implementing them, but the primitive boundary stays clear |
| Protocol compatibility | Message/payload field names align with peerjs-server (camelCase JSON), so a self-hosted signalserver can interoperate |

## Tests

- `flowcontrol_test.go`: SendFrame flow-control behavior (low-water wait/timeout/close paths)
- `peer_test.go`: verifies message construction/routing through an in-memory signaling stub (including payloadConnectionID extraction)
- No standalone test file — this module is the type layer of the tested subject, covered via the Connection/Peer tests

## File inventory

| File | Description |
|---|---|
| `transport.go` | Frame + DataChannel interface + pionChannel (57 lines) |
| `message.go` | Message/enum/payload/Options + package doc comment (93 lines) |
