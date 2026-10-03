# Peerdrive Node (Go) Features & P2P Connection System

> Code: `back/cmd/server/main.go` (entry) + `back/internal/transport/` (interconnection layer) + `back/peerjs/` (transport primitives).
> Node responsibilities: **always online + content-addressed storage (sha256) + bidirectional file service + port forwarding**, interconnecting with browsers/other nodes.

## 1. Node Features Overview

| Feature | Entry | Description |
|---|---|---|
| Signaling registration | `peerjs_service.go` Start/startLoop | PeerJS WS to public cloud or self-hosted; exponential backoff disconnect reconnect (2s→60s) full round (H7) |
| Active interconnection | `connectLoop` | `PEERDRIVE_PEERJS_PEERS` static config + discovery callback; infinite retry until OnOpen |
| Passive receive | `onIncomingConnection` | Wait for OnOpen then bindConn (pitfall: premature binding gets unready connection) |
| File service (inbound) | `inbound.go` | serveFile (64KB chunks + flow control), index verbs (create/upload/list/info/delete/sync), upload chunk worker |
| File fetch (outbound) | `outbound.go` | `OpenStream` streaming fetch (UUID reqId routing + bounded queue) + sha256 verify fallback; `FetchFromPeer` compatibility wrapper |
| Local session | `/ws/peer` | Browser WS directly connected to this node, frame protocol identical to DataChannel, millisecond-level no hole-punching needed |
| Room discovery | `http_discovery.go`/`mqtt_discovery.go` | Self-hosted discovery API priority (`PEERDRIVE_DISCOVER_URL`), otherwise MQTT sharded rooms |
| Port forwarding v2 | `forward.go` | HMAC challenge authentication + port whitelist, TCP tunnel over DataChannel |
| Unified source | `main.go:98` | LocalSource (file_index+CAS) → PeerSource (p2p pass-through) → URLSource (template) |
| Management endpoints | `peerjs_routes.go`/`source_routes.go` | `/peerjs/node`、`/peerjs/fetch`、`/sources` |

Startup flow: `InitDB → NewPeerJSService → SetupRouter → Gin :PORT`.

## 2. P2P Connection Classification

### 2.1 By Transport Type (2 types, unified by `Session` interface, `ws_session.go:22`)

| Type | Adapter | id | Usage |
|---|---|---|---|
| **WebRTC DataChannel** | `rtc_session.go` Wraps `*peerjs.Connection` | Remote peer id | Remote node / browser directly connected via signaling (NAT hole-punch) |
| **Local WebSocket** | `ws_session.go`（`NewWSSession`） | `"local"` | Browser directly connected to this node, no signaling/hole-punch overhead |

Both have identical semantics: same reqId state machine, same frame protocol (text frame=JSON header, binary frame=data block),
After `Session` interface abstraction, `FetchFromPeer("local", ...)` and remote fetch reuse with zero branches.

### 2.2 By Establishment Direction (Signaling Layer Role, `peer.go`)

- **offerer (active side)**: `connectLoop` → `peer.Connect(ctx, dst, label)` → send OFFER
- **answerer (passive side)**: receive OFFER → `handleOffer` creates Connection → reply ANSWER
- Constraint: answerer must use offerer's `connectionId`, otherwise ANSWER won't route (REFACTOR §5 first pitfall)
- Reconnect: connectLoop infinite loop + exponential backoff (EXPIRE / ICE failure / peer disconnect all trigger)

### 2.3 By Frame Role (inbound / outbound, `conn.go` header comments)

WebRTC connections are full-duplex symmetric, same Session **carries both roles simultaneously** (can serve peer req on one side,
and collect own request responses on the other), sharing no mutable state (except respective slots within connState):

| Role | Attribution | File |
|---|---|---|
| **inbound** (inbound = 'others ask me, I answer') | Respond to verbs: `req` (serveFile), `create/upload/list/info/delete/sync` (serve* index), `fwd-open/fwd-auth/fwd-data/fwd-close` (forwarding) | `inbound.go` + `forward.go` |
| **outbound** (outbound = 'I ask others') | Initiate `req` (requestFile/openStream) and collect `meta/data/done/err` (routeResponse); client-side forwarding handshake `OpenForward` | `outbound.go` + `forward.go` |
| **Shared mechanism** (only one copy) | Frame types, reqId state machine, binary block routing, flow control | `conn.go` |

### 2.4 By Usage

