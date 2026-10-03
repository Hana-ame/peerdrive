# Peerdrive Complete Requirements List

> Last updated: 2026-04-28 · Markers: ✅ Done 🚧 In Progress 📋 To Develop

---

## 1. File System

| # | Requirement | Status |
|---|-------------|--------|
| 1.1 | SHA256 content-addressed storage | ✅ |
| 1.2 | File upload (multipart) | ✅ |
| 1.3 | Local file registration | ✅ |
| 1.4 | Recursive folder registration | ✅ |
| 1.5 | URL file registration | ✅ |
| 1.6 | SHA256 download | ✅ |
| 1.7 | Range chunked download | ✅ |
| 1.8 | CID dual index (/ipfs/:cid) | ✅ |
| 1.9 | File verification (exists/consistent) | ✅ |
| 1.10 | File deletion | ✅ |
| 1.11 | File browsing (BrowseDir) | ✅ |
| 1.12 | Upload size limit (100MB authenticated/10MB anonymous) | ✅ |
| 1.13 | IPFS public gateway pull (ipfs.io/cloudflare-ipfs) | ✅ |
| 1.14 | Filesize metadata recording | ✅ |

## 2. Collection System

| # | Requirement | Status |
|---|-------------|--------|
| 2.1 | Anonymous collection creation (immutable) | ✅ |
| 2.2 | Collection viewing (SHA256/URL) | ✅ |
| 2.3 | Collection list | ✅ |
| 2.4 | Single file preview (image/PDF/text) | ✅ |
| 2.5 | Single file collection shows file icon+filename (not 📦) | ✅ |
| 2.6 | Empty collection auto-delete | ✅ |
| 2.7 | Nested collection links | ✅ |
| 2.8 | Version management (Commit/Log/Rollback) | ✅ |
| 2.9 | Fork | ✅ |
| 2.10 | Merge | ✅ |
| 2.11 | Sharing link (token) | ✅ |
| 2.12 | Collection name priority (collection_name→friendly_name→name_preview→hash) | ✅ |
| 2.13 | "Broadcast"=Create single file collection+dual network announce | ✅ |
| 2.14 | Collection name AI suggestion (LLM) | ✅ |
| 2.15 | Sharing creation input box (not alert) | ✅ |
| 2.16 | Local/P2P collections tab separation | ✅ |

## 3. P2P Network

| # | Requirement | Status |
|---|-------------|--------|
| 3.1 | libp2p host (TCP/QUIC/WebSocket) | ✅ |
| 3.2 | mDNS LAN discovery | ✅ |
| 3.3 | DHT (IPFS Kademlia) | ✅ |
| 3.4 | Ping latency measurement | ✅ |
| 3.5 | Exchange protocol (/peerdrive/exchange/1.0.0) | ✅ |
| 3.6 | Relay (server/client) | ✅ |
| 3.7 | NAT hole punching | ✅ |
| 3.8 | AutoNAT | ✅ |
| 3.9 | IPv6 support | ✅ |
| 3.10 | Connection manager (heartbeat/reconnect/stats) | ✅ |
| 3.11 | Peer detail tracking (first_seen/last_seen/bytes/transports) | ✅ |
| 3.12 | Peer Scanner (DHT/Reg/LAN/Bootstrap) | ✅ |
| 3.13 | WebRTC signaling (room mode) | ✅ |
| 3.14 | P2P port forwarding (same key, experimental) | ✅ |
| 3.15 | Chunked transfer (256KB chunk/8 concurrent) | ✅ |
| 3.16 | Resumable download (ResumeManager) | ✅ |
| 3.17 | Multi-peer parallel download | ✅ |
| 3.18 | Public relay node (VPS systemd) | ✅ |
| 3.19 | IPFS compatibility mode (Bitswap blockstore) | ✅ |
| 3.20 | IPFS gateway pull | ✅ |
| 3.21 | Relay mode explanation (client/server) | ✅ |
| 3.22 | Peer protocol version display | ✅ |

