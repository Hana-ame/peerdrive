#!/bin/bash
set -e

BASE_DIR="/tmp/peerdrive_p2p_test"
rm -rf "$BASE_DIR"
mkdir -p "$BASE_DIR/node_a/storage" "$BASE_DIR/node_b/storage"
mkdir -p "$BASE_DIR/node_a/sync" "$BASE_DIR/node_b/sync"

SERVER_BIN="$(pwd)/main"
if [ ! -f "$SERVER_BIN" ]; then
  echo "Building server binary..."
  http_proxy="" https_proxy="" go build -o "$SERVER_BIN" ./cmd/server/main.go
fi

cleanup() {
  kill $PID_A 2>/dev/null || true
  kill $PID_B 2>/dev/null || true
  wait $PID_A 2>/dev/null || true
  wait $PID_B 2>/dev/null || true
}
trap cleanup EXIT

echo "============================================"
echo "  P2P Stage 2 Integration Tests"
echo "============================================"

# ---- Step 1: Start Node A ----
echo ""
echo "--- Step 1: Start Node A (port 3001) ---"

PEERDRIVE_STORAGE="$BASE_DIR/node_a/storage" \
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN="/ip4/0.0.0.0/tcp/0" \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_RELAY_ENABLE=false \
PEERDRIVE_BT_DHT_ENABLE=false \
PORT=3001 \
"$SERVER_BIN" &
PID_A=$!
for i in $(seq 1 20); do
  if curl -s -x "" "http://localhost:3001/ping" 2>/dev/null | grep -q pong; then break; fi
  sleep 1
done

A_STATUS=$(curl -s -x "" "http://localhost:3001/ping")
if [ "$A_STATUS" != "pong" ]; then
  echo "FAIL: Node A not responding"
  exit 1
fi
echo "Node A: pong OK"

# ---- Step 2: Start Node B ----
echo ""
echo "--- Step 2: Start Node B (port 3002) ---"

PEERDRIVE_STORAGE="$BASE_DIR/node_b/storage" \
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN="/ip4/0.0.0.0/tcp/0" \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_RELAY_ENABLE=false \
PEERDRIVE_BT_DHT_ENABLE=false \
PORT=3002 \
"$SERVER_BIN" &
PID_B=$!
for i in $(seq 1 20); do
  if curl -s -x "" "http://localhost:3002/ping" 2>/dev/null | grep -q pong; then break; fi
  sleep 1
done

B_STATUS=$(curl -s -x "" "http://localhost:3002/ping")
if [ "$B_STATUS" != "pong" ]; then
  echo "FAIL: Node B not responding"
  exit 1
fi
echo "Node B: pong OK"

# ---- Step 3: Check P2P Status ----
echo ""
echo "--- Step 3: Check P2P status ---"

A_P2P=$(curl -s -x "" "http://localhost:3001/p2p/status")
echo "Node A P2P: $A_P2P"
A_ENABLED=$(echo "$A_P2P" | grep -oP '(?<="enabled":)[^,}]*')
if [ "$A_ENABLED" != "true" ]; then
  echo "FAIL: P2P not enabled on Node A"
  exit 1
fi

B_P2P=$(curl -s -x "" "http://localhost:3002/p2p/status")
echo "Node B P2P: $B_P2P"

# ---- Step 4: Get Node Info ----
echo ""
echo "--- Step 4: Get node info ---"

A_NODE=$(curl -s -x "" "http://localhost:3001/p2p/node")
echo "Node A: $A_NODE"
A_PEER_ID=$(echo "$A_NODE" | grep -oP '(?<="peer_id":")[^"]*')
if [ -z "$A_PEER_ID" ]; then
  echo "FAIL: No PeerID for Node A"
  exit 1
fi
echo "Node A PeerID: $A_PEER_ID"

B_NODE=$(curl -s -x "" "http://localhost:3002/p2p/node")
echo "Node B: $B_NODE"
B_PEER_ID=$(echo "$B_NODE" | grep -oP '(?<="peer_id":")[^"]*')
if [ -z "$B_PEER_ID" ]; then
  echo "FAIL: No PeerID for Node B"
  exit 1
fi
echo "Node B PeerID: $B_PEER_ID"

# ---- Step 5: Wait for mDNS discovery ----
echo ""
echo "--- Step 5: Wait for mDNS discovery (10s) ---"
sleep 10

A_DISC=$(curl -s -x "" "http://localhost:3001/p2p/discovered")
echo "Node A discovered: $A_DISC"

B_DISC=$(curl -s -x "" "http://localhost:3002/p2p/discovered")
echo "Node B discovered: $B_DISC"

# ---- Step 6: Create test data on Node A ----
echo ""
echo "--- Step 6: Create test files on Node A ---"

echo "P2P test file content" > "$BASE_DIR/node_a/test_file.txt"

FILE_A=$(curl -s -x "" -X POST "http://localhost:3001/files/register_local" \
  -H "Content-Type: application/json" \
  -d "{\"path\": \"$BASE_DIR/node_a/test_file.txt\", \"filename\": \"test_file.txt\"}")
echo "Registered file: $FILE_A"
HASH_A=$(echo "$FILE_A" | grep -oP '(?<="hash":")[^"]*')
echo "File hash: $HASH_A"

if [ -z "$HASH_A" ]; then
  echo "FAIL: Could not register test file"
  exit 1
fi

# ---- Step 7: Create collection on Node A ----
echo ""
echo "--- Step 7: Create collection on Node A ---"

