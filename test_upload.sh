#!/bin/bash
set -e

echo "=== Peerdrive Upload Test ==="

BASE_URL="http://localhost:3000"
TEST_DIR="/tmp/peerdrive_upload_test"
mkdir -p "$TEST_DIR"

echo "First Upload" > "$TEST_DIR/new.txt"
echo "Second Upload" > "$TEST_DIR/new2.txt"

# ── Helper ──
upload() {
  curl -s -x "" -w "\n%{http_code}" -X POST "$BASE_URL/files/upload" -F "file=@$1"
}

# ── Test 1: Upload new file ──
echo "=== Test 1: Upload new file ==="
HTTP_RESPONSE=$(upload "$TEST_DIR/new.txt")
HTTP_BODY=$(echo "$HTTP_RESPONSE" | sed '$d')
HTTP_CODE=$(echo "$HTTP_RESPONSE" | tail -n1)
echo "Code: $HTTP_CODE  Body: $HTTP_BODY"

if [ "$HTTP_CODE" != "201" ]; then
  echo "FAIL: Expected 201"
  exit 1
fi

HASH=$(echo "$HTTP_BODY" | grep -oP '(?<="hash":")[^"]*')
SIZE=$(echo "$HTTP_BODY" | grep -oP '(?<="size":)[0-9]+')

if [ -z "$HASH" ] || [ "$SIZE" -le 0 ]; then
  echo "FAIL: Bad metadata"
  exit 1
fi
echo "Hash=$HASH Size=$SIZE"

# Verify download
curl -s -x "" -o "$TEST_DIR/downloaded.txt" "$BASE_URL/sha256sum/$HASH"
if ! diff "$TEST_DIR/new.txt" "$TEST_DIR/downloaded.txt"; then
  echo "FAIL: Content mismatch"
  exit 1
fi
echo "PASS: New file upload"

# Verify file exists on disk
STORAGE_PATH="./storage/${HASH:0:2}/$HASH"
if [ ! -f "$STORAGE_PATH" ]; then
  echo "FAIL: File not at $STORAGE_PATH"
  exit 1
fi
echo "PASS: File saved to disk"

# ── Test 2: Duplicate file ──
echo "=== Test 2: Duplicate upload ==="
HTTP_RESPONSE2=$(upload "$TEST_DIR/new.txt")
HTTP_BODY2=$(echo "$HTTP_RESPONSE2" | sed '$d')
HTTP_CODE2=$(echo "$HTTP_RESPONSE2" | tail -n1)
echo "Code: $HTTP_CODE2  Body: $HTTP_BODY2"

if [ "$HTTP_CODE2" != "200" ]; then
  echo "FAIL: Expected 200 for duplicate"
  exit 1
fi

ALREADY=$(echo "$HTTP_BODY2" | grep -oP '(?<="already_exists":)[a-z]+')
if [ "$ALREADY" != "true" ]; then
  echo "FAIL: Expected already_exists=true"
  exit 1
fi

HASH2=$(echo "$HTTP_BODY2" | grep -oP '(?<="hash":")[^"]*')
if [ "$HASH2" != "$HASH" ]; then
  echo "FAIL: Hash changed"
  exit 1
fi

curl -s -x "" -o "$TEST_DIR/downloaded2.txt" "$BASE_URL/sha256sum/$HASH2"
if ! diff "$TEST_DIR/new.txt" "$TEST_DIR/downloaded2.txt"; then
  echo "FAIL: Content mismatch"
  exit 1
fi
echo "PASS: Duplicate upload"

# ── Test 3: Another new file ──
echo "=== Test 3: Upload another new file ==="
HTTP_RESPONSE3=$(upload "$TEST_DIR/new2.txt")
HTTP_BODY3=$(echo "$HTTP_RESPONSE3" | sed '$d')
HTTP_CODE3=$(echo "$HTTP_RESPONSE3" | tail -n1)

if [ "$HTTP_CODE3" != "201" ]; then
  echo "FAIL: Expected 201"
  exit 1
fi

HASH3=$(echo "$HTTP_BODY3" | grep -oP '(?<="hash":")[^"]*')
curl -s -x "" -o "$TEST_DIR/downloaded3.txt" "$BASE_URL/sha256sum/$HASH3"
if ! diff "$TEST_DIR/new2.txt" "$TEST_DIR/downloaded3.txt"; then
  echo "FAIL: Content mismatch"
  exit 1
fi
echo "PASS: Second new file"

# ── Cleanup ──
rm -rf "$TEST_DIR"

echo ""
echo "=== All upload tests passed ==="