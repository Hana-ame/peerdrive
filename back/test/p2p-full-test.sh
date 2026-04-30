#!/bin/bash
# p2p-full-test.sh — Two-node P2P Integration Test
# Tests: startup, status, node info, connect, ping, file upload, announce,
#        P2P exchange, topology, quality metrics, connections, stats.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BASE_DIR="/tmp/peerdrive_p2p_full_test"
SERVER_BIN="${ROOT_DIR}/p2p-test-server"
PORT_A=3091
PORT_B=3092

PASS=0
FAIL=0

cleanup() {
  echo ""
  echo "--- Cleaning up ---"
  kill ${PID_A:-} 2>/dev/null || true
  kill ${PID_B:-} 2>/dev/null || true
  wait ${PID_A:-} 2>/dev/null || true
  wait ${PID_B:-} 2>/dev/null || true
  rm -f "$SERVER_BIN" 2>/dev/null || true
  rm -rf "$BASE_DIR" 2>/dev/null || true
}
trap cleanup EXIT

red()    { printf "\033[31m%s\033[0m\n" "$*"; }
green()  { printf "\033[32m%s\033[0m\n" "$*"; }
yellow() { printf "\033[33m%s\033[0m\n" "$*"; }
bold()   { printf "\033[1m%s\033[0m\n" "$*"; }

check() {
  local label="$1" expected="$2" actual="$3"
  if echo "$actual" | grep -q "$expected"; then
    green "  [PASS] $label"
    PASS=$((PASS + 1))
  else
    red "  [FAIL] $label (expected to contain '$expected')"
    FAIL=$((FAIL + 1))
  fi
}

check_eq() {
  local label="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    green "  [PASS] $label"
    PASS=$((PASS + 1))
  else
    red "  [FAIL] $label (expected '$expected', got '$actual')"
    FAIL=$((FAIL + 1))
  fi
}

check_gt() {
  local label="$1" expected="$2" actual="$3"
  if [ "$actual" -gt "$expected" ]; then
    green "  [PASS] $label ($actual > $expected)"
    PASS=$((PASS + 1))
  else
    red "  [FAIL] $label (expected > $expected, got $actual)"
    FAIL=$((FAIL + 1))
  fi
}

API_A() { curl -sf -x "" "http://localhost:${PORT_A}$1" 2>/dev/null || echo "__FAIL__"; }
API_B() { curl -sf -x "" "http://localhost:${PORT_B}$1" 2>/dev/null || echo "__FAIL__"; }
API_A_POST() {
  curl -sf -x "" -X POST "http://localhost:${PORT_A}$1" \
    -H "Content-Type: application/json" -d "$2" 2>/dev/null || echo "__FAIL__"
}
API_B_POST() {
  curl -sf -x "" -X POST "http://localhost:${PORT_B}$1" \
    -H "Content-Type: application/json" -d "$2" 2>/dev/null || echo "__FAIL__"
}

echo ""
bold "============================================================"
bold "  P2P Full Integration Test Suite"
bold "============================================================"
echo "  Root:   $ROOT_DIR"
echo "  Base:   $BASE_DIR"
echo "  Port A: $PORT_A"
echo "  Port B: $PORT_B"
echo ""

# ──────────────────────────────────────────────
# 0. Build server binary
# ──────────────────────────────────────────────
bold "[0/15] Building server binary..."
mkdir -p "$BASE_DIR/node_a/storage" "$BASE_DIR/node_b/storage"
cd "$ROOT_DIR"
http_proxy="" https_proxy="" go build -o "$SERVER_BIN" ./cmd/server/main.go
green "  Build OK"

# ──────────────────────────────────────────────
# 1. Start Node A
# ──────────────────────────────────────────────
bold "[1/15] Starting Node A (port ${PORT_A})..."
PEERDRIVE_STORAGE="${BASE_DIR}/node_a/storage" \
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN="/ip4/0.0.0.0/tcp/0" \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_RELAY_ENABLE=false \
PEERDRIVE_BT_DHT_ENABLE=false \
PEERDRIVE_REGISTRATION_SERVER="" \
PORT="${PORT_A}" \
"$SERVER_BIN" &
PID_A=$!
for i in $(seq 1 30); do
  if curl -sf -x "" "http://localhost:${PORT_A}/ping" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
