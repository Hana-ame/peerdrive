# Interconnection Framework: WS + PeerJS Dual Transport + Session Abstraction + Signaling Assembly

> 2026-08-16 · Corresponding code `back/internal/transport/` (peerjs_service.go / conn.go /
> inbound.go / outbound.go / ws_session.go / rtc_session.go / forward.go)
> This document describes how transport is abstracted, how connections are established, and how assembly is wired.

---

## 1. Framework Overview

```
                    ┌────────────────────────────────────────────┐
                    │              PeerJSService                 │
                    │   (Assembly layer: signaling lifecycle + connection establishment)        │
                    │                                            │
  ┌─ startLoop ─────┤  Signaling registration / disconnect-reconnect (Signaller abstraction)        │
  │  connectLoop ───┤  Active dial to remote nodes (including reconnect)                 │
  │ onIncoming ─────┤  Passive accept of browser/node connections                   │
  │  BindLocal ─────┤  Local WS session (management only, browser directly connected to this node)   │
  └─────────────────┴──────────────┬─────────────────────────────┘
                                   │ bindConn (unified dispatch)
                    ┌──────────────▼──────────────┐
                    │        connState            │  ← conn.go shared core
                    │  fetchState / uploadState   │     (reqId state machine,
                    │  fwdStream / binCh/fwdCh    │       binary block routing,
                    │                            │       flow control, worker delivery)
                    └──────┬───────────────┬──────┘
              ┌────────────▼────┐   ┌──────▼────────────┐
              │  inbound.go     │   │  outbound.go      │
              │  Inbound role       │   │  Outbound role         │
              │  serveFile/     │   │  FetchFromPeer/   │
              │  serve* index verbs │   │  requestFile/     │
              │  uploadWorker   │   │  routeResponse    │
              └────────────┬────┘   └──────┬────────────┘
                           │               │
┌────────────▼───────────────▼──────┐
               │          Session Interface              │
               │  (Transport abstraction: both implementations have consistent semantics)      │
               └───────┬─────────────────┬──────────┘
         ┌─────────────▼─────┐   ┌───────▼─────────────┐
         │  WSSession        │   │  rtcSession         │
         │  Local WebSocket   │   │  WebRTC DataChannel │
         │  (Management only, no transport)  │   │  (File data transfer)     │
         └───────────────────┘   └─────────────────────┘
```

> ****Division of labor principle: WS for management only, WebRTC for data.**** WS local sessions only carry management-type verbs
> (metadata plane + control for file index create/upload/list/info/delete/sync),
> do not carry file content transfer (req/meta/data/done/err large file fetching goes through WebRTC).

## 2. Session Abstraction (Direction-neutral Transport, `ws_session.go` / `rtc_session.go`)

```go
type Session interface {
    ID() string
    SendJSON(v any) error              // Text frame (JSON control header)
    SendFrame(header any, body []byte) error // 'JSON header + binary body' atomic frame
    OnMessage(f func(peerjs.Frame))    // Frame callback (IsText distinguishes text/binary)
    OnClose(f func())
    Close()
}
```

**Why abstract**: Both transports carry the same frame protocol (req/meta/data/done/err + file index verbs +
forward verbs), same reqId state machine / serveFile / FetchFromPeer zero-branch reuse——
browser side only needs one set of protocol codecs. But **division of labor differs**: WS for management only (metadata/index verbs),
WebRTC for data (large file fetch/upload).

| Implementation | File | Purpose | Differences |
|---|---|---|---|
| `WSSession` | ws_session.go | Browser directly connects to this node (`/ws/peer`), **management only**: file index verb metadata plane + control | No signaling/hole-punching, millisecond-level; TCP built-in backpressure so no write buffer flow control; SetReadLimit(3×64KB) + 90s ping/pong keepalive (M5) |
| `rtcSession` | rtc_session.go | Remote nodes/browsers directly connected via signaling, **file data transfer** (fetch/upload/forward) | Wraps a `*peerjs.Connection`: Connection.ID is the signaling routing key (connectionId), semantically different from session identifier (peer id); provides DataChannel() for serveFile water-level flow control |

## 3. Signaling Assembly (`peerjs_service.go`)

The assembly layer only does four things: **signaling lifecycle, connection establishment, local session binding, discovery assembly**.
Both connection establishment paths (accept/dial) merely create a full-duplex Session, then via
`bindConn` attaches the same inbound+outbound roles——WebRTC connections are symmetric, no direction distinction.

### 3.1 startLoop — Signaling Lifecycle (Automatic Disconnect Reconnect)

```
Start() → startLoop loop:
  1. Construct peerjs.NewPeer(id, opts) with cfg (Host/Port/Secure/Key), register signaling
  2. p.Dial(ctx) fails → exponential backoff retry (2s→60s)
  3. success → dial configured peers (PeerJSPeers) → start discovery (DiscoverURL takes priority over MQTT)
  4. Blocks until: ctx cancelled / closed / signaling disconnect (Signaller.Done(), H7 fix)
     → Full round reconnect (reusing backoff)
```

