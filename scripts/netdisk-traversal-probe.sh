#!/usr/bin/env bash
# netdisk-traversal-probe.sh —— 对着**真跑起来的节点**打一整套路径穿透 payload。
#
# 单测（back/internal/{pathutil,transport,service,controller}/*traversal*）
# 验的是函数与 handler，本脚本验的是"编译出来的二进制 + 真 HTTP 端口"这一层：
# 配置有没有被正确加载、路由有没有绕过控制器直接读文件、错误码是不是 200。
# 两者互补——历史上多次事故都是"单测绿、真实部署被打穿"。
#
# 用法：
#   ./scripts/netdisk-traversal-probe.sh
#   DEMO_DIR=/tmp/mydemo ./scripts/netdisk-traversal-probe.sh
#
# 前置：Linux/macOS + Go（本仓库 back 模块）。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${DEMO_DIR:-/tmp/pdprobe}"
BIN="$DEMO_DIR/bin"
SIG_PORT=9130
A_PORT=3031

COMMON_ENV="PEERDRIVE_PEERJS_ENABLE=true \
PEERDRIVE_PEERJS_HOST=127.0.0.1 \
PEERDRIVE_PEERJS_PORT=$SIG_PORT \
PEERDRIVE_PEERJS_KEY=peerjs \
PEERDRIVE_PEERJS_SECURE=false \
PEERDRIVE_DISCOVER_URL=http://127.0.0.1:$SIG_PORT \
PEERDRIVE_BT_DHT_ENABLE=false \
PEERDRIVE_IPFS_GATEWAY_ENABLE=false \
PEERDRIVE_PSK=demo-psk"

say() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
ok()  { printf '  \033[32mPASS\033[0m %s\n' "$*"; }
bad() { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=1; }
FAILED=0

wait_up() { # url label
  for _ in $(seq 1 60); do
    curl -s -m 2 "$1" >/dev/null 2>&1 && { ok "$2 ready"; return 0; }
    sleep 2
  done
  bad "$2 启动超时"; return 1
}

kill_port() { # port...
  local p
  for p in "$@"; do
    if command -v fuser >/dev/null 2>&1; then
      fuser -k -n tcp "$p" 2>/dev/null
    elif command -v ss >/dev/null 2>&1; then
      ss -lntpH "sport = :$p" 2>/dev/null |
        grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u |
        while read -r pid; do kill "$pid" 2>/dev/null; done
    fi
  done
  return 0
}

say "清理端口 $SIG_PORT/$A_PORT"
kill_port $SIG_PORT $A_PORT
sleep 2
ok "已清理"

say "准备目录 $DEMO_DIR"
rm -rf "$DEMO_DIR"/node "$DEMO_DIR"/outside "$DEMO_DIR"/media
mkdir -p "$DEMO_DIR"/node/root/downloads "$DEMO_DIR"/node/run \
         "$DEMO_DIR"/outside "$DEMO_DIR"/media "$BIN"
printf 'top secret\n' > "$DEMO_DIR"/outside/secret.txt
printf 'shared\n'     > "$DEMO_DIR"/media/movie.mkv
ok "诱饵：$DEMO_DIR/outside/secret.txt（storage 之外，绝不能被摸到）"

say "编译 back/cmd/server"
( cd "$ROOT/back" && go build -tags nosqlite -o "$BIN/server" ./cmd/server ) || { bad "编译失败"; exit 1; }
ok "编译完成"

say "启动自托管信令 :$SIG_PORT"
( cd "$ROOT/back/signalserver" && \
  setsid --fork nohup go run ./cmd/peersignal -addr ":$SIG_PORT" -key peerjs \
    > "$DEMO_DIR/signal.log" 2>&1 < /dev/null & )
wait_up "http://127.0.0.1:$SIG_PORT/status" "信令" || exit 1

say "启动被测节点 :$A_PORT"
( cd "$DEMO_DIR/node/run" && env PORT=$A_PORT \
    PEERDRIVE_PEERJS_ID=node-probe \
    PEERDRIVE_STORAGE="$DEMO_DIR/node/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/node/root/downloads" \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS="$DEMO_DIR/media" \
    $COMMON_ENV setsid --fork nohup "$BIN/server" > "$DEMO_DIR/node.log" 2>&1 < /dev/null & )
wait_up "http://127.0.0.1:$A_PORT/ping" "节点" || exit 1
sleep 3

API="http://127.0.0.1:$A_PORT"

# probe <方法> <路径> <body 或空> <期望> <说明>
probe() { # method endpoint body expect desc
  local code body_out
  if [ "$1" = "GET" ]; then
    body_out=$(curl -s -m 15 -w '\n%{http_code}' "$API$2")
  else
    body_out=$(curl -s -m 15 -w '\n%{http_code}' -X POST "$API$2" \
               -H 'Content-Type: application/json' -d "$3")
  fi
  code=$(echo "$body_out" | tail -n1)
  case "$4" in
    reject) [ "$code" != "200" ] && ok "$5（HTTP $code）" \
              || bad "$5 —— 竟然返回 200：$(echo "$body_out" | head -n1 | head -c 160)" ;;
    accept) [ "$code" = "200" ] && ok "$5（HTTP $code）" \
              || bad "$5 —— 正常请求被误伤：$(echo "$body_out" | head -n1 | head -c 160)" ;;
  esac
}

