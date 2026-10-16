# Peerdrive Test Pipeline Document

> Flow, expected behavior, error analysis, and whether code changes are needed for each test script
> Updated: 2026-04-29

---

## Test Script Overview

| Script | Type | Assertions | Port | Self-contained |
|------|------|--------|------|--------|
| `go test ./...` | Unit test | 66 | — | ✅ |
| `test/e2e-all.sh` | E2E integration | 85 | 3999 | ✅ |
| `test/bt-full-test.sh` | BT-specific | 38 | 3000 | ❌ |
| `test/p2p-full-test.sh` | P2P-specific | 35 | 3000 | ❌ |
| `test/p2p.sh` | P2P dual-node | 13 | 3001/3002 | ✅ |
| `test/relay.sh` | Relay traversal | 12 | 3001/3002 | ✅ |
| `test/webrtc_signal_test.sh` | WebRTC signaling | 23 | 3000 | ❌ |
| `test/ipfs-full-test.sh` | IPFS-specific | — | 3000 | ❌ |
| `test/storage-full-test.sh` | Storage-specific | 28 | 3000 | ❌ |
| `test/auth-full-test.sh` | Auth-specific | 20 | 4000 | ❌ |
| `test/upload.sh` | Upload test | 3 | 3000 | ❌ |
| `test/register.sh` | Registration test | 3 | 3000 | ❌ |
| `test/anon-collection.sh` | Anonymous collection | 6 | 3000 | ❌ |
| `test/all.sh` | All-in-one modules | — | 3000 | ❌ |

---

## 1. go test ./... — Go Unit Tests

### Run
```bash
cd back && go test ./... -count=1
```

### Test Segments
| Package | Tests | Coverage |
|----|--------|------|
| config | 10 | Load(), getEnv, getEnvBool, parseRelayMode |
| model | 6 | Collection/AnonCollection structs |
| repository | 5 | FileMeta CRUD, Provider management |
| service | 29 | File register/upload/verify, anonymous collections, path traversal |
| controller | 17 | Ping, Collection CRUD, Commit/Rollback |

### Expected Output
```
ok  peerdrive/internal/config     0.021s
ok  peerdrive/internal/controller  0.087s
ok  peerdrive/internal/model       0.007s
ok  peerdrive/internal/repository  0.020s
ok  peerdrive/internal/service     0.314s
```

### Common Errors

| Error | Possible cause | Code change needed |
|------|----------|-------------|
| `cannot find package` | go.mod dependencies not downloaded | No, run `go mod tidy` |
| `undefined: xxx` | Code references a deleted function | Yes, check imports and function names |
| `FAIL: TestXxx` | Test logic doesn't match current implementation | Yes, check if test case matches the latest API |
| `database is locked` | SQLite concurrent access conflict | No, run this test separately |
| `no test files` | Test file is in a non-test directory | No, confirm `_test.go` file exists |

### Troubleshooting Steps
1. Check if `go.mod` is complete: `go mod tidy`
2. View specific failures: `go test -v ./internal/... 2>&1 | grep FAIL`
3. Run failing package separately: `go test -v -run TestXxx ./internal/xxx/`
4. Check if code was modified by linter (see git diff)

---

## 2. test/e2e-all.sh — E2E Full Endpoint Test

### Run
```bash
cd back && bash test/e2e-all.sh
```

### Test Flow

```
Phase 1: Compile peerdrive-server → Start on :3999 → Wait for ready
Phase 2-12: Execute 85 curl assertions in sequence
Phase 13: Kill process, clean up temp files
```

### 12 Test Segment Details

#### Segment 1 — Health (2 assertions)
- `GET /ping` → 200 "pong"
- **If fails**: Process not started or port conflict
- **Troubleshoot**: Use `fuser 3999/tcp` to check for port occupation, view compilation errors

#### Segment 2 — File Upload (10 assertions)
- Upload new file → 201 + hash + on-disk verification
- Duplicate upload → 200 + already_exists + same hash
- Second file → different hash
- **If fails**: 
  - 201 failure → storage directory permission issue
  - hash mismatch → `hashutil.SHA256` or `io.Copy` issue (code change needed)
  - on-disk verification failure → `c.SaveUploadedFile` path issue (code change needed)

#### Segment 3 — File Verify (3 assertions)
- Valid hash → 200 + metadata
- Invalid hash → 400
- **If fails**: `/files/verify/:hash` route or `GetFileMeta` query issue (code change needed)

