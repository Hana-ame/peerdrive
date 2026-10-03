# Module 11: signalserver Signaling and Discovery Service

- **Code location**: `back/signalserver` (independent go.mod, module `github.com/Hana-ame/go-peersignal`, see `back/signalserver/go.mod:1`; main repository requires v0.0.0 at `back/go.mod:35` and replaces with `replace github.com/Hana-ame/go-peerserver => ./signalserver` pointing to local directory at `back/go.mod:164`)
- **One-line function**: Self-hosted PeerJS signaling server (node registration, OFFER/ANSWER/CANDIDATE forwarding by dst, offline queuing, heartbeat keepalive, ID assignment) + built-in room discovery (node announce of watched collections, HTTP query online nodes), replacing public signaling (0.peerjs.com) and public MQTT broker; **pure in-memory, does not persist any data**.
- **Dependencies**: Only `github.com/gorilla/websocket v1.5.3` (WS upgrade and read/write, `back/signalserver/go.mod:5`); standard library `crypto/rand` (random id, `back/signalserver/signalserver.go:19,790-798`), `embed` (embedded dashboard.html, `back/signalserver/signalserver.go:20,32-33`), `encoding/json`, `net/http`, `sync`/`sync/atomic`, `time`; tests use `stretchr/testify` (`back/signalserver/go.mod:7`). Does not depend on gin / sqlite / database.
- **Depended upon by**:
  - Independent binary `cmd/peersignal` (`back/signalserver/cmd/peersignal/main.go:22-52` assembles Server + route registration, README deployment instructions see `back/signalserver/README.md:10-22`);
  - Main repository integration tests use httptest to embed this package as "in-memory signaling" (`back/test/integration/integration_test.go:32,43`, `back/test/integration/file_lifecycle_test.go:19,33`) — main repository production code **does not** import this package (only these two locations across entire repo);
  - Node-side `transport.HTTPDiscovery` consumes `/discover/announce|nodes` (`back/internal/transport/http_discovery.go:84-100,103-117,136-170`);
  - Browser-side peerjs client directly connects to `/peerjs` WS and `/peerjs/id` (details in 12-frontend-signalserver).

## 1. Logic

**Module positioning**: Package comment explicitly states "self-hosted PeerJS signaling server (compatible with peerjs-server protocol subset) + built-in room discovery (replaces public signaling + public MQTT broker)", responsibilities split into signaling and discovery (`back/signalserver/signalserver.go:1-16`). Protocol aligns with peers/peerjs-server's `webSocketServer` / `messageHandler`: WS URL format `/{path}peerjs?key=&id=&token=`; message `{type, src, dst, payload}` and **server overwrites src**; dst online forwards, offline queues (LEAVE/EXPIRE not queued); OPEN/ID-TAKEN/ERROR control messages; client sends HEARTBEAT every 5s (`back/signalserver/signalserver.go:10-15`).

**Core types**:

