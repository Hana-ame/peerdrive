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
  ssh "${CLOUDCONE_USER}@${CLOUDCONE_HOST}" -p "${CLOUDCONE_SSH_PORT}" bash -s <<'SYSTEMD_EOF'
cat > /etc/systemd/system/peersignal.service << 'UNIT'
[Unit]
Description=Peerdrive Peersignal (PeerJS Signaling + Discovery)
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/opt/peersignal/peersignal --addr 127.0.0.1:9000 --key pd-signal-KEYPLACEHOLDER
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
Environment=GOMEMLIMIT=256MiB
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

# 替换 key
KEY=$(grep "KEY=" /tmp/deploy-peersignal.sh | sed 's/.*="//;s/".*//')
sed -i "s/pd-signal-KEYPLACEHOLDER/${KEY}/" /etc/systemd/system/peersignal.service

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
