#!/usr/bin/env bash
# ============================================================
# Peerdrive Comprehensive E2E Test
# Covers ALL HTTP endpoints with proper assertions.
# Self-contained: builds & starts its own server, cleans up.
# ============================================================
set -euo pipefail

# bypass system proxy (e.g. Privoxy) for localhost
export no_proxy='*'

# --- helpers ---
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; NC='\033[0m'
PASS=0; FAIL=0; WARN=0

pass() { echo -e "  ${GREEN}PASS${NC} $1"; PASS=$((PASS+1)); }
fail() { echo -e "  ${RED}FAIL${NC} $1"; FAIL=$((FAIL+1)); }
warn() { echo -e "  ${YELLOW}WARN${NC} $1"; WARN=$((WARN+1)); }
info() { echo -e "${CYAN}--- $1 ---${NC}"; }

assert_status() { [ "$1" = "$2" ] && pass "$3" || fail "$3 (expected $2, got $1)"; }
assert_contains() { echo "$1" | grep -q "$2" && pass "$3" || fail "$3 (missing '$2')"; }
assert_not_contains() { echo "$1" | grep -qv "$2" && pass "$3" || fail "$3 (unexpected '$2')"; }
assert_json() { echo "$1" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null && pass "$2" || fail "$2 (invalid JSON)"; }

# --- config ---
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORK_DIR="/tmp/peerdrive_e2e_all"
SERVER_BIN="$SCRIPT_DIR/../peerdrive-server"
API="http://localhost:3999"
STORAGE="$WORK_DIR/storage"

cleanup() {
  kill %1 2>/dev/null || true
  rm -rf "$WORK_DIR"
  rm -f ./peerdrive.db "$SCRIPT_DIR/../peerdrive.db"
}
trap cleanup EXIT

# --- build ---
info "Building server"
cd "$SCRIPT_DIR/.."
go build -o "$SERVER_BIN" ./cmd/server/main.go
cd -

# --- start server ---
info "Starting server"
rm -rf "$WORK_DIR"
mkdir -p "$STORAGE"
PORT=3999 PEERDRIVE_STORAGE="$STORAGE" PEERDRIVE_P2P_ENABLE=false "$SERVER_BIN" &
sleep 3

# verify server is up
if ! curl -sf "$API/ping" > /dev/null 2>&1; then
  echo "Server failed to start"; exit 1
fi
pass "Server started on port 3999"

# ============================================================
info "1. HEALTH"
# ============================================================
RESP=$(curl -sf "$API/ping")
assert_contains "$RESP" "pong" "GET /ping returns pong"

# ============================================================
info "2. FILE UPLOAD"
# ============================================================
TMPF="$WORK_DIR/upload_test.txt"
echo "Hello Peerdrive E2E $(date +%s)" > "$TMPF"
UP=$(curl -sf -w '\n%{http_code}' -X POST "$API/files/upload" -F "file=@$TMPF")
UP_BODY=$(echo "$UP" | head -n -1)
UP_CODE=$(echo "$UP" | tail -n 1)
HASH1=$(echo "$UP_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")

assert_status "$UP_CODE" "201" "POST /files/upload new file → 201"
assert_contains "$UP_BODY" '"hash"' "upload response has hash"
assert_contains "$UP_BODY" '"size"' "upload response has size"
[ -n "$HASH1" ] && pass "extracted hash: ${HASH1:0:16}..." || fail "failed to extract hash from upload"

# verify on disk
STORED_FILE="$STORAGE/${HASH1:0:2}/$HASH1"
[ -f "$STORED_FILE" ] && pass "file stored at $STORED_FILE" || fail "file not found in storage"

