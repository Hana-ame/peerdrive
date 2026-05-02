#!/usr/bin/env bash
# ============================================================
# Peerdrive BT Module Full Integration Test
# Tests ALL BT-related API endpoints against a running instance.
# ============================================================
set -euo pipefail

# bypass system proxy for localhost
export no_proxy='*'

# --- helpers ---
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; NC='\033[0m'
PASS=0; FAIL=0; TOTAL=0

pass() { echo -e "  ${GREEN}PASS${NC} $1"; PASS=$((PASS+1)); TOTAL=$((TOTAL+1)); }
fail() { echo -e "  ${RED}FAIL${NC} $1"; FAIL=$((FAIL+1)); TOTAL=$((TOTAL+1)); }
info() { echo -e "${CYAN}--- $1 ---${NC}"; }

assert_status() {
  local code="$1" expected="$2" label="$3"
  if [ "$code" = "$expected" ]; then
    pass "$label"
  else
    fail "$label (expected HTTP $expected, got $code)"
  fi
}

assert_contains() {
  local body="$1" pattern="$2" label="$3"
  if echo "$body" | grep -q "$pattern"; then
    pass "$label"
  else
    fail "$label (missing '$pattern' in response)"
  fi
}

assert_json() {
  local body="$1" label="$2"
  if echo "$body" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null; then
    pass "$label"
  else
    fail "$label (invalid JSON)"
  fi
}

assert_gt() {
  local val="$1" expected="$2" label="$3"
  if [ "$val" -gt "$expected" ]; then
    pass "$label"
  else
    fail "$label (expected > $expected, got $val)"
  fi
}

# --- config ---
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORK_DIR="/tmp/peerdrive_bt_test_$$"
SERVER_BIN="$WORK_DIR/peerdrive-server"
API="http://localhost:39997"
STORAGE="$WORK_DIR/storage"
DOWNLOADS="$WORK_DIR/downloads"
BT_DHT_PORT="19997"
TORRENT_FILE="$WORK_DIR/test.torrent"
TEST_DATA_FILE="$WORK_DIR/test_data.txt"

cleanup() {
  echo ""
  info "CLEANUP"
  kill $SERVER_PID 2>/dev/null || true
  rm -rf "$WORK_DIR"
  echo "  Cleaned up $WORK_DIR"
}
trap cleanup EXIT

# --- 0. Setup ---
info "0. SETUP"
rm -rf "$WORK_DIR"
mkdir -p "$STORAGE" "$DOWNLOADS"

# Write test data
TEST_DATA="Peerdrive BT integration test $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "$TEST_DATA" > "$TEST_DATA_FILE"
echo "  Test data: $TEST_DATA"

# We need python3 for JSON parsing
if ! command -v python3 &>/dev/null; then
  echo "  ERROR: python3 is required"
  exit 1
fi

# Build server from worktree
echo "  Building server..."
cd "$SCRIPT_DIR/.."
go build -tags nosqlite -o "$SERVER_BIN" ./cmd/server/main.go
echo "  Build complete: $SERVER_BIN"

# Start server
export PORT=39997
export PEERDRIVE_STORAGE="$STORAGE"
export PEERDRIVE_P2P_ENABLE=false
export PEERDRIVE_BT_DHT_ENABLE=true
export PEERDRIVE_BT_DHT_LISTEN=":$BT_DHT_PORT"
export PEERDRIVE_DOWNLOAD_DIR="$DOWNLOADS"
export PEERDRIVE_STORAGE_ENABLE=false

"$SERVER_BIN" &
SERVER_PID=$!

# Wait up to 45 seconds for the server to be ready (DHT bootstrap can be slow).
echo -n "  Waiting for server startup"
for i in $(seq 1 45); do
  if curl -sf "$API/ping" > /dev/null 2>&1; then
    echo " (ready after ${i}s)"
    break
  fi
  echo -n "."
  sleep 1
done
if ! curl -sf "$API/ping" > /dev/null 2>&1; then
  echo ""
  echo "  FAIL: Server failed to start on port 39997 after 45s"
  exit 1
fi
pass "Server started on port 39997 (PID $SERVER_PID)"

# Get DHT some time to bootstrap
echo "  Waiting 5s more for DHT bootstrap..."
sleep 5

# ============================================================
info "1. HEALTH (ping)"
# ============================================================
RESP=$(curl -sf "$API/ping")
assert_contains "$RESP" "pong" "GET /ping returns pong"
# ping returns "pong" (plain text), not JSON — skip assert_json for this endpoint