Trigger condition: `PEERDRIVE_PEERJS_ENABLE=true` (default), `Start()` during main assembly.

### 3.2 connectLoop — Active Dial (Including Auto Reconnect)

```
connectLoop(peerID)：
  1. connecting deduplication (low-risk 3: configured PEERS and discovery callback may trigger simultaneously)
  2. peer.Connect(ctx, peerID, "peerdrive")
  3. OnOpen → bindConn(newRTCSession(c))，then block until conn.Done()
  4. fail/timeout/disconnect → exponential backoff retry, until service shuts down
```

Trigger condition: peer may be offline (OFFER enqueued EXPIRE) or ICE failure——must loop retry
until actual OnOpen; `connecting` deduplication prevents double connections.

### 3.3 onIncomingConnection — Passive Accept

```
Peer initiates connection → bindConn after OnOpen (must wait for open: answerer callback in handleOffer
triggers immediately, premature registration causes FetchFromPeer to get unready connection)
```

### 3.4 BindLocal — Local WS Session (Management Only)

```
BindLocal(sess Session) → bindConn(sess)
Register key="local": Browser directly connects to this node via /ws/peer, carries management verbs
(file index create/upload/list/info/delete/sync metadata plane + control),
does not carry file content transfer——all file data goes through WebRTC (rtcSession)
```

Trigger condition: Router calls BindLocal after successful `/ws/peer` upgrade (browser direct connection).
Security boundary: Before Upgrade, CheckOrigin only allows configured Origins (same as HTTP CORS whitelist,
peerjs_routes.go:98-110)——local sessions are the 'browser can manage local files' channel,
Origin whitelist is the only line of defense.

### 3.5 Discovery Assembly (Multi-path Concurrent)

```
DiscoverURL non-empty → HTTPDiscovery (self-hosted discovery API, 10s polling, priority over MQTT)
otherwise MQTTEnable → MQTTDiscovery (sharded rooms peerdrive/v1/{collHash}/nodes)
Both callbacks → onDiscoveredPeer → connectLoop (skip if already connected)
```

## 4. bindConn — Connection Unified Dispatch (`conn.go`)

bindConn is the **shared connection core (do not copy)**: one full-duplex connection reused, same Session
carries both inbound+outbound roles simultaneously, distinguished by 'frame type + reqId':

| Frame | Attribution | Handling |
|---|---|---|
| Text frame type = verb (req/create/upload/list/info/delete/sync + fwd-open/auth/data/close) | Inbound role | `go s.serveXxx(c, ...)`(fork goroutine to respond) |
| Text frame type = fwd-challenge/ok/err | Outbound role (forward client side) | `routeForwardResponse`(forward.go:351, independent of file response routing) |
| Other text frame types | Outbound role | `routeResponse` Route by reqId (meta/data/done/err file fetch responses) |
| Binary frame | Data block | Pump routing decision: fwd tunnel (pending flag) → expect (download) → upload (chunked); disk IO delegated to connection-level worker (H5, prevents head-of-line blocking) |

**Three protocol constraints (do not break)**:
1. JSON control headers must be **text frames**, data blocks must be **binary frames**
2. data header and data block must be **atomic consecutive**（SendFrame sendMu）
3. 3. Go side always carries reqId (browser may omit, backward compatible)

## 5. Assembly Call Order (main → router → transport)

```
internal/serverapp/app.go
  ├─ cfg := config.Load()
  ├─ repository.InitDB / SetAnonStorageDir
  ├─ peerjsSvc = transport.NewPeerJSService(cfg, storageDir)  // Assembly point
  │    ├─ SetForwardRules（PEERDRIVE_FORWARD_RULES）
  │    └─ source registration:local(fileIndex) → peer → url
  ├─ router.SetPeerJSService / SetPeerJSConfig / SetSourceManager
  └─ router.SetupRouter(cfg)
       └─ /ws/peer upgrade → peerjsService.BindLocal(sess)
       └─ /peerjs/* routing (node discovery + fetch verification)
```

## 6. Extension Points (Future extensions don't touch core)

- Change signaling：`peerjs.NewPeerWithSignaller()` Inject custom Signaller
- Change transport：Implement `Session` interface (existing WSSession/rtcSession examples)
- Add verb：`MessageType`/`Frame` Open type + add case to bindConn dispatch
- Add discovery：HTTPDiscovery/MQTTDiscovery implement the same callback interface

## 7. Verification

```bash
cd back/peerjs && go test ./... -count=1 -race     # Transport primitives
cd back && go test -tags nosqlite ./internal/transport/ -race  # Assembly + roles
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v
```