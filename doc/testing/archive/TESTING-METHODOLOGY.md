# Peerdrive Testing Methodology

## Test Environment

| Component | Address | Purpose |
|-----------|---------|---------|
| Node A (relay) | 97.64.30.221:3000 | Public relay, IPFS+BT |
| Node B | 97.64.30.221:3001 | Client node |
| Reg Server | 97.64.30.221:4000 | JWT authentication |
| IPFS test peer | 97.64.30.221:9001 | Simulated IPFS node |
| BT test peer | 97.64.30.221:6883 | Simulated BT DHT node |

---

## Phase 1: Basic Health Checks

### Test 1.1 — Ping Endpoint
**Purpose**: Confirm the service process is running and HTTP layer is reachable  
**Test target**: 200 response + `pong`

```bash
curl http://97.64.30.221:3000/ping
# → pong
```

### Test 1.2 — P2P Status
**Purpose**: Confirm libp2p host is created and DHT is bootstrapped  
**Test target**: `enabled: true`, `peer_id` non-empty, `addrs` non-empty

```bash
curl http://97.64.30.221:3000/p2p/status
# → {"enabled":true, "peer_id":"12D3KooW...", "relay_mode":"server", ...}
```

---

## Phase 2: IPFS/libp2p Connectivity

### Test 2.1 — Node B Start and Connect
**Purpose**: Verify the bootstrap mechanism can allow a new node to automatically discover and connect to the relay  
**Test target**: After Node B starts, `connected_count >= 1`, Node A's peers list contains B

```bash
# Start Node B, bootstrap pointing to relay
PORT=3001 PEERDRIVE_P2P_ENABLE=true \
  PEERDRIVE_BOOTSTRAP_PEER="/ip4/97.64.30.221/tcp/37537/p2p/<RELAY_ID>" \
  /root/pd-server &

sleep 5
curl http://127.0.0.1:3001/p2p/peers   # → ["<RELAY_ID>"]
curl http://127.0.0.1:3000/p2p/peers   # → ["<NODE_B_ID>"]
```

### Test 2.2 — Ping Latency
**Purpose**: Verify libp2p ping protocol communication  
**Test target**: RTT < 10ms (same machine), returns JSON with `rtt`

```bash
B_ID=$(curl -s http://127.0.0.1:3001/p2p/node | jq -r .peer_id)
curl http://127.0.0.1:3000/p2p/ping/$B_ID
# → {"peer":"12D3...","rtt":"1.649995ms"}
```

### Test 2.3 — Manual Connect
**Purpose**: Verify manual multiaddr connection  
**Test target**: Returns `{"status":"connected"}`

```bash
curl -X POST http://127.0.0.1:3000/p2p/connect \
  -H 'Content-Type: application/json' \
  -d '{"addr":"/ip4/97.64.30.221/tcp/44729/p2p/<B_ID>"}'
# → {"status":"connected"}
```

---

## Phase 3: File Operations

### Test 3.1 — HTTP Upload
**Purpose**: Verify multipart upload + SHA256 calculation + storage
**Test target**: Returns `hash` (64 hex chars), `size` correct

```bash
echo "test content" > /tmp/test.txt
curl -X POST http://97.64.30.221:3000/files/upload \
  -F "file=@/tmp/test.txt"
# → {"hash":"eafb6f7...","size":13,"filename":"test.txt"}
```

### Test 3.2 — Local File Registration (New File)
**Purpose**: Verify registering a file to Peerdrive from a disk path
**Test target**: Returns hash, then SHA256 download available

```bash
curl -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname"}'
# → {"hash":"abc123...","filename":"hostname"}
```

### Test 3.3 — Duplicate Registration (Already in DB)
**Purpose**: Verify registering a file that already exists in DB does not create duplicate records
**Test target**: Returns same hash, file_meta table has only one record

```bash
# First registration → hash1
HASH1=$(curl -s -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname"}' | jq -r .hash)

# Second registration of same file → should return same hash
HASH2=$(curl -s -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname"}' | jq -r .hash)

test "$HASH1" = "$HASH2"  # Must be same
```

### Test 3.4 — Local File Registration (With Custom Filename)
**Purpose**: Verify specifying a filename different from the disk name during registration
**Test target**: Returned filename equals the custom name

```bash
curl -X POST http://97.64.30.221:3000/files/register_local \
  -H 'Content-Type: application/json' \
  -d '{"path":"/etc/hostname","filename":"my-host.txt"}'
# → {"hash":"...","filename":"my-host.txt"}
```

### Test 3.5 — Recursive Folder Registration
**Purpose**: Verify recursively traversing a directory to register all files
**Test target**: Returns registered array, count equals number of files in directory

```bash
curl -X POST http://97.64.30.221:3000/files/register_folder \
  -H 'Content-Type: application/json' \
  -d '{"folder_path":"/etc/ssl"}'
# → {"registered":[{path,hash,filename},...],"count":N}
```

### Test 3.6 — Partial Folder Registration (Some Already Registered)
**Purpose**: Verify when some files in a directory are already registered, only new files are registered
**Test target**: New registration count + already-existing count = total file count

