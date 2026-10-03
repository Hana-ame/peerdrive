# go-peerjs

Go implementation of [PeerJS](https://peerjs.com) compatible signaling client + WebRTC DataChannel transport layer.
Based on `pion/webrtc/v4`, interoperable with browser-side peerjs (`serialization: "raw"`) and Go nodes.

**Module positioning: transport primitives (signaling + data plane). Business frame protocol (verb) is defined by the upper layer** ——
Same design philosophy as [hana-link](https://github.com/Hana-ame/hana-link) (PeerJS+MQTT transport layer, format-agnostic).

```
Browser (peerjs) ──Public cloud signaling (0.peerjs.com)──┐
                                          ├── WebRTC DataChannel direct connection
Go node (this module) ──Public cloud signaling────────────┘
```

## Installation

```bash
go get github.com/Hana-ame/go-peerjs
```

## Quick Start

```go
import (
    "context"
    peerjs "github.com/Hana-ame/go-peerjs"
)

// Node A: passive service (provides files)
opts := peerjs.DefaultOptions()
opts.ID = "pd-node-a"
opts.ICEServers = []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}

p := peerjs.NewPeer("pd-node-a", opts)
if err := p.Dial(ctx); err != nil { log.Fatal(err) }

// Passive connection: receive and send data after the peer connects
p.OnConnection(func(c *peerjs.Connection) {
    c.OnMessage(func(f peerjs.Frame) {
        if f.IsText { /* JSON control frame */ } else { /* binary data chunk */ }
    })
})

// Node B: actively connect to A and send data
p2 := peerjs.NewPeer("pd-node-b", peerjs.DefaultOptions())
p2.Dial(ctx)
conn, err := p2.Connect(ctx, "pd-node-a", "peerdrive")
conn.OnOpen(func(c *peerjs.Connection) {
    c.SendJSON(map[string]any{"type": "hello"})          // Text frame
    c.SendFrame(map[string]any{"type": "data", "size": 4}, []byte("1234")) // Atomic "header+body" frame
})
```

## Function Set

### Types

| Type | Description |
|---|---|
| `Options` | Configuration: `Host/Port/Secure/Path/Key` (signaling), `ID/Token` (identity), `PingInterval`, `ICEServers` (STUN/TURN) |
| `MessageType` | Signaling message types, open string: `OPEN/OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE/HEARTBEAT/...`, custom types use literals directly |
| `Message` | Signaling message `{Type, Src, Dst, Payload}` |
| `Frame` | Data frame `{IsText, Data}`: `IsText=true` text frame (JSON control header), `false` binary chunk |
| `Offer/Answer/CandidatePayload` | OFFER/ANSWER/CANDIDATE payloads (field-aligned with peerjs protocol) |

### Peer (Signaling Client)

| Method | Description |
|---|---|
| `NewPeer(id string, opts Options) *Peer` | Create; if `id` is empty, the server assigns a random ID |
| `NewPeerWithSignaller(s Signaller) *Peer` | **Extension point**: inject a custom signaling implementation |
| `p.Dial(ctx) error` | Register and connect to the signaling server |
| `p.OnConnection(fn)` | Passive connection callback (peer initiates OFFER) |
| `p.Connect(ctx, dst, label) (*Connection, error)` | Actively initiate connection (offerer) |
| `p.Send(m Message) error` | Send custom signaling messages (extension point) |
| `p.ID()` / `p.Connected()` | Node information queries |
| `p.Close()` | Close signaling + all connections |

### Connection (Data Connection)

| Method | Description |
|---|---|
| `c.OnOpen(fn)` / `c.OnClose(fn)` / `c.Done()` | Lifecycle events |
| `c.OnMessage(fn(Frame))` | Data callback (text/binary frames) |
| `c.Send(data []byte)` | Binary frame (data chunk) |
| `c.SendText(s)` / `c.SendJSON(v)` | Text frame (JSON control header) |
| `c.SendFrame(header, body)` | **Atomic "header+body" frame** (concurrency-safe + built-in write buffer flow control, see protocol constraints) |
| `c.DataChannel() DataChannel` | Underlying channel (advanced usage: close, etc.) |
| `c.Open()` / `c.Close()` | Status and close |

| Layer | Responsibility | Extension Point |
|---|---|---|
| `Signaller` | Signaling channel: register nodes, forward signaling messages | Swap signaling (MQTT rooms, self-hosted server): implement `Dial/ID/Send/Close` |
| `Peer` | Node role: route OFFER/ANSWER/CANDIDATE, connection registry (one-to-many), lifecycle | Reuse; `NewPeer` / `NewPeerWithSignaller` |
| `Connection` | A single data connection: SDP exchange, ICE forwarding, frame send/flow control, events | Swap transport: implement `DataChannel` interface (WS/TCP direct) |

Layering rationale: each can be independently replaced/tested (fake signaller for routing, fake dc for frame protocol);
Peer does not depend on a specific signaling protocol, Connection does not depend on a specific transport.

Extension: `MessageType` / `Frame` are open types — custom verb/message types require no library changes.

> **`MessageHandler` / `Signaller.OnMessage` are Deprecated**: message handling is fully
> taken over by internal `Peer` routing (OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE are all handled automatically),
> users do not need to touch callbacks. When implementing a custom `Signaller`, you only need to implement
> `Dial/ID/Send/Close`; after receiving a signaling message, pass it to the callback injected by the framework
> (see the approach in `peerJSSignaller.readLoop`).

## Protocol and Constraints (Do Not Break)

### Signaling (PeerJS Public Cloud)

- Registration: if `ID` is specified, connect directly to `wss://host:port/peerjs?key=&id=&token=`; if not specified, first `GET /id` to obtain a random ID
- Messages are forwarded by the server according to `dst`, `src` is overwritten by the server
- **connectionId is defined by the offerer, the answerer must reuse it** (previously, the answerer generating a new ID caused ANSWER routing failures and ICE stuck in checking)
- **ICE candidates must be manually forwarded via signaling** (`OnICECandidate` → `CANDIDATE` message), pion does not send them automatically
- Heartbeat: the client sends `HEARTBEAT` every `PingInterval` for keepalive

### Data Plane (DataChannel)

- **JSON control headers must be text frames, data chunks must be binary frames** (`SendText` vs `Send`) — sending them reversed causes the peer to swallow the control header as a data chunk
- **`SendFrame` atomic send + built-in flow control**: the receiver routes by "data header → immediately following binary chunk" state machine; interleaving headers and bodies will misroute requests;
  the sender serializes via sendMu + bufferedAmount low-water waiting (register once when callback is attached, lowWater broadcast,
  immediately exit waiting when connection closes). **Callers should not register `OnBufferedAmountLow` on their own** (pion replacement-style callbacks, concurrent registration will overwrite each other → deadlock)
- The receiver has only one "expecting data chunk" state per connection at a time (guaranteed by sender atomic frames)

### Business Frame Protocol (Defined by Upper Layer, Library Does Not Manage)

```jsonc
// Request (any peer); reqId recommended UUID v4
{"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<uuid>"}
// Response (echoes reqId)
{"type":"meta","hash","total","reqId"}
{"type":"data","hash","offset","size","reqId"}   // Followed by size bytes of binary
{"type":"done","hash","offset","size","reqId"}
{"type":"err","msg","reqId"}
```

## Reference Implementation

Peerdrive backend `internal/service/peerjs_service.go` is a complete business usage example of this module:
file service (serveFile) + active pull (requestFile, reqId routing state machine) + MQTT shard room discovery
(`internal/service/mqtt_discovery.go`, topic `peerdrive/v1/{collectionHash}/nodes`).
