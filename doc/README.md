# Peerdrive Architecture Documentation

> Content-addressed + Git-style version-controlled P2P file sharing system · v3.0

## Monorepo Structure

```
peerdrive/
├── front/                    React frontend (Vite + TailwindCSS + Vitest)
│   ├── src/
│   │   ├── pages/
│   │   │   ├── AnonCreator/  Collection creation page (three-column layout: filter | preview | edit)
│   │   │   ├── AnonExplorer/ Collection browsing page
│   │   │   ├── Plaza/        Collection plaza
│   │   │   ├── FileManager/  File management
│   │   │   ├── P2PDashboard/ P2P dashboard
│   │   │   └── Settings/     Settings
│   │   ├── components/       Shared components
│   │   └── api.js            API client
│   └── tests/                Frontend tests
├── back/                     Go backend (Gin + SQLite + libp2p + BT DHT)
│   ├── cmd/server/           Entry point
│   ├── internal/
│   │   ├── controller/       HTTP handler layer
│   │   ├── service/          Business logic layer (P2P/files/collections/downloads)
│   │   ├── repository/       SQLite persistence
│   │   ├── provider/         Data source abstraction (local/HTTP/IPFS)
│   │   ├── router/           Gin routing & middleware
│   │   ├── model/            Data models
│   │   ├── config/           Configuration
│   │   └── p2p_bt/           BT DHT implementation
│   ├── pkg/hashutil/         Hash utilities
│   └── test/                 Integration/E2E test scripts
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
      → P2P (libp2p / BT DHT / WebRTC)
      → IPFSService (boxo Bitswap + DHT)  ← New
```

| Layer | Location | Responsibility |
|----|------|------|
| Router | `back/internal/router/` | Route registration, CORS, Auth middleware |
| Controller | `back/internal/controller/` | HTTP handling, parameter parsing |
| Service | `back/internal/service/` | Core logic: file registration/download, collection CRUD/versions, P2P transport/signaling, **IPFSService (boxo Bitswap)** |
| Repository | `back/internal/repository/` | SQLite CRUD |
| Provider | `back/internal/provider/` | Data source interface: `local` / `http` / `ipfsgw` (IPFS prefers Bitswap) |
| P2P BT | `back/internal/p2p_bt/` | Mainline DHT, BEP44/BEP51, torrent/magnet |

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
| File Management | `FileManager.jsx`, `Sha256Manager.jsx` | `controller/file.go`, `service/file_service.go` |
| Collections | `AnonCreator/`, `AnonExplorer/`, `CollectionBuilder.jsx` | `controller/anon.go`, `controller/collection.go`, `service/anon_service.go` |
| P2P | `P2PDashboard.jsx`, `P2PTopology.jsx`, `WebRTCPeer.jsx` | `service/p2p.go`, `controller/p2p.go` |
| BT DHT | `BTController.jsx`, `BTPanel.jsx` | `p2p_bt/`, `controller/p2p.go` (BT endpoints) |
| IPFS | `IPFSPanel.jsx` | `provider/ipfs.go`, `service/ipfs_compat.go` |
| WebRTC | `WebRTCTransfer.jsx` | `service/p2p.go` (WebRTC signaling) |
| Authentication | `UserGroupPicker.jsx`, `VisibilityPicker.jsx` | `controller/auth.go`, `service/auth_service.go` |

## Data Flow

```
Upload:   front → POST /files/upload → Controller → FileService → Provider(local) → SQLite
Download: front → GET /sha256sum/:hash → Controller → Downloader → Provider → Response
P2P:      Node ← libp2p DHT → Discover Peers → WS/WebRTC Transfer
BT:       Node ← Mainline DHT → Find Peers → Torrent Download
Collection: front → POST /anon/collections → AnonService → CollectionRepo → JSON → SHA256
```

## Quick Start

```bash
# Backend
cd back
go run ./cmd/server/
# → http://localhost:3000 , Swagger at /swagger/index.html

# Frontend
cd front
npm ci && npm run dev
# → http://localhost:5173
```

## Testing

```bash
# Backend unit tests
cd back && go test ./... -count=1

# E2E full endpoint tests
cd back && bash test/e2e-all.sh

# P2P dual-node tests
cd back && bash test/p2p.sh

# Frontend tests
cd front && npm test

# Frontend build
cd front && npm run build
```

## Environment Variables

| Variable | Default | Description |
|------|--------|------|
| `PORT` | `3000` | Backend port |
| `PEERDRIVE_STORAGE` | `./storage` | File storage directory |
| `PEERDRIVE_P2P_ENABLE` | `true` | Enable libp2p |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | P2P listen address |
| `PEERDRIVE_BT_DHT_ENABLE` | `true` | Enable BT DHT |
| `PEERDRIVE_MDNS_ENABLE` | `true` | LAN discovery |
| `PEERDRIVE_RELAY_ENABLE` | `false` | Relay mode |
| `PEERDRIVE_RELAY_MODE` | `client` | `server` / `client` |
| `PEERDRIVE_HOLE_PUNCH` | `true` | NAT hole punching |
| `CORS_MODE` | Allowlist | `all` / `localhost` |
| `VITE_API_BASE` | `http://localhost:3000` | Frontend API base URL |
| `PEERDRIVE_DISCOVER_URL` | Empty | Self-hosted signaling discovery API (when set, takes priority over MQTT) |
| `PEERDRIVE_DISCOVER_PRESENCE` | `true` | Node-level "presence room" discovery: nodes with zero shared collections can also discover each other (see [ROADMAP.md](ROADMAP.md) Phase 1, [REFACTOR.md](REFACTOR.md) §3.18) |
| `PEERDRIVE_MAX_PEERS` | `8` | Dial limit triggered by discovery (prevents full-mesh degradation); static `PEERDRIVE_PEERJS_PEERS` is unlimited |
| `PEERDRIVE_DB_PATH` | `./peerdrive.db` | SQLite metadata database path (new, 2026-09-23) |
| `PEERDRIVE_RATE_LIMIT_RPS` | `30` | Per-IP request rate limit, 0=unlimited (new) |
| `PEERDRIVE_CSP` | On | Set to `off` to disable Content-Security-Policy (new) |
| `PEERDRIVE_TRUSTED_PROXIES` | Empty | Trusted reverse proxies (IP/CIDR comma-separated or `all`); empty=only trust RemoteAddr (new) |

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
| [FILE-REFERENCE.md](FILE-REFERENCE.md) | Complete project file path and description manual (~240 files) |

### spec — Technical Specifications

| File | Content |
|------|------|
| [spec/API-REFERENCE.md](spec/API-REFERENCE.md) | Complete API reference (105 endpoints) |
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
