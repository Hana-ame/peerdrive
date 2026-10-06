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
CLOUDCONE_HOST="127.26.9.5"          # cloudcone IP（或 SSH 别名）
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

# ====== 检查前置 ======
[ -f "$BINARY" ] || { echo "❌ 二进制不存在: $BINARY"; exit 1; }

if [ "$1" = "--remote" ]; then
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
  scp -P "${CLOUDCONE_SSH_PORT}" "$BINARY" "${CLOUDCONE_USER}@${CLOUDCONE_HOST}:${REMOTE_DIR}/peersignal"
  echo "✅ 二进制已上传"
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
ExecStart=/opt/peersignal/peersignal --addr 127.0.0.1:9000 --key pd-signal-KEYPLACEHOLDER --tokens OPS_TOKENPLACEHOLDER
# 两个安全开关（代码早已/现已支持，默认都不填 = 行为与现在完全一致）：
#   -cors-origin  面板侧 REST 端点的 CORS 白名单。不填 = 历史行为（通配 *）。
#               填了只回显命中的 Origin，没命中的**不发 Allow-Origin 头**，浏览器因此读不到。
#               ⚠️ 双击打开的面板 Origin 是 null，要保留它必须显式带上 null，例如：
#               --cors-origin "https://peerdrive.pages.dev,https://peerdrive.moonchan.xyz,null"
#   -tokens       同一个白名单现在同时管**信令注册**与**运维面（/status、/status/key）**。
#               ⚠️ 2026-10-06 起**必须填**：不填时 opsTokenOK 默认拒绝，
#               面板会显示「Error: 需要 ops token」而不是伪装成正常数据。
#               它的另一个作用是信令注册白名单——不填则任意客户端都能注册任意 id
#               冒充在线节点收走信令。
#               取值：下面脚本生成的 OPS_TOKEN（部署完会打印一次）。
#   -cors-origin  仍建议单独评估，见上面的说明。
# 除 -tokens 外，其余开关保持原样；升级本身不应改变既有部署的行为。
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
Environment=GOMEMLIMIT=256MiB
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

# 替换 key 与 ops token
KEY=$(grep '^KEY=' /tmp/deploy-peersignal.sh | head -1 | sed 's/.*="//;s/".*//')
OPS=$(grep '^OPS_TOKEN=' /tmp/deploy-peersignal.sh | head -1 | sed 's/.*="//;s/".*//')
sed -i "s/pd-signal-KEYPLACEHOLDER/${KEY}/" /etc/systemd/system/peersignal.service
sed -i "s/OPS_TOKENPLACEHOLDER/${OPS}/" /etc/systemd/system/peersignal.service

systemctl daemon-reload
systemctl enable peersignal
systemctl start peersignal
sleep 2
systemctl status peersignal --no-pager -l | head -10
echo "✅ systemd 服务已启动"
SYSTEMD_EOF
}

# ====== Step 3: nginx 反向代理 ======
deploy_nginx() {
  echo "── Step 3: 配置 nginx ──"
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" bash -s <<'NGINX_EOF'
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

# 替换域名
DOMAIN=$(grep "DOMAIN=" /tmp/deploy-peersignal.sh | sed 's/.*="//;s/".*//')
sed -i "s/PEERSIGNAL_DOMAIN_PLACEHOLDER/${DOMAIN}/" /etc/nginx/sites-available/peersignal

ln -sf /etc/nginx/sites-available/peersignal /etc/nginx/sites-enabled/peersignal
nginx -t
systemctl reload nginx
echo "✅ nginx 已配置"
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
echo "状态:    https://${DOMAIN}/status"
echo "API Key: ${KEY}"
echo ""
echo "节点配置:"
echo "  PEERDRIVE_PEERJS_HOST=${DOMAIN}"
echo "  PEERDRIVE_PEERJS_PORT=443"
echo "  PEERDRIVE_PEERJS_SECURE=true"
echo "  PEERDRIVE_PEERJS_KEY=${KEY}"
echo "  PEERDRIVE_DISCOVER_URL=https://${DOMAIN}"
