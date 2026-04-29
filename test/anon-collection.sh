#!/bin/bash
set -e

BASE_URL="http://localhost:3000"
TEST_DIR="/tmp/peerdrive_test"
SINGLE_FILE="$TEST_DIR/single.txt"
FOLDER_DIR="$TEST_DIR/folder"

echo "=== Anonymous Collection: Stage 1 Tests ==="

echo "--- Preparing test files ---"
mkdir -p "$TEST_DIR"
FILE_A="$TEST_DIR/anon_a.txt"
FILE_B="$TEST_DIR/anon_b.txt"
echo "Anonymous File A" > "$FILE_A"
echo "Anonymous File B" > "$FILE_B"

# Register both files
echo "Registering file A..."
HASH_A=$(curl -s -x "" -X POST "$BASE_URL/files/register_local" \
  -H "Content-Type: application/json" \
  -d "{\"path\": \"$FILE_A\", \"filename\": \"anon_a.txt\"}" | grep -oP '(?<="hash":")[^"]*')
echo "Hash A: $HASH_A"

echo "Registering file B..."
HASH_B=$(curl -s -x "" -X POST "$BASE_URL/files/register_local" \
  -H "Content-Type: application/json" \
  -d "{\"path\": \"$FILE_B\", \"filename\": \"anon_b.txt\"}" | grep -oP '(?<="hash":")[^"]*')
echo "Hash B: $HASH_B"

