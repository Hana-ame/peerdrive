# Peerdrive Project File Reference Manual

> All file paths and descriptions, organized by module. Last updated: 2026-04-30.

---

## Root Directory

| File | Description |
|------|------|
| `README.md` | Project overview — P2P content-addressed file sharing system, with quick start, port descriptions |
| `.gitignore` | Root-level Git ignore rules |
| `opencode.sh` | OpenCode launch script |
| `test.sh` | Project-level test entry script |

---

## back/ — Go Backend (Gin + SQLite + libp2p + BT DHT)

### Entry Points

| File | Description |
|------|------|
| `back/cmd/server/main.go` | Server main entry — starts Gin HTTP server, initializes DB, registers routes |

### Configuration

| File | Description |
|------|------|
| `back/internal/config/config.go` | Config struct and loading logic (ports, DB path, P2P keys, etc.) |
| `back/internal/config/config_test.go` | Config package unit tests |
| `back/go.mod` | Go module definition (`peerdrive`), includes libp2p/BT/Gin dependencies |
| `back/go.sum` | Go dependency checksums |
| `back/.gitignore` | Backend .gitignore |

### Controller — HTTP Handlers

| File | Description |
|------|------|
| `back/internal/controller/anon.go` | Anonymous collection CRUD + anonymous file upload/download endpoints |
| `back/internal/controller/auth.go` | User authentication endpoints (register/login/token refresh) |
| `back/internal/controller/collection.go` | Collection management endpoints (create/delete/list/update/merge/fork) |
| `back/internal/controller/collection_test.go` | Collection controller tests |
| `back/internal/controller/download.go` | File download endpoints (HTTP range requests, resumable download) |
| `back/internal/controller/download_test.go` | Download controller tests |
| `back/internal/controller/file.go` | File CRUD endpoints (upload/list/rename/move/delete) |
| `back/internal/controller/file_test.go` | File controller tests |
| `back/internal/controller/fork.go` | Collection fork endpoint |
| `back/internal/controller/merge.go` | Collection merge endpoint (three-way merge, conflict resolution) |
| `back/internal/controller/p2p.go` | P2P transfer control endpoints (initiate/cancel/status query) |
| `back/internal/controller/p2p_download.go` | P2P download-specific endpoints |
| `back/internal/controller/ping.go` | Health check endpoint (`/ping`) |
| `back/internal/controller/ping_test.go` | Ping controller tests |
| `back/internal/controller/share.go` | Share link endpoints (generate/verify/delete) |
| `back/internal/controller/signal.go` | WebRTC signaling endpoints (SDP/ICE exchange) |
| `back/internal/controller/sync.go` | Device sync endpoints |
| `back/internal/controller/task.go` | Transfer task endpoints (list/retry/cancel/clear) |
| `back/internal/controller/webrtc.go` | WebRTC connection control endpoints |

### Service — Business Logic Layer