## 4. BitTorrent

| # | Requirement | Status |
|---|-------------|--------|
| 4.1 | BT DHT (Mainline, UDP) | ✅ |
| 4.2 | BT announce (global DHT) | ✅ |
| 4.3 | BT find (cross-node verification) | ✅ |
| 4.4 | .torrent file parsing | ✅ |
| 4.5 | Magnet link parsing | ✅ |
| 4.6 | Wire protocol (handshake/piece exchange) | ✅ |
| 4.7 | HTTP Tracker support | ✅ |
| 4.8 | BT downloader panel (frontend) | ✅ |
| 4.9 | Pause/Resume/Remove | ✅ |
| 4.10 | Global DHT connection verification (127+ nodes) | ✅ |
| 4.11 | BEP 44 (DHT data storage) | ✅ |
| 4.12 | BEP 51 (Infohash indexing) | ✅ |
| 4.13 | BT error prompts (hover tooltip) | ✅ |
| 4.14 | Full BT client features | ✅ |

## 5. IPFS Interoperability

| # | Requirement | Status |
|---|-------------|--------|
| 5.1 | CID calculation (SHA256→CIDv1) | ✅ |
| 5.2 | /ipfs/:cid download endpoint | ✅ |
| 5.3 | IPFS gateway pull (3 public gateways) | ✅ |
| 5.4 | IPFS compatibility mode (Bitswap) | ✅ |
| 5.5 | IPFS toggle (Settings switch) | ✅ |
| 5.6 | Real IPFS peer (kubo) connection test | ✅ |
| 5.7 | Bitswap interoperability | ✅ |

## 6. Registration and Authentication

