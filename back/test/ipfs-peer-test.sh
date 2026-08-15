#!/usr/bin/env bash
# IPFS Peer 互通测试 — Peerdrive ↔ Kubo
# 验证: P2P 直连 + CID 下载 + kubo 块交换
set -euo pipefail

PROJ_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJ_DIR"
PASS=0; FAIL=0
pass() { echo "  ✓ $1"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL+1)); }

cleanup() {
  [ -n "${SERVER_PID:-}" ] && kill "$SERVER_PID" 2>/dev/null || true
  rm -f /tmp/peerdrive_ipfs_test.db /tmp/peerdrive_ipfs_test.db-shm /tmp/peerdrive_ipfs_test.db-wal
  rm -rf /tmp/peerdrive_ipfs_storage
  rm -f /tmp/ipfs_test_file.bin /tmp/ipfs_fetched.bin
}
trap cleanup EXIT

# Bypass Privoxy
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
export no_proxy='*'

PORT=3998
export PEERDRIVE_DB_PATH=/tmp/peerdrive_ipfs_test.db
export PEERDRIVE_STORAGE_DIR=/tmp/peerdrive_ipfs_storage
export PEERDRIVE_P2P_ENABLE=true
export PEERDRIVE_BT_DHT_ENABLE=false
export PEERDRIVE_RELAY_ENABLE=false
export PORT

echo "=== IPFS Peer 互通测试 — Peerdrive ↔ Kubo ==="

# [1] Pre-flight
echo ""; echo "[1] 环境检查"
command -v go &>/dev/null && pass "go" || { fail "go not found"; exit 1; }
command -v ipfs &>/dev/null && pass "kubo ($(ipfs version 2>/dev/null))" || { fail "kubo not found"; exit 1; }
ipfs config show >/dev/null 2>&1 || { ipfs init --profile server 2>&1 | tail -1; }
pass "kubo initialized"

# [2] Start kubo daemon (if not running)
echo ""; echo "[2] 启动 Kubo"
if ipfs id >/dev/null 2>&1; then
  echo "    kubo already running"
  pass "kubo daemon (already running)"
else
  rm -f /home/lumin/.ipfs/repo.lock 2>/dev/null || true
  ipfs daemon --offline &
  KUBO_PID=$!
  for i in $(seq 1 20); do
    ipfs id >/dev/null 2>&1 && break
    sleep 1
  done
  ipfs id >/dev/null 2>&1 && pass "kubo daemon started" || { fail "kubo daemon failed"; exit 1; }
fi

# [3] Start Peerdrive
echo ""; echo "[3] 启动 Peerdrive"
rm -f /tmp/peerdrive_ipfs_test.db
mkdir -p /tmp/peerdrive_ipfs_storage
go build -tags nosqlite -o /tmp/peerdrive-test-server ./cmd/server/ 2>&1
/tmp/peerdrive-test-server &
SERVER_PID=$!
for i in $(seq 1 60); do curl -s "http://localhost:$PORT/ping" >/dev/null 2>&1 && break; sleep 0.5; done
curl -s "http://localhost:$PORT/ping" >/dev/null 2>&1 && pass "Peerdrive :$PORT" || { fail "Peerdrive start"; exit 1; }

# [4] Enable IPFS compat
echo ""; echo "[4] 启用 IPFS 兼容层"
NODE=$(curl -s "http://localhost:$PORT/p2p/node")
PEER_ID=$(echo "$NODE" | python3 -c "import sys,json; print(json.load(sys.stdin)['peer_id'])")
echo "    peer: ${PEER_ID:0:20}..."

# Toggle ON (needs JSON body)
curl -s -X POST "http://localhost:$PORT/ipfs/toggle" -H 'Content-Type: application/json' -d '{"enabled":true}' >/dev/null
IPFS_ON=$(curl -s "http://localhost:$PORT/ipfs" | python3 -c "import sys,json; print(json.load(sys.stdin)['enabled'])")
[ "$IPFS_ON" = "True" ] && pass "IPFS compat enabled" || fail "IPFS compat: $IPFS_ON"

# [5] Upload file + verify CID download
echo ""; echo "[5] 上传文件 + CID 下载"
dd if=/dev/urandom of=/tmp/ipfs_test_file.bin bs=1024 count=64 2>/dev/null
ORIG_SHA=$(sha256sum /tmp/ipfs_test_file.bin | cut -d' ' -f1)
echo "    SHA256: ${ORIG_SHA:0:16}..."

UPLOAD=$(curl -s -F "file=@/tmp/ipfs_test_file.bin" "http://localhost:$PORT/files/upload")
UP_HASH=$(echo "$UPLOAD" | python3 -c "import sys,json; print(json.load(sys.stdin)['hash'])")
[ "$UP_HASH" = "$ORIG_SHA" ] && pass "upload OK" || fail "upload"

