#!/usr/bin/env bash
# Peerdrive Full Test Suite
# Runs: Go build, Go unit tests, Frontend build, Playwright smoke tests
set -euo pipefail

PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

echo "══════════════════════════════════════════"
echo "  Peerdrive Full Test Suite"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "══════════════════════════════════════════"

GO_DIR="$(dirname "$0")/.."

# ── 1. Go Build ──
echo ""
echo "── 1. Go Build ──"
if (cd "$GO_DIR" && go build ./... 2>&1); then
  pass "go build"
else
  fail "go build"
fi

# ── 2. Go Unit Tests ──
echo ""
echo "── 2. Go Unit Tests ──"
if (cd "$GO_DIR" && go test ./... 2>&1); then
  pass "go test"
else
  fail "go test"
fi

# ── 3. Frontend Build ──
echo ""
echo "── 3. Frontend Build ──"
if npm run --prefix /mnt/d/WorkPlace/peerdrive/react build 2>&1 | tail -1 | grep -q "built"; then
  pass "frontend build"
else
  fail "frontend build"
fi

# ── 4. Playwright Smoke Tests ──
echo ""
echo "── 4. Playwright Smoke Tests ──"
SMOKE_OUT=$(node /home/lumin/.claude/skills/playwright-test/scripts/test-runner.mjs \
  /mnt/d/WorkPlace/peerdrive/go/test/peerdrive-smoke.mjs 2>&1)
if echo "$SMOKE_OUT" | grep -q "Fail 0"; then
  pass "playwright smoke (16 tests)"
else
  fail "playwright smoke"
  echo "$SMOKE_OUT" | grep "FAIL\|Pass\|Fail"
fi

# ── 5. Playwright Functional Tests ──
echo ""
echo "── 5. Playwright Functional Tests ──"
FUNC_OUT=$(node /home/lumin/.claude/skills/playwright-test/scripts/test-runner.mjs \
  /mnt/d/WorkPlace/peerdrive/go/test/peerdrive-functional.mjs 2>&1)
if echo "$FUNC_OUT" | grep -q "Fail 0"; then
  FCOUNT=$(echo "$FUNC_OUT" | grep "Total" | grep -oP '\d+')
  pass "playwright functional ($FCOUNT tests)"
else
  fail "playwright functional"
  echo "$FUNC_OUT" | grep "FAIL\|Pass\|Fail"
fi

# ── Summary ──
echo ""
echo "══════════════════════════════════════════"
echo "  Results: $PASS pass, $FAIL fail"
echo "══════════════════════════════════════════"

[ $FAIL -eq 0 ] && echo "✅ ALL PASS" || echo "❌ SOME FAILED"
exit $FAIL
