#!/bin/bash
set -euo pipefail

API_BASE="${API_BASE:-http://localhost:3000}"
CURL="curl -sf --noproxy '*'"
CURL_OK="curl -s --noproxy '*'"   # 正向断言用：无 -f，4xx/5xx 也返回 0，失败走 fail 统计（2026-08-19 修：-f+pipefail 下失败直接杀脚本，fail 从未生效）

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

pass_cnt=0
fail_cnt=0

pass() {
  echo -e "  ${GREEN}✓${NC} $1"
  pass_cnt=$((pass_cnt + 1))
}
fail() {
  echo -e "  ${RED}✗${NC} $1: $2"
  fail_cnt=$((fail_cnt + 1))
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

DATA=$($CURL_OK "$API_BASE/ping") && pass "/ping → $DATA" || fail "/ping" "$DATA"

DATA=$($CURL_OK "$API_BASE/peerjs/node") && pass "/peerjs/node → $(echo $DATA | head -c 60)" || fail "/peerjs/node" "$DATA"

# ─────── 2. 文件上传与校验 ───────
section "2. 文件上传与校验"

# Create a test file
TMPFILE=$(mktemp)
echo "Hello Peerdrive $(date)" > "$TMPFILE"
TMP_HASH=$(sha256sum "$TMPFILE" | awk '{print $1}')

DATA=$($CURL_OK -F "file=@$TMPFILE" "$API_BASE/files/upload")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Upload file → hash matched: $TMP_HASH"
else
  fail "Upload file" "hash mismatch, got: $DATA"
fi

DATA=$($CURL_OK "$API_BASE/files/verify/$TMP_HASH")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Verify file by hash → OK"
else
  fail "Verify file" "$DATA"
fi

# 访问：按 hash 下载文件本体，内容哈希必须与上传一致（主线「添加→访问」闭环）
DATA=$($CURL_OK "$API_BASE/sha256sum/$TMP_HASH" | sha256sum | awk '{print $1}')
if [ "$DATA" = "$TMP_HASH" ]; then
  pass "Download by hash → content matches ($TMP_HASH)"
else
  fail "Download by hash" "content hash mismatch: $DATA"
fi

# ─────── 3. 并发访问 ───────
section "3. 并发访问"

# 并发：10 个并行下载同一 hash，所有响应内容哈希必须一致。
# 子进程失败不触发 set -e（后台 + wait || true），统一收集结果统计。
CONC_FILE=$(mktemp)
for i in $(seq 1 10); do
  (
    H=$($CURL_OK "$API_BASE/sha256sum/$TMP_HASH" | sha256sum | awk '{print $1}')
    [ "$H" = "$TMP_HASH" ] && echo ok || echo "fail:$H"
  ) &
done > "$CONC_FILE"
wait || true
CONC_FAIL=$(grep -cv '^ok$' "$CONC_FILE" || true)
if [ "$CONC_FAIL" = "0" ]; then
  pass "Concurrent downloads (x10) → all content matches"
else
  fail "Concurrent downloads" "$CONC_FAIL/10 failed"
fi
rm -f "$CONC_FILE"

# ─────── 4. 注册本地文件 ───────
section "4. 注册本地文件"

TMPFILE2=$(mktemp)
echo "Local file content $(date)" > "$TMPFILE2"
cp "$TMPFILE2" storage/register_test_file.txt
HASH2=$(sha256sum storage/register_test_file.txt | awk '{print $1}')

DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d "{\"path\":\"register_test_file.txt\",\"filename\":\"test.txt\"}" \
  "$API_BASE/files/register_local")
if echo "$DATA" | grep -q "$HASH2"; then
  pass "Register local file → OK"
else
  fail "Register local file" "$DATA"
fi

# 访问：注册文件按 hash 走 /download（UniversalDownloader 的 LocalFetcher
# 会回读 file_providers 里的 local 路径，与 /sha256sum 的严格 blob 语义不同）
DATA=$($CURL_OK "$API_BASE/download/$HASH2" | sha256sum | awk '{print $1}')
if [ "$DATA" = "$HASH2" ]; then
  pass "Download registered file → content matches"
else
  fail "Download registered file" "content hash mismatch: $DATA"
fi

# ─────── 5. 注册文件夹 ───────
section "5. 注册文件夹"

mkdir -p storage/testfolder
echo "folder file1" > storage/testfolder/f1.txt
echo "folder file2" > storage/testfolder/f2.txt
DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d '{"folder_path":"testfolder"}' \
  "$API_BASE/files/register_folder")
if echo "$DATA" | grep -q "registered"; then
  pass "Register folder → OK"
else
  fail "Register folder" "$DATA"
fi

# 访问：文件夹注册的文件走 /download（LocalFetcher 回读 provider 路径）
F1_HASH=$(sha256sum storage/testfolder/f1.txt | awk '{print $1}')
DATA=$($CURL_OK "$API_BASE/download/$F1_HASH" | sha256sum | awk '{print $1}')
if [ "$DATA" = "$F1_HASH" ]; then
  pass "Download folder-registered file → content matches"
else
  fail "Download folder file" "content hash mismatch: $DATA"
fi

# ─────── 6. 边界情况 ───────
section "6. 边界情况"

DATA=$($CURL "$API_BASE/sha256sum/badhash") && fail "Invalid hash download should fail" "got 200" || pass "GET /sha256sum/badhash → 4xx"

DATA=$($CURL "$API_BASE/files/verify/badhash") && fail "Invalid hash verify should fail" "got 200" || pass "GET /files/verify/badhash → 4xx"

# ─────── 7. 合集管理 ───────
section "7. 合集管理"

