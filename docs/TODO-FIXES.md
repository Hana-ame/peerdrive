# Peerdrive Fixes TODO

> Last: 2026-04-28 · Check before claiming "done"

## 🔴 P0 — 用户刚指出的

- [ ] IPFS 控制面板页面 → `/p2p/ipfs` (文件不存在)
- [ ] BT DHT 控制面板页面 → `/p2p/bt` (文件不存在)
- [ ] P2P 双栈页面 → `/p2p` (文件不存在)
- [ ] Navbar 里加 P2P 下拉链接
- [ ] 单文件合集在广场显示文件图标+文件名, 不是 📦
- [ ] LLM 随页面切换更新上下文
- [ ] LLM 显示 thinking 过程, SSE 流式响应
- [ ] 分享创建输入框, 不用 browser alert
- [ ] "广播"=创建匿名单文件 collection 并 announce

## 🟡 P1 — 遗留

- [ ] Range 下载 — handler 已加但未实际验证 Range 返回分块
- [ ] WebRTC 实际端到端传输 — 代码有, 未测
- [ ] Docker 5 节点网络 — compose 文件有, 未跑通
- [ ] 断点续传 — ResumeManager 代码有, 未端到端验证

## 🔵 已验证

- [x] 全部 API 端点 200 (11 GET + 3 POST + 1 DELETE)
- [x] Go build + test 通过
- [x] WSL 节点 :3000 在线
- [x] VPS reg server :4000 在线
- [x] CF Tunnel wsl-3000 在线
- [x] 合集名 "未命名" bug 修复
- [x] announce 500 改为 200+WARN
