#!/usr/bin/env bash
# test-user-layer.sh — Executes the dedicated User Invariant Test Layer (L0-user).
#
# RULE & CONTRACT:
# 1. Tests in user_tests/ are formulated exclusively by the human user.
# 2. AI agents MUST NOT modify, rename, weaken, or delete any test in user_tests/.
# 3. All tests in this layer are non-negotiable hard invariants and must pass 100%.
# 4. If any test fails, agents must fix the application code, never the test.

set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_TESTS_DIR="$ROOT/user_tests"

GREEN='\033[32m'
RED='\033[31m'
YELLOW='\033[33m'
CYAN='\033[36m'
BOLD='\033[1m'
NC='\033[0m'

echo -e "${CYAN}${BOLD}============================================================${NC}"
echo -e "${CYAN}${BOLD}  L0-user: USER INVARIANT TEST LAYER${NC}"
echo -e "  Directory: ${USER_TESTS_DIR}"
echo -e "  Policy: ${YELLOW}User Formulated · Strictly Immutable for AI Agents${NC}"
echo -e "${CYAN}${BOLD}============================================================${NC}"

if [ ! -d "$USER_TESTS_DIR" ]; then
  echo -e "${RED}Error: user_tests directory not found at $USER_TESTS_DIR${NC}" >&2
  exit 1
fi

# Discover test files (excluding templates and docs)
TEST_FILES=()
while IFS= read -r file; do
  [ -z "$file" ] && continue
  fname=$(basename "$file")
  # Skip templates, readmes, and hidden files
  if [[ "$fname" =~ ^(README\.md|template[\._].*|\..*)$ ]]; then
    continue
  fi
  TEST_FILES+=("$file")
done < <(find "$USER_TESTS_DIR" -maxdepth 1 -type f \( -name "*.sh" -o -name "*.mjs" -o -name "*.js" -o -name "*.go" -o -perm -u+x \) | sort)

# Check if there are user Go test packages in user_tests subdirectories
USER_GO_DIRS=()
while IFS= read -r gdir; do
  [ -z "$gdir" ] && continue
  USER_GO_DIRS+=("$gdir")
done < <(find "$USER_TESTS_DIR" -mindepth 2 -maxdepth 3 -type f -name "*_test.go" -exec dirname {} + 2>/dev/null | sort -u)

# Also check for internal user invariants in back/test/user_invariants
if [ -d "$ROOT/back/test/user_invariants" ] && ls "$ROOT/back/test/user_invariants"/*_test.go >/dev/null 2>&1; then
  USER_GO_DIRS+=("$ROOT/back/test/user_invariants")
fi

TOTAL=$((${#TEST_FILES[@]} + ${#USER_GO_DIRS[@]}))
if [ "$TOTAL" -eq 0 ]; then
  echo -e "${YELLOW}Notice: No active user invariant tests found in user_tests/.${NC}"
  echo "Users can place *.sh, *.go, *.js, or executable test scripts into user_tests/."
  exit 0
fi

echo "Discovered $TOTAL user invariant test suite(s)."
PASS=0
FAIL=0
FAILED_NAMES=()

# Run single file tests
for test_path in "${TEST_FILES[@]}"; do
  test_name=$(basename "$test_path")
  log_path="/tmp/user-test-${test_name}.log"
  echo -n "  ▶ Running user test: ${test_name} ... "

  cmd=""
  if [[ "$test_name" == *.sh ]]; then
    cmd="bash '$test_path'"
  elif [[ "$test_name" == *.mjs ]] || [[ "$test_name" == *.js ]]; then
    cmd="node '$test_path'"
  elif [[ "$test_name" == *_test.go ]]; then
    cmd="go test -tags nosqlite -v '$test_path'"
  elif [[ "$test_name" == *.go ]]; then
    cmd="go run -tags nosqlite '$test_path'"
  elif [ -x "$test_path" ]; then
    cmd="'$test_path'"
  else
    cmd="bash '$test_path'"
  fi

  if bash -c "$cmd" > "$log_path" 2>&1; then
    echo -e "${GREEN}PASS${NC}"
    PASS=$((PASS + 1))
  else
    echo -e "${RED}FAIL${NC} (log: $log_path)"
    FAIL=$((FAIL + 1))
    FAILED_NAMES+=("$test_name")
  fi
done

# Run Go package test directories
for gdir in "${USER_GO_DIRS[@]}"; do
  rel_dir="${gdir#$ROOT/}"
  log_path="/tmp/user-test-$(echo "$rel_dir" | tr '/' '_').log"
  echo -n "  ▶ Running user Go package: ${rel_dir} ... "

  if bash -c "cd '$gdir' && go test -tags nosqlite -v ./..." > "$log_path" 2>&1; then
    echo -e "${GREEN}PASS${NC}"
    PASS=$((PASS + 1))
  else
    echo -e "${RED}FAIL${NC} (log: $log_path)"
    FAIL=$((FAIL + 1))
    FAILED_NAMES+=("$rel_dir")
  fi
done

echo ""
echo "------------------------------------------------------------"
echo -e "User Layer Result: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC} (out of ${TOTAL})"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo -e "${RED}${BOLD}============================================================${NC}"
  echo -e "${RED}${BOLD}  ❌ CRITICAL FAILURE: User Invariant Test(s) Failed!${NC}"
  echo -e "${RED}${BOLD}============================================================${NC}"
  for failed_test in "${FAILED_NAMES[@]}"; do
    echo -e "${RED}• Failed: ${failed_test}${NC}"
    echo "--- [Log: /tmp/user-test-${failed_test}.log] ---"
    cat "/tmp/user-test-${failed_test}.log" 2>/dev/null | tail -n 25
    echo "--------------------------------------------------"
  done
  echo ""
  echo -e "${YELLOW}${BOLD}⚠️  MANDATORY DIRECTIVE FOR ALL AI AGENTS:${NC}"
  echo -e "   1. You are ${RED}${BOLD}STRICTLY FORBIDDEN${NC} from modifying or deleting the test files in user_tests/."
  echo -e "   2. The user test represents an immutable specification."
  echo -e "   3. You MUST analyze the failure above and fix the application code until this test passes."
  echo -e "${RED}${BOLD}============================================================${NC}"
  exit 1
fi

echo -e "${GREEN}${BOLD}✓ All user invariant tests successfully passed!${NC}"
exit 0
