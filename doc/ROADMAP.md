# Peerdrive Development Roadmap (User-specified Order)

> Source: **Strict priority order** explicitly specified by the user on 2026-09-20. This file is the basis for scheduling and trade-offs.
> Note: distinguish from `doc/archive/report/ROADMAP.md` — that one is the v3.0 historical roadmap (already implemented); this file is the current schedule.
>
> **Narrative guardrail**: Phase 7 identity is **not implemented** (see the table below). Until it lands, product-facing docs
> (`README.md`, `doc/NETDISK.md`, `doc/PROJECT-VISION.md`) must describe ownership as the node `peerId`, not accounts —
> and must not present accounts/JWT/user↔node directory/statistics as an available capability. Shared positioning and the
> rules for keeping the four docs consistent: `doc/PROJECT-VISION.md` §9.

## Order

| # | Phase | Summary | Status |
|---|------|--------|------|
| 1 | **PeerJS-based Interconnect** | Nodes can discover each other and establish WebRTC direct connections | 🟢 Mostly ready (added node-level presence room + user-facing node directory/join) |
| 2 | **Files** | Content-addressed storage + file index + file transfer primitives | 🟢 Mostly ready |
| 3 | **Combine** | Interconnect × Files = cross-node file fetch/serve | 🟢 Mostly ready |
| 4 | **Management Chain** | Management plane (control channel): issuing commands to nodes/files | 🟡 Partially ready |
| 5 | **File Scope Management** | Which files/directories are under management and at what scope they're visible externally | 🟢 Ready on this node side (cross-node restricted content still pending Phase 7) |
| 6 | **Upload/Download/Save** | Complete closed loop for upload / download / persistent save | 🟢 Mostly ready (cross-node save + progress/cancel + no-node consumer) |
| 7 | **Identity Management** | Accounts, authentication, authorization (who is who, what can who do) | 🔴 Design ready, not implemented |

Do the above first. Don't skip levels if lower layers aren't stable — for example, "File Scope Management" depends on "Management Chain" to distribute scopes,
and "Identity Management" depends on earlier phases to get "ownership" working with node identity first.

## Hard Constraint: No Account Dependencies Before Phase 7

"Identity management last" does not mean "write the code last"; rather, **the first 6 phases must not treat accounts as a prerequisite on the path**:

