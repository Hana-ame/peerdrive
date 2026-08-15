# Peerdrive 旧代码清单（Legacy Inventory）

> 2026-08-13 · 以「PeerJS 公共云信令 + WebRTC DataChannel」为新互联层后，原 libp2p / BT DHT / 自建信令栈整体降级为旧代码。
> 状态标记：**可删**（无引用/无调用）| **待迁移**（被新方案取代，功能需搬）| **保留**（仍承担职责）

---

## 一、后端旧代码（back/internal）

### A. libp2p 栈（被 PeerJS 互联取代）— 整体 **待迁移**

| 文件/目录 | 职责 | 状态 | 说明 |
|---|---|---|---|
| `service/p2p.go` | libp2p 节点、DHT、流协议 | 待迁移 | 原 P2P 核心；PeerJS 后不再需要，但 `p2p.go` 内含 PeerJS 未覆盖的「端口转发」逻辑（见 F） |
| `service/p2p_transfer.go` | 文件分块传输（chunk 协议） | 可删 | 帧协议被 `peerjs_service.go` 取代 |
| `service/p2p_resume.go` | 断点续传 | 可删 | 未迁移到 PeerJS |
| `service/p2p_multipeer.go` | 多 peer 并行下载 | 可删 | 同上 |
| `service/p2p_dual.go` | 双 DHT（IPFS+BT）编排 | 可删 | 双 DHT 发现被 MQTT/静态配置取代 |
| `service/p2p_ws.go` | WS 传输通道 | 可删 | CSWSH 漏洞源，PeerJS 无此问题 |
| `service/p2p_helpers.go` | hash/CID 工具 | 待迁移 | 少量工具函数可并入 `pkg/hashutil` |
| `service/p2p_connection.go` | 连接管理 | 可删 | |
| `service/signaling.go` | 自建 WS 信令 hub | 可删 | 被公共云信令取代（`internal/peerjs/`） |
| `service/relay.go` | 中继服务 | 可删 | PeerJS 走 TURN |
| `service/relay_registry.go` | 中继注册 | 可删 | |
| `service/node_registrar.go` | 节点注册 | 可删 | 节点 ID 由 PeerJS 信令承担 |
| `service/peer_scanner.go` / `peer_tracker.go` | 对端扫描 | 可删 | 被 `PEERDRIVE_PEERJS_PEERS` + 发现端点取代 |
| `controller/p2p.go` | P2P HTTP 控制器 | 待迁移 | 大部分端点可删；`p2p_download` 相关保留至迁移完成 |

### B. BT 栈（BT DHT 下载/做种）— **待迁移**（README 定位为可独立成库）

| 文件/目录 | 职责 | 状态 | 说明 |
|---|---|---|---|
| `p2p_bt/`（bep44、client、dht、bt_bridge 等） | BT DHT + 下载客户端 | 待迁移 | 有价值（纯 Go BEP44），建议拆独立库而非删除 |
| `controller/bt.go` | BT HTTP 控制器 | 待迁移 | 前端 BTPanel 仍用 |
| `service/forward.go` | 端口转发 | 待迁移 | 高危（匿名转发本地端口），建议直接删或加认证 |

### C. IPFS 栈 — **保留**（独立功能，与互联层无关）

| 文件/目录 | 职责 | 状态 | 说明 |
|---|---|---|---|
| `service/ipfs_service.go` / `ipfs_compat.go` / `ipfs.go` | Bitswap/网关 | 保留 | 默认关闭，opt-in |
| `service/universal_downloader.go` | 多协议回退下载 | 保留 | 核心下载链路 |
| `service/webdav.go` | WebDAV | **可删** | 无认证任意读写删，高危；新架构无位置 |
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

### F. 死代码组件（无任何 import）— **可删**，约 4000 行

| 文件 | 行数 | 备注 |
|---|---|---|
| `pages/FileManager.jsx` | 1311 | 路由 `/files` 被重定向，永不可达；内含 rules-of-hooks 违规 + 未定义函数 |
| `components/ServiceStatus.jsx` | 699 | |
| `components/P2PStatus.jsx` | 658 | 无 import 引用 |
| `components/WebRTCTransfer.jsx` | 588 | 旧自建 WebRTC，被 peerjs 方案取代；含 stale closure 等雷 |
| `components/WebRTCPeer.jsx` | 191 | 同上，连接泄漏 |
| `components/UserGroupPicker.jsx` | 145 | |
| `components/PathRegistrar.jsx` | 107 | |
| `components/Sha256Manager.jsx` | 79 | |
| `components/VisibilityPicker.jsx` | 31 | |
| `components/MobileNav.jsx` | 15 | 被 App.jsx import 但恒渲染 null |
| `pages/AnonCreator/{TimelineView, RegisteredView, SourceFilters, SourceTabs, SplitHandle, Toast, CollectionHeader, CollBrowserNav}.jsx` | ~218 | 无 import |

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
2. **M2**：后端 A 组 libp2p 栈（先确认 `controller/p2p.go` 中哪些端点还有前端调用）
3. **M3**：BT 栈（拆 `p2p_bt/` 为独立库后从主模块移除）
4. **M4**：杂项（cmd/p2p-test、manual-tests、peerdrive.db、auth 死代码）
