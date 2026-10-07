#!/usr/bin/env bash
# deploy-peersignal.sh — 一键部署 peersignal 到 cloudcone + nginx + cloudflare
#
# 用法：
#   本地执行：bash deploy-peersignal.sh
#   或在 cloudcone 上执行：bash deploy-peersignal.sh --remote
#
# 前置：
#   1. cloudcone 127.26.9.5 已可 SSH（root 或 sudo）
#   2. moonchan.xyz 域名在 Cloudflare 管理
#   3. 本脚本在 peerdrive 仓库根目录执行

set -euo pipefail

# ====== 配置 ======
CLOUDCONE_HOST="cloudcone"           # SSH 别名（HostName/User 由 ~/.ssh/config 解析）
CLOUDCONE_USER="root"                 # SSH 用户
CLOUDCONE_PORT="9000"                # peersignal 监听端口
CLOUDCONE_SSH_PORT="22"              # SSH 端口
DOMAIN="peersignal.moonchan.xyz"     # 域名
# 信令 key：⚠️ 必须是**稳定**的，不是每次随机生成。
#
# 2026-10-06 实测踩到的坑：这里原本是 `pd-signal-$(openssl rand -hex 12)`，
# 每次部署都换一个新值。而客户端把这个 key **硬编码**在
# back/peerjs/peer.go:54 与 back/internal/config/config.go:22，
# 没法跟着服务器变。于是结果是：
#   每部署一次 → 线上 key 变了 → 所有已发布客户端全部连不上信令
#                → 跨节点传输断掉，且没有任何报错提示「key 变了」
# 实际验证：拿客户端里的旧 key 对生产跑 live 测试，13 次 bad handshake。
#
# 也就是说，**照原样跑这个脚本，第一次部署之后就把自己的产品弄坏了**，
# 而部署本身会报「✅ 部署完成」。
#
# 现在：优先读服务器上已有的 key，读不到才生成一次并写回去。
# 这样重复部署是幂等的；真要轮换，得显式 SIGNAL_KEY_ROTATE=1（并同步改客户端）。
REMOTE_DIR="/opt/peersignal"          # peersignal 在服务器上的目录
KEY_FILE="${REMOTE_DIR}/signal.key"
# 白名单落盘文件：让「开没开 token 白名单」这个状态跟 key 一样跨部署存活。
# 没有它的话，第二次部署忘带环境变量就会**静默关掉**防线，而部署照样报成功。
TOKENS_FILE="${REMOTE_DIR}/signal.tokens"

# 信令 token 白名单（审计 A-2 的部署侧开关）。
#
# 这一层**今天已存在但从未被打开过**：
#   - 服务端校验是有的：back/signalserver/signalserver.go HandleWS 里
#     `if len(s.tokenWhitelist) > 0 && !s.tokenWhitelist[token]` → "Invalid token provided"
#   - 开关也有：独立二进制的 `peersignal --tokens a,b`（单跑信令走这条），
#     以及 `peerdrive signal` / `all` 读的环境变量 PEERJS_TOKENS
#     （back/internal/services/services.go 的 SignalConfig / UnifiedMux）
#   - 缺的是**发送口**：面板此前没有 token 字段（本 PR 补上），
#     而**节点侧（Go）至今没有可填的 token**（见下面「开启后果」）
# 所以此前 PEERJS_TOKENS 这条防线实际从未生效——部署侧没有开关、代码侧没有发送口，
# 任一端单独改都不产生效果。本脚本补的是开关，**默认仍然关闭**（不填 = 行为与
# 今天逐字一致）；把默认改成开启属于产品决策，不在这个改动里。
#
# 用法（显式 opt-in，一次配置长期沿用）：
#   SIGNAL_TOKENS="tok-alice,tok-bob" bash scripts/deploy-peersignal.sh
#   SIGNAL_TOKENS_CLEAR=1 bash scripts/deploy-peersignal.sh   # 显式关闭
SIGNAL_TOKENS="${SIGNAL_TOKENS:-}"
SIGNAL_TOKENS_CLEAR="${SIGNAL_TOKENS_CLEAR:-0}"