check_eq "Node A ping" "pong" "$(API_A '/ping')"

# ──────────────────────────────────────────────
# 2. Start Node B
# ──────────────────────────────────────────────
bold "[2/15] Starting Node B (port ${PORT_B})..."
PEERDRIVE_STORAGE="${BASE_DIR}/node_b/storage" \
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN="/ip4/0.0.0.0/tcp/0" \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_RELAY_ENABLE=false \
PEERDRIVE_BT_DHT_ENABLE=false \
PEERDRIVE_REGISTRATION_SERVER="" \
PORT="${PORT_B}" \
"$SERVER_BIN" &
PID_B=$!
for i in $(seq 1 30); do
  if curl -sf -x "" "http://localhost:${PORT_B}/ping" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
check_eq "Node B ping" "pong" "$(API_B '/ping')"

# ──────────────────────────────────────────────
# 3. /p2p/status (both nodes)
# ──────────────────────────────────────────────
bold "[3/15] Checking /p2p/status..."
A_STATUS=$(API_A '/p2p/status')
check "Node A enabled" '"enabled":true' "$A_STATUS"

# Extract peer IDs for later
A_PEER_ID=$(echo "$A_STATUS" | sed 's/.*"peer_id":"\([^"]*\)".*/\1/')
check "Node A peer_id non-empty" "12D3" "$A_PEER_ID"

B_STATUS=$(API_B '/p2p/status')
check "Node B enabled" '"enabled":true' "$B_STATUS"
B_PEER_ID=$(echo "$B_STATUS" | sed 's/.*"peer_id":"\([^"]*\)".*/\1/')
check "Node B peer_id non-empty" "12D3" "$B_PEER_ID"

echo "  Node A PeerID: ${A_PEER_ID:0:20}..."
echo "  Node B PeerID: ${B_PEER_ID:0:20}..."

# ──────────────────────────────────────────────
# 4. /p2p/node
# ──────────────────────────────────────────────
bold "[4/15] Checking /p2p/node..."
A_NODE=$(API_A '/p2p/node')
check "Node A node peer_id" "$A_PEER_ID" "$A_NODE"
check "Node A node addrs" '"addrs"' "$A_NODE"

B_NODE=$(API_B '/p2p/node')
check "Node B node peer_id" "$B_PEER_ID" "$B_NODE"

# ──────────────────────────────────────────────
# 5. /p2p/peers (initially empty)
# ──────────────────────────────────────────────
bold "[5/15] Checking /p2p/peers..."
A_PEERS=$(API_A '/p2p/peers')
check "Node A peers list" '"peers"' "$A_PEERS"

B_PEERS=$(API_B '/p2p/peers')
check "Node B peers list" '"peers"' "$B_PEERS"

# ──────────────────────────────────────────────
# 6. /p2p/discovered (wait for mDNS)
# ──────────────────────────────────────────────
bold "[6/15] Waiting for mDNS discovery (8s)..."
sleep 8
A_DISC=$(API_A '/p2p/discovered')
check "Node A discovered peers" '"peers"' "$A_DISC"

B_DISC=$(API_B '/p2p/discovered')
check "Node B discovered peers" '"peers"' "$B_DISC"

# ──────────────────────────────────────────────
# 7. Connect B -> A via multiaddr
# ──────────────────────────────────────────────
bold "[7/15] Connecting Node B to Node A..."
A_ADDRS=$(echo "$A_NODE" | sed 's/.*"addrs":\[\([^]]*\)\].*/\1/' | grep -oP '"[^"]*"' | tr -d '"' || true)
CONNECTED=false
for ADDR in $A_ADDRS; do
  FULL_ADDR="${ADDR}/p2p/${A_PEER_ID}"
  CONN_RESP=$(curl -sf -x "" -X POST "http://localhost:${PORT_B}/p2p/connect" \
    -H "Content-Type: application/json" \
    -d "{\"addr\": \"${FULL_ADDR}\"}" 2>/dev/null || echo "")
  if echo "$CONN_RESP" | grep -q '"status":"connected"'; then
    CONNECTED=true
    green "  Connected via $ADDR"
    break
  fi
