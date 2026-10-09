#!/usr/bin/env bash
# 01_baseline_invariants.sh — Core repository invariants formulated by the user.
#
# AGENT NOTICE:
# This test was formulated as an immutable user invariant.
# Agents are FORBIDDEN from modifying or loosening the assertions below.
# If this fails, fix the codebase to satisfy the assertion.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAILURES=0

pass() {
  echo -e "  \033[32m✓\033[0m $1"
}

fail() {
  echo -e "  \033[31m✗\033[0m $1"
  FAILURES=$((FAILURES + 1))
}

echo "=================================================="
echo "  [User Invariant] 01 Baseline Repository Invariants"
echo "=================================================="

# Invariant 1: Top-level architecture directories must exist
echo "Checking core architecture layout..."
for dir in "back" "front" "doc" "scripts" "user_tests"; do
  if [ -d "$ROOT/$dir" ]; then
    pass "Directory '$dir' exists"
  else
    fail "Required directory '$dir' is missing!"
  fi
done

# Invariant 2: Backend main binary must compile without errors
echo "Checking backend compilation..."
if (cd "$ROOT/back" && go build -tags nosqlite -o /dev/null ./cmd/server/ >/dev/null 2>&1); then
  pass "Backend server compiles successfully with -tags nosqlite"
else
  fail "Backend server compilation failed!"
fi

# Invariant 3: Legacy libp2p stack must NOT be imported in backend code
echo "Checking legacy stack exclusion..."
if grep -rn "github.com/libp2p" "$ROOT/back/internal" >/dev/null 2>&1; then
  fail "Forbidden dependency found: back/internal contains imports of libp2p!"
else
  pass "Zero libp2p imports in back/internal (clean stack invariant maintained)"
fi

# Invariant 4: Secure path containment package must exist
echo "Checking path containment primitive..."
if [ -d "$ROOT/back/internal/pathutil" ]; then
  pass "back/internal/pathutil is present"
else
  fail "Security primitive back/internal/pathutil is missing!"
fi

# Invariant 5: Frontend configuration integrity
echo "Checking frontend configuration..."
if [ -f "$ROOT/front/package.json" ]; then
  pass "front/package.json exists"
else
  fail "front/package.json is missing!"
fi

echo "=================================================="
if [ "$FAILURES" -gt 0 ]; then
  echo -e "\033[31m[FAILED]\033[0m $FAILURES user invariant check(s) failed!"
  echo "Reminder to Agents: Do not edit this test file. Fix the repository code."
  exit 1
fi

echo -e "\033[32m[PASSED]\033[0m All baseline invariants satisfied!"
exit 0