# ops token：/status 与 /status/key 的凭据。**2026-10-06 起必须配。**
# 不配的后果是实测出来的，不是推演：
#   curl https://peersignal.moonchan.xyz/status   → 200 + 信令 key + 全网节点名册
# 因为 opsTokenOK 在白名单为空时曾经「任何非空 token 都放行」，
# 于是 `?token=随便编的` 也能进。现已改成默认拒绝（signalserver.go:343）。
# 这个 token 只给运维从命令行查面板用，不进前端、不进仓库。
OPS_TOKEN="pd-ops-$(openssl rand -hex 16)"
BINARY="/tmp/peersignal-linux-amd64"
SYSTEMD_SERVICE="peersignal"

# ====== 定位仓库根 =====
# 原脚本只写了一句「在仓库根目录执行」，靠人自觉。2026-10-06 改成自己算：
# 从脚本自身位置往上找 go.mod 所在目录，这样从任何 cwd 调用都对。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$REPO_ROOT"
[ -d "back/signalserver" ] || { echo "❌ 不是 peerdrive 仓库（缺 back/signalserver）: $REPO_ROOT"; exit 1; }

# ====== 取信令 key（幂等）======
# 先看服务器上有没有；有就沿用，没有才生成并写回服务器。
# 这样重复部署不会改 key，只有显式轮换才会。
load_or_create_key() {
  local remote_key=""
  if [ "$REMOTE" = "1" ]; then
    remote_key=$(ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
      "cat ${KEY_FILE} 2>/dev/null || true")
  fi
  if [ -n "$remote_key" ] && [ "${SIGNAL_KEY_ROTATE:-0}" != "1" ]; then
    KEY="$remote_key"
    echo "── 沿用服务器上已有的信令 key（不轮换）──"
  else
    KEY="pd-signal-$(openssl rand -hex 12)"
    echo "── 生成新的信令 key${SIGNAL_KEY_ROTATE:+ （显式轮换）}──"
    [ -n "$remote_key" ] && echo "⚠️  轮换后，旧客户端（硬编码了旧 key）会连不上，需同步更新。"
  fi
  # 写回服务器，保证下次部署读到同一个值。权限 600：它是凭据。
  if [ "$REMOTE" = "1" ]; then
    ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
      "mkdir -p ${REMOTE_DIR} && printf '%s\\n' '${KEY}' > ${KEY_FILE} && chmod 600 ${KEY_FILE}" \
      || { echo "❌ 写回 key 文件失败，中止（否则下次部署会生成另一个 key）"; exit 1; }
  fi
}

