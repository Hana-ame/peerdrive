# P2P VPS 部署里程碑

> 开始: 2026-04-28

## 目标

让 `bwh.moonchan.xyz` 作为 Peerdrive 公共基础设施：
- Registration Server（用户注册认证）
- Public Relay Node（libp2p 中继）
- Public Bootstrap Node（P2P 网络入口）
- STUN/TURN Server（NAT 穿透与中继）
- 本地节点通过 VPS 互相发现和通信

## 阶段

### 阶段 1: 环境检查 ✅
- [x] 检查 VPS 运行状态 (wsl-3000.moonchan.xyz 在线)
- [x] 检查现有服务 (Peerdrive API: pong, P2P: disabled)
- [x] 检查 registration-server 是否部署
- [x] 检查防火墙/端口开放情况

### 阶段 2: Registration Server ✅
- [x] 部署 registration-server 到 VPS
- [x] 验证用户注册/登录 API
- [x] 配置 CORS 和域名

### 阶段 3: Public Relay + Bootstrap ✅
- [x] 在 VPS 启动 libp2p 中继节点
- [x] 配置为公网可达（ForceReachabilityPublic）
- [x] 测试本地节点通过 VPS 中继连接
- [x] 测试 DHT 发现

### 阶段 4: IPv6 ✅
- [x] Go 后端配置 IPv6 监听 (P2PListenAddrV6)
- [x] libp2p 双栈监听 (IPv4 + IPv6)
- [ ] 测试 IPv6 连接

### 阶段 5: 测试
- [ ] curl 测试 registration API
- [ ] Go 测试 P2P 连接
- [ ] Python 脚本测试多节点文件传输
- [ ] Playwright 测试前端 P2P 状态

### 阶段 6: 文档 ✅
- [x] P2P VPS 部署指南
- [x] 用户使用手册（如何连接公共节点）

### 阶段 7: STUN/TURN 部署
- [ ] 安装并配置 coturn (STUN 3478 UDP/TCP, TURN 5349 TLS)
- [ ] 配置防火墙开放 STUN/TURN 端口
- [ ] 验证 STUN 响应 (stunclient)
- [ ] 配置 Peerdrive 后端连接 STUN/TURN Server
- [ ] 测试 NAT 穿透成功率
- [ ] 编写 Docker Compose 文件包含 coturn
- [ ] 编写 systemd 服务文件

## 配置字段

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `PEERDRIVE_STUN_SERVER` | `stun:stun.moonchan.xyz:3478` | STUN 服务器地址 |
| `PEERDRIVE_TURN_SERVER` | `""` | TURN 服务器地址 |
| `PEERDRIVE_TURN_USER` | `""` | TURN 认证用户名 |
| `PEERDRIVE_TURN_PASS` | `""` | TURN 认证密码 |

## 问题记录

| # | 日期 | 问题 | 状态 |
|---|------|------|------|
