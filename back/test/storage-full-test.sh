#!/bin/bash
set -e
B="http://127.0.0.1:3000"
P=0; F=0
pass() { echo "  [PASS] $1"; P=$((P+1)); }
fail() { echo "  [FAIL] $1"; F=$((F+1)); }
echo "Peerdrive Storage Test"
echo ""

# 1. Health
echo "--- 1. Health ---"
curl -s --noproxy "*" $B/ping | grep -q pong && pass "ping" || fail "ping"

# 2. Upload
echo "--- 2. Upload ---"
echo "test $(date)" > /tmp/st-test.txt
HASH=$(curl -s --noproxy "*" -X POST $B/files/upload -F "file=@/tmp/st-test.txt" | python3 -c "import sys,json;print(json.load(sys.stdin)['hash'])")
[ ${#HASH} -eq 64 ] && pass "upload (hash=$HASH)" || fail "upload"

# 3. SHA256 download
echo "--- 3. SHA256 Download ---"
CODE=$(curl -s --noproxy "*" -o /dev/null -w "%{http_code}" $B/sha256sum/$HASH)
[ "$CODE" = "200" ] && pass "SHA256 download" || fail "SHA256 download ($CODE)"

# 4. Range download
echo "--- 4. Range ---"
CODE=$(curl -s --noproxy "*" -o /dev/null -w "%{http_code}" -H "Range: bytes=0-3" $B/sha256sum/$HASH)
[ "$CODE" = "206" ] && pass "Range 206" || fail "Range ($CODE)"

# 5. Register local
echo "--- 5. Register Local ---"
curl -s --noproxy "*" -X POST $B/files/register_local -H 'Content-Type: application/json' -d '{"path":"/etc/hostname"}' | python3 -c "import sys,json;d=json.load(sys.stdin);exit(0 if d.get('hash') else 1)" && pass "register local" || fail "register local"

# 6. Register folder  
echo "--- 6. Register Folder ---"
curl -s --noproxy "*" -X POST $B/files/register_folder -H 'Content-Type: application/json' -d '{"folder_path":"/etc/ssl"}' | python3 -c "import sys,json;d=json.load(sys.stdin);exit(0 if d.get('count',0) > 0 else 1)" && pass "register folder" || fail "register folder"

# 7. File list
echo "--- 7. File List ---"
curl -s --noproxy "*" "$B/files?sort=time" | python3 -c "import sys,json;d=json.load(sys.stdin);exit(0 if len(d)>0 else 1)" && pass "list files" || fail "list files"

# 8. Verify
echo "--- 8. Verify ---"
curl -s --noproxy "*" $B/files/verify/$HASH | python3 -c "import sys,json;d=json.load(sys.stdin);exit(0 if d.get('hash') else 1)" && pass "verify" || fail "verify"

# 9. Browse
echo "--- 9. Browse ---"
curl -s --noproxy "*" "$B/files/browse?path=/etc" | python3 -c "import sys,json;d=json.load(sys.stdin);exit(0 if isinstance(d,list) else 1)" && pass "browse" || fail "browse"

# 10. Collection CRUD
echo "--- 10. Collection ---"
C=$(curl -s --noproxy "*" -X POST $B/anon/collections -H 'Content-Type: application/json' -d "{\"entries\":[{\"path\":\"t.txt\",\"hash\":\"$HASH\"}],\"friendly_name\":\"Test\"}")
CH=$(echo "$C" | python3 -c "import sys,json;print(json.load(sys.stdin).get('hash',''))")
[ ${#CH} -eq 64 ] && pass "create collection" || fail "create collection"
curl -s --noproxy "*" $B/anon/collections/$CH | python3 -c "import sys,json;d=json.load(sys.stdin);exit(0 if d.get('hash') else 1)" && pass "get collection" || fail "get collection"

# 11. Share
echo "--- 11. Share ---"
S=$(curl -s --noproxy "*" -X POST $B/shares -H 'Content-Type: application/json' -d "{\"hash\":\"$HASH\",\"type\":\"file\"}")
ST=$(echo "$S" | python3 -c "import sys,json;print(json.load(sys.stdin).get('token',''))")
[ -n "$ST" ] && pass "create share" || fail "create share"
curl -s --noproxy "*" $B/s/$ST -o /dev/null -w "%{http_code}" | grep -q 200 && pass "access share" || fail "access share"

# 12. CID download
echo "--- 12. CID ---"
CID=$(echo -n "test" | sha256sum | cut -d' ' -f1)
curl -s --noproxy "*" -X POST $B/files/upload -F "file=@/tmp/st-test.txt" > /dev/null
CODE=$(curl -s --noproxy "*" -o /dev/null -w "%{http_code}" $B/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn 2>/dev/null)
[ "$CODE" = "200" ] && pass "CID download ($CODE)" || fail "CID download ($CODE)"

echo ""
echo "PASS: $P  FAIL: $F  TOTAL: $((P+F))"