# ====== 取 token 白名单（显式 opt-in，状态跨部署存活）======
# 与 key 同一套「先看服务器上有没有，有就沿用」的结构（照抄 load_or_create_key
# 的模式，不重构它），目的是让「开没开这条防线」成为**可查询、可重复部署**的状态，
# 而不是每次部署看手气的临时 flag：
#   忘带 flag 就静默关掉，而部署照样打印「✅ 部署完成」——这是本脚本对
#   --ops-token 那一类事故（配置在、防线不在、探测一切正常）已付出过的学费。
# 三态：
#   SIGNAL_TOKENS="a,b"      → 写入/更新白名单（本次部署起生效）
#   SIGNAL_TOKENS_CLEAR=1    → 显式关闭（删掉落盘文件，ExecStart 里不再出现 --tokens）
#   两者都不给                → 沿用服务器上的现状。默认（文件不存在）= 关闭，
#                               单元行与今天的线上行为逐字一致。
load_or_create_tokens() {
  local remote_tokens=""
  # ⚠️ 两种模式都要读**当前这台机器能看到的状态文件**：
  #   REMOTE=1 →  ssh 上去 cat；REMOTE=0（脚本已在 cloudcone 上跑）→ 本地 cat。
  # 只处理 REMOTE=1 的话，--remote 模式下读不到现状 → 单元行里 --tokens 消失 →
  # **重部署一次就把防线静默关掉**（部署照样打印成功）。这正是本函数要防的失效，
  # 不能自己再犯一遍。（load_or_create_key 有同形不对称，但 key 的兜底是「生成并
  # 写回」，不会关掉已有配置；token 的兜底是「空 = 不限制」，会。所以这里补全。）
  if [ "$REMOTE" = "1" ]; then
    remote_tokens=$(ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
      "cat ${TOKENS_FILE} 2>/dev/null || true")
  else
    remote_tokens=$(cat "${TOKENS_FILE}" 2>/dev/null || true)
  fi

  if [ -n "$SIGNAL_TOKENS" ] && [ "$SIGNAL_TOKENS_CLEAR" = "1" ]; then
    echo "❌ SIGNAL_TOKENS 与 SIGNAL_TOKENS_CLEAR 同时给出，矛盾，中止"
    exit 1
  fi

  TOKENS="$remote_tokens"
  if [ -n "$SIGNAL_TOKENS" ]; then
    TOKENS="$SIGNAL_TOKENS"
    echo "── 信令 token 白名单：开启（$(echo "$TOKENS" | tr ',' '\n' | grep -c .) 个）──"
  elif [ "$SIGNAL_TOKENS_CLEAR" = "1" ]; then
    TOKENS=""
    echo "── 信令 token 白名单：显式关闭 ──"
  elif [ -n "$remote_tokens" ]; then
    echo "── 沿用服务器上已有的 token 白名单（不重置）──"
  else
    echo "── 信令 token 白名单：未开启（默认 = 不限制，行为与今天一致）──"
  fi

  # 字符集白名单，不是洁癖：这个值要穿过 ssh 命令行、sed 替换、systemd ExecStart
  # 三层。逐条实测过的炸点——
  #   空格：ssh 把远端命令按空格重拼，`PD_TOKENS=--tokens a b` 会变成执行 `b`；
  #   `$` ：systemd 对 ExecStart 做变量展开，含 `$` 的 token 会被替换成空/怪值，
  #          服务照常起，白名单却变成几条没人认识的条目（静默）；
  #   `&`：sed 替换串里 `&` 代表「整个匹配」，会写出循环文本。
  # 逗号外的字符一律拒绝，报错在部署**之前**，不是之后去 journalctl 考古。
  if [ -n "$TOKENS" ] && ! printf '%s' "$TOKENS" | grep -Eq '^[A-Za-z0-9._,-]+$'; then
    echo "❌ SIGNAL_TOKENS 含白名单字符集之外的内容（只允许 字母数字 . _ - 和逗号分隔）。"
    echo "   原因见上面的注释：空格炸 ssh、\$ 炸 systemd、& 炸 sed，且都是静默炸。"
    exit 1
  fi

  # 落盘沿用（权限 600，它是凭据）。关闭态则删除文件，
  # 否则下次「不带参数重部署」会把旧白名单当成现状沿用回来。
  # 读写对称：REMOTE=0 时状态文件就在本机，直接本地落盘（理由见上面的读取注释）。
  if [ "$REMOTE" = "1" ]; then
    if [ -n "$TOKENS" ]; then
      ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
        "mkdir -p ${REMOTE_DIR} && printf '%s\\n' '${TOKENS}' > ${TOKENS_FILE} && chmod 600 ${TOKENS_FILE}" \
        || { echo "❌ 写回 token 文件失败，中止（否则下次部署会按旧状态生成单元行）"; exit 1; }
    else
      ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
        "rm -f ${TOKENS_FILE}" || true
    fi
  else
    if [ -n "$TOKENS" ]; then
      umask 077
      mkdir -p "${REMOTE_DIR}" \
        && printf '%s\n' "$TOKENS" > "${TOKENS_FILE}" \
        && chmod 600 "${TOKENS_FILE}" \
        || { echo "❌ 写回 token 文件失败，中止（否则下次部署会按旧状态生成单元行）"; exit 1; }
    else
      rm -f "${TOKENS_FILE}" || true
    fi
  fi
}

