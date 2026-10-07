# signalserver —— the self-hosted signaling server

> One-line responsibility: a PeerJS signaling server compatible with a subset of the peerjs-server protocol + a built-in room discovery
> API (`back/signalserver/`, **standalone go.mod**; the entry point is now `peerdrive signal`) —— replacing public cloud
> signaling (0.peerjs.com) and the public MQTT broker; discovery only exchanges peerIds and does not carry business data.

- Layer belonging: AOP ⑥ discovery aspect (`doc/LAYERS.md` §1)
- Positioning: signaling + discovery in one; the server naturally knows all online nodes (they all connect to it for signaling),
  so room discovery becomes an HTTP query and MQTT broadcast is no longer needed
- History: REFACTOR.md §3.6 (the self-hosted signaling server), §3.6.1 (cloudcone production deployment)

---

## Responsibilities

1. **PeerJS protocol signaling**: a node registers with `WS /{path}peerjs?key=&id=&token=`;
   `OFFER/ANSWER/CANDIDATE/LEAVE` messages are forwarded by `dst`; when `dst` is offline, enqueue
   (with a 30s expiry, resent after it comes online); `OPEN`/`ID-TAKEN` control messages; heartbeat keepalive.
2. **ID allocation**: `GET /peerjs/id` returns a random id (peerjs API compatible).
3. **Room discovery** (the MQTT feature merged in): `POST /discover/announce {peerId, collections}`
   registers the collections a node follows (refreshed by a 30s heartbeat), and `GET /discover/nodes?coll=` returns the list of online nodes
   (removed after a 90s heartbeat expiry).
4. **Resource protection**: offline queue limits, read limits, body limits, sweeper cleanup —— preventing a malicious
   client from blowing up the server's memory/CPU.

## How to run

```bash
# Start a server locally (unit/integration tests hang the handler straight off httptest and do not need it)
# Since v0.3.0 the main binary has a `signal` subcommand, so no separate build is needed:
cd back && go run -tags nosqlite ./cmd/peerdrive/ signal -addr :9000 -key peerjs
# (the standalone entry point is `back/signalserver/cmd/peersignal` — note the name is peersignal, not peerserver)

# Verify the signaling handshake (from the node's perspective; pointing host/port/key at self-hosted is a zero-change switch)
# Discovery API smoke:
curl -X POST localhost:9000/discover/announce -d '{"peerId":"pd-node-a","collections":["<64hex>"]}'
curl "localhost:9000/discover/nodes?coll=<64hex>"   # → {"nodes":[{peerId,lastSeen}]}
curl localhost:9000/peerjs/id                        # → a random id (peerjs API compatible)
```

The deployment form is a **standalone process** (systemd), with no dependency on the main service (cmd/peerdrive signal is a separate subcommand) —— a crashed signaling
server does not affect already-established WebRTC DataChannel direct connections (only new connections and discovery are affected).

## Module inventory (each file: filename + one-line responsibility + key exports)

### `signalserver.go` —— the signaling + discovery core

| Key exports | Description |
|---|---|
| The `Server` struct | `key` (API key) / `path` / `queueTTL` (30s) / `heartbeatTTL` (90s) / `clients` (id→connection) / `queues` (dst→messages) / `disc` (collection→peerId→lastSeen) |
| The `Message` struct | `{type, src, dst, payload}` —— identical to the peerjs client protocol |
| `NewServer(key string) *Server` | Create an instance (key empty → default "peerjs") |
| `Server.Start()` | Start the background sweeper (30s period, cleaning expired queue items and empty queues) |
| `HandleID(w, r)` | `GET /{path}{key}/id` → a random alphanumeric id (`randomID`, 16 bytes) |
| `HandleWS(w, r)` | WS upgrade + registration + readLoop; validates id/token/key; a token mismatch with the same ID is rejected (ID-TAKEN) |
| `HandleAnnounce(w, r)` | `POST /discover/announce` —— body limited to 1KB, at most 64 collections (M15) |
| `HandleNodes(w, r)` | `GET /discover/nodes?coll=` → `{nodes: [{peerId, lastSeen}]}`, expired ones removed |
| The `NodeInfo` struct | `{peerId, lastSeen}` (a discovery response entry) |
| `maxQueuedPerDst = 100` | The offline queue cap per dst, dropping the oldest on overflow (the H3 fix) |

