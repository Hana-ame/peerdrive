#!/usr/bin/env bash
# WebRTC Signaling Server Test Suite
# Tests WebSocket signaling hub: register, peer discovery, file announce/find,
# room management, SDP offer/answer relay, ICE candidate relay.
set -euo pipefail

API="${API:-http://localhost:3000}"
PASS=0; FAIL=0
pass() { echo "  [PASS] $1"; PASS=$((PASS+1)); }
fail() { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }

echo "============================================"
echo "  WebRTC Signaling Test Suite"
echo "  Server: $API"
echo "  Date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "============================================"
echo ""

# ── 1. HTTP Endpoint Checks ──────────────────────────────────────
echo "─── 1. HTTP Endpoint Checks ───"

STATUS=$(curl -s --noproxy '*' "$API/p2p/status" 2>/dev/null || echo "{}")
if [ -n "$STATUS" ] && [ "$STATUS" != "{}" ]; then
  pass "API online (GET /p2p/status)"
else
  fail "API online"
fi

if echo "$STATUS" | grep -q "signal_peers"; then
  SP=$(echo "$STATUS" | python3 -c "import sys,json; print(json.load(sys.stdin).get('signal_peers','?'))" 2>/dev/null)
  pass "signal_peers in P2P status (value=$SP)"
else
  fail "signal_peers in P2P status"
fi

WEBRTC_INFO=$(curl -s --noproxy '*' "$API/p2p/webrtc/info" 2>/dev/null || echo "{}")
if echo "$WEBRTC_INFO" | grep -q "stun_server"; then
  STUN=$(echo "$WEBRTC_INFO" | python3 -c "import sys,json; print(json.load(sys.stdin).get('stun_server','?'))" 2>/dev/null)
  pass "WebRTC info endpoint (stun=$STUN)"
else
  fail "WebRTC info endpoint"
fi

echo ""

# ── 2. WebSocket Signaling Protocol Tests ────────────────────────
echo "─── 2. WebSocket Signaling Protocol Tests ───"

RESULTS_FILE=$(mktemp)
# Note: avoid asyncio.wait_for(ws.recv(), ...) with timeout — it can corrupt
# the websocket internal state in websockets 12 on cancellation.
python3 << 'PYEOF' > "$RESULTS_FILE" 2>&1
import asyncio, json, sys

results = []

async def ws_connect(path="/ws/signal"):
    import websockets
    return await asyncio.wait_for(
        websockets.connect(f"ws://localhost:3000{path}"), timeout=5)

async def send_expect(ws, msg, expect_type, timeout=5):
    """Send a message and verify the response type."""
    await ws.send(json.dumps(msg))
    resp = await asyncio.wait_for(ws.recv(), timeout=timeout)
    data = json.loads(resp)
    return data.get("type") == expect_type, data

async def recv_or_fail(ws, label="", timeout=4):
    """Receive a message with timeout. Raises on timeout."""
    resp = await asyncio.wait_for(ws.recv(), timeout=timeout)
    return json.loads(resp)

async def safe_close(ws):
    try:
        await ws.close()
    except Exception:
        pass