| File | Description |
|------|------|
| `back/internal/service/anon_service.go` | Anonymous collection business logic (create/read/write/browse/expiry management) |
| `back/internal/service/anon_service_test.go` | Anonymous service tests |
| `back/internal/service/auth_service.go` | Authentication service — token issuance/verification/user management |
| `back/internal/service/downloader.go` | HTTP downloader — fetches files from external URLs and indexes them |
| `back/internal/service/downloader_test.go` | Downloader tests |
| `back/internal/service/file_service.go` | File service — storage/indexing/deduplication/SHA256 verification |
| `back/internal/service/file_service_test.go` | File service tests |
| `back/internal/service/forward.go` | Request forwarding — transparent forwarding through relay nodes |
| `back/internal/service/ipfs_compat.go` | IPFS CID compatibility layer — CID ↔ SHA256 mapping, boxo/IPFSService fallback |
| `back/internal/service/ipfs_service.go` | **IPFSService** — boxo Bitswap client/server + peerdriveBlockstore + DHT provide/lookup |
| `back/internal/service/node_registrar.go` | Node registration — register and discover with registration-server |
| `back/internal/service/p2p.go` | P2P transfer core — dual-stack coordination (libp2p + BT DHT) |
| `back/internal/service/p2p_connection.go` | P2P connection management — establish/maintain/timeout/reconnect |
| `back/internal/service/p2p_dual.go` | Dual-stack transfer — libp2p and BT protocol parallel/switching |
| `back/internal/service/p2p_helpers.go` | P2P helper functions |
| `back/internal/service/p2p_multipeer.go` | Multi-Peer concurrent download — chunk scheduling and aggregation |
| `back/internal/service/p2p_resume.go` | Resumable transfer — transfer state persistence, resume logic |
| `back/internal/service/p2p_test.go` | P2P service tests |
| `back/internal/service/p2p_transfer.go` | P2P actual transfer — data block read/write, rate control |
| `back/internal/service/p2p_ws.go` | P2P WebSocket channel — signaling and metadata exchange |
| `back/internal/service/peer_scanner.go` | Peer scanner — actively discovers peer nodes on the network |
| `back/internal/service/peer_tracker.go` | Peer tracker — records and manages known node states |
| `back/internal/service/relay.go` | Relay service — provides traffic relay for NAT'd nodes |
| `back/internal/service/relay_registry.go` | Relay registration — relay node registration and discovery |
| `back/internal/service/signaling.go` | WebRTC signaling service — SDP/ICE candidate exchange |
| `back/internal/service/sync_service.go` | Device sync service — multi-device collection/file sync |
| `back/internal/service/sync_service_test.go` | Sync service tests |
| `back/internal/service/universal_downloader.go` | Universal downloader — selects HTTP/P2P/IPFS/BT download strategy by priority |
| `back/internal/service/universal_downloader_test.go` | Universal downloader tests |
| `back/internal/service/webdav.go` | WebDAV service — mounts collections as WebDAV drives |

### Repository — Data Persistence (SQLite)

| File | Description |
|------|------|
| `back/internal/repository/db.go` | Database initialization — SQLite connection, migration, connection pool |
| `back/internal/repository/anon_repo.go` | Anonymous collection data access — create/expiry cleanup/read/write |
| `back/internal/repository/collection_repo.go` | Collection data access — CRUD/version management/member queries |
| `back/internal/repository/collection_repo_test.go` | Collection repo tests |
| `back/internal/repository/file_repo.go` | File metadata access — SHA256 indexing/path management |
| `back/internal/repository/file_repo_test.go` | File repo tests |
| `back/internal/repository/pin_repo.go` | Pin record access — prevents GC cleanup marks |
| `back/internal/repository/share_repo.go` | Share link record access |
| `back/internal/repository/sync_repo.go` | Sync state record access |
| `back/internal/repository/task_repo.go` | Transfer task record access — create/status update/query |
| `back/internal/repository/user_repo.go` | User record access |

### Provider — Data Source Abstraction

| File | Description |
|------|------|
| `back/internal/provider/provider.go` | ContentProvider interface definition — unified file block acquisition abstraction |
| `back/internal/provider/provider_test.go` | Provider interface tests |
| `back/internal/provider/local.go` | Local filesystem Provider |
| `back/internal/provider/http.go` | HTTP/HTTPS remote file Provider |
| `back/internal/provider/ipfs.go` | IPFS network Provider (via Kubo RPC / HTTP Gateway) |
| `back/internal/provider/ipfs_test.go` | IPFS Provider tests |
| `back/internal/provider/manager.go` | Provider manager — register/select/failover |

### p2p_bt — BT DHT Implementation