Internal structures: `client{id, token, conn, sendMu, last}` (sendMu serializes gorilla's concurrent
writes), `queuedMsg{msg, expire}` (enqueued with an expiry), `route` (forward when online/enqueue when offline,
LEAVE/EXPIRE are not enqueued), `flushQueue` (replay on coming online + expiry cleanup), `removeClient`
(LEAVE broadcast + discovery record cleanup, **sending outside the lock**).

### Entry point —— the `signal` subcommand of the main binary

| Key exports | Description |
|---|---|
| `-addr` / `-key` flags | The listen address (default `:9000`) / the API key (default "peerjs") |
| Routes | `/peerjs` (HandleWS), `/peerjs/id` (HandleID), `/discover/announce`, `/discover/nodes` |

## Key mechanisms

### 1. Signaling message routing (aligned with peers/peerjs-server)

```
client A ──{type:OFFER, dst:B, payload}──▶ server
                                             ├─ B online → forward (the server overwrites src=A)
                                             └─ B offline → enqueue (except LEAVE/EXPIRE,
                                                 30s expiry, cap of 100 messages)
B comes online ──OPEN──▶ server ──flushQueue──▶ B (replay the backlog of OFFERs)
B disconnects ──────────▶ removeClient: broadcast LEAVE to all online clients, clear the discovery record
```

- The server **overwrites `src`** (`m.Src = cl.id` in readLoop) —— preventing a client from forging a source.
- ID occupation protection: on reconnect with the same ID, if the token matches then **take over** (closeConn the old connection and attach the new one),
  and if it does not match then `ID-TAKEN`.
- Heartbeat: the client sends a HEARTBEAT every 5s; readLoop extends the 60s read timeout on each message received
  (`SetReadDeadline`), so a disconnected client no longer occupies resources.

### 2. Discovery = a server-side in-memory table (replacing MQTT broadcast)

`disc` is `map[collection]map[peerID]lastSeen`. announce is decoupled from the signaling connection
(any HTTP entry point can report); `HandleNodes` uses `heartbeatTTL=90s` as its window to remove
expired nodes. A node with a 30s heartbeat does not disappear from the discovery list even if the signaling WS flaps (reconnecting).

### 3. Resource protection (multi-layer)

| Layer | Limit | Guards against |
|---|---|---|
| WS upgrade | id/token/key required, key must match | Unauthorized connections |
| Read limit | `SetReadLimit(40<<10)` | A malicious oversized signaling payload |
| Read timeout | 60s (refreshed by HEARTBEAT) | A disconnected client occupying resources |
| Offline queue | 100 per dst (drop the oldest) + 30s expiry + sweeper cleanup | A dst that never connects growing the queue without bound → OOM (H3) |
| announce body | `MaxBytesReader` 1KB + a cap of 64 collections (M15) | Unbounded decode |
| Write | `sendMu` + a 10s write timeout | A gorilla concurrent-write panic / a slow client blocking |

## Relationships with other modules

```
cmd/peerdrive (signal subcommand) ──► signalserver
transport/http_discovery.go ──► /discover/announce + /discover/nodes (the discovery client)
transport/peerjs_service.go ──► the peerjs client (PEERDRIVE_PEERJS_HOST/PORT/KEY point
                                 at this server, with zero protocol changes)
test/integration/selfhosted_test.go ──► full-chain verification (signaling + discovery + file fetch)
```

- **Node side**: `PEERDRIVE_PEERJS_HOST=peersignal.moonchan.xyz` +
  `PEERDRIVE_PEERJS_KEY=<key>` (signaling) + `PEERDRIVE_DISCOVER_URL=...` (discovery,
  taking priority over MQTT) —— REFACTOR.md §3.6.
