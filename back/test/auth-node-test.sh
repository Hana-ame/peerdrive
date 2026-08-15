#!/usr/bin/env bash
# 认证系统端到端测试：Node-用户绑定 + 流量统计 + operator查询
set -euo pipefail

PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

cleanup() {
  [ -n "${REG_PID:-}" ] && kill "$REG_PID" 2>/dev/null || true
  [ -n "${N1:-}" ] && kill "$N1" 2>/dev/null || true
  [ -n "${N2:-}" ] && kill "$N2" 2>/dev/null || true
  rm -f /tmp/regsvr_at.db /tmp/pd_at1.db /tmp/pd_at2.db /tmp/pd_at*.db-*
  rm -rf /tmp/pd_at_s1 /tmp/pd_at_s2
}
trap cleanup EXIT

unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
export no_proxy='*'
C="curl -s --noproxy '*'"
RP=4002; AP=3096; BP=3095

echo "=== 认证系统测试 ==="

echo ""; echo "[0] Build"
cd "$(dirname "$0")/../../registration-server" && go build -o /tmp/reg-server ./cmd/server/ 2>/dev/null && pass "reg-server" || { fail "reg-server"; exit 1; }
cd "$(dirname "$0")/.." && go build -tags nosqlite -o /tmp/pd-node ./cmd/server/ 2>/dev/null && pass "pd-node" || { fail "pd-node"; exit 1; }

echo ""; echo "[1] Start reg server"
rm -f /tmp/regsvr_at.db; mkdir -p /tmp/pd_at_s1 /tmp/pd_at_s2
DB_PATH=/tmp/regsvr_at.db PORT=$RP JWT_SECRET=test-secret /tmp/reg-server &
REG_PID=$!
for i in $(seq 1 20); do $C "http://localhost:$RP/ping" >/dev/null 2>&1 && break; sleep 0.3; done
$C "http://localhost:$RP/ping" | python3 -c "import sys,json; assert json.load(sys.stdin)['status']=='ok'" 2>/dev/null && pass "reg server :$RP" || { fail "reg server"; exit 1; }

echo ""; echo "[2] Register user"
TK=$($C -X POST "http://localhost:$RP/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"test123456"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
[ -n "$TK" ] && pass "register" || { fail "register"; exit 1; }
AH="Authorization: Bearer $TK"

echo ""; echo "[3] Node register"
$C -X POST "http://localhost:$RP/auth/node/register" -H 'Content-Type: application/json' -H "$AH" \
  -d '{"peer_id":"12D3KooTEST01","addrs":["/ip4/10.0.0.1/tcp/4001"],"version":"v3"}' \
  | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['status']=='registered'" 2>/dev/null && pass "node registered" || fail "node register"

echo ""; echo "[4] Heartbeat"
$C -X POST "http://localhost:$RP/auth/node/heartbeat" -H 'Content-Type: application/json' -H "$AH" \
  -d '{"peer_id":"12D3KooTEST01"}' | python3 -c "import sys,json; assert json.load(sys.stdin)['status']=='ok'" 2>/dev/null && pass "heartbeat" || fail "heartbeat"

echo ""; echo "[5] Operator query (public)"
$C "http://localhost:$RP/p2p/node/12D3KooTEST01/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator']['username']=='tester'" 2>/dev/null && pass "operator=tester" || fail "operator"

echo ""; echo "[6] Anonymous node"
$C "http://localhost:$RP/p2p/node/UNKNOWN/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator'] is None" 2>/dev/null && pass "anon=null" || fail "anon"

echo ""; echo "[7] Stats report"
$C -X POST "http://localhost:$RP/auth/node/stats" -H 'Content-Type: application/json' -H "$AH" \
  -d '{"peer_id":"12D3KooTEST01","upload_bytes":2097152,"download_bytes":1048576}' \
  | python3 -c "import sys,json; assert json.load(sys.stdin)['status']=='stats recorded'" 2>/dev/null && pass "stats reported" || fail "stats"
$C "http://localhost:$RP/p2p/node/12D3KooTEST01/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator']['total_upload_bytes']==2097152
assert d['operator']['total_download_bytes']==1048576
" 2>/dev/null && pass "stats 2MB/1MB" || fail "stats verify"

echo ""; echo "[8] Cumulative stats"
$C -X POST "http://localhost:$RP/auth/node/stats" -H 'Content-Type: application/json' -H "$AH" \
  -d '{"peer_id":"12D3KooTEST01","upload_bytes":524288,"download_bytes":0}' > /dev/null
$C "http://localhost:$RP/p2p/node/12D3KooTEST01/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator']['total_upload_bytes']==2621440" 2>/dev/null && pass "cumulative 2.5MB" || fail "cumulative"

echo ""; echo "[9] List user nodes"
$C "http://localhost:$RP/auth/nodes/tester" -H "$AH" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['total']>=1" 2>/dev/null && pass "list nodes" || fail "list"

echo ""; echo "[10] PD node anonymous"
PEERDRIVE_STORAGE=/tmp/pd_at_s1 PEERDRIVE_P2P_ENABLE=false PORT=$AP /tmp/pd-node &
N1=$!
for i in $(seq 1 30); do $C "http://localhost:$AP/ping" >/dev/null 2>&1 && break; sleep 0.3; done
$C "http://localhost:$AP/ping" >/dev/null 2>&1 && pass "pd :$AP" || { fail "pd start"; exit 1; }
$C "http://localhost:$AP/p2p/node/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator'] is None" 2>/dev/null && pass "pd anonymous" || fail "pd anon"

echo ""; echo "[11] PD node authenticated"
PEERDRIVE_STORAGE=/tmp/pd_at_s2 PEERDRIVE_P2P_ENABLE=false PORT=$BP \
  PEERDRIVE_AUTH_TOKEN="$TK" PEERDRIVE_REG_SERVER="http://localhost:$RP" \
  /tmp/pd-node &
N2=$!
for i in $(seq 1 30); do $C "http://localhost:$BP/ping" >/dev/null 2>&1 && break; sleep 0.3; done
$C "http://localhost:$BP/ping" >/dev/null 2>&1 && pass "pd :$BP" || { fail "pd auth start"; exit 1; }
$C "http://localhost:$BP/p2p/node/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator']=='tester'" 2>/dev/null && pass "pd operator=tester" || fail "pd operator"
$C "http://localhost:$RP/auth/nodes/tester" -H "$AH" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['total']>=1" 2>/dev/null && pass "reg auto-registered" || fail "reg auto"

echo ""; echo "[12] Anonymous still works"
$C "http://localhost:$AP/p2p/node/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator'] is None" 2>/dev/null && pass "anon confirmed" || fail "anon final"

echo ""; echo "=== PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -gt 0 ] && exit 1 || exit 0
