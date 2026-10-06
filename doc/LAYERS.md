# Architecture layering and membership guide (AOP aspects)

> 2026-08-18 · Decision document for "which layer to put new code in". One-line criterion:
> **mechanism → peerjs; protocol semantics → transport; business → controller/service;
> management convergence → admin aspect**.
> Relationship to other docs in this project: REFACTOR.md is a refactoring record
> (pitfalls/decisions); this doc is "layer membership rules".

---

## 1. Layer overview (8 aspects)

```
┌─────────────────────────────────────────────────────┐
│  ① Signaling/transport primitives layer  back/peerjs (standalone go.mod) │
│     PeerJS signaling, WS/WebRTC connections, flow control, frame sending │
│     ← zero business knowledge, independently reusable │
│       (github.com/Hana-ame/go-peerjs, main go.mod replace reference) │
├─────────────────────────────────────────────────────┤
│  ② Frame protocol layer  internal/transport         │
│     Session state machine, verb dispatch, req fetch, upload, port forwarding │
│     ← knows "frames/verbs", not "business semantics" │
├─────────────────────────────────────────────────────┤
│  ③ Management aspect  transport/admin.go + router assembly │
│     Local sessions only → internally forwards to gin engine │
│     ← cross-cutting: converges management operations, reuses all controllers │
├─────────────────────────────────────────────────────┤
│  ④ Business core  controller → service → repository │
│     HTTP semantic business (files/collections/auth/BT/IPFS/sync) │
│     ← unaware whether being forwarded via WS frames or called directly over HTTP │
├─────────────────────────────────────────────────────┤
│  ⑤ Data aspect  repository (SQLite file_index etc.) │
├─────────────────────────────────────────────────────┤
│  ⑥ Discovery aspect  mqtt_discovery / http_discovery / │
│     signalserver (self-hosted signaling cmd/peerserver) │
├─────────────────────────────────────────────────────┤
│  ⑦ External capability aspect  p2p_bt (go-peerdrive-bt standalone library) │
├─────────────────────────────────────────────────────┤
│  ⑧ Frontend aspect  front/src/ws.js (WS client)     │
│     api.js (business semantics wrapper) │
└─────────────────────────────────────────────────────┘
```

## 2. Dependency direction (unidirectional, acyclic, don't break)

```
peerjs ← transport ← router ← controller ← service ← repository
   ↑                        ↑
   └──── admin internal forwarding ────┘    (③ cross-cuts ②④, direction unidirectional:
                                              transport→router→controller)

⑥ Discovery  ←  consumed by transport (PeerJSService assembly)
⑦ External  ←  consumed by service/controller
⑧ Frontend  ←  independent process, only communicates with ② WS sessions + ③ admin verb
```

**Rule**: higher layers can depend on lower layers; lower layers never depend on higher
ones. If new code needs a "reverse reference" (e.g. transport importing controller), it
is in the wrong layer.

## 3. Decision criteria (three questions to set the layer)

Answer three questions before adding a new feature:

| Question | Answer → Membership |
|---|---|
| Does it change "**how it's transmitted**" (channel/buffer/flow control/frame-sending primitives)? | → **① peerjs** |
| Does it define "**what's transmitted**" (new verb, frame format, connection state machine)? | → **② transport** |
| Does it implement "**why transmit**" (specific capability, business rules)? | → **④ controller/service** |
| Is it "**management operation convergence**" (local management of this node, not a data stream)? | → **③ admin aspect** |

Typical follow-up: **Does this feature require changes to the frame protocol?**
- Yes → it belongs to ② transport (frame protocol is transport layer's private matter)
- No, only buffer/channel → ① peerjs
- Pure rules/data organization → ④

## 4. Placement decision table (common feature landing points)

| Feature | Membership layer | Landing (file/pattern) |
|---|---|---|
| Inter-node file transfer (req fetch) | ② transport | Existing: inbound.go (responding) + outbound.go (initiating) + conn.go (dispatch) |
| New data verb (push/peer upload/resume) | ② transport | Add case in conn.go dispatch; inbound/outbound each implement their role |
| Chunking/resume (protocol-level) | ② transport | Extend existing verb semantics, keep the "header+binary blocks atomically continuous" constraint |
| Underlying flow control/buffering improvement | ① peerjs | connection.go's SendFrame (already has built-in low-watermark flow control) |
| New transport channel (QUIC/direct UDP replacing WebRTC) | ① peerjs | transport.go's DataChannel interface —— new implementation only swaps the lower layer, upper layers zero change |
| BT data transferred via peerjs | ④ + ② | service organizes business, goes through ②'s FetchFromPeer/requestFile semantic API for data plane |
| Management-initiated transfer ("push to a node") | ③ entry + ② execution | admin verb only converges management operations; actual data still goes via ②'s verb (admin has 64MB limit, doesn't carry sustained data streams) |
| New discovery method | ⑥ | Implement Discovery interface, PeerJSService assembly |
| New frontend management UI | ⑧ | api.js wrapper → ws.js's admin()/upload()/download() |
| Auth/permission refinement (roles) | ③ + ④ | Add session-level verification in admin aspect; business rules in ④ |

