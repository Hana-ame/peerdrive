# Peerdrive VPS 部署指南

> 目标: bwh.moonchan.xyz 运行 Registration Server + Public Relay + Public Node

## 组件

| 组件 | 端口 | 说明 |
|------|------|------|
| Peerdrive Backend | 3000 | HTTP API + libp2p 公共中继 |
| Registration Server | 4000 | 用户注册/认证 (JWT) |
| libp2p Relay | P2P 协议端口 | libp2p 中继转发 |

## 快速部署

### 1. Peerdrive Backend (公共中继模式)

```bash
# 克隆代码
git clone https://github.com/Hana-ame/peerdrive.git
cd peerdrive/go

# 编译
go build -o peerdrive ./cmd/server/main.go

# 启动 (公共中继节点)
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
PORT=3000 \
./peerdrive
```

### 2. Registration Server

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

### 3. 防火墙

```bash
# VPS 防火墙开放端口
ufw allow 3000/tcp   # HTTP API
ufw allow 4000/tcp   # Registration
ufw allow 4001/tcp   # libp2p (IPv4)
ufw allow 4001/tcp6  # libp2p (IPv6)
```

## 本地节点连接

### 启动本地节点

```bash
PEERDRIVE_P2P_ENABLE=true \
PEERDRIVE_P2P_LISTEN=/ip4/0.0.0.0/tcp/0 \
PEERDRIVE_P2P_LISTEN_V6=/ip6/::/tcp/0 \
PEERDRIVE_BOOTSTRAP_PEER=/ip4/<VPS_IP>/tcp/4001/p2p/<VPS_PEER_ID> \
PEERDRIVE_STATIC_RELAYS=/ip4/<VPS_IP>/tcp/4001/p2p/<VPS_PEER_ID> \
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=client \
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
