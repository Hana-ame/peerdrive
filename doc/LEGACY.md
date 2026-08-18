# Peerdrive 旧代码清单（Legacy Inventory）

> 2026-08-13 · 以「PeerJS 公共云信令 + WebRTC DataChannel」为新互联层后，原 libp2p / BT DHT / 自建信令栈整体降级为旧代码。
> 状态标记：**可删**（无引用/无调用）| **待迁移**（被新方案取代，功能需搬）| **保留**（仍承担职责）

---

## 一、后端旧代码（back/internal）

### A. libp2p 栈（被 PeerJS 互联取代）— ✅ 已删（2026-08-16，批2）

| 文件/目录 | 职责 | 状态 | 说明 |
|---|---|---|---|
| `service/p2p.go` | libp2p 节点、DHT、流协议 | ✅ 已删 | 原 P2P 核心；其中的「端口转发」逻辑重建为 PeerJS 版（见 F） |
| `service/p2p_transfer.go` | 文件分块传输（chunk 协议） | ✅ 已删 | 帧协议被 `peerjs_service.go` 取代 |
| `service/p2p_resume.go` | 断点续传 | ✅ 已删 | 未迁移到 PeerJS |
| `service/p2p_multipeer.go` | 多 peer 并行下载 | ✅ 已删 | 同上 |
| `service/p2p_dual.go` | 双 DHT（IPFS+BT）编排 | ✅ 已删 | 双 DHT 发现被 MQTT/静态配置取代 |
| `service/p2p_ws.go` | WS 传输通道 | ✅ 已删 | CSWSH 漏洞源，PeerJS 无此问题 |
| `service/p2p_helpers.go` | hash/CID 工具 | ✅ 已删 | 少量工具函数并入 `pkg/hashutil` |
| `service/p2p_connection.go` | 连接管理 | ✅ 已删 | |
| `service/signaling.go` | 自建 WS 信令 hub | ✅ 已删 | 被公共云信令取代（`internal/peerjs/`） |
| `service/relay.go` | 中继服务 | ✅ 已删 | PeerJS 走 TURN |
| `service/relay_registry.go` | 中继注册 | ✅ 已删 | |
| `service/node_registrar.go` | 节点注册 | ✅ 已删 | 节点 ID 由 PeerJS 信令承担 |
| `service/peer_scanner.go` / `peer_tracker.go` | 对端扫描 | ✅ 已删 | 被 `PEERDRIVE_PEERJS_PEERS` + 发现端点取代 |
| `controller/p2p.go` | P2P HTTP 控制器 | 保留 | 现承载端口转发 v2 端点（fwd-*，见 REFACTOR §3.9）与 webrtc 信息端点 |

### B. BT 栈（BT DHT 下载/做种）— ✅ 已独立成库（2026-08-16 起）

| 文件/目录 | 职责 | 状态 | 说明 |
|---|---|---|---|
| `p2p_bt/`（bep44、client、dht、bt_bridge 等） | BT DHT + 下载客户端 | ✅ 独立库 | 拆为 `github.com/Hana-ame/go-peerdrive-bt`（back/p2p_bt 即其源码，go.mod replace 引用） |
| `controller/bt.go` | BT HTTP 控制器 | 已不存在 | BT 端点实际在 `controller/p2p.go`（BTDHTStatus/BTAnnounce/BTDownload* 等），前端 BTPanel 仍用 |
| `service/forward.go` | 端口转发 | ✅ 已删（2026-08-16） | libp2p 版删除；重建为 PeerJS DataChannel 版（REFACTOR §3.9，带 HMAC 质询认证+端口白名单） |

### C. IPFS 栈 — ✅ 已删（2026-08-16，批2）
- `ipfs_service.go`（libp2p host+DHT+Bitswap）与 `ipfs_compat.go`（兼容层）已随
  libp2p 互联层一并删除：它们复用 P2PService 的 host/DHT，无法独立存活；
  前端无 IPFS 组件、`PEERDRIVE_IPFS_COMPAT` 默认关闭。
- **保留的 IPFS 相关能力**：HTTP gateway 抓取（`provider.IPFSProvider` +
  `PEERDRIVE_IPFS_GATEWAYS`，ipfsgw fetcher + `GET /ipfs/:cid` 回退）+ pin 管理
  （`POST/DELETE /ipfs/pin/:cid`）+ `GET /ipfs/gateways` 健康检查。
- 配置已删：`PEERDRIVE_IPFS_COMPAT`、`PEERDRIVE_IPFS_BLOCKSTORE`、全部
  `PEERDRIVE_P2P_*`/`PEERDRIVE_RELAY_*`/`PEERDRIVE_MDNS_*`/`PEERDRIVE_NAT_*`、
  `PEERDRIVE_BOOTSTRAP_PEER`、`PEERDRIVE_STATIC_RELAYS`、`PEERDRIVE_P2P_KEY_FILE`。

| 文件/目录 | 职责 | 状态 | 说明 |
|---|---|---|---|
| `service/ipfs_service.go` / `ipfs_compat.go` / `ipfs.go` | Bitswap/网关 | 保留 | 默认关闭，opt-in |
| `service/universal_downloader.go` | 多协议回退下载 | 保留 | 核心下载链路 |
| `service/webdav.go` | WebDAV | ✅ 已删（2026-08-16） | 无认证任意读写删，高危；新架构无位置 |
| `service/sync_service.go` / `controller/sync.go` | 本地同步 | 保留 | |

### D. 已修/清理（本次重构顺带处理）

