#!/usr/bin/env bash
# netdisk-local-demo.sh —— 网盘链路的一键本地验收环境（脱外网）。
#
# 起 4 个进程：自托管信令（peersignal）+ 两个 peerdrive 节点（A 共享 / B 消费），
# 然后自动跑一遍完整链路并逐步打印结果：
#
#   1. 节点市场：B 能否发现 A
#   2. 加入节点：POST /peerjs/nodes/join（含重启后 joined 是否还在的落盘校验）
#   3. 共享清单：B 经 share 帧拿到 A 的文件列表（files 应非空）
#   4. 跨节点拉取：B 逐个拉取并落盘
#   5. 内容校验：落盘文件的 sha256 必须与源 hash 一致
#   6. PSK 门禁：A 重启成「带密钥」→ 没密钥的 B 必须被拦；给 B 同一把钥匙 → 恢复
#
# 用法：
#   ./scripts/netdisk-local-demo.sh            # 跑完自动验证，服务保留供手测
#   ./scripts/netdisk-local-demo.sh --stop     # 停掉本脚本起的所有进程
#   DEMO_DIR=/tmp/mydemo ./scripts/netdisk-local-demo.sh
#
# 前置：Linux/macOS + Go（本仓库 back 模块）+ 端口 9100/3001/3002 空闲。
#
# 两个容易踩的配置约束（代码里有对应安全边界，配错了会静默失败）：
#   - 共享目录必须在 storage 根内（RegisterFolder 的 storage root 校验）
#   - 共享目录必须在 download 根内（serveFile 的 allowed root 校验）
#   所以本脚本把共享目录放在 $DEMO_DIR/a/root/downloads/shared，
#   并令 STORAGE=<...>/a/root、DOWNLOAD_DIR=<...>/a/root/downloads。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${DEMO_DIR:-/tmp/pddemo}"
BIN="$DEMO_DIR/bin"
SIG_PORT=9100
A_PORT=3001
B_PORT=3002

COMMON_ENV="PEERDRIVE_PEERJS_ENABLE=true \
PEERDRIVE_PEERJS_HOST=127.0.0.1 \
PEERDRIVE_PEERJS_PORT=$SIG_PORT \
PEERDRIVE_PEERJS_KEY=peerjs \
PEERDRIVE_PEERJS_SECURE=false \
PEERDRIVE_DISCOVER_URL=http://127.0.0.1:$SIG_PORT \
PEERDRIVE_BT_DHT_ENABLE=false \
PEERDRIVE_IPFS_GATEWAY_ENABLE=false"

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

# 杀掉占用某端口的进程。fuser 属于 psmisc，不是每个环境都有（CI 镜像就未必装），
# 所以先用 fuser，没有的话退回 ss 找 pid 再 kill，两者都没有就放弃（不致命）。
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

if [[ "${1:-}" == "--stop" ]]; then
  say "停止本地验收环境"
  kill_port $A_PORT $B_PORT $SIG_PORT
  sleep 1
  ok "已停止"
  exit 0
fi

say "清理残留进程（端口 $SIG_PORT/$A_PORT/$B_PORT）"
# 这个脚本假设自己是这三个端口的唯一主人。如果上一次的进程还在跑，新起的 server
# 会因端口占用直接退出，而脚本后面的 curl 全打到**旧进程**上：于是能看到节点、
# 也能 join，但共享清单是旧的、拉取落盘路径也是旧的 —— 表现为「任务 done 但文件
# 没落盘」这种极误导人的失败（2026-09-20 实测踩到）。所以重跑前先清干净。
kill_port $SIG_PORT $A_PORT $B_PORT
sleep 2
ok "已清理"

say "准备目录 $DEMO_DIR"
rm -rf "$DEMO_DIR"/a "$DEMO_DIR"/b
mkdir -p "$DEMO_DIR"/a/root/downloads/shared "$DEMO_DIR"/a/run \
         "$DEMO_DIR"/b/root/downloads        "$DEMO_DIR"/b/run "$BIN"
printf 'hello peerdrive netdisk demo\n' > "$DEMO_DIR"/a/root/downloads/shared/demo.txt
head -c 300000 /dev/urandom              > "$DEMO_DIR"/a/root/downloads/shared/blob.bin
ok "样例文件：demo.txt(29B) + blob.bin(300KB)"

