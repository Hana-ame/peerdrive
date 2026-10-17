# Connection Module (back/peerjs/connection.go)

> One-line responsibility: An encapsulation of a WebRTC DataConnection — SDP/ICE via signaling, data plane via DataChannel, providing atomic send primitives for "text frames (JSON headers) + binary frames (data blocks)" with write buffer flow control.

## Responsibilities

- Wraps `pion/webrtc` PeerConnection + DataChannel, not bound to a specific transport type (depends on `DataChannel` interface, see transport.md)
- Provides three send primitives: `Send` (pure binary blocks), `SendText` (JSON headers), `SendFrame` (atomic header+body frames)
- Handles the parts of signaling messages related to "this connection" (ANSWER/CANDIDATE: set remote SDP, add ICE candidates)
- Lifecycle: offerer actively establishes connection (`makeOffer`) or answerer passively responds (`handleOffer`), auto-cleanup on ICE failure
- **Contains no business frame protocol knowledge** (verbs defined by upper transport package) — module positioning is transport primitives

## Key Mechanisms

### 1. Frame Model: Text Frames vs Binary Frames

`Send` uses SCTP PPID 53 (binary), `SendText` uses PPID 51 (text). Receiver (pion `OnMessage` → `Frame{IsText}`) uses this to distinguish "control headers vs data blocks" — **protocol depends on this distinction**:

- Control headers must use `SendText`/`SendJSON`. If JSON is accidentally sent with `Send([]byte)`, the peer will judge the header as a binary data block and discard/misroute it (connection.go:86-87 comment).

### 2. SendFrame Atomicity + Flow Control (connection.go:114-148)

```go
c.sendMu.Lock()          // ① Serialize: header+body must be written consecutively
dc := c.dc               // ② Read dc snapshot once while holding sendMu (M9, avoid locking each time)
dc.SendText(headerJSON)  // ③ Header
for dc.BufferedAmount() > 512KB {  // ④ Built-in backpressure
    select { <-lowWater / <-done / 30s timeout }
}
dc.Send(body)            // ⑤ Body
```

- **Why atomic**: Upper state machine routes data by "data header → subsequent binary block"; if header and body interleave during multi-goroutine concurrent sending, data blocks attach to the wrong request.
- **Flow control**: `defaultBufferLowThreshold = 512KB`. When send buffer exceeds threshold, wait for low-water event before sending next block, preventing slow consumers from blowing up pion's buffer.
- **Pitfall (original comment)**: pion's `OnBufferedAmountLow` is a **replacement callback** — if each concurrent sender registers its own, only the last registrant receives events, others deadlock (caused concurrent serveFile hangs). Therefore the callback is globally registered once during `attach`, waiting uniformly through the `lowWater` channel (capacity 1 to prevent backlog).
- 30s flow control timeout (low-severity 1 fix): Previously only waited for done/lowWater, relying on ICE disconnected (~30s) as fallback for slow consumers — now explicitly capped, timeout returns error for upper layer to disconnect/retry.

### 3. M9: dc Field Data Race Protection