| File | Description |
|------|------|
| `back/internal/p2p_bt/client.go` | BT DHT client — node startup, DHT join |
| `back/internal/p2p_bt/torrent.go` | Torrent management — metainfo parsing, piece verification |
| `back/internal/p2p_bt/magnet.go` | Magnet link parsing and handler |
| `back/internal/p2p_bt/tracker.go` | Tracker communication — announce/scrape |
| `back/internal/p2p_bt/seeder.go` | Seeder — local file seeding |
| `back/internal/p2p_bt/piece.go` | Piece management — chunked download, bitfield tracking |
| `back/internal/p2p_bt/bt_bridge.go` | BT bridge layer — connects Provider interface with BT download |
| `back/internal/p2p_bt/bt_dht.go` | Extended DHT — PUT/GET support (BEP-44) |
| `back/internal/p2p_bt/bep44.go` | BEP-44 implementation — DHT mutable data storage |
| `back/internal/p2p_bt/bep51.go` | BEP-51 implementation — DHT immutable indexing |
| `back/internal/p2p_bt/log.go` | BT module logging |
| `back/internal/p2p_bt/bt_test.go` | BT module tests |

### Model — Data Models

| File | Description |
|------|------|
| `back/internal/model/anon.go` | Anonymous collection model — metadata, expiry policy, token mapping |
| `back/internal/model/anon_test.go` | Anonymous model tests |
| `back/internal/model/collection.go` | Collection model — version chain, membership, permissions |
| `back/internal/model/collection_test.go` | Collection model tests |
| `back/internal/model/file.go` | File model — SHA256 Cid, block info, status |
| `back/internal/model/peer.go` | Peer node model — address, protocol, capabilities |
| `back/internal/model/share.go` | Share link model — token/permissions/expiry |
| `back/internal/model/sync.go` | Sync state model — device/operations/timestamps |
| `back/internal/model/transfer_task.go` | Transfer task model — progress/priority/retry |
| `back/internal/model/user.go` | User model — identity/role/keys |

### Router — Routing and Middleware

| File | Description |
|------|------|
| `back/internal/router/router.go` | Gin route registration — all endpoint mappings, middleware mounting |
| `back/internal/router/auth_middleware.go` | Auth middleware — JWT verification, role checking, anonymous access control |

### Others

| File | Description |
|------|------|
| `back/internal/log/log.go` | Structured logging wrapper |
| `back/internal/nodestate/nodestate.go` | Node runtime state management (online/offline/busy) |
| `back/pkg/hashutil/hashutil.go` | SHA256 hash utility functions |
| `back/docs/docs.go` | Swagger documentation generated code |

### Test — Test Scripts

| File | Description |
|------|------|
| `back/test/all.sh` | All tests entry |
| `back/test/e2e-all.sh` | E2E full test suite |
| `back/test/anonymous_test.sh` | Anonymous collection tests |
| `back/test/auth_test.sh` | Authentication flow tests |
| `back/test/auth-full-test.sh` | Full authentication tests |
| `back/test/auth-node-test.sh` | Auth node tests |
| `back/test/bt-full-test.sh` | BT DHT full integration tests |
| `back/test/bt-integration/create-torrent.sh` | Torrent creation helper script |
| `back/test/bt-integration/main.go` | BT integration test entry program |
| `back/test/e2e-all.sh` | E2E full test suite |
| `back/test/ipfs-full-test.sh` | IPFS full integration tests |
| `back/test/ipfs-integration/main.go` | IPFS integration test entry program |
| `back/test/ipfs-peer-test.sh` | IPFS multi-peer tests |
| `back/test/p2p.sh` | P2P basic tests |
| `back/test/p2p-full-test.sh` | P2P full tests |
| `back/test/p2p_transfer.sh` | P2P transfer tests |
| `back/test/peerdrive-functional.mjs` | Peerdrive functional tests (Node.js) |
| `back/test/peerdrive-new-features.mjs` | New features tests (Node.js) |
| `back/test/peerdrive-smoke.mjs` | Smoke tests (Node.js) |
| `back/test/register.sh` | Registration flow tests |
| `back/test/reg-server-user-mgmt.sh` | Registration server user management tests (deleted with old stack cleanup) |
| `back/test/relay.sh` | Relay functionality tests |
| `back/test/storage-full-test.sh` | Full storage tests |
| `back/test/upload.sh` | Upload functionality tests |
| `back/test/webrtc_signal_test.sh` | WebRTC signaling tests |
| `back/test/webrtc-test.sh` | Full WebRTC tests |
| `back/test/anon-collection.sh` | Anonymous collection tests |
| `back/test/p2p-full-test.sh` | P2P full tests |
| `back/test/diagnose.py` | Test diagnostics Python script |
| `back/testdata/test.txt` | Test data file |

