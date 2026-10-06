# How to Connect — PeerDrive Module Connection Overview

> This article is the connection overview for the `doc/design/` suite: how modules connect, in what order they are wired up at startup, how typical main flows run at runtime, and which document to look at for details on each pair of connections.
> Companion reading:
> - Each module's own design (logic / how to store / when to store / what to store) → [`modules/`](modules/) (14 documents, numbered `NN-*.md`)
> - The specification for each pair of connections (connection method / timing / case handling) → [`connections/`](connections/) (13 documents, numbered `NN-*.md`)
> - This article's reading entry point and documentation writing conventions → [README.md](README.md)
> - Connection topology visualization (high-resolution vector SVG, zero-crossing-line design):
>   - 🗺️ **Global overview**: 14 modules / 13 connections panorama → [connections-map.svg](connections-map.svg)
>   - 🗄️ **Storage & persistence domain**: physical disk and SQLite metadata on-disk → [domain-storage.svg](domain-storage.svg)
>   - 🌐 **P2P network & direct-connection domain**: signaling discovery and cross-node/cross-browser DataChannel direct connections → [domain-p2p.svg](domain-p2p.svg)
>   - 🎛️ **Frontend control & routing domain**: Web admin plane, WS admin proxy, and internal scheduling → [domain-control.svg](domain-control.svg)

---

## 1. Module Map

| # | Module | Code location | One-liner |
|---|------|----------|--------|
| 01 | [config](modules/01-config.md) | `back/internal/config/` | Env variable loading, defaults, startup-time Validate; not persisted |
| 02 | [repository metadata DB](modules/02-repository.md) | `back/internal/repository/` | SQLite metadata persistence: files/collections/shares/sync/pin/anon/file_index |
| 03 | [storage content-addressed store](modules/03-storage.md) | `back/internal/pathutil/` + `back/storage/` | File content-addressed on-disk, path safety, hard links, reserved names |
| 04 | [router routing & middleware](modules/04-router.md) | `back/internal/router/` | HTTP routing, auth/rate-limit/logging/CORS, collection dispatch, admin verb reusing gin |
| 05 | [controller HTTP handlers](modules/05-controller.md) | `back/internal/controller/` | HTTP semantic business surface (17 endpoint groups); not persisted |
| 06 | [service business logic layer](modules/06-service.md) | `back/internal/service/` | Business orchestration (collection/file/share/sync/pin/anon/node_directory/nodeshare/peerpull) |
| 07 | [source file source](modules/07-source.md) | `back/internal/source/` | local/url/peer/ipfs/bt source abstraction + Manager registry; also serves as transport-layer FileRouter |
| 08 | [downloader multi-protocol download](modules/08-downloader.md) | `back/internal/downloader/` | UniversalDownloader: multi-protocol fetch by priority |
| 09 | [transport P2P transport](modules/09-transport.md) | `back/internal/transport/` | Session state machine + verb dispatch (req/index/forward/share/pull/admin), discovery client |
| 10 | [peerjs protocol library](modules/10-peerjs.md) | `back/peerjs/` | PeerJS signaling + WebRTC DataChannel primitives, zero business knowledge |
| 11 | [signalserver signaling & discovery](modules/11-signalserver.md) | `back/signalserver/` | Self-hosted PeerJS signaling + room discovery (pure in-memory state) |
| 12 | [media-node ECH media chain](modules/12-media-node.md) | `back/cmd/media-node/` + `back/ech/` | Standalone binary: ECH domain-fronted direct connection to twimg media CDN |
| 13 | [frontend Web consumer](modules/13-frontend.md) | `front/src/` | React SPA: local WS session for admin plane + PeerJS dialing consumer |
| 14 | [nodestate shared state](modules/14-nodestate.md) | `back/internal/nodestate/` | operator/reg/peerID in-process shared state (independent package breaking circular imports) |

## 2. Connection Map