- `Server`: Container for all state — `key/path/queueTTL/heartbeatTTL/tokenWhitelist` config items + `startedAt` (startup time), `msgCount` (atomic forward count) + six `mu sync.Mutex` protected memory tables (`clients/queues/disc/peerLinks/peerColls/peerStats`) (`back/signalserver/signalserver.go:35-52`).
- `client`: One online signaling connection — `id/token/conn`, `sendMu` (gorilla doesn't allow concurrent writes, serialized writes), `last` (last heartbeat) (`back/signalserver/signalserver.go:162-169`).
- `queuedMsg`: Offline queue entry — `msg Message` + `expire time.Time` (with expiry time on enqueue) (`back/signalserver/signalserver.go:171-175`).
- `Message`: `{Type,Src,Dst,Payload}` consistent with peerjs client protocol, Payload is arbitrary JSON (`back/signalserver/signalserver.go:177-183`).
- `NodeInfo` / `GraphLink` / `PeerStats`: Discovery API response and internal statistics structures (`back/signalserver/signalserver.go:561-670`).
- `Option`: Functional config item, only implementation `WithTokenWhitelist` (takes effect only when tokens non-empty, `back/signalserver/signalserver.go:54-75`).

**Signaling main flow** (`HandleWS`, `back/signalserver/signalserver.go:246-296`):

1. Parameter validation: `id/token/key` any empty → HTTP 400; `key != s.key` → 400 "Invalid key provided"; token whitelist non-empty and token not in list → 400 "Invalid token provided" (empty whitelist = no restriction, default, `signalserver.go:249-263`).
2. Upgrade WS (`CheckOrigin` always allows — self-hosted, access control configured by caller, `signalserver.go:265-271`); `SetReadLimit(40<<10)` (signaling messages are small, 40KB enough, prevents oversized payload) and set 60s read timeout (`signalserver.go:272-275`).
3. ID occupation: If same id already has connection with matching token → close old connection and take over; token mismatch → return `ID-TAKEN` and close (`signalserver.go:277-291`; behavior test `signalserver_test.go:122-136`).
4. Create `client` and add to table, return `OPEN`, `flushQueue` to resend offline messages, start `readLoop` (`signalserver.go:292-295`).
5. `readLoop`: Read JSON → **`m.Src = cl.id` overwrite src** → update `cl.last` (message received = active) → extend 60s read timeout → `route(m)`; read error means cleanup (`signalserver.go:299-317`).
6. `route`: dst online → **unlock before write** (`send` has internal 10s write timeout, holding lock would deadlock entire routing) → success then `msgCount++`; write failure goes to `handleDeadDst`; dst offline and not `LEAVE/EXPIRE`, dst non-empty → queue (`signalserver.go:319-356`).
7. Dead connection handling `handleDeadDst`: Send failed but still in table → remove table entry, clean up residuals in disc/peerLinks/peerStats/peerColls for that id, close connection, broadcast `LEAVE` to other online nodes and message originator (fix: old implementation `_ = dst.send(m)` silently dropped, half-open connections would swallow OFFER/ANSWER/CANDIDATE causing initiator to permanently stall in handshake, `signalserver.go:319-328,360-402`).
8. Disconnect cleanup `removeClient` (readLoop defer): Remove from `clients` → clean up all discovery residuals → broadcast `LEAVE` to other nodes (`signalserver.go:418-447`; behavior test `signalserver_test.go:138-163`).

**Heartbeat and TTL cleanup** (`signalserver.go:449-475`): Background goroutine every 10s checks `clients` — if `time.Since(cl.last) > heartbeatTTL` (default 60s), triggers `removeClient`. `queues` expired entries are also cleaned (`signalserver.go:477-493`).

**Discovery (room announcement)** (`signalserver.go:500-560`):

- `POST /discover/announce`: Node announces watched collections `{id, collections: []string, share_scope?}` → stored in `disc[id]` and `peerColls[coll] += id` (`signalserver.go:510-535`).
- `GET /discover/nodes`: Query online nodes; optional `?collection=<coll>` filter to find nodes with that collection (`signalserver.go:537-555`). Response includes `NodeInfo{ID, Collections, ShareScope, LastSeen}`.
- `GET /discover/graph`: Returns peer topology `{links: [{src, dst}]}` — connection edges collected from OFFER routing (`signalserver.go:561-620`).
- `GET /discover/stats`: Returns global statistics `{msgCount, clientCount, queueCount, discCount}` (`signalserver.go:622-670`).
- `GET /discover/rooms`: Lists all watched collections and their subscriber counts (`signalserver.go:672-700`).

**ID allocation** (`signalserver.go:700-740`): `GET /id` (or `/peerjs/id`) allocates a random 10-char alphanumeric ID (base62, `signalserver.go:790-798`). Optional `?version=` parameter for client version tracking.

**Dashboard** (`signalserver.go:32-33,750-790`): Embedded `dashboard.html` served at `/` — a simple monitoring panel showing online clients, message count, discovery rooms. Read-only, no authentication.

## 2. How It Stores

**No persistence whatsoever — pure in-memory state**:

| Aspect | Details |
|--------|---------|
| Client connections | In-memory `clients map[string]*client` |
| Message queue | In-memory `queues map[string][]queuedMsg` |
| Discovery data | In-memory `disc/peerLinks/peerColls/peerStats` maps |
| Message count | In-memory atomic counter `msgCount` |
| Lifecycle | All state lost on process restart |

**Memory tables** (all protected by `mu sync.Mutex`):

| Table | Key | Value | Purpose |
|-------|-----|-------|---------|
| `clients` | peer ID | `*client` (conn, token, last heartbeat) | Online connection registry |
| `queues` | peer ID | `[]queuedMsg` (message + expiry) | Offline message queue |
| `disc` | peer ID | `DiscoveryInfo` (collections, share_scope) | Room announcements |
| `peerLinks` | pair (src,dst) | timestamp | Connection edge tracking |
| `peerColls` | collection name | set of peer IDs | Collection-to-node mapping |
| `peerStats` | peer ID | `PeerStats` (messages, uptime) | Per-peer statistics |

## 3. When It Stores

**Never.** All data is transient:

| Timing | Action | Notes |
|--------|--------|-------|
| Client connects | Add to `clients`, flush `queues`, return `OPEN` | In-memory |
| Message received | Overwrite src, update heartbeat, route to dst | In-memory |
| Message to offline dst | Queue with expiry | In-memory queue |
| Heartbeat expiry | Remove client, broadcast LEAVE, clean up discovery | In-memory cleanup |
| Node announces | Update `disc` and `peerColls` | In-memory |
| Node disconnects | Remove from `clients`, clean up discovery, broadcast LEAVE | In-memory cleanup |
| Process restart | All state lost | No persistence |

## 4. What It Stores

**Nothing persistent.** In-memory only:

- Online client connections (peer ID, token, WebSocket connection, last heartbeat)
- Offline message queues (messages with expiry, waiting for dst to reconnect)
- Room discovery data (node → collections mapping, collection → node mapping)
- Connection topology (peer-to-peer edges from OFFER routing)
- Per-peer statistics (message count, uptime)
- Global message counter

## 5. Boundaries and Pitfalls

- **No persistence**: All state is lost on restart. This is intentional — signaling is transient by nature. Nodes must re-announce after server restart.
- **Queue TTL**: Offline messages expire after `queueTTL` (default 5 minutes). Long-disconnected nodes miss queued messages.
- **Heartbeat TTL**: Clients must send heartbeats every 5s; if no heartbeat for 60s, the connection is considered dead and removed.
- **Token whitelist is optional**: When empty, any token is accepted. When non-empty, only listed tokens are accepted. This is for basic access control, not security.
- **No authentication**: The only "auth" is the key check and optional token whitelist. There is no real authentication or authorization.
- **Single-threaded routing**: `mu sync.Mutex` protects all memory tables. Heavy message traffic could cause lock contention.
- **CORS is open**: All REST endpoints have CORS fully open (allows cross-origin from any origin). This is because browser panels connect from different origins.
- **Dashboard is unauthenticated**: The embedded dashboard at `/` has no authentication. It shows monitoring data that could be sensitive.
- **ID allocation is random**: `GET /id` generates random IDs. If a generated ID is already taken, a new one is generated (up to 10 attempts).

## 6. External Connections

- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md): peerjs library as signaling client communicates with **this service** bidirectionally (OFFER/ANSWER/CANDIDATE/LEAVE/HEARTBEAT bidirectional flow; server overwriting src is protocol requirement, `signalserver.go:12,309`); this service only forwards SDP/ICE, does not touch data plane.
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Browser panel borrows temporary id via `GET /peerjs/id`, connects to this service's WS with same PeerJS protocol and dials by peer id; also does room discovery via `GET /discover/nodes` (`packages/peerdrive-client`). Since it's on different origin, all REST endpoints have CORS open (§5 CORS).
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md): `back/cmd/media-node` (via module 10's peerjs library) registers to this service to carry browser media DataChannel — this service is its signaling channel, not involved in frame protocol.
- Related note: [frontend ↔ backend](../connections/01-frontend-backend.md) (local WS sessions) is **not directly related** to this module — local sessions go through router's WSSession, not this signaling; this module only serves peer-to-peer and browser (panel→node) signaling/discovery.
- Cross-reference within module: [01-config](01-config.md) — `PEERDRIVE_PEERJS_HOST/PORT/KEY/DISCOVER_URL` etc. determine the default instance of this service that nodes point to (`back/internal/config/config.go:54-58,76`); [10-peerjs](10-peerjs.md) — signaling client-side protocol and default public instance `peersignal.moonchan.xyz` (this service's online deployment, `back/signalserver/README.md:68`).
