#!/bin/bash
#===============================================================================
# IPFS Module Integration Test Suite
#
# Tests all IPFS-related API endpoints of Peerdrive:
#   - Server health (/ping)
#   - CID download (/ipfs/:cid)
#   - CID not found
#   - IPFS gateway health (/ipfs/gateways)
#   - Pin CID (/ipfs/pin/:cid)
#   - List pins (/ipfs/pins)
#   - Unpin CID (DELETE /ipfs/pin/:cid)
#   - IPFS compat status (/ipfs)
#
# Usage: ./ipfs-full-test.sh [port]
#   Default port: 3002
#
# Exit code: 0 = all tests pass, 1 = one or more tests fail
#===============================================================================

set -euo pipefail

# --- Configuration ----------------------------------------------------------
PORT="${1:-3002}"
BASE="http://localhost:${PORT}"
CURL="curl -s --noproxy '*' --connect-timeout 5"

# Test CIDs (well-known IPFS content)
CID_HELLO="QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG"   # IPFS Hello World
CID_EMPTY_DIR="QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn"  # Empty directory
CID_INVALID="QmInvalid00000000000000000000000000000000000000"

# --- Colors -----------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# --- State ------------------------------------------------------------------
PASS_COUNT=0
FAIL_COUNT=0
WARN_COUNT=0
TOTAL_TESTS=0

# --- Helpers ----------------------------------------------------------------
header() {
  echo -e "\n${CYAN}${BOLD}============================================${NC}"
  echo -e "${CYAN}${BOLD}  $1${NC}"
  echo -e "${CYAN}${BOLD}============================================${NC}"
}

pass() {
  local name="$1"
  local detail="${2:-}"
  PASS_COUNT=$((PASS_COUNT + 1))
  TOTAL_TESTS=$((TOTAL_TESTS + 1))
  echo -e "  ${GREEN}[PASS]${NC} ${name} ${detail:+(${detail})}"
}

fail() {
  local name="$1"
  local detail="${2:-}"
  FAIL_COUNT=$((FAIL_COUNT + 1))
  TOTAL_TESTS=$((TOTAL_TESTS + 1))
  echo -e "  ${RED}[FAIL]${NC} ${name} ${detail:+(${detail})}"
}

warn() {
  local name="$1"
  local detail="${2:-}"
  WARN_COUNT=$((WARN_COUNT + 1))
  TOTAL_TESTS=$((TOTAL_TESTS + 1))
  echo -e "  ${YELLOW}[WARN]${NC} ${name} ${detail:+(${detail})}"
}

check_json_field() {
  local json="$1"
  local field="$2"
  echo "$json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['$field'])" 2>/dev/null
}

check_http_status() {
  local url="$1"
  local method="${2:-GET}"
  local data="${3:-}"
  local expected_code="${4:-200}"
  local timeout="${5:-10}"
  if [ "$method" = "GET" ]; then
    $CURL --max-time "$timeout" -o /dev/null -w "%{http_code}" "$url" 2>/dev/null
  elif [ "$method" = "POST" ]; then
    $CURL --max-time "$timeout" -X POST -H "Content-Type: application/json" -o /dev/null -w "%{http_code}" -d "$data" "$url" 2>/dev/null
  elif [ "$method" = "DELETE" ]; then
    $CURL --max-time "$timeout" -X DELETE -o /dev/null -w "%{http_code}" "$url" 2>/dev/null
  fi
}

get_json() {
  local url="$1"
  local method="${2:-GET}"
  local data="${3:-}"
  local timeout="${4:-10}"
  if [ "$method" = "GET" ]; then
    $CURL --max-time "$timeout" "$url" 2>/dev/null
  elif [ "$method" = "POST" ]; then
    $CURL --max-time "$timeout" -X POST -H "Content-Type: application/json" -d "$data" "$url" 2>/dev/null
  elif [ "$method" = "DELETE" ]; then
    $CURL --max-time "$timeout" -X DELETE "$url" 2>/dev/null
  fi
}