# ============================================================
info "2. BT DHT STATUS"
# ============================================================
RESP=$(curl -sf "$API/bt/status")
assert_json "$RESP" "GET /bt/status returns valid JSON"
assert_contains "$RESP" '"enabled":true' "BT DHT is enabled"

# Extract num_nodes
NUM_NODES=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('num_nodes', 0))")
echo "  DHT nodes: $NUM_NODES"
assert_gt "$NUM_NODES" "0" "DHT has at least 1 node in routing table"

# ============================================================
info "3. CREATE TEST TORRENT"
# ============================================================
# Use the Python script from bt-integration if available, else create inline
if [ -f "$SCRIPT_DIR/bt-integration/create_torrent.py" ]; then
  python3 "$SCRIPT_DIR/bt-integration/create_torrent.py" "$TEST_DATA_FILE" "$TORRENT_FILE"
  echo "  Torrent created via create_torrent.py"
elif [ -f "$SCRIPT_DIR/../test/bt-integration/create_torrent.py" ]; then
  python3 "$SCRIPT_DIR/../test/bt-integration/create_torrent.py" "$TEST_DATA_FILE" "$TORRENT_FILE"
  echo "  Torrent created via create_torrent.py (alt path)"
else
  # Create torrent inline using Python
  python3 -c "
import hashlib, os, sys
data = open('$TEST_DATA_FILE', 'rb').read()
pl = 16384
pieces = b''
for off in range(0, len(data), pl):
    pieces += hashlib.sha1(data[off:off+pl]).digest()
def bs(s): return str(len(s)).encode() + b':' + s
def bi(i): return b'i' + str(i).encode() + b'e'
info = b'd' + bs(b'name') + bs(b'test.txt') + bs(b'piece length') + bi(pl) + bs(b'pieces') + bs(pieces) + bs(b'length') + bi(len(data)) + b'e'
ih = hashlib.sha1(info).hexdigest()
tor = b'd' + bs(b'announce') + bs(b'') + bs(b'created by') + bs(b'peerdrive-bt-test') + bs(b'creation date') + bi(int(os.path.getmtime('$TEST_DATA_FILE'))) + bs(b'info') + info + b'e'
open('$TORRENT_FILE', 'wb').write(tor)
print('InfoHash:', ih)
"
fi

if [ ! -f "$TORRENT_FILE" ]; then
  fail "Torrent file was not created"
else
  pass "Torrent file created at $TORRENT_FILE"
fi