- **File transfer**: Fetch (req streaming) + Chunked upload (upload, 64KB chunk bitmap) + Index sync (sync, seq cursor incremental)
- **Port forwarding tunnel**: Single slot `connState.fwd` per connection (one active forwarding stream at a time); fwd-data block routing has priority over file data (binary blocks route by 'fwd-data header declaration' first)

## 3. Connection Establishment Flow (Full Chain)

```
Discovery (HTTP polling 10s / MQTT heartbeat / PEERS static config)
   │ onDiscoveredPeer / config parsing
   ▼
connectLoop(peerID)  ──dedup（connecting map）──►  peer.Connect
   │ OFFER {dst, connectionId, SDP} ──signaling──► Peer handleOffer
   │ ◄── ANSWER（reuses connectionId）        peer replies ANSWER
   │ CANDIDATE ⇄ ICE candidate exchange (STUN hole-punch)
   ▼
WebRTC DataChannel opened → OnOpen → bindConn(newRTCSession)
   │ Register conns[peerID] + connState (fetches/pendingUpload/fwd slots)
   │ OnMessage pump: text frames dispatched by type (verb→inbound role / rest→outbound response routing)
   │ Binary blocks routed by expect state (fetch queue / upload worker / forwarding tunnel)
   ▼
Full-duplex concurrency: serveFile responds to peer req while simultaneously OpenStream can fetch peer files
```

## 4. Verification Information Generation

### 4.1 Signaling Layer (Identity, `peer.go`)

| Credential | Generation Method | Purpose |
|---|---|---|
| **key** | Shared config (`PEERDRIVE_PEERJS_KEY`, default peerjs) | WS URL parameter; server rejects if mismatch (prevents stranger registration) |
| **token** | Client startup `randomToken()` = 16 bytes random hex (`peer.go:302`) | On same-id reconnect, token must match to take over old connection; mismatch returns `ID-TAKEN` (prevents ID hijacking) |
| **id** | Custom (`PEERDRIVE_PEERJS_ID`) or server-assigned via `GET /peerjs/id` | Node identity in signaling network |

### 4.2 Business Layer (Port Forwarding HMAC Challenge, `forward.go`)

```
Server                                   Client (OpenForward)
  │ ←── fwd-open {port, reqId} ────────────
  │ rand.Read → 16B nonce（one-time + 5min expiry + max 64)
  │ ── fwd-challenge {nonce} ──►
  │                                     hmac = HMAC-SHA256(key, nonce) → hex
  │ ◄── fwd-auth {hmac} ────────────────
  │ Iterate rules table, recompute HMAC with original key, hmac.Equal constant-time comparison
  │ Pass → verify port in key authorization whitelist → dial 127.0.0.1:port (SSRF protection)
  │ ── fwd-ok ──►  tunnel established，fwd-data bidirectional pass-through
```

- key is the credential: `PEERDRIVE_FORWARD_RULES="key1:8080,key2:8443"` or runtime `POST /p2p/forward/create` dynamic addition (not persisted)
- key plaintext never on the wire (client holds locally, only HMAC on wire; DataChannel itself DTLS encrypted for double protection)
- Failure only returns `fwd-err`, no rule details leaked; anti-replay: nonce marked used upon retrieval

### 4.3 Transport Layer (WebRTC Built-in)

- **DTLS encryption**: pion auto-generates self-signed certificate, negotiated after signaling SDP exchange
- **ICE**: STUN/TURN hole-punch (`parseICEServers`, `PEERDRIVE_WEBRTC_STUN/TURN` config)

### 4.4 Local WS Session (No token)

- Only HTTP **Origin whitelist** check (`peerjs_routes.go:99`, same CORS config `IsOriginAllowed`);
  No Origin (curl) allowed, Origin outside whitelist rejected for upgrade

## 5. Security Boundary Summary

| Layer | Protection |
|---|---|
| Signaling registration | key verification + token prevents ID hijacking |
| File service | hash strictly 64hex (H1); file_index path must be within allowed roots, unauthorized fallback to CAS (H2); remote declaration limit 8GB (H6) |
| HTTP fetch endpoints | Authentication + 64MB single-limit (H4) |
| Upload | size ≤8GB, filename sanitization (prevents path traversal), offset chunk alignment (REFACTOR §4) |
| Forwarding | HMAC challenge + port whitelist + loopback only + nonce one-time |
| WS sessions | Origin whitelist + read limit 192KB + ping/pong 90s keepalive (M5) |
| Signaling server | key verification, ID-TAKEN, queue limit 100/dst, 40KB read limit, body 1KB (see doc/PEERSIGNAL.md) |