# payload 集合：编码形态 + 未编码形态 + 绝对路径。
# OUTSIDE 是"绝不能被摸到"的目录，与 storage 同级。
OUTSIDE="$DEMO_DIR/outside"
PAYLOADS=(
  "../../outside/secret.txt"
  "..%2f..%2foutside%2fsecret.txt"
  "%2e%2e%2f%2e%2e%2foutside%2fsecret.txt"
  "....//....//outside/secret.txt"
  "..%5c..%5coutside%5csecret.txt"
  "/etc/passwd"
  "$OUTSIDE/secret.txt"
  "$DEMO_DIR/node/root/downloads/../../outside/secret.txt"
)

# 编码形态（带 % 或 ....//）只在 **query** 里会变成真逃逸——gin 会先解码；
# JSON body 不做 URL 解码，`..%2f..` 就是一个含百分号的**文件名**，
# Join 之后仍落在 storage 内。所以这两类要分开断言：
#   - 全部 payload：一律要求非 200；
#   - 只有"未编码的真逃逸"额外要求错误来自路径判定（而不是碰巧源不存在）。
needs_boundary() { # payload → 0=要求路径判定拦下，1=只要求非 200
  case "$1" in
    *%*|*....//*) return 1 ;;
    *)            return 0 ;;
  esac
}

say "[1] register_local：任意文件读"
for p in "${PAYLOADS[@]}"; do
  probe POST /files/register_local "{\"path\":\"$p\",\"filename\":\"x.txt\"}" reject "拒绝 $p"
done

say "[2] register_folder：任意目录列举 + 批量任意文件读"
for p in "${PAYLOADS[@]}"; do
  probe POST /files/register_folder "{\"folder_path\":\"$p\"}" reject "拒绝 $p"
done

say "[3] browse（query 会被 gin 先解码，是这一层独有的面）"
for p in "${PAYLOADS[@]}"; do
  probe GET "/files/browse?path=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$p")" "" reject "拒绝 $p"
done

say "[4] copy：任意文件写（最危险，要确认是**路径判定**拦下的）"
# 为什么这一步要单独看错误内容：copy 的源是个不存在的 dummy hash，
# "被拒绝"本身说明不了什么——必须确认拒绝来自**目标路径判定**，
# 而不是"先查源、源不存在就返回"（那种顺序会把安全边界藏在后面）。
for p in "${PAYLOADS[@]}"; do
  RES=$(curl -s -m 15 -X POST "$API/files/copy" -H 'Content-Type: application/json' \
        -d "{\"hash\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"dest_path\":\"$p\"}")
  CODE=$(curl -s -m 15 -o /dev/null -w '%{http_code}' -X POST "$API/files/copy" \
        -H 'Content-Type: application/json' \
        -d "{\"hash\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"dest_path\":\"$p\"}")
  if [ "$CODE" = "200" ]; then
    bad "copy 竟然返回 200：$p"
  elif needs_boundary "$p"; then
    if echo "$RES" | grep -q 'outside'; then
      ok "由路径判定拦下（HTTP $CODE，$p）"
    else
      bad "被拦了但不是路径判定的功劳：$p -> $(echo "$RES" | head -c 120)"
    fi
  else
    ok "非 200（HTTP $CODE；编码形态在 JSON 里只是个怪文件名，不逃逸：$p）"
  fi
done

say "[5] 反向：正常用法不能被打死"
probe GET "/files/browse?path=" "" accept "browse storage 根（空 path）"
probe GET "/files/browse?path=/" "" accept "browse storage 根（/）"
probe GET "/files/browse?path=downloads" "" accept "browse 根内子目录"
probe POST /files/register_folder "{\"folder_path\":\"$DEMO_DIR/media\"}" accept "登记**声明过**的共享目录（storage 之外）"
probe POST /files/register_local "{\"path\":\"$DEMO_DIR/media/movie.mkv\",\"filename\":\"movie.mkv\"}" accept "登记**声明过**的共享文件（storage 之外）"

say "[6] 对外清单里不能出现诱饵文件"
SH=$(curl -s -m 20 "$API/peerjs/shares" 2>/dev/null || true)
if [ -n "$SH" ] && echo "$SH" | grep -q 'secret.txt'; then
  bad "共享清单里出现了不该出现的 secret.txt：$SH"
else
  ok "共享清单干净（未包含 storage 之外的诱饵）"
fi

say "[7] 磁盘确认：诱饵目录没被动过"
if [ -f "$OUTSIDE/secret.txt" ] && [ "$(cat "$OUTSIDE/secret.txt")" = "top secret" ]; then
  ok "诱饵文件内容未变"
else
  bad "诱饵文件被改写或删除了 —— 写入类穿透可能真的发生了"
fi
N=$(ls -1 "$OUTSIDE" | wc -l)
[ "$N" -eq 1 ] && ok "诱饵目录没有多出任何新文件" || bad "诱饵目录里多出了 $((N-1)) 个文件"

say "结果"
if [ "$FAILED" -eq 0 ]; then
  ok "穿透探针全绿：真实节点拒绝了全部越权 payload，正常用法未被误伤"
else
  bad "存在被放行的 payload，看上面 FAIL 行；日志：$DEMO_DIR/node.log"
fi

say "停止本脚本起的进程"
disown -a 2>/dev/null || true
kill_port $SIG_PORT $A_PORT >/dev/null 2>&1
sleep 1
pkill -f "$DEMO_DIR/bin/server" >/dev/null 2>&1
sleep 1
ok "已停止（日志保留在 $DEMO_DIR）"
exit $FAILED
