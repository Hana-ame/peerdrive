# Peerdrive Architecture Documentation

> Content-addressed + Git-style version-controlled P2P file sharing system · v3.0

## Monorepo Structure

```
peerdrive/
├── front/                    React frontend (Vite + TailwindCSS + Vitest)
│   ├── src/
│   │   ├── features/         Feature-sliced modules
│   │   │   ├── node/         Node search, connect & control (Connect, NodeControl)
│   │   │   ├── drive/        Drive & file management (Drive, Grid/List view, Cast)
│   │   │   ├── collection/   Collections & browser (Collections, CollectionView, Browser)
│   │   │   ├── transfers/    Transfer manager (Transfers)
│   │   │   ├── bt/           BitTorrent DHT client (BT)
│   │   │   ├── ipfs/         IPFS gateway interface (IPFS)
│   │   │   ├── settings/     System & node settings (Settings)
│   │   │   ├── display/      Media display & casting (Display)
│   │   │   └── iwara/        Iwara media integration (Iwara)
│   │   ├── components/       Shared components (MobileNav, modals, layout)
│   │   ├── context/          React application contexts (AppContext)
│   │   ├── lib/              Session, connection status & client helpers
│   │   └── platform/         Transport bridges (WS, Service Worker)
│   └── tests/                Frontend tests
├── back/                     Go backend (Gin + SQLite + BT DHT), single binary since v0.3.0
│   ├── cmd/peerdrive/        Entry point (subcommands: demo/serve/signal/reg/all)
│   ├── internal/
│   │   ├── serverapp/        Startup assembly + HTTP bootstrap (was cmd/server)
│   │   ├── panel/            Embedded public panel + peerjs (go:embed, since v0.3.2)
│   │   ├── regserver/        Registration / auth / relay registration
│   │   ├── services/         In-process handler assembly (signal + reg multiplexing)
│   │   ├── controller/       HTTP handler layer
│   │   ├── transport/        WebRTC frame protocol / session management
│   │   ├── service/          Business logic layer (P2P/files/collections/downloads)
│   │   ├── repository/       SQLite persistence
│   │   ├── provider/         Data source abstraction (local/HTTP/IPFS)
│   │   ├── router/           Gin routing & middleware
│   │   ├── model/            Data models
│   │   └── config/           Configuration
│   ├── pkg/hashutil/         Hash utilities
│   └── test/integration/     Integration/E2E tests (build tag `integration`, must run with -p 1)
├── doc/                      Project documentation (you are here)
└── .github/workflows/        CI/CD
```

## Layered Architecture (Backend)

```
HTTP API (Gin Router)
  → Controller (parameter validation, response formatting)
    → Service (business logic)
      → Provider (data source: local / http / ipfsgw)
      → Repository (SQLite)
      → P2P (PeerJS signaling + WebRTC DataChannel / BT DHT)
```

| Layer | Location | Responsibility |
|----|------|------|
| Router | `back/internal/router/` | Route registration, CORS, Auth middleware |
| Controller | `back/internal/controller/` | HTTP handling, parameter parsing |
| Service | `back/internal/service/` | Core logic: file registration/download, collection CRUD/versions, P2P transport/signaling, node sharing & directory, cross-node puller |
| Repository | `back/internal/repository/` | SQLite CRUD |
| Provider | `back/internal/provider/` | Data source interface: `local` / `http` / `ipfsgw` |
| P2P BT | `back/p2p_bt/` | Mainline DHT, BEP44/BEP51, torrent/magnet |
| Startup assembly | `back/internal/serverapp/` | Assemble router/service/transport; **also prints the "next step" guidance and the clickable `/panel` URL** (since v0.3.2) |
| Public panel | `back/internal/panel/` | `go:embed` panel.html + peerjs.min.js, served at `/panel`; auto reverse-lookup of node id and signaling (since v0.3.2) |

## Ports