```
                    ┌──────────────────── Browser (13 frontend) ─────────────────────┐
                    │  ws.js (local WS session)   api.js (HTTP compat) PeerJSConnect  │
                    │                                                                    │
        conn 01     │  conn 01 admin verb + req/meta/data   conn 12 (PeerJS dial signaling)
        local WS session │      │                                  │
                    ▼      ▼                                  ▼
              ┌──────────────────┐              ┌─────────────────────┐
              │ 04 router        │              │ 11 signalserver     │
              │ (HTTP + admin    │              │ (signaling+discover,│
              │  forward gin eng)│              │  pure memory)       │
              └───────┬──────────┘              └──────────┬──────────┘
                      │ conn 02                              │ conn 08 (register/heartbeat/
                      ▼                                     ▼   forward/announce/discover)
              ┌──────────────────┐              ┌─────────────────────┐
              │ 05 controller    │              │ 09 transport        │
              └───┬─────────┬────┘              │ (session state mach+verb) │
      conn 03     │         │ conn 09            └──┬──────┬───────┬───┘
                    ▼         ▼                     │      │       │
            ┌──────────┐  ┌──────────┐    conn 07    │      │conn 11│
            │ 06 service│  │08 downloader│  ◄────────┤      │ disk  │
            └─┬───┬─────┘  └──────────┘            ▼      ▼       ▼
     conn 04  │   │conn 05                     ┌────────┐ ┌──────────────┐
              ▼   ▼                           │10 peerjs││ 03 storage   │
     ┌────────────┐  ┌───────────────┐        │protocol││ content-addr │
     │ 02 repository┐ │ 07 source     │        │ lib    ││ disk write   │
     │    SQLite   ││ │ src/FileRouter│
     └────────────┘  └───────────────┘
              conn 10 (controller→storage upload write)              conn 13
              conn 06 (service→transport P2P business)     ┌─────────────────┐
                                                      │ 12 media-node   │
                                                      │ + ech media chain│
                                                      └─────────────────┘
```

> The above is the ASCII version; the full topology with colors and direction annotations is at [connections-map.svg](connections-map.svg).
> Connection list (13 pairs) and detailed documentation for each pair: see the table below. Thick arrow direction = direction of control flow initiation; most pairs are actually bidirectional channels — refer to each `connections/*.md` for per-pair "direction" field.

## 3. Connection Pairs Summary Table (one document per pair)

> **💡 Architectural decoupling principle for Control Stream and Data Stream**:
> - **🕹️ Control Stream (signaling & admin plane)**: transport commands, handshake negotiation (SDP/ICE), heartbeats, room hashes and SQL metadata. Very small volume (bytes to a few KB), responsible for addressing, connection establishment and admission gating; never carries large files, protecting the signaling server from being overwhelmed by large traffic. Additionally, dangerous admin-plane commands are **restricted to local WS sessions only**; WebRTC does not implement admin verbs.
> - **🚚 Data Stream (P2P transport & persistence plane)**: transport real file binary blocks (64KB chunks). After two nodes establish a connection, they leave signaling and transmit at full point-to-point P2P speed, with built-in 4MB dynamic backpressure flow control to prevent memory overflow, incremental SHA-256 computed as received for tamper prevention, and SafeWrite atomic on-disk writes.

| # | Connection pair | Protocol used | Flow attribute | Payload / Stream passed | Document |
|---|--------|----------|----------|--------------------------------|------|
| 01 | frontend ↔ backend | WebSocket (`/ws/peer`) + HTTP | Control stream + local data stream | `admin` JSON frames (`{method,path,body}`) · `admin-resp` · `admin-bin` 64KB stream | [01-frontend-backend.md](connections/01-frontend-backend.md) |
| 02 | router ↔ controller | HTTP/1.1 REST / internal transfer | Control stream | `*gin.Context` context · internal request after admin frame unpacking · JSON struct response | [02-router-controller.md](connections/02-router-controller.md) |
| 03 | controller ↔ service | Go in-memory call | Control stream | Business DTO (`hash`, `collectionId`, `peerId`, `fileInfo`) · `io.Reader` stream handle | [03-controller-service.md](connections/03-controller-service.md) |
| 04 | service ↔ repository | SQLite DB driver (IPC) | Control stream | SQL queries & parameters · `file_index` records · `seq` cursor · collection tree metadata | [04-service-repository.md](connections/04-service-repository.md) |
| 05 | router ↔ source | Go in-memory interface | Control stream | `source.Source` abstract interface (`Read / Size / Hash`) · physical and network stream locator handle | [05-router-source.md](connections/05-router-source.md) |
| 06 | service ↔ transport | Go in-memory call | Control stream | `PullRequest(hash, peerId)` task · `PEERDRIVE_PSK` credential · transport progress callback stream | [06-service-transport.md](connections/06-service-transport.md) |
| 07 | transport ↔ peerjs | WebRTC primitive call | Control/data frames | DataConnection handle · `raw bytes` protocol raw binary frames (`share/req/meta/data`) | [07-transport-peerjs.md](connections/07-transport-peerjs.md) |
| 08 | transport ↔ signalserver | WebSocket (TCP / JSON) | Control stream | WebSocket JSON (`OPEN/OFFER/ANSWER/CANDIDATE`) · 64hex presence room broadcast heartbeat | [08-transport-signalserver.md](connections/08-transport-signalserver.md) |
| 09 | controller ↔ downloader | Multi-protocol pipeline (Local/IPFS/BT/HTTP) | Control/data | Input 64-bit SHA-256 hash string → returns `[]byte` complete file data and hit protocol name | [09-controller-downloader.md](connections/09-controller-downloader.md) |
| 10 | controller ↔ storage | OS disk IO (SafeWrite) | Data stream | multipart upload file stream (`io.Reader`) physically written to disk via SafeWrite with hash computed | [10-controller-storage.md](connections/10-controller-storage.md) |
| 11 | transport ↔ storage | OS disk IO (SafeWrite) | Data stream | 64KB binary chunk stream (SHA-256 checksum computed as received and written / chunked disk reads for push) | [11-transport-storage.md](connections/11-transport-storage.md) |
| 12 | frontend ↔ signalserver | WebSocket (TCP / PeerJS) | Control stream | WebSocket JSON (PeerJS SDP/ICE candidate exchange) · `/discover/nodes` online node list | [12-frontend-signalserver.md](connections/12-frontend-signalserver.md) |
| 13 | media-node ↔ ech | TLS 1.3 with ECH + HTTP/2 | Data stream | media URL · TLS ECH encrypted SNI ClientHello · HTTP media stream chunked data | [13-media-node-ech.md](connections/13-media-node-ech.md) |
| P2P | Local Node ↔ Remote Peer | WebRTC DataChannel (SCTP/UDP) | Data stream (with frame control) | share directory browse frames / req(hash) fetch frames / **64KB binary chunk stream** / 4MB backpressure | [doc/NETDISK.md](../NETDISK.md) |

