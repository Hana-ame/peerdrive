# Peerdrive Fixes TODO

> Last: 2026-04-28 · Check before claiming "done"

## 🔴 P0 — 刚修完

- [x] IPFS 控制面板 → `/p2p/ipfs` ✅
- [x] BT DHT 控制面板 → `/p2p/bt` ✅
- [x] P2P 双栈页面 → `/p2p` ✅
- [x] Navbar P2P 下拉 ✅
- [x] 单文件合集显示文件图标+文件名 ✅
- [x] 分享创建输入框（不再 alert）✅
- [x] 广播按钮 ✅
- [x] IPv6 (P2PListenAddrV6) ✅
- [x] NAT 打洞 (PEERDRIVE_HOLE_PUNCH=true) ✅
- [x] Peer 状态查看 (/p2p/peers/detail) ✅

## 🟡 P1 — LLM 优化
- [x] LLM 随页面切换更新上下文 ✅
- [x] LLM SSE 流式响应 + thinking 显示 ✅

## 🟡 P1 — 遗留

- [x] Range 下载 ✅ HTTP 206, 100 bytes 验证通过
- [ ] WebRTC 实际端到端传输 — 代码有, 未测
- [x] Docker relay 启动 ✅ P2P 健康
- [ ] Docker 5 节点全通 — relay 通, peer-a/b 待测
- [ ] 断点续传 — ResumeManager 代码有, 未端到端验证

## 🔵 已验证

- [x] 全部 API 端点 200 (11 GET + 3 POST + 1 DELETE)
- [x] Go build + test 通过
- [x] WSL 节点 :3000 在线
- [x] VPS reg server :4000 在线
- [x] CF Tunnel wsl-3000 在线
- [x] 合集名 "未命名" bug 修复
- [x] announce 500 改为 200+WARN
