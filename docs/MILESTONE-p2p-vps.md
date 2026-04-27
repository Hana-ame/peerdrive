# P2P VPS 部署里程碑

> 开始: 2026-04-28

## 目标

让 `bwh.moonchan.xyz` 作为 Peerdrive 公共基础设施：
- Registration Server（用户注册认证）
- Public Relay Node（libp2p 中继）
- Public Bootstrap Node（P2P 网络入口）
- 本地节点通过 VPS 互相发现和通信

## 阶段

### 阶段 1: 环境检查
- [ ] 检查 VPS 运行状态
- [ ] 检查现有服务（wsl-3000 的 API、P2P 节点）
- [ ] 检查 registration-server 是否部署
- [ ] 检查防火墙/端口开放情况

### 阶段 2: Registration Server
- [ ] 部署 registration-server 到 VPS
- [ ] 验证用户注册/登录 API
- [ ] 配置 CORS 和域名

### 阶段 3: Public Relay + Bootstrap
- [ ] 在 VPS 启动 libp2p 中继节点
- [ ] 配置为公网可达（ForceReachabilityPublic）
- [ ] 测试本地节点通过 VPS 中继连接
- [ ] 测试 DHT 发现

### 阶段 4: IPv6
- [ ] Go 后端配置 IPv6 监听
- [ ] libp2p 监听 `/ip6/::/tcp/0`
- [ ] 测试 IPv6 连接

### 阶段 5: 测试
- [ ] curl 测试 registration API
- [ ] Go 测试 P2P 连接
- [ ] Python 脚本测试多节点文件传输
- [ ] Playwright 测试前端 P2P 状态

### 阶段 6: 文档
- [ ] P2P VPS 部署指南
- [ ] 用户使用手册（如何连接公共节点）

## 问题记录

| # | 日期 | 问题 | 状态 |
|---|------|------|------|
