# Peerdrive 全部需求总表

> 最后更新: 2026-04-28 · 标记: ✅ 已完成 🚧 进行中 📋 待开发

---

## 1. 文件系统

| # | 需求 | 状态 |
|---|------|------|
| 1.1 | SHA256 内容寻址存储 | ✅ |
| 1.2 | 文件上传 (multipart) | ✅ |
| 1.3 | 本地文件注册 | ✅ |
| 1.4 | 文件夹递归注册 | ✅ |
| 1.5 | URL 文件注册 | ✅ |
| 1.6 | SHA256 下载 | ✅ |
| 1.7 | Range 分块下载 | ✅ |
| 1.8 | CID 双索引 (/ipfs/:cid) | ✅ |
| 1.9 | 文件验证 (exists/consistent) | ✅ |
| 1.10 | 文件删除 | ✅ |
| 1.11 | 文件浏览 (BrowseDir) | ✅ |
| 1.12 | 上传大小限制 (100MB认证/10MB匿名) | ✅ |
| 1.13 | IPFS 公网网关拉取 (ipfs.io/cloudflare-ipfs) | ✅ |
| 1.14 | filesize metadata 记录 | ✅ |

## 2. 合集系统

| # | 需求 | 状态 |
|---|------|------|
| 2.1 | 匿名合集创建 (不可变) | ✅ |
| 2.2 | 合集查看 (SHA256/URL) | ✅ |
| 2.3 | 合集列表 | ✅ |
| 2.4 | 单文件预览 (图片/PDF/文本) | ✅ |
| 2.5 | 单文件合集显示文件图标+文件名 (非📦) | ✅ |
| 2.6 | 空合集自动删除 | ✅ |
| 2.7 | 嵌套合集链接 | ✅ |
| 2.8 | 版本管理 (Commit/Log/Rollback) | ✅ |
| 2.9 | Fork | ✅ |
| 2.10 | Merge 合并 | ✅ |
| 2.11 | 分享链接 (token) | ✅ |
| 2.12 | 合集命名优先级 (collection_name→friendly_name→name_preview→hash) | ✅ |
| 2.13 | "广播"=创建单文件合集+双网宣告 | ✅ |
| 2.14 | 合集名称 AI 推荐 (LLM) | ✅ |
| 2.15 | 分享创建输入框 (非 alert) | ✅ |
| 2.16 | 本地/P2P 合集分 tab | ✅ |

## 3. P2P 网络

| # | 需求 | 状态 |
|---|------|------|
| 3.1 | libp2p host (TCP/QUIC/WebSocket) | ✅ |
| 3.2 | mDNS 局域网发现 | ✅ |
| 3.3 | DHT (IPFS Kademlia) | ✅ |
| 3.4 | Ping 延迟测量 | ✅ |
| 3.5 | Exchange 协议 (/peerdrive/exchange/1.0.0) | ✅ |
| 3.6 | Relay 中继 (server/client) | ✅ |
| 3.7 | NAT 打洞 (Hole Punch) | ✅ |
| 3.8 | AutoNAT | ✅ |
| 3.9 | IPv6 支持 | ✅ |
| 3.10 | 连接管理器 (心跳/重连/统计) | ✅ |
| 3.11 | Peer 详情追踪 (first_seen/last_seen/bytes/transports) | ✅ |
| 3.12 | Peer Scanner (DHT/Reg/LAN/Bootstrap) | ✅ |
| 3.13 | WebRTC 信令 (房间模式) | ✅ |
| 3.14 | P2P 端口转发 (同key, 实验性) | ✅ |
| 3.15 | 分片传输 (256KB chunk/8并发) | ✅ |
| 3.16 | 断点续传 (ResumeManager) | ✅ |
| 3.17 | 多Peer并行下载 | ✅ |
| 3.18 | 公网 relay 节点 (VPS systemd) | ✅ |
| 3.19 | IPFS 兼容模式 (Bitswap blockstore) | ✅ |
| 3.20 | IPFS 网关拉取 | ✅ |
| 3.21 | 中继模式解释 (client/server) | ✅ |
| 3.22 | Peer 协议版本显示 | ✅ |

## 4. BitTorrent