```bash
# First register a single file
curl -X POST http://97.64.30.221:3000/files/register_local \
  -d '{"path":"/etc/ssl/certs/ca-certificates.crt"}'

# Then register the entire directory → should skip already-registered files
curl -X POST http://97.64.30.221:3000/files/register_folder \
  -d '{"folder_path":"/etc/ssl"}'
# → count should be < total files in directory (skipped already-registered ones)
```

### Test 3.7 — URL File Registration
**Purpose**: Verify registering a remote file via HTTP URL
**Test target**: Returns hash, then downloadable via SHA256

```bash
curl -X POST http://97.64.30.221:3000/files/register_url \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/robots.txt","filename":"robots.txt"}'
# provider_type = "http"
# → {"hash":"...","filename":"robots.txt","provider_type":"http"}
```

### Test 3.8 — URL-Registered File Download
**Purpose**: Verify files registered via URL can be downloaded through SHA256 (pulled from remote)
**Test target**: HTTP 200, returns correct content

```bash
# Register URL file
RESULT=$(curl -s -X POST http://97.64.30.221:3000/files/register_url \
  -d '{"url":"https://example.com/robots.txt"}')
HASH=$(echo "$RESULT" | jq -r .hash)

# SHA256 download → should auto-pull via http provider
curl http://97.64.30.221:3000/sha256sum/$HASH | sha256sum
```

### Test 3.9 — Registered File Listing
**Purpose**: Verify listing all registered files
**Test target**: Returns array containing hash/filename/size/mime_type/provider_type

```bash
curl http://97.64.30.221:3000/files?sort=time
# → [{hash,filename,size,mime_type,provider_type,provider_path,created_at},...]
```

### Test 3.10 — File Verification
**Purpose**: Verify checking if a file exists and is consistent by hash
**Test target**: exists=true, consistent=true

```bash
HASH=$(curl -s http://97.64.30.221:3000/files?sort=time | jq -r '.[0].hash')
curl http://97.64.30.221:3000/files/verify/$HASH
# → {"hash":"...","exists":true,"consistent":true,"filename":"...","size":N}
```

### Test 3.11 — Nonexistent File Verification
**Purpose**: Verify correct status for nonexistent hashes
**Test target**: exists=false

```bash
curl http://97.64.30.221:3000/files/verify/0000000000000000000000000000000000000000000000000000000000000000
# → {"exists":false} or 404
```

### Test 3.12 — File Deletion
**Purpose**: Verify deleting file metadata (not physical file)
**Test target**: After deletion, SHA256 download returns 404, but physical file still exists

```bash
# First register a file
HASH=$(echo "del-test" > /tmp/del.txt && \
  curl -s -X POST http://97.64.30.221:3000/files/upload -F "file=@/tmp/del.txt" | jq -r .hash)

# Delete
curl -X DELETE http://97.64.30.221:3000/files/$HASH
# → 200

# SHA256 download → 404
curl -o /dev/null -w "%{http_code}" http://97.64.30.221:3000/sha256sum/$HASH
# → 404
```

### Test 3.13 — File Browser
**Purpose**: Verify browsing server filesystem (not limited to registered files)
**Test target**: Returns directory entry array containing is_dir/name/path/size

```bash
curl "http://97.64.30.221:3000/files/browse?path=/etc"
# → [{name,path,is_dir,size,mod_time},...]
```

### Test 3.14 — File Browser Root Directory
**Purpose**: Verify default path (Linux /, Windows C:\)
**Test target**: Returns root directory contents

```bash
curl "http://97.64.30.221:3000/files/browse"
# → Directory listing for default path
```

### Test 3.15 — Empty Folder Registration
**Purpose**: Verify behavior when registering an empty directory
**Test target**: Returns registered=[], count=0, no error

```bash
mkdir -p /tmp/empty-dir
curl -X POST http://97.64.30.221:3000/files/register_folder \
  -d '{"folder_path":"/tmp/empty-dir"}'
# → {"registered":[],"count":0}
```

---

## Phase 4: BT DHT Protocol

### Test 4.1 — BT DHT Status
**Purpose**: Verify BT Mainline DHT node has started and connected to the global network  
**Test target**: `enabled: true`, `num_nodes > 0` (proves connected to global BT network)

```bash
curl http://97.64.30.221:3000/bt/status
# → {"enabled":true,"listen_addr":"0.0.0.0:6881","num_nodes":127}
```

### Test 4.2 — BT Announce
**Purpose**: Verify ability to announce files to the global BT DHT  
**Test target**: Returns `"status":"announced on BT DHT"`

```bash
curl -X POST http://97.64.30.221:3000/bt/announce \
  -H 'Content-Type: application/json' \
  -d '{"hash":"eafb6f737b516be4c8899299b4732f3d54ea5d119ce0571ce6f5cd2d55735275"}'
# → {"status":"announced on BT DHT"}
```

### Test 4.3 — BT Find (Cross-Node)
**Purpose**: Verify cross-node BT DHT lookup — Node A announces, Node B finds  
**Test target**: Node B querying BT DHT can find files announced by Node A (count > 0)

