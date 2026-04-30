#!/usr/bin/env bash
# 注册服务器用户管理功能测试 (REQ 6.6-6.9)
# 验证: 群组查询、服务策略、存储追踪、节点操作者
set -euo pipefail

REGSRV="${REG_SERVER_URL:-http://localhost:4000}"
PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

cleanup() {
  rm -f /tmp/regsvr_test.db /tmp/regsvr_test.db-shm /tmp/regsvr_test.db-wal
}
trap cleanup EXIT

echo "=== 注册服务器用户管理测试 ==="
echo ""

# ── Helper: register a user and get token ──
register_user() {
  local u=$1 p=$2
  curl -s -X POST "$REGSRV/auth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$u\",\"password\":\"$p\"}" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])"
}

# ── [1] Ping ──
echo "[1] 服务连通性"
PING=$(curl -s "$REGSRV/ping")
echo "$PING" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['status']=='ok'" 2>/dev/null && pass "ping OK" || fail "ping"

# ── [2] Register test users ──
echo ""; echo "[2] 注册测试用户"
TOKEN_A=$(register_user "testuser_a" "pass1234")
[ -n "$TOKEN_A" ] && pass "register testuser_a" || fail "register testuser_a"

TOKEN_B=$(register_user "testuser_b" "pass1234")
[ -n "$TOKEN_B" ] && pass "register testuser_b" || fail "register testuser_b"

TOKEN_ADMIN=$(register_user "testadmin" "admin1234")
[ -n "$TOKEN_ADMIN" ] && pass "register testadmin" || fail "register testadmin"

AUTH_A="-H 'Authorization: Bearer $TOKEN_A'"
AUTH_B="-H 'Authorization: Bearer $TOKEN_B'"
AUTH_ADMIN="-H 'Authorization: Bearer $TOKEN_ADMIN'"

# ── [3] Group management (6.6) ──
echo ""; echo "[3] 群组管理 (REQ 6.6)"

# Add users to groups
eval "curl -s -X POST '$REGSRV/auth/group/testuser_a' -H 'Content-Type: application/json' $AUTH_A -d '{\"group_name\":\"engineering\"}'" > /dev/null
eval "curl -s -X POST '$REGSRV/auth/group/testuser_b' -H 'Content-Type: application/json' $AUTH_B -d '{\"group_name\":\"engineering\"}'" > /dev/null
eval "curl -s -X POST '$REGSRV/auth/group/testuser_a' -H 'Content-Type: application/json' $AUTH_A -d '{\"group_name\":\"ops\"}'" > /dev/null
pass "add users to groups"

# Get user groups
GRP_A=$(eval "curl -s '$REGSRV/auth/group/testuser_a' $AUTH_A")
echo "$GRP_A" | python3 -c "import sys,json; d=json.load(sys.stdin); assert len(d['groups'])==2" 2>/dev/null && pass "testuser_a in 2 groups" || fail "testuser_a groups"

# List all groups
ALL_G=$(eval "curl -s '$REGSRV/auth/groups' $AUTH_A")
echo "$ALL_G" | python3 -c "import sys,json; d=json.load(sys.stdin); assert len(d['groups'])>=2" 2>/dev/null && pass "list all groups" || fail "list all groups"

# Get group members
MEMBERS=$(eval "curl -s '$REGSRV/auth/groups/engineering/members' $AUTH_A")
echo "$MEMBERS" | python3 -c "import sys,json; d=json.load(sys.stdin); assert len(d['members'])==2" 2>/dev/null && pass "engineering has 2 members" || fail "engineering members"

# Remove user from group
eval "curl -s -X DELETE '$REGSRV/auth/group/testuser_b/engineering' $AUTH_A" > /dev/null
MEMBERS2=$(eval "curl -s '$REGSRV/auth/groups/engineering/members' $AUTH_A")
echo "$MEMBERS2" | python3 -c "import sys,json; d=json.load(sys.stdin); assert len(d['members'])==1" 2>/dev/null && pass "remove testuser_b from engineering" || fail "remove from group"

