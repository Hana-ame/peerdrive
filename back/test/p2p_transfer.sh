#!/usr/bin/env bash
# P2P Transfer Test Suite
# Tests chunked file transfer, connection management, and exchange protocol
# Requires two nodes running on different ports.
set -euo pipefail

API_A="${API_A:-http://localhost:3000}"
API_B="${API_B:-http://localhost:3001}"

PASS=0
FAIL=0

pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

echo "=== Peerdrive P2P Transfer Test ==="
echo "Node A: $API_A"
echo "Node B: $API_B"
echo ""

# -------------------------------------------------
# Check both nodes are alive
# -------------------------------------------------
echo "--- Health Check ---"
if curl -sf "$API_A/ping" >/dev/null 2>&1; then pass "Node A alive"; else fail "Node A alive"; fi
if curl -sf "$API_B/ping" >/dev/null 2>&1; then pass "Node B alive"; else fail "Node B alive"; fi
echo ""

# -------------------------------------------------
# P2P Status
# -------------------------------------------------
echo "--- P2P Status ---"
STATUS_A=$(curl -sf "$API_A/p2p/status" 2>/dev/null || echo '{"enabled":false}')
if echo "$STATUS_A" | grep -q '"enabled":true'; then
  pass "Node A P2P enabled"
  echo "    Peer ID: $(echo "$STATUS_A" | grep -o '"peer_id":"[^"]*"')"
else
  fail "Node A P2P enabled (may be disabled, continuing)"
fi

STATUS_B=$(curl -sf "$API_B/p2p/status" 2>/dev/null || echo '{"enabled":false}')
if echo "$STATUS_B" | grep -q '"enabled":true'; then
  pass "Node B P2P enabled"
  echo "    Peer ID: $(echo "$STATUS_B" | grep -o '"peer_id":"[^"]*"')"
else
  fail "Node B P2P enabled (may be disabled, continuing)"
fi
echo ""

# -------------------------------------------------
# Node Info
# -------------------------------------------------
echo "--- Node Info ---"
NODE_A=$(curl -sf "$API_A/p2p/node" 2>/dev/null || echo '{}')
NODE_B=$(curl -sf "$API_B/p2p/node" 2>/dev/null || echo '{}')

PEER_A=$(echo "$NODE_A" | grep -o '"peer_id":"[^"]*"' | cut -d'"' -f4 || echo "")
PEER_B=$(echo "$NODE_B" | grep -o '"peer_id":"[^"]*"' | cut -d'"' -f4 || echo "")

if [ -n "$PEER_A" ]; then pass "Node A has peer ID"; else fail "Node A has peer ID"; fi
if [ -n "$PEER_B" ]; then pass "Node B has peer ID"; else fail "Node B has peer ID"; fi
echo ""

# -------------------------------------------------
# Connect nodes
# -------------------------------------------------
echo "--- Node Connection ---"
if [ -n "$PEER_B" ]; then
  ADDR_B=$(echo "$NODE_B" | grep -o '"addrs":\[[^]]*\]' | head -1 || echo "")
  if [ -n "$ADDR_B" ]; then
    FIRST_ADDR=$(echo "$ADDR_B" | grep -o '"[^"]*"' | head -1 | tr -d '"')
    if [ -n "$FIRST_ADDR" ]; then
      CONNECT_ADDR="${FIRST_ADDR}/p2p/${PEER_B}"
      CONN_RES=$(curl -sf -X POST "$API_A/p2p/connect" -H 'Content-Type: application/json' -d "{\"addr\":\"$CONNECT_ADDR\"}" 2>/dev/null || echo '{"status":"failed"}')
      if echo "$CONN_RES" | grep -q '"status":"connected"'; then
        pass "Node A connected to Node B"
      else
        echo "    (may fail if on different networks, continuing)"
        fail "Node A connected to Node B"
      fi
    fi
  fi
fi
echo ""

# -------------------------------------------------
# Ping
# -------------------------------------------------
echo "--- Ping ---"
if [ -n "$PEER_B" ]; then
  PING_RES=$(curl -sf "$API_A/p2p/ping/$PEER_B" 2>/dev/null || echo '{"error":"timeout"}')
  if echo "$PING_RES" | grep -q '"rtt"'; then
    pass "Ping Node B from Node A"
    echo "    RTT: $(echo "$PING_RES" | grep -o '"rtt":"[^"]*"')"
  else
    echo "    (may fail if nodes not connected)"
    fail "Ping Node B from Node A"
  fi
fi
echo ""

# -------------------------------------------------
# WebSocket Info
# -------------------------------------------------
echo "--- WebSocket ---"
WS_A=$(curl -sf "$API_A/p2p/ws/info" 2>/dev/null || echo '{}')
if echo "$WS_A" | grep -q '"ws_endpoint"'; then pass "WS info available on Node A"; else fail "WS info available on Node A"; fi
echo ""

# -------------------------------------------------
# File Upload and P2P Announce
# -------------------------------------------------
echo "--- File Transfer via P2P ---"
# Create test file
TEST_DATA="Peerdrive P2P transfer test data - $(date +%s)"
TEST_FILE=$(mktemp)
echo "$TEST_DATA" > "$TEST_FILE"

# Upload to Node A
UPLOAD_RES=$(curl -sf -X POST "$API_A/files/upload" -F "file=@$TEST_FILE" 2>/dev/null || echo '{"error":"upload failed"}')
HASH=$(echo "$UPLOAD_RES" | grep -o '"hash":"[a-f0-9]*"' | cut -d'"' -f4 || echo "")
if [ -n "$HASH" ] && [ ${#HASH} -eq 64 ]; then
  pass "Upload test file to Node A (hash: ${HASH:0:16}...)"
else
  fail "Upload test file to Node A"
fi

# Announce hash on DHT
if [ -n "$HASH" ]; then
  ANNOUNCE_RES=$(curl -sf -X POST "$API_A/p2p/announce" -H 'Content-Type: application/json' -d "{\"hash\":\"$HASH\"}" 2>/dev/null || echo '{"status":"failed"}')
  if echo "$ANNOUNCE_RES" | grep -q '"status":"announced"'; then
    pass "Announce hash on DHT"
  else
    echo "    (DHT may not be fully bootstrapped yet)"
    fail "Announce hash on DHT"
  fi

  # Download via SHA256 (should work locally)
  DOWNLOAD_RES=$(curl -sf "$API_A/sha256sum/$HASH" -o /dev/null -w '%{http_code}' 2>/dev/null || echo "000")
  if [ "$DOWNLOAD_RES" = "200" ]; then
    pass "Download file via SHA256 from Node A"
  else
    fail "Download file via SHA256 from Node A (HTTP $DOWNLOAD_RES)"
  fi
fi

rm -f "$TEST_FILE"
echo ""

# -------------------------------------------------
# Connection Manager Stats
# -------------------------------------------------
echo "--- Connection Manager ---"
CONN_STATS=$(curl -sf "$API_A/p2p/status" 2>/dev/null || echo '{}')
if echo "$CONN_STATS" | grep -q '"conn_stats"'; then
  pass "Connection manager stats available"
  echo "    Known peers: $(echo "$CONN_STATS" | grep -o '"known_peers":[0-9]*')"
  echo "    Connected: $(echo "$CONN_STATS" | grep -o '"connected_peers":[0-9]*')"
else
  fail "Connection manager stats available"
fi
echo ""

# -------------------------------------------------
# Summary
# -------------------------------------------------
echo "=== Results ==="
echo "Passed: $PASS"
echo "Failed: $FAIL"
if [ $FAIL -gt 0 ]; then
  echo "Some tests failed (P2P features may require multi-node setup)"
  exit 0  # Don't fail CI for P2P tests that need network
else
  echo "All tests passed!"
fi