---

## front/ — React Frontend (Vite + TailwindCSS)

### Entry and Configuration

| File | Description |
|------|------|
| `front/index.html` | HTML entry — SPA mount point |
| `front/package.json` | Node dependencies definition |
| `front/package-lock.json` | Dependency lock file |
| `front/vite.config.ts` | Vite build configuration |
| `front/vitest.config.ts` | Vitest test configuration |
| `front/tailwind.config.js` | TailwindCSS configuration |
| `front/postcss.config.js` | PostCSS configuration |
| `front/.gitignore` | Frontend .gitignore |
| `front/README.md` | Frontend README |

### public/ — Static Assets

| File | Description |
|------|------|
| `front/public/favicon.svg` | Website icon |
| `front/public/icons.svg` | SVG icon sprite |
| `front/public/icons/icon-192.png` | PWA 192px icon |
| `front/public/icons/icon-512.png` | PWA 512px icon |
| `front/public/manifest.json` | PWA manifest |
| `front/public/service-worker.js` | Service Worker (offline caching) |
| `front/public/_redirects` | Netlify/Caddy redirect rules |

### src/ — Source Code

#### Core Framework

| File | Description |
|------|------|
| `front/src/main.jsx` | React application entry — mounts root component and routes |
| `front/src/App.jsx` | Root component — global layout, route distribution, state management |
| `front/src/api.js` | API client — Axios wrapper, token injection, error handling |
| `front/src/index.css` | Global styles — Tailwind directives and custom |

#### components/ — Shared Components

| File | Description |
|------|------|
| `front/src/components/ActiveConnPanel.jsx` | Active connections panel — displays current P2P connection list |
| `front/src/components/AnonCollectionManager.jsx` | Anonymous collection manager — create and manage anonymous collections |
| `front/src/components/CollectionBuilder.jsx` | Collection builder wizard |
| `front/src/components/CollectionCard.jsx` | Collection card — cover/title/summary display |
| `front/src/components/CommentSection.jsx` | Comment section component |
| `front/src/components/FileTree.jsx` | File tree component — tree directory browsing and operations |
| `front/src/components/LLMAssistant.jsx` | LLM AI assistant panel |
| `front/src/components/MobileNav.jsx` | Mobile navigation bar |
| `front/src/components/Navbar.jsx` | Desktop navigation bar |
| `front/src/components/P2PStatus.jsx` | P2P connection status indicator |
| `front/src/components/PathRegistrar.jsx` | Path registrar component — registers with registration-server |
| `front/src/components/PeerDetailPanel.jsx` | Peer detail panel — node info/status/statistics |
| `front/src/components/ServiceStatus.jsx` | Backend service status indicator (ping-based) |
| `front/src/components/SettingsSection.jsx` | Settings panel component |
| `front/src/components/Sha256Manager.jsx` | SHA256 file management — content-addressed operations |
| `front/src/components/UserGroupPicker.jsx` | User/group picker |
| `front/src/components/VersionLog.jsx` | Version history viewer (Git-style) |
| `front/src/components/VisibilityPicker.jsx` | Visibility picker (public/private/group) |
| `front/src/components/WebRTCPeer.jsx` | WebRTC Peer management component |
| `front/src/components/WebRTCTransfer.jsx` | WebRTC file transfer component |

#### pages/AnonCreator/ — Anonymous Collection Creator Page