#### Segment 4 — SHA256 Download (3 assertions)
- Valid hash → 200 + content matches
- Invalid hash → 404
- **If fails**: provider cannot find file path or file was deleted

#### Segment 5 — Register Local (5 assertions)
- Register local file → hash + verify + download
- Duplicate registration → same hash
- **If fails**: path permission issue or `RegisterLocal` logic (code change needed)

#### Segment 6 — Register Folder (2 assertions)
- Register folder → return file list
- **If fails**: directory traversal logic or permission issue

#### Segment 7 — Anonymous Collections (22 assertions)
- Create collection → path traversal rejected → friendly_name
- GET collection JSON → sha256sum download → entries download
- Fork → commit
- **If fails**: 
  - Path traversal not rejected → security vulnerability (code change needed)
  - commit has no version → collection_versions table issue (code change needed)

#### Segment 8 — Named Collections (22 assertions)
- Create/duplicate reject/list/get/add entry/delete entry/download
- Commit → version → rollback
- **If fails**: DB table or repository logic issue (code change needed)

#### Segment 9 — Fork/Merge/Pull (4 assertions)
- Fork creates new collection → Merge combines → Pull response
- **If fails**: `actions/` route or fork/merge logic (code change needed)

#### Segment 10 — File Delete (2 assertions)
- Delete succeeds → verify returns 404
- **If fails**: delete logic or DB transaction issue (code change needed)

#### Segment 11 — Tasks (2 assertions)
- Task list valid → non-existent task 404
- **If fails**: transfer_tasks table or task handler issue (code change needed)

#### Segment 12 — Edge Cases (5 assertions)
- Non-existent user, empty collection, invalid hash, malformed body
- **If fails**: boundary condition handling issue (code change needed)

### Global Troubleshooting
- Script requires `python3` for JSON parsing
- Port 3999 must be free
- System proxy may interfere with curl; script automatically sets `no_proxy='*'`

---

## 3. test/bt-full-test.sh — BitTorrent Full Feature Test

### Run
```bash
# Start service first
PEERDRIVE_BT_DHT_ENABLE=true go run ./cmd/server/main.go &
# Then test
bash test/bt-full-test.sh
```

### Test Segments

| # | Test | Expected |
|---|------|------|
| 1 | BT DHT status | enabled:true, num_nodes > 0 |
| 2 | BT Announce | status:"announced on BT DHT" |
| 3 | BT Find (self) | Find own announcement |
| 4 | BT Find (cross) | A announces → B finds → count >= 1 |
| 5 | BEP 44 Put | Returns target hash |
| 6 | BEP 44 Get | Data round-trip consistent (base64) |
| 7 | BEP 51 Sample | Returns sample array |
| 8 | Torrent Parse | name/pieces/size/infohash |
| 9 | Magnet Parse | infohash/name/trackers |
| 10 | Wire Handshake | Protocol handshake succeeds |
| 11 | Piece Download | SHA1 verification passes |
| 12 | Full Download | SHA256 final match |

### Common Errors

| Error | Possible cause | Code change needed |
|------|----------|-------------|
| `num_nodes: 0` | UDP 6881 blocked by firewall / DHT bootstrap takes 1-2 minutes | No, wait 2 minutes and retry |
| `BEP44 put: 500` | DHT remote node doesn't support BEP44 | No, fixed with local fallback |
| `BEP44 get: not found` | Data not stored in DHT (isolated node) | No, check BEP44 localBEP44Store |
| `Wire handshake timeout` | Seeder not started or wrong port | No, check Python seeder process |
| `SHA1 mismatch` | Piece download corrupted | Yes, check piece.go verification logic |

### Troubleshooting Steps
1. Confirm service is running: `curl localhost:3000/bt/status`
2. Confirm DHT has nodes: wait 60 seconds and recheck `num_nodes`
3. BEP44 issue: check `localBEP44Store` in `internal/p2p_bt/bep44.go`
4. Wire issue: start Python seeder: `python3 test/bt-integration/bt-listener.py`

---

## 4. test/p2p.sh — P2P Dual-Node Test

### Run
```bash
cd back && bash test/p2p.sh
```

### Test Flow
1. Compile two nodes
2. Start Node A (:3001) and Node B (:3002)
3. Wait for mDNS discovery (10s)
4. Node A registers file + creates collection
5. Node B connects to A + P2P fetch + sync
6. Clean up processes

### Common Errors

