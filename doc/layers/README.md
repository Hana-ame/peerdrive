# Layer documentation index (doc/layers/)

> AOP-aspect-organized module documentation tree (established 2026-08-18).
> Membership decision rules see `doc/LAYERS.md`; architecture decisions/pitfalls see
> `doc/REFACTOR.md`; old business module docs (auth/bt/ipfs/p2p/storage) are still in
> `doc/modules/`; where they were marked outdated this tree takes precedence.

---

## ① Signaling/transport primitives layer — `back/peerjs/`

**One-liner**: PeerJS signaling + WebRTC DataChannel transport primitives, zero business knowledge.

| Module | File | Doc |
|---|---|---|
| Connection | `connection.go` | [connection.md](L1-peerjs/connection.md) |
| Peer (signaling lifecycle) | `peer.go` + `signaller.go` | [peer.md](L1-peerjs/peer.md) |
| DataChannel abstraction | `transport.go` | [transport.md](L1-peerjs/transport.md) |
| Messages/flow control | `message.go` + `flowcontrol_test.go` | [connection.md](L1-peerjs/connection.md) (merged) |

## ② Frame protocol layer — `back/internal/transport/`

**One-liner**: Session state machine + verb dispatch (req fetch/upload/index/forward),
knows frames not business.

| Module | File | Doc |
|---|---|---|
| Connection shared core (dispatch) | `conn.go` | [conn.md](L2-transport/conn.md) |
| Inbound role (responding) | `inbound.go` | [inbound.md](L2-transport/inbound.md) |
| Outbound role (initiating) | `outbound.go` | [outbound.md](L2-transport/outbound.md) |
| File index persistence | `file_index.go` | [file-index.md](L2-transport/file-index.md) |
| Port forwarding v2 | `forward.go` | [forward.md](L2-transport/forward.md) |
| Session abstraction | `ws_session.go` + `rtc_session.go` | [sessions.md](L2-transport/sessions.md) |

## ③ Management aspect — `back/internal/transport/admin.go` + router assembly

**One-liner**: Local WS session management verb; internally forwards to gin engine
reusing all controllers.

| Module | File | Doc |
|---|---|---|
| admin verb (protocol+upload+response classification) | `admin.go` + `admin_test.go` | [README.md](L3-admin/README.md) |
| router assembly | `back/internal/router/router.go` (SetAdminHandler) + `peerjs_routes.go` | Same as above |

## ④ Business core — `back/internal/controller|service|source|downloader`

**One-liner**: HTTP semantic business, unaware whether being forwarded via WS frames or
called directly over HTTP.

| Module | File | Doc |
|---|---|---|
| controller (17 endpoint groups) | `controller/*.go` | [controllers.md](L4-core/controllers.md) |
| service (business services) | `service/*.go` | [services.md](L4-core/services.md) |
| source (unified file fetching) | `source/*.go` | [source.md](L4-core/source.md) |
| downloader (multi-protocol pipeline) | `downloader/*.go` | [downloader.md](L4-core/downloader.md) |

## ⑤ Data aspect — `back/internal/repository/`

**One-liner**: SQLite persistence (file_index table sha256→path + seq cursor incremental
sync).

| Module | File | Doc |
|---|---|---|
| repository (11 repos) | `repository/*.go` + `db.go` | [README.md](L5-data/README.md) |

## ⑥ Discovery aspect — `back/signalserver/`（独立模块）+ `back/internal/transport/*_discovery.go`

**One-liner**: How nodes find each other (self-hosted signaling/HTTP discovery/MQTT rooms),
only exchanges peerId.

| Module | File | Doc |
|---|---|---|
| Self-hosted signaling + discovery API | `signalserver/*.go` (cmd/peerserver) | [signalserver.md](L6-discovery/signalserver.md) |
| Discovery client | `transport/http_discovery.go` + `transport/mqtt_discovery.go` | [discovery.md](L6-discovery/discovery.md) |

## ⑦ External capability aspect — `back/p2p_bt/` + `back/internal/provider/`

**One-liner**: Independent ecosystem-slot capabilities (BT DHT / IPFS gateway), standalone
go.mod or independently evolvable.

| Module | File | Doc |
|---|---|---|
| BT DHT bridge | `p2p_bt/*.go` (standalone go.mod) | [p2p-bt.md](L7-external/p2p-bt.md) |
| IPFS gateway provider | `internal/provider/ipfs.go` | [provider.md](L7-external/provider.md) |

## ⑧ Frontend aspect — `front/src/`

**One-liner**: Browser side, only communicates with ②'s WS sessions + ③ admin verb.

| Module | File | Doc |
|---|---|---|
| WS client | `ws.js` | [ws-client.md](L8-frontend/ws-client.md) |
| api wrapper | `api.js` | [api-layer.md](L8-frontend/api-layer.md) |
| Pages/routes/navigation | `pages/` + `App.jsx` + `components/Navbar.jsx` | [pages.md](L8-frontend/pages.md) |

---

## Layer tests (scripts/test-layers.sh)

Each layer runs independently; full run aggregates (any layer failing exits nonzero):

```bash
bash scripts/test-layers.sh           # L1-L8 (2026-08-18 measured 8/8 all green)
bash scripts/test-layers.sh --integration  # Adds real signaling integration section (-p 1 serial)
```

| Layer | Test command (in script) | Count |
|----|--------------------|----|
| L1 | `cd back/peerjs && go test ./... -count=1 -race` | 21 |
| L2 | `go test -tags nosqlite ./internal/transport/ -count=1 -skip "^TestAdmin"` | 32 |
| L3 | `go test -tags nosqlite ./internal/transport/ -count=1 -run "^TestAdmin"` | 9 |
| L4 | `go test -tags nosqlite ./internal/controller/... ./internal/service/... ./internal/source/... ./internal/downloader/...` | 94 |
| L5 | `go test -tags nosqlite ./internal/repository/...` | 11 |
| L6 | `go test -tags nosqlite ./internal/signalserver/...` | 6 |
| L7 | `cd back/p2p_bt && go test ./... -count=1` | 7 |
| L8 | `cd front && npm test` (vitest) | 32 |
| INT | `go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1` | — |

Details and independent-package (peerdrive-media) verification chain see
[doc/testing/README.md](../testing/README.md).

## Maintenance conventions

- Each doc keeps the "responsibilities → key mechanisms → pitfalls → tests → file
  inventory" structure
- Frame protocol/verb definitions follow `doc/REFACTOR.md` §4; this doc tree references
  them without redefining
- After code changes, if behavior changes, synchronously update the corresponding module
  doc (mandatory, same as AGENTS.md coding standards)