## 5. Absolute prohibitions per layer (violating = wrong layer)

| Layer | Forbidden |
|---|---|
| ① peerjs | Knowing any business verb, importing internal/*, containing business terms like "file/collection/BT" |
| ② transport | Importing controller/service/repository; implementing specific business rules |
| ③ admin | Carrying sustained binary data streams (>64MB directly rejected; large files go via ② req verb) |
| ④ Business core | Directly operating WebSocket/peerjs connections (only via ②'s semantic API) |
| ⑥ Discovery | Transmitting business data (only exchanges peerId/connection info) |
| Frontend | Directly fetching local HTTP endpoints (except /ws/peer upgrade; LEGACY routes for old clients/curl only) |

## 6. Existing code boundary checks (2026-08-18 status)

- **admin.go** (③) internally constructs *http.Request → gin engine ServeHTTP, reusing
  all controllers —— this is "convergence" not "business", consistent with ③'s definition
- **conn.go** (②) serveAdmin rejects non-local connections by session ID —— permission
  decision at the protocol layer entry, business layer unaware, correct
- **peerjs_service.go** (② assembly) holds peerjs.Peer + connection management —— no
  business logic, correct
- **controller dual entry** (HTTP direct call + admin internal forwarding) is by design
  (zero duplication), not a violation: business layer only recognizes *http.Request,
  unaware of the source
- Only boundary to note: **port forwarding (forward.go)** semantically leans toward
  "mechanism", but protocol-level verbs (fwd-open/challenge/...) in ② are reasonable
  —— its verb semantics belong to transport; if the underlying tunnel implementation
  needs to be independent it can be extracted above peerjs, below transport

## 7. Recommended change flow

When adding a new capability, modify code in this order:

1. Three questions to set the layer (§3) → determine landing point
2. If landing in ②: first check frame protocol constraints in conn.go's header comment
   (atomic header+blocks, expect state machine, request timeout/cancel semantics), then
   write dispatch + role implementations
3. If landing in ①: only touch peerjs module (standalone go.mod), run
   `cd back/peerjs && go test ./... -race`
4. If involving management plane: admin only does entry forwarding, data streams still
   go through ②
5. Tests and docs: corresponding layer unit tests + REFACTOR.md records (pitfalls/
   decisions); if this table has new feature types, add them here too

## 8. Maintainer-perspective module grouping (2026-08-19 doc grouping)

> This grouping does not change code structure and does not change §1–§7 layering rules;
> it is only for quick locating during daily discussions, repository indexing, and PR
> classification. It is "two views of the same system" relative to the 8 aspects in §1.

| Group | Main code scope | Corresponding §1 layers | Responsibility summary |
|---|---|---|---|
| **File Source module** | `internal/source`, `internal/downloader`, `internal/transport/file_index.go`; external protocol sources: `internal/provider/ipfs.go`, `back/p2p_bt`, download/pin branches in `internal/controller`, `front/src/pages/IPFS.jsx`, `front/src/pages/BT.jsx`（BT 与 DHT 浏览已合为同一页） | ② + ⑤ + ⑦ | Where files come from / go to: local, Peer, URL, IPFS, BT, unified manager, download, index, upload sessions |
| **Control module** | `internal/controller`, `internal/service`, `internal/repository`, `internal/model` | ④ + ⑤ | Business control, service orchestration, persistence, model definitions; unaware whether being forwarded via WS frames |
| **Peer module** | PeerJS core of `internal/transport`: `peerjs_service.go`, `conn.go`, `inbound.go`, `outbound.go`, `file_index.go` | ② | Node identity, frame protocol, inbound/outbound request semantics, file index sync |
| **Network connection module** | Connection carriers of `internal/transport`: `ws_session.go`, `rtc_session.go`, `http_discovery.go`, `mqtt_discovery.go`, `forward.go`; `back/peerjs`, `back/signalserver` | ① + ⑥ | Low-level connections, signaling, discovery, port forwarding tunnels |
| **Routing (core)** | Assembly relationships of `internal/router`, `internal/transport/admin.go` | ③ + routing essence | Combining all modules: HTTP routes, collection/file dispatch, admin internal forwarding, source routing assembly |

Boundary notes:

- `file_index.go` is physically in `internal/transport`, but semantically leans toward
  File Source/storage index; doc grouping places it in Source, code location not yet
  migrated.
- IPFS/BT current code is scattered across external capability packages like `provider`/
  `downloader`/`p2p_bt`, and has not yet directly implemented `internal/source.Source`
  interface; but semantically they are all Sources of "fetching files from external
  networks", so the doc groups them by semantics into the Source group.
- `admin.go` is also physically in `internal/transport`, but it's the cross-cutting
  aspect "management operations converge through local WS to gin/controller"; doc
  grouping places it in routing/assembly.
- The boundary between `Peer` and `Network connection` is: Peer is about "protocol and
  semantics", network connection is about "low-level connections and signaling".