| File | Description |
|------|------|
| `front/src/pages/AnonCreator/index.jsx` | AnonCreator page main entry — three-column layout container |
| `front/src/pages/AnonCreator/constants.js` | Page constants (categories, tags, default values) |
| `front/src/pages/AnonCreator/utils.js` | Utility functions (formatting, validation, state calculation) |
| `front/src/pages/AnonCreator/CollBrowser.jsx` | Collection browser — browse referencable collections |
| `front/src/pages/AnonCreator/CollBrowserNav.jsx` | Collection browser navigation |
| `front/src/pages/AnonCreator/CollectionHeader.jsx` | Collection header information display |
| `front/src/pages/AnonCreator/CollectionRow.jsx` | Collection list row |
| `front/src/pages/AnonCreator/CollFileRow.jsx` | File row within collection |
| `front/src/pages/AnonCreator/EditorPanel.jsx` | Editor panel — collection metadata editing |
| `front/src/pages/AnonCreator/EditorToolbar.jsx` | Editor toolbar |
| `front/src/pages/AnonCreator/FileSourceRow.jsx` | File source row — source selection and preview |
| `front/src/pages/AnonCreator/LeftPanel.jsx` | Left panel — data source filtering |
| `front/src/pages/AnonCreator/MiddlePanel.jsx` | Middle panel — file preview |
| `front/src/pages/AnonCreator/RightPanel.jsx` | Right panel — collection edit/publish |
| `front/src/pages/AnonCreator/NamePrompt.jsx` | Naming prompt dialog |
| `front/src/pages/AnonCreator/RegisteredView.jsx` | Registered collection view |
| `front/src/pages/AnonCreator/SearchHistory.jsx` | Search history records |
| `front/src/pages/AnonCreator/SourceFilters.jsx` | Data source filter |
| `front/src/pages/AnonCreator/SourceTabs.jsx` | Data source tabs |
| `front/src/pages/AnonCreator/SplitHandle.jsx` | Panel split handle (drag to resize) |
| `front/src/pages/AnonCreator/SystemBrowse.jsx` | System file browser |
| `front/src/pages/AnonCreator/TimelineView.jsx` | Timeline view |
| `front/src/pages/AnonCreator/Toast.jsx` | Toast notification |

#### pages/AnonExplorer/ — Anonymous Collection Explorer Page

| File | Description |
|------|------|
| `front/src/pages/AnonExplorer/index.jsx` | AnonExplorer page main entry |
| `front/src/pages/AnonExplorer/utils.js` | Utility functions |
| `front/src/pages/AnonExplorer/BreadcrumbNav.jsx` | Breadcrumb navigation |
| `front/src/pages/AnonExplorer/CollectionHeader.jsx` | Collection header information display |
| `front/src/pages/AnonExplorer/EmptyState.jsx` | Empty state placeholder |
| `front/src/pages/AnonExplorer/FileList.jsx` | File list |
| `front/src/pages/AnonExplorer/FileRow.jsx` | File list row |
| `front/src/pages/AnonExplorer/GenericFilePreview.jsx` | Generic file preview (binary/unknown format) |
| `front/src/pages/AnonExplorer/ImagePreview.jsx` | Image preview component |
| `front/src/pages/AnonExplorer/NestedCollectionLink.jsx` | Nested collection link (collection referencing collection) |
| `front/src/pages/AnonExplorer/PdfPreview.jsx` | PDF preview component |
| `front/src/pages/AnonExplorer/SearchBar.jsx` | Search bar |
| `front/src/pages/AnonExplorer/SingleFilePreview.jsx` | Single file preview |
| `front/src/pages/AnonExplorer/TextPreview.jsx` | Text file preview |
| `front/src/pages/AnonExplorer/Toast.jsx` | Toast notification |

#### pages/ — Other Pages

| File | Description |
|------|------|
| `front/src/pages/BTController.jsx` | BT DHT controller page |
| `front/src/pages/BTPanel.jsx` | BT panel — torrent/magnet link management |
| `front/src/pages/DHTExplorer.jsx` | DHT network browser |
| `front/src/pages/Explorer.jsx` | File manager — global file browsing |
| `front/src/pages/FileManager.jsx` | File management page — upload/organize/delete |
| `front/src/pages/IPFSPanel.jsx` | IPFS panel — CID query/content management |
| `front/src/pages/P2PDashboard.jsx` | P2P dashboard — global status overview |
| `front/src/pages/P2PPanel.jsx` | P2P panel — node/connection/transfer control |
| `front/src/pages/P2PTopology.jsx` | P2P topology graph — network visualization |
| `front/src/pages/Plaza.jsx` | Collection plaza — public collection browsing and discovery |
| `front/src/pages/Settings.jsx` | Settings page |