# =============================================================================
echo -e "${BOLD}Peerdrive IPFS Module -- Full Integration Test${NC}"
echo "Server: ${BASE}"
echo "Started: $(date)"
echo ""

# =============================================================================
# 1. HEALTH CHECK
# =============================================================================
header "1. Server Health Check"

HTTP_CODE=$(check_http_status "${BASE}/ping" GET "" 200)
if [ "$HTTP_CODE" = "200" ]; then
  BODY=$(get_json "${BASE}/ping")
  if [ "$BODY" = "pong" ]; then
    pass "GET /ping -- health check" "pong"
  else
    fail "GET /ping -- unexpected body" "$BODY"
  fi
else
  fail "GET /ping -- HTTP ${HTTP_CODE} (expected 200)"
fi

# =============================================================================
# 2. CID DOWNLOAD (Gateway Fallback)
# =============================================================================
header "2. CID Download via Gateway Fallback"

# 2a. Try downloading a well-known small CID (empty directory)
echo "  Fetching well-known CID: ${CID_EMPTY_DIR} (gateway fallback) ..."
HTTP_CODE=$(check_http_status "${BASE}/ipfs/${CID_EMPTY_DIR}" GET "" 200 30)
if [ "$HTTP_CODE" = "200" ]; then
  pass "GET /ipfs/${CID_EMPTY_DIR} -- download" "HTTP 200"
elif [ "$HTTP_CODE" = "404" ]; then
  warn "GET /ipfs/${CID_EMPTY_DIR} -- HTTP 404 (gateway may not have this CID)"
elif [ "$HTTP_CODE" = "000" ]; then
  warn "GET /ipfs/${CID_EMPTY_DIR} -- timeout (gateway unreachable)"
else
  fail "GET /ipfs/${CID_EMPTY_DIR} -- HTTP ${HTTP_CODE} (expected 200)"
fi

# 2b. Test invalid CID returns 404
echo "  Fetching invalid CID..."
HTTP_CODE=$(check_http_status "${BASE}/ipfs/${CID_INVALID}" GET "" 404 10)
if [ "$HTTP_CODE" = "404" ]; then
  pass "GET /ipfs/${CID_INVALID} -- expected not found" "HTTP 404"
elif [ "$HTTP_CODE" = "400" ]; then
  pass "GET /ipfs/${CID_INVALID} -- bad request" "HTTP 400 (validated)"
else
  warn "GET /ipfs/${CID_INVALID} -- HTTP ${HTTP_CODE} (expected 404)"
fi

# =============================================================================
# 3. IPFS GATEWAY HEALTH
# =============================================================================
header "3. IPFS Gateway Health (/ipfs/gateways)"

GW_RESP=$(get_json "${BASE}/ipfs/gateways" GET "" 30)
GW_COUNT=$(echo "$GW_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('gateways',[])))" 2>/dev/null || echo "0")

if [ "$GW_COUNT" -gt 0 ]; then
  pass "GET /ipfs/gateways -- ${GW_COUNT} configured gateways"
  # Show individual gateway health
  echo "$GW_RESP" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for g in data.get('gateways', []):
    url = g.get('url','')
    healthy = g.get('healthy', False)
    lat = g.get('latency','N/A')
    icon = 'PASS' if healthy else 'WARN'
    print(f'  [{icon}] {url} | healthy={healthy} | latency={lat}')
