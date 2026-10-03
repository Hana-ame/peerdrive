# HTTP API Tunnel (HTTP API Proxy over P2P)

> 2026-08-16 · Design document (not yet implemented).
> Goal: Enable browsers/NAT'd nodes to access remote nodes' gin REST API through existing PeerJS + WebRTC P2P connections,
> with **permission-based access control** (untrusted node network, peerId cannot be used as identity).

---

## 1. Why

peerdrive's interconnection layer is PeerJS signaling + WebRTC DataChannel; nodes/browsers currently only exchange
file protocol (verbs: `req/meta/data/done/err` + file index). Each node runs a gin REST API,
but it's only accessible locally — NAT'd remote browsers/nodes can't reach it at all.

With HTTP tunnel added, any connected and authorized peer can:
- Remotely operate NAT'd nodes (`curl http://A:3000/peerjs/r/<BID>/collections`)
- Browser bridges through local node A to read/manage remote node B's collections, files, shares, tasks
- Future: browser can directly WebRTC-connect to B, same verb set, no relay needed

## 2. Core Decisions

- **Verb-based, not repurposing wintools' webrtc-proxy binary**: wintools `cmd/webrtc-proxy` is an independent
  binary + independent peerjs implementation; peerdrive already has `back/peerjs` (go-peerjs) + `Session` abstraction +
  reqId state machine + `SendFrame` built-in flow control. The correct approach is to reuse these, implementing HTTP tunnel as a new verb.
- **Target fixed to local node's gin**: Forward target is always `http://127.0.0.1:<cfg.Port>`, naturally no SSRF.
- **Default deny + token auth + least privilege**: Untrusted node network, peerId can be hijacked/spoofed,
  authorization only recognizes tokens, not peerId.

## 3. Frame Protocol (4 New Verbs, Reusing Session/reqId/SendFrame)

```
auth        (text)  {token}                    Auth first after connection established (within DTLS encrypted channel)
                    → auth-ok {perm} | auth-deny {msg}
httpreq     (text)  {reqId, method, url, headers, body:bool}   url must start with /
httpbody    (text)  {reqId, size} + subsequent binary blocks    Request/response body blocks
httpend     (text)  {reqId, error?}                           Request body end / response complete
```

- `httpbody` header+body sent atomically via `Session.SendFrame` (`back/peerjs/connection.go` sendMu),
  receiver uses "connection-level expect" state machine to attach binary blocks to the stream following its header — same mechanism as existing
  download/upload binary routing, multiple concurrent streams on the same connection don't conflict.
- `reqId` is UUID v4 (consistent with existing `requestFile`), unique across connections.
- Request body composed of multiple `httpbody` blocks after `httpreq{body:true}`, `httpend` indicates EOF;
  response is symmetric (server replies `httpresp` + `httpbody` blocks + `httpend`).

## 4. Permission Model (token → permission name → rules)

### 4.1 Connection-Level Authentication

After DataChannel open, the peer must first send `auth{token}` text frame (transmitted within DTLS channel,
signaling MITM cannot access it). Server validates token → binds permission name to that connection's `connState`.
**Unauthenticated connection / wrong token → all subsequent httpreq return 403, never reaches gin.**
The connection itself can be retained (file pulling continues to work normally).

### 4.2 Built-in Roles

| Role | Permissions |
|---|---|
| `none` (default) | No tunnel permission, httpreq → 403 |
| `read` | GET/HEAD + read-only API prefix whitelist (e.g., `/collections`, `/peerjs/node`, `/p2p/*`, `/tasks`, `/sha256sum/`) |
| `admin` | All methods / paths |

### 4.3 Optional Rule Stacking

Custom permission names can stack `method:path-prefix` rules on top of base roles,
precisely down to individual endpoints (e.g., only allow `post:/collections/fork`).

### 4.4 Configuration (env)

```bash
PEERDRIVE_HTTP_PROXY_ENABLE=true
PEERDRIVE_HTTP_PROXY_TOKENS=tokenA:admin,tokenB:read,tokenC:custom   # token:permission_name
PEERDRIVE_HTTP_PROXY_RULES=custom:post:/collections/fork,custom:get:/collections/*  # Optional stacking
PEERDRIVE_HTTP_PROXY_CLIENT_TOKEN=<token held by this node when initiating tunnel>
PEERDRIVE_HTTP_PROXY_RATE=50            # Requests per second per connection
PEERDRIVE_HTTP_PROXY_MAXSTREAMS=16      # Concurrent tunnel stream limit
```

## 5. Permission Enforcement Chain

```
Peer connection open
  → Peer sends auth{token} (within DTLS channel)
  → Target node validates token → binds perm to connState
  → Each httpreq: method + path validated against perm + rules
  → Only forward to http://127.0.0.1:<cfg.Port> + url if passed
  → Unauthenticated / unauthorized: httpresp 403, never reaches gin
```

- **Stacks with REST auth, doesn't replace**: Tunnel layer decides "can use tunnel at all, which API groups can be touched";
  gin's original Bearer user auth (`AuthOptional`/`AuthRequired`) continues to work normally, tunnel headers pass through.
- **Rate limiting + audit**: Per-connection rate/concurrency limits, `serveHTTPProxy` records audit logs
  (remote peerId + method + path + result), "irresponsible" nodes are traceable, immediate circuit breaking on limit exceedance.

## 6. Client Entry Points

### 6.1 Node Bridging (Phase 1, zero frontend changes)

```
ANY /peerjs/r/:peer/*path → Get conns[peer] → Connection-level auth (using CLIENT_TOKEN) → httpreq tunnel
```

Browser/curl: `curl http://localhost:3000/peerjs/r/<BID>/collections`.

### 6.2 Browser Direct (Future Extension)

Browser directly sends `auth` + `httpreq` frames on its existing Session (WS local `/ws/peer` or WebRTC remote),
JS helper encapsulation. Zero backend changes needed on frontend side.

## 7. Threat Model (Documented to Prevent Misuse)

- **peerId can be hijacked/spoofed** (PeerJS ID first-come-first-served) → **Authorization only recognizes tokens, not peerId**.
- Tokens are only transmitted within DTLS-encrypted DataChannel, signaling MITM cannot access them.
- The upper limit of what a peer node can do = permissions corresponding to its held token; default is "cannot touch your REST API".
- Target is always local node's gin → No SSRF; url validation must start with `/`, no scheme/host.

## 8. Implementation Order

1. Config items (config.go): the 6 `PEERDRIVE_HTTP_PROXY_*` above
2. Frame protocol + `auth` verb handling (add case to `bindConn` switch) + `connState` binds perm
3. `serveHTTPProxy` (forward to local node's gin, with reqBodyQueue backpressure + timeout + audit)
4. Bridge route `ANY /peerjs/r/:peer/*path` (client-side stream state machine, blocks on full without dropping blocks)
5. Rate limiting + concurrency limit + audit logging
6. Unit tests (WSSession mutual auth/httpreq frames) + integration tests (dual-node connection curl)

## 9. Related Files

- Frame protocol/reqId routing/`bindConn`: `back/internal/service/peerjs_service.go`
- `Session` abstraction: `back/internal/service/ws_session.go`, `rtc_session.go`
- Atomic frames + flow control: `back/peerjs/connection.go` (`SendFrame`)
- Route mounting: `back/internal/router/peerjs_routes.go`
- Reference implementation (wintools): `cmd/webrtc-proxy/main.go` (reqBodyQueue / stream / forward timeout)
- Reference security pairing: `~/p2ptun` (OFFER metadata secret pairing)