`dc` (DataChannel) is written **without a lock** during `attach` (pion's `OnDataChannel` callback runs on PC goroutine), while `Open/Send/SendFrame/Close` read from any goroutine concurrently — -race always triggers, extreme cases read nil half-initialized. `dcMu sync.RWMutex` protects read/write; attach is only called once, lock overhead is negligible.

### 4. Lifecycle

- **Establishment** (offerer, `newConnection` → `makeOffer`):
  1. `NewPeerConnection` + register ICE state/candidate/DataChannel callbacks
  2. `CreateDataChannel(label, {Ordered:true})` → `attach`
  3. `CreateOffer` → `SetLocalDescription` → signaling `OFFER` (payload includes connectionId/label/reliable/serialization=raw)
- **Response** (answerer, `handleOffer`): `SetRemoteDescription` → `CreateAnswer` → `SetLocalDescription` → signaling `ANSWER`
- **Cleanup**: `pc.OnICEConnectionStateChange` calls `conn.Close()` for Closed/Failed/Disconnected states (same behavior as peerjs-client negotiator, preventing leaks)
- **Pitfall (connection.go:201-203)**: Answerer must reuse offerer's connectionId. Previously answerer generated new ID, causing ANSWER not to find peer conn during signaling routing (by connectionId), ICE stuck at checking forever.
- **Pitfall (connection.go:216-221)**: On duplicate OFFER, old connection must go through full `Close` (closeOnce idempotent) — only closing pc leaks: old connection remains in conns map, done never closes, onClose doesn't fire. Must be called after `p.mu` unlock (Close→forgetConnection needs the same lock, Go mutex is non-reentrant).

### 5. Signaling Message Handling (handleMessage)

Only handles ANSWER (SetRemoteDescription) and CANDIDATE (AddICECandidate). Parse errors are **silently ignored**: peer may send out-of-order/expired candidates, failure only means this round of negotiation failed, ICE state callback handles final cleanup.

### 6. attach Callback Layout

- `OnOpen` → upper `onOpen` (connection ready notification)
- `OnMessage` → upper `onMessage(Frame)` (data plane)
- `OnClose` → `c.Close()`: **When remote actively closes dc, this side cleans up immediately** (Close idempotent). Otherwise local connection hangs, relying on ICE disconnected as fallback (seconds to minutes, too slow)
- `OnBufferedAmountLow` → `lowWater` broadcast (global once)

### 7. ICE Candidate Forwarding (connection.go:234-244)

Pion doesn't automatically send candidates — must manually `OnICECandidate` + signaling CANDIDATE message (`CandidatePayload{Type: ConnData, ConnectionID}`), otherwise both sides stay at checking forever and never connect.

## Relationships with Other Modules

```
Peer (signaling routing/registry)
  └─ Connection (this module)
        ├─ Signaling messages: ANSWER/CANDIDATE (in) / OFFER/CANDIDATE (out, via Peer.Send)
        ├─ DataChannel interface (transport.go): pionChannel adapter
        └─ Upper (internal/transport): OnOpen/OnMessage/OnClose callbacks + SendFrame primitive
```

- `Peer.Connect` → `newConnection(offered=true)`; `Peer.handleOffer` → `newConnection(offered=false)`
- Upper transport package gets the underlying channel through `DataChannel()` for advanced flow control (water-level flow control only applies to WebRTC, see sessions.md)

## Pitfalls and Design Decisions

| # | Pitfall | Fix | Source |
|---|---|---|---|
| M9 | attach writes dc without lock vs concurrent reads, -race always triggers | dcMu RWMutex | connection.go:30-34 |
| — | OnBufferedAmountLow replacement callback, concurrent registrations overwrite each other → deadlock | Register once during attach + lowWater channel | connection.go:14-17, 276-283 |
| — | Answerer generates new connectionId → ICE stuck at checking | Reuse offerer's connID | connection.go:201-203 |
| — | Duplicate OFFER only closes pc → conns map leak + done never closes | Full Close (closeOnce idempotent), called outside lock | connection.go:216-221 |
| — | Pion doesn't auto-send ICE candidates | Manual OnICECandidate + signaling forwarding | connection.go:232-233 |
| — | Slow consumer waits indefinitely for flow control | 30s timeout cap (low-severity 1) | connection.go:134-144 |
| — | Remote closes dc, local side hangs | dc.OnClose → Close (idempotent) | connection.go:295-297 |
| — | Sending JSON header as binary frame → peer misjudges as data block | SendText exclusively for control headers | connection.go:86-87 |
| Low-7 | ID-TAKEN silently ignored → same-ID dual nodes lose contact with no trace | Log output (peer.go side) | peer.go:183-194 |

## Tests

- `flowcontrol_test.go` (77 lines): SendFrame flow control behavior verification (low-water wait/timeout/closure exit paths)
- `peer_test.go` (450 lines, including `testutil_test.go` 174 lines in-memory signaling stub): Connection establishment/message routing/lifecycle — in-memory signaller makes unit tests not depend on public network

## File List

| File | Description |
|---|---|
| `connection.go` | This module (335 lines) |
| `flowcontrol_test.go` | Flow control unit tests |
| `peer_test.go` + `testutil_test.go` | Connection-level tests and test utilities (in-memory signaling stub) |