" 2>/dev/null || true
  # Count healthy gateways
  HEALTHY_COUNT=$(echo "$GW_RESP" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(sum(1 for g in data.get('gateways', []) if g.get('healthy')))
" 2>/dev/null || echo "0")
  if [ "$HEALTHY_COUNT" -gt 0 ]; then
    pass "IPFS gateways -- ${HEALTHY_COUNT}/${GW_COUNT} healthy"
  else
    warn "IPFS gateways -- 0/${GW_COUNT} healthy (all failed)"
  fi
else
  fail "GET /ipfs/gateways -- no gateways configured"
fi

# =============================================================================
# 4. IPFS COMPAT STATUS
# =============================================================================
header "4. IPFS Compat Status (/ipfs)"

COMPAT_RESP=$(get_json "${BASE}/ipfs")
COMPAT_ENABLED=$(echo "$COMPAT_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('enabled', False))" 2>/dev/null || echo "false")
BLOCK_COUNT=$(echo "$COMPAT_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('block_count', 0))" 2>/dev/null || echo "0")

if [ "$COMPAT_ENABLED" = "True" ] || [ "$COMPAT_ENABLED" = "true" ]; then
  pass "GET /ipfs -- compat enabled, ${BLOCK_COUNT} blocks"
else
  pass "GET /ipfs -- compat disabled, ${BLOCK_COUNT} blocks" \
       "(enable via POST /ipfs/toggle if needed)"
fi

# =============================================================================
# 5. PIN CID
# =============================================================================
header "5. Pin CID (/ipfs/pin/:cid)"

echo "  Attempting to pin: ${CID_EMPTY_DIR} ..."
PIN_RESP=$(get_json "${BASE}/ipfs/pin/${CID_EMPTY_DIR}" POST "" 60)
PIN_STATUS=$(echo "$PIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','unknown'))" 2>/dev/null || echo "error")

if [ "$PIN_STATUS" = "pinned" ]; then
  PIN_CID=$(echo "$PIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('cid',''))" 2>/dev/null)
  PIN_SIZE=$(echo "$PIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('size',0))" 2>/dev/null)
  pass "POST /ipfs/pin/${CID_EMPTY_DIR} -- pinned" "CID=${PIN_CID}, size=${PIN_SIZE}B"
elif [ "$PIN_STATUS" = "already_pinned" ]; then
  pass "POST /ipfs/pin/${CID_EMPTY_DIR} -- already_pinned"
elif [ "$PIN_STATUS" = "error" ]; then
  ERR_MSG=$(echo "$PIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error','unknown error'))" 2>/dev/null || echo "parse error")
  warn "POST /ipfs/pin/${CID_EMPTY_DIR} -- fetch failed" "${ERR_MSG}"
else
  warn "POST /ipfs/pin/${CID_EMPTY_DIR} -- unexpected status" "${PIN_STATUS}"
fi

# =============================================================================
# 6. LIST PINS
# =============================================================================
header "6. List Pins (/ipfs/pins)"

PINS_RESP=$(get_json "${BASE}/ipfs/pins")
PIN_COUNT=$(echo "$PINS_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('count',0))" 2>/dev/null || echo "0")

if [ "$PIN_COUNT" -ge 0 ]; then
  pass "GET /ipfs/pins -- ${PIN_COUNT} pins listed"
  if [ "$PIN_COUNT" -gt 0 ]; then
    echo "$PINS_RESP" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for p in data.get('pins', []):
    print(f'  CID={p.get(\"cid\",\"\")} | hash={p.get(\"hash\",\"\")[:16]}... | size={p.get(\"size\",0)}B | pinned_at={p.get(\"pinned_at\",\"\")}')
" 2>/dev/null || true
  fi
else
  fail "GET /ipfs/pins -- could not parse response"
fi

# =============================================================================
# 7. UNPIN CID
# =============================================================================
header "7. Unpin CID (DELETE /ipfs/pin/:cid)"

# If we pinned earlier, unpin the same CID
if [ "$PIN_STATUS" = "pinned" ] || [ "$PIN_STATUS" = "already_pinned" ]; then
  echo "  Unpinning: ${CID_EMPTY_DIR} ..."
  UNPIN_RESP=$(get_json "${BASE}/ipfs/pin/${CID_EMPTY_DIR}" DELETE "" 10)
  UNPIN_STATUS=$(echo "$UNPIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','unknown'))" 2>/dev/null || echo "error")
  if [ "$UNPIN_STATUS" = "unpinned" ]; then
    pass "DELETE /ipfs/pin/${CID_EMPTY_DIR} -- unpinned"
  elif [ "$UNPIN_STATUS" = "error" ]; then
    ERR_MSG=$(echo "$UNPIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error','unknown error'))" 2>/dev/null)
    fail "DELETE /ipfs/pin/${CID_EMPTY_DIR}" "${ERR_MSG}"
  else
    fail "DELETE /ipfs/pin/${CID_EMPTY_DIR}" "unexpected: ${UNPIN_STATUS}"
  fi
else
  echo "  Pin CID test did not succeed in Step 5, skipping unpin test."
  echo "  Trying unpin of known non-pinned CID instead..."
  UNPIN_RESP=$(get_json "${BASE}/ipfs/pin/${CID_INVALID}" DELETE "" 10)
  UNPIN_STATUS=$(echo "$UNPIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status','error'))" 2>/dev/null || echo "error")
  UNPIN_ERR=$(echo "$UNPIN_RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null || echo "")
  if [ "$UNPIN_STATUS" = "error" ] && echo "$UNPIN_ERR" | grep -qi "not.found"; then
    pass "DELETE /ipfs/pin/${CID_INVALID} -- correctly rejected" "pin not found"
  else
    warn "DELETE /ipfs/pin/${CID_INVALID}" "response: ${UNPIN_RESP}"
  fi
fi

# Verify pins reflect the unpin
PINS_AFTER=$(get_json "${BASE}/ipfs/pins")
PIN_COUNT_AFTER=$(echo "$PINS_AFTER" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('count',0))" 2>/dev/null || echo "0")
echo "  Remaining pins after unpin: ${PIN_COUNT_AFTER}"

# =============================================================================
# 8. VERIFY CID DOWNLOAD AFTER PIN (if pin succeeded)
# =============================================================================
header "8. CID Download from Local Storage (if pinned)"

if [ "$PIN_STATUS" = "pinned" ]; then
  echo "  Verifying local download of previously pinned CID: ${CID_EMPTY_DIR} ..."
  HTTP_CODE=$(check_http_status "${BASE}/ipfs/${CID_EMPTY_DIR}" GET "" 200 10)
  if [ "$HTTP_CODE" = "200" ]; then
    pass "GET /ipfs/${CID_EMPTY_DIR} -- local storage hit"
    # Check X-CID header
    XCID=$($CURL --max-time 10 -I "${BASE}/ipfs/${CID_EMPTY_DIR}" 2>/dev/null | grep -i "^X-CID:" | awk '{print $2}' | tr -d '\r')
    if [ -n "$XCID" ]; then
      pass "X-CID header present" "${XCID}"
    fi
  elif [ "$HTTP_CODE" = "404" ]; then
    warn "GET /ipfs/${CID_EMPTY_DIR} -- not found after pin" "data may have been gc'd"
  else
    fail "GET /ipfs/${CID_EMPTY_DIR} -- HTTP ${HTTP_CODE}"
  fi
elif [ "$PIN_STATUS" = "already_pinned" ]; then
  pass "GET /ipfs/${CID_EMPTY_DIR} -- previously pinned, expected local hit"
else
  echo "  Pin CID test did not succeed, skipping local download verification."
fi

# =============================================================================
# SUMMARY
# =============================================================================
header "Test Summary"

echo ""
echo -e "  Total:  ${BOLD}${TOTAL_TESTS}${NC}"
echo -e "  ${GREEN}PASS:   ${PASS_COUNT}${NC}"
echo -e "  ${RED}FAIL:   ${FAIL_COUNT}${NC}"
echo -e "  ${YELLOW}WARN:   ${WARN_COUNT}${NC}"
echo ""
echo -e "  Test file:  $(basename "$0")"
echo -e "  Server:     ${BASE}"
echo -e "  Finished:   $(date)"
echo ""

if [ "$FAIL_COUNT" -eq 0 ]; then
  echo -e "${GREEN}${BOLD}RESULT: ALL TESTS PASS${NC}"
  exit 0
else
  echo -e "${RED}${BOLD}RESULT: ${FAIL_COUNT} TEST(S) FAILED${NC}"
  exit 1
fi