```bash
# Node A announces
curl -X POST http://127.0.0.1:3000/bt/announce \
  -d '{"hash":"FILE_HASH"}'

# Node B finds (wait for DHT propagation)
sleep 3
curl -X POST http://127.0.0.1:3001/bt/find \
  -d '{"hash":"FILE_HASH"}'
# → {"count":1,"peers":["31.200.249.231:31934"]}
```

---

## Phase 5: P2P File Exchange

### Test 5.1 — IPFS Exchange
**Purpose**: Verify libp2p exchange protocol — B downloads file from A  
**Test target**: responses=1, returns correct file size

```bash
A_ID=$(curl -s http://127.0.0.1:3000/p2p/node | jq -r .peer_id)
curl -X POST http://127.0.0.1:3001/p2p/request-file \
  -d "{\"hash\":\"FILE_HASH\",\"peer_ids\":[\"$A_ID\"]}"
# → {"responses":1,"details":[{"hash":"...","size":13}]}
```

### Test 5.2 — Collection Sync
**Purpose**: Verify cross-node collection sync — A creates collection, B pulls  
**Test target**: synced count = 1

```bash
# A creates collection
curl -X POST http://127.0.0.1:3000/anon/collections \
  -d '{"entries":[{"path":"test.txt","hash":"FILE_HASH"}],"friendly_name":"Sync Test"}'
# → {"hash":"COLLECTION_HASH"}

# B syncs from A
curl -X POST http://127.0.0.1:3001/p2p/sync \
  -d "{\"peer_id\":\"$A_ID\",\"hash\":\"COLLECTION_HASH\",\"target_dir\":\"/tmp/synced\"}"
# → {"synced":["FILE_HASH"],"count":1}
```

---

## Phase 6: Dual-Stack

### Test 6.1 — Dual Announce
**Purpose**: Verify announcing on both IPFS and BT DHTs simultaneously  
**Test target**: `"announced on both networks"`

```bash
curl -X POST http://97.64.30.221:3000/p2p/dual/announce \
  -d '{"hash":"FILE_HASH"}'
# → {"status":"announced on both networks"}
```

### Test 6.2 — Dual Find
**Purpose**: Verify finding providers on both DHTs simultaneously  
**Test target**: Returns `ipfs_peers` and `bt_peers` fields

```bash
curl -X POST http://97.64.30.221:3000/p2p/dual/find \
  -d '{"hash":"FILE_HASH"}'
# → {"hash":"...","ipfs_peers":null,"bt_peers":["31.200.249.231:31934"]}
```

---

## Phase 7: Multi-Source Parallel Download

### Test 7.1 — Three-Source Simultaneous Download
**Purpose**: Verify the same file can be downloaded from relay, IPFS test peer, and BT test peer  
**Test target**: Three downloads return identical content, SHA256 matches

```python
# Download from relay
data1 = requests.get(f"http://97.64.30.221:3000/sha256sum/{hash}").content
# Download from IPFS test peer
data2 = requests.get(f"http://97.64.30.221:9001/files/{hash}").content
# Find peer via BT, then download
peers = bt_find(hash)  # find peer addresses
data3 = download_from_peer(peers[0], hash).content

assert sha256(data1) == hash
assert sha256(data2) == hash
assert sha256(data3) == hash
assert data1 == data2 == data3  # Three sources return identical content
```

---

## Phase 8: Frontend

### Test 8.1 — Playwright Smoke (16 tests)
**Purpose**: Verify all pages load and key UI elements render  
**Test target**: 16/16 pass

```javascript
// Test pages: Plaza, AnonCreator, FileManager, AnonExplorer, Settings
// Check items: search box, 4-tab, checkboxes, SHA256 input, LLM config
```

### Test 8.2 — Playwright Functional (18 tests)
**Purpose**: Verify actual user interaction flows  
**Test target**: 18/18 pass

```javascript
// Plaza: paste SHA256 → navigate to collection page
// AnonCreator: 4-tab visible, timeline date grouping, directory breadcrumb registration
// FileManager: checkbox toggle → selection count update
// Settings: LLM endpoint input, model dropdown
// Navbar: Ctrl+K search panel
```

---

## Phase 9: Go Unit Tests

### Test 9.1 — Full Run
**Purpose**: Verify all Go packages have no regressions  
**Test target**: 7 packages all `ok`

```bash
go test ./...
# ok  peerdrive/internal/config    0.021s
# ok  peerdrive/internal/controller 0.087s
# ok  peerdrive/internal/model      0.007s
# ok  peerdrive/internal/provider   0.008s
# ok  peerdrive/internal/repository 0.020s
# ok  peerdrive/internal/service    0.314s
```

---

## Unverified Items

| Item | Reason |
|------|--------|
| Docker 5-node networking | Code ready, `docker compose up` not executed |
| WebRTC actual transfer | Signaling + frontend components ready, not tested with two browsers |
| Resume/transfer continuation | ResumeManager code ready, not tested with interrupted download scenario |
| BT torrent download | Piece exchange code ready, not tested with real .torrent file |
| Chaos network | chaos-net.sh ready, tests not re-run under chaos conditions |
