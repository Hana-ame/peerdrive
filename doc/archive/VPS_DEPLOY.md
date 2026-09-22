# Peerdrive VPS 部署指南

> 目标: bwh.moonchan.xyz 运行 Registration Server + Public Relay + Public Node + STUN/TURN

## 组件

| 组件 | 端口 | 说明 |
|------|------|------|
| Peerdrive Backend | 3000 | HTTP API + libp2p 公共中继 |
| Registration Server | 4000 | 用户注册/认证 (JWT) |
| STUN Server | 3478 UDP/TCP | NAT 类型探测、IP/端口映射 |
| TURN Server | 5349 TLS | 无法打洞时中继媒体/数据 |
| libp2p Relay | 4001 TCP | libp2p 中继转发 |

## 端口与防火墙

```bash
# 开放所有 P2P 相关端口
ufw allow 3000/tcp           # HTTP API
ufw allow 4000/tcp           # Registration
ufw allow 4001/tcp           # libp2p relay (IPv4)
ufw allow 4001/tcp6          # libp2p relay (IPv6)
ufw allow 3478/udp           # STUN (UDP)
ufw allow 3478/tcp           # STUN (TCP, 备用)
ufw allow 5349/tcp           # TURN (TLS)

# 启用
ufw enable
ufw status verbose
```

## 快速部署

### 1. STUN/TURN Server (coturn)

在 VPS 上安装并配置 coturn:

```bash
# 安装 coturn
apt update && apt install -y coturn

# 编辑配置文件
cat > /etc/turnserver.conf << 'TURNEOF'
listening-port=3478
tls-listening-port=5349
fingerprint
realm=bwh.moonchan.xyz
server-name=bwh.moonchan.xyz
lt-cred-mech
user=peerdrive:change-me-to-secret-password
total-quota=100
stale-nonce
no-multicast-peers
mobility
no-cli
TURNEOF

# 启用并启动 coturn
systemctl enable coturn
systemctl start coturn

# 验证
systemctl status coturn
```

如果不需要 TURN 认证（仅使用 STUN），可简化为:

```bash
# 仅为 STUN 服务 (无需认证)
cat > /etc/turnserver.conf << 'STUNEOF'
listening-port=3478
fingerprint
realm=bwh.moonchan.xyz
server-name=bwh.moonchan.xyz
stale-nonce
no-multicast-peers
no-cli
# 仅 STUN，不提供 TURN 中继
no-tls
no-dtls
STUNEOF
```

### 2. Peerdrive Backend (公共中继模式)

```bash
# 克隆代码
git clone https://github.com/Hana-ame/peerdrive.git
cd peerdrive/go

# 编译
go build -o peerdrive ./cmd/server/main.go

# 启动 (公共中继节点 + STUN/TURN 配置)
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN=/ip4/0.0.0.0/tcp/4001 \
PEERDRIVE_P2P_LISTEN_V6=/ip6/::/tcp/4001 \
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=server \
PEERDRIVE_PUBLIC_REACHABLE=true \
PEERDRIVE_PUBLIC_DOMAIN=bwh.moonchan.xyz \
PEERDRIVE_MDNS_ENABLE=true \
PEERDRIVE_HOLE_PUNCH=true \
PEERDRIVE_AUTO_NAT=true \
PEERDRIVE_STUN_SERVER=stun:bwh.moonchan.xyz:3478 \
PEERDRIVE_TURN_SERVER=turn:bwh.moonchan.xyz:5349 \
PEERDRIVE_TURN_USER=peerdrive \
PEERDRIVE_TURN_PASS=change-me-to-secret-password \
PORT=3000 \
./peerdrive
```

### 3. Registration Server

```bash
cd peerdrive/registration-server

# 编译
go build -o reg-server ./cmd/server/main.go

# 配置 JWT_SECRET
export JWT_SECRET="your-secret-key"
export PORT=4000
export DB_PATH=./registration.db
export REGISTRATION_HOST=https://bwh.moonchan.xyz:4000

./reg-server
```

### 4. 防火墙

```bash
# VPS 防火墙开放所有端口
ufw allow 3000/tcp        # HTTP API
ufw allow 4000/tcp        # Registration
ufw allow 4001/tcp        # libp2p (IPv4)
ufw allow 4001/tcp6       # libp2p (IPv6)
ufw allow 3478/udp        # STUN UDP
ufw allow 3478/tcp        # STUN TCP
ufw allow 5349/tcp        # TURN TLS
```

## systemd 服务文件

### Peerdrive Backend 服务

`/etc/systemd/system/peerdrive.service`:

```ini
[Unit]
Description=Peerdrive Backend (P2P Relay + API)
After=network.target
Wants=coturn.service

[Service]
Type=simple
User=peerdrive
Group=peerdrive
WorkingDirectory=/opt/peerdrive
ExecStart=/opt/peerdrive/peerdrive
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

# P2P Relay config
Environment=PEERDRIVE_P2P_ENABLE=true
Environment=PEERDRIVE_P2P_LISTEN=/ip4/0.0.0.0/tcp/4001
Environment=PEERDRIVE_P2P_LISTEN_V6=/ip6/::/tcp/4001
Environment=PEERDRIVE_RELAY_ENABLE=true
Environment=PEERDRIVE_RELAY_MODE=server
Environment=PEERDRIVE_PUBLIC_REACHABLE=true
Environment=PEERDRIVE_PUBLIC_DOMAIN=bwh.moonchan.xyz
Environment=PEERDRIVE_MDNS_ENABLE=true
Environment=PEERDRIVE_HOLE_PUNCH=true
Environment=PEERDRIVE_AUTO_NAT=true
Environment=PEERDRIVE_NAT_PORTMAP=false

# STUN/TURN
Environment=PEERDRIVE_STUN_SERVER=stun:bwh.moonchan.xyz:3478
Environment=PEERDRIVE_TURN_SERVER=turn:bwh.moonchan.xyz:5349
Environment=PEERDRIVE_TURN_USER=peerdrive
Environment=PEERDRIVE_TURN_PASS=change-me-to-secret-password

# API
Environment=PORT=3000

# Storage
Environment=PEERDRIVE_STORAGE=/opt/peerdrive/storage
Environment=PEERDRIVE_STORAGE_ENABLE=true

# Allowed origins
Environment=PEERDRIVE_ALLOWED_ORIGINS=http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev

# Registration
Environment=PEERDRIVE_REG_SERVER=http://localhost:4000

[Install]
WantedBy=multi-user.target
```

### Registration Server 服务

`/etc/systemd/system/peerdrive-reg.service`:

```ini
[Unit]
Description=Peerdrive Registration Server
After=network.target

[Service]
Type=simple
User=peerdrive
Group=peerdrive
WorkingDirectory=/opt/peerdrive/registration
ExecStart=/opt/peerdrive/registration/reg-server
Restart=on-failure
RestartSec=5

Environment=PORT=4000
Environment=DB_PATH=/opt/peerdrive/registration/registration.db
Environment=JWT_SECRET=your-secret-key
Environment=REGISTRATION_HOST=https://bwh.moonchan.xyz:4000

[Install]
WantedBy=multi-user.target
```

### 启用服务

```bash
# 创建 peerdrive 用户
useradd -r -s /bin/false -d /opt/peerdrive peerdrive
mkdir -p /opt/peerdrive/storage /opt/peerdrive/registration
chown -R peerdrive:peerdrive /opt/peerdrive

# 放置二进制文件
cp peerdrive /opt/peerdrive/
cp registration-server/reg-server /opt/peerdrive/registration/

# 安装服务
cp peerdrive.service /etc/systemd/system/
cp peerdrive-reg.service /etc/systemd/system/
systemctl daemon-reload

# 启动服务
systemctl enable peerdrive-reg peerdrive
systemctl start peerdrive-reg peerdrive

# 检查状态
systemctl status peerdrive-reg peerdrive
```

## Docker Compose 部署

`docker-compose.yml`:

```yaml
version: "3.9"

services:
  coturn:
    image: instrumentisto/coturn:latest
    restart: unless-stopped
    network_mode: host
    volumes:
      - ./turnserver.conf:/etc/coturn/turnserver.conf:ro
    environment:
      - DETECT_EXTERNAL_IP=yes

  registration:
    build:
      context: ./registration-server
      dockerfile: Dockerfile
    restart: unless-stopped
    ports:
      - "4000:4000"
    environment:
      PORT: "4000"
      DB_PATH: /data/registration.db
      JWT_SECRET: your-secret-key
      REGISTRATION_HOST: https://bwh.moonchan.xyz:4000
    volumes:
      - reg-data:/data

  peerdrive:
    build:
      context: ./go
      dockerfile: Dockerfile
    restart: unless-stopped
    network_mode: host
    depends_on:
      - registration
      - coturn
    environment:
      PORT: "3000"
      PEERDRIVE_STORAGE: /data/storage
      PEERDRIVE_STORAGE_ENABLE: "true"
      PEERDRIVE_P2P_ENABLE: "true"
      PEERDRIVE_P2P_LISTEN: /ip4/0.0.0.0/tcp/4001
      PEERDRIVE_P2P_LISTEN_V6: /ip6/::/tcp/4001
      PEERDRIVE_RELAY_ENABLE: "true"
      PEERDRIVE_RELAY_MODE: server
      PEERDRIVE_PUBLIC_REACHABLE: "true"
      PEERDRIVE_PUBLIC_DOMAIN: bwh.moonchan.xyz
      PEERDRIVE_MDNS_ENABLE: "true"
      PEERDRIVE_HOLE_PUNCH: "true"
      PEERDRIVE_AUTO_NAT: "true"
      PEERDRIVE_STUN_SERVER: stun:bwh.moonchan.xyz:3478
      PEERDRIVE_TURN_SERVER: turn:bwh.moonchan.xyz:5349
      PEERDRIVE_TURN_USER: peerdrive
      PEERDRIVE_TURN_PASS: change-me-to-secret-password
      PEERDRIVE_REG_SERVER: http://localhost:4000
      PEERDRIVE_ALLOWED_ORIGINS: http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev
    volumes:
      - peerdrive-data:/data

volumes:
  reg-data:
  peerdrive-data:
```