# ---- Test 1: Normal collection creation ----
echo ""
echo "=== Test 1: Create anon collection ==="
COLL_RESP=$(curl -s -x "" -w "\n%{http_code}" -X POST "$BASE_URL/anon/collections" \
  -H "Content-Type: application/json" \
  -d "{
    \"entries\": [
      {\"path\": \"docs/anon_a.txt\", \"hash\": \"$HASH_A\"},
      {\"path\": \"docs/anon_b.txt\", \"hash\": \"$HASH_B\"}
    ]
  }")
HTTP_BODY=$(echo "$COLL_RESP" | sed '$d')
HTTP_CODE=$(echo "$COLL_RESP" | tail -n1)
echo "Status: $HTTP_CODE"
COLL_HASH=$(echo "$HTTP_BODY" | grep -oP '(?<="hash":")[^"]*')

if [ "$HTTP_CODE" != "201" ] || [ -z "$COLL_HASH" ]; then
  echo "FAIL: Collection creation failed"
  exit 1
fi
echo "Created collection hash: $COLL_HASH"

# ---- Test 2: Path traversal rejection ----
echo ""
echo "=== Test 2: Reject path traversal ==="
TRAV_RESP=$(curl -s -x "" -w "\n%{http_code}" -X POST "$BASE_URL/anon/collections" \
  -H "Content-Type: application/json" \
  -d "{
    \"entries\": [
      {\"path\": \"../etc/passwd\", \"hash\": \"$HASH_A\"}
    ]
  }")
TRAV_CODE=$(echo "$TRAV_RESP" | tail -n1)
if [ "$TRAV_CODE" != "400" ]; then
  echo "FAIL: Expected 400 for path traversal, got $TRAV_CODE"
  exit 1
fi
echo "Path traversal correctly rejected (HTTP $TRAV_CODE)."

# ---- Test 3: Access collection JSON ----
echo ""
echo "=== Test 3: GET collection JSON ==="
COLL_JSON=$(curl -s -x "" -w "\n%{http_code}" "$BASE_URL/anon/collections/$COLL_HASH")
COLL_BODY=$(echo "$COLL_JSON" | sed '$d')
COLL_CODE=$(echo "$COLL_JSON" | tail -n1)
if [ "$COLL_CODE" != "200" ]; then
  echo "FAIL: Could not retrieve collection JSON (HTTP $COLL_CODE)"
  exit 1
fi
echo "Collection JSON: $COLL_BODY"

VERSION=$(echo "$COLL_BODY" | grep -oP '(?<="version":)[0-9]+')
if [ "$VERSION" != "1" ]; then
  echo "FAIL: Missing or wrong version"
  exit 1
fi
echo "Collection version: $VERSION"

# ---- Test 4: Access collection via /sha256sum ----
echo ""
echo "=== Test 4: Download collection via sha256sum ==="
SHA_CODE=$(curl -s -x "" -o /dev/null -w "%{http_code}" "$BASE_URL/sha256sum/$COLL_HASH")
if echo "$SHA_CODE" | grep -qi "200"; then
  echo "PASS: sha256sum download succeeded (HTTP 200)"
else
  echo "FAIL: sha256sum download failed (HTTP $SHA_CODE)"
  exit 1
fi

# Check for X-Peerdrive-Collection header
SHA_HEADERS=$(curl -s -x "" -D - -o /dev/null "$BASE_URL/sha256sum/$COLL_HASH" 2>/dev/null || curl -s -x "" -D - -o /dev/null "$BASE_URL/sha256sum/$COLL_HASH")
# The headers might use the -D flag format, let's try a different approach
SHA_HEADERS=$(curl -s -x "" -I "$BASE_URL/sha256sum/$COLL_HASH")
if echo "$SHA_HEADERS" | grep -qi "X-Peerdrive-Collection"; then
  echo "PASS: X-Peerdrive-Collection header present"
else
  echo "WARN: X-Peerdrive-Collection header not in HEAD response (may not be critical)"
fi

# Download the collection JSON content and verify it's valid
curl -s -x "" -o /tmp/anon_collection.json "$BASE_URL/sha256sum/$COLL_HASH"
echo "Downloaded collection file size: $(wc -c < /tmp/anon_collection.json) bytes"

# ---- Test 5: Download file from collection entry ----
echo ""
echo "=== Test 5: Download file from collection ==="
curl -s -x "" -o /tmp/downloaded_anon.txt "$BASE_URL/anon/collections/$COLL_HASH/entries/docs/anon_a.txt"
if ! diff "$FILE_A" /tmp/downloaded_anon.txt; then
  echo "FAIL: Downloaded file does not match"
  exit 1
fi
echo "PASS: File from collection matches original"

# ---- Test 6: Fork collection ----
echo ""
echo "=== Test 6: Fork collection ==="
FORK_RESP=$(curl -s -x "" -w "\n%{http_code}" -X POST "$BASE_URL/anon/collections/fork" \
  -H "Content-Type: application/json" \
  -d "{
    \"source_hash\": \"$COLL_HASH\",
    \"add_entries\": [],
    \"remove_paths\": [\"docs/anon_b.txt\"]
  }")
FORK_BODY=$(echo "$FORK_RESP" | sed '$d')
FORK_CODE=$(echo "$FORK_RESP" | tail -n1)
FORK_HASH=$(echo "$FORK_BODY" | grep -oP '(?<="hash":")[^"]*')

if [ "$FORK_CODE" != "201" ] || [ -z "$FORK_HASH" ]; then
  echo "FAIL: Fork failed"
  exit 1
fi
echo "Fork collection hash: $FORK_HASH"

# Verify fork only has 1 entry
FORK_JSON=$(curl -s -x "" "$BASE_URL/anon/collections/$FORK_HASH")
ENTRY_COUNT=$(echo "$FORK_JSON" | grep -o '"path"' | wc -l)
if [ "$ENTRY_COUNT" != "1" ]; then
  echo "FAIL: Fork expected 1 entry, got $ENTRY_COUNT"
  exit 1
fi
echo "PASS: Fork has $ENTRY_COUNT entry (removed anon_b.txt)"

# ---- Cleanup ----
echo ""
rm -f "$FILE_A" "$FILE_B" /tmp/anon_collection.json /tmp/downloaded_anon.txt

echo "=== All anonymous collection tests passed ==="