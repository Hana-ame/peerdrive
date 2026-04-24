#!/bin/bash
set -euo pipefail

API_BASE="${API_BASE:-http://localhost:3000}"
CURL="curl -x '' -sf"

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

pass_cnt=0
fail_cnt=0

pass() {
  echo -e "  ${GREEN}✓${NC} $1"
  ((pass_cnt++))
}
fail() {
  echo -e "  ${RED}✗${NC} $1: $2"
  ((fail_cnt++))
}

section() {
  echo -e "\n${CYAN}══════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  $1${NC}"
  echo -e "${CYAN}══════════════════════════════════════════════${NC}"
}

echo "========================================"
echo "  Peerdrive 集成测试"
echo "  API_BASE = $API_BASE"
echo "========================================"

# ─────── 1. 基础健康 ───────
section "1. 基础健康检查"

DATA=$($CURL "$API_BASE/ping") && pass "/ping → $DATA" || fail "/ping" "$DATA"

DATA=$($CURL "$API_BASE/p2p/node") && pass "/p2p/node → $(echo $DATA | head -c 60)" || fail "/p2p/node" "$DATA"

# ─────── 2. 文件上传与校验 ───────
section "2. 文件上传与校验"

# Create a test file
TMPFILE=$(mktemp)
echo "Hello Peerdrive $(date)" > "$TMPFILE"
TMP_HASH=$(sha256sum "$TMPFILE" | awk '{print $1}')

DATA=$($CURL -F "file=@$TMPFILE" "$API_BASE/files/upload")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Upload file → hash matched: $TMP_HASH"
else
  fail "Upload file" "hash mismatch, got: $DATA"
fi

DATA=$($CURL "$API_BASE/files/verify/$TMP_HASH")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Verify file by hash → OK"
else
  fail "Verify file" "$DATA"
fi

# ─────── 3. 注册本地文件 ───────
section "3. 注册本地文件"

TMPFILE2=$(mktemp)
echo "Local file content $(date)" > "$TMPFILE2"
cp "$TMPFILE2" storage/register_test_file.txt
HASH2=$(sha256sum storage/register_test_file.txt | awk '{print $1}')

DATA=$($CURL -H 'Content-Type: application/json' \
  -d "{\"path\":\"register_test_file.txt\",\"filename\":\"test.txt\"}" \
  "$API_BASE/files/register_local")
if echo "$DATA" | grep -q "$HASH2"; then
  pass "Register local file → OK"
else
  fail "Register local file" "$DATA"
fi

# ─────── 4. 注册文件夹 ───────
section "4. 注册文件夹"

mkdir -p storage/testfolder
echo "folder file1" > storage/testfolder/f1.txt
echo "folder file2" > storage/testfolder/f2.txt
DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"folder_path":"testfolder"}' \
  "$API_BASE/files/register_folder")
if echo "$DATA" | grep -q "registered"; then
  pass "Register folder → OK"
else
  fail "Register folder" "$DATA"
fi

# ─────── 5. 不合法的 hash ───────
section "5. 边界情况"

DATA=$($CURL "$API_BASE/sha256sum/badhash") && fail "Invalid hash download should fail" "got 200" || pass "GET /sha256sum/badhash → 4xx"

DATA=$($CURL "$API_BASE/files/verify/badhash") && fail "Invalid hash verify should fail" "got 200" || pass "GET /files/verify/badhash → 4xx"

# ─────── 6. 合集管理 ───────
section "6. 合集管理"

# 6a. 创建合集
DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"test-coll"}' \
  -X POST "$API_BASE/collections")
if echo "$DATA" | grep -q "test-coll"; then
  pass "Create collection → OK"
else
  fail "Create collection" "$DATA"
fi

# 6b. 列出合集
DATA=$($CURL "$API_BASE/collections/tester")
if echo "$DATA" | grep -q "test-coll"; then
  pass "List collections → found test-coll"
else
  fail "List collections" "$DATA"
fi

# 6c. 获取合集详情
DATA=$($CURL "$API_BASE/collections/tester/test-coll")
if echo "$DATA" | grep -q "collection"; then
  pass "Get collection → OK"
else
  fail "Get collection" "$DATA"
fi

# 6d. 添加条目
DATA=$($CURL -H 'Content-Type: application/json' \
  -d "{\"path\":\"hello.txt\",\"hash\":\"$TMP_HASH\"}" \
  -X POST "$API_BASE/collections/tester/test-coll/entries")
if echo "$DATA" | grep -q "added"; then
  pass "Add entry → OK"
else
  fail "Add entry" "$DATA"
fi

# 6e. 再次获取合集（应包含条目）
DATA=$($CURL "$API_BASE/collections/tester/test-coll")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Collection now has entry with correct hash"
else
  fail "Collection missing entry" "$DATA"