# ====== 检查前置 =====
# 二进制改为**缺失时自动构建**。
# 原来是 `[ -f "$BINARY" ] || exit 1`——但 BINARY 指向 /tmp/peersignal-linux-amd64，
# 一个 /tmp 下的临时路径，clone 一份新仓库或重启一次机器它就没了。
# 于是「跑一下部署脚本」的第一步就 exit 1，而这条命令正是关闭 2026-10-06
# 那个线上泄漏所必需的。给一个修复步骤埋这种地雷是最坏组合：
# 它让人以为没别的办法，只能继续用旧二进制 / 继续不部署。
# 无条件重建。2026-10-06 实测踩到的坑：原来是 `[ ! -f "$BINARY" ]` 才构建，
# 于是 /tmp 里那个昨天编的二进制被直接推上线——而它还不认识新加的 --ops-token，
# 服务以 status=2/INVALIDARGUMENT 起不来。
# 「文件已存在就跳过构建」在这里是错的：部署要的是**当前这棵树**编出来的东西，
# 不是磁盘上碰巧残留的那一个。对一个把新代码送上线来说，缓存是负资产。
{
  echo "── 构建 ${BINARY}（每次都重建，避免推送陈旧产物）──"
  # Go 交叉编译。-tags nosqlite：与 sqlite3 的 CGO 三方冲突（见 AGENTS/CLAUDE 的构建约定），
  # 信令服务本身不碰 sqlite，但显式带上以免未来依赖变动时炸在这里。
  ( cd back/signalserver && \
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags nosqlite \
    -o "$BINARY" ./cmd/peersignal ) \
    || { echo "❌ 构建失败"; exit 1; }
  echo "✅ 已构建 $BINARY"
}

# ${1:-} ：脚本开头有 `set -u`，不这么写的话**不带参数调用**（也就是文件头
# 写的那个用法 `bash deploy-peersignal.sh`）会在这一行直接崩：
#   line 60: $1: unbound variable
# 于是一个功能完全正常的部署命令，默认用法跑不起来。
if [ "${1:-}" = "--remote" ]; then
  echo "=== 模式: 直接在 cloudcone 上执行 ==="
  REMOTE=0
else
  echo "=== 模式: 从本地 SSH 部署到 $CLOUDCONE_HOST ==="
  REMOTE=1
fi

# REMOTE 确定之后才能读服务器上的 key（REMOTE=0 时在本机读/写同一个文件）
load_or_create_key
# 同样要在 REMOTE 确定之后：token 白名单沿用服务器现状的逻辑与 key 一致
load_or_create_tokens

# ====== Step 1: 上传二进制 + 配置 ======
deploy_files() {
  echo "── Step 1: 上传文件 ──"
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" "mkdir -p ${REMOTE_DIR}"

  # ⚠️ 不要直接 scp 到 ${REMOTE_DIR}/peersignal。
  # scp 是**就地截断写入**，而那个文件正被运行中的服务占着，内核会拒绝：
  #   scp: dest open "/opt/peersignal/peersignal": Failure
  #   dd: failed to open 'peersignal': Text file busy
  # 2026-10-06 实测：只有服务在跑时才会撞上，所以「第一次部署能成功、
  # 第二次起就失败」，非常容易被误判成偶发网络问题。
  # 正解是先传到临时路径，再 mv 覆盖（rename 是原子的，不受占用限制）。
  scp -P "${CLOUDCONE_SSH_PORT}" "$BINARY" "${CLOUDCONE_USER}@${CLOUDCONE_HOST}:${REMOTE_DIR}/peersignal.new"
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
    "chmod +x ${REMOTE_DIR}/peersignal.new && mv -f ${REMOTE_DIR}/peersignal.new ${REMOTE_DIR}/peersignal"

  # 确认落地的是这次构建的产物，而不是上一次的残留
  local want_size got_size
  want_size=$(wc -c < "$BINARY")
  got_size=$(ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
    "wc -c < ${REMOTE_DIR}/peersignal")
  if [ "$want_size" != "$got_size" ]; then
    echo "❌ 上传后大小不符：本地 ${want_size} vs 远端 ${got_size}，中止（不重启服务）"
    exit 1
  fi
  echo "✅ 二进制已上传（${got_size} 字节，与本地一致）"
}

