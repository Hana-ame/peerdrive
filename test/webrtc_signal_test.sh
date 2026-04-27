#!/usr/bin/env bash
# WebRTC Signaling Server Test
# Tests WebSocket signaling hub: register, peer discovery, file announce/find
set -euo pipefail

API="${API:-http://localhost:3000}"
WS_API="${WS_API:-ws://localhost:3000}"
PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

echo "=== WebRTC Signaling Test ==="
echo "Server: $API"
echo ""

# Check signaling endpoint exists
echo "--- HTTP Endpoint Check ---"
STATUS=$(curl -sI --noproxy '*' "$API/p2p/status" 2>/dev/null | head -1 || echo "FAIL")
if echo "$STATUS" | grep -q "200"; then
  pass "API online"
else
  fail "API online"
fi

# Check signal_peers in P2P status
SIGNAL=$(curl -s --noproxy '*' "$API/p2p/status" 2>/dev/null || echo '{}')
if echo "$SIGNAL" | grep -q "signal_peers"; then
  pass "signal_peers in P2P status"
  echo "    Signal peers: $(echo "$SIGNAL" | grep -o '"signal_peers":[0-9]*')"
else
  fail "signal_peers in P2P status"
fi
echo ""

echo "--- WebSocket Signaling Protocol Test ---"
# Use python to test WS signaling
PYTEST=$(python3 << 'PYEOF'
import asyncio, json, sys
try:
    import websockets
except ImportError:
    print("SKIP: websockets not installed (pip install websockets)")
    sys.exit(0)

async def test():
    results = []
    try:
        ws1 = await asyncio.wait_for(
            websockets.connect("ws://localhost:3000/ws/signal"), timeout=5)
        # Register peer 1
        await ws1.send(json.dumps({"type": "register", "peer_id": "test-peer-1"}))
        resp = await asyncio.wait_for(ws1.recv(), timeout=3)
        data = json.loads(resp)
        results.append(("register", data.get("type") == "registered"))

        # Request peers
        await ws1.send(json.dumps({"type": "request_peers"}))
        resp = await asyncio.wait_for(ws1.recv(), timeout=3)
        data = json.loads(resp)
        results.append(("request_peers", data.get("type") == "peers"))

        # Announce file
        await ws1.send(json.dumps({"type": "announce_file", "peer_id": "test-peer-1", "hash": "a"*64}))
        await asyncio.sleep(0.3)

        # Find file
        await ws1.send(json.dumps({"type": "find_file", "hash": "a"*64}))
        resp = await asyncio.wait_for(ws1.recv(), timeout=3)
        data = json.loads(resp)
        results.append(("find_file", data.get("type") == "file_providers"))

        # Connect peer 2
        ws2 = await asyncio.wait_for(
            websockets.connect("ws://localhost:3000/ws/signal"), timeout=5)
        await ws2.send(json.dumps({"type": "register", "peer_id": "test-peer-2"}))
        await asyncio.wait_for(ws2.recv(), timeout=3)

        # Peer 1 should get peer_joined notification
        resp = await asyncio.wait_for(ws1.recv(), timeout=3)
        data = json.loads(resp)
        results.append(("peer_joined", data.get("type") == "peer_joined"))

        # Offer/Answer exchange
        await ws1.send(json.dumps({"type": "offer", "to": "test-peer-2", "peer_id": "test-peer-1", "sdp": "v=0"}))
        resp = await asyncio.wait_for(ws2.recv(), timeout=3)
        data2 = json.loads(resp)
        results.append(("offer_relay", data2.get("type") == "offer"))

        await ws1.close()
        await ws2.close()
    except Exception as e:
        results.append(("error", False, str(e)[:80]))

    for name, passed, *extra in results:
        status = "PASS" if passed else "FAIL"
        detail = extra[0] if extra else ""
        print(f"RESULT:{name}:{status}:{detail}")

asyncio.run(test())
PYEOF
)
echo "$PYTEST"
echo "$PYTEST" | grep "RESULT:" | while read line; do
    name=$(echo "$line" | cut -d: -f2)
    status=$(echo "$line" | cut -d: -f3)
    detail=$(echo "$line" | cut -d: -f4-)
    if [ "$status" = "PASS" ]; then pass "$name"; else fail "$name ($detail)"; fi
done
echo ""

echo "=== Results: $PASS pass, $FAIL fail ==="
[ $FAIL -eq 0 ] && echo "✅ ALL PASS" || echo "❌ SOME FAILED"
exit $FAIL