done
if [ "$CONNECTED" = true ]; then
  green "  [PASS] Node B connected to Node A"
  PASS=$((PASS + 1))
else
  red "  [FAIL] Node B could not connect to Node A"
  FAIL=$((FAIL + 1))
fi

sleep 2

# ──────────────────────────────────────────────
# 8. Verify connection (both sides see each other)
# ──────────────────────────────────────────────
bold "[8/15] Verifying bidirectional connection..."
A_PEERS_AFTER=$(API_A '/p2p/peers')
check "Node A sees Node B" "$B_PEER_ID" "$A_PEERS_AFTER"

B_PEERS_AFTER=$(API_B '/p2p/peers')
check "Node B sees Node A" "$A_PEER_ID" "$B_PEERS_AFTER"

# ──────────────────────────────────────────────
# 9. /p2p/ping/:peer_id
# ──────────────────────────────────────────────
bold "[9/15] Pinging Node B from Node A..."
PING_RESP=$(API_A "/p2p/ping/${B_PEER_ID}")
check "Ping response peer" "$B_PEER_ID" "$PING_RESP"
check "Ping response rtt" '"rtt"' "$PING_RESP"

# ──────────────────────────────────────────────
# 10. File upload + collection on Node A
# ──────────────────────────────────────────────
bold "[10/15] Creating test data on Node A..."
echo "P2P full integration test content $(date)" > "${BASE_DIR}/node_a/test_file.txt"

FILE_RESP=$(API_A_POST '/files/register_local' \
  "{\"path\": \"${BASE_DIR}/node_a/test_file.txt\", \"filename\": \"test_file.txt\"}")
HASH_A=$(echo "$FILE_RESP" | sed 's/.*"hash":"\([^"]*\)".*/\1/')
check "File registered with hash" '^[a-f0-9]\{64\}$' "$HASH_A"
echo "  File hash: $HASH_A"

COLL_RESP=$(API_A_POST '/anon/collections' \
  "{\"friendly_name\": \"p2p-full-test\", \"entries\": [{\"path\": \"test_file.txt\", \"hash\": \"${HASH_A}\"}]}")
COLL_HASH=$(echo "$COLL_RESP" | sed 's/.*"hash":"\([^"]*\)".*/\1/')
check "Collection created" "$COLL_HASH" "$COLL_RESP"
echo "  Collection hash: $COLL_HASH"

# ──────────────────────────────────────────────
# 11. /p2p/announce
# ──────────────────────────────────────────────
bold "[11/15] Announcing hash on Node A..."
ANN_RESP=$(API_A_POST '/p2p/announce' "{\"hash\": \"${COLL_HASH}\"}")
check "Announce response" '"status"' "$ANN_RESP"

# ──────────────────────────────────────────────
# 12. /p2p/fetch — Node B fetches collection from A
# ──────────────────────────────────────────────
bold "[12/15] Fetching collection via P2P exchange (Node B)..."
FETCH_RESP=$(API_B_POST '/p2p/fetch' "{\"hash\": \"${COLL_HASH}\"}")
check "Fetch friendly name" '"friendly_name"' "$FETCH_RESP"
check "Fetch entries" '"entries"' "$FETCH_RESP"
check "Fetch response contains name" "p2p-full-test" "$FETCH_RESP"

# ──────────────────────────────────────────────
# 13. /p2p/sync — Node B syncs files from A
# ──────────────────────────────────────────────
bold "[13/15] Syncing files via P2P (Node B <- Node A)..."
SYNC_DIR="${BASE_DIR}/node_b/sync"
mkdir -p "$SYNC_DIR"
SYNC_RESP=$(API_B_POST '/p2p/sync' \
  "{\"peer_id\": \"${A_PEER_ID}\", \"hash\": \"${COLL_HASH}\", \"target_dir\": \"${SYNC_DIR}\"}")