> 注意: peerdrive 和 coturn 使用 `network_mode: host` 以便 libp2p 和 STUN/TURN 直接绑定主机端口。

## 健康检查

### Peerdrive Backend API

```bash
# 基础健康检查
curl -s http://localhost:3000/ping
# 预期: {"message":"pong"}

# P2P 状态
curl -s http://localhost:3000/p2p/status
# 预期: {"enabled":true,"peerID":"...","addrs":[...],"relayMode":"server",...}

# 已连接节点
curl -s http://localhost:3000/p2p/peers

# 带域名的 HTTPS 检查 (Nginx 反向代理后)
curl -s https://bwh.moonchan.xyz:3000/ping
```

### Registration Server

```bash
# 用户注册
curl -s -X POST http://localhost:4000/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"test","password":"test123"}'

# 用户登录（获取 JWT）
TOKEN=$(curl -s -X POST http://localhost:4000/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"test","password":"test123"}' | jq -r '.token')

# 验证登录状态
curl -s http://localhost:4000/auth/whoami \
  -H "Authorization: Bearer $TOKEN"

# 健康检查
curl -s http://localhost:4000/api/health \
  -H "Authorization: Bearer $TOKEN"
```

### STUN/TURN Server

```bash
# 检查 coturn 是否监听
ss -tulpn | grep -E '3478|5349'

# 使用 stunclient 测试 STUN (需安装: apt install stuntman-client)
stunclient bwh.moonchan.xyz 3478
# 预期: 返回公网 IP 和端口

# 日志查看
journalctl -u coturn --no-pager -n 50

# curl 测试 STUN (UDP 无法直接用 curl, 使用 nc)
echo -n "" | nc -u -w 2 bwh.moonchan.xyz 3478
```

## 本地节点连接

### 启动本地节点 (使用 VPS STUN/TURN)

```bash
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN=/ip4/0.0.0.0/tcp/0 \
PEERDRIVE_P2P_LISTEN_V6=/ip6/::/tcp/0 \
PEERDRIVE_BOOTSTRAP_PEER=/ip4/<VPS_IP>/tcp/4001/p2p/<VPS_PEER_ID> \
PEERDRIVE_STATIC_RELAYS=/ip4/<VPS_IP>/tcp/4001/p2p/<VPS_PEER_ID> \
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=client \
PEERDRIVE_STUN_SERVER=stun:bwh.moonchan.xyz:3478 \
PEERDRIVE_TURN_SERVER=turn:bwh.moonchan.xyz:5349 \
PEERDRIVE_TURN_USER=peerdrive \
PEERDRIVE_TURN_PASS=change-me-to-secret-password \
PORT=3001 \
go run ./cmd/server/main.go
```

### 验证连接

```bash
# 检查 VPS 节点状态
curl https://bwh.moonchan.xyz:3000/p2p/status

# 查看已连接节点
curl https://bwh.moonchan.xyz:3000/p2p/peers

# 本地节点连接 VPS
curl -X POST http://localhost:3001/p2p/connect \
  -H 'Content-Type: application/json' \
  -d '{"addr":"/ip4/<VPS_IP>/tcp/4001/p2p/<VPS_PEER_ID>"}'

# Ping VPS
curl http://localhost:3001/p2p/ping/<VPS_PEER_ID>
```

## 测试

```bash
# 多节点 e2e 测试
bash test/p2p_vps_e2e.sh

# Python 脚本测试文件传输
python3 test/p2p_vps_transfer.py
```

## 常见问题

### STUN 不工作

1. 确认端口 3478/udp 在防火墙中开放
2. 确认 coturn 正在运行: `systemctl status coturn`
3. 检查日志: `journalctl -u coturn --no-pager -n 100`
4. 从外部用 `stunclient` 测试

### TURN 连接失败

1. 确保 TURN 用户凭据正确
2. 确认 5349/tcp 端口开放
3. 如果使用 TLS, 需要配置证书 (将 cert.pem / privkey.pem 放到 /etc/ssl/)
4. 在 turnserver.conf 中添加:
   ```
   cert=/etc/ssl/certs/cert.pem
   pkey=/etc/ssl/private/privkey.pem
   ```
