#!/usr/bin/env bash
# test-layers.sh — 按 AOP 分层逐层跑测试（分层定义见 doc/layers/README.md，L1-L8）。
#
# 为什么按层分：L1-L8 是「关注点分组」（含依赖链与并列生态位，非严格嵌套），
# 每层可独立验证的测试集合不同——独立 go.mod（L1/L7）、同包子集（L2-L3 用
# -skip/-run 拆开）、跨包一组（L4-L5）、独立前端 vitest（L8）。
# 脚本跑到最后汇总，任一层失败即非零退出（不中途停，一次拿全部结果）。
#
# 用法: bash scripts/test-layers.sh [--integration]
#   --integration  追加真实信令集成段（需外网+代理；公共信令上必须 -p 1 串行，
#                  多组测试并行会互相干扰——AGENTS.md 硬性约束）
# 失败日志: /tmp/layer-test-<层>.log
#
# ⚠️ 本脚本**不等于** `back` 的全量测试：`cd back && go test -tags nosqlite ./...`
#    覆盖的是主模块全部包。改后端请两个都跑（详见 doc/testing/README.md §3.13）。

set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INTEGRATION=0
[[ "${1:-}" == "--integration" ]] && INTEGRATION=1

# node 常见是经 nvm 装的：PATH 里可能压根没有 node，L8 前端那层会因此假 FAIL。
# 这里把最高版本的 nvm node 补进 PATH（已能找到 node 时不动）。
if ! command -v node >/dev/null 2>&1 && [ -d "$HOME/.nvm/versions/node" ]; then
  PATH="$(ls -d "$HOME/.nvm/versions/node"/*/bin 2>/dev/null | sort -V | tail -1):$PATH"
  export PATH
fi

# go 命令代理（AGENTS.md：出网走宿主机代理 10809；GOPROXY 走 goproxy.cn 加速）
export HTTPS_PROXY="${HTTPS_PROXY:-http://172.29.80.1:10809}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

PASS=0
FAIL=0
GREEN='\033[32m'; RED='\033[31m'; NC='\033[0m'

run_layer() { # name cmd
  local name="$1" cmd="$2"
  echo "──────── [${name}] ${cmd}"
  if bash -c "cd '$ROOT' && $cmd" >"/tmp/layer-test-${name}.log" 2>&1; then
    echo -e "${GREEN}  PASS${NC}"
    PASS=$((PASS + 1))
  else
    echo -e "${RED}  FAIL${NC}  (log: /tmp/layer-test-${name}.log)"
    FAIL=$((FAIL + 1))
  fi
}

echo "== AOP 分层测试 =="
echo "ROOT: $ROOT"

# L1 信令/传输原语（独立 go.mod，-race）
run_layer "L1-peerjs" 'cd back/peerjs && go test ./... -count=1 -race'
# L2 帧协议层（transport 包，剥掉 L3 admin 子集）
run_layer "L2-transport" 'cd back && go test -tags nosqlite ./internal/transport/ -count=1 -skip "^TestAdmin"'
# L3 管理面切面（与 L2 同包，单跑 admin 子集）
run_layer "L3-admin" 'cd back && go test -tags nosqlite ./internal/transport/ -count=1 -run "^TestAdmin"'
# L4 业务核心（controller/service/source/downloader）
run_layer "L4-core" 'cd back && go test -tags nosqlite ./internal/controller/... ./internal/service/... ./internal/source/... ./internal/downloader/...'
# L5 数据切面（repository）
run_layer "L5-data" 'cd back && go test -tags nosqlite ./internal/repository/...'
# L6 发现切面（自托管信令）。注意 signalserver 是**独立 go.mod**，位于 back/signalserver
#   而不是 back/internal —— 主模块的 ./... 扫不到它，跟 peerjs 一样必须 cd 进去单独跑。
#   （2026-09-20 前这里写的是 ./internal/signalserver/...，pattern 不存在直接报错退出：
#    该层恒为 FAIL，而这 21 个用例从来没被执行过。）
run_layer "L6-discovery" 'cd back/signalserver && go test ./... -count=1'
# LB 基础包切面（config/model/provider/router）。这四个包不属于 L1-L8 任何一层，
#   但 `go test ./...` 会跑到；以前这个脚本会把它们整体漏掉（57 个用例）。
run_layer "LB-baseline" 'cd back && go test -tags nosqlite ./internal/config/... ./internal/model/... ./internal/provider/... ./internal/router/...'
# L7 外部能力切面（独立 go.mod）
run_layer "L7-external" 'cd back/p2p_bt && go test ./... -count=1'
# L8 前端切面（vitest）
run_layer "L8-frontend" 'cd front && npm test'

if [ "$INTEGRATION" = "1" ]; then
  # 集成段：真实公共信令 + 公共 broker（需外网/代理），必须 -p 1 串行
  run_layer "INT" 'cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1'
fi

echo "========================"
echo -e "结果: ${GREEN}${PASS} 层通过${NC}, ${RED}${FAIL} 层失败${NC}"
[ "$FAIL" = "0" ]