# Extract infohash from torrent
TORRENT_IH=$(python3 -c "
import hashlib, sys
data = open('$TORRENT_FILE', 'rb').read()
def bdecode(d, pos):
    if d[pos:pos+1] == b'i':
        end = d.index(b'e', pos)
        return int(d[pos+1:end]), end+1
    elif d[pos:pos+1] in b'0123456789':
        colon = d.index(b':', pos)
        n = int(d[pos:colon])
        return d[colon+1:colon+1+n], colon+1+n
    elif d[pos:pos+1] == b'd':
        pos += 1
        dct = {}
        while d[pos:pos+1] != b'e':
            k, pos = bdecode(d, pos)
            v, pos = bdecode(d, pos)
            dct[k] = v
        return dct, pos+1
    raise ValueError('bad')
info_dict = bdecode(data, 0)[0][b'info']
info_raw = open('$TORRENT_FILE', 'rb').read()
# Find info dict boundaries
start = data.index(b'4:info') + 6
end = len(data) - 1
# Benencde the info dict
if isinstance(info_dict, dict):
    import io
    def bencode_dict(d):
        buf = io.BytesIO()
        buf.write(b'd')
        for k in sorted(d.keys(), key=lambda x: x if isinstance(x, bytes) else x.encode()):
            buf.write(bencode_val(k))
            buf.write(bencode_val(d[k]))
        buf.write(b'e')
        return buf.getvalue()
    def bencode_val(v):
        if isinstance(v, int):
            return b'i' + str(v).encode() + b'e'
        elif isinstance(v, bytes):
            return str(len(v)).encode() + b':' + v
        elif isinstance(v, dict):
            return bencode_dict(v)
        elif isinstance(v, list):
            buf = io.BytesIO()
            buf.write(b'l')
            for item in v:
                buf.write(bencode_val(item))
            buf.write(b'e')
            return buf.getvalue()
        return b''
    info_bencoded = bencode_dict(info_dict)
else:
    info_bencoded = info_dict
print(hashlib.sha1(info_bencoded).hexdigest())
")
echo "  Torrent infohash: $TORRENT_IH"

# ============================================================
info "4. UPLOAD TORRENT VIA API"
# ============================================================
UP=$(curl -s -w '\n%{http_code}' -X POST "$API/bt/torrent" -F "torrent=@$TORRENT_FILE")
UP_BODY=$(echo "$UP" | head -n -1)
UP_CODE=$(echo "$UP" | tail -n 1)
assert_status "$UP_CODE" "200" "POST /bt/torrent returns 200"
assert_json "$UP_BODY" "POST /bt/torrent returns valid JSON"
assert_contains "$UP_BODY" '"infohash"' "Response has infohash"
assert_contains "$UP_BODY" '"name"' "Response has name"
assert_contains "$UP_BODY" '"status"' "Response has status"

# Extract infohash from response
IH=$(echo "$UP_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['infohash'])")
echo "  Download infohash: $IH"

# ============================================================
info "5. ANNOUNCE VIA BT DHT"
# ============================================================
ANNOUNCE_RESP=$(curl -s -w '\n%{http_code}' -X POST "$API/bt/announce" \
  -H "Content-Type: application/json" \
  -d "{\"hash\":\"$IH\"}")
ANNOUNCE_BODY=$(echo "$ANNOUNCE_RESP" | head -n -1)
ANNOUNCE_CODE=$(echo "$ANNOUNCE_RESP" | tail -n 1)
assert_status "$ANNOUNCE_CODE" "200" "POST /bt/announce returns 200"
assert_json "$ANNOUNCE_BODY" "POST /bt/announce returns valid JSON"
assert_contains "$ANNOUNCE_BODY" '"status"' "Announce response has status"
echo "  Announce: $ANNOUNCE_BODY"

# ============================================================
info "6. FIND PROVIDERS VIA BT DHT"
# ============================================================
FIND_RESP=$(curl -s -w '\n%{http_code}' -X POST "$API/bt/find" \
  -H "Content-Type: application/json" \
  -d "{\"hash\":\"$IH\"}")
FIND_BODY=$(echo "$FIND_RESP" | head -n -1)
FIND_CODE=$(echo "$FIND_RESP" | tail -n 1)

# Find may succeed with 200 even if no peers found (that's expected for a new infohash)
if [ "$FIND_CODE" = "200" ]; then
  assert_json "$FIND_BODY" "POST /bt/find returns valid JSON"
  assert_contains "$FIND_BODY" '"peers"' "Find response has peers field"
  assert_contains "$FIND_BODY" '"hash"' "Find response has hash field"
  pass "POST /bt/find returns 200"
else
  # If DHT find timed out or failed, warn but don't fail
  echo "  WARN: find returned HTTP $FIND_CODE (DHT may not be fully bootstrapped)"
  assert_json "$FIND_BODY" "POST /bt/find returned JSON even on error"
fi

# ============================================================
info "7. CHECK DOWNLOAD PROGRESS"
# ============================================================
DL_RESP=$(curl -s -w '\n%{http_code}' "$API/bt/download/$IH")
DL_BODY=$(echo "$DL_RESP" | head -n -1)
DL_CODE=$(echo "$DL_RESP" | tail -n 1)
if [ "$DL_CODE" = "200" ]; then
  assert_json "$DL_BODY" "GET /bt/download/:ih returns valid JSON"
  assert_contains "$DL_BODY" '"status"' "Download progress has status"
  echo "  Download status: $(echo "$DL_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','unknown'))")"
  pass "GET /bt/download/:ih returns 200"
else
  # Download may have already errored out (no real peers) — that's OK for API test
  echo "  Note: Download progress returned HTTP $DL_CODE (expected for no-peers environment)"
  assert_json "$DL_BODY" "GET /bt/download/:ih returned JSON"
fi

# ============================================================
info "8. LIST DOWNLOADS"
# ============================================================
LIST_RESP=$(curl -sf "$API/bt/downloads")
assert_json "$LIST_RESP" "GET /bt/downloads returns valid JSON"
assert_contains "$LIST_RESP" '"downloads"' "List response has downloads array"
assert_contains "$LIST_RESP" '"count"' "List response has count"

# ============================================================
info "9. BEP 44 PUT/GET ROUNDTRIP"
# ============================================================

# Encode test message as base64
TEST_MSG="BEP44 test message $(date -u +%s)"
B64_DATA=$(echo -n "$TEST_MSG" | base64)

PUT_RESP=$(curl -s -w '\n%{http_code}' -X POST "$API/bt/bep44/put" \
  -H "Content-Type: application/json" \
  -d "{\"data\":\"$B64_DATA\",\"mutable\":false}")
PUT_BODY=$(echo "$PUT_RESP" | head -n -1)
PUT_CODE=$(echo "$PUT_RESP" | tail -n 1)

if [ "$PUT_CODE" = "200" ]; then
  assert_json "$PUT_BODY" "POST /bt/bep44/put returns valid JSON"
  assert_contains "$PUT_BODY" '"target"' "BEP44 put response has target"
  BEP44_TARGET=$(echo "$PUT_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['target'])")
  echo "  BEP44 target: $BEP44_TARGET"
  pass "POST /bt/bep44/put returns 200"

  # Now GET it back
  sleep 1
  GET_RESP=$(curl -s -w '\n%{http_code}' -X POST "$API/bt/bep44/get" \
    -H "Content-Type: application/json" \
    -d "{\"target\":\"$BEP44_TARGET\"}")
  GET_BODY=$(echo "$GET_RESP" | head -n -1)
  GET_CODE=$(echo "$GET_RESP" | tail -n 1)

  if [ "$GET_CODE" = "200" ]; then
    assert_json "$GET_BODY" "POST /bt/bep44/get returns valid JSON"
    assert_contains "$GET_BODY" '"data"' "BEP44 get response has data"
    # Decode and verify
    ROUNDTRIP=$(echo "$GET_BODY" | python3 -c "import sys,json,base64; print(base64.b64decode(json.load(sys.stdin)['data']).decode())" 2>/dev/null || echo "")
    if [ "$ROUNDTRIP" = "$TEST_MSG" ]; then
      pass "BEP44 put/get roundtrip data matches"
    else
      echo "  Expected: $TEST_MSG"
      echo "  Got: $ROUNDTRIP"
      fail "BEP44 put/get roundtrip data mismatch"
    fi
  else
    echo "  BEP44 get returned HTTP $GET_CODE (may not propagate DHT fast enough)"
    assert_json "$GET_BODY" "POST /bt/bep44/get returned JSON"
  fi
else
  echo "  BEP44 put returned HTTP $PUT_CODE (DHT may not be fully bootstrapped)"
  assert_json "$PUT_BODY" "POST /bt/bep44/put returned JSON"
fi

# ============================================================
info "10. BEP 51 SAMPLE INFOHASHES"
# ============================================================
SAMPLE_RESP=$(curl -s -w '\n%{http_code}' "$API/bt/bep51/sample")
SAMPLE_BODY=$(echo "$SAMPLE_RESP" | head -n -1)
SAMPLE_CODE=$(echo "$SAMPLE_RESP" | tail -n 1)

if [ "$SAMPLE_CODE" = "200" ]; then
  assert_json "$SAMPLE_BODY" "GET /bt/bep51/sample returns valid JSON"
  assert_contains "$SAMPLE_BODY" '"samples"' "BEP51 response has samples array"
  assert_contains "$SAMPLE_BODY" '"count"' "BEP51 response has count"
  SAMPLE_COUNT=$(echo "$SAMPLE_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin).get('count', 0))")
  echo "  BEP51 samples collected: $SAMPLE_COUNT"
  pass "GET /bt/bep51/sample returns 200"
else
  echo "  BEP51 sample returned HTTP $SAMPLE_CODE (DHT may not have enough peers)"
  assert_json "$SAMPLE_BODY" "GET /bt/bep51/sample returned JSON"
fi

# ============================================================
info "11. BT GLOBAL STATS"
# ============================================================
STATS_RESP=$(curl -sf "$API/bt/stats")
assert_json "$STATS_RESP" "GET /bt/stats returns valid JSON"
assert_contains "$STATS_RESP" '"active_torrents"' "Stats has active_torrents"
assert_contains "$STATS_RESP" '"dht_nodes"' "Stats has dht_nodes"
assert_contains "$STATS_RESP" '"seeding"' "Stats has seeding"

# ============================================================
info "SUMMARY"
# ============================================================
echo -e "  ${GREEN}PASS: $PASS${NC}"
echo -e "  ${RED}FAIL: $FAIL${NC}"
echo "  TOTAL: $TOTAL"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
exit 0