say "编译 back/cmd/server"
( cd "$ROOT/back" && go build -tags nosqlite -o "$BIN/server" ./cmd/server ) || { bad "编译失败"; exit 1; }
ok "编译完成"

say "启动自托管信令 :$SIG_PORT"
( cd "$ROOT/back/signalserver" && \
  setsid nohup go run ./cmd/peersignal -addr ":$SIG_PORT" -key peerjs \
    > "$DEMO_DIR/signal.log" 2>&1 < /dev/null & )
wait_up "http://127.0.0.1:$SIG_PORT/status" "信令" || exit 1

say "启动 node-a :$A_PORT（共享方）"
( cd "$DEMO_DIR/a/run" && env PORT=$A_PORT \
    PEERDRIVE_PEERJS_ID=node-a \
    PEERDRIVE_STORAGE="$DEMO_DIR/a/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/a/root/downloads" \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS="$DEMO_DIR/a/root/downloads/shared" \
    $COMMON_ENV setsid nohup "$BIN/server" > "$DEMO_DIR/a.log" 2>&1 < /dev/null & )

say "启动 node-b :$B_PORT（消费方）"
( cd "$DEMO_DIR/b/run" && env PORT=$B_PORT \
    PEERDRIVE_PEERJS_ID=node-b \
    PEERDRIVE_STORAGE="$DEMO_DIR/b/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/b/root/downloads" \
    $COMMON_ENV setsid nohup "$BIN/server" > "$DEMO_DIR/b.log" 2>&1 < /dev/null & )

wait_up "http://127.0.0.1:$A_PORT/ping" "node-a" || exit 1
wait_up "http://127.0.0.1:$B_PORT/ping" "node-b" || exit 1
sleep 5 # 等 announce + WebRTC 握手
echo "  （两节点各自 cwd 下独立的 peerdrive.db，模拟两台机器）"

say "[1] 节点市场：B 能否发现 A"
MK=$(curl -s -m 15 "http://127.0.0.1:$B_PORT/peerjs/nodes")
echo "$MK" | head -c 400; echo
echo "$MK" | grep -q '"peer_id":"node-a"' && ok "市场里能看到 node-a" || bad "市场里没有 node-a"

say "[2] 加入节点"
JR=$(curl -s -m 20 -X POST "http://127.0.0.1:$B_PORT/peerjs/nodes/join" \
      -H 'Content-Type: application/json' -d '{"peer":"node-a"}')
echo "  $JR"
echo "$JR" | grep -q '"status":"joined"' && ok "join 成功" || bad "join 失败"
sleep 1
[ -f "$DEMO_DIR/b/root/joined_nodes.json" ] \
  && ok "已落盘 joined_nodes.json（重启后仍在）" \
  || bad "joined_nodes.json 未落盘"

say "[3] A 登记共享目录（写入可被共享的文件索引）"
RR=$(curl -s -m 60 -X POST "http://127.0.0.1:$A_PORT/files/register_folder" \
      -H 'Content-Type: application/json' \
      -d "{\"folder_path\":\"$DEMO_DIR/a/root/downloads/shared\"}")
echo "  $RR" | head -c 300; echo

say "[4] 共享清单：B 经 share 帧问 A"
SH=$(curl -s -m 25 "http://127.0.0.1:$B_PORT/peerjs/nodes/node-a/shares")
echo "$SH" | head -c 500; echo
NFILES=$(echo "$SH" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["files"]))' 2>/dev/null || echo 0)
[ "$NFILES" -gt 0 ] && ok "清单里有 $NFILES 个文件" || bad "清单里没有文件（files=[]）"

say "[5] 跨节点拉取 + 内容校验"
python3 - <<'PY' > "$DEMO_DIR/targets.txt"
import sys, json, urllib.request
shares = json.load(urllib.request.urlopen("http://127.0.0.1:3002/peerjs/nodes/node-a/shares", timeout=25))
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

say "[6] PSK 门禁（预共享密钥）"
# 门禁只在真实网络里才有意义，所以这一步不 mock：把 A 重启成「带密钥」，
# 先看没密钥的 B 是不是真被拦住，再给 B 同一把钥匙看是不是立刻恢复。
# 注意 B 重启后 joined_nodes.json 还在（落盘校验已在 [2] 覆盖），会重连上来。
PSK=demo-psk