| # | 需求 | 状态 |
|---|------|------|
| 4.1 | BT DHT (Mainline, UDP) | ✅ |
| 4.2 | BT announce (全局 DHT) | ✅ |
| 4.3 | BT find (跨节点验证) | ✅ |
| 4.4 | .torrent 文件解析 | ✅ |
| 4.5 | Magnet 链接解析 | ✅ |
| 4.6 | Wire protocol (handshake/piece exchange) | ✅ |
| 4.7 | HTTP Tracker 支持 | ✅ |
| 4.8 | BT 下载器面板 (前端) | ✅ |
| 4.9 | Pause/Resume/Remove | ✅ |
| 4.10 | 全局 DHT 连接验证 (127+ nodes) | ✅ |
| 4.11 | BEP 44 (DHT数据存储) | ✅ |
| 4.12 | BEP 51 (Infohash索引) | ✅ |
| 4.13 | BT 错误提示 (hover tooltip) | ✅ |
| 4.14 | 完整 BT 客户端功能 | ✅ |

## 5. IPFS 互操作

| # | 需求 | 状态 |
|---|------|------|
| 5.1 | CID 计算 (SHA256→CIDv1) | ✅ |
| 5.2 | /ipfs/:cid 下载端点 | ✅ |
| 5.3 | IPFS 网关拉取 (3个公网网关) | ✅ |
| 5.4 | IPFS 兼容模式 (Bitswap) | ✅ |
| 5.5 | IPFS toggle (Settings开关) | ✅ |
| 5.6 | 真实 IPFS peer (kubo) 连接测试 | ✅ |
| 5.7 | Bitswap 互通 | ✅ |

## 6. 注册与认证

