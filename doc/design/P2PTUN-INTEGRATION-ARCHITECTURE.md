# P2PTun Integration Architecture (Issue #315)

## 1. Background & Context

In the Peerdrive ecosystem, TCP port forwarding was introduced in `CapForward` (`fwd-open`, `fwd-challenge`, `fwd-auth`, `fwd-data`, `fwd-close`).
However, that implementation is single-stream: each port forward session requires a dedicated channel pairing, and lacks stream-level multiplexing (SYN/DATA/FIN) and granular bufferedAmount backpressure.

The organization maintains `Hana-ame/p2ptun` (https://github.com/Hana-ame/p2ptun), a battle-tested P2P TCP multiplexer built on PeerJS signaling and WebRTC DataChannels.

## 2. Capability Negotiation

A new capability identifier is added to `KnownCapabilities` (Issue #315):
- `CapP2PTun = "p2ptun"` (`BitP2PTun`)

When two nodes connect, they negotiate `p2ptun` support via the `cap` / `psk-auth` handshake frame.
If both sides declare `CapP2PTun`, high-performance multiplexed tunneling is activated.

## 3. Multiplexed Frame Specification

Over the DataChannel, virtual TCP streams are multiplexed using binary frames with a 9-byte header:

```
+---------------+----------------+----------------+-------------------+
| StreamID (4B) | FrameType (1B) | Length (4B BE) | Payload (N bytes) |
+---------------+----------------+----------------+-------------------+
```

Frame types:
- `0x01` SYN: Open a new virtual TCP stream for target port/service.
- `0x02` SYN-ACK: Confirm stream establishment.
- `0x03` DATA: Carry TCP payload bytes with stream sequence.
- `0x04` FIN: Half-close the stream (graceful TCP shutdown).
- `0x05` RST: Immediate stream teardown (connection reset/error).
- `0x06` ACK / WINDOW: Stream-level flow control window update.

## 4. Flow Control & Backpressure

To prevent head-of-line blocking (linking with Issue #274 and #316):
- Each virtual stream respects an initial 1MB window buffer.
- When the underlying DataChannel `bufferedAmount` exceeds 4MB, sender halts reading from the local TCP socket.
- When `bufferedAmountLow` fires (< 1MB), socket read loops are resumed.

## 5. Security Model

- Admission: Governed by `PEERDRIVE_PSK` and node authorization.
- Port Whitelist: Target ports are restricted by operator policy (e.g. `PEERDRIVE_FORWARD_PORTS`).
- Isolation: `p2ptun` streams cannot access unadvertised services or local file systems.