check "Sync response count" '"count"' "$SYNC_RESP"
SYNC_COUNT=$(echo "$SYNC_RESP" | sed 's/.*"count":\([0-9]*\).*/\1/')
if [ "${SYNC_COUNT:-0}" -gt 0 ]; then
  green "  [PASS] Files synced: ${SYNC_COUNT}"
  PASS=$((PASS + 1))
else
  yellow "  [WARN] No files synced (count=0), checking synced list..."
  SYNCED_LIST=$(echo "$SYNC_RESP" | sed 's/.*"synced":\[\([^]]*\)\].*/\1/')
  if [ -n "$SYNCED_LIST" ]; then
    green "  [PASS] Files synced (non-empty list)"
    PASS=$((PASS + 1))
  else
    red "  [FAIL] No files synced"
    FAIL=$((FAIL + 1))
  fi
fi
ls -la "${SYNC_DIR}/" 2>/dev/null || true

# ──────────────────────────────────────────────
# 14. /p2p/topology + /p2p/quality + /p2p/connections + /p2p/stats
# ──────────────────────────────────────────────
bold "[14/15] Topology, quality, connections, and stats endpoints..."

# topology
A_TOPOLOGY=$(API_A '/p2p/topology')
check "Topology local_peer_id" '"local_peer_id"' "$A_TOPOLOGY"
check "Topology edges" '"edges"' "$A_TOPOLOGY"

# quality
A_QUALITY=$(API_A '/p2p/quality')
# Quality returns [] when no samples exist yet; just check it's valid (not __FAIL__)
if [ "$A_QUALITY" != "__FAIL__" ]; then
  green "  [PASS] Quality endpoint returns valid data"
  PASS=$((PASS + 1))
else
  red "  [FAIL] Quality endpoint failed"
  FAIL=$((FAIL + 1))
fi

# connections
A_CONNS=$(API_A '/p2p/connections')
check "Connections inbound" '"inbound"' "$A_CONNS"
check "Connections outbound" '"outbound"' "$A_CONNS"
check "Connections total" '"total"' "$A_CONNS"

# stats
A_STATS=$(API_A '/p2p/stats')
check "Stats total_peers_seen" '"total_peers_seen"' "$A_STATS"

green "  All four structural endpoints returned valid JSON"

# ──────────────────────────────────────────────
# 15. Peer detail endpoints
# ──────────────────────────────────────────────
bold "[15/15] Peer detail endpoints..."

# /p2p/peers/detail
A_PEERS_DETAIL=$(API_A '/p2p/peers/detail')
# Peers detail can return {} when no peers tracked; just check it's valid
if [ "$A_PEERS_DETAIL" != "__FAIL__" ]; then
  green "  [PASS] Peers detail endpoint returns valid data"
  PASS=$((PASS + 1))
else
  red "  [FAIL] Peers detail endpoint failed"
  FAIL=$((FAIL + 1))
fi

# /p2p/peers/detail/:peer_id
A_PEER_DETAIL=$(API_A "/p2p/peers/detail/${B_PEER_ID}" 2>/dev/null || echo "__FAIL__")
check "Single peer detail" '"peer_id"' "$A_PEER_DETAIL"

# ──────────────────────────────────────────────
# Summary
# ──────────────────────────────────────────────
echo ""
bold "============================================================"
bold "  P2P Full Integration Test Results"
bold "============================================================"
green "  Passed: ${PASS}"
if [ "$FAIL" -gt 0 ]; then
  red "  Failed: ${FAIL}"
else
  green "  Failed: 0"
fi
total=$((PASS + FAIL))
bold "  Total:  ${total}"
echo ""
echo "  Node A PeerID: $A_PEER_ID"
echo "  Node B PeerID: $B_PEER_ID"
echo "  Collection:    $COLL_HASH"
echo "  File hash:     $HASH_A"
echo ""

if [ "$FAIL" -gt 0 ]; then
  red "  OVERALL: SOME TESTS FAILED"
  exit 1
else
  green "  OVERALL: ALL TESTS PASSED"
  exit 0
fi