- Interconnect, files, and management chain all operate at the **node level** (peerId); full functionality without querying regserver;
- When an "ownership" concept is needed, use peerId first; Phase 7 will layer on the `account ↔ peerId` mapping;
- Current manifestation: the "restricted to specified permissions" tier for anonymous collections is currently only usable within the same node
  (remote sync requests don't yet carry requester identity); cross-node sharing of restricted collections waits for Phase 7
  — see `doc/REFACTOR.md` §3.16 known limitations and `doc/modules/auth/API-DESIGN.md` §6.

---

## 1. PeerJS-based Interconnect

**Goal**: Two nodes can discover each other and establish direct connections without any manual configuration;
connections self-heal after disconnection; connection count has a cap; state is observable.

**Ready**
- Signaling: `back/peerjs` (standalone module, PeerJS protocol client) + `back/signalserver` (self-hosted signaling, with built-in discovery API);
- Connection establishment: `connectLoop` in `transport/peerjs_service.go` (dial + exponential backoff reconnect + 30s connection timeout)
  and `onIncomingConnection` (passive accept); `bindConn` bidirectional dial dedup (UUID lexicographic order, both ends keep the same physical connection);
- Signaling disconnect self-heal: `Signaller.Done()` → `startLoop` full-round reconnect;
- Discovery (HTTP, self-hosted): `transport/http_discovery.go` announce (30s heartbeat) + 10s polling.

**Fixed in this phase (2026-09-20)**
- **Node-level interconnect missing** (blocking): discovery is **content-sharded** — only announces/queries collection hash rooms it cares about.
  Default config `PEERDRIVE_MQTT_COLLECTIONS` is empty → neither announces nor queries any room, **discovery is completely idle**,
  two default-config nodes can never see each other (only static `PEERDRIVE_PEERJS_PEERS` can interconnect).
  Fix: HTTP discovery additionally joins a fixed "presence room"
  `transport.PresenceRoom` (= `sha256("peerdrive/presence/v1")`,
  using a sha256 literal rather than a readable name is for compatibility with signaling implementations that may enforce 64hex validation),
  toggle `PEERDRIVE_DISCOVER_PRESENCE` (default on).
- **Dial budget**: the presence room enables "any node discovers any node"; without limits it degrades to O(n²) full mesh;
  use the previously idle `PEERDRIVE_MAX_PEERS` as the cap (static PEERS is unlimited, that's explicitly declared by the operator).

**Gaps (TODO)**
- Weak connection state observability: only `GET /peerjs/node` (id + peers list) and the signaling server's dashboard;
  the node side lacks "interconnect health" (per-peer RTT / last frame received time / reconnect count).
- Discovery results don't distinguish node capabilities: `announce` already has `nodeType`/`loadInfo` fields but the node sends the constant
  `"go-persistent"`, so filtering by capability is impossible. **Partial improvement**: `loadInfo.shares` now includes shared **counts**
  (collections/files/directories each, see Phase 5), marketplace cards can display a capability summary.
- `CollectionHashes()` only reads config, not locally stored collections (comment once claimed "config + local storage") —
  ✅ **Definitively resolved (Phase 5)**: no longer need to broadcast local collection hashes. Changed to: announce reports only **counts**,
  and the detailed list is obtained peer-to-peer via the `share` frame after direct connection — the list contains hashes of publicly visible content only,
  no longer equivalent to broadcasting "what this node holds" to the entire network.
- MQTT discovery (public broker) does not add a presence room: opening a global room on a public broker is equivalent to broadcasting this node to the public internet.

**Done in this phase (2026-09-20 Netdisk Goal M1)**
- User-facing **node directory**: `service.NodeDirectory` + `GET /peerjs/nodes` (online ∪ joined, offline also retained, avoiding the illusion of "a node I added disappeared"),
  `GET /peerjs/nodes/joined`, `POST/DELETE /peerjs/nodes/join`.
- **Joined nodes** are persisted (`<storageDir>/joined_nodes.json`, temp file + rename atomic write)
  and become persistent peers: `SetExtraPeers` makes it auto-dial after signaling reconnects, **not subject to the discovery dial budget**
  (these are explicitly declared by the operator, not strangers encountered by discovery); immediately after joining, `EnsureConnection` dials,
  without waiting for the next discovery poll.

**Acceptance**: New integration test `TestInterconnectViaPresenceRoom` (two nodes with zero shared collections interconnect via presence room alone),
`TestNodeMarketListsDiscoveredPeer` (appears in the other's marketplace after interconnect, and not in its own list); unit tests lock down the presence room constant, dial budget semantics, and joined list persistence.

## 2. Files

**Ready**: Content-addressed storage `storage/{h[:2]}/{h}`, `file_index` (sha256 → absolute path + seq cursor), frame protocol verbs `create/upload/list/info/delete/sync`, `source` unified multi-source routing.

**Gaps**: See Phase 5 and 6 (scope and upload/download loop).

## 3. Combine

**Ready**: `serveFile` multi-source routing (local → peer → URL template); `FetchFromPeer` outbound fetch (with connection churn retry, see REFACTOR §3.17).

**Gaps**: Identity propagation for reading restricted content across nodes (Phase 7).

## 4. Management Chain

**Ready**: Local WS session (`/ws/peer`) + admin verb (internal forwarding to gin engine, reusing all HTTP controllers); **local WS only**, admin verb not implemented on WebRTC (to prevent permission exposure).

**Gaps**: Cross-node management (sending commands to **remote** nodes) has no design yet — before Phase 7, only node-level trust is available; `forward` (port forwarding v2) is currently the only cross-node controlled channel, and can serve as a reference.

**Used in this phase**: all new management endpoints added for the netdisk goal (node directory/join-remove, peer's share list, pull tasks) are attached to the admin frame of `/ws/peer`,
meaning the "browser → this node" segment uses the existing management chain; the cross-node segment uses `share`/`req` frames (**not** admin verb — admin is not implemented on WebRTC, to prevent permission exposure).

## 5. File Scope Management

**Ready on this node side (2026-09-20 Netdisk Goal M2)**.

Previously only had defensive `IsPathAllowed` (rejecting unauthorized paths), with no **user-facing "what I offer externally" declaration model**. Now there is:

- Config declares sharing scope (**default all off**, nothing exposed externally unless explicitly enabled):
  `PEERDRIVE_SHARE_ENABLE` (default `false`), `PEERDRIVE_SHARE_COLLECTIONS` (hash list or `all`),
  `PEERDRIVE_SHARE_DIRS` (directory list, empty = **don't share files**, not "share everything").
- `share` frame (`transport/share.go`): `{type:"share"}` → `{type:"share-resp", collections:[…], files:[…], dirs:[…], total:N}`.
  **Does not reuse `list`** — `list` is the local management index (full file_index, including local absolute paths), semantically "which files I'm managing";
  using `list` as a share list would mean default full-disk exposure. When sharing is disabled, returns an **empty array** instead of an error (empty is a valid business state).
- Restricted/private collections are always skipped: the share frame doesn't carry requester identity (hard constraint: no account dependencies before Phase 7); exposing them would be equivalent to making them public, so only content that was already allowed to be public is returned.
- Files return only basename + relative path, not local absolute paths.
- announce reports shared **count** summary (`loadInfo.shares`), marketplace cards have guidance info without leaking content.

**Still not done (waiting for Phase 7)**: Identity propagation for cross-node restricted content — remote requests don't carry requester identity, so the "restricted to specified permissions" tier is currently only usable within the same node.

**Acceptance**: Integration `TestShareProtocolContract` — A explicitly shares a collection → B gets an identical list via the `share` frame (including entry path/hash/mime) → successfully fetches content directly by entry hash → nodes with sharing disabled return an empty list.
14 unit tests covering default off / `all` only with public / skipping restricted private / ignoring invalid hashes / directory prefix boundary (`/data/share2` not matched by `/data/share`) / frame field name contract.

## 6. Upload/Download/Save

**Upload** already exists: `upload` verb (connection-level streaming + persistence + hash verification) + management-plane multipart upload.

**Cross-node download/save is ready (2026-09-20 Netdisk Goal M3)** —

- `service.PeerPuller`: task-based pull (form modeled after BT downloads: list/progress/cancel).
  stream → `<DownloadDir>/pulled/<relative_path>.part` → verify sha256 → rename → `file_index.Create` registration.
  After registration, it appears in "my files" and can also be `serveFile`d by this node to other nodes.
- Local already has same hash → skip (content-addressed dedup, no wasted download).
- Path sanitization (`../`, absolute paths, Windows reserved characters) + target absolute path prefix as double safety.
- Concurrency gate 3, task table cap 200 (only trimming finished ones), cancel = ctx + `Close(reader)`.
- Collection batch save: `StartCollection` requests a `share` list from the peer once, then creates tasks one by one; bad entries fail independently without dragging down the batch.
- Endpoints: `GET /p2p/pull`, `POST /p2p/pull`, `POST /p2p/pull/collection`, `POST /p2p/pull/cancel` (all static paths — gin doesn't allow the same level of routes to have both static and parameter segments).
- No-node consumer: `packages/peerdrive-client` (pure browser, zero dependencies, uses the same `share`/`req` frames).

**Still not done**: Resume download (protocol supports `offset`, resume logic not implemented), automatic retry on failure, configurable save location (currently fixed to `<DownloadDir>/pulled/`).

**Acceptance**: Integration `TestPeerPullSavesToLocalDrive` — A holds content → B pulls and saves → content matches + index searchable + second save skips + B can continue serving the saved file to C. 10 unit test groups covering persistence/skip/hash mismatch/path traversal/cancel/batch partial failure/input validation/sanitization rules/list order/task table trimming.

## 7. Identity Management

**Design ready, not implemented**: `doc/modules/auth/API-DESIGN.md` (JWT/accounts, user↔node directory, traffic statistics anti-fraud, encrypted channels) + `README.md` index. See §10 of that document for suggested implementation order.