| # | Requirement | Status |
|---|-------------|--------|
| 6.1 | Registration Server (JWT) | ✅ |
| 6.2 | User registration/login | ✅ |
| 6.3 | Token authentication (endpoint#token) | ✅ |
| 6.4 | Auth middleware | ✅ |
| 6.5 | Relay registration/discovery/heartbeat | ✅ |
| 6.6 | User group query | ✅ |
| 6.7 | Determine whether to provide relay/p2p services based on user info | ✅ |
| 6.8 | Node operator account info query | ✅ |
| 6.9 | Registered user DB storage space | ✅ |

## 7. Frontend

| # | Requirement | Status |
|---|-------------|--------|
| 7.1 | Plaza collection square (local/P2P tab) | ✅ |
| 7.2 | AnonCreator (4-tab: timeline/registered/local/collection) | ✅ |
| 7.3 | AnonExplorer (single file preview) | ✅ |
| 7.4 | FileManager (checkbox/multi-select/share) | ✅ |
| 7.5 | Explorer (user collections) | ✅ |
| 7.6 | P2PDashboard (network dashboard) | ✅ |
| 7.7 | IPFS panel (/ipfs) | ✅ |
| 7.8 | BT DHT panel (/bt) | ✅ |
| 7.9 | P2P dual-stack panel (/p2p) | ✅ |
| 7.10 | BT downloader (/bt/controller) | ✅ |
| 7.11 | Settings (LLM/P2P/IPFS/BT/WebDAV config) | ✅ |
| 7.12 | PWA (manifest/service-worker/mobile nav) | ✅ |
| 7.13 | LLM assistant (function calling) | ✅ |
| 7.14 | LLM page context update | ✅ |
| 7.15 | LLM SSE streaming+thinking display | ✅ |
| 7.16 | Drag files to collection editor | ✅ |
| 7.17 | File directory tree (VSCode-style) | ✅ |
| 7.18 | Windows-like file operations | ✅ |
| 7.19 | Unified collection UI (AnonExplorer) | ✅ |

## 8. Storage and Sync

| # | Requirement | Status |
|---|-------------|--------|
| 8.1 | Anonymous user localStorage | ✅ |
| 8.2 | Three-layer mutual backup (localStorage↔Reg Server↔Node) | 🚧 |
| 8.3 | Registered user DB space | 📋 |
| 8.4 | WebDAV mount | ✅ |
| 8.5 | Local storage progress indicator (loadingProgress) | ✅ |

## 9. Testing

| # | Requirement | Status |
|---|-------------|--------|
| 9.1 | Go unit tests (6 packages) | ✅ |
| 9.2 | BT integration tests (7 tests) | ✅ |
| 9.3 | Playwright Smoke (16 tests) | ✅ |
| 9.4 | Playwright Functional (39/44) | 🚧 |
| 9.5 | Cross-machine P2P (Docker↔WSL) | ✅ |
| 9.6 | Docker 5-node networking | 🚧 |
| 9.7 | Chaos network testing (chaos-net.sh) | ✅ |
| 9.8 | Test peers (ipfs-peer.py + bt-peer.py) | ✅ |
| 9.9 | CI (GitHub Actions) | ✅ |
| 9.10 | Test grid (5W1H HTML) | ✅ |

## 10. Deployment

| # | Requirement | Status |
|---|-------------|--------|
| 10.1 | Single binary (51MB, Linux/amd64) | ✅ |
| 10.2 | GitHub Actions cross-compile (win/mac/linux) | ✅ |
| 10.3 | VPS relay node (systemd) | ✅ |
| 10.4 | CF Tunnel (wsl-3000.moonchan.xyz) | ✅ |
| 10.5 | CF Pages (peerdrive.pages.dev) | ✅ |
| 10.6 | Docker compose (5 nodes) | 🚧 |

## 11. Security

| # | Requirement | Status |
|---|-------------|--------|
| 11.1 | Upload size limit | ✅ |
| 11.2 | Auth middleware | ✅ |
| 11.3 | Path traversal protection | ✅ |
| 11.4 | CORS configuration | ✅ |
| 11.5 | Security review (SECURITY-REVIEW.md) | ✅ |

## 🔴 Remaining Priority

| Priority | Requirement |
|----------|-------------|
| P1 | WebRTC end-to-end transfer |
| P1 | Three-layer storage mutual backup sync |
| P1 | Docker 5-node full connectivity |
| P2 | Playwright Functional (39/44) |

## 12. Message Board & Statistics

| # | Requirement | Status |
|---|-------------|--------|
| 12.1 | Collection message board (comment system) | ✅ |
| 12.2 | Comments stored on registration server | ✅ |
| 12.3 | GET /comments/:hash (read comments) | ✅ |
| 12.4 | POST /comments/:hash (post comments, requires auth) | ✅ |
| 12.5 | Anonymous can read, authenticated can post | ✅ |
| 12.6 | Node statistics info (uptime/file count/transfer volume) | ✅ |
| 12.7 | P2P network statistics panel | ✅ |
| 12.8 | User group query | ✅ |
| 12.9 | Relay/P2P service based on user info | ✅ |
| 12.10 | Node operator account info query API | ✅ |

## 13. DHT Hash Table Query Service (2026-04-28)

| # | Requirement | Status |
|---|-------------|--------|
| 13.1 | Unified DHT query panel (webapp) — Input hash, query IPFS+BT simultaneously | ✅ |
| 13.2 | IPFS DHT query: /p2p/announce, /p2p/dual/find | ✅ |
| 13.3 | BT DHT query: /bt/announce, /bt/find, /bt/bep51/sample | ✅ |
| 13.4 | Dual-stack query: /p2p/dual/announce, /p2p/dual/find | ✅ |
| 13.5 | Frontend DHT Explorer page — Input hash, display both results | ✅ |
| 13.6 | BEP 51 infohash sampling — Discover content on BT network | ✅ |
| 13.7 | IPFS provider discovery — Find who has a certain CID | ✅ |
| 13.8 | Users can manually trigger DHT crawl/scan | ✅ |

| 2.17 | Private collections (visibility=private) | ✅ |
| 2.18 | Unlisted collections (visibility=unlisted) | ✅ |
| 2.19 | Public collections (visibility=public) | ✅ |
