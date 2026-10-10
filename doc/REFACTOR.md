# Refactoring Log (REFACTOR)

> 2026-08-13 · Architecture refactoring log after the project restart. Everything new, all decisions, and all pitfalls are recorded here.
> For agents working later, quickly align context: **read this document first, then touch code**.

---

## 1. Why Refactor

The original peerdrive was a giant monolith of libp2p + BT DHT + IPFS + WebDAV + custom signaling. Review found:
- **Service could not start**: duplicate gin route registration caused a panic directly
- Auth was effectively useless, arbitrary file read/write/delete, P2P path traversal, CSWSH, remote crash DoS and other security holes
- A large amount of dead code (~4000 lines in frontend), global singletons, data races

Core decision: **replace the entire interconnect layer with PeerJS public cloud signaling + WebRTC DataChannel** (same design philosophy as [hana-link](https://github.com/Hana-ame/hana-link): transport-format-agnostic, upper layer defines verbs). The BT/libp2p stack is downgraded to legacy.

## 2. New Architecture Overview

```
Browser(peerjs) ─┐
                ├── PeerJS public cloud signaling (0.peerjs.com) ──→ WebRTC DataChannel direct connection
Go node          ─┘
    │
    └── MQTT sharded room discovery (peerdrive/v1/{collectionHash}/nodes) ← only exchange peerId
    └── Static config (PEERDRIVE_PEERJS_PEERS)
    └── (reserved) DHT bep44 discovery

Go node responsibilities: always online, content-addressed storage (sha256), bidirectional file service (serve + fetch)
Browser responsibilities: pull files from nodes via peerjs direct connection; node↔node interconnect sharing
```

## 3. Completed Changes

### 3.1 Route Fixes (Revive Service)
`internal/router/router.go` — removed all legacy redirects (`/anon/*`, `/actions/*`, `/files`),
conflicting routes merged into a dispatcher (`collection_dispatch.go`):
- `POST /collections` → `dispatchCreateCollection` (body with username goes through user system, otherwise anonymous)
- `GET /collections/:id` → `dispatchGetCollection` (64hex is anonymous hash, otherwise username)
- `GET /collections/:id/*filepath` → `dispatchGetTree` (gin does not allow `:param` and `*wildcard` to coexist,
  user system deep GET is all merged into this route)
- `withParams` uses `c.Copy()` + appending Params to complete parameter names, controller zero changes

### 3.2 peerjs Standalone Module (New)
Location: `back/peerjs/`, module name `github.com/Hana-ame/go-peerjs` (main go.mod uses replace reference).
Positioning: **transport primitives (signaling + data plane), business verbs defined by upper layer**.

```
peerjs/
├── message.go      MessageType(open string)/Message/Options/Offer/Answer/CandidatePayload
├── signaller.go    Signaller interface (signaling abstraction, PeerJS public cloud as default impl)
├── transport.go    DataChannel interface (transport abstraction) + Frame{IsText,Data} + pion adapter
├── peer.go         Peer top-level (Dial/Connect/OnConnection/routing) + PeerJS cloud signaling impl
├── connection.go   Connection (SDP exchange/ICE forwarding/text binary frames/atomic frames)
└── README.md       module-level Function Set documentation
```

Extension points (future extensions don't touch core):
- Change signaling: `NewPeerWithSignaller()` injects custom `Signaller`
- Change transport: implement `DataChannel` interface
- Add verbs: `MessageType`/`Frame` open types

### 3.3 PeerJS File Service (New)
`internal/service/peerjs_service.go` — bidirectional file service:
- **Passive**: browser/node connects to this node → `serveFile` (hash validation 64hex → chunked send)
- **Active**: `FetchFromPeer(peerID, hash)` → `requestFile` (reqId routing state machine collects responses)
- Node interconnect: `PEERDRIVE_PEERJS_PEERS` static config, `connectLoop` auto-reconnects on disconnect
- HTTP: `GET /peerjs/node` (discovery + peer list), `POST /peerjs/fetch` (pull verification)

### 3.4 MQTT Sharded Room Discovery (New)
`internal/service/mqtt_discovery.go` — topic `peerdrive/v1/{collectionHash}/nodes`:
- **Sharded mode** (sharded by collection hash, public broker has no scale limit; global single topic fan-out is the bottleneck)
- announce idempotent dedup + 60s heartbeat; paho auto-resubscribes on disconnect (SetOnConnectHandler)
- discovery only exchanges `{peerId, ts}`, actual transport still via WebRTC direct connection
- Config: `PEERDRIVE_MQTT_ENABLE/BROKER/TOPIC_PREFIX/COLLECTIONS`

### 3.5 Local WebSocket Session (New)
`internal/service/ws_session.go` — browser local direct connection goes through WS, **frame protocol is identical to DataChannel**:

```
Browser ──WS(/ws/peer)──→ Local node: management/metadata/small files (millisecond-level, no hole punching)
Browser ──WebRTC───────→ Any node (including remote): large files, cross-node (hole-punched direct)
```

- `Session` interface abstracts both transports (`internal/service/ws_session.go` + `rtc_session.go`):
  same reqId state machine / serveFile / FetchFromPeer with zero branch reuse
- `FetchFromPeer("local", ...)` reuses the same pull path; `WSSession` has no write buffer flow control
  (TCP has it built-in, serveFile uses interface assertion to only do water-level control on DataChannel)
- Note: not the same as the old `/ws/signal`, `/ws/transfer` (legacy custom signaling)

### 3.6 Self-hosted Signaling Server (New)
`internal/signalserver/` + `cmd/peerserver/` — self-hosted PeerJS signaling + **built-in room discovery**:
replaces public cloud signaling (0.peerjs.com) and public MQTT broker.

```
Run on vps: peerserver -addr :9000 -key <key>
Node side: PEERDRIVE_PEERJS_HOST/PORT/KEY points to self-hosted (peerjs client protocol zero changes)
          PEERDRIVE_DISCOVER_URL=http://vps:9000 (discovery takes priority over MQTT)
Browser: host/port/key config points to self-hosted (own signaling, no MITM surface)
```

- **Signaling**: compatible with peerjs-server protocol subset (WS registration + token, OFFER/ANSWER/CANDIDATE/LEAVE forwarded by dst,
  dst offline enqueue 30s expiry, OPEN/ID-TAKEN, heartbeat keepalive, `GET /{key}/id` allocation)
- **Discovery** (MQTT functionality merged in): `POST /discover/announce {peerId, collections}` (30s heartbeat) +
  `GET /discover/nodes?coll=` query online nodes — server naturally knows all online nodes, no broadcasting needed
- Node-side `HTTPDiscovery` (`service/http_discovery.go`): announce + 10s polling → onPeer → auto interconnect
- Security: with self-owned signaling there is no public cloud MITM surface; can add token whitelist on server later

### 3.6.1 Online Deployment (cloudcone)
```
peersignal.moonchan.xyz ──CF gray cloud A record──▶ 117.55.237.217 (cloudcone nginx)
        │ wss + https
   peerserver (systemd, 127.0.0.1:9000, key=pd-signal-1edf5e05e4a52b7351392574)
```

- Deployment details and operations commands see project AGENTS.md "Online Deployment" section
- Online verification: `PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v`
  (TestLiveSignal_DiscoveryAndInterop: online signaling+discovery+pull files full chain; TestLiveSignal_ProtocolCompat: client protocol compatibility)

### 3.7 transport Package inbound/outbound Role Split (2026-08-16)

Split `transport/` by frame role into two roles + shared core (aligning with Xray/sing-box mental model,
but **only split roles, not connections** — WebRTC connections are full-duplex symmetric, the same Session carries both roles simultaneously):

```
conn.go          ← Shared connection core (copying forbidden): dcReq/dcResp, connState (fetches/expect belong
                     to outbound, pendingUpload/binCh belong to inbound, flat shared), bindConn dispatch
inbound.go       ← Inbound role = answer peer's full verb set: serveFile/openFile,
                     create/upload/list/info/delete/sync server-side (original file_index_verbs.go merged in),
                     uploadWorker (upload to disk)
outbound.go      ← Outbound role = local-initiated full verb set: FetchFromPeer/requestFile/routeResponse/
                     stateFor + maxPeerFetchSize
peerjs_service.go← Slimmed down to assembly layer (854 → 420 lines): signaling lifecycle, connection establishment
                     (connectLoop dialing / onIncomingConnection accept), BindLocal,
                     discovery assembly — connection establishment just creates a full-duplex Session, then bindConn attaches both roles
```

- Pure code repositioning (cut+rebuild files), zero behavior change; `file_index_verbs.go` deleted and merged into inbound.go
- Verification: `go build -tags nosqlite ./...` + `go test -tags nosqlite ./internal/transport/ -race` all green
- Reserved (not done): outbound-side `Fetcher` interface (localFetcher/peerFetcher/future httpFetcher)
  implements "any entry request → any exit" routing matrix, to be landed when new transport needs arise

### 3.8 Unified Source System (2026-08-16)

Unified file management: multiple backends (local disk / p2p passthrough / URL template) abstracted as `Source`,
routed uniformly by `SourceManager` (priority), statistics (Snapshot), runtime adjustment (SetPriority).

```
internal/source/
  source.go   ← Source interface + Capability(CapFile=1 full pull / CapStream=2 streaming shards)
                 + FileMeta/Stats/SourceStatus
  manager.go  ← Register/unregister, priority ascending routing: hit returns immediately, Available()==false skipped,
                 all fail returns aggregated error; record() logs success/failure/bytes/last error
  local.go    ← LocalSource: resolvePath replicates serveFile (file_index priority + IsPathAllowed
                 defense + CAS fallback), CapStream
  peer.go     ← PeerSource: Connections enumeration + per-peer Mutex.TryLock serial attempts
                 (connection-level expect single-slot constraint: same peer only allows one fetch stream at a time)
  url.go      ← URLSource: fmt template (%s=hash, including %d declares CapStream) → Range shards;
                 full request reads then sha256 verify (content-addressed fallback); can inject http.Client
                 pointing to ech-proxy or similar egress (wintools cmd/ech-proxy), no independent source type created
```

- **Routing semantics**: local hit returns immediately, miss degrades to peer, URL source last fallback; large files only go through
  CapStream (OpenRange rejects CapFile sources — prevents 8GB full buffer OOM), OpenAny can degrade to full
- **Assembly** (internal/serverapp/app.go): local → peer → url(optional, `PEERDRIVE_URL_SOURCE_TEMPLATE`)
- **Management plane**: `GET /sources` (status+stats), `POST /sources/:name/priority` (runtime adjustment)
- **Boundary**: serveFile keeps local semantics without connecting to manager (avoids inbound→outbound passthrough regression loop);
  /peerjs/fetch still calls FetchFromPeer directly (keeps semantics, not switched to Manager)
- Companion: `requestFile` streaming (fetchState adds `q chan []byte` block queue + pump delivery,
  OpenStream streaming read; FetchFromPeer keeps []byte compatible signature); fixed cleanup double close panic,
  fetchReader drain loop buf overwrite losing blocks — two bugs
- Verification: `go test -tags nosqlite ./internal/source/ -race` + transport all green;
  integration tests referencing `service.*` M3 legacy changed to `transport.*`


### 3.9 Port Forwarding forward v2 (2026-08-16, inbound link rebuilt)

The legacy libp2p stream forwarding (`/peerdrive/forward/1.0.0`, plaintext `KEY xxx` single-line auth, no
whitelist) has been deleted, replaced with a TCP tunnel on PeerJS DataChannel (`internal/transport/forward.go`,
approximately 470 lines + 8 unit tests):

```
client ──fwd-open {port, reqId}──────────────▶ server   request forwarding target port
client ◀──fwd-challenge {nonce, reqId}────────  server   one-time random nonce (5min TTL, cap 64 to prevent flood)
client ──fwd-auth {hmac, reqId}──────────────▶ server   HMAC-SHA256(key, nonce)
client ◀──fwd-ok / fwd-err────────────────────  server
Then: fwd-data header+binary blocks bidirectional passthrough (reusing SendFrame atomic header-block constraint); fwd-close to close
```

- **Permission control**: rule table `key → port whitelist` (config `PEERDRIVE_FORWARD_RULES="key:port,..."`
  or runtime `POST /p2p/forward/create` dynamic registration); port privilege escalation → fwd-err, doesn't leak rules
- **Key exchange**: challenge-response (nonce one-time + expiry), key plaintext never on the wire; server-side verification needs
  key original (=<credential, config chmod 600)
- **SSRF protection**: server side only dials `127.0.0.1`; handshake doesn't occupy tunnel slot, tunnel establishment occupies connection-level single slot
- **API**: `PeerJSService.OpenForward(ctx, peerID, key, port)` (net.Conn); HTTP endpoints
  4 retained (create=register rule / connect=local listener proxy / list / close)
- Forward block write goes through connection-level worker (fwdCh, same as H5) — TCP backpressure doesn't block message pump;
  CloseForwardStream active disconnect releases single slot immediately
- Verification: 8 unit tests (handshake full flow/bad key/port privilege escalation/replay/timeout/no tunnel defense/data passthrough)
  + `-race` all green

### 3.10 Frontend Full Migration to WS + admin Management verb (2026-08-17)

Frontend fully migrated from HTTP API to local WS session (`/ws/peer` frame protocol), HTTP routes all retained
(`router.go` marked with LEGACY comment section). User decision: **management plane only through local WS**, WebRTC connections
do not implement management verbs (prevents permission surface exposed to unknown nodes on public signaling); data plane still goes through original `req` verb.

- **admin verb** (`internal/transport/admin.go`, ~300 lines + 6 unit tests): browser sends
  `{"type":"admin","method","path","body","token","reqId"}` through local session, internally constructs
  *http.Request → injects into gin engine's ServeHTTP (`httptest.NewRecorder`, router assembled via
  `SetAdminHandler`) → **reuses all HTTP controllers, zero duplicate implementation**
- **Binary upload**: admin frame `binary:true` + filename/field/size declaration, subsequent binary frames
  collected to temp file → multipart repackage forwarding (controller's FormFile unaware). field defaults to
  `file`, BT torrent uses `torrent` + `path=/bt/torrent` (reqPath determined by declaration frame,
  **pitfall**: initial version hardcoded `/files/upload` causing torrent to hit wrong route, see admin_test.go)
- **Binary response**: file stream → `admin-bin` header + single binary frame (≤64MB; large files go through req verb)
- **Authentication**: admin frame token field → forwarding injects `Authorization: Bearer`, consistent with HTTP
- **Upload slot replacement pitfall (2026-08-18 review fix)**: when redeclaring upload, replacing old slot, must return
  err frame to old reqId — otherwise old upload browser Promise hangs forever (pending until disconnect clears), and old upload
  late blocks mix into new au's got count causing new upload to be falsely judged size exceeded and aborted, err pointing to new reqId
- **Frontend**: `front/src/ws.js` (new, admin/upload/download/downloadToFile, reqId pending
  map + single slot binaryExpect) + `api.js` all requests go through WS; page download/preview changed to Blob
  approach (`getBlobUrl`/`downloadFileToDisk`); `__mocks__/api.js` synchronized.
  **getBlobUrl cache leak pitfall (2026-08-18 review fix)**: blobUrlCache only grows never shrinks (each
  preview hash occupies a Blob + objectURL) → LRU cap 50, evict on review
- Verification: backend 10 admin unit tests + frontend `tests/ws.test.js` 8 unit tests (reqId out-of-order routing/
  409 passthrough/token/chunked collection/err/admin-bin/disconnect/upload FileReader fallback) + full unit tests +
  build all green

### 3.11 standalone Package peerdrive-media (2026-08-18, independent repo)

`packages/peerdrive-media/` is an independent npm package for third parties: browser loads URL resources from Node side via PeerJS signaling +
WebRTC DataChannel and renders `<img>`/`<video>`.
**Independently hosted at github.com/Hana-ame/peerdrive-media** (tag synced with version), reason:
npm doesn't support git dependency `#path:` subdirectory syntax (only pnpm/yarn support it), main repo root directory
also has no package.json, cannot be used as git dependency as a whole.

- **Three entry points**: `peerdrive-media` (React components PeerImage/PeerVideo/PeerMedia +
  Provider/usePeerMedia), `peerdrive-media/vanilla` (IIFE/ESM `<script>` direct reference,
  also jsDelivr CDN direct link), `peerdrive-media/node` (createPeerMediaServer provider)
- **Frame protocol**: peerjs `serialization:'raw'` — text frames=JSON headers (url/meta/done/err),
  binary frames=64KB data blocks; connection-level serial (one connection only serves one request at a time),
  `bufferedAmount > 4MB` backpressure pauses reading upstream
- **dist committed to repo**: main repo root .gitignore's `dist/` swallows package's dist/ (git parent directory exclusion
  doesn't descend, `!dist/` counter-effect invalid), use `git add -f` to force; independent repo has no such issue, commit directly
- **Peer install**: npm 7+ doesn't auto-install dependencies marked as `peerDependenciesMeta optional`,
  will get `Cannot find package 'react'` (discovered by e2e tests) → removed meta, react/react-dom
  installed together
- **Verification**: package unit+E2E 17/17 (frame protocol 6 + local signaling full chain 7 + core serial queue 4);
  consumer scenario
  re-verification — temp project `npm i github:Hana-ame/peerdrive-media` then three entry points import smoke +
  e2e.test.mjs (changed package name import) 7/7
- **Browser E2E (10/10, 2026-08-18 completed)**: playwright local Firefox headless
  (`~/.claude/skills/playwright-test/` deprecated CDP 9222, switched to local firefox; needs
  `playwright-core/cli.js install firefox` via proxy to supplement install), demo page uses node native static
  server (`scripts/static-serve.mjs` ⚠️该脚本已不存在, vite dev will transform IIFE breaking `PeerMedia`
  global + stale cache). Five pitfalls in a row (all fixed in core.js):
  1. **peerjs dual build default export semantics differ**: `main`(cjs) default=module.exports
     (contains Peer), `module`(esm) default=internal util object (no Peer) → three-way fallback
     `NS.Peer || pkg.Peer || pkg.default?.Peer`
  2. **peerjs-server 0.2.9 has no retrieveId endpoint**: browser-side Peer must explicitly use random id
     (`pd-b-` prefix), otherwise signaling 404 → ServerError
  3. **Default STUN stuck gathering**: no external UDP environment (WSL) peerjs default stun.l.google.com
     stuck ICE → `config: { iceServers: [] }` (intranet host candidate is sufficient)
  4. **Firefox mDNS obfuscation**: `media.peerconnection.ice.obfuscate_host_addresses=false`
     (runner firefoxUserPrefs)
  5. **core.js three self bugs**: conn error unconditional teardown (DC open instant flush triggers
     peerjs NotOpenYet race → tears down just-created connection, real disconnect handled by close event);
     every queued request calls open() (4 components concurrent = 4 Peer connections, Node side busy rejects);
     client didn't self-queue per Node side "connection-level serial" (concurrent requests rejected by `another request in
     flight`) → opening guard + flush() queue
  6. **closed slot reuse hangs**: teardown sets `closed=true` but slot still in client cache,
     subsequent load() reuses closed slot causes request()'s "closed doesn't open()" guard to let new requests
     queue forever, Promise never settles (no error during loading). Fix: load() detects slot.closed
     and rebuilds; browser test dispose → load again succeeds. Discovery background: code review 2026-08-18
  7. **Serial slot empty occupation three entry points (2026-08-18 review fix)**: abort/late frames/conn.send exception three paths
     can all cause "connection-level serial slot" to be empty-occupied — abort only deletes pending without flushing (Node side
     still returns done/err after processing, handleData directly returns because pending doesn't exist, queued requests never
     send); meta>=400 branch delete+reject but also doesn't flush; conn.send exception path doesn't
     cleanup (abort listener leak, closure holds slot and connection). Fix: three places unified
     cleanup+flush (flush has pending.size==0 guard, won't over-send). core.test.mjs
     4 mock-driven event stream regression items. Discovery background: code review 2026-08-18 (component unmount abort +
     subsequent queued request scenario)
- Legacy: `test/e2e-browser.mjs` readyState≥1 assertion doesn't apply to no-container fake video bytes
  (demo fake.mp4 has no valid container), changed to only verify element mount
- Disconnect perception hint: WebRTC without STUN keepalive timeout can reach tens of seconds, after disconnect requests will
  error out before teardown (correct behavior); for immediate failure use client.dispose()

### 3.12 Optimization Batch 1-10 (2026-08-18)

10 optimizations from code review + architecture inspection (all landed and verified):

| # | Optimization | Location | Verification |
|---|---|---|---|
| 1 | Frontend large file download full memory assembly (OOM risk) | `front/src/ws.js` `downloadStream` (ReadableStream enqueue as received) + `downloadToFile` (File System Access API streaming to disk, fallback Blob) + `stat`; `api.js` getBlobUrl 200MB threshold (`err.code='TOO_LARGE'`) | ws.test.js +4; frontend 36/36 |
| 2 | uploadWorker single worker serial blocking (8GB upload blocks forwarding tunnel) | `inbound.go` `fwdWorker` split out (shares binDone exit with uploadWorker), conn.go bindConn dual workers | transport `TestFwd/TestUpload -race` |
| 3 | serveFile not connected to multi-source routing (§3.8 Source system reserved but not wired) + origin loop prevention | `inbound.go` `FileRouter` interface (OpenRange/InfoSize) + `serveFile` routing branch (meta.total from InfoSize, miss returns err not found, nil falls back to local semantics); `dcReq.Trace` node chain loop prevention (omitempty compatible with old peers); `outbound.go` `OpenStreamFrom` propagation point; `peerjs_service.go` `SetFileRouter`; `source/manager.go` `InfoSize` adapter; `internal/serverapp/app.go` assembly | servefile_router_test.go (loop/router/fallback/trace 4 tests) |
| 4 | core.js disconnect perception slow (timeout tens of seconds without STUN) | `packages/peerdrive-media/src/core.js` keepalive (`KEEPALIVE_INTERVAL=5000`/`KEEPALIVE_TIMEOUT=15000`, timeout tears down and rebuilds; Node side serveConnection symmetric) | core.test.mjs +2; npm test 20/20 |
| 5 | core.js queued abort doesn't settle immediately | core.js `request()` queued branch adds abort listener, abort dequeues reject (AbortError), removes listener to prevent leak | core.test.mjs +1 |
| 6 | Integration tests depend on real public signaling (needs external network+proxy, parallel interference) | TestMain starts global self-hosted signalserver (httptest), newService all point to it; MQTT public broker tests `PEERDRIVE_MQTT_TEST=1` gated; also fixed go-peerjs `Connection` callback registration data race (handlerMu, independent library synced) | Integration tests 13.7s external-network-free all green |
| 7 | getBlobUrl same hash concurrent double download race | `front/src/api.js` `blobUrlInflight` Map in-flight dedup | Frontend 36/36 |
| 8 | signalserver token whitelist | `signalserver.go` `WithTokenWhitelist` (empty=no limit) + `cmd/peerserver` `-tokens` flag | TestSignal_TokenWhitelist |
| 9 | README env table outdated (residual deleted P2P_ENABLE/WEBDAV/IPFS_COMPAT/RELAY_MODE) | README.md env table aligned with config.go | — |
| 10 | Document line number references easily drift | This section convention (see below): long-term docs prefer function names; this round's docs (conn.md/ws-client.md/README) all removed line numbers | — |

**Document reference convention (item 10 landed)**: long-term maintained documents (REFACTOR.md, doc/layers/*, README,
AGENTS.md) referencing code must use **`file path:function name`** (e.g., `inbound.go serveFile`), line numbers forbidden
(line numbers drift with code changes, historical documents' accurate line numbers are long since invalid). One-time review documents
(HTTP-REVIEW/FRONTEND-FIX etc.) are snapshots at the time, line numbers allowed. Line count annotations ("xxx lines")
also forbidden — just use file list to explain responsibilities.

**Item 6 details**: integration test data plane is still real WebRTC (same-machine host candidate direct, no
STUN needed) — no UDP sandbox (docker default) can't connect, interconnect tests can use `PEERDRIVE_SKIP_RTC=1`
to skip (local WS/admin types unaffected). go-peerjs's race fix (`connection.go` handlerMu
protects onOpen/onMessage/onClose callback registration and trigger concurrency) is a
real bug exposed during -race consecutive self-hosted test runs, exists the same way on real networks (slow timing makes it hard to trigger).

**Test completion batch (2026-08-18, 77cff65)**: after all 1-10 items landed, regression tests added per "ensure comprehensive tests"
covering previously unlocked behaviors:

| Test | Locked Target |
|---|---|
| `front/tests/api.test.js` (new file, 5 items) | getBlobUrl 200MB TOO_LARGE threshold, same hash concurrent in-flight dedup (item 7), retry after failure, cache hit refresh LRU, LRU eviction revoke (item 1) |
| `peerjs` peer_test.go `TestConnection_CallbackRegistration_Concurrent` | handlerMu fix -race regression: 50 rounds concurrent "register vs trigger", also verifies snapshot read doesn't break register semantics |
| transport peerjs_service_test.go `TestServeFile_IndexPathAllowed` | fallback branch file_index hit valid path must return (item 3 rewrite lost branch regression — integration test reported not found exposing it) |
| transport servefile_router_test.go `TestServeFile_RouterInfoSizeFail` | InfoSize failure → meta.total=-1 (protocol convention, data flow unaffected); `infoFailRouter` aligns with PeerSource/URLSource no metadata scenario |
| integration selfhosted_test.go `TestStartClose_RacePressure` | 30 rounds Start/Close + 50ms hitting race window: locks startLoop discovery component peerMu snapshot + ctx.Err() guard (dfd4acb fix -race regression) |

Verification baseline updated: frontend 41/41 (36+5), peerjs `-race` green, transport `-race` green,
integration `-race -p 1` external-network-free 18.9s green.

### 3.13 Dead Code Cleanup Batch (2026-08-19, post-review cleanup)

Code review found "doc/archive/LEGACY.md marked as deletable/pending migration but not landed" stock all processed:

**Backend (-8 files)**:
- Auth subsystem whole set (`controller/auth.go`, `service/auth_service.go`, `model/user.go`,
  `repository/user_repo.go`): `NewAuthController` zero call points, no `/auth/*` routes
  (router auth goes through remote reg server's `AuthRequired()`, unrelated to this local set)
- Task subsystem whole set (`controller/task.go`, `service/task_service.go`,
  `model/transfer_task.go`, `repository/task_repo.go`): `ListTasks` always returns empty placeholder,
  TaskService only writer is PullCollection
- `fork.go` `PullCollection`: no-op + writes fake completed task; `/tasks` route,
  `/collections/pull`, `/actions/pull` synchronously removed; db.go's `users`/`transfer_tasks`
  table creation DDL deleted (existing DB old tables have no read/write endpoints, harmless to keep)
- `p2p.go` top ~30 lines of outdated comments describing deleted functions (GetNodeInfo/GetPeers/P2PStatus etc.) cleared

**Frontend (-4 files + 2 pages rewritten + dead export cleanup)**:
- P2PPanel / P2PDashboard / P2PTopology deleted: all called deleted endpoints
  (/p2p/status, /p2p/peers, /p2p/dual/* etc.); App.jsx routes (/p2p, /p2p/dashboard,
  /p2p/topology) and Navbar "P2P Network" dropdown synchronously removed
- `api.js` dead export cleanup (14): getP2PStatus/getP2PNode/getP2PPeers/getP2PDiscovered/
  pingPeer/connectPeer/p2pAnnounce/p2pRequestFile/getWSInfo/getSignalPeers/
  getP2PTopology/getP2PQuality/dualAnnounce/dualFind/getTasks + getNodeInfo alias
- Added `getPeerjsNode` (GET /peerjs/node, replacing old /p2p/status online status query),
  Plaza "P2P Online" badge and BTController status banner changed to use it
- DHTExplorer rewritten to only BEP51 sampling (/bt/bep51/sample alive; dual-stack query dead),
  /ipfs/dht route merged into /bt/dht; IPFSPanel rewritten to only CID pin + gateway status
- LLMAssistant removed get_tasks/get_p2p_status two dead endpoint tools

Verification: back all packages + integration (-p 1 external-network-free) all green; frontend 41/41; `go vet` clean.

```jsonc
// Request (any endpoint); reqId is command UUID v4 (server-generated, guarantees cross-connection uniqueness)
{"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<uuid-v4>"}
// Response (echoes reqId)
{"type":"meta","hash","total","reqId"}
{"type":"data","hash","offset","size","reqId"}   // followed by size bytes binary
{"type":"done","hash","offset","size","reqId"}
{"type":"err","msg","reqId"}
```

**File Index verb** (`FileIndexService`, SQLite `file_index` table persists sha256→absolute path):

```jsonc
create   {type:"create", path}              → created {hash,size,name,path,seq}
upload   {type:"upload", name, size, offset?, reqId}  chunked upload (offset defaults 0)
         → meta {total, offset:contiguous written} → data×1 → uploaded{hash,path} (fully complete) | ack{offset} (resume)
list     {type:"list", offset?, size?}       → list-resp {files,total}
info     {type:"info", hash}                → info-resp {hash,size,name,path,seq}
delete   {type:"delete", hash}              → deleted {hash,seq}
sync     {type:"sync", seq}                 → sync-resp {files,lastSeq} (metadata incremental sync)
```

- **Chunked upload**: offset aligned by 64KB chunks, one upload request = one chunk (data block ≤64KB);
  server-side `UploadSession` bitmap tracking (chunk granularity), **multi-source** = multi-connection concurrent uploading different chunks,
  bitmap full auto-triggers uploaded (last chunk's requester receives it)
- **Resume upload**: same name reopening session idempotent reuse; meta.offset returns contiguous written offset (bitmap rebuilt,
  after process restart approximated by file size, final sha256 verification fallback); session 10 min inactivity cleanup
- Sync model: `file_index.seq` monotonic cursor, `sync{seq}` gets incremental changes (including tombstone), peer `ApplySync` merges
- Upload security: size cap 8GB, filename sanitization (prevents path traversal), offset must be chunk aligned, out-of-bounds write rejected
- download prefers file_index (externally registered/uploaded files), then content-addressed storage

**Three protocol constraints (don't break)**:
1. JSON control headers must be **text frames** (`SendText`), data blocks are **binary frames** (`Send`) — sending reversed causes peer to swallow control header as data block
2. data header and data block must be **atomically continuous** (`SendFrame`'s sendMu), receiver routes by connection-level expect state machine
3. Browser side can omit reqId (backward compatible), Go side always carries it (UUID v4)

**admin management verb** (§3.10, local WS session only, `admin.go`):

```jsonc
// Normal request → admin-resp (4xx/5xx also go through admin-resp, body is structured error body, 409 contains conflicts)
{"type":"admin","method":"GET|POST|DELETE","path":"/files?x=1","body":<JSON>,"token":"<optional>","reqId"}
{"type":"admin-resp","status":200,"body":<original JSON>,"reqId"}

// Binary upload: declaration frame + subsequent binary frames (collect complete multipart repackage forwarding; field defaults "file")
{"type":"admin","method":"POST","path":"/files/upload","binary":true,"filename":"a.bin","field":"file","size":N,"reqId"} + N bytes binary frame

// Binary response (file stream, ≤64MB): admin-bin header + single binary frame
{"type":"admin-bin","status":200,"size":N,"reqId"} + binary frame
```

### 3.14 Independent repo Mirror: go-peerjs / go-peerserver (2026-08-19)

After module closure review, two modules with zero internal dependencies mirrored as independent repos (main repo dual-maintained,
same pattern as peerdrive-media: **main go.mod keeps replace pointing to local directory**, changes committed with main repo
then mirrored to sync):

| Independent repo | Local location | tag |
|---|---|---|
| `github.com/Hana-ame/go-peerjs` | `back/peerjs/` | v0.1.0 |
| `github.com/Hana-ame/go-peerserver` | `back/signalserver/` | v0.1.0 |

**go-peerserver split details** (this round's main repo structure change):
- `back/internal/signalserver/` + `back/cmd/peerserver/` → `back/signalserver/`
  (independent go.mod `github.com/Hana-ame/go-peerserver`, containing signalserver package +
  `cmd/peerserver` independent binary)
- Main go.mod: `require ... v0.0.0` + `replace => ./signalserver` (local dual-maintained)
- Integration test imports changed to `github.com/Hana-ame/go-peerserver` (TestMain self-hosted signaling unchanged)
- Independent module has zero peerdrive dependencies (only gorilla/websocket + stdlib), can independently `go build ./cmd/peerserver`

**Why keep local replace rather than switch to remote dependency**: ensures `back/peerjs`, `back/signalserver`
changes can be directly built and verified with main repo, no need to push independent repo first — zero dev friction; external users
`go get github.com/Hana-ame/go-peerjs@v0.1.0` unaffected.

**Why go-peerjs didn't remove go.mod original version prefix**: within this repo both main module and independent module exist,
module name is `github.com/Hana-ame/go-peerjs`, replace uses local path, no version designator needed.

Split decision criteria (recording tradeoffs): forward engine (transport/forward.go only depends on log) and
file data plane engine (req/index verb coupled with file_index storage + config/log) **not independent for now** — both have
no out-of-repo users, splitting is negative benefit; wait until second user appears then split.

### 3.15 Efficiency Optimization Batch (2026-08-19)

3 efficiency optimizations from large file transfer path code review (all landed and verified):

| # | Optimization | Location | Verification |
|---|---|---|---|
| 1 | **fetchReader timer leak**: `Read` creates `time.After(fetchIdleTimeout)` new 5-min timer for every consumed data block — 8GB file = 130k timers resident in runtime timer heap until expiry (memory + GC double waste, long-term accumulation during large file transfer) | `outbound.go fetchReader.Read`: single timer creation + `Reset` each time block received (Stop then drain C then Reset, prevents Go timer semantics pitfall) | transport full -race green |
| 2 | **serveFile allocates 64KB block buffer per request**: concurrent serveFile each make, GC pressure + memory peak | `inbound.go` `chunkPool` (sync.Pool 64KB, SendFrame synchronous copy then return — pion Send internally copies, return is safe) | transport full -race green |
| 3 | **PeerSource serial peer attempts**: first slow peer (remote disk slow/network jitter) blocks entire origin fallback, concurrent race reserved in comments now landed | `source/peer.go Open`: multi-peer concurrent OpenStreamFrom, **first success returns immediately** (real race, slow peer doesn't block this call); late/failure results collected by background goroutine Close (prevents loser stream leak: peer stream mutex permanently occupied + local fetch state hanging). Single peer goes original serial path zero overhead; failure path immediately Unlocks | `source/peer_test.go` 2 new tests (first-responder peer wins + late frames silently ignored + all fail error and lock release, using real PeerJSService + BindLocal in-memory session full chain driven); added `transport file_index.go PendingFetchesForTest` cross-package observation helper |

Verification baseline: back all packages + peerjs -race + integration `-p 1` external-network-free 17.9s all green.

**Race test completion batch (2026-08-19, item 3 PeerSource race regression coverage)**:
`source/peer_test.go` expanded to 8 items, using real PeerJSService + BindLocal in-memory session full chain driven:

| Test | Locked Target |
|---|---|
| `TestPeerSource_RaceWinsFastest` | Dual-peer race: data complete + loser collection (fetch state cleanup) + late frames silently ignored |
| `TestPeerSource_RaceThreePeers` | Three-peer race: all peers concurrently receive req; after collection second round race succeeds (lock no leak) |
| `TestPeerSource_RaceMixedFailSuccess` | Partial failure + partial success: failure path immediately releases lock, can race again after recovery |
| `TestPeerSource_RaceBusyPeerSkipped` | Busy peer (mutex occupied) skipped without blocking: no req sent no state occupied, remaining peers win normally |
| `TestPeerSource_RaceAllFail` | All fail error + failure path zero residual (lock + fetch state) |
| `TestPeerSource_MultiBlockTransfer` | Winner stream >64KB three blocks reassembled in order complete |
| `TestPeerSource_WinnerPeerLockReleased` | Winner Close / loser collection two lock release paths per-peer verified (failSend converges to unique winner) |
| `TestPeerSource_WinnerErrFrame` | Winner stream err frame read-time error (doesn't silently return bad data) |

**Test design pitfall (recorded this batch)**: race winner is non-deterministic (`Connections()` map iteration random +
goroutine scheduling uncertain), assertions relying on "feed to specific peer" unreliable (once wrote test with 50% probability
timeout) — uniformly changed to "response fed to all candidate peers (loser late frames ignored)"
or "failSend converges competition to unique peer" two deterministic approaches.

**Failure fallback batch (2026-08-19, item 3 PeerSource failure/fallback path coverage)**:

| Test | Locked Target |
|---|---|
| `TestPeerSource_NoConnections` | No online peers → explicit error (diagnosable error at end of fallback chain) |
| `TestPeerSource_OpenAfterSvcClose` | Open after service closed → error without hanging (all connections released) |
| `TestPeerSource_ReadAfterSvcClose` | Race established then service closed → read-time ctx cancel error, doesn't treat buffered data as complete result |
| `TestPeerSource_WinnerPeerDropped` | Race wins then peer disconnects mid-transfer (no done frame) → read-time error, doesn't return truncated data (content-addressed semantics) |
| `TestPeerSource_AllFailErrorDetail` | All failure error aggregation: each peer failure reason enters final error (implementation improvement: failure reason existed in serial version but lost in race version, added `strings.Join` aggregation) |

Companion changes: `source/peer.go Open` all-fail error upgraded from "all peers failed" to
aggregated per-peer reasons; `fakePeerSess` added `Close` triggering `onClose` (simulates peer disconnect,
consistent with real `WSSession.Close` semantics — previously Close was empty implementation, disconnect-type tests couldn't drive
bindConn's OnClose cleanup path).

**Multi-connection batch (2026-08-19, same peer dual connection dedup + multi-connection tests)**:

**Problem**: bidirectional mutual dialing (A↔B dialing each other simultaneously, integration test default scenario) or reconnection race leaves
two WebRTC connections under same peerID — `conns` map keyed by peerID only keeps one,
the other becomes orphan: connState resident in pending map + uploadWorker/fwdWorker
goroutine leak + wastes one connection resource.

**Fix** (`transport/conn.go bindConn`):
1. Same peer dual connection dedup: `conns` keeps one, `Close` eliminated one outside lock (deadlock pitfall from Close inside lock see §5); eliminated connection's OnClose cleanup has `s.conns[c.ID()] == c` value
   equality guard, won't accidentally delete kept connection.
2. **Retention strategy must be consistent on both ends**: lexicographically smaller connection-level UUID wins
   (`rtcSession.ConnID()` = peerjs `Connection.ID`, both ends see same value).
   Bidirectional mutual dial each end sees {own dial-out, peer dial-in}, if each keeps own dial-out connection, the kept
   is exactly the peer's already-closed broken link → pull timeout failure. **Discovery background**: after dedup batch went live,
   integration test ~1/4 probability failure (`TestSelfHostedSignalAndDiscover` 10.5s timeout),
   logs `dedup connection ... closing stale` appearing in pairs — both ends each close one,
   mutual broken links left. After fix 6 consecutive runs all green. fakeSession without connection UUID (rank equal)
   keeps new connection (test double semantics).
3. local WS session not deduped: multiple browser tabs each have one local session, actively closing old tab
   connection would interrupt its ongoing inbound service.

**Tests** (`transport/conn_test.go` 4 new + `source/peer_test.go` 1 new):

| Test | Locked Target |
|---|---|
| `TestBindConn_SamePeerDedup` | Same peer dual connection: new connection kept, old connection closed, old OnClose cleanup doesn't accidentally delete new connection |
| `TestBindConn_ReplacedConnOldStreamErrors` | After connection replaced, ongoing streams on old connection must error end (no hang no truncation) |
| `TestBindConn_LocalNoDedup` | Multiple local sessions coexist without killing each other (multi-tab scenario) |
| `TestBindConn_DedupFreesSlot` | After dedup resources fully released, new connection can normally serve pulls (real pump chain: meta/data header/binary block/done) |
| `TestPeerSource_ParallelStreamsAcrossPeers` | 3 peers 3 connections parallel streams: peer stream mutex isolated per peer without blocking each other, data each complete, end zero residual |

Companion changes: `fakeSession` added `closed` flag + `Close` triggers `onClose` (simulates
real connection close cleanup path, dedup tests depend on it); `rtcSession` added `ConnID()` +
`connRanker` optional interface (Session interface itself unchanged, WSSession unaffected).

**Test design pitfall (recorded this batch)**: race goroutines are asynchronous — `Open` returning
≠ all candidate goroutines finished, failed peer unlock may be later than return; consecutive multi-round racing,
next round `TryLock` hits unreleased lock (`-count=5` occasional failure, error
"peer peerA: ..." indicates previous round loser lock unreleased). Fix: `waitLockFree` uses
TryLock probing + immediately release waiting for "this round candidate lock" free (can't wait for all locks free — winner
lock intentionally held by reader until test end).

**Readability and hotspot batch (2026-08-19, limb 2)**:

Readability (committed 806aff9):
- `transport/conn.go`: `bindConn` 130-line inline `OnMessage` → `dispatchFrame`
  method (pump internal sync semantics comments preserved: admin slot occupation/fwd-data frame order); 45-line `OnClose`
  → `cleanupConn` (fwdOut lock-outside-close order-sensitive comments); dedup decision → `dedupConn`
  returns `(loser, keepOld)`. `bindConn` now only orchestrates.
- `source/peer.go`: deleted outdated comment "serial attempts, future concurrent race" (race already live);
  race logic split into `collectPeers` (enumeration+TryLock) and `raceOpen` (race+collection),
  `raceResult` promoted to package-level type.

Hotspots (this commit):
| # | Optimization | Location | Verification |
|---|---|---|---|
| 1 | **Complete bitmap full check O(words)→O(1)**: upload calls Complete once per chunk (64KB), each 8GB = 130k chunks × 2048 word full scan = 260 million comparisons, all spent on single worker goroutine (fsync+hashFile prerequisite step) | `file_index.go UploadSession` added `fullWords` incremental count (setBit when word changes from non-full to full +1, repeated setting doesn't double count); Complete only checks fullWords count + last word mask (<64 chunks special case, =64 full), empty file special case | Added `TestFileIndex_FullWordsIncrementalBoundaries` (non-64 multiple/exactly 64 multiple size/repeated setting/empty file four boundaries) + all 13 existing TestFileIndex green |
| 2 | **routeResponse's reject closure → package-level `failFetch`**: each data frame once closure heap allocation (8GB transfer 130k times) | `outbound.go` | transport/source -race green |
| 3 | Dead code cleanup: `var _ = uploadChunkSize` ×2 (constants could be unused), duplicate comment blocks ×2, `UploadSession.seq` field (no readers) | `file_index.go` | Compile + full tests green |

**Known limitations (not fixed, reason recorded)**: `PeerSource.peerLocks` (sync.Map) only grows never shrinks
— after peer permanently offline lock entries remain (~60B/peer). Deletion requires serializing with concurrent Open's
LoadOrStore+TryLock race (otherwise new stream in deletion window gets old lock, another stream gets new lock
→ breaks connection-level expect single slot), complexity and risk far exceed benefit (private node network peer count
tens of peers, residual tens of KB). Keep only growing never shrinking, no cleanup needed.

### 3.16 Anon Collection Broadcast Permission Three Levels + AnonCreator Panel Regression Fix (2026-09-19)

**Requirement**: change anon collection "broadcast" from binary to three levels — **public access / restricted to specific permissions / only self**;
"restricted to specific permissions" pops regserver account list (avatar + nickname + @id, supports group quick share);
only public level shows "Save and Broadcast" button (goes through BT DHT announce + local seeding).

**Semantics (backend model/anon.go)**:

| visibility | Who can view | Null value fallback |
|---|---|---|
| `public` | Everyone | Historical collections without this field → treated as public (backward compatible) |
| `restricted` | Owner + accounts in `access_list` | Empty list → **400 on create**, doesn't silently create "nobody can open" collection |
| `private` | Only `owner` (= this node's regserver operator) | Node not logged into regserver (operator empty) frontend disables this level |

**Key decisions and pitfalls**:

1. **Permissions participate in digest → changing level necessarily produces new hash**. Collection is content-addressed, permissions written in
   JSON, `UpdateCollectionVisibility` copies original collection writing new file returning **new hash**;
   old hash still parses old permissions (snapshot semantics, not in-place modification). Frontend takes new hash as collection's new identity.
2. **`CanView("")` must be false** (restricted/private). Unauthenticated requests (empty account) cannot
   penetrate restricted levels, otherwise any P2P sync with a hash can drag away restricted collections. This is the most
   easily-mistaken line in this module.
3. **Privilege escalation always manifests as 404 rather than 403** (`GetCollectionVisibleTo`) — otherwise equal to confirming to attackers
   "this hash exists and belongs to someone else".
4. **All read/write entry points changed to visibility version**: `GetAnonCollection` / `DownloadAnonFile` / `ForkAnonCollection`
   changed from `GetCollectionByHash` to `GetCollectionVisibleTo(hash, nodestate.GetOperator())`.
   Local session injects this node's operator, so one's own restricted collections read normally.
   **Why fork also**: fork produces a new collection copy, if source read doesn't gate, getting any hash can fork
   a private collection into a public copy → three-level permissions bypassed by fork.
5. **Derived paths must inherit permissions** (missing this silently leaks):
   - `CommitCollection` originally only copied name/entries/tags → restricted/private collections
     **commit once reverts to public**; now unified through `inheritVisibility(dst, src)` copying
     Visibility/AccessList/Owner, and source collection read by visibility (non-viewer commit directly not found).
   - `ForkAnonCollection` produced copy also inherits source's visibility/access_list (Owner continues from source,
     when source has no Owner records as local operator), avoids "same content different hash becomes public".
6. **`access_list` is flat account name array, not hash of `/access/list`**. Earlier api.js sent
   `access_list_hash`, backend only reads `access_list` field → restricted collection list empty → 400. Fixed.
7. **Clear list when switching levels** (`pickVisibility`): otherwise "switched to restricted but list still has last time's people",
   backend receives stale list.
8. `AuthStatus` response added `operator` field: `username` is this request's caller,
   `operator` is account registered after node logs into regserver (Owner takes this), they're not the same thing;
   frontend needs it to determine if "only self" level is available.
9. **Account directory service absence doesn't block functionality**: `/reg/users`, `/reg/groups` unavailable returns empty arrays,
   `AccountPicker` degrades to manual `@id` input (UI explicitly says "no accounts available yet").

**Locations**:

| Layer | File | Change |
|---|---|---|
| model | `back/internal/controller/anon.go` | `Visibility`/`AccessList`/`Owner` fields + `IsValidVisibility`/`EffectiveVisibility`/`CanView`; `AnonCollectionSummary` backfills visibility/owner |
| service | `back/internal/service/anon_service.go` | `CreateCollectionWithVisibility` (restricted no list directly 400), `GetCollectionVisibleTo` (privilege escalation→404), `UpdateCollectionVisibility` (Owner validation + new hash), `saveCollectionJSON` |
| controller | `back/internal/controller/anon.go` | Create/detail/download/level switch connect visibility; `PUT /anon/collections/:hash/visibility` (mounted with authRequired) |
| controller | `back/internal/controller/p2p.go` | `AuthStatus` adds `operator` |
| repository | `back/internal/repository/anon_repo.go` | List summary backfills `EffectiveVisibility()` (empty string can't pass directly to frontend, otherwise three options no highlight) |
| router | `back/internal/router/router.go` | New visibility route added |
| Frontend | `front/src/api.js` | `createAnonCollection` changed to send `access_list` array; `listKnownAccounts`/`listKnownGroups` (can be absent degraded) |
| Frontend | `AnonCreator/index.jsx` | visibility/accessList/accounts/operator state; `pickVisibility`, `openAccountPicker`, `handleSave(broadcast)` pre-save validation |
| Frontend | `AnonCreator/VisibilityPicker.jsx` | Three-level switch (private disabled when no operator + reason hint) |
| Frontend | `AnonCreator/AccountPicker.jsx` | Nickname + @id list, group quick share, manual @id fallback |
| Frontend | `AnonCreator/EditorPanel.jsx` | Permission row + "📡 Save and Broadcast" button (only shown for public) |

**Panel regressions fixed in same batch** (`😅.txt` list): left source tags added back "Registered·by directory" and added
`RegisteredDirView.jsx` directory drill-down view; `FileTree` renders toolbar even when list empty (otherwise "New Folder" button entirely disappears when entries 0); "Registered (by file)" scope corrected to
`provider_path || provider_type` (old impl looked at `f.providers.length`, `FileListItem` doesn't have this
field → list always empty); time sort changed to `Date.parse` numeric comparison (RFC3339 strings directly localeCompare
mixes `+08:00` with `Z`).

**Verification**: back `go build -tags nosqlite ./...` + `go vet` + all package tests green;
`gofmt -l` no output on changed files. New tests
`back/internal/service/anon_visibility_test.go` (CanView all levels + unauthenticated reject,
restricted no list error, level switch produces new hash and old hash snapshot unchanged, Owner privilege escalation reject,
privilege escalation manifests as not found) and `front/tests/VisibilityPicker.test.jsx` ⚠️(该测试文件已不存在).

**Known limitations**: P2P sync path (remote peer pulling collections) doesn't yet carry requester identity, so remote
uniformly treats as `requester=""` — restricted/private collections currently only readable on this node, cross-node sharing
restricted collections awaits "registration auth service" online then bring account into sync requests (see `doc/modules/auth`).

### 3.17 Dedup Race Residual Window: Outbound Pull Adds One Retry (2026-09-19)

**Symptom**: `TestSelfHostedSignalAndDiscover` ~1/4 probability failure, reports
`self-hosted discovery pull failed: peerjs: connection closed` (10.05s, exactly stuck at the moment right after waitConnections
returns and immediately pulls).

**Root cause** (residual window from 3.15/multi-connection batch, not new bug): discovery phase both ends discover each other →
bidirectional mutual dialing → two connections under same peerID. `dedupConn` retention strategy consistent on both ends (connection-level UUID
lexicographically smaller wins), this solves "both ends mutual broken links left"; but there's a third window:
`bindConn` first writes new connection into `conns[peerID]`, then dedup decision on who to eliminate. Test's
`waitConnections` only checks if key exists in `conns`, once returns in the "already in conns, not yet decided"
window, immediately following pull sends on the **connection about to be eliminated**, `cleanupConn` closes it,
in-flight fetch immediately receives `connection closed`.

**Fix** (`transport/outbound.go`): `FetchFromPeer` adds one conditional retry —
- Only retries when error is "connection churn" (`isConnChurnErr`: connection closed /
  connection not bound / no connection to); content errors (hash mismatch, limit rejection,
  peer err frame) don't retry, retry won't help.
- Only retries once: when truly disconnected there's no replaceable connection in `conns`, second time fails immediately with same error.
- Waits 150ms before retry, lets dedup decision and `conns` re-pointing complete; uses `sleepCtx` to ensure service closure
  immediately returns, doesn't drag `Close`.
- Full retry rather than resume: `FetchFromPeer` semantics is "retrieve complete content" (returns single `[]byte`),
  restart won't produce half-data; offset/size chunked requests replaying same range equally safe.

**Why not change to "dedup doesn't close old connection"**: design explicitly requires "after connection replaced, old connection's
ongoing streams must error end" (`TestBindConn_ReplacedConnOldStreamErrors`, prevents hanging),
so fallback responsibility is on upper caller — switch to surviving connection and retry. The two rules are complementary, not contradictory.

**Verification**: `TestSelfHostedSignalAndDiscover -count=4 -p 1` 4 consecutive runs all green (before fix
single run reproduced); `internal/transport`, `internal/source` unit tests and -race all green.

**Known residual**: `source.PeerSource` (node passthrough origin fallback chunked read) goes through `OpenStreamFrom`
streaming reader, mid-failure consumed bytes can't be safely replayed, so **doesn't** add same retry;
its candidate enumeration comes from `conns`, same window can be hit. To truly eliminate need
"zero-byte failure reopens once" at `PeerSource.Open` race layer, left for later (current private node
scenario frequency extremely low, and upper HTTP download itself is retryable).

### 3.18 Interconnect Layer: Node-level "Presence Room" + Discovery Dial Budget (2026-09-20)

**Background**: user rearranged development order (see `doc/ROADMAP.md`), putting "PeerJS-based interconnect" first.
Inventoried interconnect layer per that order and found a blocking gap, fixed in this batch.

**Gap (blocking)**: discovery is **content-sharded** — `HTTPDiscovery` only announces/queries
collection hash rooms declared in `PEERDRIVE_MQTT_COLLECTIONS`. Default config that variable is empty,
so node **neither announces nor queries any room**, discovery completely idles: two default-configured nodes never
see each other, only manual static `PEERDRIVE_PEERJS_PEERS` config enables interconnect.
(Integration tests didn't expose this: `TestSelfHostedSignalAndDiscover` set same
`MQTTCollections` hash on both ends, effectively manually set up the room for the test.)

**Fix (client side, doesn't touch signaling server)**:
- `transport/http_discovery.go` added `PresenceRoom` constant = `sha256("peerdrive/presence/v1")`
  = `405265e5…d15a`; HTTP discovery additionally joins this fixed room, making interconnect layer independent of content sharding.
- `transport/peerjs_service.go`: `collectionHashes()` (content rooms, config only) on top adds
  `discoveryRooms()` = content rooms + presence room; added `discoveryDialAllowed()` /
  `maxPeers()`, discovery-triggered dialing constrained by `PEERDRIVE_MAX_PEERS` (previously **defined but never used**).
- Config: added `PEERDRIVE_DISCOVER_PRESENCE` (default true).
- New tests: `internal/transport/discovery_rooms_test.go` (5 groups), integration test
  `TestInterconnectViaPresenceRoom` (two nodes with zero shared collections interconnect via presence room only).

**Why presence room uses sha256 literal rather than readable name like `"_presence"`** (key decision):
room names get stuffed into announce's `collections` field. peerdrive's own `signalserver` only trims that field
without validation, but **online signaling maintained by wintools, implementation unknown** — once that side adds "must be 64hex" validation,
readable name causes **entire announce to be 400 rejected**, dragging content sharded rooms unable to register, discovery fully broken.
Cost (unreadable) far less than risk. sha256 preimage resistance also ensures it won't collide with any real content/collection hash.
Test `TestPresenceRoom_IsStrictSHA256` locks this constraint.

**Why dialing needs a cap**: content sharding naturally limits flow (only same-room nodes meet); presence room makes
"any node can discover any node", discovery-triggered dialing degrades to O(n²) full interconnect. Use idle
`PEERDRIVE_MAX_PEERS` to catch (default 8; `<=0` means unlimited). Static `PEERDRIVE_PEERJS_PEERS`
not limited — that's operator's explicit declaration. Budget calculation excludes `"local"` (browser direct local
WS session to this node is not a peer node).

**MQTT branch deliberately doesn't add presence room**: opening global room on public broker equals broadcasting this node online to public net,
not doing. Presence room only applies to self-hosted signaling HTTP discovery.

**Intentionally retained gap**: `collectionHashes()` only reads config, doesn't read locally stored collections (original comment falsely claimed
"config + local storage"). Broadcasting local collection hash equals publicly "what this node holds", restricted/private collections
would directly leak room names — must filter by visibility (only broadcast public) before doing, left for "file scope management"
phase (`doc/ROADMAP.md` phase 5).

**Not done (future)**: connection health observation (per-peer RTT / last received frame / reconnect count), capability-based peer filtering
(announce already has `nodeType`/`loadInfo` fields, node side currently sends constants).

### 3.19 Netdisk Chain: Node Market / Shared Scope / Cross-node Save / Netdisk UI / Pure Consumer (2026-09-20)

**Background**: user gave target form (see `doc/NETDISK.md`): frontend is normal netdisk UI (own nodes / others' nodes /
market join / see other's "file links" / select save) + one pure WebRTC consumer. Implemented by module branch (M1-M6),
each branch runs CI through then merges back to `refactor`.

**New frame verbs: `share` / `share-resp`**

```
Request {"type":"share","reqId":"…"}
Response {"type":"share-resp","collections":[…],"files":[…],"dirs":[…],"total":N,"reqId":"…"}
```

- **Don't reuse `list`** (key decision): `list` is local **management** index (full file_index, including local absolute paths),
  semantics is "which files am I managing"; `share` is operator **explicitly declared** external scope. Mixing equals default full disk public.
- Shared not enabled returns **empty array** rather than `err`: empty state is legitimate business state (other didn't share), frontend renders "no content" accordingly.
- Doesn't contain requester identity (ROADMAP hard constraint: no accounts before phase 7), so only returns content that was already allowed to be public
  (restricted/private collections uniformly skipped — publishing equals making public).
- Response frame fields (`collections/files/dirs`) not in `dcResp`, so Go side one-shot verb wait slot
  `connState.verbWaits` stores **original JSON bytes**, not parsed structure (re-marshaling would lose fields).
  This is also why "one-shot verb wait slot" and "streaming fetch state machine" are separate: latter needs to handle data blocks,
  former just needs one JSON to end.

**announce only reports share count**: `loadInfo.shares = {collections,files,dirs}` (**only count, no hash**).
announce gets broadcast by discovery server to all queryers, reporting hash equals publicly "what this node holds"; count is enough to support
market card guiding info, specific lists only obtained via `share` frame after point-to-point direct connection. This also qualitatively resolves
§3.18's "broadcast local collection hash" TODO.

**Cross-node pull save (`service.PeerPuller`)**

- Landing location: `<DownloadDir>/pulled/<relative_path>.part` → sha256 verify → rename → `file_index.Create` register.
  **Don't write CAS copy**: after registration content can appear in "my files", also can be continued `serveFile` by this node to other nodes
  (integration test `TestPeerPullSavesToLocalDrive` verified from C pulling from B), writing another CAS copy is second landing of same content.
- All endpoints are **static paths**: `GET/POST /p2p/pull`, `POST /p2p/pull/collection`, `POST /p2p/pull/cancel`.
  Pitfall: gin **doesn't allow static segment and parameter segment at same level** (`/p2p/pull` and `/p2p/pull/:id` panic),
  cancel therefore changed to body passing id instead of `:id`.
- `fetchState.total` changed to `atomic.Int64`: meta frame written by message pump, `fetchReader.Total()` read by consumer
  goroutine, normal field is data race (`-race` reports).

**Frontend (M4)**

- Added `Fill` container (`App.jsx`): netdisk page is `flex flex-1 min-h-0`, while `<Routes>`'s parent is **block-level** container
  — without adding a `h-full flex` layer in between, in-page `overflow-y-auto` can't get definite height, content gets
  cut by `overflow-hidden` instead of scrolling. Existing pages have their own `h-full`, so didn't modify them (only wrapped new routes).
- `/peers/:peer` doesn't conflict with existing `/:username/:collName`: react-router v6 sorts by specificity, static segments first,
  unrelated to declaration order.
- Preview not heavily reworked: existing `AnonExplorer/*Preview` components coupled with collection entry structure, netdisk files are file_index
  entries (different shape), so go through `api.getBlobUrl` + new window open, avoids reworking two places for one entry.

**Test infrastructure fixes (worth recording separately)**

- `front/src/__mocks__/api.js` is **hand-written** mock (not automock), drifted with api.js evolution:
  missing 8 exports, extra 18 zombie exports. Manifests as pages getting `undefined` in tests, then dying at call points far from cause
  (`Cannot read properties of undefined`).
- Fix: complete + clean, and added `front/tests/api-mock-sync.test.js` as **bidirectional** guard
  (real module's export list vs hand-written mock; both missing/extra turn red). This guard caught 8 missing in this round.
- Tests needing to assert "what does clicking save actually send to backend", use file-level `vi.mock('../src/api.js', () => ({...vi.fn()}))`
  to override setup.js's hand-written mock (hand-written mock is plain function, can't assert or inject return values).

**Pure WebRTC Consumer (`packages/peerdrive-client`)**

- **Transport-agnostic** (key decision): `PeerDriveClient` only requires passing `{on(type,cb), send(data), open, close}`,
  doesn't know PeerJS. Benefits: package zero dependencies, doesn't pollute consumer bundle size; tests cover full state machine with fake connection
  (no signaling server or real WebRTC needed); future switch to raw `RTCPeerConnection`/WebTransport doesn't need to change this file.
- Replicates Go side's correctness constraints item by item: connection-level expect (binary blocks attach to **most recent** data header's request,
  blocks don't carry reqId — so two requests' blocks **cannot interleave sending**), `done` byte count comparison (prevents silent truncation corruption),
  block size limit, hash must be 64-bit lowercase hex (local immediate reject, otherwise error appears a roundtrip late).
- **Self-implemented incremental SHA-256** (`src/sha256.js`): `crypto.subtle.digest()` is **one-shot**, must first
  accumulate entire content in memory before computing digest — directly conflicts with streaming pull. Incremental implementation enables "compute as receiving", cost is pure JS ~
  30–80 MB/s; one-shot path `sha256Hex()` still prefers WebCrypto. Byproduct: pure JS doesn't require secure context.
- **Memory gate**: `fetch()/saveAs()` fully resides in memory (browser Blob download only way), default 256MB,
  exceeded throws `TOO_LARGE` and guides to `stream()`; peer's size declared in `meta` is **blocked at meta stage**, no wasted download.
- Cancel semantics honestly annotated: protocol has **no cancel frame** (Go side also no corresponding implementation), early `break` only locally drops frames and
  releases cached blocks, peer will finish this request; true interruption only by closing connection.
- Doesn't implement `list` frame: that's node's local management index, by design only open to trusted peers.
- `serialization` must be `'raw'` (same pitfall as peerdrive-media): otherwise data blocks wrapped by peerjs's own
  chunker, peer can't parse them.

**Verification**: back unit tests all green; integration `-p 1` all green (new `TestNodeMarketListsDiscoveredPeer` /
`TestShareProtocolContract` / `TestPeerPullSavesToLocalDrive`); frontend vitest 88/88 + `vite build`;
consumer `node --test` 60/60. Complete list of plan vs actual deviations see `doc/NETDISK.md` §6.1.

### 3.20 CI Green: Two Existing Red Flags (2026-09-20)

After merging netdisk module and pushing `refactor` to trigger CI, found repo has **two** workflows
(`ci.yml` and `go-build.yml`) each with one red. Both existed at `9e4ede3`,
unrelated to netdisk changes, but main branch must be green to count as "verification passed", so fixed together. Complete table see
`doc/NETDISK.md` §6.4, here only recording **reusable lessons**.

#### (1) `media-package`: `npm ci` reports lockfile missing react

`react`/`react-dom` in `packages/peerdrive-media` are only **optional
peerDependencies**, but `@vitejs/plugin-react` treats react as **required** peer →
after npm 7+ auto-installing peers the computed ideal tree contains `react@19.3.0`, but lockfile doesn't have it →
`npm ci`'s sync check fails directly (EUSAGE).

Fix is to add them to `devDependencies` (standard practice for libraries with peerDependencies:
local dev/build installs, consumers still use their own peer declaration), then
`npm install --package-lock-only` to recompute.

> Pitfall: `npm install` under local proxy **hangs for tens of minutes** (full reify makes hundreds of requests),
> but `npm install --package-lock-only` only resolves metadata, done in seconds;
> then `npm ci --dry-run` to verify sync, run `npm ci` once to verify artifacts.
> Also: `npm run build` locally reports "Cannot find native binding" is **npm optional
> deps bug** (npm/cli#4828) causing rolldown's 14 platform binaries not installed at all,
> explicitly adding `@rolldown/binding-linux-x64-gnu` passes — not a repo defect.
> Also note vite 8 requires node `^20.19 || >=22.12`, local WSL default 22.9 gets EBADENGINE,
> use nvm's 22.23 to replicate CI's real environment.

#### (2) `internal/source` race test case occasional failure

`TestPeerSource_WinnerPeerLockReleased` reds at ~1/4 probability on CI's macos/arm64,
was once considered "platform-specific". **Actually not** — local Linux `go test -count=400` reproduces
~1%-2%, slow machines just amplify the probability.

Root cause (confirmed via instrumented tracing): `raceOpen` returns on win, **loser candidate goroutines may still be
in flight holding that peer's slot** (lock released by background collection goroutine after closing stream — deliberate design).
Test third round directly opens → third round `collectPeers` sees peerB BUSY → degrades to
single peer serial path → but peerA already `failSend` → reports `peer peerA: assert.AnError`,
completely misaligned with "is lock released" assertion premise.

Fix: added `waitPeersIdle(t, ps, want)` (TryLock probing, release on get, 3s deadline),
wait once after first/second rounds, replacing original fixed `sleep(50ms)` that only covered first round.
**Production code unchanged** — race "not waiting for losers" is deliberate. Real lock leak will surface as clear Fatal
exposure, actually strengthening test semantics.

> Pitfall (important): **don't write stderr for instrumentation**. `fmt.Fprintf(os.Stderr, ...)` I/O changes
> goroutine scheduling, directly masking 1-2% race (600 runs all green, no evidence found).
> Change to **in-memory ring buffer** (locked append, capacity limit), dump on failure.
> Similarly, `-race` may not reproduce due to timing changes (this -race 300 runs all green),
> can't use "no red under race" as evidence of "no race exists".

#### (3) `.gitignore`'s `react/` swallowed media package's React source

After fixing (1), `npm ci` passed, job proceeded to `npm run build`, immediately reported
`[UNRESOLVED_ENTRY] Cannot resolve entry module src/react/index.js` — local can
build, CI fresh checkout can't find files. Reason: `.gitignore` "Root temp files"
section writes `react/` / `go/` without leading slash, **matches same-named directories at any level**, so
`packages/peerdrive-media/src/react/` entirely ignored, those 5 source files never committed;
local disk has files so never exposed.

Fix: anchor as `/react/` and `/go/` (consistent with same section's `/docs/`). `dist/react/` still covered by
`dist/`.

> Lesson: **red flags mask each other**. When one job stuck at first step, later steps may have long been broken.
> After fixing one place rerun entire chain, don't assume "the rest were already good".
> Also `.gitignore` writing "root directory temp dirs" must add leading `/` — without `/`
> directory rules match **at any level**.

### 3.21 Collection Module: Content-Addressed path+sha+preview JSON (2026-10-09)

独立后端模块 `back/internal/collection/`（仅主模块 internal 用 → internal 包，参照 hashmap 先例；
peerjs/signalserver/p2p_bt/signalframe 是独立库，无人会 import 它，故不建子模块）。

- **Collection 本体 = JSON**：`{version, entries:[{path, sha, preview}]}`。`preview` 是条目预览图/
  缩略图文件的 sha（可为空 = 无预览）。刻意极简——与 SQLite `collections` 表 / `AnonCollection`
  （visibility/owner/tags/providers/版本链）区分，本模块只有 path+sha(+preview)。
- **存 sha-文件系统**：`storageDir/<sha[:2]>/<sha>`，与 `anon_repo.SaveCollection`、`source.LocalSource`
  CAS 回退、transport serveFile 同一布局。sha = **canonical JSON** 的 sha256（entries 先按
  path/sha/preview 排序 → 同内容必同 sha，内容寻址确定性）。写路径走 `pathutil.SafeWriteFileAny`
  （AGENTS 硬要求），不依赖 repository/DB。
- **读出/响应**：`Load/ReadJSON(storageDir, sha)` 本地读出；peerjs 通道**零改动**可用——collection
  就是普通 sha 文件，对端 `req` 帧经 LocalSource 未命中 file_index 时回退 CAS 即取到（与
  NETDISK §M3 `FetchManifest` "content-addressed JSON, fetch by hash through the same req channel" 一致）。
- **校验**：sha/preview 一律 strict 64hex（拼 CAS 路径用，大写会断路径）；path 非空且唯一；
  Load 校验内容 sha 与地址一致（防文件与地址不符）。

### 3.22 PeerJS Data-plane XOR Obfuscation (2026-10-09, feat/peerjs-xor)

`back/peerjs` 库层给 DataChannel 数据面加 XOR 混淆（轻量保护，非强加密）。
设计契约（详见 `back/peerjs/xor.go` 头注释）：

- **加密点**：库层 `Connection` 的发送侧（Send/SendText/SendFrame）与收侧
  （attach 的 OnMessage）——数据面上**全帧**（text+bin：req/meta/data/done/err/
  psk-auth/fwd/admin 等）统一对称 XOR。业务层（transport）零改动，帧协议原样。
  候选对比：业务层 data 帧 payload 只保护文件内容、JSON 头（hash/offset/size/
  reqId）仍明文且其它 verb 全漏；pion dc 层无统一收口。选库层：单一收口 +
  "peerjs 传输数据"字面全覆盖。
- **密钥**：每连接派生 `key = SHA-256(secret || ":" || connID)`。connID 由
  offerer 生成、经 OFFER 传给 answerer 复用（既有信令握手），两端 newConnection
  即得同一 key，**零额外握手**。secret 本地配置（`PEERDRIVE_PEERJS_XOR_KEY`），
  不走信令交换（信令面 signalframe 保持明文）。
- **开关**：`Options.XOREnable/XORKey`，默认关 = 恒等，线缆字节与现状一致，
  浏览器 peerjs / peerdrive-client / media 互操作不受影响。开启要求两端同版本
  同 secret；错 key 解出垃圾 → 文本帧 JSON 解析失败被丢 → 取文件确定性失败
  （不静默损坏）。主模块 env：`PEERDRIVE_PEERJS_XOR_ENABLE`（默认 false）+
  `PEERDRIVE_PEERJS_XOR_KEY`；Validate 拦"开了没配 key"。
- **流式边界**：帧级独立加密（每帧从头按 `key[i%len(key)]`，无跨帧状态），
  serveFile 64KB 分块各自独立、块边界天然对齐。XOR 自逆，编解码同一函数。
- **测试**：peerjs 模块表驱动（已知向量/round-trip/派生一致性/错 key/流式分块/
  关=恒等）+ fakeDC 线缆断言 + **转发式信令 + 本机 WebRTC 真实双 peer**
  （同 secret 往返明文一致；错 secret 传输照常、应用层垃圾，`-race` 5 连跑）；
  主模块集成 `TestXOR_TwoNodesFetch_ShaMatch`（全量+分片取回 sha 一致）、
  `TestXOR_ConcurrentFetch_Race`（8 路并发取回）。
- **取舍**：XOR 是混淆不是加密（同明文同 key 同密文、模式可统计），定位
  防明文嗅探/偶然窥视；防定向破解用 PSK + 可信信令。**镜像同步**：改动在
  `back/peerjs/`（独立 repo `github.com/Hana-ame/go-peerjs`），合并后需按
  §3.14 流程镜像。

### 3.23 全链路 E2E 验证、安全漏洞扫描与深度加固 (2026-10-10)

本轮针对端到端可靠性与安全防线进行了全链路扫描、漏洞修复与自动化测试补充：

1. **端到端生命周期与安全门禁测试 (`back/test/integration/e2e_full_chain_test.go`)**：
   - `TestEndToEnd_FullChainLifecycle`:
     - 3 节点（Node A Publisher -> Node B Peer -> Node C Secondary Consumer）真实自托管信令与 WebRTC DataChannel 全链路；
     - 双向对称 Capability 握手（`CapHighThroughput`, `CapP2PTun`, `CapReq`, `CapShare`）；
     - WebRTC 直接分块流式拉取（300KB 超大跨分块 payload）；
     - 受密码保护的合集门禁（`RequestSharesWithPasscode`，无口令隐藏条目，有口令解锁条目）；
     - 流式落盘与内容寻址校验，写入本地 `file_index`；
     - 本地重复拉取跳过（Deduplication）；
     - 节点级级联中继（Node C 自动从 Node B 本地驱动拉取，Node B 自主提供权威文件内容）；
     - 断点续传测试：预埋未完成 `.part` 文件，从已有字节偏移处继续续传，校验最终哈希一致。
   - `TestEndToEnd_TamperAndResumeResilience`:
     - 校验网络传输中载荷篡改被拒绝、临时 `.part` 文件被自动清理、篡改内容拒绝进入 `file_index`。
   - `TestEndToEnd_SecurityBoundariesAndAccessPolicy`:
     - 跨节点受保护合集口令强校验、错误口令拦截、不存在哈希探测的优雅报错与无 panic 防护。

2. **安全漏洞排查与防御加固**：
   - **SSRF 与 DNS Rebinding 防护 (`back/pkg/urlguard/urlguard.go`)**：
     - 新增 `SafeDialContext`、`NewSafeTransport`、`NewSafeClient`，在底层 Socket 连接建立的瞬间（Dial layer）对解析到的所有候选目标 IP 进行二次校验，封堵通过 DNS Rebinding 绕过外层 URL 检查的攻击路径。
     - 统一接入 `FileService.ResolveURL`、`FileService.ReadFile`、`PeerJSService.fetchIntoIndex`。
   - **URL 外部解析内存耗尽（OOM DoS）防护 (`back/internal/service/file_service.go`)**：
     - `ResolveURL` 增加 5 分钟硬超时与 `io.LimitReader(resp.Body, maxLimit+1)`，严格根据 `MaxUploadBytes`（默认 100MB）做流式截断，防止远程恶意超大响应撑爆内存。
     - `ReadFile` 入口增加 64 位 strict hash 格式校验（防空串/短串切片 panic），本地 provider 增加 `isPathAllowed` 与 `openAllowed` 校验，HTTP provider 接入 safe client 与 limit reader。
   - **远端目录遍历递归深度上限 (`back/internal/source/openlist_crawler.go`)**：
     - `CrawlDirectory` 递归遍历增加 `maxCrawlDepth = 32` 与 `visited` 环路检测，防止恶意循环软链接或深层嵌套引发调用栈溢出（Stack Overflow）。
   - **受保护合集口令与权限继承 (`back/internal/service/anon_service.go` & `controller/anon.go`)**：
     - 修复 `inheritVisibility` 遗漏 `AccessPolicy` 与 `Passcode` 导致 Commit 生成新版本后密码保护失效的重大漏洞。
     - 口令校验全面改为 `subtle.ConstantTimeCompare`，防范计时侧信道攻击。
     - 修复 `GetAnonCollection` 在解锁状态下回显明文 `passcode` 的数据泄露漏洞；无论任何状态，API 均不暴露明文口令。
     - 补全 `ForkAnonCollection` 与 `CommitAnonCollection` 对受保护合集的口令准入鉴权，未提供有效口令者拒绝 fork 或 commit（403 Forbidden）。
   - **路径穿越与文件名规范化 (`back/internal/service/file_service.go` & `controller/download.go`)**：
     - `Upload` 与 `RegisterLocal` 对传入的文件名强制执行 `filepath.Base(filepath.Clean(name))` 过滤，彻底剥离 `../` 等目录穿越字符。
     - `FileService` 导出 `OpenAllowed` / `IsPathAllowed`，`DownloadBySHA256Local` 统一改用 `OpenAllowed`，杜绝历史脏数据或恶意数据库注入导致读取 storage 根目录外的越权文件。

3. **CI 测试流水线增强 (`.github/workflows/ci.yml` & `scripts/test-layers.sh`)**：
   - `ci.yml` 引入独立的 `security-probes` job，包含：
     - 四层穿透测试矩阵（162 条单测用例，覆盖 pathutil、transport、service、controller 层）；
     - 真实节点路径穿透探针（`scripts/netdisk-traversal-probe.sh`，直接向运行中的节点发动穿透 payload 测试）；
     - Storage 外部共享目录全链路验证（`scripts/netdisk-sharedir-outside.sh`）。
   - `scripts/test-layers.sh` 引入 `L-sec` 安全与穿透防线层。

4. **组合型循环压力与混沌 E2E 测试 (`back/test/integration/e2e_combinatorial_test.go`)**：
   - `TestEndToEnd_CombinatorialRepetitionAndStress`：
     - 针对用户需求「组合，重复多个操作步骤以获得」，构建 4 轮连续循环深度组合测试；
     - 涵盖多尺度动态载荷合成、公开与口令受控合集动态装配、Git 风格分叉变体（CommitCollectionWithPasscode）与不可变性校验；
     - 动态共享范围发布、WebRTC 远程清单多级口令门禁准入探针；
     - 跨节点分块流式拉取落盘与 `file_index` 注册；
     - 内容寻址去重（Dedup Skip）在重复拉取下的幂等性验证；
     - 远程访客隔离收件箱（Inbox Quarantine）沙箱流式写入，以及宿主节点交替执行「审核批准入库（Approve）」与「拒绝物理清除（Reject）」的严密状态机校验；
     - 下游二次节点级联服务（Node A -> Node B -> Node C）可靠性；
     - 周期性注入 5 路高并发切片读取与损坏哈希混沌扰动，确保 WebRTC DataChannel、`sendMu` 与工作池维持零死锁、零状态污染；
     - 轮次终点断言所有临时 `.part` 文件 100% 自动清理，杜绝资源泄漏。

## 5. E2E Pitfalls Encountered (All Fixed)

| Pitfall | Fix |
|---|---|
| answerer generates new connectionId → ANSWER routing fails, ICE stuck checking | answerer must reuse offerer's connectionId |
| pion doesn't auto-send ICE candidates → both sides forever checking | `OnICECandidate` → signaling CANDIDATE manual forwarding |
| `dc.Send([]byte)` sends binary frame, JSON header treated as data block discarded | Headers use `SendText` |
| Peer not online OFFER queued expiry (EXPIRE) → forever waiting for OnOpen | On EXPIRE close connection, connectLoop loops reconnect |
| No retry after reconnect failure (connectLoop one-shot exit) | Infinite loop + exponential backoff |
| Browser test timeout: chromium doesn't use system proxy / about:blank has no crypto.subtle | chromium explicit `--proxy-server`; sha256 computed on Node side |
| **Calling `conn.Close()` while holding lock deadlock** (Go mutex non-reentrant): handleOffer duplicate connectionId cleanup, handleLeave closing peer connection | Only collect inside lock, Close after unlocking |
| **WS concurrent write panic**: `gorilla/websocket` doesn't allow concurrent WriteJSON, heartbeat/ICE candidates/ANSWER multi-goroutine concurrent (3-node interconnect test triggered) | signaller added writeMu serialization |
| **Concurrent flow control deadlock**: old impl each serveFile registered own `OnBufferedAmountLow` (pion replacement callback) — concurrent requests only last registrant receives low water level event, others deadlock waiting when bufferedAmount exceeds threshold (4 concurrent × 2MB integration test reproduced, before fix stuck until timeout) | Flow control sunk to `peerjs.Connection.SendFrame` (attach-time global single callback registration + lowWater broadcast), serveFile zero flow control code; waiting available uses c.done to exit (connection close no hang) |

## 5.1 Test System

Unit tests (no network, run under race):

```bash
cd back/peerjs && go test ./... -count=1 -race
```

Coverage: connectionId reuse, duplicate OFFER cleanup, EXPIRE/LEAVE close, Close idempotent,
SendFrame concurrent atomicity (8×50 rounds verify header-body non-interleaving), **SendFrame built-in flow control
(high water level blocking → low water level recovery; connection close exit no hang)**, text/binary frame types,
remote close cleanup, ICE config entry.

Integration tests (external-network-free, global self-hosted signaling — see §3.12 item 6; run under race):

```bash
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -race
```

**Must `-p 1` serial**: multiple test groups share global self-hosted signaling server, parallel interferes with each other (signaling
registered IDs/rooms visible to each other). MQTT public broker tests require `PEERDRIVE_MQTT_TEST=1` explicit
gating (default skipped, external-network-free); online environment tests `PEERDRIVE_LIVE_TEST=1`; no UDP sandbox
(docker default) use `PEERDRIVE_SKIP_RTC=1` to skip WebRTC interconnect (local WS/admin types
unaffected).

Coverage: dual-node interconnect+range pull, 3-node pairwise interconnect, 4-node star one-to-many concurrent pull,
**4 concurrent × 2MB large file pull (flow control deadlock regression, before fix stuck until timeout)**,
MQTT sharded mutual discovery (including 60s heartbeat fallback timing, gated external network), MQTT discovery→PeerJS interconnect→
full pull file chain (gated external network), local WS session pull + FetchFromPeer("local") bidirectional reuse,
self-hosted signaling protocol compatibility (peerjs client module direct), self-hosted discovery API interconnect pull file,
Start/Close race pressure (30 rounds, -race regression).

## 6. Old Code Disposal (see doc/archive/LEGACY.md for details)

- libp2p stack (p2p.go/transfer/resume/multipeer/dual/ws/signaling/relay...): ✅ **Deleted** (2026-08-16 batch 2, replaced by PeerJS)
- BT stack (p2p_bt/): ✅ **Independent into library** `github.com/Hana-ame/go-peerdrive-bt` (back/p2p_bt is its source, go.mod replace reference).
  Original README said "can be used independently" was **wrong** (depends on `internal/log`, `PutImmutable` local store priority masks network failures,
  `putLocal` depends on anacrolix internal behavior) — that assertion corrected on 2026-08-18
- WebDAV/forward/auth dead code: ✅ **Deleted** (2026-08-16, WebDAV no auth arbitrary read/write/delete; forward rebuilt as PeerJS version)
- Frontend ~4000 lines dead components: ✅ **Deleted** (2026-08-16, see doc/archive/LEGACY.md F section; includes FileManager/WebRTCTransfer/
  old P2P status panels/localDB etc 21 files + api.js dead export cleanup; CollBrowserNav errata retained)
  Simultaneously fixed a batch of active main-chain bugs (merge/move semantics/race guards etc, see doc/archive/REVIEW-FIX-2026-08-16.md second round)

## 7. Target Package Structure (Dependency Layering, Incremental Migration)

```
internal/
├── domain/        Layer 0 domain model (zero deps) — future split model into collection/file/peer
├── config/ log/   Layer 0 infrastructure leaves
├── repository/    Layer 1 persistence (only depends on domain)
├── provider/      Layer 1 file fetch abstraction (consolidates local lookup copied 6 times in service)
├── service/       Layer 2 use case orchestration (only depends on domain/repository/provider/transport)
├── transport/     Layer 2 interconnect transport (peerjs_service + discovery/ migrated in)
└── api/           Layer 3 HTTP (original controller only depends on service) + router assembly
```
> ✅ After 2026-08-16 batch 1/batch 2: `legacy/` all deleted (webdav/forward→v2/test tools/batch 2 full stack).
> `internal/downloader/` (original universal_downloader) is independent downloader (local/ipfsgw/btdht/http).
```

Migration order: M0 dependency rules doc → M1 legacy isolation → M2 collect controller cross-layer deps → M3 split transport → M4 provider landed.


### §8 Dependency Rules (M0, established 2026-08-16)

Hard rules (enforced via code review + doc dual channel):
1. **No import `internal/p2p_bt`** (except p2p_bt library itself and cmd/test entry points; `internal/legacy`
   deleted in 2026-08-16 batch 2, this rule auto-upgrades to "legacy no longer exists").
2. Package hierarchy unidirectional: `model ← repository ← provider ← service ← controller ← router ← cmd`,
   `transport` and `provider` same level (can be referenced by service/controller, not reversed).
3. `service` package doesn't directly import `transport`; cross-layer uniformly through controller assembly injection.
4. Exit conditions: all new package tests pass; `go build -tags nosqlite ./...` all green.
5. ✅ Achieved: legacy stock references zeroed in 2026-08-16 batch 2 (webdav/forward/libp2p endpoints deleted).

**Migration status (2026-08-16)**: M2 ✅ Complete · M3 ✅ Complete · M4 ✅ (provider landed) · M1 ✅ Complete (p2p_bt split into independent library counted separately).

M1 legacy isolation key points (completed this round, internal/legacy/ landed):
- 22 files migrated from service to legacy package: libp2p stack (p2p.go/transfer/resume/multipeer/dual/ws/
  helpers/connection/key + tests), signaling (signaling.go), relay (relay + relay_registry),
  registration (node_registrar), scanning (peer_scanner/peer_tracker), IPFS (ipfs_service/ipfs_compat),
  webdav, forward, universal_downloader (P2PService-dependent download stack core).
- legacy dependency surface converged to config/log/model/nodestate/p2p_bt/provider/repository/hashutil
  (layer 0/1), service package zero legacy reverse references besides circular deps.
- Transition residuals: service/file_service + sync_service, controller/{p2p,signal,download},
  router, cmd/server still reference legacy (old stack endpoints kept until deletion decision);
  test-p2p-colls / test/bt-integration old tools changed references.
- ✅ Follow-up: p2p_bt split into independent library (5fb1193, README assertion holds); webdav/forward deleted (6bfc000/e030216);
  libp2p+IPFS full stack deleted (a5b090d); legacy package zeroed (downloader migrated to internal/downloader).

M3 layer collection key points (completed this round, transport package landed):
- Created `internal/transport/`: PeerJS file service subsystem migrated entirely —
  `peerjs_service.go` (interconnect + frame protocol server-side), `file_index.go` + `file_index_verbs.go`
  (sha256 file index + req/meta/data/done/err business verbs), `ws_session.go` + `rtc_session.go`
  (Session abstraction: local WS / WebRTC DataChannel dual impl), `mqtt_discovery.go` +
  `http_discovery.go` (discovery components).
- transport dependency surface converged to `config/log/repository/pkg/hashutil` (layer 0/1), no longer touches
  service package; `pkg/hashutil` added `IsStrictSHA256` (strict lowercase 64 hex, replaces original
  service package isValidHash usage in transport layer).
- External assembly (router/peerjs_routes, cmd/server main) changed to reference `transport.*`;
  tests migrated with (file_index_test / peerjs_service_test), transport↔service no circular deps.

M2 layer collection key points (completed this round):
- controller no longer imports repository: collection/share/task/pin direct calls all collected into service —
  `CollectionService` (collection_service.go, including fork/merge/version/anonymous collections),
  `ShareService`, `TaskService`, `PinService`; download/file controllers changed to use FileService
  (added GetMeta/GetMetaByCID/ListAll/ImportGatewayData/RegisterBTFile).
- Domain types moved up to model: `FileTypeBlob/FileTypeAnonCollection`, `IPFSPin`;
  repository retains aliases for compatibility.
- router no longer writes DB inline (BT onComplete callback collected into FileService.RegisterBTFile);
  router only retains DI assembly like SyncRepository.
- collection.go's direct SQL ListPublicCollections collected into repository.ListPublicCollections.

## 8. Environment and Verification

```bash
cd back
go build -tags nosqlite ./...          # must include nosqlite (dual SQLite driver CGO conflict)
go test -tags nosqlite ./...
# Integration tests (external-network-free from 3.12 item 6: global self-hosted signaling + same-machine WebRTC; must -p 1 serial):
go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1
#   External network tests explicitly gated:
#   PEERDRIVE_MQTT_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestMQTT -v
#   PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v (no proxy run)
# No UDP sandbox (docker default) skip interconnect: PEERDRIVE_SKIP_RTC=1
# E2E manual verification (needs external network):
#   A/B nodes each set PEERDRIVE_PEERJS_ID, B sets PEERDRIVE_PEERJS_PEERS=pd-node-a
#   curl -X POST localhost:PORT/peerjs/fetch -d '{"peer":"pd-node-a","hash":"<64hex>"}'
# admin verb smoke (no external network needed, local server start only):
#   PEERDRIVE_STORAGE=/tmp/pd-storage PORT=3000 go run ./cmd/server/ &
#   node front/tests/e2e-admin-smoke.mjs   # connects /ws/peer for admin full chain (ping/upload/download/collections/404)
```

**Build environment pitfall**: go commands need `HTTPS_PROXY=http://172.29.80.1:10809 GOPROXY=https://goproxy.cn,direct`
(WSL outbound goes through host machine proxy, opencode environment unsets proxy). cloudcone 443 exception (direct connection).