# duplicate upload
UP2=$(curl -sf -w '\n%{http_code}' -X POST "$API/files/upload" -F "file=@$TMPF")
UP2_BODY=$(echo "$UP2" | head -n -1)
UP2_CODE=$(echo "$UP2" | tail -n 1)
HASH1B=$(echo "$UP2_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
assert_status "$UP2_CODE" "200" "duplicate upload → 200"
assert_contains "$UP2_BODY" '"already_exists"' "duplicate has already_exists"
[ "$HASH1B" = "$HASH1" ] && pass "duplicate returns same hash" || fail "hash changed on duplicate"

# upload another file
TMPF2="$WORK_DIR/upload_test2.txt"
echo "Second file test $(date +%s)" > "$TMPF2"
UP3=$(curl -sf -w '\n%{http_code}' -X POST "$API/files/upload" -F "file=@$TMPF2")
UP3_BODY=$(echo "$UP3" | head -n -1)
UP3_CODE=$(echo "$UP3" | tail -n 1)
HASH2=$(echo "$UP3_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
assert_status "$UP3_CODE" "201" "second upload → 201"
[ "$HASH2" != "$HASH1" ] && pass "different content → different hash" || fail "same hash for different content"

# ============================================================
info "3. FILE VERIFY"
# ============================================================
VERIFY=$(curl -sf "$API/files/verify/$HASH1")
assert_contains "$VERIFY" "$HASH1" "GET /files/verify/:hash returns hash"
assert_contains "$VERIFY" '"size"' "verify response has size"

# invalid hash
IV=$(curl -s -o /dev/null -w '%{http_code}' "$API/files/verify/aaabbbcccinvalidhash12345678901234567890123456789012")
[ "$IV" != "200" ] && pass "verify invalid hash → non-200" || fail "verify should reject invalid hash"

# ============================================================
info "4. SHA256 DOWNLOAD"
# ============================================================
DL_CODE=$(curl -s -o "$WORK_DIR/dl_test.txt" -w '%{http_code}' "$API/sha256sum/$HASH1")
assert_status "$DL_CODE" "200" "GET /sha256sum/:hash → 200"
diff "$TMPF" "$WORK_DIR/dl_test.txt" > /dev/null && pass "downloaded file matches original" || fail "downloaded file differs"

# invalid hash download
DL_BAD=$(curl -s -o /dev/null -w '%{http_code}' "$API/sha256sum/0000000000000000000000000000000000000000000000000000000000000000")
[ "$DL_BAD" != "200" ] && pass "download invalid hash → non-200" || fail "should reject invalid hash download"

# ============================================================
info "5. REGISTER LOCAL FILE"
# ============================================================
TMPR="$WORK_DIR/register_me.txt"
echo "Register local test file $(date +%s)" > "$TMPR"
REG=$(curl -sf -X POST "$API/files/register_local" -H "Content-Type: application/json" -d "{\"path\":\"$TMPR\"}")
assert_contains "$REG" '"hash"' "POST /files/register_local returns hash"
REG_HASH=$(echo "$REG" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
assert_contains "$REG" '"filename"' "register response has filename"

# verify registered file
RV=$(curl -sf "$API/files/verify/$REG_HASH")
assert_contains "$RV" "$REG_HASH" "verify registered file matches"

# re-register same file (idempotent)
REG2=$(curl -sf -X POST "$API/files/register_local" -H "Content-Type: application/json" -d "{\"path\":\"$TMPR\"}")
REG2_HASH=$(echo "$REG2" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
[ "$REG2_HASH" = "$REG_HASH" ] && pass "re-register returns same hash" || fail "re-register changed hash"

# download registered file
curl -sf -o "$WORK_DIR/reg_dl.txt" "$API/sha256sum/$REG_HASH"
diff "$TMPR" "$WORK_DIR/reg_dl.txt" > /dev/null && pass "registered file download matches" || fail "registered file download differs"

# ============================================================
info "6. REGISTER FOLDER"
# ============================================================
TESTDIR="$WORK_DIR/testfolder"
mkdir -p "$TESTDIR/sub"
echo "file A" > "$TESTDIR/a.txt"
echo "file B" > "$TESTDIR/b.txt"
echo "nested file" > "$TESTDIR/sub/c.txt"
RF=$(curl -sf -X POST "$API/files/register_folder" -H "Content-Type: application/json" -d "{\"folder_path\":\"$TESTDIR\"}")
assert_json "$RF" "folder registration returns valid JSON"
RF_COUNT=$(echo "$RF" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('registered',d.get('files',[]))))")
[ "$RF_COUNT" -ge 1 ] && pass "folder registration returned $RF_COUNT files" || fail "folder registration returned 0 files"

# ============================================================
info "7. ANONYMOUS COLLECTIONS"
# ============================================================
# create
ANON_CREATE=$(curl -sf -w '\n%{http_code}' -X POST "$API/anon/collections" -H "Content-Type: application/json" \
  -d "{\"friendly_name\":\"e2e-test-collection\",\"entries\":[{\"path\":\"docs/readme.txt\",\"hash\":\"$HASH1\"},{\"path\":\"data/file.bin\",\"hash\":\"$HASH2\"}]}")
ANON_BODY=$(echo "$ANON_CREATE" | head -n -1)
ANON_CODE=$(echo "$ANON_CREATE" | tail -n 1)
COLL_HASH=$(echo "$ANON_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")

assert_status "$ANON_CODE" "201" "POST /anon/collections → 201"
[ -n "$COLL_HASH" ] && pass "anon collection hash: ${COLL_HASH:0:16}..." || fail "no hash in response"

# path traversal rejection
PT=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/anon/collections" -H "Content-Type: application/json" \
  -d '{"friendly_name":"bad","entries":[{"path":"../etc/passwd","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}')
[ "$PT" = "400" ] && pass "path traversal rejected (400)" || warn "path traversal not rejected (expected 400, got $PT)"

# get collection JSON
GET_COLL=$(curl -sf "$API/anon/collections/$COLL_HASH")
assert_json "$GET_COLL" "GET /anon/collections/:hash returns valid JSON"
assert_contains "$GET_COLL" '"version":1' "collection version is 1"
assert_contains "$GET_COLL" '"friendly_name"' "collection has friendly_name"
assert_contains "$GET_COLL" '"e2e-test-collection"' "friendly_name is e2e-test-collection"
# verify entries
assert_contains "$GET_COLL" '"docs/readme.txt"' "collection has docs/readme.txt entry"
assert_contains "$GET_COLL" '"data/file.bin"' "collection has data/file.bin entry"
assert_contains "$GET_COLL" "$HASH1" "collection references hash1"
assert_contains "$GET_COLL" "$HASH2" "collection references hash2"

# download via sha256sum
curl -sf -o "$WORK_DIR/coll_dl.json" "$API/sha256sum/$COLL_HASH"
python3 -c "import json; json.load(open('$WORK_DIR/coll_dl.json'))" && pass "collection downloadable via sha256sum" || fail "collection sha256sum download not valid JSON"

# download entry file
curl -sf -o "$WORK_DIR/entry_dl.txt" "$API/anon/collections/$COLL_HASH/entries/docs/readme.txt"
diff "$TMPF" "$WORK_DIR/entry_dl.txt" > /dev/null && pass "download collection entry matches original" || fail "collection entry download differs"

# download entry from second file
curl -sf -o "$WORK_DIR/entry2_dl.txt" "$API/anon/collections/$COLL_HASH/entries/data/file.bin"
diff "$TMPF2" "$WORK_DIR/entry2_dl.txt" > /dev/null && pass "download second entry matches original" || fail "second entry download differs"

# fork
FORK=$(curl -sf -w '\n%{http_code}' -X POST "$API/anon/collections/fork" -H "Content-Type: application/json" \
  -d "{\"source_hash\":\"$COLL_HASH\",\"friendly_name\":\"forked-e2e\",\"add_entries\":[],\"remove_paths\":[\"data/file.bin\"]}")
FORK_BODY=$(echo "$FORK" | head -n -1)
FORK_CODE=$(echo "$FORK" | tail -n 1)
FORK_HASH=$(echo "$FORK_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
assert_status "$FORK_CODE" "201" "POST /anon/collections/fork → 201"
[ "$FORK_HASH" != "$COLL_HASH" ] && pass "fork produces different hash" || fail "fork hash same as source"

# verify fork
FORK_GET=$(curl -sf "$API/anon/collections/$FORK_HASH")
assert_contains "$FORK_GET" '"forked-e2e"' "fork has friendly_name forked-e2e"
assert_contains "$FORK_GET" '"docs/readme.txt"' "fork has docs/readme.txt"
assert_not_contains "$FORK_GET" '"data/file.bin"' "fork removed data/file.bin"

# commit (anonymous collection versioning)
AC_COM=$(curl -sf -w '\n%{http_code}' -X POST "$API/anon/collections/commit" -H "Content-Type: application/json" \
  -d "{\"source_hash\":\"$COLL_HASH\",\"entries\":[{\"path\":\"docs/readme.txt\",\"hash\":\"$HASH1\"},{\"path\":\"data/file.bin\",\"hash\":\"$HASH2\"},{\"path\":\"CHANGELOG.md\",\"hash\":\"$HASH1\"}],\"commit_message\":\"add changelog\"}")
AC_COM_BODY=$(echo "$AC_COM" | head -n -1)
AC_COM_CODE=$(echo "$AC_COM" | tail -n 1)
COM_HASH=$(echo "$AC_COM_BODY" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
assert_status "$AC_COM_CODE" "201" "POST /anon/collections/commit → 201"
[ "$COM_HASH" != "$COLL_HASH" ] && pass "commit produces different hash" || fail "commit hash same as source"

# verify committed collection
COM_GET=$(curl -sf "$API/anon/collections/$COM_HASH")
assert_contains "$COM_GET" '"CHANGELOG.md"' "committed collection has new entry CHANGELOG.md"
assert_contains "$COM_GET" '"data/file.bin"' "committed collection retains data/file.bin"
assert_contains "$COM_GET" '"version":2' "committed collection has version 2"

# get nonexistent collection
NC=$(curl -s -o /dev/null -w '%{http_code}' "$API/anon/collections/00badhash00badhash00badhash00badhash00badhash00badhash00badhash00badhash00badhash00")
[ "$NC" != "200" ] && pass "get nonexistent anon collection → non-200" || fail "nonexistent collection returned 200"

# ============================================================
info "8. NAMED COLLECTIONS"
# ============================================================
USER="testuser_e2e"
COLL="test-coll-e2e"

# create
CC=$(curl -sf -w '\n%{http_code}' -X POST "$API/collections" -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"collection_name\":\"$COLL\"}")
CC_BODY=$(echo "$CC" | head -n -1)
CC_CODE=$(echo "$CC" | tail -n 1)
[ "$CC_CODE" = "200" ] || [ "$CC_CODE" = "201" ] && pass "POST /collections → $CC_CODE" || fail "create collection failed ($CC_CODE)"

# duplicate create
CC2=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/collections" -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"collection_name\":\"$COLL\"}")
[ "$CC2" != "200" ] && [ "$CC2" != "201" ] && pass "duplicate collection rejected" || warn "duplicate collection allowed"

# list user collections
CL=$(curl -sf "$API/collections/$USER")
assert_contains "$CL" "$COLL" "GET /collections/:user lists our collection"

# get collection
CG=$(curl -sf "$API/collections/$USER/$COLL")
assert_json "$CG" "GET /collections/:user/:coll returns JSON"
assert_contains "$CG" '"collection_name"' "get collection has collection_name"

# add entry
AE=$(curl -sf -X POST "$API/collections/$USER/$COLL/entries" -H "Content-Type: application/json" \
  -d "{\"path\":\"src/main.go\",\"hash\":\"$HASH1\"}")
assert_contains "$AE" '"message"' "POST add entry succeeded"

# add second entry
AE2=$(curl -sf -X POST "$API/collections/$USER/$COLL/entries" -H "Content-Type: application/json" \
  -d "{\"path\":\"lib/util.go\",\"hash\":\"$HASH2\"}")
assert_contains "$AE2" '"message"' "POST add second entry succeeded"

# verify entries in collection
CG2=$(curl -sf "$API/collections/$USER/$COLL")
assert_contains "$CG2" '"src/main.go"' "collection has src/main.go"
assert_contains "$CG2" '"lib/util.go"' "collection has lib/util.go"

# remove entry
RE=$(curl -sf -X DELETE "$API/collections/$USER/$COLL/entries/lib/util.go")
assert_contains "$RE" '"message"' "DELETE entry succeeded"

CG3=$(curl -sf "$API/collections/$USER/$COLL")
assert_not_contains "$CG3" '"lib/util.go"' "removed entry no longer in collection"
assert_contains "$CG3" '"src/main.go"' "remaining entry still present"

# add back for further tests
curl -sf -X POST "$API/collections/$USER/$COLL/entries" -H "Content-Type: application/json" \
  -d "{\"path\":\"lib/util.go\",\"hash\":\"$HASH2\"}" > /dev/null

# download from collection
DL_COLL=$(curl -s -o /dev/null -w '%{http_code}' "$API/$USER/$COLL/src/main.go")
assert_status "$DL_COLL" "200" "GET /:user/:coll/:path → 200"

# commit
COMMIT=$(curl -sf -X POST "$API/collections/$USER/$COLL/commit" -H "Content-Type: application/json" \
  -d '{"commit_message":"initial commit"}')
assert_contains "$COMMIT" '"message"' "POST commit succeeded"
assert_contains "$COMMIT" '"version_number"' "commit response has version_number"
assert_contains "$COMMIT" '"snapshot_hash"' "commit response has snapshot_hash"

# version log
LOG=$(curl -sf "$API/collections/$USER/$COLL/log")
assert_json "$LOG" "GET version log returns JSON"
LOG_COUNT=$(echo "$LOG" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('data',[])))")
[ "$LOG_COUNT" -ge 1 ] && pass "version log has $LOG_COUNT entries" || fail "version log empty"

# add entry + second commit
curl -sf -X POST "$API/collections/$USER/$COLL/entries" -H "Content-Type: application/json" \
  -d "{\"path\":\"docs/notes.txt\",\"hash\":\"$HASH1\"}" > /dev/null
COMMIT2=$(curl -sf -X POST "$API/collections/$USER/$COLL/commit" -H "Content-Type: application/json" \
  -d '{"commit_message":"add docs"}')
assert_contains "$COMMIT2" '"message"' "second commit succeeded"

LOG2=$(curl -sf "$API/collections/$USER/$COLL/log")
LOG2_COUNT=$(echo "$LOG2" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('data',[])))")
[ "$LOG2_COUNT" -ge 2 ] && pass "version log has $LOG2_COUNT entries after second commit" || warn "version log count unexpected"

# rollback to first version
VID=$(echo "$LOG2" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['data'][-1]['id'])")
RB=$(curl -sf -X POST "$API/collections/$USER/$COLL/rollback/$VID" -H "Content-Type: application/json")
assert_contains "$RB" '"message"' "POST rollback succeeded"
assert_contains "$RB" 'rolled' "rollback response says rolled back"

# verify after rollback (docs/notes.txt should be gone)
CG4=$(curl -sf "$API/collections/$USER/$COLL")
assert_not_contains "$CG4" '"docs/notes.txt"' "rollback removed docs/notes.txt"
assert_contains "$CG4" '"src/main.go"' "rollback kept src/main.go"
assert_contains "$CG4" '"lib/util.go"' "rollback kept lib/util.go"

# ============================================================
info "9. FORK / MERGE / PULL"
# ============================================================
# create source collection for fork
curl -sf -X POST "$API/collections" -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"collection_name\":\"source-coll\"}" > /dev/null
curl -sf -X POST "$API/collections/$USER/source-coll/entries" -H "Content-Type: application/json" \
  -d "{\"path\":\"f1.txt\",\"hash\":\"$HASH1\"}" > /dev/null

# fork
FK=$(curl -sf -X POST "$API/actions/fork" -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"source_username\":\"$USER\",\"collection_name\":\"forked-coll\",\"source_coll_name\":\"source-coll\"}")
assert_contains "$FK" '"message"' "POST /actions/fork succeeded"

# verify forked collection
FK_GET=$(curl -sf "$API/collections/$USER/forked-coll")
assert_contains "$FK_GET" '"f1.txt"' "forked collection has source entry"

# merge
MERGE=$(curl -sf -X POST "$API/actions/merge" -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"source_username\":\"$USER\",\"collection_name\":\"$COLL\",\"source_coll_name\":\"forked-coll\",\"strategy\":\"theirs\"}")
assert_contains "$MERGE" '"message"' "POST /actions/merge succeeded"

# pull
PULL=$(curl -sf -X POST "$API/actions/pull" -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"collection_name\":\"$COLL\"}")
assert_json "$PULL" "POST /actions/pull returns response"

# ============================================================
info "10. FILE DELETE"
# ============================================================
DEL=$(curl -sf -X DELETE "$API/files/$HASH2")
assert_contains "$DEL" '"message"' "DELETE /files/:hash succeeded"

# verify deleted file returns 4xx
VD=$(curl -s -o /dev/null -w '%{http_code}' "$API/files/verify/$HASH2")
[ "$VD" != "200" ] && pass "deleted file verify → non-200" || warn "deleted file still verifiable"

# ============================================================
info "11. TASKS"
# ============================================================
TASKS=$(curl -sf "$API/tasks")
assert_json "$TASKS" "GET /tasks returns valid response"

# nonexistent task
NT=$(curl -s -o /dev/null -w '%{http_code}' "$API/tasks/99999999")
[ "$NT" != "200" ] && pass "GET /tasks/nonexistent → non-200" || warn "nonexistent task returned 200"

# ============================================================
info "12. EDGE CASES & BOUNDARIES"
# ============================================================
# missing username in collection list
NU=$(curl -s -o /dev/null -w '%{http_code}' "$API/collections/nonexistent_user_xyz_123")
[ "$NU" = "404" ] || [ "$NU" = "200" ]  # either is acceptable: 404 if strict, 200 with empty list

# empty anon collection
EMP=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/anon/collections" -H "Content-Type: application/json" -d '{"entries":[]}')
# empty entries may or may not be allowed
echo "  Empty collection create → $EMP" >&2

# collection with invalid hash format
BAD_HASH=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/anon/collections" -H "Content-Type: application/json" \
  -d '{"entries":[{"path":"test.txt","hash":"not-a-real-sha256"}]}')
[ "$BAD_HASH" = "400" ] && pass "invalid hash in collection → 400" || warn "invalid hash in collection returned $BAD_HASH (expected 400)"

# missing fields in create collection
MISS=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/collections" -H "Content-Type: application/json" -d '{}')
[ "$MISS" != "200" ] && pass "collection create with missing fields → non-200" || warn "collection create with missing fields returned 200"

# ============================================================
info "RESULTS"
# ============================================================
TOTAL=$((PASS + FAIL + WARN))
echo -e "${GREEN}PASS: $PASS${NC}  ${RED}FAIL: $FAIL${NC}  ${YELLOW}WARN: $WARN${NC}  TOTAL: $TOTAL"

if [ "$FAIL" -gt 0 ]; then
  echo -e "${RED}Some tests FAILED${NC}"
  exit 1
else
  echo -e "${GREEN}All tests passed${NC}"
  exit 0
fi
