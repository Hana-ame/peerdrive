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
KEY="pd-signal-$(openssl rand -hex 12)"  # API key（自动生成）

# ops token：/status 与 /status/key 的凭据。**2026-10-06 起必须配。**
# 不配的后果是实测出来的，不是推演：
#   curl https://peersignal.moonchan.xyz/status   → 200 + 信令 key + 全网节点名册
# 因为 opsTokenOK 在白名单为空时曾经「任何非空 token 都放行」，
# 于是 `?token=随便编的` 也能进。现已改成默认拒绝（signalserver.go:343）。
# 这个 token 只给运维从命令行查面板用，不进前端、不进仓库。
OPS_TOKEN="pd-ops-$(openssl rand -hex 16)"
BINARY="/tmp/peersignal-linux-amd64"
REMOTE_DIR="/opt/peersignal"
SYSTEMD_SERVICE="peersignal"

# ====== 定位仓库根 =====
# 原脚本只写了一句「在仓库根目录执行」，靠人自觉。2026-10-06 改成自己算：
# 从脚本自身位置往上找 go.mod 所在目录，这样从任何 cwd 调用都对。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$REPO_ROOT"
[ -d "back/signalserver" ] || { echo "❌ 不是 peerdrive 仓库（缺 back/signalserver）: $REPO_ROOT"; exit 1; }

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
  # 把已求值的 KEY/OPS_TOKEN 传进远端（原因见 SYSTEMD_EOF 内那段说明）
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" \
    PD_KEY="${KEY}" PD_OPS="${OPS_TOKEN}" bash -s <<'SYSTEMD_EOF'
cat > /etc/systemd/system/peersignal.service << 'UNIT'
[Unit]
Description=Peerdrive Peersignal (PeerJS Signaling + Discovery)
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/opt/peersignal/peersignal --addr 127.0.0.1:9000 --key pd-signal-KEYPLACEHOLDER --ops-token OPS_TOKENPLACEHOLDER
# 两个安全开关（代码早已/现已支持，默认都不填 = 行为与现在完全一致）：
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
#   -tokens       信令注册白名单。**默认不填**（= 不限制），保持既有节点可用。
#               真要收紧时单独评估：一旦打开，所有节点都必须发白名单里的 token。
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
echo "节点配置:"
echo "  PEERDRIVE_PEERJS_HOST=${DOMAIN}"
echo "  PEERDRIVE_PEERJS_PORT=443"
echo "  PEERDRIVE_PEERJS_SECURE=true"
echo "  PEERDRIVE_PEERJS_KEY=${KEY}"
echo "  PEERDRIVE_DISCOVER_URL=https://${DOMAIN}"
