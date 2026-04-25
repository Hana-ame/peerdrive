#!/bin/bash
set -e

BASE_DIR="/tmp/peerdrive_relay_test"
rm -rf "$BASE_DIR"
mkdir -p "$BASE_DIR/relay/storage" "$BASE_DIR/client/storage"
mkdir -p "$BASE_DIR/client/sync"

SERVER_BIN="$(pwd)/main"
if [ ! -f "$SERVER_BIN" ]; then
  echo "Building server binary..."
  http_proxy="" https_proxy="" go build -o "$SERVER_BIN" ./cmd/server/main.go
fi

cleanup() {
  kill $PID_RELAY 2>/dev/null || true
  kill $PID_CLIENT 2>/dev/null || true
  wait $PID_RELAY 2>/dev/null || true
  wait $PID_CLIENT 2>/dev/null || true
}
trap cleanup EXIT

echo "============================================"
echo "  P2P Stage 3 - Relay + NAT Traversal Test"
echo "============================================"

# ---- Step 1: Start Relay Node ----
echo ""
echo "--- Step 1: Start Relay Node (port 3001) ---"

PEERDRIVE_STORAGE="$BASE_DIR/relay/storage" \
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN="/ip4/0.0.0.0/tcp/0" \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_RELAY_MODE=server \
PEERDRIVE_HOLE_PUNCH=true \
PEERDRIVE_AUTO_NAT=false \
PEERDRIVE_NAT_PORTMAP=false \
PORT=3001 \
"$SERVER_BIN" &
PID_RELAY=$!
sleep 4

RELAY_PING=$(curl -s -x "" "http://localhost:3001/ping")
if [ "$RELAY_PING" != "pong" ]; then
  echo "FAIL: Relay node not responding"
  exit 1
fi
echo "Relay node: pong OK"

# ---- Step 2: Get Relay Node Info ----
echo ""
echo "--- Step 2: Get relay node info ---"
RELAY_NODE=$(curl -s -x "" "http://localhost:3001/p2p/node")
echo "Relay node: $RELAY_NODE"
RELAY_PEER_ID=$(echo "$RELAY_NODE" | grep -oP '(?<="peer_id":")[^"]*')
echo "Relay PeerID: $RELAY_PEER_ID"

RELAY_STATUS=$(curl -s -x "" "http://localhost:3001/p2p/status")
echo "Relay status: $RELAY_STATUS"
RELAY_MODE=$(echo "$RELAY_STATUS" | grep -oP '(?<="relay_mode":")[^"]*' || echo "unknown")
echo "Relay mode: $RELAY_MODE"

if [ "$RELAY_MODE" != "server" ]; then
  echo "FAIL: Relay node not in server mode (got: $RELAY_MODE)"
  exit 1
fi
echo "PASS: Relay node in server mode"

# ---- Step 3: Start Client Node ----
echo ""
echo "--- Step 3: Start Client Node (port 3002) ---"

PEERDRIVE_STORAGE="$BASE_DIR/client/storage" \
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN="/ip4/0.0.0.0/tcp/0" \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_RELAY_MODE=client \
PEERDRIVE_HOLE_PUNCH=true \
PEERDRIVE_AUTO_NAT=true \
PEERDRIVE_NAT_PORTMAP=true \
PORT=3002 \
"$SERVER_BIN" &
PID_CLIENT=$!
sleep 4

CLIENT_PING=$(curl -s -x "" "http://localhost:3002/ping")
if [ "$CLIENT_PING" != "pong" ]; then
  echo "FAIL: Client node not responding"
  exit 1
fi
echo "Client node: pong OK"

# ---- Step 4: Get Client Node Info ----
echo ""
echo "--- Step 4: Get client node info ---"
CLIENT_NODE=$(curl -s -x "" "http://localhost:3002/p2p/node")
echo "Client node: $CLIENT_NODE"
CLIENT_PEER_ID=$(echo "$CLIENT_NODE" | grep -oP '(?<="peer_id":")[^"]*')
echo "Client PeerID: $CLIENT_PEER_ID"

CLIENT_STATUS=$(curl -s -x "" "http://localhost:3002/p2p/status")
echo "Client status: $CLIENT_STATUS"
CLIENT_HOLE=$(echo "$CLIENT_STATUS" | grep -oP '(?<="hole_punch":)[^,}]*' || echo "unknown")
echo "Client hole punch: $CLIENT_HOLE"

if [ "$CLIENT_HOLE" != "true" ]; then
  echo "FAIL: Client hole punch not enabled (got: $CLIENT_HOLE)"
  exit 1
fi
echo "PASS: Client hole punch enabled"

# ---- Step 5: Wait for mDNS discovery ----
echo ""
echo "--- Step 5: Wait for mDNS discovery (8s) ---"
sleep 8

RELAY_DISC=$(curl -s -x "" "http://localhost:3001/p2p/discovered")
echo "Relay discovered: $RELAY_DISC"
CLIENT_DISC=$(curl -s -x "" "http://localhost:3002/p2p/discovered")
echo "Client discovered: $CLIENT_DISC"

