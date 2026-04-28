# Peerdrive Fixes TODO

> Last: 2026-04-29 · Check before claiming "done"

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
- [x] WebRTC 信令 ✅ 23/23 测试通过 (2026-04-29)
- [x] Docker relay 启动 ✅ P2P 健康
- [ ] Docker 5 节点全通 — relay 通, peer-a/b 待测
- [x] 断点续传 ✅ ResumeManager 端点已接入路由 (2026-04-29)
- [x] BEP 44 PUT/GET ✅ 本地存储回退, 38/38 测试通过 (2026-04-29)
- [x] 三种文件浏览模式 ✅ 时间线/本机目录/数据目录 (2026-04-29)
- [x] 探索合集 ✅ 修复 4 个 bug (2026-04-29)
- [x] BT 端到端下载验证 ✅ 1MB/16片, SHA256 匹配 (2026-04-29)

## 🟢 2026-04-29 修复 (本轮)

- [x] WebDAV URL: `window.location.origin` → `api.getApiBase()` ✅ Settings.jsx
- [x] "Board 666" 无效链接已删除 ✅ Settings.jsx
- [x] Plaza 广播按钮移除（广播应在合集内操作）✅ Plaza.jsx
- [x] "🌐 P2P 打开" → "📡 广播" 文案修正 ✅ AnonExplorer.jsx
- [x] alert() 弹窗 → 内联 toast 消息 ✅ AnonExplorer.jsx（3处）
- [x] BT 测试重跑确认: BEP44 PUT/GET 往返通过, 38/38 PASS ✅
- [x] 前端编译零错误 (45 modules) ✅
- [x] Swagger 文档重新生成: 24→96 路径, 105 端点全覆盖 ✅ (2026-04-29)
- [x] API-REFERENCE.md 补全 resume/multipeer 6 个端点 ✅

## 🔵 已验证

- [x] 全部 API 端点 200 (11 GET + 3 POST + 1 DELETE)
- [x] Go build + test 通过
- [x] WSL 节点 :3000 在线
- [x] VPS reg server :4000 在线
- [x] CF Tunnel wsl-3000 在线
- [x] 合集名 "未命名" bug 修复
- [x] announce 500 改为 200+WARN

## P2P Module Status (2026-04-28)
- ✅ libp2p host (TCP/QUIC/WS)
- ✅ DHT + mDNS
- ✅ Exchange protocol
- ✅ Relay server/client
- ✅ Connection manager (heartbeat/reconnect)
- ✅ Topology + quality metrics
- ✅ P2P topology visualization frontend
- Test results: 35/35 pass

## Auth Module Status (2026-04-28)
- ✅ User register/login
- ✅ JWT token validation
- ✅ Relay registration/heartbeat/list
- ✅ Group management
- ✅ Comment system (per collection)
- ✅ Registration server stats
- ✅ Auth middleware (Bearer token)
- ✅ Navbar identity indicator
- Test results: 20/20 pass

## Storage Module Status (2026-04-28)
- ✅ SHA256 content-addressed storage
- ✅ CID dual-indexing
- ✅ File upload/register/delete/verify
- ✅ Range (HTTP 206) download
- ✅ URL file registration
- ✅ WebDAV mount
- ✅ File copy
- ✅ Universal downloader (local->ipfs->btdht->http)
- Test results: 28/28 pass