# ====== Step 2: systemd 服务 ======
deploy_systemd() {
  echo "── Step 2: 安装 systemd 服务 ──"
  # 把已求值的 KEY/OPS_TOKEN/TOKENS 传进远端（原因见 SYSTEMD_EOF 内那段说明）
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
    PD_KEY="${KEY}" PD_OPS="${OPS_TOKEN}" PD_TOKENS="${TOKENS}" bash -s <<'SYSTEMD_EOF'
cat > /etc/systemd/system/peersignal.service << 'UNIT'
[Unit]
Description=Peerdrive Peersignal (PeerJS Signaling + Discovery)
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/opt/peersignal/peersignal --addr 127.0.0.1:9000 --key pd-signal-KEYPLACEHOLDER --ops-token OPS_TOKENPLACEHOLDER TOKENS_PLACEHOLDER
# 三个安全开关（代码早已/现已支持，默认都不填 = 行为与现在完全一致）：
#   -cors-origin  面板侧 REST 端点的 CORS 白名单。不填 = 历史行为（通配 *）。
#               填了只回显命中的 Origin，没命中的**不发 Allow-Origin 头**，浏览器因此读不到。
#               ⚠️ 双击打开的面板 Origin 是 null，要保留它必须显式带上 null，例如：
#               --cors-origin "https://peerdrive.pages.dev,https://peerdrive.moonchan.xyz,null"
#   --ops-token   /status 与 /status/key 的凭据。2026-10-06 起**必须填**：
#               不填时 opsTokenOK 默认拒绝，面板会显示
#               「Error: 需要 ops token」而不是把错误 JSON 渲染成 undefined。
#               ⚠️ 它与 -tokens 是**两个独立开关**，这个分离是被实测逼出来的：
#               最初复用 -tokens 给运维面用，结果所有不发 token 的既有节点
#               全部连不上信令（跨节点传输断掉），而 HTTP 探测仍返回 200，
#               看起来一切正常。现在开启运维鉴权不会影响任何节点连接。
#   -tokens       信令注册白名单（审计 A-2 的部署侧开关，现在才真的可开）：
#               由 SIGNAL_TOKENS / SIGNAL_TOKENS_CLEAR 环境变量控制，三态见
#               本脚本 load_or_create_tokens 的注释。不填 = 不限制（默认；
#               关闭态下 ExecStart 行尾那段占位文字被 sed 整段删掉，
#               单元行与本开关存在之前逐字一致）。
#               ⚠️ **开启前必读**：这是三端防线，不是两端——
#                 ① 服务端校验：有（signalserver HandleWS 的 tokenWhitelist）；
#                 ② 面板发送口：2026-10 起有（panel「信令 token」输入框，
#                    peerOptions 条件带 token 字段）；
#                 ③ **Go 节点没有发送口**：internal/transport/peerjs_service.go
#                    只填 Host/Port/Key/Secure，go-peerjs 每次发**随机** token。
#                    今天单元行没有 --tokens，所以③无症状；一旦打开，
#                    **所有 peerdrive 节点会立刻连不上信令**（bad handshake），
#                    而 /discover/nodes 探测照常有响应——与 2026-10-06 那次
#                    「复用 -tokens 把节点全关在外面」是同一形态的坑。
#               也就是说：把白名单用于「只放行面板」可以立刻验证；要放行节点，
#               得先给 back/ 的 PEERDRIVE_PEERJS_TOKEN 补上（另属后端改动）。
#   -cors-origin  仍建议单独评估，见上面的说明。
# 除 --ops-token 外，其余开关保持原样；升级本身不应改变既有部署的行为。
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
Environment=GOMEMLIMIT=256MiB
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

# 替换 key 与 ops token。
#
# ⚠️ 这里原来在**远端** grep /tmp/deploy-peersignal.sh 里的 `KEY=`/`OPS_TOKEN=` 两行。
# 那是错的：远端 grep 到的是**字面文本** `KEY="pd-signal-$(openssl rand -hex 12)"`，
# 于是 sed 把 `$(openssl rand …)` 原样写进 systemd 单元——服务会拿着一个含 `$(` 的
# 字符串当凭据启动，看着配好了其实等于没配。
# （实测过：模拟这段解析，KEY 拿到的是 `pd-signal-$(op…` 而不是随机值。）
#
# 正确做法：两个值在**本地**第 24/29 行就已经求过了，直接传进来用，不在远端再算。
sed -i "s/pd-signal-KEYPLACEHOLDER/${PD_KEY}/" /etc/systemd/system/peersignal.service
sed -i "s/OPS_TOKENPLACEHOLDER/${PD_OPS}/" /etc/systemd/system/peersignal.service

# token 白名单格子（审计 A-2）。两条分支都必须落到「单元行里没有任何残留」：
#   - 开启：TOKENS_PLACEHOLDER → --tokens=a,b,c（等号形式，不让 systemd 按空格
#     再切一次；值已在本地校验过字符集，不含空格 / & / $，见下）
#   - 关闭：连**前面的空格**一起吃掉，ExecStart 行回到本开关存在之前的样子
# 为什么不在远端读环境变量 PEERJS_TOKENS：远端 shell 里没有这个变量（本脚本
# 从头到尾没用过它），照抄 services.go 的读法只会拿到空串——而「空 = 不限制」
# 恰好静默。这里必须走本地已求值的 PD_TOKENS，与 PD_KEY/PD_OPS 同一教训。
if [ -n "${PD_TOKENS:-}" ]; then
  sed -i "s/TOKENS_PLACEHOLDER/--tokens=${PD_TOKENS}/" /etc/systemd/system/peersignal.service
  echo "⚠️  信令 token 白名单已开启（$(echo "${PD_TOKENS}" | tr ',' '\n' | grep -c .) 个）。"
  echo "⚠️  Go 节点侧**没有** token 发送口（transport/peerjs_service.go 只填"
  echo "    Host/Port/Key/Secure，go-peerjs 每次发随机 token）。所有 peerdrive"
  echo "    节点此刻会被拒（bad handshake / 'Invalid token provided'）。"
  echo "    当前只影响面板（面板已有「信令 token」输入框）；要放行节点，"
  echo "    必须先补 back/ 的 PEERDRIVE_PEERJS_TOKEN。验证命令见脚本末尾。"
else
  sed -i "s/ *TOKENS_PLACEHOLDER//" /etc/systemd/system/peersignal.service
fi

# 兜底：占位符没被替换干净就**不许启动**。
# 单元里带着 `KEYPLACEHOLDER` 字样启动 = 服务起来了，但凭据是个字符串常量，
# 而外部看起来一切正常——这正是最坏的失败形态（静默）。
if grep -q PLACEHOLDER /etc/systemd/system/peersignal.service; then
  echo "❌ 单元里仍有未替换的占位符，拒绝启动："; grep PLACEHOLDER /etc/systemd/system/peersignal.service
  exit 1
fi

systemctl daemon-reload
systemctl enable peersignal

# ⚠️ 原来是 `systemctl start`。服务已在运行时，start 是**空操作**——不会重启。
# 于是部署脚本会：上传新二进制 → 重载单元 → 打印「✅ 服务已启动」，
# 而旧进程**仍在用被删掉的 inode 继续服务**（/proc/<pid>/exe 显示 (deleted)）。
# 2026-10-06 实测：新二进制的行为在直连 9000 时完全没出现（/status/key 仍 404），
# 原因就是这个。所以必须 restart，不是 start。
systemctl restart peersignal
sleep 2
systemctl status peersignal --no-pager -l | head -10

# 确认真的换成了新进程：重启后 pid 必须变，且 exe 不再是 (deleted)
NEW_PID=$(systemctl show peersignal -p MainPID --value)
if [ "${NEW_PID}" = "0" ] || [ "${NEW_PID}" = "0" ]; then
  echo "❌ 服务没起来（MainPID=0），中止"; exit 1
fi
if [ -e "/proc/${NEW_PID}/exe" ] && readlink "/proc/${NEW_PID}/exe" | grep -q 'deleted'; then
  echo "❌ 新进程仍指向已删除的 inode，说明 restart 没生效，中止"; exit 1
fi
echo "✅ systemd 服务已启动（新 pid=${NEW_PID}）"
SYSTEMD_EOF
}