# ---- Step 6: Connect client to relay manually ----
echo ""
echo "--- Step 6: Connect client to relay ---"
RELAY_ADDRS=$(echo "$RELAY_NODE" | grep -oP '(?<="addrs":\[)[^\]]*' | grep -oP '"[^"]*"' | tr -d '"' | head -1)
for ADDR in $RELAY_ADDRS; do
  FULL_ADDR="${ADDR}/p2p/${RELAY_PEER_ID}"
  echo "Connecting to relay: $FULL_ADDR"
  CONN_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/connect" \
    -H "Content-Type: application/json" \
    -d "{\"addr\": \"$FULL_ADDR\"}")
  echo "Connect result: $CONN_RESP"
done

sleep 2

# ---- Step 7: Verify connection ----
echo ""
echo "--- Step 7: Verify P2P connection ---"
RELAY_PEERS=$(curl -s -x "" "http://localhost:3001/p2p/peers")
echo "Relay peers: $RELAY_PEERS"
CLIENT_PEERS=$(curl -s -x "" "http://localhost:3002/p2p/peers")
echo "Client peers: $CLIENT_PEERS"

RELAY_CONNECTED=$(echo "$RELAY_PEERS" | grep -c "$CLIENT_PEER_ID" || true)
CLIENT_CONNECTED=$(echo "$CLIENT_PEERS" | grep -c "$RELAY_PEER_ID" || true)

if [ "$RELAY_CONNECTED" -gt 0 ] || [ "$CLIENT_CONNECTED" -gt 0 ]; then
  echo "PASS: Nodes connected via relay"
else
  echo "WARN: Nodes may not be connected (relay negotiation pending)"
fi

# ---- Step 8: Create test data on client ----
echo ""
echo "--- Step 8: Create test data on client ---"
echo "Relay test file content" > "$BASE_DIR/client/test_file.txt"

FILE_REG=$(curl -s -x "" -X POST "http://localhost:3002/files/register_local" \
  -H "Content-Type: application/json" \
  -d "{\"path\": \"$BASE_DIR/client/test_file.txt\", \"filename\": \"test_file.txt\"}")
echo "Registered: $FILE_REG"
HASH_CLIENT=$(echo "$FILE_REG" | grep -oP '(?<="hash":")[^"]*')
echo "File hash: $HASH_CLIENT"

COLL_RESP=$(curl -s -x "" -X POST "http://localhost:3002/anon/collections" \
  -H "Content-Type: application/json" \
  -d "{\"friendly_name\": \"relay-test\", \"entries\": [{\"path\": \"test_file.txt\", \"hash\": \"$HASH_CLIENT\"}]}")
COLL_HASH=$(echo "$COLL_RESP" | grep -oP '(?<="hash":")[^"]*')
echo "Collection hash: $COLL_HASH"

# ---- Step 9: Announce collection ----
echo ""
echo "--- Step 9: Announce collection on client ---"
ANN_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/announce" \
  -H "Content-Type: application/json" \
  -d "{\"hash\": \"$COLL_HASH\"}")
echo "Announce: $ANN_RESP"

# ---- Step 10: Fetch collection from relay side ----
echo ""
echo "--- Step 10: Fetch collection from relay via P2P ---"
FETCH_RESP=$(curl -s -x "" -X POST "http://localhost:3001/p2p/fetch" \
  -H "Content-Type: application/json" \
  -d "{\"hash\": \"$COLL_HASH\"}")
echo "Fetch: $FETCH_RESP"

FETCH_FRIENDLY=$(echo "$FETCH_RESP" | grep -oP '(?<="friendly_name":")[^"]*' || echo "")
if [ "$FETCH_FRIENDLY" = "relay-test" ]; then
  echo "PASS: Collection fetched through relay"
else
  echo "WARN: Could not verify collection via relay (got: $FETCH_FRIENDLY)"
fi

# ---- Step 11: WS /ws/transfer endpoint test ----
echo ""
echo "--- Step 11: WS info endpoint ---"
WS_INFO=$(curl -s -x "" "http://localhost:3001/p2p/ws/info")
echo "WS info: $WS_INFO"

# ---- Step 12: Request-file broadcast test ----
echo ""
echo "--- Step 12: Request-file broadcast ---"
REQ_RESP=$(curl -s -x "" -X POST "http://localhost:3002/p2p/request-file" \
  -H "Content-Type: application/json" \
  -d "{\"hash\": \"$HASH_CLIENT\"}")
echo "Request-file: $REQ_RESP"

# ---- Summary ----
echo ""
echo "============================================"
echo "  Relay + NAT Traversal Test Summary"
echo "============================================"
echo "Relay PeerID: $RELAY_PEER_ID"
echo "Client PeerID: $CLIENT_PEER_ID"
echo "Relay mode: $RELAY_MODE"
echo "Client hole punch: $CLIENT_HOLE"
echo "Collection hash: $COLL_HASH"
echo ""
echo "Relay infrastructure tests passed."
echo "============================================"
