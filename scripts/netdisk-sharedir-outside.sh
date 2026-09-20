#!/usr/bin/env bash
# netdisk-sharedir-outside.sh —— 「共享目录可以在任意位置」的专项验收。
#
# 背景（2026-09-20 实测）：把 PEERDRIVE_SHARE_DIRS 配到下载目录之外时，
# 登记能过、共享清单也列得出，但对端一拉就 `read failed`。原因是同一个
# 「路径是否越权」的判断在项目里写了三份，且读取侧复用了**写/登记**的边界。
# 现在三处统一到 internal/pathutil，并把读/写边界拆开：
#   - 写/登记边界：storage 根 ∪ PEERDRIVE_SHARE_DIRS ∪ DownloadDir
#   - 读取边界  ：storage 根 ∪ DownloadDir ∪ 运营者声明的共享目录
#
# 本脚本验证的是产品约定「只要设置了目录就要能访问」：共享目录放在
# storage 根**之外**（模拟挂载在 /mnt/media 的另一块盘），全链路仍要通。
# 同时反向验证安全边界没被放宽：没声明过的目录照样拒绝。
#
# 用法：
#   ./scripts/netdisk-sharedir-outside.sh
#   DEMO_DIR=/tmp/mydemo ./scripts/netdisk-sharedir-outside.sh
#
# 前置：Linux/macOS + Go（本仓库 back 模块）。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${DEMO_DIR:-/tmp/pdshare}"
BIN="$DEMO_DIR/bin"
SIG_PORT=9120
A_PORT=3021
B_PORT=3022

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

say "清理端口 $SIG_PORT/$A_PORT/$B_PORT"
kill_port $SIG_PORT $A_PORT $B_PORT
sleep 2
ok "已清理"

say "准备目录 $DEMO_DIR"
rm -rf "$DEMO_DIR"/a "$DEMO_DIR"/b "$DEMO_DIR"/media "$DEMO_DIR"/secret
mkdir -p "$DEMO_DIR"/a/root/downloads "$DEMO_DIR"/a/run \
         "$DEMO_DIR"/b/root/downloads "$DEMO_DIR"/b/run \
         "$DEMO_DIR"/media "$DEMO_DIR"/secret "$BIN"
# 关键点：media 与 secret 都在 storage 根（a/root）之外，与它平级。
printf 'shared dir lives outside the storage root\n' > "$DEMO_DIR"/media/demo.txt
head -c 262144 /dev/urandom                          > "$DEMO_DIR"/media/blob.bin
printf 'must not be reachable\n'                     > "$DEMO_DIR"/secret/leak.txt
ok "共享目录 $DEMO_DIR/media（storage 根之外），另建未声明目录 $DEMO_DIR/secret"

say "编译 back/cmd/server"
( cd "$ROOT/back" && go build -tags nosqlite -o "$BIN/server" ./cmd/server ) || { bad "编译失败"; exit 1; }
ok "编译完成"

say "启动自托管信令 :$SIG_PORT"
( cd "$ROOT/back/signalserver" && \
  setsid --fork nohup go run ./cmd/peersignal -addr ":$SIG_PORT" -key peerjs \
    > "$DEMO_DIR/signal.log" 2>&1 < /dev/null & )
wait_up "http://127.0.0.1:$SIG_PORT/status" "信令" || exit 1

say "启动 node-a :$A_PORT（共享方，共享目录在 storage 之外）"
( cd "$DEMO_DIR/a/run" && env PORT=$A_PORT \
    PEERDRIVE_PEERJS_ID=node-a \
    PEERDRIVE_STORAGE="$DEMO_DIR/a/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/a/root/downloads" \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS="$DEMO_DIR/media" \
    $COMMON_ENV setsid --fork nohup "$BIN/server" > "$DEMO_DIR/a.log" 2>&1 < /dev/null & )

say "启动 node-b :$B_PORT（消费方）"
( cd "$DEMO_DIR/b/run" && env PORT=$B_PORT \
    PEERDRIVE_PEERJS_ID=node-b \
    PEERDRIVE_STORAGE="$DEMO_DIR/b/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/b/root/downloads" \
    $COMMON_ENV setsid --fork nohup "$BIN/server" > "$DEMO_DIR/b.log" 2>&1 < /dev/null & )

wait_up "http://127.0.0.1:$A_PORT/ping" "node-a" || exit 1
wait_up "http://127.0.0.1:$B_PORT/ping" "node-b" || exit 1
sleep 5

say "[1] 登记 storage 根之外的共享目录（旧代码会 400 path outside storage root）"
RR=$(curl -s -m 60 -X POST "http://127.0.0.1:$A_PORT/files/register_folder" \
      -H 'Content-Type: application/json' \
      -d "{\"folder_path\":\"$DEMO_DIR/media\"}")