| Port | Service | Repository Location | Description |
|------|------|----------|------|
| `:3000` | back (main API) | `back/` | Gin HTTP, all file/collection/P2P endpoints |
| `:5173` | front (Dev) | `front/` | Vite HMR development server |
| `:4000` | registration-server | Independent repo | User registration, JWT authentication |

## Core Concepts

| Concept | Git Analogy | Description |
|------|----------|------|
| Collection | Repository | File collection |
| Entry | Tree | `path → [{type, value, mime_type}]` |
| Version / Commit | Commit | Version snapshot |
| Fork | Fork | Create a new version based on an existing collection |
| Merge | Merge | Three-way merge |
| Anonymous Collection | — | No registration required, direct access via SHA256 hash |

## Modules

| Module | Frontend | Backend |
|------|------|------|
| Drive & File Management | `features/drive/` (`Drive.jsx`, `DriveGridView.jsx`, `DriveListView.jsx`, `CastModal.jsx`) | `controller/file.go`, `controller/file_inbox.go`, `service/file_service.go`, `service/file_index_service.go` |
| Collections | `features/collection/` (`Collections.jsx`, `CollectionView.jsx`, `CollectionBrowser.jsx`) | `controller/anon.go`, `controller/collection.go`, `service/anon_service.go` |
| Node & Interconnection | `features/node/` (`Connect.jsx`, `NodeControl.jsx`) | `transport/`, `service/node_directory.go`, `service/nodeshare_service.go` |
| Transfers & Puller | `features/transfers/` (`Transfers.jsx`) | `service/peer_puller.go`, `controller/p2p.go` |
| BT DHT | `features/bt/` (`BT.jsx`) | `p2p_bt/`, `controller/p2p.go` (BT endpoints) |
| IPFS | `features/ipfs/` (`IPFS.jsx`) | `provider/ipfs.go`, `controller/ipfs.go` |
| Media Display | `features/display/` (`Display.jsx`) | Media casting and presentation |
| Iwara Integration | `features/iwara/` (`Iwara.jsx`) | `controller/iwara.go`, `service/iwara_service.go` |
| Settings | `features/settings/` (`Settings.jsx`) | `controller/system.go` |
| Authentication & Session | `lib/` session auth, `platform/transport-ws.js` | `controller/auth.go`, `service/auth_service.go`, `router/auth_middleware.go` |

### Frontend Navigation Architecture (3-Tier)

Frontend navigation in `front/src/App.jsx` dynamically adapts to the connection state across three tiers:

| Tier | Navigation Items | Description / Visibility |
|------|-------------------|--------------------------|
| **`ALWAYS_NAV`** | Connect (`/`), Display (`/display`), Iwara (`/iwara`) | Standalone and consumer-facing pages; always visible. |
| **`OWNER_NAV`** | Drive (`/drive`), Node (`/node`), Collections (`/collections`), Transfers (`/transfers`), BT (`/bt`), IPFS (`/ipfs`), Settings (`/settings`) | Active when a local Node WebSocket session is open (`ws`). Provides full host management. |
| **`GUEST_NAV`** | Collections (`/collections`), Drive (`/drive`), Transfers (`/transfers`) | Active when connected to a remote peer via WebRTC guest mode (`isGuest`). Exposes only remote shared content and transfers. |

## Data Flow

```
Upload:      front → POST /files/upload → Controller → FileService → Provider(local) → SQLite
Download:    front → GET /sha256sum/:hash → Controller → Downloader → Provider → Response
P2P WebRTC:  Node ← PeerJS Signaling / HTTP Discovery / MQTT → DataChannel (req/share/list/sync/fwd)
BT DHT:      Node ← Mainline DHT → Find Peers → Torrent Download
Collection:  front → POST /anon/collections → AnonService → CollectionRepo → JSON → SHA256
Cross-Pull:  Node A (PeerPuller) → WebRTC req/data stream → Node B → Safe disk write + FileIndex
```

## Quick Start

