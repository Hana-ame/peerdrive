# Peerdrive Backend Modification Specification (Completed)

## Overall Goals (Implemented)
1. **Anonymous Collection**: Implemented immutable anonymous collections based on content-addressed JSON.
2. **Registered user Collection changed to hash pointer**: `collections` table added `current_hash`; each Commit generates an anonymous snapshot hash.
3. **P2P download fallback**: `/sha256sum/:hash` automatically fetches from P2P when not found locally (integrates libp2p Bitswap logic).
4. **Multi-replica file storage**: The same hash allows multiple `file_providers` records, each with an independent `available` flag.
5. **Centralized authentication**: Implemented user registration, login, AuthKey session management, and route-level permission control.

## Modification Log
(The original table is preserved here as an implementation reference)

| # | File | Operation | Purpose | Status |
|---|------|-----------|---------|--------|
| 1 | `internal/model/anon.go` | New | Anonymous collection JSON struct + file type constants | ✅ |
| 2 | `internal/controller/anon.go` | New | Anonymous collection CRUD controller | ✅ |
| 3 | `internal/repository/anon_repo.go` | New | Anonymous collection JSON persistence | ✅ |
| 4 | `internal/model/collection.go` | Modified | Collection added CurrentHash field | ✅ |
| 5 | `internal/model/file.go` | Modified | FileMetadata added Available field | ✅ |
| 6 | `internal/repository/db.go` | Modified | Database schema update (users, file_meta, file_providers) | ✅ |
| 7 | `internal/repository/collection_repo.go` | Modified | Adapted to current_hash; added search functionality | ✅ |
| 8 | `internal/repository/file_repo.go` | Modified | GetFileByHash replica loop retry logic | ✅ |
| 9 | `internal/controller/collection.go` | Modified | Commit generates snapshot; GetCollection prefers snapshot | ✅ |
| 10 | `internal/controller/file.go` | Modified | Upload/Register no longer checks for hash duplicates | ✅ |
| 11 | `internal/router/router.go` | Modified | Added /auth, /anon routes and Auth middleware | ✅ |
| 12 | `internal/service/downloader.go` | Modified | GetFileStream loops over available locations + P2P fallback | ✅ |
| 13 | `internal/service/p2p.go` | Modified | Added FetchFile implementation | ✅ |
| 14 | `internal/serverapp/app.go` | Modified | Initialize AuthService and inject into router | ✅ |

## Constraint Verification
- [x] All changes are backward-compatible with existing APIs
- [x] Anonymous collection hash = SHA256 of canonical JSON
- [x] JSON entries are sorted by path before serialization
- [x] Database is migration-compatible via `ALTER TABLE` or `CREATE TABLE IF NOT EXISTS`

---

## Stage 2: P2P DHT + mDNS + Custom Exchange Protocol (Completed)

| # | File | Operation | Purpose | Status |
|---|------|-----------|---------|--------|
| 15 | `internal/config/config.go` | Modified | P2P environment variables (ENABLE/LISTEN/BOOTSTRAP/MDNS/RELAY) | ✅ |
| 16 | `internal/service/p2p.go` | Rewritten | libp2p + DHT + mDNS + Exchange/Announce protocol | ✅ |
| 17 | `internal/service/p2p_helpers.go` | New | cidFromSha256 / parsePeerAddr utility functions | ✅ |
| 18 | `internal/controller/p2p.go` | Rewritten | status/node/peers/discovered/ping/connect/announce/fetch/sync/push | ✅ |
| 19 | `internal/model/anon.go` | Modified | AnonCollection added FriendlyName | ✅ |
| 20 | `internal/service/anon_service.go` | Modified | CreateCollection(name, entries) supports naming | ✅ |
| 21 | `internal/router/router.go` | Modified | Registered all P2P and new anon routes | ✅ |
| 22 | `internal/serverapp/app.go` | Modified | Pass cfg to NewP2PService | ✅ |
| 23 | `test/p2p.sh` | New | 13-step dual-node integration test | ✅ |
| 24 | `.github/workflows/ci.yml` | New | CI: build + 4 test suites | ✅ |
| 25 | `docs/specs/api-reference.md` | Modified | P2P Stage 2 endpoint documentation | ✅ |
| 26 | `docs/testing/p2p-stage2-report.md` | New | Stage 2 test report | ✅ |

## Stage 3: NAT Traversal + Relay + WS Transfer (Completed)

| # | File | Operation | Purpose | Status |
|---|------|-----------|---------|--------|
| 27 | `internal/config/config.go` | Modified | RelayMode / StaticRelays / HolePunch / AutoNAT / NATPortMap / PublicReachable | ✅ |
| 28 | `internal/service/p2p.go` | Modified | EnableRelay / EnableHolePunching / AutoNAT / BroadcastRequest / ProtocolRequest | ✅ |
| 29 | `internal/service/p2p_ws.go` | New | WebSocket hub (wsHub) + WSHandler for /ws/transfer | ✅ |
| 30 | `internal/service/p2p_helpers.go` | Modified | parseStaticRelays() | ✅ |
| 31 | `internal/controller/p2p.go` | Modified | RequestFile / WSInfo endpoints; P2PStatus extended | ✅ |
| 32 | `internal/router/router.go` | Modified | /p2p/request-file / /p2p/ws/info / /ws/transfer routes | ✅ |
| 33 | `test/relay.sh` | New | 12-step relay + WS integration test | ✅ |
| 34 | `docs/specs/api-reference.md` | Modified | Stage 3 endpoint documentation (request-file / ws/info / ws/transfer) | ✅ |
| 35 | `docs/testing/p2p-stage2-report.md` | Modified | Stage 3 test results section | ✅ |
| 36 | `.github/workflows/ci.yml` | Modified | Added relay.sh test step | ✅ |