echo "  $RR" | head -c 300; echo
if echo "$RR" | grep -q 'outside storage root'; then
  bad "共享目录在 storage 之外就被拒了 —— 「设置了目录就该能访问」没兑现"
else
  ok "storage 之外的共享目录登记成功"
fi

say "[2] 反向：未声明的目录必须仍然拒绝（安全边界不能被顺手放宽）"
RS=$(curl -s -m 60 -X POST "http://127.0.0.1:$A_PORT/files/register_folder" \
      -H 'Content-Type: application/json' \
      -d "{\"folder_path\":\"$DEMO_DIR/secret\"}")
echo "  $RS" | head -c 300; echo
if echo "$RS" | grep -q 'outside storage root'; then
  ok "未声明目录（$DEMO_DIR/secret）被拒绝"
else
  bad "未声明目录竟然登记成功 —— 任意文件读取的口子被打开了"
fi

say "[3] 共享清单：B 经 share 帧问 A"
curl -s -m 20 -X POST "http://127.0.0.1:$B_PORT/peerjs/nodes/join" \
  -H 'Content-Type: application/json' -d '{"peer":"node-a"}' >/dev/null
sleep 3
SH=$(curl -s -m 25 "http://127.0.0.1:$B_PORT/peerjs/nodes/node-a/shares")
echo "$SH" | head -c 500; echo
NFILES=$(echo "$SH" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["files"]))' 2>/dev/null || echo 0)
[ "$NFILES" -gt 0 ] && ok "清单里有 $NFILES 个文件" || bad "清单里没有文件（$SH）"

say "[4] 跨节点拉取 + 内容校验（这一步旧代码会 read failed）"
python3 - "$B_PORT" > "$DEMO_DIR/targets.txt" <<'PY'
import sys, json, urllib.request
port = sys.argv[1]
shares = json.load(urllib.request.urlopen(
    "http://127.0.0.1:%s/peerjs/nodes/node-a/shares" % port, timeout=25))
for f in shares["files"]:
    print(f["hash"], f["name"], f["size"])
PY
while read -r HASH NAME SIZE; do
  [ -z "$HASH" ] && continue
  RES=$(curl -s -m 120 -X POST "http://127.0.0.1:$B_PORT/p2p/pull" \
        -H 'Content-Type: application/json' \
        -d "{\"peer\":\"node-a\",\"hash\":\"$HASH\",\"name\":\"$NAME\"}")
  echo "  pull $NAME -> $(echo "$RES" | head -c 160)"
done < "$DEMO_DIR/targets.txt"
sleep 4

curl -s -m 10 "http://127.0.0.1:$B_PORT/p2p/pull" \
  | python3 -c 'import sys,json
for j in json.load(sys.stdin)["jobs"]:
    print("  %-10s %-8s %s/%s %s" % (j["name"], j["status"], j["received"], j["total"], j.get("saved_to") or j.get("error","")))'

say "校验落盘内容"
while read -r HASH NAME SIZE; do
  [ -z "$HASH" ] && continue
  F="$DEMO_DIR/b/root/downloads/pulled/$NAME"
  if [ -f "$F" ]; then
    ACTUAL=$(sha256sum "$F" | cut -d' ' -f1)
    S1=$(stat -c%s "$F")
    if [ "$ACTUAL" = "$HASH" ] && [ "$S1" = "$SIZE" ]; then
      ok "$NAME 内容一致（sha256 匹配，$SIZE 字节）"
    else
      bad "$NAME 内容不一致 got=$ACTUAL want=$HASH size=$S1/$SIZE"
    fi
  else
    bad "$NAME 未落盘（期望 $F）"
  fi
done < "$DEMO_DIR/targets.txt"

say "结果"
if [ "$FAILED" -eq 0 ]; then
  ok "共享目录可以放在任意位置：登记 → 清单 → 拉取 → 校验全绿，且未声明目录仍被拦"
else
  bad "存在失败项，看上面 FAIL 行；日志：$DEMO_DIR/{a,b,signal}.log"
fi

# 收尾必须把自己的后台进程一起收掉：信令是 `go run` 起的子进程，它是本脚本的
# **直系子进程**（`setsid` 在已经是进程组组长时不再 fork），脚本会一直 wait 它
# 而不退出——表现为「结果已经全绿，终端却卡住不返回」（2026-09-20 实测）。
say "停止本脚本起的进程"
# disown 先行：不然 bash 会给每个被 kill 的后台作业打一行 "Killed ..." 作业通知，
# 把结果段冲得看不清。
disown -a 2>/dev/null || true
kill_port $SIG_PORT $A_PORT $B_PORT >/dev/null 2>&1
sleep 1
pkill -f "$DEMO_DIR/bin/server" >/dev/null 2>&1
sleep 1
ok "已停止（日志保留在 $DEMO_DIR）"
exit $FAILED
