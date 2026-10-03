# Peerdrive Test Plan

> 2026-04-29 · 389+ test cases all passed

---

## 1. Test System Overview

```
                    ┌─────────────────────────────────────┐
                    │    go test ./...                      │
                    │    163 unit tests                     │
                    │    7 packages · instant feedback      │
                    └──────────────────┬──────────────────┘
                                       │
        ┌───────────────────────────────┼───────────────────────────────┐
        ▼                               ▼                               ▼
 ┌──────────────────┐  ┌────────────────────┐  ┌──────────────────┐
 │  E2E full        │  │  Module-specific   │  │  Browser tests   │
 │  endpoint        │  │  BT/P2P/           │  │  Playwright      │
 │  85 assertions   │  │  WebRTC etc.       │  │  36 cases        │
 │  Self-contained  │  │                    │  │                  │
 └──────────────────┘  └────────────────────┘  └──────────────────┘
```

| Level | Tool | Cases | Runtime | Trigger |
|------|------|--------|---------|------|
| Unit tests | `go test` | 163 | ~3s | Every push |
| E2E integration | `test/e2e-all.sh` | 85 | ~15s | Every push |
| Module-specific | `test/{bt,p2p,webrtc}-full-test.sh` | 96 | ~30s | CI + manual |
| Browser | Playwright (`.mjs`) | 36 | ~20s | Manual |
| Dual-node | `test/p2p.sh` `relay.sh` | 25 | ~20s | Manual |

---

## 2. Test Categories

### 2.1 Unit Tests — `go test ./...`

| Package | What's tested | Cases |
|----|--------|------|
| `config` | Environment variable loading, defaults, bool parsing | 10 |
| `model` | Struct serialization, JSON compatibility, Provider normalization | 10 |
| `repository` | SQLite CRUD, providers_json, version snapshots | 25 |
| `service` | Collection create/verify, file registration/MIME detection, downloader | 48 |
| `controller` | HTTP handlers, CRUD, Fork/Merge/Rollback | 20 |
| `p2p_bt` | DHT status, BEP44, Torrent/Magnet, Wire | 38 |
| `provider` | HTTP/Local/IPFS providers | 12 |

### 2.2 E2E Integration Test — `test/e2e-all.sh`

Self-contained: compile → start on :3999 → 12 test segments → cleanup.

| Segment | Test content | Assertions |
|----|---------|------|
| Health | `GET /ping` | 2 |
| Upload | New file 201, duplicate 200, hash verification | 10 |
| Verify | File verification, invalid rejection | 3 |
| Download | SHA256 download, content match | 3 |
| Register Local | Local registration, idempotency, download verification | 5 |
| Register Folder | Folder registration | 2 |
| Anon Collections | Create/Fork/Commit/entries download | 22 |
| Named Collections | CRUD/entries/Commit/Rollback | 22 |
| Fork/Merge/Pull | Entry copying, strategy merging | 4 |
| File Delete | Verify 404 after delete | 2 |
| Tasks | Task list | 2 |
| Edge Cases | Boundary conditions | 5 |

### 2.3 Module-specific Tests

#### BitTorrent — `test/bt-full-test.sh` (38 assertions)

```
DHT status → Announce → Find(self) → Find(cross)
→ BEP44 Put/Get → BEP51 Sample
→ Torrent/Magnet Parse → Wire Handshake
→ Piece Download(SHA1) → Full Download(SHA256)
```

#### P2P — `test/p2p-full-test.sh` (35 assertions)

```
Node start → PeerID/multiaddr → DHT + mDNS discovery
→ Exchange handshake → File announce/discover
→ Connection management (heartbeat/reconnect) → Topology + quality metrics
```

#### WebRTC — `test/webrtc_signal_test.sh` (23 assertions)

```
WebSocket register → Room join/leave
→ SDP Offer/Answer → ICE candidate forwarding
→ File announce/discover → Direct messages → 3-person room + isolation
```

### 2.4 Dual-node/Traversal Tests

| Script | Content | Assertions | Self-contained |
|------|------|------|--------|
| `p2p.sh` | Dual-node mDNS discovery → P2P fetch → sync | 13 | ✅ |
| `relay.sh` | Relay server + client traversal | 12 | ✅ |

### 2.5 Browser Tests — Playwright

| Script | Content | Cases |
|------|------|------|
| `peerdrive-smoke.mjs` | Page load/navigation/basic interaction | 16 |
| `peerdrive-functional.mjs` | Collection create/file upload/download flow | 20+ |

---

## 3. How to Run

### Local Quick Verification (3 commands)

```bash
cd back && go test ./... -count=1          # 163 unit tests
cd back && bash test/e2e-all.sh            # 85 E2E assertions
cd front && npm run build                # Frontend build
```

### CI Auto-run (every push)

| Workflow | Trigger | Content |
|----------|------|------|
| **Peerdrive CI** | push main | go test + build + anon/upload/register + P2P/relay soft skip |
| **Go Build Matrix** | push main | 5-platform compilation + go test (skip Windows) |
| **React CI** | push frontend | npm test (vitest 20) + npm run build |

### Full Test Suite

```bash
cd back && bash test/all.sh   # All-in-one: compile + unit + frontend + Playwright
```

---

## 4. Test Environment

| Dependency | Purpose |
|------|------|
| Go 1.21+ | Compilation + unit tests |
| Node.js 20+ | Frontend + Playwright |
| Python 3 | curl test JSON parsing |
| bash | Integration test scripts |

### Proxy Notes

System HTTP_PROXY will intercept localhost. Solution:

```bash
env -u HTTP_PROXY -u http_proxy bash test/e2e-all.sh
# Or within script: export no_proxy='*'
```

---

## 5. Documentation Navigation

| Document | Content |
|------|------|
| [如何测试.md](如何测试.md) | Chinese operation guide — run commands and troubleshooting for each test |
| [TESTING-HANDBOOK.md](TESTING-HANDBOOK.md) | Complete test handbook — environment setup, architecture, manual flow (840 lines) |
| [TEST-PIPELINE.md](TEST-PIPELINE.md) | Test pipeline — flow, expectations, error analysis for each script |
| [TEST-MATRIX.md](TEST-MATRIX.md) | Test matrix — ID/steps/expectations for 109 test cases |
| [TESTING-METHODOLOGY.md](TESTING-METHODOLOGY.md) | Methodology details — 9-phase testing methodology |
| [CHAOS_TESTING.md](CHAOS_TESTING.md) | Chaos testing — harsh network simulation |
| [测试报告-2026-04-29.md](../../archive/report/测试报告-2026-04-29.md) | Latest test report (389+ PASS) |
| [CI-FIXES.md](../../archive/report/CI-FIXES.md) | CI fix records (8 issues) |

### Archived (obsolete/early documents)

| Document | Notes |
|------|------|
| [测试说明.md](archive/测试说明.md) | Early test description → merged into 如何测试.md |
| [register.md](archive/register.md) | Early registration test notes |
| [test-case-spec.md](archive/test-case-spec.md) | Old test cases → upgraded to TEST-MATRIX.md |
| [test-peers.md](archive/test-peers.md) | P2P node test notes |
| [p2p-stage2-report.md](archive/p2p-stage2-report.md) | P2P Stage 2 test report |
| [test-steps.md](archive/test-steps.md) | P2P actual test steps log |
