# Module 09: transport P2P Transport Layer

- **Code location**: `back/internal/transport` (PeerJSService assembly layer + frame protocol + roles + discovery + file index; `back/peerjs` is an independent library, see module 10)
- **One-line function**: PeerJS/WebRTC data plane file service — a full-duplex Session (WS or DataChannel) carries a unified frame protocol (pull/index/share/admin/forward/PSK), combined with HTTP/MQTT node discovery, enabling browsers and nodes to interconnect via public signaling for sha256 content access.
- **Dependencies**: `github.com/Hana-ame/go-peerjs` (signaling + DataChannel, `peerjs_service.go:26`), `github.com/pion/webrtc/v4` (ICEServer type, `peerjs_service.go:24`), `github.com/gorilla/websocket` (local WS sessions, `ws_session.go:7`), `github.com/eclipse/paho.mqtt.golang` (MQTT discovery, `mqtt_discovery.go:11`), `github.com/google/uuid` (reqId, `outbound.go:21`); internal packages `config` (`peerjs_service.go:27`), `log`, `pathutil` (`file_index.go:15`, `inbound.go:19`), `repository` (file_index persistence delegation, `file_index.go:16`), `pkg/hashutil` (`peerjs_service.go:29`); `Session` interface (`ws_session.go:22-29`); `FileRouter` interface implemented by external `source.Manager` and injected (`inbound.go:46-51`).
- **Depended upon by**: `cmd/server/main.go` (assembly injection: `SetExtraPeers` L124, `SetShareProvider/SetShareGate` L156-158, `SetFileRouter` L233, `SetForwardRules` L210, `Start/Close` L110-111); `back/internal/router` (`registerPeerJSRoutes` registers `/peerjs/*`, `/ws/peer` and at L182-183 creates `NewWSSession("local")` + `BindLocal`; `router.go:402-415` injects gin internal forwarding via `SetAdminHandler`); `back/internal/router/peerjs_routes.go:142` and HTTP layer consume `FetchFromPeer`; `service.NodeShare` (injects share snapshots/gate, `nodeShare → transport.SetShareProvider`), `service.NodeDirectory` (`extraPeers` persistent peer list), `service.PeerPuller` (consumes `OpenStream`/`FetchFromPeer`, `source/peer.go`); `source.LocalSource` reuses `FileIndex()` (`source/local.go:78-80,157-180`); consumer/browser frame protocol peer; tests in `back/internal/transport/*_test.go`, `back/test/integration/*`.

## 1. Logic

**Module positioning**: Frame protocol core + connection-level state machine + bidirectional role assembly (`conn.go:3-11` header comments). **inbound/outbound are "frame roles" not connection directions** — WebRTC connections are full-duplex symmetric; the same Session carries both roles simultaneously (one side answers peer req, the other collects responses for its own requests); connection sharing mechanism is only one copy, must not duplicate when splitting roles (`conn.go:5-11`):

- `peerjs_service.go`: PeerJSService assembly layer (lifecycle Start/Close/startLoop, connection establishment connectLoop/onIncomingConnection, local session binding BindLocal, `peerjs_service.go:3-14`)
- `conn.go`: Connection sharing core (frame types, connState, bindConn dispatch, binary chunk routing, flow control wiring, dedup, cleanup)
- `inbound.go`: Inbound role (answers verbs: serveFile / serveCreate / serveUploadBegin / serveList / serveInfo / serveDelete / serveSync / servePull / serveShare, plus uploadWorker/fwdWorker)
- `outbound.go`: Outbound role (FetchFromPeer / OpenStream / requestVerb / routeResponse)
- `ws_session.go` / `rtc_session.go`: Direction-neutral transport (two implementations of `Session` abstraction)
- `file_index.go`: Direction-neutral persistence (shared by both roles)

**Session abstraction** (`ws_session.go:15-29`): `ID()/SendJSON/SendFrame/OnMessage/OnClose/Close`. Both implementations have identical semantics:
- `rtcSession` adapts `*peerjs.Connection` (DataChannel; `rtc_session.go:11-14`), `ID()` = remote peer id, `ConnID()` = connectionId (dedup key, `rtc_session.go:20-24`);
- `WSSession` wraps local `/ws/peer` WebSocket (`id=="local"`, `ws_session.go:34-42`; connection point `router/peerjs_routes.go:158-184`). Three session identity semantics: **"local" is browser's direct management channel to this node** (`peerjs_service.go:46` comments; `IsLocal()==true` treated as "self", `ws_session.go:88`, `share.go:120-123`).

**Frame protocol** (`conn.go:13-26`, shared by go↔go and go↔web):
- Text frames = JSON control headers, binary frames = data chunks (pion `dc.Send` sends binary, `SendText` sends text, sending wrong way causes header to be swallowed as data chunk, constraint 1);
- **Data header and data chunks must be consecutive** (peer `SendFrame` sends atomically), receiver uses connection-level `expect` state machine to attach binary chunks to the nearest data header's request (constraint 2);
- `reqId` routing: Browser side can omit (backward compatible), Go side always includes (UUIDv4, `outbound.go:201-202`; constraint 3).
- Request/response frame types: Requests `req`, `create`, `upload`, `pull`, `list`, `share`, `info`, `delete`, `sync` (inbound verbs, `conn.go:296-327`); Responses `meta/data/done/err` for file pulling (`conn.go:16-19`); `admin`, `fwd-*`, `psk-*` see below.
- `dcReq` carries `Trace` re-source chain (2026-08-18 anti-loop: serveFile re-source to peer carries "node chain traversed", downstream finds itself in chain and rejects, `conn.go:41-55,78-89`).

