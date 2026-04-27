#!/usr/bin/env bash
# Auth / Registration Server Test
# Tests: register, login, whoami, invalid token rejection
set -euo pipefail

REG="${REG_SERVER:-http://localhost:4000}"
PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

TEST_USER="testuser_$(date +%s)"
TEST_PASS="testpass123"

echo "=== Auth Test ==="
echo "Reg Server: $REG"
echo "Test user: $TEST_USER"
echo ""

# 1. Register
echo "--- Register ---"
REG_RES=$(curl -s --noproxy '*' -X POST "$REG/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$TEST_USER\",\"password\":\"$TEST_PASS\"}" 2>/dev/null || echo '{"error":"connection failed"}')
if echo "$REG_RES" | grep -q '"token"'; then
  pass "register new user"
  TOKEN=$(echo "$REG_RES" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
  echo "    Token: ${TOKEN:0:30}..."
elif echo "$REG_RES" | grep -q "already exists"; then
  pass "user already exists (continuing)"
  LOGIN_RES=$(curl -s --noproxy '*' -X POST "$REG/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$TEST_USER\",\"password\":\"$TEST_PASS\"}" 2>/dev/null)
  TOKEN=$(echo "$LOGIN_RES" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
else
  fail "register ($REG_RES)"
fi

if [ -z "${TOKEN:-}" ]; then
  fail "no token received"
  echo "=== Results: $PASS pass, $FAIL fail ==="
  exit $FAIL
fi

# 2. Login
echo ""
echo "--- Login ---"
LOGIN_RES=$(curl -s --noproxy '*' -X POST "$REG/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$TEST_USER\",\"password\":\"$TEST_PASS\"}" 2>/dev/null || echo '{"error":"fail"}')
if echo "$LOGIN_RES" | grep -q '"token"'; then
  pass "login"
  TOKEN=$(echo "$LOGIN_RES" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
else
  fail "login ($LOGIN_RES)"
fi

# 3. WhoAmI with valid token
echo ""
echo "--- WhoAmI ---"
WHO_RES=$(curl -s --noproxy '*' "$REG/auth/whoami" \
  -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo '{"error":"fail"}')
if echo "$WHO_RES" | grep -q "$TEST_USER"; then
  pass "whoami valid token"
else
  fail "whoami valid token ($WHO_RES)"
fi

# 4. WhoAmI with invalid token
echo ""
echo "--- Invalid Token ---"
INV_RES=$(curl -s --noproxy '*' "$REG/auth/whoami" \
  -H "Authorization: Bearer invalidtoken123" 2>/dev/null || echo '{"error":"fail"}')
if echo "$INV_RES" | grep -qi "invalid\|unauthorized\|error"; then
  pass "whoami rejects invalid token"
else
  fail "whoami rejects invalid token (got: $INV_RES)"
fi

# 5. Missing auth header
echo ""
echo "--- Missing Auth ---"
NOAUTH_RES=$(curl -s --noproxy '*' "$REG/auth/whoami" 2>/dev/null || echo '{"error":"fail"}')
if echo "$NOAUTH_RES" | grep -qi "unauthorized\|missing\|error"; then
  pass "whoami rejects missing auth"
else
  fail "whoami rejects missing auth (got: $NOAUTH_RES)"
fi

echo ""
echo "=== Results: $PASS pass, $FAIL fail ==="
[ $FAIL -eq 0 ] && echo "✅ ALL PASS" || echo "❌ SOME FAILED"
exit $FAIL
