# Peerdrive Dual-Stack P2P TODO

> Branch: feat/bt-dht · Status: 🚧 IN PROGRESS

## 已验证 (Updated 2026-04-28)

- ✅ IPFS/libp2p 双节点连接 (A↔B + Docker↔WSL)
- ✅ BT DHT 连接全球网络 (127+ 节点)
- ✅ BT DHT announce + find (跨节点发现)
- ✅ P2P exchange (文件传输)
- ✅ Dual announce/find (双网)
- ✅ 单二进制编译 (51MB)
- ✅ systemd 持久化 relay
- ✅ 全局结构化日志 (internal/log/)
- ✅ 测试网格 (smoke 16/16, functional 39/44)
- ✅ Test peers (ipfs-peer.py, bt-peer.py 在 VPS 运行)
- ✅ BEP 44 (DHT 数据存储) + BEP 51 (infohash 索引)
- ✅ Reg server relay 发现 (register/list/heartbeat)
- ✅ Auth token scheme (endpoint#token)
- ✅ 上传大小限制 (100MB/10MB)
- ✅ URL 注册 + 301 跟随
- ✅ P2P/IPFS/BT React 面板
- ✅ PWA + 移动端
- ✅ BT DHT 协议文档 (bt-dht-protocol.md)
- ✅ dual-stack 协议文档
- ✅ 安全审查 (SECURITY-REVIEW.md)
- ✅ 用户角色模型 (USER-ROLES.md)
- ✅ LLM (siliconflow.moonchan.xyz Qwen/Qwen3-8B)

## 待完成

- [ ] P2P exchange 后自动写入 storage
- [ ] WebRTC 实际端到端传输
- [ ] Docker 5 节点网络实际跑通
- [ ] 混沌网络下测试
- [ ] BT torrent 真实下载
