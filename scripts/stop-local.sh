#!/usr/bin/env bash
# stop-local.sh — 停止本机运行的 peerdrive 服务。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_FILE="$ROOT/peerdrive.pid"
PORT="${PEERDRIVE_PORT:-3000}"

STOPPED=0

if [[ -f "$PID_FILE" ]]; then
  PID="$(cat "$PID_FILE")"
  if kill -0 "$PID" 2>/dev/null; then
    echo "==> 正在终止 peerdrive 进程 (PID: $PID)..."
    kill "$PID" || true
    for _ in $(seq 1 10); do
      if ! kill -0 "$PID" 2>/dev/null; then
        STOPPED=1
        break
      fi
      sleep 0.5
    done
    if [[ "$STOPPED" -eq 0 ]]; then
      echo "==> 强制终止进程 (PID: $PID)..."
      kill -9 "$PID" 2>/dev/null || true
    fi
  fi
  rm -f "$PID_FILE"
fi

# 检查端口是否有残留
if ss -lntpH "sport = :$PORT" 2>/dev/null | grep -q "peerdrive"; then
  echo "==> 清理端口 $PORT 上的残留 peerdrive 进程..."
  fuser -k -n tcp "$PORT" 2>/dev/null || true
fi

echo "✅ peerdrive 服务已停止"