| # | 需求 | 状态 |
|---|------|------|
| 6.1 | Registration Server (JWT) | ✅ |
| 6.2 | 用户注册/登录 | ✅ |
| 6.3 | Token 认证 (endpoint#token) | ✅ |
| 6.4 | Auth middleware | ✅ |
| 6.5 | Relay 注册/发现/心跳 | ✅ |
| 6.6 | 用户群组查询 | 📋 |
| 6.7 | 根据用户信息判断是否提供 relay/p2p 服务 | 📋 |
| 6.8 | Node 运行者账户信息查询 | 📋 |
| 6.9 | 注册用户 DB 存储空间 | 📋 |

## 7. 前端

| # | 需求 | 状态 |
|---|------|------|
| 7.1 | Plaza 合集广场 (本机/P2P tab) | ✅ |
| 7.2 | AnonCreator (4-tab: 时间线/已注册/本机/合集) | ✅ |
| 7.3 | AnonExplorer (单文件预览) | ✅ |
| 7.4 | FileManager (复选框/多选/分享) | ✅ |
| 7.5 | Explorer (用户合集) | ✅ |
| 7.6 | P2PDashboard (网络仪表板) | ✅ |
| 7.7 | IPFS 面板 (/p2p/ipfs) | ✅ |
| 7.8 | BT DHT 面板 (/p2p/bt) | ✅ |
| 7.9 | P2P 双栈面板 (/p2p) | ✅ |
| 7.10 | BT 下载器 (/p2p/bt/controller) | ✅ |
| 7.11 | Settings (LLM/P2P/IPFS/BT/WebDAV配置) | ✅ |
| 7.12 | PWA (manifest/service-worker/移动端导航) | ✅ |
| 7.13 | LLM 助手 (function calling) | ✅ |
| 7.14 | LLM 页面上下文更新 | ✅ |
| 7.15 | LLM SSE流式+thinking显示 | ✅ |
| 7.16 | 拖拽文件到合集编辑器 | ✅ |
| 7.17 | 文件目录树 (VSCode式) | ✅ |
| 7.18 | Windows-like 文件操作 | ✅ |
| 7.19 | 合集统一界面 (AnonExplorer) | ✅ |

## 8. 存储与同步

| # | 需求 | 状态 |
|---|------|------|
| 8.1 | 匿名用户 localStorage | ✅ |
| 8.2 | 三层互备 (localStorage↔Reg Server↔Node) | 🚧 |
| 8.3 | 注册用户 DB 空间 | 📋 |
| 8.4 | WebDAV 挂载 | ✅ |
| 8.5 | 本地存储进度指示 (loadingProgress) | ✅ |

## 9. 测试

| # | 需求 | 状态 |
|---|------|------|
| 9.1 | Go 单元测试 (6 packages) | ✅ |
| 9.2 | BT 集成测试 (7 tests) | ✅ |
| 9.3 | Playwright Smoke (16 tests) | ✅ |
| 9.4 | Playwright Functional (39/44) | 🚧 |
| 9.5 | 跨机器 P2P (Docker↔WSL) | ✅ |
| 9.6 | Docker 5节点组网 | 🚧 |
| 9.7 | 混沌网络测试 (chaos-net.sh) | ✅ |
| 9.8 | Test peers (ipfs-peer.py + bt-peer.py) | ✅ |
| 9.9 | CI (GitHub Actions) | ✅ |
| 9.10 | Test grid (5W1H HTML) | ✅ |

## 10. 部署

| # | 需求 | 状态 |
|---|------|------|
| 10.1 | 单二进制 (51MB, Linux/amd64) | ✅ |
| 10.2 | GitHub Actions cross-compile (win/mac/linux) | ✅ |
| 10.3 | VPS relay node (systemd) | ✅ |
| 10.4 | CF Tunnel (wsl-3000.moonchan.xyz) | ✅ |
| 10.5 | CF Pages (peerdrive.pages.dev) | ✅ |
| 10.6 | Docker compose (5节点) | 🚧 |

## 11. 安全

| # | 需求 | 状态 |
|---|------|------|
| 11.1 | 上传大小限制 | ✅ |
| 11.2 | Auth middleware | ✅ |
| 11.3 | 路径穿越防护 | ✅ |
| 11.4 | CORS 配置 | ✅ |
| 11.5 | 安全审查 (SECURITY-REVIEW.md) | ✅ |

## 🔴 待完成优先级

| 优先级 | 需求 |
|--------|------|
| P0 | 注册用户群组查询 |
| P0 | Node 运行者账户查询 |
| P0 | Relay/P2P 服务根据用户信息判断 |
| P0 | WebRTC 端到端传输 |
| P1 | 三层存储互备同步 |
| P1 | Windows-like 文件操作 |
| P1 | IPFS kub 真实 peer 连接 + Bitswap 互通 |
| P1 | Docker 5节点全通 |
| P2 | Playwright Functional (39/44) |

## 12. 留言板 & 统计

| # | 需求 | 状态 |
|---|------|------|
| 12.1 | 合集留言板 (评论系统) | ✅ |
| 12.2 | 留言存储在注册服务器 | ✅ |
| 12.3 | GET /comments/:hash (读留言) | ✅ |
| 12.4 | POST /comments/:hash (发留言，需认证) | ✅ |
| 12.5 | 匿名可读，认证可发 | ✅ |
| 12.6 | 节点统计信息 (运行时间/文件数/传输量) | ✅ |
| 12.7 | P2P 网络统计面板 | ✅ |
| 12.8 | 用户群组查询 | 📋 |
| 12.9 | Relay/P2P 服务根据用户信息判断 | 📋 |
| 12.10 | Node 运行者账户信息查询接口 | 📋 |

## 13. DHT 哈希表查询服务 (2026-04-28)

| # | 需求 | 状态 |
|---|------|------|
| 13.1 | 统一 DHT 查询面板 (webapp) — 输入 hash, 同时查 IPFS+BT | ✅ |
| 13.2 | IPFS DHT 查询: /p2p/announce, /p2p/dual/find | ✅ |
| 13.3 | BT DHT 查询: /p2p/bt/announce, /p2p/bt/find, /p2p/bt/bep51/sample | ✅ |
| 13.4 | 双栈查询: /p2p/dual/announce, /p2p/dual/find | ✅ |
| 13.5 | 前端 DHT Explorer 页面 — 输入 hash, 显示两边结果 | ✅ |
| 13.6 | BEP 51 infohash 采样 — 发现 BT 网络上的内容 | ✅ |
| 13.7 | IPFS provider 发现 — 查找谁有某个 CID | ✅ |
| 13.8 | 用户可手动触发 DHT crawl/scan | ✅ |

| 2.17 | 私人合集 (visibility=private) | ✅ |
| 2.18 | 非公开合集 (visibility=unlisted) | ✅ |
| 2.19 | 公开合集 (visibility=public) | ✅ |