#### storage/ — Local Storage

| File | Description |
|------|------|
| `front/src/storage/localDB.js` | Local database — IndexedDB wrapper, frontend cache |
| `front/src/storage/syncManager.js` | Sync manager — frontend ⇄ backend data sync |

#### tests/ — Frontend Tests

| File | Description |
|------|------|
| `front/tests/setup.js` | Test environment initialization (jsdom, mocks) |
| `front/tests/components.test.jsx` | Component unit tests |
| `front/tests/FileTree.test.jsx` | FileTree component unit tests |
| `front/tests/smoke.test.jsx` | Frontend smoke tests |
| `front/tests/playwright-smoke.mjs` | Playwright E2E smoke tests |

#### dist/ — Build Artifacts

| File | Description |
|------|------|
| `front/dist/index.html` | Built HTML entry |
| `front/dist/manifest.json` | Build manifest |
| `front/dist/favicon.svg` | Built favicon |
| `front/dist/icons.svg` | Built icon sprite |
| `front/dist/service-worker.js` | Built Service Worker |
| `front/dist/assets/index-C5vd8htV.css` | Built CSS bundle |
| `front/dist/assets/index-D4NOIXnB.js` | Built JS bundle |

---

## doc/ — Project Documentation

### Entry and Dashboard

| File | Description |
|------|------|
| `doc/README.md` | Documentation overview + full index — architecture, layered design, module mapping, all document entries |
| `doc/ROADMAP.md` | Development sequence schedule (currently active; distinguished from `doc/archive/report/ROADMAP.md` v3.0 historical roadmap) |
| `doc/REFACTOR.md` | Refactoring manual |
| `doc/NETDISK.md` | Netdisk (PeerJS node + panel) manual |
| `doc/LAYERS.md` | Layered architecture description |
| `doc/NODE.md` / `doc/NODE-API.md` | Node / Node API |
| `doc/PEERSIGNAL.md` | Self-hosted signaling |
| `doc/PROJECT-VISION.md` | Product vision |
| `doc/HTTP_API_PROXY.md` | HTTP API proxy |
| `doc/TODO-SIMPLIFY.md` | Simplification TODO |
| `doc/dht-wire-format.md` | DHT wire format |
| `doc/source-control.md` | Version control (collection fork/merge) |
| `doc/api-reference.md` | API reference (simplified) |
| `doc/tutorial/` | Tutorials (for users) |

### spec/ — Technical Specifications

| File | Description |
|------|------|
| `doc/spec/REQUIREMENTS.md` | Full requirements table (130+ items) |
| `doc/spec/API-REFERENCE.md` | Complete API reference (105 endpoints) |
| `doc/spec/COLLECTION-LOGIC.md` | Complete collection logic trace |
| `doc/spec/USER-ROLES.md` | User role model |
| `doc/spec/BACKEND_TASKS.md` | Backend task checklist |
| `doc/spec/FRONTEND_TASKS.md` | Frontend task checklist |

#### spec/backend/ — Backend Specifications

| File | Description |
|------|------|
| `doc/spec/backend/BACKEND_DOC.md` | Backend overview document |
| `doc/spec/backend/IMPLEMENTATION_SPEC.md` | Implementation specification |
| `doc/spec/backend/design.md` | Backend design document |
| `doc/spec/backend/api-reference.md` | Backend API reference |
| `doc/spec/backend/backend-reference.md` | Backend code reference |
| `doc/spec/backend/database.md` | Database design (table structure/indexes/migrations) |
| `doc/spec/backend/sha256-download.md` | SHA256 content-addressed download design |
| `doc/spec/backend/upload.md` | File upload flow design |
| `doc/spec/backend/register.md` | Registration server interaction design |
| `doc/spec/backend/anon-collection.md` | Anonymous collection design |

