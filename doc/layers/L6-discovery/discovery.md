# discovery —— the discovery clients (HTTP / MQTT)

> One-line responsibility: the client half of "how nodes find each other" —— `transport/http_discovery.go`
> (the room discovery API of the self-hosted signaling server) and `transport/mqtt_discovery.go` (sharded room discovery on a
> public
> broker); they only exchange peerIds, and actual transport still goes through PeerJS cloud signaling +
> WebRTC direct connection.

- Layer belonging: AOP ⑥ discovery aspect (`doc/LAYERS.md` §1) —— ⑥'s prohibition "transport business data
  (only exchange peerId/connection information)" is strictly enforced in this module
- Location: the two files live inside the `internal/transport/` package (migrated in REFACTOR.md §3.6), consumed by
  `peerjs_service.go` assembly
- The server half (`signalserver`) is in `L6-discovery/signalserver.md`

---

## Responsibilities

1. **HTTPDiscovery**: the preferred discovery method replacing MQTT —— announce the collections this node
   follows
   (`POST /discover/announce`, 30s heartbeat) + poll for online nodes
   (`GET /discover/nodes?coll=`, 10s period) → the `onPeer` callback for interconnection.
2. **MQTTDiscovery**: public broker sharded room discovery —— topics are sharded by collection hash
   (`peerdrive/v1/{hash}/nodes`), and a node only subscribes to the shards it follows; announce
   is idempotently de-duplicated + a 60s heartbeat; paho auto-reconnect on disconnect + resubscribe.
3. **Assembly decision**: when `PEERDRIVE_DISCOVER_URL` is set, HTTP discovery takes priority, otherwise
   MQTT is enabled when `PEERDRIVE_MQTT_ENABLE` is set (peerjs_service.go:197-210).
4. **Deduplication and defense**: both discovery kinds de-duplicate already-reported peers (avoiding duplicate onPeer); and validate
   payload size, peerId length, and collection hash legality (M15).

## Module inventory (each file: filename + one-line responsibility + key exports)

### `http_discovery.go` —— the HTTP discovery client for the self-hosted signaling server

| Key exports | Description |
|---|---|
| The `HTTPDiscovery` struct | `baseURL` / `peerID` / `collections` / the `onPeer` callback / `client` (10s timeout) / `seen` (dedup table) / ctx |
| `NewHTTPDiscovery(baseURL, peerID, collections, onPeer)` | Creation (with its own `context.WithCancel`) |
| `Start()` / `Stop()` | Start/stop the async loop (cancel to exit) |
| `loop()` | announce once first → dual tickers: 10s poll (discover) + 30s hb (announce) |
| `announce()` | `POST {baseURL}/discover/announce {peerId, collections}`; a failure is only debug |
| `discover()` | per-collection `GET /discover/nodes?coll=` → decode (**LimitReader 256KB**, M15) → filter empty/self/over-long ids → `seen` dedup → `onPeer` |

### `mqtt_discovery.go` —— the public broker sharded room discovery client

| Key exports | Description |
|---|---|
| The `MQTTDiscovery` struct | `broker` / `topicPrefix` / `clientID` / `onPeer` / `announceTick` (60s) / `announce` (idempotent table) / ctx |
| `NewMQTTDiscovery(broker, topicPrefix, clientID, onPeer)` | Creation; default broker `tcp://broker.emqx.io:1883`, prefix `peerdrive/v1`, clientID with a timestamp suffix |
| `nodeTopic(hash)` | The sharded topic: `{prefix}/{hash}/nodes` |
| `Start(collections)` | Connect to the broker (paho: CleanSession + AutoReconnect + ConnectRetry 5s); in `SetOnConnectHandler`, **resubscribe to all collection shards** (paho does not retain old subscriptions) |
| `onMessage` | Parse `{peerId, ts}` → onPeer; **payload ≤64KB, peerId ≤128** (M15) |
| `Announce(peerID, collections)` | Idempotent announce (same peer+collection sent only once) + start the heartbeat loop |
| `loop()` | A 60s ticker resends all announced peer+collections (guards against broker cleanup + notifies late-arriving nodes) |
| `Stop()` | cancel → wait for `done` → Disconnect |

### Assembly point: `peerjs_service.go`

| Location | Behavior |
|---|---|
| peerjs_service.go:198-202 | `DiscoverURL != ""` → `NewHTTPDiscovery(...)` + `Start()` |
| peerjs_service.go:203-209 | otherwise `MQTTEnable` → `NewMQTTDiscovery(...)` + `Start(cols)` + `Announce(id, cols)` |
| `onDiscoveredPeer` | The discovery callback → skip if already connected, otherwise `connectLoop` auto-interconnects (deduplication is guaranteed by the caller) |

## Key mechanisms

### 1. The discovery protocol (HTTP)