kill_port $A_PORT
sleep 2
( cd "$DEMO_DIR/a/run" && env PORT=$A_PORT \
    PEERDRIVE_PEERJS_ID=node-a \
    PEERDRIVE_STORAGE="$DEMO_DIR/a/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/a/root/downloads" \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS="$DEMO_DIR/a/root/downloads/shared" \
    PEERDRIVE_PSK="$PSK" \
    $COMMON_ENV setsid nohup "$BIN/server" > "$DEMO_DIR/a.log" 2>&1 < /dev/null & )
wait_up "http://127.0.0.1:$A_PORT/ping" "node-a（带 PSK 重启）" || exit 1
sleep 6

NODE_A=$(curl -s -m 10 "http://127.0.0.1:$A_PORT/peerjs/node")
echo "  $NODE_A" | head -c 200; echo
echo "$NODE_A" | grep -q '"psk":true' && ok "A 报告已开启 PSK 门禁" || bad "A 没报告 PSK 开启（$NODE_A）"

SH2=$(curl -s -m 30 "http://127.0.0.1:$B_PORT/peerjs/nodes/node-a/shares")
echo "  $SH2" | head -c 300; echo
if echo "$SH2" | grep -q '"files":\['; then
  bad "B 没有密钥却拿到了清单 —— 门禁没生效"
else
  ok "B 没带密钥 → 被门禁拦下（$(echo "$SH2" | head -c 120)）"
fi

kill_port $B_PORT
sleep 2
( cd "$DEMO_DIR/b/run" && env PORT=$B_PORT \
    PEERDRIVE_PEERJS_ID=node-b \
    PEERDRIVE_STORAGE="$DEMO_DIR/b/root" \
    PEERDRIVE_DOWNLOAD_DIR="$DEMO_DIR/b/root/downloads" \
    PEERDRIVE_PSK="$PSK" \
    $COMMON_ENV setsid nohup "$BIN/server" > "$DEMO_DIR/b.log" 2>&1 < /dev/null & )
wait_up "http://127.0.0.1:$B_PORT/ping" "node-b（带同一把密钥重启）" || exit 1
sleep 8

SH3=$(curl -s -m 30 "http://127.0.0.1:$B_PORT/peerjs/nodes/node-a/shares")
N3=$(echo "$SH3" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["files"]))' 2>/dev/null || echo 0)
[ "$N3" -gt 0 ] && ok "B 带同一把密钥 → 恢复（$N3 个文件）" \
                || bad "B 带了密钥仍拿不到清单（$SH3）"

say "结果"
if [ "$FAILED" -eq 0 ]; then
  ok "网盘链路全绿：市场 → 加入 → 清单 → 拉取 → 校验"
else
  bad "存在失败项，看上面 FAIL 行；日志：$DEMO_DIR/{a,b,signal}.log"
fi

cat <<TIP

服务保持运行，可以接着手测：
  公共面板（推荐，不需要任何服务器）
           : cd packages/peerdrive-client && npm run build:panel
             然后浏览器打开 dist/panel.html，或直接带参数打开：
             dist/panel.html?node=node-a&host=<本机IP>:$SIG_PORT&path=/&key=peerjs&secure=0&auto=1
             ⚠️ 第 [6] 步把 A/B 都重启成了带 PSK 的节点，所以现在手测要在面板的
             「预共享密钥」框里填 $PSK（或链接里带 &psk=$PSK），否则拿不到清单。
             想回到无门禁状态：$0 --stop 再重跑本脚本的前 5 步。
             （面板与信令不同源，所以信令必须开 CORS —— 已在 back/signalserver 处理）
  面板自检 : cd packages/peerdrive-client
             SIG_HOST=<本机IP> SIG_PORT=$SIG_PORT NODE_ID=node-a PSK=$PSK node scripts/verify-panel.mjs
  节点管理台: cd front && npm run dev -- --host 0.0.0.0 --port 5173
             浏览器打开后在设置里把后端改成 http://<本机IP>:$B_PORT
  最小演示 : cd packages/peerdrive-client && npm run demo（需要 http 服务提供包目录）
  停止环境 : $0 --stop
TIP