- **Browser side**: the host/port/key configuration points at self-hosted, so signaling is your own with no public cloud MITM surface.
- **Discovery clients**: see `L6-discovery/discovery.md`.
- This module **does not import** any business package (it depends only on gorilla/websocket) and is the
  server half of the ⑥ aspect.

## Protocol alignment details (differences from peerjs-server)

The implementation is aligned with `peers/peerjs-server` (src/services/webSocketServer, messageHandler),
so the client library can switch with zero changes. Key behaviors:

1. **WS URL format**: `/{path}peerjs?key=&id=&token=` (`path` is decided by the deployer;
   peerserver mounts `/peerjs`; the go-peerjs client additionally has a `/{path}/` prefix convention on the path,
   which the server accommodates with `strings.HasSuffix` or an explicit mount point).
2. **The message envelope**: `{type, src, dst, payload}` —— the server **rewrites src** before forwarding by dst;
   payload is a `json.RawMessage` passed through as-is (the server does not parse the SDP/ICE
   content of OFFER/ANSWER/CANDIDATE).
3. **The offline queue**: `LEAVE`/`EXPIRE` are not enqueued (they are only valid while online); a message with an empty
   `dst` is not enqueued.
4. **ID-TAKEN**: on a token mismatch, reply with `ID-TAKEN` and close immediately; on a match, take over the old connection
   (the old connection is closeConn'd, and its resources are then cleaned up by the readLoop defer).
5. **Heartbeat**: a 5s HEARTBEAT from the client; the server uses a 60s read timeout + a 90s discovery heartbeat window ——
   the former manages signaling connection liveness, the latter manages discovery list freshness, and the two are decoupled.
6. **Differences from the public cloud**: no rate limiting / no token allowlist (a token allowlist can be added to the server
   later, reserved in REFACTOR.md §3.6); `CheckOrigin` is always true (in a self-hosted scenario the
   deployer is responsible for the source allowlist).

## Production deployment (cloudcone, state as of 2026-08-17)

```
peersignal.moonchan.xyz ──CF orange-cloud A record──▶ 117.55.237.217 (cloudcone nginx)
         │ wss://peersignal.moonchan.xyz/peerjs (signaling)
         │ https://peersignal.moonchan.xyz/discover/* (discovery API)
    peerserver (systemd, listening on 127.0.0.1:9000, key=pd-signal-1edf5e05e4a52b7351392574)
```

- nginx reverse-proxies `127.0.0.1:9000`, passing the WS upgrade headers through; CF's 100s idle timeout does not affect signaling
  (the 5s heartbeat keeps it alive).
- **DNS decision**: proxied=true (orange cloud) has been verified to work —— CF routes back to the A record IP and reaches
  cloudcone nginx (with your own certificate). Pitfall: before adding the A record, the peersignal orange-cloud path was measured as
  404 (nginx/1.18.0, not cloudcone's 1.22.1) —— the origin target was uncertain at that time, and once the A record
  was established the origin routing was correct.
- **Note**: `cloudcone.moonchan.xyz` is occupied by livekit (livekit.conf →
  127.0.0.1:7880) and cannot be reused, hence a separate subdomain.
- **Proxy note**: cloudcone 443 does **not go through the host proxy** (the proxy times out connecting to cloudcone) ——
  curl/tests connect directly without a proxy; public services like 0.peerjs.com must go through the proxy. The two are distinguished
  by target domain (the "production deployment" section of the project AGENTS.md).
- Deployment update: `GOOS=linux CGO_ENABLED=0 go build -tags nosqlite -o /tmp/peerserver
  ./peerdrive signal` → upload writing to `.new` → `systemctl restart peerserver` (avoiding
  Text file busy).
- Live verification: `PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration"
  ./test/integration/ -run TestLive -v` (run without a proxy).

## Pitfalls and design decisions

| Number | Pitfall | Fix |
|---|---|---|
| H3 | The offline queue had no limit —— when a dst never connected, the queue grew without bound (each malicious client could send OFFERs to any random ID to blow up memory) | `maxQueuedPerDst=100`, dropping the oldest on overflow (signaling messages expire and become invalid, so dropping the old is more reasonable than dropping the new); plus a `Start()` 30s sweeper cleaning expired items and empty queues (the original cleanup only triggered in flushQueue, so when a dst never connected, expired messages accumulated) |
| M15 | Unbounded announce body decode, no cap on the collection count | `http.MaxBytesReader` 1KB + `maxCollectionsPerAnnounce=64` |
| Concurrent write | gorilla/websocket does not allow concurrent WriteJSON —— heartbeat/ICE/multi-goroutine forwarding would panic (triggered by the 3-node interop test) | `client.sendMu` serializes all writes |
| Close while holding the lock | Calling `conn.Close()` inside a lock can deadlock interacting with sendMu | removeClient only collects victims inside the lock, then sends one by one after unlocking (the same class of pitfall as REFACTOR §5) |
| Protocol detail | The answerer generates a new connectionId → ANSWER is not routed | The server only forwards by dst; keeping the connectionId is the client's responsibility (the REFACTOR §5 E2E pitfall) |
| Deployment | cloudcone.moonchan.xyz is occupied by livekit (:7880) | peerserver uses a separate domain peersignal.moonchan.xyz (orange-cloud A record → cloudcone nginx → 127.0.0.1:9000); 443 direct connection without a proxy (the proxy times out connecting to cloudcone) |

## Tests (6 unit tests, the L6 section of `scripts/test-layers.sh`)

> Command: `cd back/signalserver && go test ./...`  (independent go.mod, **do not append `-tags nosqlite`**)

### `signalserver_test.go` (unit tests, all in-memory httptest + gorilla client)

| Test | Background of discovery |
|---|---|
| `TestSignal_OpenAndForward` | Functional test —— register OPEN + messages forwarded by dst; **the server overwriting src is a protocol requirement** (prevents a forged source) |
| `TestSignal_OfflineQueue` | Alignment with peerjs-server behavior —— an OFFER arriving before the target comes online must not be lost; it is resent after it comes online |
| `TestSignal_LeaveBroadcast` | Functional test —— the LEAVE broadcast lets the peer know about the disconnect (alignment with peerjs-server behavior) |
| `TestSignal_IDTaken` | Functional test —— ID occupation protection: a token mismatch is rejected (prevents hijacking someone else's ID) |
| `TestSignal_InvalidKey` | Defensive test —— a key validation failure must reject the connection |
| `TestDiscover_AnnounceAndQuery` | Functional test —— after self-hosting, room discovery merged into the signaling server (replacing MQTT broadcast); coll-a returns only node-1/node-2, and coll-b nodes do not mix in |

### `selfhosted_test.go` (integration, `go test -tags "nosqlite integration"`)

| Test | Background of discovery |
|---|---|
| `TestSelfHostedSignalAndDiscover` | Functional requirement —— after self-hosting, both PeerJS signaling and room discovery are under your own control: start a local signaling server, and B interconnects with A via HTTP discovery only and fetches a file (no external service at all) |
| `TestSelfHostedPeerJSSignal` | Functional requirement —— the node side switches to self-hosting with zero changes (only the host config): connect directly with the peerjs client module to self-hosted and complete WebRTC data plane interconnection |

### `live_test.go` (`PEERDRIVE_LIVE_TEST=1`, direct connection without a proxy)

- `TestLiveSignal_DiscoveryAndInterop` / `TestLiveSignal_ProtocolCompat`:
  background of discovery = deployment verification —— after the self-hosted server (peersignal.moonchan.xyz) came online, the node
  completes signaling + discovery + file fetch with zero changes (only host/key/discover are changed) along with client protocol compatibility.

## File inventory

| File | Responsibility |
|---|---|
| `back/signalserver/signalserver.go` | The signaling + discovery core (independent go.mod) |
| `back/signalserver/signalserver_test.go` | Unit tests (with background-of-discovery annotations) |
| `back/signalserver/cmd/peersignal/main.go` | Standalone entry (**note the name `peersignal`, not `peerserver`**) |
| `back/test/integration/selfhosted_test.go` | The self-hosted full-chain integration tests |
| `back/test/integration/live_test.go` | Production deployment verification (TestLiveSignal_*) |