```bash
# Backend (⚠️ -tags nosqlite is mandatory: three-way SQLite CGO conflict)
cd back
go build -tags nosqlite -o peerdrive ./cmd/peerdrive/ && ./peerdrive
# → http://localhost:3000 , panel at /panel (zero config), Swagger at /swagger/index.html

# Zero-config full-chain demo (no Go/repo/Node needed):
./peerdrive demo

# Frontend
cd front
npm ci && npm run dev
# → http://localhost:5173
```

## Testing

> **Note**: Peerdrive follows a **CI-only verification model** (all CI matrix jobs run automatically via GitHub Actions).
> For the authoritative testing guide, selection table, and command reference across all 14 components, consult [`doc/testing/README.md`](testing/README.md).

```bash
# Backend unit tests (nosqlite tag required)
cd back && go test -tags nosqlite ./... -count=1

# Frontend tests (Vitest)
cd front && npm test

# Frontend build
cd front && npm run build

# End-to-end full netdisk chain local demo (dual-node + self-hosted signaling + PSK verification)
./scripts/netdisk-local-demo.sh
```

## Environment Variables

| Variable | Default | Description |
|------|--------|------|
| `PORT` / `PEERDRIVE_PORT` | `3000` | Backend HTTP API port |
| `PEERDRIVE_HOST` | Empty (`0.0.0.0`) | Listen address (e.g. `127.0.0.1` for local-only admin isolation) |
| `PEERDRIVE_STORAGE` | `./storage` | File storage directory |
| `PEERDRIVE_DB_PATH` | `./peerdrive.db` | SQLite metadata database path |
| `PEERDRIVE_DOWNLOAD_DIR` | `./downloads` | Downloaded files directory |
| `PEERDRIVE_PEERJS_ENABLE` | `true` | Enable PeerJS signaling & WebRTC interconnect |
| `PEERDRIVE_PEERJS_HOST` | `0.peerjs.com` | PeerJS signaling host (override for self-hosted peersignal) |
| `PEERDRIVE_PEERJS_PORT` | `9000` | PeerJS signaling port (e.g. `443` for TLS proxy) |
| `PEERDRIVE_PEERJS_KEY` | `peerjs` | PeerJS signaling key |
| `PEERDRIVE_PEERJS_ID` | Empty | Node PeerJS ID (auto-generates `peerdrive-<random>` if empty) |
| `PEERDRIVE_PEERJS_PEERS` | Empty | Comma-separated peer IDs for auto-interconnection on startup |
| `PEERDRIVE_PEERJS_XOR_ENABLE` | `false` | WebRTC DataChannel lightweight XOR wire obfuscation |
| `PEERDRIVE_PEERJS_XOR_KEY` | Empty | Per-connection XOR derivation seed (both sides must match) |
| `PEERDRIVE_PSK` | Empty | Pre-shared key for node access admission (open mode if empty) |
| `PEERDRIVE_SHARE_ENABLE` | `false` | Master switch for external sharing via `share` frame (default off for privacy) |
| `PEERDRIVE_SHARE_COLLECTIONS` | Empty | Shared collections (comma-separated hashes or `all` for public collections) |
| `PEERDRIVE_SHARE_DIRS` | Empty | Shared directory paths (comma-separated; must be explicitly declared) |
| `PEERDRIVE_SHARE_FRIENDS` | Empty | Comma-separated peer IDs allowed access to private-level shares |
| `PEERDRIVE_DISCOVER_URL` | Empty | Self-hosted signaling discovery API (takes priority over MQTT when set) |
| `PEERDRIVE_DISCOVER_PRESENCE` | `true` | Node-level presence room discovery: nodes with zero shared collections can also find each other |
| `PEERDRIVE_DISCOVER_MODE` | `auto` | Discovery mechanism (`auto` / `peerjs` / `discover` / `mqtt` / `off`) |
| `PEERDRIVE_MAX_PEERS` | `8` | Maximum dials triggered by automatic discovery |
| `PEERDRIVE_MQTT_ENABLE` | `false` | Enable MQTT content shard room discovery |
| `PEERDRIVE_MQTT_BROKER` | `tcp://broker.emqx.io:1883` | Public MQTT broker address |
| `PEERDRIVE_MQTT_COLLECTIONS` | Empty | Comma-separated collection hash shards to watch on MQTT |
| `PEERDRIVE_BT_ENABLE` | `false` | Opt-in master switch for BitTorrent integration |
| `PEERDRIVE_BT_DHT_ENABLE` | `false` | Enable BitTorrent mainline DHT |
| `PEERDRIVE_IPFS_ENABLE` | `false` | Opt-in master switch for IPFS bridge |
| `PEERDRIVE_IPFS_GATEWAY_ENABLE` | `true` | Enable public IPFS HTTP gateways fallback |
| `PEERDRIVE_PORTFWD_ENABLE` | `false` | Opt-in master switch for port forwarding v2 |
| `PEERDRIVE_FORWARD_RULES` | Empty | Port forwarding whitelist credentials (`key1:8080,key2:8443`) |
| `PEERDRIVE_MAX_CONCURRENT_STREAMS` | `8` | QoS limit for concurrent outgoing download streams |
| `PEERDRIVE_MAX_UPLOAD_SPEED` | `0` | QoS global outgoing bandwidth limit in bytes/sec (0 = unlimited) |
| `PEERDRIVE_RATE_LIMIT_RPS` | `30` | Per-IP request rate limit (0 = unlimited) |
| `PEERDRIVE_CSP` | On | Set to `off` to disable Content-Security-Policy |
| `PEERDRIVE_TRUSTED_PROXIES` | Empty | Trusted reverse proxies (comma-separated IP/CIDR; empty = only trust RemoteAddr) |
| `PEERDRIVE_ALLOW_HARDLINKS` | `false` | Set to `1` to allow files with multiple hard links |
| `PEERDRIVE_ALLOW_UNSAFE_ROOT` | `false` | Set to `1` to allow root volume (`/`, `C:\`) as storage/download directory |
| `CORS_MODE` | Allowlist | `all` / `localhost` |
| `VITE_API_BASE` | `http://localhost:3000` | Frontend API base URL |

> Background and remaining items for new additions see [FULLSTACK-AUDIT.md](FULLSTACK-AUDIT.md).

## Documentation Index

### Architecture

| File | Content |
|------|------|
| [ROADMAP.md](ROADMAP.md) | **Development order (sequenced by user in 2026-09)**: PeerJS interconnection → files → combination → management chain → file-scope management → upload/download/save → identity management (last) |
| [REFACTOR.md](REFACTOR.md) | Refactoring manual: layered boundaries, migration order, fixes for existing issues |
| [FULLSTACK-AUDIT.md](FULLSTACK-AUDIT.md) | **Full-stack health check report (2026-09-23)**: configuration/logging/data layer/auth/hardening current state, 12 fixed and 7 remaining items |
| [NETDISK.md](NETDISK.md) | Netdisk (PeerJS node + panel) usage and implementation manual |
| [LAYERS.md](LAYERS.md) / [layers/](layers/) | Layered architecture and per-layer descriptions |
| [NODE.md](NODE.md) / [NODE-API.md](NODE-API.md) | Node and node API |
| [PEERSIGNAL.md](PEERSIGNAL.md) | Self-hosted signaling (peersignal) deployment and protocol |
| [PROJECT-VISION.md](PROJECT-VISION.md) | Product vision |
| [HTTP_API_PROXY.md](HTTP_API_PROXY.md) | HTTP API proxy |
| [TODO-SIMPLIFY.md](TODO-SIMPLIFY.md) | Simplification to-do |
| [source-control.md](source-control.md) | Version control (collection fork / merge) |
| [dht-wire-format.md](dht-wire-format.md) | DHT wire format |
| [api-reference.md](api-reference.md) | API reference (brief) |
| [design/PEERDRIVE-DSH-INSPIRED.md](design/PEERDRIVE-DSH-INSPIRED.md) | Compositional architecture design inspired by dsh (profile/bundle/patch) |
| [design/FRONTEND-DSH-INSPIRED.md](design/FRONTEND-DSH-INSPIRED.md) | Frontend compositional design inspired by dsh (bundle manifest + registry + profile) |
| [design/FRONTEND-DSH-KERNEL.md](design/FRONTEND-DSH-KERNEL.md) | Frontend kernel bootstrap/module/slot/transport design inspired by dsh |
| [design/ADR-001-ROUTER-CHOICE.md](design/ADR-001-ROUTER-CHOICE.md) | Architecture Decision Record: Routing Strategy (HashRouter vs BrowserRouter trade-offs) |
| [FILE-REFERENCE.md](FILE-REFERENCE.md) | Complete project file path and description manual (~240 files) |

### spec — Technical Specifications

| File | Content |
|------|------|
| [spec/API-REFERENCE.md](spec/API-REFERENCE.md) | Complete API reference (107 endpoints) |
| [spec/REQUIREMENTS.md](spec/REQUIREMENTS.md) | Complete requirements table (130+ items) |
| [spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) | Complete collection logic trace |
| [spec/USER-ROLES.md](spec/USER-ROLES.md) | User role model |
| [spec/BACKEND_TASKS.md](spec/BACKEND_TASKS.md) | Backend task list |
| [spec/FRONTEND_TASKS.md](spec/FRONTEND_TASKS.md) | Frontend task list |
| [spec/backend/](spec/backend/) | Backend API / database / design specifications |
| [spec/frontend/](spec/frontend/) | Frontend API documentation |

### modules — Module Design

| Directory | Content |
|------|------|
| [modules/auth/](modules/auth/) | Authentication module: API design, security review, user roles |
| [modules/bt/](modules/bt/) | BT DHT module: protocol, test matrix, API design |
| [modules/ipfs/](modules/ipfs/) | IPFS module: protocol, WebRTC architecture |
| [modules/p2p/](modules/p2p/) | P2P module: current interconnection framework (TRANSPORT) + API design; legacy stack docs see [modules/p2p/archive/](modules/p2p/archive/) |
| [modules/storage/](modules/storage/) | Storage module: database, collection logic, API |

### guide — Operation Guides

| File | Content |
|------|------|
| [guide/API-USAGE.md](guide/API-USAGE.md) | API usage manual — call order/purpose/conditions |
| [guide/USER_MANUAL.md](guide/USER_MANUAL.md) | User manual |
| [guide/siliconflow-setup.md](guide/siliconflow-setup.md) | LLM configuration |
| [guide/FRONTEND.md](guide/FRONTEND.md) | Frontend interface description — tech stack, routing, component tree, workflows, code map |
| [guide/operation-manual.md](guide/operation-manual.md) | Chinese operation guide |

### testing — Testing

| File | Content |
|------|------|
| [testing/README.md](testing/README.md) | **Testing components overview (entry point)** — selection table/commands/scale/CI mapping/blind-spot checklist for 14 components (2026-09-20 measured: 308+21+23+21+7+88+60+21) |
| [testing/index.md](testing/index.md) | Testing documentation portal (points to the above + layered docs) |
| [testing/archive/](testing/archive/) | Legacy stack testing documentation (2026-04~05, libp2p / e2e-all.sh / reg-server), historical reference only |

> The old index listed several filenames such as `TESTING-HANDBOOK.md` / `TEST-PIPELINE.md` / `TEST-MATRIX.md` /
> `FRONTEND-TESTING.md`, **which no longer exist in the current directory** (cleaned up after the 2026-08-18 rewrite),
> content has been unified into `testing/README.md`. The current testing state is authoritative per that file; do not reference old filenames.
>
> Related: [NETDISK.md §7 Local run-through manual](NETDISK.md#7-local-runthrough-manual-test-guide),
> `scripts/test-layers.sh` (layered aggregation), `scripts/netdisk-local-demo.sh` (end-to-end).

### tutorial — Tutorial (the proper path for end users)

| File | Content |
|------|------|
| [tutorial/README.md](tutorial/README.md) | Tutorial entry point and reading order |
| [tutorial/01-run-and-connect.md](tutorial/01-run-and-connect.md) | Get it running & connect: install, start, see the other side |
| [tutorial/02-add-local-files.md](tutorial/02-add-local-files.md) | Add local files to the netdisk |
| [tutorial/03-share-levels.md](tutorial/03-share-levels.md) | Share levels public / unlisted / private |
| [tutorial/04-choose-what-to-share.md](tutorial/04-choose-what-to-share.md) | Choose what to share (directory / single file / collection) |
| [tutorial/05-save-from-other-nodes.md](tutorial/05-save-from-other-nodes.md) | Save content from other nodes |
| [tutorial/appendix-build-from-source.md](tutorial/appendix-build-from-source.md) | Appendix: build from source |

### archive — Archive (legacy docs / legacy structure, **no longer maintained**)

> The documents here describe the **pre-refactor structure** (`registration-server/`, `go/cmd/server/`,
> `p2p-dual-stack/`, `libp2p` main stack, `e2e-all.sh`, etc.). Kept only for decision traceability and history,
> **do not follow them** —— paths and endpoints no longer exist.

| File / Directory | Content |
|------|------|
| [archive/report/](archive/report/) | Legacy "reports" directory (36 docs: REPORT-OVERVIEW / ROADMAP / SECURITY-REVIEW / DEVELOPMENT_PLAN / TODO-FIXES / changelog / GIT-ANALYSIS / MILESTONE-* / various test and fix reports) |
| [archive/TUTORIAL.md](archive/TUTORIAL.md) | Legacy tutorial (superseded by [tutorial/](tutorial/README.md)) |
| [archive/DASHBOARD.md](archive/DASHBOARD.md) | Legacy project dashboard |
| [archive/LEGACY.md](archive/LEGACY.md) | Legacy stack remnants description |
| [archive/VPS_DEPLOY.md](archive/VPS_DEPLOY.md) | VPS deployment under the old structure (`registration-server/` etc. paths no longer exist) |
| [archive/docker.md](archive/docker.md) | Docker deployment under the old structure |
| [archive/p2p-discovery-flow.md](archive/p2p-discovery-flow.md) | Legacy libp2p discovery flow |
| [archive/TRANSPORT-REVIEW-2026-08-15.md](archive/TRANSPORT-REVIEW-2026-08-15.md), [archive/TRANSPORT-REVIEW2-2026-08-16.md](archive/TRANSPORT-REVIEW2-2026-08-16.md) | Two rounds of transport layer review |
| [archive/HTTP-REVIEW-2026-08-16.md](archive/HTTP-REVIEW-2026-08-16.md), [archive/REVIEW-FIX-2026-08-16.md](archive/REVIEW-FIX-2026-08-16.md) | HTTP layer review and fix checklist |
| [archive/FRONTEND-FIX-2026-08-16.md](archive/FRONTEND-FIX-2026-08-16.md), [archive/FRONTEND-FIXES-2026-08-16.md](archive/FRONTEND-FIXES-2026-08-16.md) | Two rounds of frontend fix records |
| [archive/SESSION-REPORT-20260503T135000.md](archive/SESSION-REPORT-20260503T135000.md) | 2026-05-03 session report |
| [archive/CODE-DOC-MAPPING.md](archive/CODE-DOC-MAPPING.md) | 2026-04 code ↔ document mapping table (most target documents no longer exist) |
| [archive/AGENTS.md](archive/AGENTS.md), [archive/proposal.md](archive/proposal.md), [archive/test-plan.md](archive/test-plan.md), [archive/knowledge-base.md](archive/knowledge-base.md), [archive/reply.md](archive/reply.md) | Early design drafts and reference materials |
| [modules/p2p/archive/](modules/p2p/archive/) | Legacy libp2p/BT-DHT stack: p2p / dual-stack-protocol / grid |
| [testing/archive/](testing/archive/) | Legacy stack era testing documentation (libp2p / e2e-all.sh / reg-server tests) |