```
node comes online ──POST /discover/announce {peerId, collections}──▶ server
   │  30s heartbeat renewal (the server-side 90s expiry window)
   ▼  10s polling
GET /discover/nodes?coll={hash} ◀── {nodes:[{peerId,lastSeen}]}
   │  filter: empty id / itself / >128 chars; seen dedup
   ▼
onPeer(peerID) → PeerJSService.connectLoop (WebRTC direct connection)
```

announce is decoupled from the signaling connection —— even if the WS signaling flaps, HTTP discovery still maintains the online status.
Polling and heartbeat each use independent tickers, and the server-side TTL is more generous than the heartbeat interval (90s vs 30s) to tolerate
packet loss.

### 2. The discovery protocol (MQTT)

```
broker topic: peerdrive/v1/{collectionHash}/nodes
node A ──Publish {peerId, ts} (announce, idempotent)──▶ broker
node B ◀─Subscribe (same shard)── onMessage → onPeer(A)
60s heartbeat: loop() resends all announced keys (the idempotent table only records keys, so resending is side-effect free)
disconnect: paho SetAutoReconnect + SetOnConnectHandler resubscribe (paho does not retain old subscriptions)
```

The sharding design motivation: a public broker has no scale ceiling only if **subscription counts and message volume are spread across collections** ——
the fan-out of a single global topic is the bottleneck; after sharding by collection hash, each shard only serves nodes
following that collection (comment at mqtt_discovery.go:17-23).

### 3. Priority and mutual exclusion

`DiscoverURL` takes priority over `MQTTEnable` (an if/else if structure), and each is assembled only once
(guards `s.httpDisc == nil` / `s.discovery == nil`, preventing the signaling reconnect loop from repeatedly starting
the discovery component).

### 4. HTTP discovery vs MQTT discovery (a selection comparison)

| Dimension | HTTPDiscovery | MQTTDiscovery |
|---|---|---|
| Server | A self-hosted signalserver (you run it, no third-party dependency) | A public broker (broker.emqx.io, free but an external dependency) |
| Discovery method | 10s polling query (pull) | Subscribed sharded topic real-time push (push; late-arriving nodes are backstopped by heartbeat resends) |
| Timing window | server 90s heartbeat expiry vs client 30s heartbeat | 60s heartbeat; no expiry window (messages take effect immediately) |
| Privacy surface | collection hashes go only to your own server | collection hashes go to the public broker, where anyone can subscribe and observe |
| Configuration | `PEERDRIVE_DISCOVER_URL` | `PEERDRIVE_MQTT_ENABLE/BROKER/COLLECTIONS` |

History: MQTT was the first discovery implementation (REFACTOR.md §3.4, 2026-08-13, sharded mode design);
after the self-hosted signaling landed (§3.6), discovery merged into the server (which naturally knows all online nodes),
and HTTP became the preferred choice —— **use MQTT in the public cloud (0.peerjs.com + broker.emqx.io) zero-trust scenario,
and HTTP in the own-server scenario**.

### 5. Collection source: collectionHashes()

Both discovery kinds take `s.collectionHashes()` (peerjs_service.go) as their collection list input ——
the node config `PEERDRIVE_MQTT_COLLECTIONS` (comma-separated 64hex). Hash legality filtering
(`IsStrictSHA256`) happens before subscribe/announce: illegal values are not subscribed to and not published,
preventing topic injection (such as `../` or an over-long string polluting the shard namespace).

## Relationships with other modules

```
transport/peerjs_service.go (assembly/consumption)
    ├─► HTTPDiscovery ──► signalserver (/discover/*, the server half)
    └─► MQTTDiscovery ──► public broker (broker.emqx.io)
test/integration/mqtt_test.go (public broker integration, requires outernet + proxy)
test/integration/selfhosted_test.go (self-hosted HTTP discovery end-to-end)
internal/config/config.go (PEERDRIVE_DISCOVER_URL / PEERDRIVE_MQTT_* configuration)
```

- Both discovery kinds produce the same thing: a `peerID` string callback. The upper layer (PeerJSService) is unaware of
  the discovery method, matching ⑥'s design of "decoupling discovery from transport" —— switching discovery methods does not touch the interconnection layer.