#### spec/frontend/ — Frontend Specifications

| File | Description |
|------|------|
| `doc/spec/frontend/FRONTEND_DOC.md` | Frontend overview document |
| `doc/spec/frontend/API_DOC.md` | Frontend API call documentation |

### modules/ — Module Design Documents

#### modules/auth/ — Authentication Module

| File | Description |
|------|------|
| `doc/modules/auth/README.md` | Authentication module overview |
| `doc/modules/auth/API-DESIGN.md` | Authentication API design |
| `doc/modules/auth/SECURITY-REVIEW.md` | Authentication security review |
| `doc/modules/auth/USER-ROLES.md` | User roles and permissions |

#### modules/bt/ — BT DHT Module

| File | Description |
|------|------|
| `doc/modules/bt/README.md` | BT module overview |
| `doc/modules/bt/API-DESIGN.md` | BT API design |
| `doc/modules/bt/bt-dht-protocol.md` | BT DHT protocol design |
| `doc/modules/bt/TEST-MATRIX.md` | BT test matrix |

#### modules/ipfs/ — IPFS Module

| File | Description |
|------|------|
| `doc/modules/ipfs/README.md` | IPFS module overview |
| `doc/modules/ipfs/API-DESIGN.md` | IPFS API design |
| `doc/modules/ipfs/ipfs-protocol.md` | IPFS integration protocol design |
| `doc/modules/ipfs/webrtc-architecture.md` | WebRTC architecture design (browser IPFS direct connection) |

#### modules/p2p/ — P2P Module

| File | Description |
|------|------|
| `doc/modules/p2p/README.md` | P2P module overview |
| `doc/modules/p2p/API-DESIGN.md` | P2P API design |
| `doc/modules/p2p/TRANSPORT.md` | Current interconnection framework (WS + PeerJS + Session abstraction) |

#### modules/storage/ — Storage Module

| File | Description |
|------|------|
| `doc/modules/storage/README.md` | Storage module overview |
| `doc/modules/storage/API-DESIGN.md` | Storage API design |
| `doc/modules/storage/api-reference.md` | Storage API reference |
| `doc/modules/storage/COLLECTION-LOGIC.md` | Detailed collection logic design |
| `doc/modules/storage/database.md` | Database design |

### guide/ — User Guides

| File | Description |
|------|------|
| `doc/guide/API-USAGE.md` | API usage manual — call order/purpose/conditions |
| `doc/guide/USER_MANUAL.md` | User manual |
| `doc/archive/VPS_DEPLOY.md` | VPS deployment guide (archived: points to pre-refactor structure) |
| `doc/archive/docker.md` | Docker deployment guide (archived: points to pre-refactor structure) |
| `doc/guide/siliconflow-setup.md` | SiliconFlow LLM API configuration |
| `doc/guide/operation-manual.md` | Chinese operation instructions |

### archive/report/ — Project Reports (2026-04~05 historical reports, archived)

| File | Description |
|------|------|
| `doc/archive/report/REPORT-OVERVIEW.md` | Report overview |
| `doc/archive/report/index.md` | Report index |
| `doc/archive/report/DEVELOPMENT_PLAN.md` | Development plan |
| `doc/archive/report/ROADMAP.md` | Product roadmap |
| `doc/archive/report/MILESTONE-P2P.md` | P2P milestone |
| `doc/archive/report/MILESTONE-p2p-vps.md` | P2P VPS deployment milestone |
| `doc/archive/report/changelog.md` | Changelog |
| `doc/archive/report/refactor-report.md` | Refactoring report |
| `doc/archive/report/SECURITY-REVIEW.md` | Security review report |
| `doc/archive/report/grid.md` | Grid topology report |
| `doc/archive/report/TASK-COMPLETION-2026-04-29.md` | 2026-04-29 task completion report |
| `doc/archive/report/test-report-2026-04-29.md` | 2026-04-29 test report |
| `doc/archive/report/TXT-REPLY.md` | TXT reply records |
| `doc/archive/report/TXT-STATUS.md` | TXT status records |
| `doc/archive/report/MEMO.md` | Development memo |
| `doc/archive/report/memo-go.md` | Go development memo |
| `doc/archive/report/ISSUES_FOR_GEMINI.md` | Issues to report to Gemini |
| `doc/archive/report/CI-FIXES.md` | CI fix records |
| `doc/archive/report/TODO-FIXES.md` | TODO fix checklist |
| `doc/archive/report/TODO-P2P-DUAL-STACK.md` | P2P dual-stack TODO |