# 集合名动态化：集合无 DELETE API，重复跑会撞 UNIQUE 约束（残留库）。
# 每次跑生成唯一名，测试数据天然幂等（2026-08-19 起，此前残留导致 5 项假失败）。
COLL="test-coll-$$-$RANDOM"

# 7a. 创建合集
DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"'"${COLL}"'"}' \
  -X POST "$API_BASE/collections")
if echo "$DATA" | grep -q "${COLL}"; then
  pass "Create collection → OK"
else
  fail "Create collection" "$DATA"
fi

# 7b. 列出合集
DATA=$($CURL_OK "$API_BASE/collections/tester")
if echo "$DATA" | grep -q "${COLL}"; then
  pass "List collections → found ${COLL}"
else
  fail "List collections" "$DATA"
fi

# 7c. 获取合集详情
DATA=$($CURL_OK "$API_BASE/collections/tester/${COLL}")
if echo "$DATA" | grep -q "collection"; then
  pass "Get collection → OK"
else
  fail "Get collection" "$DATA"
fi

# 7d. 添加条目
DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d "{\"path\":\"hello.txt\",\"hash\":\"$TMP_HASH\"}" \
  -X POST "$API_BASE/collections/tester/${COLL}/entries")
if echo "$DATA" | grep -q "added"; then
  pass "Add entry → OK"
else
  fail "Add entry" "$DATA"
fi

# 7e. 再次获取合集（应包含条目）
DATA=$($CURL_OK "$API_BASE/collections/tester/${COLL}")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Collection now has entry with correct hash"
else
  fail "Collection missing entry" "$DATA"
fi

# 7f. 下载合集文件（内容哈希必须与上传一致）
DATA=$($CURL_OK "$API_BASE/tester/${COLL}/hello.txt" | sha256sum | awk '{print $1}')
if [ "$DATA" = "$TMP_HASH" ]; then
  pass "Download from collection → content matches"
else
  fail "Download from collection" "content hash mismatch: $DATA"
fi

# ─────── 8. Commit + 版本历史 + Rollback ───────
section "8. 版本管理"

# 8a. Commit
DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d '{"commit_message":"initial commit"}' \
  -X POST "$API_BASE/collections/tester/${COLL}/commit")
if echo "$DATA" | grep -q "committed"; then
  pass "Commit collection → OK"
else
  fail "Commit collection" "$DATA"
fi

# 8b. 另一个 commit
DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d '{"commit_message":"second version"}' \
  -X POST "$API_BASE/collections/tester/${COLL}/commit")
if echo "$DATA" | grep -q "committed"; then
  pass "Second commit → OK"
else
  fail "Second commit" "$DATA"
fi

# 8c. 版本日志
DATA=$($CURL_OK "$API_BASE/collections/tester/${COLL}/log")
if echo "$DATA" | grep -q "version_number"; then
  pass "Version log → OK"
  # pick first version id for rollback
  VID=$(echo "$DATA" | python3 -c "import sys,json; print(json.load(sys.stdin)['data'][-1]['id'])" 2>/dev/null || echo "")
else
  fail "Version log" "$DATA"
  VID=""
fi

# 8d. Rollback
if [ -n "$VID" ]; then
  DATA=$($CURL_OK -X POST "$API_BASE/collections/tester/${COLL}/rollback/$VID")
  if echo "$DATA" | grep -q "rolled"; then
    pass "Rollback to version $VID → OK"
  else
    fail "Rollback" "$DATA"
  fi
fi

# ─────── 9. Fork ───────
section "9. Fork 合集"

DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"'"forked-${COLL}"'","source_username":"tester","source_coll_name":"'"${COLL}"'"}' \
  -X POST "$API_BASE/actions/fork")
if echo "$DATA" | grep -q "forked"; then
  pass "Fork collection → OK"
else
  fail "Fork collection" "$DATA"
fi

# Verify fork has entries
DATA=$($CURL_OK "$API_BASE/collections/tester/forked-${COLL}")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Forked collection has entries"
else
  fail "Fork missing entries" "$DATA"
fi

# ─────── 10. Merge ───────
section "10. Merge 合集"

DATA=$($CURL_OK -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"'"${COLL}"'","source_username":"tester","source_coll_name":"'"forked-${COLL}"'","strategy":"theirs"}' \
  -X POST "$API_BASE/actions/merge")
if echo "$DATA" | grep -q "merge complete"; then
  pass "Merge collection → OK"
else
  fail "Merge collection" "$DATA"
fi

# ─────── 11. 重复文件上传 ───────
section "11. 重复文件上传"

# 同 hash 二次上传（此时文件仍在库中）不应报错（dedupe/幂等）
DATA=$($CURL_OK -F "file=@$TMPFILE" "$API_BASE/files/upload")
if echo "$DATA" | grep -q "$TMP_HASH"; then
  pass "Re-upload same file → still OK"
else
  fail "Re-upload" "$DATA"
fi

# ─────── 12. 删除文件 ───────
section "12. 删除文件"

DATA=$($CURL_OK -X DELETE "$API_BASE/files/$TMP_HASH")
if echo "$DATA" | grep -q "deleted"; then
  pass "Delete file by hash → OK"
else
  fail "Delete file" "$DATA"
fi

# 删除后 verify 应 4xx（文件本体与索引均已清除）
DATA=$($CURL "$API_BASE/files/verify/$TMP_HASH") && fail "Verify deleted file should fail" "got 200" || pass "Verify deleted file → 4xx"

# ─────── 13. 重复创建合集 ───────
section "13. 重复操作"

DATA=$($CURL -H 'Content-Type: application/json' \
  -d '{"username":"tester","collection_name":"'"${COLL}"'"}' \
  -X POST "$API_BASE/collections") && fail "Duplicate collection should fail" "got 200" || pass "Duplicate collection → 4xx/409"

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