# ====== Step 3: nginx 反向代理 ======
deploy_nginx() {
  echo "── Step 3: 配置 nginx ──"
  # PD_DOMAIN 由本地求好后传入（原因见 NGINX_EOF 内的说明）
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
    PD_DOMAIN="${DOMAIN}" bash -s <<'NGINX_EOF'
cat > /etc/nginx/sites-available/peersignal << 'CONF'
server {
    listen 80;
    server_name PEERSIGNAL_DOMAIN_PLACEHOLDER;

    # WebSocket 信令
    location /peerjs {
        proxy_pass http://127.0.0.1:9000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }

    # 发现 API
    location /discover/ {
        proxy_pass http://127.0.0.1:9000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    # 状态 + dashboard
    location /status {
        proxy_pass http://127.0.0.1:9000;
        proxy_set_header Host $host;
    }

    location / {
        proxy_pass http://127.0.0.1:9000;
        proxy_set_header Host $host;
    }
}
CONF

# 替换域名。同 Step 2：PD_DOMAIN 由**本地**第 20 行求好后传进来。
# 原来在远端 grep /tmp/deploy-peersignal.sh（那份文件不在远端），DOMAIN 解析为空，
# 于是写出 `server_name ;` —— nginx -t 直接 emerg：
#   invalid number of arguments in "server_name" directive
# 而脚本紧接着 systemctl reload nginx，失败也不拦，最后照样打印「✅ nginx 已配置」。
# 后果是：旧 worker 继续用旧配置跑，**HTTP 探测（curl /status）照样有响应**，
# 掩盖了配置已坏；只有 WebSocket 升级受影响，表现为节点连不上、
# 客户端报 `websocket: bad handshake`。这种「一半好一半坏」最难查。
sed -i "s/PEERSIGNAL_DOMAIN_PLACEHOLDER/${PD_DOMAIN}/" /etc/nginx/sites-available/peersignal

if grep -q "server_name[[:space:]]*;" /etc/nginx/sites-available/peersignal; then
  echo "❌ server_name 为空，nginx 配置无效，拒绝 reload："
  grep -n "server_name" /etc/nginx/sites-available/peersignal
  exit 1
fi

ln -sf /etc/nginx/sites-available/peersignal /etc/nginx/sites-enabled/peersignal
# 必须先验证再 reload。`nginx -t` 失败时**不能**继续 reload——
# 否则旧 worker 顶着旧配置继续服务，把「配置已坏」伪装成「一切正常」。
if ! nginx -t; then
  echo "❌ nginx 配置校验失败，未 reload（线上仍用旧配置）"; exit 1
fi
systemctl reload nginx
echo "✅ nginx 已配置并通过 nginx -t"
NGINX_EOF
}

# ====== Step 4: Cloudflare DNS ======
setup_cloudflare() {
  echo "── Step 4: Cloudflare DNS ──"
  cat <<EOF
  请在 Cloudflare 控制台为 ${DOMAIN} 添加 A 记录：

  Name:    peersignal
  Type:    A
  Content: 127.26.9.5
  Proxy:   ✅ Proxied (橙云)
  TTL:     Auto

  然后配置 SSL/TLS:
    - Encryption Mode: Full (严格模式需 cloudcone 有证书)
    - 或 Full (Flexible 模式，Cloudflare 终止 TLS)

  验证：
    dig ${DOMAIN}
    curl -I https://${DOMAIN}/status
EOF
}

# ====== 执行 ======
if [ "$REMOTE" -eq 1 ]; then
  deploy_files
  deploy_systemd
  deploy_nginx
fi
setup_cloudflare

echo ""
echo "=== 部署完成 ==="
echo "域名:    https://${DOMAIN}"
echo "信令:    wss://${DOMAIN}/peerjs"
echo "发现:    https://${DOMAIN}/discover/nodes"
echo "状态:    https://${DOMAIN}/status?token=<下面的 OPS Token>"
echo "API Key: ${KEY}"
echo ""
# 这一行是必须的，不是可选的礼节：
# OPS_TOKEN 每次部署都是新生成的随机值，**只出现在这一处输出**（不进仓库、不进前端）。
# 不打印它，运维拿不到访问 /status 的凭据 —— 那时唯一的选择是把面板留着半坏，
# 或者退回匿名可读（也就是这个脚本本来要堵的洞）。
echo "=== 凭据（本次部署新生成，只在这里出现一次，请自行妥善保存）==="
echo "OPS Token: ${OPS_TOKEN}"
echo "  查看面板:  https://${DOMAIN}/?token=${OPS_TOKEN}"
echo "  读取信令 key: curl 'https://${DOMAIN}/status/key?token=${OPS_TOKEN}'"
echo "  ⚠️ 没有它，/status 会返回 401（这是预期行为，不是故障）。"
echo ""
# 白名单状态必须在这里出现（审计 A-2）：这一层此前「配了没生效」之所以能存活
# 两轮，就因为部署输出从不报告它是开是关。现在每次部署都打印现状 + 验证命令。
if [ -n "${TOKENS:-}" ]; then
  echo "信令 token 白名单: ✅ 开启（$(echo "${TOKENS}" | tr ',' '\n' | grep -c .) 个条目）"
  echo "  手动验证（两端对上了，这条防线才算真的生效）："
  echo "    ① 不带 token → 升级被拒（白名单拦在 HTTP 升级层，状态码可直接观测）："
  echo "       curl -is -o /dev/null -w '%{http_code}\\n' \\"
  echo "         -H 'Connection: Upgrade' -H 'Upgrade: websocket' \\"
  echo "         -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \\"
  echo "         'https://${DOMAIN}/peerjs?key=${KEY}&id=t1&token=not-in-list'   # 期望 400"
  echo "    ② 同上一条，把 token=not-in-list 换成白名单里的值 → 期望 101"
  echo "    ③ 面板：host/port/key 照默认，「信令 token」留空 → 连不上；"
  echo "       填入②那个值 → 连上（这就是 A-2 说的「两端对上」）"
  echo "    ④ 服务端日志: journalctl -u peersignal -n 20（看 'Invalid token provided'）"
  echo "  ⚠️ peerdrive **节点**没有 token 发送口（back/ 未配 PEERDRIVE_PEERJS_TOKEN，"
  echo "     go-peerjs 每次发随机 token）：开着白名单 = 所有节点连不上信令。"
  echo "     只给面板用可先行；要放行节点必须先补后端那一端。"
else
  echo "信令 token 白名单: ⬜ 未开启（默认 = 不限制，既有节点与面板行为不变）"
  echo "  要开启: SIGNAL_TOKENS=\"tok1,tok2\" bash scripts/deploy-peersignal.sh"
  echo "  显式关闭: SIGNAL_TOKENS_CLEAR=1 bash scripts/deploy-peersignal.sh"
fi
echo ""
echo "节点配置:"
echo "  PEERDRIVE_PEERJS_HOST=${DOMAIN}"
echo "  PEERDRIVE_PEERJS_PORT=443"
echo "  PEERDRIVE_PEERJS_SECURE=true"
echo "  PEERDRIVE_PEERJS_KEY=${KEY}"
echo "  PEERDRIVE_DISCOVER_URL=https://${DOMAIN}"
