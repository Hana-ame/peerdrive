#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PORT="${PORT:-3000}"
STORAGE_DIR="$ROOT/run/storage"
SHARED_DIR="$ROOT/run/shared"
LOGS_DIR="$ROOT/run/logs"

mkdir -p "$STORAGE_DIR/downloads" "$SHARED_DIR" "$LOGS_DIR"

echo "== [1/5] 生成测试图像与文档数据 =="
python3 "$ROOT/scripts/create-test-netdisk-data.py" "$SHARED_DIR"

echo "== [2/5] 检查端口 $PORT 并清理残留 =="
if command -v fuser >/dev/null 2>&1; then
  fuser -k -n tcp "$PORT" 2>/dev/null || true
fi
sleep 1

echo "== [3/5] 启动本地 peerdrive 节点 (端口: $PORT) =="
export PORT="$PORT"
export PEERDRIVE_HOST="127.0.0.1"
export PEERDRIVE_ALLOW_NO_AUTH="1"
export PEERDRIVE_STORAGE="$STORAGE_DIR"
export PEERDRIVE_DOWNLOAD_DIR="$STORAGE_DIR/downloads"
export PEERDRIVE_SHARE_ENABLE="true"
export PEERDRIVE_SHARE_DIRS="$SHARED_DIR"
export PEERDRIVE_PEERJS_ENABLE="true"
export PEERDRIVE_PEERJS_URL="https://peersignal.moonchan.xyz"
export PEERDRIVE_PEERJS_KEY="pd-signal-1edf5e05e4a52b7351392574"
export PEERDRIVE_PEERJS_ID="peerdrive-local-demo"
export PEERDRIVE_DISCOVER_URL="https://peersignal.moonchan.xyz"
export PEERDRIVE_DISCOVER_PRESENCE="true"
export PEERDRIVE_BT_DHT_ENABLE="false"
export PEERDRIVE_IPFS_GATEWAY_ENABLE="false"
export PEERDRIVE_SWAGGER="off"

# 启动后台进程 (完全脱离终端)
setsid "$ROOT/peerdrive-bin" serve > "$LOGS_DIR/node.log" 2>&1 < /dev/null &
NODE_PID=$!
disown "$NODE_PID" 2>/dev/null || true
echo "Node PID: $NODE_PID"

echo "等待服务启动..."
for i in {1..30}; do
  if curl -s -m 2 "http://127.0.0.1:$PORT/ping" >/dev/null 2>&1; then
    echo "✓ 本地节点已成功就绪 (http://127.0.0.1:$PORT)"
    break
  fi
  sleep 1
done

echo "== [4/5] 批量登记共享文件夹文件到索引 =="
REG_RES=$(curl -s -X POST "http://127.0.0.1:$PORT/files/register_folder" \
  -H "Content-Type: application/json" \
  -d "{\"folder_path\":\"$SHARED_DIR\"}")
echo "登记结果: $REG_RES"

echo "== [5/5] 创建测试 Collection (高清壁纸与演示资源合集) =="
COL_RES=$(python3 - <<PY
import urllib.request, json

url = "http://127.0.0.1:$PORT/files"
try:
    with urllib.request.urlopen(url, timeout=5) as resp:
        files = json.loads(resp.read().decode())
except Exception as e:
    print(f"Error fetching files: {e}")
    files = []

entries = []
for f in files:
    h = f.get("hash")
    name = f.get("filename") or f.get("name")
    mime = f.get("mime_type") or f.get("mime")
    if h and name:
        entries.append({
            "path": f"resources/{name}",
            "hash": h,
            "mime_type": mime or "application/octet-stream"
        })

col_payload = {
    "friendly_name": "高清壁纸与演示资源合集 (Wallpapers & Demos)",
    "entries": entries,
    "tags": ["wallpaper", "photos", "demo", "sample"],
    "visibility": "public"
}

req = urllib.request.Request(
    "http://127.0.0.1:$PORT/anon/collections",
    data=json.dumps(col_payload).encode(),
    headers={"Content-Type": "application/json"}
)

try:
    with urllib.request.urlopen(req, timeout=5) as resp:
        print(resp.read().decode())
except Exception as e:
    print(f"Error creating collection: {e}")
PY
)
echo "Collection 创建结果: $COL_RES"

echo "=========================================="
echo "本地节点运行就绪！"
echo "API 地址: http://127.0.0.1:$PORT"
echo "WS 地址:  ws://127.0.0.1:$PORT/ws/peer"
echo "日志文件: $LOGS_DIR/node.log"
echo "=========================================="
