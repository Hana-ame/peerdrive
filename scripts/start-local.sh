#!/usr/bin/env bash
# start-local.sh — 本机一键启动 peerdrive 统一服务（主网盘 + 内置信令 + 节点发现 + 注册认证 + 公共面板）。
#
# 使用方式：
#   ./scripts/start-local.sh
#   PEERDRIVE_PORT=3000 ./scripts/start-local.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PEERDRIVE_PORT:-3000}"
HOST="${PEERDRIVE_HOST:-127.0.0.1}"
PID_FILE="$ROOT/peerdrive.pid"
LOG_DIR="$ROOT/logs"
LOG_FILE="$LOG_DIR/peerdrive.log"

mkdir -p "$ROOT/storage" "$ROOT/downloads" "$ROOT/shared" "$LOG_DIR"

# 1. 检查二进制
if [[ ! -f "$ROOT/peerdrive" ]]; then
  echo "==> 未找到可执行文件，正在编译 back/cmd/peerdrive..."
  ( cd "$ROOT/back" && go build -tags nosqlite -o "$ROOT/peerdrive" ./cmd/peerdrive )
  echo "==> 编译完成"
fi

# 2. 检查是否已在运行
if [[ -f "$PID_FILE" ]]; then
  PID="$(cat "$PID_FILE")"
  if kill -0 "$PID" 2>/dev/null; then
    echo "peerdrive 已在运行中 (PID: $PID, 端口: $PORT)"
    exit 0
  else
    rm -f "$PID_FILE"
  fi
fi

# 检查端口占用
if ss -lnt "sport = :$PORT" | grep -q "$PORT"; then
  echo "错误: 端口 $PORT 已被其他进程占用"
  exit 1
fi

# 3. 启动服务（all 模式：单进程单端口一体化）
if [[ -f "$ROOT/.env" ]]; then
  # shellcheck source=/dev/null
  set -a; source "$ROOT/.env"; set +a
fi

if [[ -z "${PEERDRIVE_JWT_SECRET:-}" ]]; then
  PEERDRIVE_JWT_SECRET="$(head -c 32 /dev/urandom | base64 | tr -d '\r\n')"
  echo "PEERDRIVE_JWT_SECRET=$PEERDRIVE_JWT_SECRET" >> "$ROOT/.env"
  chmod 600 "$ROOT/.env"
fi
export PEERDRIVE_JWT_SECRET

export PORT="$PORT"
export PEERDRIVE_PORT="$PORT"
export PEERDRIVE_HOST="$HOST"
export PEERDRIVE_STORAGE="$ROOT/storage"
export PEERDRIVE_DOWNLOAD_DIR="$ROOT/downloads"
export PEERDRIVE_SHARE_ENABLE="true"
export PEERDRIVE_SHARE_DIRS="$ROOT/storage,$ROOT/downloads,$ROOT/shared"
export PEERDRIVE_ALLOW_NO_AUTH="1"
export PEERDRIVE_PEERJS_ENABLE="true"
export PEERDRIVE_PEERJS_HOST="$HOST"
export PEERDRIVE_PEERJS_PORT="$PORT"
export PEERDRIVE_PEERJS_KEY="peerjs"
export PEERDRIVE_PEERJS_SECURE="false"
export PEERDRIVE_DISCOVER_URL="http://$HOST:$PORT"
export PEERDRIVE_BT_DHT_ENABLE="false"
export PEERDRIVE_IPFS_GATEWAY_ENABLE="false"

( cd "$ROOT" && setsid nohup ./peerdrive all > "$LOG_FILE" 2>&1 < /dev/null & echo $! > "$PID_FILE" )
PID="$(cat "$PID_FILE")"

# 4. 等待存活探针
echo "==> 等待服务就绪..."
READY=0
for _ in $(seq 1 30); do
  if curl -s -m 2 "http://$HOST:$PORT/ping" >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 1
done

if [[ "$READY" -eq 1 ]]; then
  echo "================================================================"
  echo "  ✅ peerdrive 本机部署成功 (PID: $PID)"
  echo "================================================================"
  echo "  网盘 API 地址:     http://$HOST:$PORT"
  echo "  公共内嵌面板:      http://$HOST:$PORT/panel/"
  echo "  信令仪表盘:        http://$HOST:$PORT/_signal"
  echo "  信令状态 API:      http://$HOST:$PORT/status"
  echo "  房间发现 API:      http://$HOST:$PORT/discover/nodes"
  echo "  健康检查:          http://$HOST:$PORT/ping"
  echo "  Swagger API 文档:  http://$HOST:$PORT/swagger/index.html"
  echo "  日志文件:          $LOG_FILE"
  echo "  停止命令:          ./scripts/stop-local.sh"
  echo "================================================================"
else
  echo "❌ 启动超时或失败，请查看日志: $LOG_FILE"
  rm -f "$PID_FILE"
  exit 1
fi