fi

# da50. 下载合集文件
DATA=$($CURL -o /dev/null -w "%{http_code}" "$API_BASE/tester/test-coll/hello.txt")
if [ "$DATA" = "200" ]; then
  pass "Download from collection → HTTP 200"
else
  fail "Download from collection" "HTTP $DATA"
fi

# ─────── 7. Commit + 版本历史 + Rollback ───────
section "7. 版本管理"

# 7a. Commit
DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"commit_message":"initial commit"}' \
  -X POST "$API_BASE/collections/tester/test-coll/commit")
if echo "$DATA" | grep -q "committed"; then
  pass "Commit collection → OK"
else
  fail "Commit collection" "$DATA"
fi

# 7b. 另一个 commit
DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"commit_message":"second version"}' \
  -X POST "$API_BASE/collections/tester/test-coll/commit")
if echo "$DATA" | grep -q "committed"; then
  pass "Second commit → OK"
else
  fail "Second commit" "$DATA"
fi

# 7c. 版本日志
DATA=$($CURL "$API_BASE/collections/tester/test-coll/log")
if echo "$DATA" | grep -q "version_number"; then
  pass "Version log → OK"
  # pick first version id for rollback
  VID=$(echo "$DATA" | python3 -c "import sys,json; print(json.load(sys.stdin)['data'][-1]['id'])" 2>/dev/null || echo "")
else
  fail "Version log" "$DATA"
  VID=""
fi

# 7d. Rollback
if [ -n "$VID" ]; then
  DATA=$($CURL -X POST "$API_BASE/collections/tester/test-coll/rollback/$VID")
  if echo "$DATA" | grep -q "rolled"; then
    pass "Rollback to version $VID → OK"
  else
    fail "Rollback" "$DATA"
  fi
fi

# ─────── 8. Fork ───────
section "8. Fork 合集"

DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"forked-coll","source_username":"tester","source_coll_name":"test-coll"}' \
  -X POST "$API_BASE/actions/fork")
if echo "$DATA" | grep -q "forked"; then
  pass "Fork collection → OK"
else
  fail "Fork collection" "$DATA"
fi

# Verify fork has entries
DATA=$($CURL "$API_BASE/collections/tester/forked-coll")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Forked collection has entries"
else
  fail "Fork missing entries" "$DATA"
fi

# ─────── 9. Merge ───────
section "9. Merge 合集"

DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"test-coll","source_username":"tester","source_coll_name":"forked-coll","strategy":"theirs"}' \
  -X POST "$API_BASE/actions/merge")
if echo "$DATA" | grep -q "merge complete"; then
  pass "Merge collection → OK"
else
  fail "Merge collection" "$DATA"
fi

# ─────── 10. Pull ───────
section "10. Pull"

DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"test-coll"}' \
  -X POST "$API_BASE/actions/pull")
if echo "$DATA" | grep -q "task_id\|message"; then
  pass "Pull collection → OK"
else
  fail "Pull collection" "$DATA"
fi

# ─────── 11. 删除 ───────
section "11. 删除文件"

DATA=$($CURL -X DELETE "$API_BASE/files/$TMP_HASH")
if echo "$DATA" | grep -q "deleted"; then
  pass "Delete file by hash → OK"
else
  fail "Delete file" "$DATA"
fi

# ─────── 12. 任务系统 ───────
section "12. 任务系统"

DATA=$($CURL "$API_BASE/tasks")
if echo "$DATA" | grep -q "tasks"; then
  pass "List tasks → OK"
else
  fail "List tasks" "$DATA"
fi

DATA=$($CURL "$API_BASE/tasks/99999") && fail "Get nonexistent task should fail" "got 200" || pass "Get nonexistent task → 4xx"

# ─────── 13. 重复创建合集 ───────
section "13. 重复操作"

DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"test-coll"}' \
  -X POST "$API_BASE/collections") && fail "Duplicate collection should fail" "got 200" || pass "Duplicate collection → 4xx/409"

# ─────── 14. 上传同名文件 ───────
section "14. 重复文件上传"

DATA=$($CURL -F "file=@$TMPFILE" "$API_BASE/files/upload") && pass "Re-upload same file → still OK" || fail "Re-upload" "$DATA"

# ─────── 清理 ───────
rm -f "$TMPFILE" "$TMPFILE2" storage/register_test_file.txt
rm -rf storage/testfolder

# ─────── 结果 ───────
echo ""
echo "========================================"
echo -e "  结果: ${GREEN}$pass_cnt passed${NC}, ${RED}$fail_cnt failed${NC}"
echo "========================================"
if [ "$fail_cnt" -gt 0 ]; then
  exit 1
fi