| 位置 | 内容 | 状态 |
|---|---|---|
| `router.go` 355-358、409-411 | legacy redirect（/anon/*、/actions/*）与真实路由重复注册 → 启动 panic | ✅ 已删 |
| `router.go` | `/collections/:username` 与 `:hash` 通配符冲突 | ✅ 合并为分派器 |
| `controller/auth.go` / `service/auth_service.go` | Auth 子系统（无路由注册的死代码） | 可删 |
| `controller/task.go` `ListTasks` | 恒返回空 | 可删 |
| `service/fork.go` `PullCollection` | 写假完成任务的 no-op | 可删 |

### E. 测试/杂项目录

| 目录 | 说明 | 状态 |
|---|---|---|
| `back/cmd/p2p-test/` | libp2p 测试工具 | 可删 |
| `back/manual-tests/` `test-p2p-colls/` `test/` `testdata/` | 旧测试 | 可删（保留有断言的核心测试） |
| 仓库根 `peerdrive.db` | 提交进 git 的数据库 | 可删（加 .gitignore） |
| `go.mod` 双 SQLite 驱动（mattn + modernc + go-llsqlite） | CGO 符号冲突 | 待清理（`-tags nosqlite` 是妥协） |

---

## 二、前端旧代码（front/src）

### F. 死代码组件（无任何 import）— ✅ 已删（2026-08-16 前端重构批次）

| 文件 | 行数 | 备注 |
|---|---|---|
| `pages/FileManager.jsx` | 1311 | 路由 `/files` 已从 App.jsx 移除 |
| `components/ServiceStatus.jsx` | 699 | |
| `components/P2PStatus.jsx` | 658 | |
| `components/WebRTCTransfer.jsx` | 588 | |
| `components/WebRTCPeer.jsx` | 191 | |
| `components/UserGroupPicker.jsx` | 145 | |
| `components/PathRegistrar.jsx` | 107 | |
| `components/Sha256Manager.jsx` | 79 | |
| `components/VisibilityPicker.jsx` | 31 | |
| `components/MobileNav.jsx` | 15 | 原被 App.jsx import 但恒渲染 null，已随路由清理移除 |
| `pages/AnonCreator/{TimelineView, RegisteredView, SourceFilters, SourceTabs, SplitHandle, Toast, CollectionHeader}.jsx` | ~200 | |
| `components/ActiveConnPanel.jsx`、`PeerDetailPanel.jsx` | ~200 | 仅被 P2PStatus 引用，随之一并删除 |
| `storage/localDB.js` + `syncManager.js` | ~350 | 无任何 import 的孤儿 |

**注意（原清单勘误）**：`pages/AnonCreator/CollBrowserNav.jsx` 曾被列为死代码，实际被活跃的
`CollBrowser.jsx` import，**保留未删**。相关清理同步完成：
- 路由移除 `/files`；Plaza 空状态「浏览文件管理器」按钮随之删除
- api.js 死导出删除：`WS_TRANSFER_URL*` / `getWSTransferURL` / `forkAnonCollection` /
  `commitAnonCollection` / `uploadConsent`（保留 saveConsentLocal）/ `setIPFSEnabled` /
  `setCollectionVisibility` / `getTaskStatus` / `getRegServerStats` / `getServiceStats` /
  `getRegServerUrl` / `p2pFetch` / `p2pSync` / `p2pPush` / `getPeersDetail` / `getPeerDetail` /
  `getP2PStats` / `getConnections` / `btGetStats` / `updateCollectionTags` / `pullUserCollection`
- Settings「网络协议」区的 IPFS 网络 / BT DHT 网络两个假开关（只写死 localStorage，无读取端）已删除
- 测试与删除对象对齐（smoke/components 测试移除死组件用例）

### G. 旧 API 调用（api.js 中指向被删/被替代后端）

| api.js 函数 | 旧后端端点 | 状态 |
|---|---|---|
| `getP2PStatus` / P2P 面板系列 | `/p2p/*` | 待迁移（改查 `/peerjs/node`） |
| `bt*`（BTController/BTPanel 用） | `/bt/*` | 保留至 BT 栈迁移决定 |
| `sync*` | `/local/*` | 保留 |
| `registerLocal/URL/Folder` | `/files/register_*` | 保留（重复注册于 /collections/register-*） |

### H. 前端功能页面 vs 后端旧栈对应

| 页面 | 依赖 | 状态 |
|---|---|---|
| `P2PPanel` / `P2PDashboard` / `P2PTopology` / `DHTExplorer` / `IPFSPanel` / `BTPanel` / `BTController` | libp2p/BT 栈 | 待迁移：改为 PeerJS 节点面板（在线节点/连接/拉取）或删 |
| `AnonExplorer` / `AnonCreator` / `Plaza` / `Explorer` | HTTP 集合 API | ✅ 保留（新架构主链路） |

---

## 三、删除顺序建议（依赖优先）

1. **M1**：前端 F 组死组件（无依赖，-4000 行）+ `webdav.go` + `forward.go`（高危）
   - ✅ 前端 F 组已删（2026-08-16，见 F 节勘误）；`forward.go` ✅ 已删（v2 重建于 transport，§3.9）；`webdav.go` ✅ 已删（2026-08-16，连同 PEERDRIVE_WEBDAV_ENABLE 与 /webdav 路由）
2. **M2**：后端 A 组 libp2p 栈（先确认 `controller/p2p.go` 中哪些端点还有前端调用）
3. **M3**：BT 栈（拆 `p2p_bt/` 为独立库后从主模块移除）
4. **M4**：杂项（cmd/p2p-test、manual-tests、peerdrive.db、auth 死代码）