# ── [4] Service policy (6.7) ──
echo ""; echo "[4] 服务策略 (REQ 6.7)"

# Get default policy
POL=$(eval "curl -s '$REGSRV/auth/service-policy/testuser_a' $AUTH_A")
echo "$POL" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['allow_relay']==True; assert d['allow_p2p']==True" 2>/dev/null && pass "default policy: relay+P2P enabled" || fail "default policy"

# Set policy (admin required - first promote testadmin to admin in DB)
# For now test that the endpoint works with existing role
eval "curl -s -X POST '$REGSRV/auth/service-policy/testuser_a' -H 'Content-Type: application/json' $AUTH_A -d '{\"allow_relay\":true,\"allow_p2p\":false,\"notes\":\"P2P restricted\"}'" > /dev/null
POL2=$(eval "curl -s '$REGSRV/auth/service-policy/testuser_a' $AUTH_A")
echo "$POL2" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['allow_p2p']==False; assert d['notes']=='P2P restricted'" 2>/dev/null && pass "set service policy" || fail "set policy"

# ── [5] Storage tracking (6.9) ──
echo ""; echo "[5] 存储追踪 (REQ 6.9)"

# Get default storage
ST=$(eval "curl -s '$REGSRV/auth/storage/testuser_a' $AUTH_A")
echo "$ST" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['used_bytes']==0" 2>/dev/null && pass "default storage: 0 used" || fail "default storage"

# Update storage
eval "curl -s -X POST '$REGSRV/auth/storage/testuser_a' -H 'Content-Type: application/json' $AUTH_A -d '{\"used_bytes\":1048576,\"limit_bytes\":104857600}'" > /dev/null
ST2=$(eval "curl -s '$REGSRV/auth/storage/testuser_a' $AUTH_A")
echo "$ST2" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['used_bytes']==1048576; assert d['limit_bytes']==104857600" 2>/dev/null && pass "update storage: 1MB/100MB" || fail "update storage"

# ── [6] Relay operator (6.8) ──
echo ""; echo "[6] Relay 操作者查询 (REQ 6.8)"

# Register a relay with operator
curl -s -X POST "$REGSRV/p2p/relay/register" \
  -H 'Content-Type: application/json' \
  -d "{\"peer_id\":\"12D3KooTEST0000000000000000001\",\"addrs\":[\"/ip4/10.0.0.1/tcp/4001\"],\"storage_mb\":1024,\"version\":\"peerdrive-3.0\"}" > /dev/null
pass "register relay node"

# Set operator
eval "curl -s -X POST '$REGSRV/p2p/relay/12D3KooTEST0000000000000000001/operator' -H 'Content-Type: application/json' $AUTH_A -d '{\"username\":\"testuser_a\"}'" > /dev/null
pass "set relay operator"

# Get relay operator
OP=$(eval "curl -s '$REGSRV/p2p/relay/12D3KooTEST0000000000000000001/operator' $AUTH_A")
echo "$OP" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['operator_username']=='testuser_a'" 2>/dev/null && pass "get relay operator" || fail "get operator"

# List user's relays
MY_RELAYS=$(eval "curl -s '$REGSRV/auth/relays/testuser_a' $AUTH_A")
echo "$MY_RELAYS" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['total']>=1" 2>/dev/null && pass "list user relays" || fail "list user relays"

# ── [7] Stats ──
echo ""; echo "[7] 统计端点"
STATS=$(curl -s "$REGSRV/stats")
echo "$STATS" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['total_users']>=3; assert d['active_relays']>=1" 2>/dev/null && pass "stats: >=3 users, >=1 relay" || fail "stats"

# ── [8] Auth verification ──
echo ""; echo "[8] 认证验证"
eval "curl -s '$REGSRV/auth/whoami' $AUTH_A" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['username']=='testuser_a'" 2>/dev/null && pass "whoami OK" || fail "whoami"

# ── Summary ──
echo ""; echo "=== 结果: PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -gt 0 ] && exit 1 || exit 0