### testing/ — Testing Documents

| File | Description |
|------|------|
| `doc/testing/index.md` | Testing documentation portal |
| `doc/testing/README.md` | Testing overview |
| `doc/testing/TESTING-HANDBOOK.md` | Testing handbook |
| `doc/testing/TESTING-METHODOLOGY.md` | Testing methodology |
| `doc/testing/TEST-MATRIX.md` | Test matrix |
| `doc/testing/TEST-PIPELINE.md` | Test pipeline design |
| `doc/testing/CHAOS_TESTING.md` | Chaos testing plan |
| `doc/testing/archive/test-plan.md` | Chinese testing plan |
| `doc/testing/archive/how-to-test.md` | Chinese testing guide |
| `doc/testing/archive/reg-server-test-plan.md` | Registration server test plan (archived) |

### archive/ — Historical Archive

| File | Description |
|------|------|
| `doc/archive/proposal.md` | Historical proposal document |
| `doc/archive/test-plan.md` | Historical testing plan |
| `doc/archive/knowledge-base.md` | Historical knowledge base |
| `doc/archive/AGENTS.md` | Historical Agent configuration |
| `doc/archive/reply.md` | Historical reply records |
| `doc/archive/DASHBOARD.md` | Project dashboard (old structure snapshot) |
| `doc/archive/LEGACY.md` | Legacy stack (libp2p/BT/WebDAV/frontend dead code) disposal checklist |
| `doc/archive/TUTORIAL.md` | Old tutorial (libp2p era, replaced by `doc/tutorial/`) |
| `doc/archive/CODE-DOC-MAPPING.md` | Code ↔ document mapping table (2026-04 snapshot, most target documents no longer exist) |
| `doc/archive/VPS_DEPLOY.md` | VPS deployment (old structure) |
| `doc/archive/docker.md` | Docker deployment (old structure) |
| `doc/archive/p2p-discovery-flow.md` | Old libp2p discovery flow |
| `doc/archive/TRANSPORT-REVIEW-2026-08-15.md` / `-2-2026-08-16.md` | Transport layer two-round reviews |
| `doc/archive/HTTP-REVIEW-2026-08-16.md` / `REVIEW-FIX-…` | HTTP layer reviews and fix checklists |
| `doc/archive/FRONTEND-FIX-2026-08-16.md` / `FRONTEND-FIXES-…` | Frontend fix records |
| `doc/archive/SESSION-REPORT-20260503T135000.md` | 2026-05-03 session report |
| `doc/modules/p2p/archive/` | Old libp2p/BT-DHT stack documents (p2p / dual-stack-protocol / grid) |

---

## .github/workflows/ — CI/CD

| File | Description |
|------|------|
| `.github/workflows/ci.yml` | Project CI pipeline — backend tests + frontend tests + build |
| `.github/workflows/go-build.yml` | Go multi-platform build matrix (linux/macos/windows) |
| `.github/workflows/release.yml` | Release publishing process |
| `front/.github/workflows/ci.yml` | Frontend CI pipeline |

---

## Statistics

| Category | Count |
|------|------|
| Go source files | ~75 |
| Frontend source files (JSX/TS/JS/CSS) | ~70 |
| Test scripts (.sh/.mjs/.py) | ~25 |
| Documentation (.md) | ~65 |
| CI/CD configurations | 4 |
| **Total** | **~240** |
