# Peerdrive Testing Handbook

> A comprehensive testing guide for QA engineers and developers
> Updated: 2026-04-29

---

## Table of Contents

1. [Environment Setup](#1-environment-setup)
2. [Testing Architecture Overview](#2-testing-architecture-overview)
3. [Quick Start](#3-quick-start)
4. [Test Suite Details](#4-test-suite-details)
5. [Manual Testing Workflow](#5-manual-testing-workflow)
6. [Troubleshooting](#6-troubleshooting)
7. [Test Matrix](#7-test-matrix)
8. [Chaos Testing](#8-chaos-testing)

---

## 1. Environment Setup

### 1.1 Dependency Installation

```bash
# Go 1.24+
go version

# Python 3 (for test script JSON parsing)
python3 --version

# curl
curl --version

# jq (optional, for command-line JSON processing)
sudo apt-get install jq
```

### 1.2 System Proxy Configuration

If the system has an HTTP proxy configured (e.g., Privoxy), test scripts need to bypass it:

```bash
# Method 1: Use the no_proxy environment variable
export no_proxy='*'

# Method 2: Use curl with -x "" parameter
curl -x "" http://localhost:3000/ping

# Method 3: Test scripts handle this automatically (most scripts have this built in)
```

### 1.3 Port Plan

| Component | Default Port | Description |
|-----------|-------------|-------------|
| Peerdrive Main Service | 3000 | Go Gin HTTP API |
| Peerdrive Node B | 3001 | For two-node testing |
| Peerdrive Node C | 3002 | For three-node testing |
| Registration Server | 4000 | JWT authentication service |
| React Dev Server | 5173 | Vite frontend development |
| BT Tracker (Test) | 6969 | Python tracker simulation |
| BT Seeder (Test) | 6890 | Python seeder |
| IPFS Simulated Node | 9001 | IPFS test node |
| E2E Tests | 3999 | Self-contained test port |

### 1.4 Environment Variables Quick Reference

```bash
# Core configuration
PEERDRIVE_STORAGE=./storage        # File storage directory
PEERDRIVE_STORAGE_ENABLE=true      # Storage switch
PORT=3000                          # HTTP port

# P2P configuration
PEERDRIVE_P2P_ENABLE=true          # P2P switch
PEERDRIVE_MDNS_ENABLE=true         # mDNS LAN discovery
PEERDRIVE_RELAY_ENABLE=true        # Relay switch
PEERDRIVE_RELAY_MODE=client        # client / server / off
PEERDRIVE_HOLE_PUNCH=true          # NAT hole punching
PEERDRIVE_AUTO_NAT=true            # AutoNAT detection
PEERDRIVE_PUBLIC_REACHABLE=true    # Publicly reachable (relay server)
PEERDRIVE_BOOTSTRAP_PEER=<multiaddr>  # Bootstrap node

# BT configuration
PEERDRIVE_BT_DHT_ENABLE=true       # BT DHT switch

# Security (important!)
PEERDRIVE_JWT_SECRET=<random>      # JWT secret (must be set in production)
```

---

## 2. Testing Architecture Overview

```
Testing Pyramid
┌─────────────────────────┐
│     E2E Tests            │  ← e2e-all.sh (85 assertions, 12 segments)
│     Real Network Tests   │  ← BT DHT / IPFS public
├─────────────────────────┤
│    Integration Tests     │  ← p2p.sh, relay.sh, bt-full-test.sh
│    Multi-node/inter-mod.│
├─────────────────────────┤
│    API Tests             │  ← upload.sh, register.sh, anon-collection.sh
│    Single-node HTTP      │
├─────────────────────────┤
│    Unit Tests            │  ← go test ./... (66 tests)
│    Go package level      │
└─────────────────────────┘
```

### Test File Locations

```
go/
├── *_test.go                    # Go unit tests (embedded in source)
├── test/
│   ├── e2e-all.sh               # E2E all-endpoint tests (self-contained)
│   ├── test.sh                  # Full integration tests (requires running server)
│   ├── upload.sh                # Upload functionality tests
│   ├── register.sh              # File registration tests
│   ├── anon-collection.sh       # Anonymous collection tests
│   ├── p2p.sh                   # P2P two-node tests (self-contained)
│   ├── relay.sh                 # Relay penetration tests (self-contained)
│   ├── bt-full-test.sh          # BT full functionality tests
│   ├── ipfs-full-test.sh        # IPFS full functionality tests
│   ├── p2p-full-test.sh         # P2P full functionality tests
│   ├── storage-full-test.sh     # Storage full functionality tests
│   ├── auth-full-test.sh        # Auth full functionality tests
│   ├── webrtc_signal_test.sh    # WebRTC signaling tests
│   └── *-full-test-results.txt  # Test result files
```

---

## 3. Quick Start

### 3.1 Run All Tests with One Command

```bash
cd /mnt/d/WorkPlace/peerdrive/go

# 1. Unit tests — fastest, no server needed
go test ./... -count=1

# 2. E2E tests — complete HTTP endpoint coverage
bash test/e2e-all.sh

# 3. Module-specific tests (requires service running on :3000)
bash test/all.sh
```

### 3.2 Minimum Verification (30 seconds)

```bash
# Start the service
cd /mnt/d/WorkPlace/peerdrive/go
PORT=3999 PEERDRIVE_STORAGE=/tmp/pd-test go run ./cmd/server/main.go &

# Wait for startup
sleep 3

# Verify
curl -x "" http://localhost:3999/ping
# → pong

# Stop
kill %1
```

---

## 4. Test Suite Details

### 4.1 Go Unit Tests (`go test ./...`)

**No server needed**, run directly.

```bash
cd go
go test ./... -count=1
```

| Package | Tests | Coverage |
|---------|-------|----------|
| `internal/config` | 10 | Load() defaults, env parsing, bool handling, relay mode |
| `internal/model` | 6 | Collection/AnonCollection structs |
| `internal/repository` | 5 | FileMeta CRUD, Provider management |
| `internal/service` | 29 | File register/upload/verify, anonymous collections, path filtering |
| `internal/controller` | 17 | Ping, Collection CRUD, Commit/Rollback |

Expected output:
```
ok  peerdrive/internal/config     0.021s
ok  peerdrive/internal/controller  0.087s
ok  peerdrive/internal/model       0.007s
ok  peerdrive/internal/repository  0.020s
ok  peerdrive/internal/service     0.314s
```

### 4.2 E2E All-Endpoint Tests (`test/e2e-all.sh`)

**The most important integrated test**, self-contained (compiles, starts, tests, and cleans up itself).

```bash
cd go
bash test/e2e-all.sh
```

**12 test segments**:

| # | Segment | Assertions | Test Content |
|---|---------|-----------|--------------|
| 1 | Health | 2 | Service starts, returns pong |
| 2 | File Upload | 10 | New file/duplicate/exists/storage path |
| 3 | File Verify | 3 | Valid hash / invalid hash |
| 4 | SHA256 Download | 3 | Download/compare/404 |
| 5 | Register Local | 5 | Single file/duplicate/folder registration |
| 6 | Register Folder | 2 | Recursive registration/file count |
| 7 | Anonymous Collections | 22 | CRUD/path traversal/commit/fork/download |
| 8 | Named Collections | 22 | Create/entries/versions/rollback |
| 9 | Fork/Merge/Pull | 4 | Fork/Merge/Pull operations |
| 10 | File Delete | 2 | Delete/follow-up 404 |
| 11 | Tasks | 2 | Task list/nonexistent task |
| 12 | Edge Cases | 5 | Nonexistent user/invalid hash/empty body |

Expected output: `PASS: 84  FAIL: 0  WARN: 1  TOTAL: 85`

> WARN: Empty body `{}` creating a collection returns 200 (edge behavior), does not affect functionality.

### 4.3 Module-Specific Tests

These tests assume the server is already running on port 3000.

#### Upload Tests
```bash
# Start the service first
PORT=3000 PEERDRIVE_STORAGE=./storage go run ./cmd/server/main.go &

# Run tests
bash test/upload.sh
```
Tests: New file upload (201), duplicate upload (200 already_exists), second file (different hash)

#### Register Tests
```bash
bash test/register.sh
```
Tests: Single file register → verify → download, recursive folder registration, duplicate registration idempotency

#### Anonymous Collection Tests
```bash
bash test/anon-collection.sh
```
Tests: Create collection, path traversal rejection, fetch JSON, download file, Fork

### 4.4 P2P Two-Node Tests (`test/p2p.sh`)

**Self-contained** (compiles two nodes, starts them on 3001/3002 respectively).

```bash
cd go
bash test/p2p.sh
```

Test flow:
1. Node A (3001) ping
2. Node B (3002) ping
3. P2P status verification
4. mDNS discovery (soft assertion, 10 seconds)
5. A registers file + creates collection
6. A announces hash
7. B manually connects to A
8. Peer list verification
9. B P2P fetch to get collection
10. P2P sync
11. P2P push

Environment variables: `PEERDRIVE_P2P_ENABLE=true PEERDRIVE_MDNS_ENABLE=true PEERDRIVE_RELAY_ENABLE=false`

### 4.5 Relay Penetration Tests (`test/relay.sh`)

**Self-contained**, tests node communication in relay mode.

```bash
cd go
bash test/relay.sh
```

Test flow:
1. Relay node (3001, relay_mode=server) ping
2. Client node (3002, hole_punch=true) ping
3. mDNS discovery (soft assertion)
4. Client manually connects to Relay
5. Peer list verification
6. Client registers file + collection + announces
7. Relay P2P fetch to get collection
8. WS info endpoint verification
9. Request-file broadcast

### 4.6 BT Full Functionality Tests (`test/bt-full-test.sh`)

```bash
cd go
bash test/bt-full-test.sh
# Expected: 38/38 PASS, 0 FAIL
```

Coverage:
- BT DHT start/status/node count
- BT Announce / Find
- BEP 44 Put/Get (immutable data storage)
- BEP 51 Sample (DHT sampling)
- Torrent parsing / Magnet parsing
- Wire Protocol handshake / piece download
- BT download management (pause/resume/seed/unseed/delete)

### 4.7 WebRTC Signaling Tests (`test/webrtc_signal_test.sh`)

```bash
cd go
bash test/webrtc_signal_test.sh
# Expected: 23/23 PASS
```

Coverage: Registration, room join/leave, SDP exchange, ICE candidates, file announce/discovery, direct messages, 3-person rooms, room isolation.

### 4.8 IPFS Full Functionality Tests (`test/ipfs-full-test.sh`)

```bash
cd go
bash test/ipfs-full-test.sh
```

Coverage: IPFS CID indexing, Pin/Unpin, gateway health check, IPFS compatibility mode toggle.

### 4.9 Storage Full Functionality Tests (`test/storage-full-test.sh`)

```bash
cd go
bash test/storage-full-test.sh
```

Coverage: SHA256 download, CID dual indexing, file upload/register/delete/verify, Range download, URL registration, WebDAV, file copy.

### 4.10 Auth Full Functionality Tests (`test/auth-full-test.sh`)

```bash
cd go
bash test/auth-full-test.sh
# Expected: 20/20 PASS
```

Coverage: User registration/login, JWT verification, Relay register/heartbeat/list, Group management, Comment system.

### 4.11 Frontend Tests

```bash
cd react

# Playwright E2E tests
npx playwright test

# Smoke test (16 tests) — page load, element rendering
# Functional test (18 tests) — user interaction flows
```

---

## 5. Manual Testing Workflow

### 5.1 Basic Health Checks

```bash
# 1. Confirm service is online
curl -x "" http://localhost:3000/ping
# → pong

# 2. Confirm P2P is running
curl -x "" http://localhost:3000/p2p/status | jq
# → {"enabled":true, "peer_id":"12D3KooW...", "relay_mode":"..."}

# 3. Confirm BT DHT is running
curl -x "" http://localhost:3000/bt/status | jq
# → {"enabled":true, "listen_addr":"0.0.0.0:6881", "num_nodes":127}
```

### 5.2 File Upload → Download Verification

```bash
# 1. Create test file
echo "Hello Peerdrive Test $(date)" > /tmp/pd-test.txt
ORIGINAL_SHA256=$(sha256sum /tmp/pd-test.txt | cut -d' ' -f1)

# 2. Upload
RESULT=$(curl -s -x "" -F "file=@/tmp/pd-test.txt" http://localhost:3000/files/upload)
HASH=$(echo "$RESULT" | jq -r .hash)
echo "Hash: $HASH"

# 3. Verify hash match
test "$HASH" = "$ORIGINAL_SHA256" && echo "✓ Hash matches"

# 4. Download
curl -s -x "" -o /tmp/pd-downloaded http://localhost:3000/sha256sum/$HASH

# 5. Compare content
diff /tmp/pd-test.txt /tmp/pd-downloaded && echo "✓ Content matches"

# 6. Delete
curl -s -x "" -X DELETE http://localhost:3000/files/$HASH
# → 200

# 7. Confirm deleted
curl -s -x "" -o /dev/null -w "%{http_code}" http://localhost:3000/sha256sum/$HASH
# → 404
```

### 5.3 Anonymous Collection Full Flow

```bash
# 1. Prepare two files
echo "file1" > /tmp/f1.txt
echo "file2" > /tmp/f2.txt
H1=$(curl -s -x "" -F "file=@/tmp/f1.txt" http://localhost:3000/files/upload | jq -r .hash)
H2=$(curl -s -x "" -F "file=@/tmp/f2.txt" http://localhost:3000/files/upload | jq -r .hash)

# 2. Create collection
COLL=$(curl -s -x "" -H "Content-Type: application/json" \
  -d "{\"entries\":[{\"path\":\"a.txt\",\"hash\":\"$H1\"},{\"path\":\"b.txt\",\"hash\":\"$H2\"}],\"friendly_name\":\"Test Coll\"}" \
  -X POST http://localhost:3000/anon/collections)
COLL_HASH=$(echo "$COLL" | jq -r .hash)

# 3. Fetch collection JSON
curl -s -x "" http://localhost:3000/anon/collections/$COLL_HASH | jq

# 4. Download files from collection
curl -s -x "" -o /tmp/result http://localhost:3000/anon/collections/$COLL_HASH/entries/a.txt
diff /tmp/f1.txt /tmp/result && echo "✓ Entry a.txt matches"

# 5. Fork collection
FORK=$(curl -s -x "" -H "Content-Type: application/json" \
  -d "{\"source_hash\":\"$COLL_HASH\",\"friendly_name\":\"Forked\",\"remove_paths\":[\"b.txt\"]}" \
  -X POST http://localhost:3000/anon/collections/fork)
FORK_HASH=$(echo "$FORK" | jq -r .hash)

# 6. Verify fork (only a.txt remains)
curl -s -x "" http://localhost:3000/anon/collections/$FORK_HASH | jq '.entries | length'
# → 1
```

### 5.4 Path Traversal Protection Verification

```bash
# Should be rejected (400)
curl -s -x "" -H "Content-Type: application/json" \
  -d '{"entries":[{"path":"../etc/passwd","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}' \
  -X POST http://localhost:3000/anon/collections | jq
# → {"error":"path traversal detected: ../etc/passwd"}

# Should be rejected (400)
curl -s -x "" -H "Content-Type: application/json" \
  -d '{"path":"/etc/passwd"}' \
  -X POST http://localhost:3000/files/register_local | jq
```

### 5.5 P2P Two-Node Manual Test

```bash
# Terminal 1 — Start Node A (relay)
cd go
PORT=3001 PEERDRIVE_P2P_ENABLE=true PEERDRIVE_MDNS_ENABLE=true \
  PEERDRIVE_RELAY_ENABLE=true PEERDRIVE_RELAY_MODE=server \
  PEERDRIVE_STORAGE=/tmp/pd-a go run ./cmd/server/main.go

# Terminal 2 — Start Node B (client)
PORT=3002 PEERDRIVE_P2P_ENABLE=true PEERDRIVE_MDNS_ENABLE=true \
  PEERDRIVE_RELAY_ENABLE=true PEERDRIVE_RELAY_MODE=client \
  PEERDRIVE_HOLE_PUNCH=true PEERDRIVE_STORAGE=/tmp/pd-b \
  PEERDRIVE_BOOTSTRAP_PEER="/ip4/127.0.0.1/tcp/<A_P2P_PORT>/p2p/<A_PEER_ID>" \
  go run ./cmd/server/main.go

# Terminal 3 — Testing
# Get node info
curl -s -x "" http://localhost:3001/p2p/node | jq
A_ID=$(curl -s -x "" http://localhost:3001/p2p/node | jq -r .peer_id)

# View discovered nodes
curl -s -x "" http://localhost:3001/p2p/discovered | jq

# Node B connects to A
curl -s -x "" -H "Content-Type: application/json" \
  -d "{\"peer_id\":\"$A_ID\",\"addrs\":[\"/ip4/127.0.0.1/tcp/<A_P2P_PORT>\"]}" \
  -X POST http://localhost:3002/p2p/connect | jq

# Verify connection
curl -s -x "" http://localhost:3002/p2p/peers | jq
# → ["<A_PEER_ID>"]

# Ping
curl -s -x "" http://localhost:3002/p2p/ping/$A_ID | jq
# → {"peer":"...","rtt":"1.5ms"}

# Upload file to A → announce → B requests
echo "P2P test" > /tmp/p2p-test.txt
HASH=$(curl -s -x "" -F "file=@/tmp/p2p-test.txt" http://localhost:3001/files/upload | jq -r .hash)
curl -s -x "" -X POST http://localhost:3001/p2p/announce -d "{\"hash\":\"$HASH\"}" | jq
curl -s -x "" -X POST http://localhost:3002/p2p/request-file \
  -d "{\"hash\":\"$HASH\",\"peer_ids\":[\"$A_ID\"]}" | jq
```

### 5.6 BT Functionality Manual Tests

```bash
# 1. View BT DHT status
curl -s -x "" http://localhost:3000/bt/status | jq

# 2. Announce a hash
HASH="eafb6f737b516be4c8899299b4732f3d54ea5d119ce0571ce6f5cd2d55735275"
curl -s -x "" -X POST http://localhost:3000/bt/announce \
  -H "Content-Type: application/json" \
  -d "{\"hash\":\"$HASH\"}" | jq

# 3. BEP 44 Put
curl -s -x "" -X POST http://localhost:3000/bt/bep44/put \
  -H "Content-Type: application/json" \
  -d '{"v":"SGVsbG8gV29ybGQ="}' | jq
# → {"target":"...","status":"stored locally"}

# 4. BEP 44 Get (using the target returned above)
TARGET=$(curl -s -x "" -X POST http://localhost:3000/bt/bep44/put \
  -H "Content-Type: application/json" \
  -d '{"v":"SGVsbG8gV29ybGQ="}' | jq -r .target)
curl -s -x "" -X POST http://localhost:3000/bt/bep44/get \
  -H "Content-Type: application/json" \
  -d "{\"target\":\"$TARGET\"}" | jq
# → {"v":"SGVsbG8gV29ybGQ="}

# 5. BEP 51 Sample
curl -s -x "" http://localhost:3000/bt/bep51/sample | jq
```

### 5.7 WebRTC Signaling Manual Tests

```bash
# 1. Get WebRTC configuration
curl -s -x "" http://localhost:3000/p2p/webrtc/info | jq

# 2. Get WebSocket info
curl -s -x "" http://localhost:3000/p2p/ws/info | jq

# 3. Test signaling with wscat (requires wscat installation)
# npm install -g wscat
# wscat -c ws://localhost:3000/ws/signal
```

---

## 6. Troubleshooting

### 6.1 Service Fails to Start

**Symptom**: `go run ./cmd/server/main.go` errors or exits immediately

```bash
# Check port usage
fuser 3000/tcp
ss -tlnp | grep 3000

# Kill the occupying process
fuser -k 3000/tcp

# Check environment variables
echo $PORT
echo $PEERDRIVE_STORAGE

# Check if database is corrupted
rm -f go/peerdrive.db  # Warning: will lose all registered data

# Check storage directory permissions
ls -la go/storage/
```

### 6.2 P2P Nodes Cannot Interconnect

**Symptom**: `GET /p2p/peers` returns `[]`

```bash
# 1. Confirm P2P is enabled
curl -x "" http://localhost:3000/p2p/status | jq .enabled
# → true

# 2. Confirm node info is normal
curl -x "" http://localhost:3000/p2p/node | jq

# 3. Check mDNS discovery
curl -x "" http://localhost:3000/p2p/discovered | jq

# 4. Check firewall
# Ensure P2P port (usually 40000+) is not blocked by firewall
sudo ufw status

# 5. Manual connect (using correct multiaddr)
# First get P2P address from target node
curl -x "" http://localhost:<OTHER_PORT>/p2p/node | jq '.addrs'
# Then connect with the actual address
curl -x "" -X POST http://localhost:3000/p2p/connect \
  -H "Content-Type: application/json" \
  -d '{"addr":"/ip4/127.0.0.1/tcp/<PORT>/p2p/<PEER_ID>"}'
```

### 6.3 BT DHT Has No Nodes

**Symptom**: `num_nodes: 0`

```bash
# 1. Confirm BT DHT is enabled
curl -x "" http://localhost:3000/bt/status | jq .enabled

# 2. Check network connectivity
# BT DHT uses UDP 6881, ensure outbound UDP is not blocked

# 3. Wait for bootstrapping (DHT joining network takes 1-2 minutes)
sleep 60
curl -x "" http://localhost:3000/bt/status | jq .num_nodes

# 4. If still 0, check if system proxy is intercepting UDP
# Privox only proxies HTTP, generally does not affect UDP
```

### 6.4 Test Script Errors

**Symptom**: `curl: (7) Failed to connect`

```bash
# Confirm service is running
curl -x "" http://localhost:3000/ping

# Confirm correct port (some tests use 3001/3002/3999)
curl -x "" http://localhost:3999/ping

# If using a proxy, add -x "" or no_proxy='*'
export no_proxy='*'
```

**Symptom**: `jq: parse error`

```bash
# Check if response is valid JSON
curl -s -x "" http://localhost:3000/p2p/status

# If empty or HTML returned, endpoint doesn't exist or route not registered
# Check route registration
grep -n "GET\|POST" go/internal/router/router.go
```

### 6.5 Compilation Errors

```bash
# Clean Go cache
go clean -cache -modcache

# Re-download dependencies
cd go
go mod tidy
go mod download

# Check Go version
go version  # Requires >= 1.24
```

### 6.6 Common HTTP Status Codes

| Status Code | Meaning | Common Cause |
|-------------|---------|--------------|
| 200 | Success | — |
| 201 | Created successfully | — |
| 206 | Partial content | Range request |
| 400 | Bad request | Invalid parameters, path traversal |
| 401 | Unauthorized | Missing/invalid token |
| 404 | Not found | Hash not found |
| 409 | Conflict | Duplicate collection name |
| 413 | Content too large | File exceeds limit |
| 500 | Server error | Check service logs |

### 6.7 Log Viewing

```bash
# View service logs (if redirected)
tail -f /root/p2p.log

# Go program's stdout/stderr
# If running in foreground, look at terminal output

# Search for specific keywords
grep -i "error\|panic\|fatal" /root/p2p.log

# View last 100 lines
tail -n 100 /root/p2p.log
```

---

## 7. Test Matrix

For the complete test matrix, see [TEST-MATRIX.md](TEST-MATRIX.md), covering:

| Category | Test ID Range | Count | Description |
|----------|--------------|-------|-------------|
| File System | F-01 ~ F-25 | 25 | Upload/register/download/delete/verify |
| Collection System | C-01 ~ C-18 | 18 | Anonymous collections/user collections/versioning |
| P2P Network | P-01 ~ P-12 | 12 | libp2p/DHT/Exchange |
| BitTorrent | B-01 ~ B-16 | 16 | BT DHT/Wire/BEP standards |
| Dual-Stack | D-01 ~ D-03 | 3 | Dual-stack announce/find |
| Authentication | R-01 ~ R-08 | 8 | Register/login/JWT/Relay |
| Frontend | UI-01 ~ UI-16 | 16 | Page load/interaction/error handling |
| Deployment/Ops | O-01 ~ O-07 | 7 | Build/deploy/Docker/memory |
| Real Network | I-01 ~ I-04 | 4 | IPFS/BT public connectivity |

---

## 8. Chaos Testing

Verify P2P protocol robustness under harsh network conditions.

### 8.1 Prerequisites

```bash
# Load kernel modules
sudo modprobe sch_netem sch_tbf sch_htb cls_u32

# Install dependencies
sudo apt-get install iproute2 iptables
```

### 8.2 Quick Usage

```bash
cd /mnt/d/WorkPlace/peerdrive/go

# Simulate 30% packet loss + 500ms latency + 1Mbps bandwidth
sudo bash test/chaos-net.sh start --loss 30% --latency 500ms --bandwidth 1Mbps

# Run tests under chaos conditions
bash test/test-under-chaos.sh --loss 30% --test bt-full-test.sh

# Run full chaos matrix (all combinations)
bash test/test-under-chaos.sh --matrix

# Stop chaos
sudo bash test/chaos-net.sh stop
```

### 8.3 Chaos Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `--loss X%` | 0% | Packet loss rate |
| `--latency Xms` | 0ms | Extra latency |
| `--jitter Xms` | 0ms | Latency jitter |
| `--bandwidth X` | unlimited | Bandwidth limit |

### 8.4 Chaos Matrix

Matrix mode automatically tests the following combinations:
- Packet loss: 0%, 10%, 30%, 50%
- Latency: 0ms, 100ms, 500ms
- Bandwidth: unlimited, 1Mbps, 100Kbps
- Combination: 30% loss + 500ms latency + 100Kbps

Detailed documentation: [CHAOS_TESTING.md](CHAOS_TESTING.md)

---

## Appendix A: One-Click Test Environment Startup

```bash
#!/bin/bash
# Save as start-test-env.sh
cd /mnt/d/WorkPlace/peerdrive/go
export no_proxy='*'

# Clean old database
rm -f peerdrive.db

# Start service
PORT=3000 PEERDRIVE_STORAGE=./storage PEERDRIVE_P2P_ENABLE=true \
  go run ./cmd/server/main.go &

sleep 5
echo "Service started: http://localhost:3000"
echo "Swagger: http://localhost:3000/swagger/index.html"
echo "Health check: $(curl -s -x "" http://localhost:3000/ping)"
```

## Appendix B: VPS Test Environment

```bash
# SSH to VPS
ssh -p26275 root@bwh.moonchan.xyz

# Check service status
systemctl status peerdrive-relay
curl http://127.0.0.1:3000/ping
curl http://127.0.0.1:3000/p2p/status | python3 -m json.tool
curl http://127.0.0.1:3000/bt/status | python3 -m json.tool

# View logs
journalctl -u peerdrive-relay -n 50 --no-pager

# Restart service
systemctl restart peerdrive-relay
```

## Appendix C: Test Result Template

```markdown
## Test Report — YYYY-MM-DD

### Environment
- Service version: <commit hash>
- Port: 3000
- P2P: enabled / disabled
- BT DHT: ena bled / disabled

### Results
| Test Suite | Pass | Fail | Total |
|------------|------|------|-------|
| go test | | | |
| e2e-all.sh | | | |
| bt-full-test.sh | | | |
| p2p.sh | | | |

### Issues Found
1. ...
2. ...

### Notes
...
```