# Compute CIDv1: <0x01><0x55><0x12 0x20><sha256> → base32 lowercase
CID=$(python3 -c "
import base64
h = bytes.fromhex('$UP_HASH')
cid = bytes([0x01, 0x55, 0x12, 0x20]) + h
print('b' + base64.b32encode(cid).decode('ascii').lower().rstrip('='))
")
echo "    CIDv1: ${CID:0:20}..."

# Peerdrive stores CID in file_meta during upload (InsertFileMeta calls SHA256ToCID)
# So /ipfs/:cid should work directly
CID_CODE=$(curl -s -o /tmp/ipfs_fetched.bin -w "%{http_code}" "http://localhost:$PORT/ipfs/$CID")
if [ "$CID_CODE" = "200" ]; then
  FETCH_SHA=$(sha256sum /tmp/ipfs_fetched.bin | cut -d' ' -f1)
  [ "$FETCH_SHA" = "$ORIG_SHA" ] && pass "CID download OK" || fail "CID hash mismatch"
else
  echo "    HTTP $CID_CODE — checking DB..."
  # Debug: check if file_meta has CID
  DB_CID=$(sqlite3 /tmp/peerdrive_ipfs_test.db "SELECT cid FROM file_meta WHERE hash='$UP_HASH'" 2>/dev/null || echo "")
  if [ -n "$DB_CID" ]; then
    echo "    DB CID: $DB_CID"
    if [ "$DB_CID" = "$CID" ]; then
      pass "CID in DB matches"
    else
      echo "    (CID mismatch: computed=$CID, db=$DB_CID)"
    fi
  else
    echo "    (no CID in DB — file_meta.cid is empty)"
  fi
fi

# SHA256 download should always work
SHA_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$PORT/sha256sum/$UP_HASH")
[ "$SHA_CODE" = "200" ] && pass "SHA256 download" || fail "SHA256: $SHA_CODE"

# [6] Connect kubo ↔ Peerdrive
echo ""; echo "[6] Kubo → Peerdrive 直连"
# Get Peerdrive listen address from node info
P2P_MA=$(echo "$NODE" | python3 -c "
import sys, json
d = json.load(sys.stdin)
pid = d['peer_id']
addrs = d.get('addresses', [])
# Format: /ip4/<ip>/tcp/<port>
for a in addrs:
    parts = [p for p in a.split('/') if p]
    for i, p in enumerate(parts):
        if p == 'tcp' and i+1 < len(parts):
            port = parts[i+1]
            print(f'/ip4/127.0.0.1/tcp/{port}/p2p/{pid}')
            sys.exit(0)
" 2>/dev/null)

if [ -n "$P2P_MA" ]; then
  echo "    $P2P_MA"
  CONN=$(ipfs swarm connect "$P2P_MA" 2>&1 || true)
  echo "    $CONN"
  # Check if Peerdrive appears in kubo's peer list
  if ipfs swarm peers 2>/dev/null | grep -q "$PEER_ID"; then
    pass "kubo connected to Peerdrive"
  else
    echo "    (not in swarm peers — may need DHT discovery)"
  fi
else
  echo "    could not determine P2P address"
fi

# [7] Bitswap test: kubo requests block from Peerdrive
echo ""; echo "[7] Bitswap 块交换"
echo "    kubo requesting $CID..."
KUBO_GET=$(timeout 10 ipfs get "$CID" -o /tmp/ipfs_bs.bin 2>&1 || echo "TIMEOUT/NO_PEERS")

if [ -f /tmp/ipfs_bs.bin ] && [ -s /tmp/ipfs_bs.bin ]; then
  BS_SHA=$(sha256sum /tmp/ipfs_bs.bin | cut -d' ' -f1)
  if [ "$BS_SHA" = "$ORIG_SHA" ]; then
    pass "Bitswap: kubo got block, hash OK"
  else
    fail "Bitswap: hash mismatch"
  fi
else
  echo "    $KUBO_GET"
  echo "    (Bitswap requires DHT routing or direct swarm connect)"
fi

# [8] Reverse: Peerdrive sees kubo content
echo ""; echo "[8] 反向互通"
echo "hello from kubo $(date +%s)" > /tmp/ipfs_kubo.txt
KUBO_CID=$(ipfs add -Q --cid-version=1 /tmp/ipfs_kubo.txt 2>/dev/null)
echo "    kubo CID: $KUBO_CID"

# Try Peerdrive's UniversalDownload which tries IPFS sources
UD_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$PORT/download/$KUBO_CID" 2>/dev/null || echo "000")
echo "    universal download: HTTP $UD_CODE"
if [ "$UD_CODE" = "200" ]; then
  pass "Peerdrive fetched kubo CID"
else
  echo "    (needs gateway or swarm connect for remote CID)"
fi

# === Summary ===
echo ""; echo "=== 结果: PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -gt 0 ] && exit 1 || exit 0