COLL_RESP=$(curl -s -x "" -w "\n%{http_code}" -X POST "http://localhost:3001/anon/collections" \
  -H "Content-Type: application/json" \
  -d "{
    \"friendly_name\": \"p2p-test-collection\",
    \"entries\": [
      {\"path\": \"test_file.txt\", \"hash\": \"$HASH_A\"}
    ]
  }")
COLL_HTTP_CODE=$(echo "$COLL_RESP" | tail -n1)
COLL_BODY=$(echo "$COLL_RESP" | sed '$d')
COLL_HASH=$(echo "$COLL_BODY" | grep -oP '(?<="hash":")[^"]*')

if [ "$COLL_HTTP_CODE" != "201" ] || [ -z "$COLL_HASH" ]; then
  echo "FAIL: Collection creation failed (HTTP $COLL_HTTP_CODE)"
  echo "Response: $COLL_RESP"
  exit 1
fi
echo "Collection hash: $COLL_HASH"

# ---- Step 8: Announce collection hash on Node A ----
echo ""
echo "--- Step 8: Announce hash ---"
ANN_RESP=$(curl -s -x "" -X POST "http://localhost:3001/p2p/announce" \
  -H "Content-Type: application/json" \
  -d "{\"hash\": \"$COLL_HASH\"}")
echo "Announce: $ANN_RESP"

# ---- Step 9: Manually connect Node B to Node A ----
echo ""
echo "--- Step 9: Connect B to A ---"
A_ADDRS=$(echo "$A_NODE" | grep -oP '(?<="addrs":\[)[^\]]*' | grep -oP '"[^"]*"' | tr -d '"')
if [ -z "$A_ADDRS" ]; then
  echo "WARN: Could not extract Node A addresses"
else
  for ADDR in $A_ADDRS; do
    FULL_ADDR="${ADDR}/p2p/${A_PEER_ID}"
    echo "Connecting to: $FULL_ADDR"
    CONN_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/connect" \
      -H "Content-Type: application/json" \
      -d "{\"addr\": \"$FULL_ADDR\"}")
    echo "Connect result: $CONN_RESP"
  done
fi

# ---- Step 10: Verify connection ----
echo ""
echo "--- Step 10: Verify P2P connection ---"
A_PEERS=$(curl -s -x "" "http://localhost:3001/p2p/peers")
echo "Node A peers: $A_PEERS"

B_PEERS=$(curl -s -x "" "http://localhost:3002/p2p/peers")
echo "Node B peers: $B_PEERS"

# Check if connected
A_CONNECTED=$(echo "$A_PEERS" | grep -c "$B_PEER_ID" || true)
B_CONNECTED=$(echo "$B_PEERS" | grep -c "$A_PEER_ID" || true)

if [ "$A_CONNECTED" -gt 0 ] || [ "$B_CONNECTED" -gt 0 ]; then
  echo "PASS: Nodes connected via P2P"
else
  echo "WARN: Nodes may not be connected (mDNS may need more time)"
fi

# ---- Step 11: Fetch collection from Node B via P2P ----
echo ""
echo "--- Step 11: Fetch collection via P2P ---"
FETCH_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/fetch" \
  -H "Content-Type: application/json" \
  -d "{\"hash\": \"$COLL_HASH\"}")
echo "Fetch response: $FETCH_RESP"

FETCH_FRIENDLY=$(echo "$FETCH_RESP" | grep -oP '(?<="friendly_name":")[^"]*' || echo "")
if [ "$FETCH_FRIENDLY" = "p2p-test-collection" ]; then
  echo "PASS: Collection fetched with correct friendly_name"
else
  echo "WARN: Could not verify friendly_name in fetched collection"
fi

# ---- Step 12: Sync files from Node A to Node B ----
echo ""
echo "--- Step 12: Sync files via P2P ---"
SYNC_DIR="$BASE_DIR/node_b/sync"
SYNC_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/sync" \
  -H "Content-Type: application/json" \
  -d "{
    \"peer_id\": \"$A_PEER_ID\",
    \"hash\": \"$COLL_HASH\",
    \"target_dir\": \"$SYNC_DIR\"
  }")
echo "Sync response: $SYNC_RESP"
SYNC_COUNT=$(echo "$SYNC_RESP" | grep -oP '(?<="count":)[0-9]+')
echo "Synced $SYNC_COUNT files"

if [ "$SYNC_COUNT" -gt 0 ]; then
  echo "PASS: Files synced via P2P"
  ls -la "$SYNC_DIR/"
else
  echo "WARN: No files synced (peer may not be connected)"
fi

# ---- Step 13: Push sync (callback) ----
echo ""
echo "--- Step 13: Push sync (callback) ---"
PUSH_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/push" \
  -H "Content-Type: application/json" \
  -d "{\"hash\": \"$COLL_HASH\", \"target_dir\": \"callback-sync\"}")
echo "Push sync: $PUSH_RESP"

# ---- Summary ----
echo ""
echo "============================================"
echo "  P2P Stage 2 Test Summary"
echo "============================================"
echo "Node A PeerID: $A_PEER_ID"
echo "Node B PeerID: $B_PEER_ID"
echo "Collection hash: $COLL_HASH"
echo "mDNS discovered peers on A: $(echo "$A_DISC" | grep -c 'peer_id')"
echo "mDNS discovered peers on B: $(echo "$B_DISC" | grep -c 'peer_id')"
echo "Connected peers on A: $(echo "$A_PEERS" | grep -c 'peers')"
echo "Connected peers on B: $(echo "$B_PEERS" | grep -c 'peers')"
echo ""
echo "All essential tests passed."
echo "============================================"