## 4. Startup Sequence (server-side wiring order)

Derived from `back/internal/serverapp/app.go` (`InitDB → PeerJSService.Start → register sources → SetupRouter`):

```text
main()
├─ config.Load() + config.Validate()          # 01 config: read env, Fatalf on misconfiguration
├─ checkUnsafeRoots / warnUnsupportedRoots    # 03 storage related: volume root rejection / os.Root capability self-check
├─ repository.InitDB(cfg.DBPath)              # 02 repository: SQLite open+migrate; defer CloseDB
│   └─ repository.SetAnonStorageDir(storageDir)  # anonymous storage in same directory as regular files
├─ transport.NewPeerJSService(cfg, storageDir) # 09 transport: PeerJS service assembly (when cfg.PeerJSEnable)
│   ├─ FileIndex().AddReadRoot(storageDir + cfg.ShareDirs)  # read root registration (registration side ≠ reading side; missing causes read failed)
│   └─ peerjsSvc.Start()                       # 10 peerjs engine startup (signaling dial, etc.)
│   ├─ service.NewNodeDirectory(...) + SetSelfID/SetConnected/SetDial
│   │   └─ peerjsSvc.SetExtraPeers(nodeDir.JoinedPeerIDs)    # node market M1
│   ├─ service.NewNodeShare(cfg, storageDir)   # shared scope M2: storageDir/share_scope.json persistence
│   │   ├─ share.SetDirHook(dirs → FileIndex().AddReadRoot)  # runtime new shared directory back-registration
│   │   ├─ SetAnonAccess / SetFileLister / SetFileInfoReader
│   │   ├─ peerjsSvc.SetShareProvider(share.SnapshotFor) / SetShareGate(share)
│   │   └─ nodeDir.SetShareSummary(share.Summary)
│   ├─ service.NewPeerPuller(cfg.DownloadDir)  # cross-node pull M3: SetSource(peerjsSvc) + FileAccess
│   ├─ source.New() + Register(local/peer/url) # 07 source: local→peer→URL template order
│   │   └─ router.SetSourceManager(mgr) / peerjsSvc.SetFileRouter(mgr)  # serveFile multi-source routing
│   └─ SetForwardRules(PEERDRIVE_FORWARD_RULES) # port forwarding v2 rules
├─ router.SetRegServer / SetPeerJSService / SetPeerJSConfig / SetNodeDirectory / SetNodeShare / SetPeerPuller
└─ router.SetupRouter(cfg)                     # 04 router: register all HTTP routes (must be after the Set* calls above)
    └─ http.Server (ReadHeaderTimeout=15s) — ListenAndServe
```

Key points:

- **Injection order is sensitive**: `SetPeerJSService` / `SetNodeDirectory` / `SetSourceManager` must come before `SetupRouter` (routes read these handles at registration time); explicitly noted in comments.
- **Shared scope across restarts**: `service.NodeShare` runtime changes land in `storageDir/share_scope.json` (see "how to store / when to store" in [06-service.md](modules/06-service.md) and [06-service-transport.md](connections/06-service-transport.md)).
- **Dual-track read roots**: `AddReadRoot` registers "externally readable roots" — startup batch comes from `cfg.ShareDirs`, runtime additions are back-registered by `share.SetDirHook`.

## 5. Shutdown Sequence

```text
SIGINT/SIGTERM → srv.Shutdown(ctx, 20s)   # stop accepting new requests first, give in-flight requests 20s to finish
   ├─ timeout → srv.Close() force close          # cannot hang shutdown indefinitely (container orchestrator will SIGKILL)
   └─ peerjsSvc.Close()  +  repository.CloseDB()  # defer chain executes when main returns
```

Gotcha: do not use `r.Run()` (internal Fatalf calls os.Exit directly, defer does not run → on Windows SQLite handles are not closed, next startup may fail to open the DB; large file transfers get cut off).

## 6. Runtime Typical Main Flows

### 6.1 Admin Plane Operations (frontend button click → backend execution)

```
frontend page → ws.js (local WS /ws/peer)
  → admin verb: {"type":"admin","method","path","body","token","reqId"}
  → 09 transport admin.go: connection-level expect state machine → internal forward to gin engine
  → 04 router → 05 controller → 06 service → 02 repository / 07 source / 09 transport
  ← admin-resp {status, body, reqId} (status≥400 → frontend reject)
```

- Key rule: admin plane **only goes through local WS**; peerjs/WebRTC does not implement admin verbs (to prevent permission-plane vulnerabilities).
- Binary upload/download: admin-bin declares header + binary frames are atomic and contiguous (`binaryExpect` single-slot routing).
- Details: [01-frontend-backend.md](connections/01-frontend-backend.md), [02-router-controller.md](connections/02-router-controller.md).

### 6.2 File Registration/Upload (write side)

```
HTTP/WS upload → 05 controller (file/anon)
  → 10 storage (safewrite + content-addressed on-disk: storage/<hash first 2>/<hash>)
  → 02 repository records metadata (sha256→path, file_index and other tables)
  → 03 storage + 02 repository both consistent then return success
```

### 6.3 P2P Pull Download (remote → local on-disk)

```
req frame (initiator) → 09 transport outbound → 10 peerjs engine → 11 signalserver forward
  → remote inbound: FileRouter (07 source.Manager) multi-source resolution → hit local / origin remote / URL
  ← data frame chunked return (flow-control threshold) → 11 transport-storage on-disk + file_index registration + hash verification
```

- Data plane frame protocol (req/meta/data/done/err + create/upload/list/info/delete/sync index + fwd-* forwarding) is authoritative in `doc/REFACTOR.md` §4; `front/src/ws.js` header comment has precise conventions.
- Details: [07-transport-peerjs.md](connections/07-transport-peerjs.md), [08-transport-signalserver.md](connections/08-transport-signalserver.md), [11-transport-storage.md](connections/11-transport-storage.md).

### 6.4 Shared Provisioning (remote reads my files)

```
remote req frame → local node inbound → ShareGate (06 service NodeShare: private only to friends/self)
  → SnapshotFor(requester id) outputs shared manifest → FileRouter filters by manifest prefix → return frame
```

## 7. Relationship with Existing Documentation

- `doc/layers/` (L1-L8): module documents organized by **layer / AOP aspect** (established 2026-08-18), tests run per layer (`scripts/test-layers.sh`).
- This suite (`doc/design/`): organized by **module + connection pairs**, focused on each module's "how / when / what to store" and each pair's "connection method / timing / case handling" — the two perspectives are complementary, not replacements.
- Frame protocols, verbs, port forwarding v2 and other protocol-level definitions remain authoritative in `doc/REFACTOR.md` §4; this document only references, not re-defines.

## 8. Maintenance Conventions

1. Adding/removing modules or connections → synchronously update this file (module map, connection map, summary table) and the connection topology diagram connections-map.svg / .png, and [README.md](README.md).
2. Connection behavior changes → first update the corresponding `connections/NN-*.md`, then consider whether this file's typical main flows are affected.
3. Every module/connection document insists on "facts annotated with code location"; when code and documentation conflict, code is authoritative and the documentation should be updated.