**bindConn dispatch** (`conn.go:228-269`): `conns[c.ID()]=c` → dedup (`dedupConn`, `conn.go:213-226`, connection-level UUID lexicographic smaller wins, both sides keep same physical connection) → create `connState` (`fetches/verbWaits/binCh/binDone/fwdCh`, `conn.go:239-245`) → **register OnMessage before sending psk-auth** (order sensitive, see §5) → start `uploadWorker` + `fwdWorker` (`conn.go:262-265`) → `pskSendAuth` (`conn.go:268`). `dispatchFrame` (`conn.go:278-425`) is the message pump: text frames dispatched by `r.Type` (`req/create/upload/pull/list/share/info/delete/sync` all...

## 2. How It Stores

**No persistent storage for frame data.** The transport layer is a real-time data channel — data flows through, not stored:

| Aspect | Details |
|--------|---------|
| Frame data | Transient — sent/received over WebRTC/WS, not persisted |
| Connection state | In-memory (`connState` per connection, lost on disconnect) |
| File index | Persisted via repository (`file_index` SQLite table) — not owned by transport |
| Share scope | Persisted by service.NodeShare as `share_scope.json` — transport reads only |
| PSK state | In-memory (PSK shared secrets, not persisted) |
| Extra peers | In-memory (`extraPeers` list, lost on restart) |

**In-memory state**:
- `conns map[string]*Connection`: Active connections indexed by connection ID
- `connState` per connection: Active fetches, verb waits, binary channel, forward channel
- `extraPeers []string`: Persistent peer list for proactive connections
- `shareProvider` / `shareGate`: Injected from service layer for share frame handling

## 3. When It Stores

**Never for frame data.** Only metadata operations:

| Trigger | Action | Code Reference |
|---------|--------|----------------|
| Transport create verb | `file_index.Create` via repository | `file_index.go:238` |
| Transport upload verb | `file_index.Update` via repository | `file_index.go:461` |
| Transport delete verb | `file_index.Tombstone` via repository | `file_index.go:570,575` |
| Transport sync verb | `file_index.ListFileIndexSince` + `file_index.ApplySync` | `file_index.go:566-591,594-614` |
| Peer pull completion | `file_index.Create` via repository | `file_index.go` |
| Node share update | Delegated to service.NodeShare (writes `share_scope.json`) | `nodeshare.go` |

## 4. What It Stores

**No persistent frame data.** In-memory state only:

- **Connection registry**: Active WebRTC/WS connections with their state
- **Fetch tracking**: Active file pull requests per connection
- **Verb wait registry**: Pending verb responses indexed by reqId
- **Binary chunk channel**: Per-connection buffered binary chunks for reassembly
- **Forward channel**: Per-connection forwarded data routing
- **Extra peers**: Proactive peer connection list
- **PSK secrets**: Pre-shared keys for authentication (not persisted)

## 5. Boundaries and Pitfalls

- **Order sensitivity in bindConn**: OnMessage must be registered BEFORE psk-auth is sent. If reversed, auth response could arrive before handler is ready, causing message loss.
- **Full-duplex session, dual roles**: Same Session carries both inbound and outbound roles. Frame routing must correctly dispatch to the right handler based on frame type.
- **Connection dedup by UUID**: When two nodes connect to each other, both sides create connections. `dedupConn` uses lexicographic comparison of connection UUIDs to ensure only one physical connection is kept.
- **reqId is optional for browser**: Browser-side requests can omit reqId (backward compatible). Go-side always includes UUIDv4 reqId. Response routing must handle both cases.
- **Anti-loop trace**: `dcReq` carries a trace chain to prevent infinite re-source loops. Downstream nodes check if they appear in the trace and reject if so.
- **PSK authentication is first message**: `pskSendAuth` must be the first frame sent on a new connection. Missing PSK auth on a PSK-required connection means the connection is rejected.
- **Binary chunk atomicity**: Data header and data chunks must be sent atomically via `SendFrame`. Splitting them causes the receiver to misinterpret chunks as standalone messages.
- **Local WS session is special**: `id=="local"` identifies the browser's direct management channel. This session bypasses some peer-oriented logic (e.g. PSK auth, share frames).

## 6. External Connections

- [../connections/06-service-transport.md](../connections/06-service-transport.md): Service/transport PeerPuller, source, file_index assembly; cross-node pull persistence and CAS/file_index relationship.
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md): PeerJS library provides signaling + DataChannel transport primitives; this module defines the frame protocol on top.
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md): file_index mapping persistence delegated to repository (SQLite `file_index` table, `file_index_repo.go:24-36`); upload/pull persists to `uploadDir` (default `./downloads`) and `storageDir` CAS reads; share scope `share_scope.json` persisted by `service.NodeShare`, transport reads snapshots only.
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Browser-side peerjs connects to same signaling with same protocol and initiates connections to nodes — `onIncomingConnection` (`peerjs_service.go:476-481`) is the passive acceptor.
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md): `back/cmd/media-node` and verification client use same `go-peerjs` library and same frame protocol family, but use independent frame family (url/keepalive), no data plane interaction with this module — only shares library and protocol style.

> Not directly related to this module (goes through service/repository/router layers): [controller ↔ service](../connections/03-controller-service.md), [service ↔ repository](../connections/04-service-repository.md), [controller ↔ downloader](../connections/09-controller-downloader.md), [controller ↔ storage](../connections/10-controller-storage.md).
>
> Module document cross-reference: `10-peerjs.md` (signaling/DataChannel transport primitives, this module's underlying transport dependency; its §2/§3 aligns with this document's "in-memory state not persisted" conclusion).
