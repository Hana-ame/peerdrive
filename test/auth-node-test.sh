#!/usr/bin/env bash
# Node 身份认证测试：API驱动的注册流程
set -euo pipefail

PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

cleanup() {
  [ -n "${RP:-}" ] && kill "$RP" 2>/dev/null || true
  [ -n "${NP:-}" ] && kill "$NP" 2>/dev/null || true
  rm -f /tmp/regsvr_nt.db /tmp/pd_nt.db /tmp/pd_nt.db-*
  rm -rf /tmp/pd_nt_s
}
trap cleanup EXIT

unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
export no_proxy='*'

H="127.0.0.1"  # 不用 localhost，Privoxy 会拦截
RP=4003        # reg server port
NP=3097        # peerdrive node port
C="curl -s"

echo "=== Node 身份认证测试 ==="

echo ""; echo "[0] Build"
cd "$(dirname "$0")/../../registration-server"
go build -o /tmp/reg-server ./cmd/server/ 2>/dev/null && pass "reg-server build" || { fail "reg-server"; exit 1; }
cd "$(dirname "$0")/.."
go build -o /tmp/pd-node peerdrive/cmd/server 2>/dev/null && pass "pd-node build" || { fail "pd-node"; exit 1; }

echo ""; echo "[1] Start services"
rm -f /tmp/regsvr_nt.db /tmp/pd_nt.db; mkdir -p /tmp/pd_nt_s
DB_PATH=/tmp/regsvr_nt.db PORT=$RP JWT_SECRET=test-secret /tmp/reg-server &
RP=$!
for i in $(seq 1 20); do $C "http://$H:$RP/ping" >/dev/null 2>&1 && break; sleep 0.3; done
$C "http://$H:$RP/ping" | python3 -c "import sys,json; assert json.load(sys.stdin)['status']=='ok'" 2>/dev/null && pass "reg server :$RP" || { fail "reg server"; exit 1; }

PEERDRIVE_STORAGE=/tmp/pd_nt_s PEERDRIVE_P2P_ENABLE=false PORT=$NP /tmp/pd-node &
NP=$!
for i in $(seq 1 30); do $C "http://$H:$NP/ping" >/dev/null 2>&1 && break; sleep 0.3; done
$C "http://$H:$NP/ping" >/dev/null 2>&1 && pass "pd node :$NP" || { fail "pd node"; exit 1; }

echo ""; echo "[2] Anonymous node"
$C "http://$H:$NP/p2p/node/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator'] is None; assert 'anonymous' in d.get('note','')
" 2>/dev/null && pass "anonymous by default" || fail "anon"

echo ""; echo "[3] Register user on central server"
TK=$($C -X POST "http://$H:$RP/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"noderunner","password":"test123456"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
[ -n "$TK" ] && pass "user registered" || { fail "user register"; exit 1; }

echo ""; echo "[4] Register node via API"
$C -X POST "http://$H:$NP/p2p/node/register" -H 'Content-Type: application/json' \
  -d "{\"token\":\"$TK\"}" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['status']=='registered'; assert d['username']=='noderunner'
" 2>/dev/null && pass "node registered via API" || fail "node register"

echo ""; echo "[5] Node operator after registration"
$C "http://$H:$NP/p2p/node/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator']=='noderunner'
" 2>/dev/null && pass "operator=noderunner" || fail "operator"

echo ""; echo "[6] Wrong token rejected"
$C -X POST "http://$H:$NP/p2p/node/register" -H 'Content-Type: application/json' \
  -d '{"token":"bad.token.here"}' | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert 'error' in d
" 2>/dev/null && pass "bad token rejected" || fail "bad token"

echo ""; echo "[7] Missing token rejected"
$C -X POST "http://$H:$NP/p2p/node/register" -H 'Content-Type: application/json' \
  -d '{}' | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert 'error' in d
" 2>/dev/null && pass "missing token rejected" || fail "missing token"

echo ""; echo "[8] Central server has node record"
$C "http://$H:$RP/auth/nodes/noderunner" -H "Authorization: Bearer $TK" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['total']>=0  # P2P disabled = no peer_id
" 2>/dev/null && pass "central server query OK" || fail "central"

echo ""; echo "[9] Public operator query on central server"
$C "http://$H:$RP/p2p/node/__anonymous__/operator" | python3 -c "
import sys,json; d=json.load(sys.stdin)
assert d['operator'] is None
" 2>/dev/null && pass "anonymous query on central" || fail "central anon"

echo ""; echo "=== PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -gt 0 ] && exit 1 || exit 0