- Configuration items (config.go:44-48,126-130): `PEERDRIVE_MQTT_ENABLE` (default false),
  `PEERDRIVE_MQTT_BROKER` (default tcp://broker.emqx.io:1883),
  `PEERDRIVE_MQTT_TOPIC_PREFIX` (default peerdrive/v1),
  `PEERDRIVE_MQTT_COLLECTIONS` (comma-separated), `PEERDRIVE_DISCOVER_URL` (when set,
  takes priority over MQTT).
- Collection hash legality filtering uses `hashutil.IsStrictSHA256` (strict lowercase 64 hex, a transport-layer utility moved
  from service into pkg during the M3 layer collapse) —— illegal values are not subscribed to / not announced.

## Pitfalls and design decisions

| Number | Pitfall | Fix |
|---|---|---|
| M15 | A public broker / a compromised discovery server can return an arbitrary payload —— an abnormally large JSON / over-long id would blow up memory or pollute the interconnection state | HTTP: decode with `LimitReader(256KB)`; MQTT: payload ≤64KB, peerID ≤128; both filter empty id/own id |
| Idempotency | Repeated announces would flood the public broker | An `announce map[string]bool` idempotent table: same peer+collection sends the online announce only once; the heartbeat loop resends |
| Resubscribe | paho's reconnect after disconnect does not retain old subscriptions | Re-Subscribe all collection shards inside `SetOnConnectHandler` |
| Integration parallelism | Multiple test groups running in parallel on the public signaling/broker interfere with each other (discovery: with default parallelism, ThreeNodes/MQTT fail intermittently) | Integration tests must run serially with `-p 1` (REFACTOR.md §5.1) |
| Priority | Enabling both discovery kinds would double the onPeer calls | `DiscoverURL` takes priority (if/else if), and `== nil` guards prevent the reconnect loop from assembling twice |
| No announce boundary | MQTT's `Announce` and `Start` are separate —— the caller must Start first then Announce | See the assembly point (Announce immediately after Start) |

## Tests

### Unit tests

The two client files in this layer have **no independent unit tests** (they depend on a real broker/server); unit-level coverage comes through
the indirect path in `peerjs_service_test.go` and the signalserver-side tests
(`TestDiscover_AnnounceAndQuery`); the complete behavior of the discovery clients is backstopped by integration tests.

### Integration tests (`test/integration/`, `go test -tags "nosqlite integration" -p 1`)

| Test | Background of discovery |
|---|---|
| `TestMQTTDiscovery` (mqtt_test.go:16) | Functional test —— MQTT sharded rooms discover each other (announce/subscribe/onPeer callback/heartbeat idempotency), A receives B and B receives A, with the 60s heartbeat backstopping timing |
| `TestMQTTDiscoverThenPeerJSInterop` (mqtt_test.go:59) | Functional test —— the full chain of MQTT discovery → PeerJS interconnection → file fetch: B knows nothing about A's peer id, and only via sharded discovery does it fetch directly over the public cloud signaling |
| `TestSelfHostedSignalAndDiscover` (selfhosted_test.go:37) | Functional requirement —— after self-hosting, both PeerJS signaling and room discovery are under your own control: start a local signal server, and B interconnects with A and fetches a file via HTTP discovery only (no external service at all) |

> Both MQTT tests require outernet + proxy (broker.emqx.io); the self-hosted test has no external dependency.

### Running

```bash
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v
# ⚠️ must run serially with -p 1 (parallel on a public broker interferes with itself)
```

## The whole discovery chain (the complete journey of "nodes finding each other")

Taking self-hosting (HTTP discovery) as an example:

```
[node A]                              [signalserver]                    [node B]
   │ 1. start: PEERJS_HOST points at self-hosted        │                              │
   │ 2. WS register {key,id,token} ─────────────────▶│ reply OPEN, added to clients             │
   │ 3. HTTPDiscovery.Start()                         │                              │
   │ 4. POST /discover/announce {peerId:A,            │ disc[coll][A]=now               │
   │    collections:[...]} ────────────────────▶│                              │
   │ 5. 30s heartbeat renewal ─────────────────▶│                              │
   │                                       │ 6. B announces the same way (same collection)    │
   │ 7. every 10s GET /discover/nodes?coll= ◀─│ {nodes:[{peerId:B,...}]}       │
   │ 8. onPeer(B) → connectLoop(B)              │                              │
   │ 9. PeerJS OFFER ───────────────────────▶│ ──forward──▶ B                  │
   │ 10. WebRTC DataChannel direct connection established        │                              │
   │ 11. frame protocol (req/meta/data/done) fetches a file  │                              │
```

Key point: steps 3-8 only exchange peerIds (inside ⑥'s forbidden zone, no business data is sent); from step 9 onward, signaling
forwarding and the step 11 data plane no longer depend on the discovery component —— **discovery is only responsible for "the first meeting";
interconnection and transport are taken over entirely by the peerjs layer**.

## File inventory

| File | Responsibility |
|---|---|
| `back/internal/transport/http_discovery.go` | The HTTP discovery client (announce + 10s polling + 30s heartbeat) |
| `back/internal/transport/mqtt_discovery.go` | The MQTT sharded discovery client (idempotent announce + 60s heartbeat + resubscribe) |
| `back/internal/transport/peerjs_service.go` | The assembly point (DiscoverURL first, onDiscoveredPeer → connectLoop) |
| `back/internal/config/config.go` | The discovery configuration (PEERDRIVE_DISCOVER_URL / PEERDRIVE_MQTT_*) |
| `back/test/integration/mqtt_test.go` | MQTT discovery integration tests (2) |
| `back/test/integration/selfhosted_test.go` | Self-hosted HTTP discovery integration test |