| Error | Possible cause | Code change needed |
|------|----------|-------------|
| Compilation failure | libp2p dependency missing | No, `go mod tidy` |
| mDNS not discovered (WARN) | Firewall/network isolation | No, soft assertion doesn't FAIL |
| connect failure | Wrong port or peer_id mismatch | No, check multiaddr |
| fetch failure | Collection not announced or no DHT record | No, check announce status |
| sync returns 0 | File not on target node | No, confirm file is registered on A |
| Port conflict | 3001/3002 occupied | No, `fuser -k 3001/tcp` |

---

## 5. test/relay.sh — Relay Traversal Test

### Run
```bash
cd back && bash test/relay.sh
```

### Test Flow
1. Relay node (:3001, relay_mode=server)
2. Client node (:3002, hole_punch=true)
3. Client connects to Relay → P2P fetch → WS info
4. Cleanup

### Environment Variables
```bash
PEERDRIVE_RELAY_ENABLE=true
PEERDRIVE_RELAY_MODE=server  # or client
PEERDRIVE_HOLE_PUNCH=true
PEERDRIVE_AUTO_NAT=true
```

### Common Errors

| Error | Possible cause | Code change needed |
|------|----------|-------------|
| relay_mode is not server | Environment variable not set correctly | No |
| Client cannot connect to Relay | Network unreachable or wrong peer_id | No, check address |
| Hole punch failure | NAT type is symmetric | No, symmetric NAT cannot hole-punch |
| WS info is empty | WebSocket service not initialized | Yes, check p2p_ws.go |

---

## 6. test/webrtc_signal_test.sh — WebRTC Signaling Test

### Run
```bash
# Service running on :3000
bash test/webrtc_signal_test.sh
```

### Test Segments (23 items)
- Register (echo message round-trip)
- Room join/leave
- SDP Offer/Answer exchange
- ICE candidate forwarding
- File announce/discover
- Direct messages (unicast)
- 3-person room
- Room isolation

### Common Errors

| Error | Possible cause | Code change needed |
|------|----------|-------------|
| Registration failure | SignalingHub not initialized | Yes, check InitSignalHub in router.go |
| Room messages not received | broadcast logic bug | Yes, check exclude parameter in signaling.go |
| SDP exchange failure | WebSocket message format error | No, check JSON format |
| panic: slice bounds | hash/room name too short | Yes, fixed — check length >= 16 |

---

## 7. test/storage-full-test.sh — Storage Full Feature

### Run
```bash
# Service running on :3000
bash test/storage-full-test.sh
```

### Test Coverage
- SHA256 download / CID dual index
- File upload / register / delete / verify
- Range (HTTP 206) download
- URL file registration / WebDAV / file copy

### Known Issues (4 failures)
- Collection get returns inconsistent format
- Share token not accessible after creation
- CID queries fail in some scenarios
- These are known bugs; don't change test scripts, code changes needed

---

## 8. Test Failure Categorization Guide

### No code change needed (environment/config issues)
- Port occupied → `fuser -k PORT/tcp`
- Proxy interference → `export no_proxy='*'`
- Compilation error → `go mod tidy`
- Permission issue → `chmod` / `sudo`
- Database lock → rerun separately

### Test script changes needed
- API path changes
- Return format changes
- New required parameters
- Insufficient timeout

### Code changes needed
- HTTP 500 errors
- Incorrect return data
- Security vulnerabilities (path traversal, injection)
- Panic / nil pointer
- Assertion fails but API response looks correct

---

## 9. Guidelines for Adding New Tests

1. Place shell scripts in `go/test/<name>.sh`
2. Add comments at the top of the script describing the test purpose
3. Use `curl -x ""` to bypass the system proxy
4. Use `jq` or `python3 -c` to parse JSON
5. Use `|| echo "FAIL: ..."` to mark failures
6. Clean up temp files and processes
7. Avoid depending on specific file paths (use `/tmp/`)
8. Save test results to `go/test/<name>-results.txt`

### Template
```bash
#!/bin/bash
# Test: <description>
# Requires: server on PORT (default 3000)
set -e
PORT=${1:-3000}
BASE="http://localhost:$PORT"
no_proxy="*"
PASS=0; FAIL=0
check() {
  if [ "$1" = "$2" ]; then ((PASS++)); echo "PASS: $3"
  else ((FAIL++)); echo "FAIL: $3 (expected '$2', got '$1')"; fi
}
# ... tests ...
echo "PASS=$PASS FAIL=$FAIL TOTAL=$((PASS+FAIL))"
```
