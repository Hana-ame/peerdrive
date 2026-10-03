# ① Signaling/Transport Primitives Layer — back/peerjs/ (Module Overview)

> One-line: PeerJS signaling + WebRTC DataChannel transport primitives, **zero business knowledge** — business frame protocol (verbs) defined by upper layer (AOP ② transport package).

## Module List

| Module | Files | Documentation |
|---|---|---|
| Connection | `connection.go` | [connection.md](connection.md) |
| Peer + Signaling Client | `peer.go` + `signaller.go` | [peer.md](peer.md) |
| Transport Abstraction + Message Types | `transport.go` + `message.go` | [transport.md](transport.md) |

## Three-Module Collaboration

```
Upper (internal/service/peerjs_service.go, AOP ②)
  │  NewPeer / Connect / OnConnection / SendFrame / OnMessage
  ▼
Peer (signaling: registration/routing/heartbeat/reconnection)    Connection (single connection: atomic frame send/flow control/lifecycle)
  │  OFFER/ANSWER/CANDIDATE routing                                 │  SendFrame(header, body) / SendText / Send
  ▼                                                                ▼
peerJSSignaller (WS signaling, H7 disconnection notification)   DataChannel interface (pionChannel adapter)
```

## Key Facts

- Independent go.mod (`github.com/Hana-ame/go-peerjs`), main go.mod `replace` references — independently evolvable
- Three extension points: `Signaller` interface (swap signaling), `DataChannel` interface (swap transport), open string `MessageType` (add messages)
- Text frame (PPID 51) vs binary frame (PPID 53) distinction is the foundation of the upper frame protocol (control headers vs data blocks)
- Flow control trio (BufferedAmount/LowThreshold/OnBufferedAmountLow) exposed at this layer, upper serveFile water-level flow control depends on it (sessions.md)
- No business knowledge: doesn't know about req/upload/admin or any other verbs

## Tests (21 unit tests, independent go.mod)

- `peer_test.go` (in-memory signaling stub, no public network dependency) + `flowcontrol_test.go` (flow control paths)
- In-layer: `cd back/peerjs && go test ./... -count=1 -race` (`scripts/test-layers.sh` L1 section)
- Integration tests: `cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v` (real public signaling 0.peerjs.com, requires proxy, must use `-p 1` serial)

## File List

| File | Lines | Description |
|---|---|---|
| `peer.go` | 560 | Peer + peerJSSignaller (signaling client/heartbeat/H7) |
| `connection.go` | 335 | Connection (frame primitives/flow control/lifecycle) |
| `transport.go` | 57 | Frame + DataChannel interface + pionChannel |
| `message.go` | 93 | Message/payload/Options |
| `signaller.go` | 39 | Signaller interface |
| `peer_test.go` | 450 | Unit tests |
| `testutil_test.go` | 174 | In-memory signaling stub |
| `flowcontrol_test.go` | 77 | Flow control unit tests |
| `README.md` | — | Library's own README |