async def run():
    # ── 2a: Register + request_peers ──
    try:
        ws = await ws_connect()
        ok, _ = await send_expect(ws, {"type": "register", "peer_id": "a-p1"}, "registered")
        results.append(("2a-register", ok, ""))
        ok, d = await send_expect(ws, {"type": "request_peers"}, "peers")
        results.append(("2a-request_peers", ok, f"count={len(d.get('peers',[]))}"))
        await ws.close()
    except Exception as e:
        results.append(("2a", False, str(e)[:80]))

    # ── 2b: Room join + notification + room_peers ──
    try:
        w1 = await ws_connect()
        w2 = await ws_connect()
        ok, _ = await send_expect(w1, {"type": "join", "peer_id": "b-p1", "hash": "bb"*32}, "room_joined")
        results.append(("2b-join_p1", ok, ""))
        ok, d = await send_expect(w2, {"type": "join", "peer_id": "b-p2", "hash": "bb"*32}, "room_joined")
        results.append(("2b-join_p2", ok, f"room_peers={d.get('peers',[])}"))
        # w1 gets peer_joined_room
        n = await recv_or_fail(w1, "2b-notify")
        results.append(("2b-notify", n.get("type")=="peer_joined_room", f"peer={n.get('peer_id','')}"))
        ok, d = await send_expect(w1, {"type": "room_peers", "hash": "bb"*32}, "room_peers")
        pcount = len(d.get("peers", []))
        results.append(("2b-room_peers", ok and pcount == 2, f"count={pcount}"))
        await safe_close(w1); await safe_close(w2)
    except Exception as e:
        results.append(("2b", False, str(e)[:80]))

    # ── 2c: SDP Offer/Answer relay ──
    # Flow: w1 register -> "registered"; w2 register -> "registered"
    #        w1 gets "peer_joined" from w2's registration broadcast
    #        w1 sends offer -> w2 gets "offer"
    #        w2 sends answer -> w1 gets "answer"
    try:
        w1 = await ws_connect(); w2 = await ws_connect()
        ok, _ = await send_expect(w1, {"type": "register", "peer_id": "c-p1"}, "registered")
        results.append(("2c-register1", ok, ""))
        ok, _ = await send_expect(w2, {"type": "register", "peer_id": "c-p2"}, "registered")
        results.append(("2c-register2", ok, ""))
        # w1 gets "peer_joined" from w2's broadcast — consume it
        notif = await recv_or_fail(w1, "2c-notify")
        results.append(("2c-notify", notif.get("type") == "peer_joined", f"peer={notif.get('peer_id','')}"))
        # w1 sends offer to w2
        await w1.send(json.dumps({"type":"offer","to":"c-p2","peer_id":"c-p1","sdp":"v=0"}))
        r = await recv_or_fail(w2, "2c-offer")
        ok = r.get("type")=="offer" and r.get("from")=="c-p1"
        results.append(("2c-offer", ok, f"from={r.get('from','')}"))
        # w2 sends answer to w1
        await w2.send(json.dumps({"type":"answer","to":"c-p1","peer_id":"c-p2","sdp":"v=0"}))
        r = await recv_or_fail(w1, "2c-answer")
        ok = r.get("type")=="answer" and r.get("from")=="c-p2"
        results.append(("2c-answer", ok, f"from={r.get('from','')}"))
        await safe_close(w1); await safe_close(w2)
    except Exception as e:
        results.append(("2c", False, str(e)[:80]))

    # ── 2d: ICE candidate relay (legacy + shorthand) ──
    # Flow: w1 register, w2 register, w1 gets peer_joined, then test ice
    try:
        w1 = await ws_connect(); w2 = await ws_connect()
        await send_expect(w1, {"type": "register", "peer_id": "d-p1"}, "registered")
        await send_expect(w2, {"type": "register", "peer_id": "d-p2"}, "registered")
        # consume peer_joined
        await recv_or_fail(w1, "2d-notify")
        # Legacy "ice_candidate" type
        await w1.send(json.dumps({"type":"ice_candidate","to":"d-p2","peer_id":"d-p1","candidate":"c1:host"}))
        r = await recv_or_fail(w2, "2d-ice_legacy")
        ok = r.get("type")=="ice_candidate" and r.get("from")=="d-p1"
        results.append(("2d-ice_legacy", ok, f"from={r.get('from','')}"))
        # "ice" shorthand type
        await w2.send(json.dumps({"type":"ice","to":"d-p1","peer_id":"d-p2","candidate":"c2:srflx"}))
        r = await recv_or_fail(w1, "2d-ice_shorthand")
        ok = r.get("type")=="ice_candidate" and r.get("from")=="d-p2"
        results.append(("2d-ice_shorthand", ok, f"from={r.get('from','')}"))
        await safe_close(w1); await safe_close(w2)
    except Exception as e:
        results.append(("2d", False, str(e)[:80]))

    # ── 2e: File announce + find ──
    try:
        w1 = await ws_connect(); w2 = await ws_connect()
        await send_expect(w1, {"type": "register", "peer_id": "e-p1"}, "registered")
        await send_expect(w2, {"type": "register", "peer_id": "e-p2"}, "registered")
        # consume peer_joined on w1 (from w2's registration broadcast)
        await recv_or_fail(w1, "2e-notify")
        fh = "e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1"
        await w1.send(json.dumps({"type":"announce_file","peer_id":"e-p1","hash":fh}))
        await asyncio.sleep(0.3)
        ok, d = await send_expect(w2, {"type":"find_file","hash":fh}, "file_providers")
        results.append(("2e-find_existing", ok and "e-p1" in d.get("peers",[]), f"providers={d.get('peers',[])}"))
        ok, d = await send_expect(w2, {"type":"find_file","hash":"00"*32}, "file_providers")
        results.append(("2e-find_missing", ok and len(d.get("peers",[]))==0, f"providers={d.get('peers',[])}"))
        await safe_close(w1); await safe_close(w2)
    except Exception as e:
        results.append(("2e", False, str(e)[:80]))

    # ── 2f: Direct messaging ──
    try:
        w1 = await ws_connect(); w2 = await ws_connect()
        await send_expect(w1, {"type": "register", "peer_id": "f-p1"}, "registered")
        await send_expect(w2, {"type": "register", "peer_id": "f-p2"}, "registered")
        await recv_or_fail(w1, "2f-notify")  # consume peer_joined
        await w1.send(json.dumps({"type":"direct_message","to":"f-p2","peer_id":"f-p1","message":"hello"}))
        r = await recv_or_fail(w2, "2f-direct_msg")
        ok = r.get("type")=="direct_message" and r.get("from")=="f-p1"
        results.append(("2f-direct_msg", ok, f"msg={r.get('message','')}"))
        await safe_close(w1); await safe_close(w2)
    except Exception as e:
        results.append(("2f", False, str(e)[:80]))

    # ── 2g: 3-peer room ──
    try:
        all_ws = []
        for i in range(3):
            w = await ws_connect()
            await send_expect(w, {"type":"join","peer_id":f"g-p{i}","hash":"gg"*32},"room_joined")
            all_ws.append(w)
            # Each previously joined peer gets peer_joined_room for the new peer
            for j in range(i):
                await recv_or_fail(all_ws[j], f"2g-notify-g-p{i}")
        ok, d = await send_expect(all_ws[2], {"type":"room_peers","hash":"gg"*32},"room_peers")
        pcount = len(d.get("peers", []))
        results.append(("2g-three_peer_room", ok and pcount == 3, f"count={pcount}"))
        for w in all_ws:
            await safe_close(w)
    except Exception as e:
        results.append(("2g", False, str(e)[:80]))

    # ── 2h: Room isolation ──
    # Using "join" not "register", so no global peer_joined broadcast.
    # wb joins a different room, wa should NOT receive any cross-room notification.
    try:
        wa = await ws_connect(); wb = await ws_connect()
        ok, _ = await send_expect(wa, {"type":"join","peer_id":"h-p1","hash":"alpha1"},"room_joined")
        results.append(("2h-join_a", ok, ""))
        ok, _ = await send_expect(wb, {"type":"join","peer_id":"h-p2","hash":"beta2"},"room_joined")
        results.append(("2h-join_b", ok, ""))
        # No cross-room notification expected — wa's buffer is empty
        ok, d = await send_expect(wa, {"type":"room_peers","hash":"alpha1"},"room_peers")
        p = d.get("peers", [])
        results.append(("2h-room_isolation", ok and len(p)==1 and "h-p1" in p, f"peers={p}"))
        await asyncio.sleep(0.1)
        await safe_close(wb)
        await safe_close(wa)
    except Exception as e:
        import traceback
        tb = traceback.format_exc()
        results.append(("2h", False, f"{type(e).__name__}: {str(e)[:60]}"))

    # Print results
    for name, passed, detail in results:
        status = "PASS" if passed else "FAIL"
        print(f"RESULT:{name}:{status}:{detail}")

asyncio.run(run())
PYEOF

# Parse result lines from the python output
while IFS= read -r line; do
    if [[ "$line" == RESULT:* ]]; then
        IFS=: read -r _ name status detail <<< "$line"
        if [ "$status" = "PASS" ]; then
            pass "$name"
        else
            fail "$name ($detail)"
        fi
    fi
done < "$RESULTS_FILE"
# Show any non-RESULT lines (error/debug output from python)
grep -v "^RESULT:" "$RESULTS_FILE" | grep -v "^$" || true
rm -f "$RESULTS_FILE"
echo ""

# ── Summary ───────────────────────────────────────────────────────
echo "============================================"
echo "  Results: $PASS passed, $FAIL failed"
if [ $FAIL -eq 0 ]; then
    echo "  ALL TESTS PASSED"
else
    echo "  SOME TESTS FAILED"
fi
echo "============================================"
exit $FAIL
