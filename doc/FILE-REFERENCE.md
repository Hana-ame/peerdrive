# Peerdrive 项目文件参考手册

> 全部文件路径与说明，按模块组织。最后更新: 2026-04-30。

---

## 根目录

| 文件 | 说明 |
|------|------|
| `README.md` | 项目简介 — P2P 内容寻址文件分享系统，含快速开始、端口说明 |
| `.gitignore` | 根级 Git 忽略规则 |
| `opencode.sh` | OpenCode 启动脚本 |
| `test.sh` | 项目级测试入口脚本 |

---

## back/ — Go 后端 (Gin + SQLite + libp2p + BT DHT)

### 入口

| 文件 | 说明 |
|------|------|
| `back/cmd/server/main.go` | 服务主入口 — 启动 Gin HTTP server、初始化 DB、注册路由 |

### 配置

| 文件 | 说明 |
|------|------|
| `back/internal/config/config.go` | 配置结构体与加载逻辑（端口、DB 路径、P2P 密钥等） |
| `back/internal/config/config_test.go` | config 包单元测试 |
| `back/go.mod` | Go 模块定义 (`peerdrive`)，含 libp2p/BT/Gin 依赖 |
| `back/go.sum` | Go 依赖校验和 |
| `back/.gitignore` | 后端 .gitignore |

### Controller — HTTP 处理器

| 文件 | 说明 |
|------|------|
| `back/internal/controller/anon.go` | 匿名合集 CRUD + 匿名文件上传/下载端点 |
| `back/internal/controller/auth.go` | 用户认证端点（注册/登录/token 刷新） |
| `back/internal/controller/collection.go` | 合集管理端点（创建/删除/列表/更新/合并/fork） |
| `back/internal/controller/collection_test.go` | 合集控制器测试 |
| `back/internal/controller/download.go` | 文件下载端点（HTTP 范围请求、断点续传） |
| `back/internal/controller/download_test.go` | 下载控制器测试 |
| `back/internal/controller/file.go` | 文件 CRUD 端点（上传/列表/重命名/移动/删除） |
| `back/internal/controller/file_test.go` | 文件控制器测试 |
| `back/internal/controller/fork.go` | 合集 fork 端点 |
| `back/internal/controller/merge.go` | 合集合并端点（三方合并、冲突解决） |
| `back/internal/controller/p2p.go` | P2P 传输控制端点（发起/取消/状态查询） |
| `back/internal/controller/p2p_download.go` | P2P 下载专用端点 |
| `back/internal/controller/ping.go` | 健康检查端点 (`/ping`) |
| `back/internal/controller/ping_test.go` | ping 控制器测试 |
| `back/internal/controller/share.go` | 分享链接端点（生成/验证/删除） |
| `back/internal/controller/signal.go` | WebRTC 信令端点（SDP/ICE 交换） |
| `back/internal/controller/sync.go` | 设备同步端点 |
| `back/internal/controller/task.go` | 传输任务端点（列表/重试/取消/清除） |
| `back/internal/controller/webrtc.go` | WebRTC 连接控制端点 |

### Service — 业务逻辑层

| 文件 | 说明 |
|------|------|
| `back/internal/service/anon_service.go` | 匿名合集业务逻辑（创建/读写/浏览/有效期管理） |
| `back/internal/service/anon_service_test.go` | 匿名服务测试 |
| `back/internal/service/auth_service.go` | 认证服务 — token 签发/验证/用户管理 |
| `back/internal/service/downloader.go` | HTTP 下载器 — 从外部 URL 拉取文件并入库 |
| `back/internal/service/downloader_test.go` | 下载器测试 |
| `back/internal/service/file_service.go` | 文件服务 — 存储/索引/去重/SHA256 校验 |
| `back/internal/service/file_service_test.go` | 文件服务测试 |
| `back/internal/service/forward.go` | 请求转发 — 中继节点透明转发 |
| `back/internal/service/ipfs_compat.go` | IPFS CID 兼容层 — CID ↔ SHA256 映射 |
| `back/internal/service/node_registrar.go` | 节点注册 — 向 registration-server 注册与发现 |
| `back/internal/service/p2p.go` | P2P 传输核心 — 双栈协调 (libp2p + BT DHT) |
| `back/internal/service/p2p_connection.go` | P2P 连接管理 — 建立/维持/超时/重连 |
| `back/internal/service/p2p_dual.go` | 双栈传输 — libp2p 与 BT 协议并行/切换 |
| `back/internal/service/p2p_helpers.go` | P2P 辅助函数 |
| `back/internal/service/p2p_multipeer.go` | 多 Peer 并发下载 — 分片调度与聚合 |
| `back/internal/service/p2p_resume.go` | 断点续传 — 传输状态持久化、续传逻辑 |
| `back/internal/service/p2p_test.go` | P2P 服务测试 |
| `back/internal/service/p2p_transfer.go` | P2P 实际传输 — 数据块读写、速率控制 |
| `back/internal/service/p2p_ws.go` | P2P WebSocket 通道 — 信令与元数据交换 |
| `back/internal/service/peer_scanner.go` | Peer 扫描器 — 主动发现网络中的对等节点 |
| `back/internal/service/peer_tracker.go` | Peer 追踪器 — 记录与管理已知节点状态 |
| `back/internal/service/relay.go` | 中继服务 — 为 NAT 后节点提供流量中转 |
| `back/internal/service/relay_registry.go` | 中继注册 — 中继节点登记与发现 |
| `back/internal/service/signaling.go` | WebRTC 信令服务 — SDP/ICE 候选交换 |
| `back/internal/service/sync_service.go` | 设备同步服务 — 多设备间合集/文件同步 |
| `back/internal/service/sync_service_test.go` | 同步服务测试 |
| `back/internal/service/universal_downloader.go` | 通用下载器 — 按优先级选择 HTTP/P2P/IPFS/BT 下载策略 |
| `back/internal/service/universal_downloader_test.go` | 通用下载器测试 |
| `back/internal/service/webdav.go` | WebDAV 服务 — 将合集挂载为 WebDAV 驱动器 |

### Repository — 数据持久化 (SQLite)

| 文件 | 说明 |
|------|------|
| `back/internal/repository/db.go` | 数据库初始化 — SQLite 连接、迁移、连接池 |
| `back/internal/repository/anon_repo.go` | 匿名合集数据访问 — 创建/过期清理/读写 |
| `back/internal/repository/collection_repo.go` | 合集数据访问 — CRUD/版本管理/成员查询 |
| `back/internal/repository/collection_repo_test.go` | 合集 repo 测试 |
| `back/internal/repository/file_repo.go` | 文件元数据访问 — SHA256 索引/路径管理 |
| `back/internal/repository/file_repo_test.go` | 文件 repo 测试 |
| `back/internal/repository/pin_repo.go` | 固定(Pin)记录访问 — 防止 GC 清理标记 |
| `back/internal/repository/share_repo.go` | 分享链接记录访问 |
| `back/internal/repository/sync_repo.go` | 同步状态记录访问 |
| `back/internal/repository/task_repo.go` | 传输任务记录访问 — 创建/状态更新/查询 |
| `back/internal/repository/user_repo.go` | 用户记录访问 |

### Provider — 数据源抽象

| 文件 | 说明 |
|------|------|
| `back/internal/provider/provider.go` | ContentProvider 接口定义 — 统一的文件块获取抽象 |
| `back/internal/provider/provider_test.go` | Provider 接口测试 |
| `back/internal/provider/local.go` | 本地文件系统 Provider |
| `back/internal/provider/http.go` | HTTP/HTTPS 远程文件 Provider |
| `back/internal/provider/ipfs.go` | IPFS 网络 Provider（通过 Kubo RPC / HTTP Gateway） |
| `back/internal/provider/ipfs_test.go` | IPFS Provider 测试 |
| `back/internal/provider/manager.go` | Provider 管理器 — 注册/选择/故障转移 |

### p2p_bt — BT DHT 实现

| 文件 | 说明 |
|------|------|
| `back/internal/p2p_bt/client.go` | BT DHT 客户端 — 节点启动、DHT 加入 |
| `back/internal/p2p_bt/torrent.go` | Torrent 管理 — metainfo 解析、piece 校验 |
| `back/internal/p2p_bt/magnet.go` | Magnet 链接解析与处理器 |
| `back/internal/p2p_bt/tracker.go` | Tracker 通信 — announce/scrape |
| `back/internal/p2p_bt/seeder.go` | Seeder — 本地文件做种 |
| `back/internal/p2p_bt/piece.go` | Piece 管理 — 分片下载、bitfield 追踪 |
| `back/internal/p2p_bt/bt_bridge.go` | BT 桥接层 — 连接 Provider 接口与 BT 下载 |
| `back/internal/p2p_bt/bt_dht.go` | 扩展 DHT — PUT/GET 支持 (BEP-44) |
| `back/internal/p2p_bt/bep44.go` | BEP-44 实现 — DHT 可变数据存储 |
| `back/internal/p2p_bt/bep51.go` | BEP-51 实现 — DHT 不可变索引 |
| `back/internal/p2p_bt/log.go` | BT 模块日志 |
| `back/internal/p2p_bt/bt_test.go` | BT 模块测试 |

### Model — 数据模型

| 文件 | 说明 |
|------|------|
| `back/internal/model/anon.go` | 匿名合集模型 — 元数据、过期策略、token 映射 |
| `back/internal/model/anon_test.go` | 匿名模型测试 |
| `back/internal/model/collection.go` | 合集模型 — 版本链、成员关系、权限 |
| `back/internal/model/collection_test.go` | 合集模型测试 |
| `back/internal/model/file.go` | 文件模型 — SHA256 Cid、块信息、状态 |
| `back/internal/model/peer.go` | 对等节点模型 — 地址、协议、能力 |
| `back/internal/model/share.go` | 分享链接模型 — token/权限/过期 |
| `back/internal/model/sync.go` | 同步状态模型 — 设备/操作/时间戳 |
| `back/internal/model/transfer_task.go` | 传输任务模型 — 进度/优先级/重试 |
| `back/internal/model/user.go` | 用户模型 — 身份/角色/密钥 |

### Router — 路由与中间件

| 文件 | 说明 |
|------|------|
| `back/internal/router/router.go` | Gin 路由注册 — 全部端点映射、中间件挂载 |
| `back/internal/router/auth_middleware.go` | 认证中间件 — JWT 验证、角色检查、匿名访问控制 |

### 其他

| 文件 | 说明 |
|------|------|
| `back/internal/log/log.go` | 结构化日志封装 |
| `back/internal/nodestate/nodestate.go` | 节点运行时状态管理（在线/离线/忙碌） |
| `back/pkg/hashutil/hashutil.go` | SHA256 哈希工具函数 |
| `back/docs/docs.go` | Swagger 文档生成代码 |

### Test — 测试脚本

| 文件 | 说明 |
|------|------|
| `back/test/all.sh` | 全部测试入口 |
| `back/test/e2e-all.sh` | E2E 全量测试 |
| `back/test/anonymous_test.sh` | 匿名合集测试 |
| `back/test/auth_test.sh` | 认证流程测试 |
| `back/test/auth-full-test.sh` | 认证全量测试 |
| `back/test/auth-node-test.sh` | 认证节点测试 |
| `back/test/bt-full-test.sh` | BT DHT 全量集成测试 |
| `back/test/bt-integration/create-torrent.sh` | 创建 Torrent 辅助脚本 |
| `back/test/bt-integration/main.go` | BT 集成测试入口程序 |
| `back/test/e2e-all.sh` | E2E 全量测试 |
| `back/test/ipfs-full-test.sh` | IPFS 全量集成测试 |
| `back/test/ipfs-integration/main.go` | IPFS 集成测试入口程序 |
| `back/test/ipfs-peer-test.sh` | IPFS 多 Peer 测试 |
| `back/test/p2p.sh` | P2P 基础测试 |
| `back/test/p2p-full-test.sh` | P2P 全量测试 |
| `back/test/p2p_transfer.sh` | P2P 传输测试 |
| `back/test/peerdrive-functional.mjs` | Peerdrive 功能测试 (Node.js) |
| `back/test/peerdrive-new-features.mjs` | 新功能测试 (Node.js) |
| `back/test/peerdrive-smoke.mjs` | 冒烟测试 (Node.js) |
| `back/test/register.sh` | 注册流程测试 |
| `back/test/reg-server-user-mgmt.sh` | 注册服务器用户管理测试 |
| `back/test/relay.sh` | 中继功能测试 |
| `back/test/storage-full-test.sh` | 存储全量测试 |
| `back/test/upload.sh` | 上传功能测试 |
| `back/test/webrtc_signal_test.sh` | WebRTC 信令测试 |
| `back/test/webrtc-test.sh` | WebRTC 全量测试 |
| `back/test/anon-collection.sh` | 匿名合集测试 |
| `back/test/p2p-full-test.sh` | P2P 全量测试 |
| `back/test/diagnose.py` | 测试诊断 Python 脚本 |
| `back/testdata/test.txt` | 测试用数据文件 |

---

## front/ — React 前端 (Vite + TailwindCSS)

### 入口与配置

| 文件 | 说明 |
|------|------|
| `front/index.html` | HTML 入口 — SPA 挂载点 |
| `front/package.json` | Node 依赖定义 |
| `front/package-lock.json` | 依赖锁文件 |
| `front/vite.config.ts` | Vite 构建配置 |
| `front/vitest.config.ts` | Vitest 测试配置 |
| `front/tailwind.config.js` | TailwindCSS 配置 |
| `front/postcss.config.js` | PostCSS 配置 |
| `front/.gitignore` | 前端 .gitignore |
| `front/README.md` | 前端 README |

### public/ — 静态资源

| 文件 | 说明 |
|------|------|
| `front/public/favicon.svg` | 网站图标 |
| `front/public/icons.svg` | SVG 图标 sprite |
| `front/public/icons/icon-192.png` | PWA 192px 图标 |
| `front/public/icons/icon-512.png` | PWA 512px 图标 |
| `front/public/manifest.json` | PWA manifest |
| `front/public/service-worker.js` | Service Worker (离线缓存) |
| `front/public/_redirects` | Netlify/Caddy 重定向规则 |

### src/ — 源代码

#### 核心框架

| 文件 | 说明 |
|------|------|
| `front/src/main.jsx` | React 应用入口 — 挂载根组件与路由 |
| `front/src/App.jsx` | 根组件 — 全局布局、路由分发、状态管理 |
| `front/src/api.js` | API 客户端 — Axios 封装、token 注入、错误处理 |
| `front/src/index.css` | 全局样式 — Tailwind 指令与自定义 |

#### components/ — 共享组件

| 文件 | 说明 |
|------|------|
| `front/src/components/ActiveConnPanel.jsx` | 活跃连接面板 — 显示当前 P2P 连接列表 |
| `front/src/components/AnonCollectionManager.jsx` | 匿名合集管理器 — 创建与管理匿名合集 |
| `front/src/components/CollectionBuilder.jsx` | 合集构建向导 |
| `front/src/components/CollectionCard.jsx` | 合集卡片 — 封面/标题/摘要展示 |
| `front/src/components/CommentSection.jsx` | 评论区组件 |
| `front/src/components/FileTree.jsx` | 文件树组件 — 树形目录浏览与操作 |
| `front/src/components/LLMAssistant.jsx` | LLM AI 助手面板 |
| `front/src/components/MobileNav.jsx` | 移动端导航栏 |
| `front/src/components/Navbar.jsx` | 桌面端导航栏 |
| `front/src/components/P2PStatus.jsx` | P2P 连接状态指示器 |
| `front/src/components/PathRegistrar.jsx` | 路径注册组件 — 注册到 registration-server |
| `front/src/components/PeerDetailPanel.jsx` | Peer 详情面板 — 节点信息/状态/统计 |
| `front/src/components/ServiceStatus.jsx` | 后端服务状态指示器 (ping-based) |
| `front/src/components/SettingsSection.jsx` | 设置面板组件 |
| `front/src/components/Sha256Manager.jsx` | SHA256 文件管理 — 内容寻址操作 |
| `front/src/components/UserGroupPicker.jsx` | 用户/群组选择器 |
| `front/src/components/VersionLog.jsx` | 版本历史查看组件 (Git 风格) |
| `front/src/components/VisibilityPicker.jsx` | 可见性选择器 (公开/私有/群组) |
| `front/src/components/WebRTCPeer.jsx` | WebRTC Peer 管理组件 |
| `front/src/components/WebRTCTransfer.jsx` | WebRTC 文件传输组件 |

#### pages/AnonCreator/ — 匿名合集创建页

| 文件 | 说明 |
|------|------|
| `front/src/pages/AnonCreator/index.jsx` | AnonCreator 页主入口 — 三列布局容器 |
| `front/src/pages/AnonCreator/constants.js` | 页面常量（分类、标签、默认值） |
| `front/src/pages/AnonCreator/utils.js` | 工具函数（格式化、校验、状态计算） |
| `front/src/pages/AnonCreator/CollBrowser.jsx` | 合集浏览器 — 浏览可引用合集 |
| `front/src/pages/AnonCreator/CollBrowserNav.jsx` | 合集浏览器导航 |
| `front/src/pages/AnonCreator/CollectionHeader.jsx` | 合集头信息展示 |
| `front/src/pages/AnonCreator/CollectionRow.jsx` | 合集列表行 |
| `front/src/pages/AnonCreator/CollFileRow.jsx` | 合集内文件行 |
| `front/src/pages/AnonCreator/EditorPanel.jsx` | 编辑面板 — 合集元数据编辑 |
| `front/src/pages/AnonCreator/EditorToolbar.jsx` | 编辑器工具栏 |
| `front/src/pages/AnonCreator/FileSourceRow.jsx` | 文件来源行 — 来源选择与预览 |
| `front/src/pages/AnonCreator/LeftPanel.jsx` | 左面板 — 数据源筛选 |
| `front/src/pages/AnonCreator/MiddlePanel.jsx` | 中面板 — 文件预览 |
| `front/src/pages/AnonCreator/RightPanel.jsx` | 右面板 — 合集编辑/发布 |
| `front/src/pages/AnonCreator/NamePrompt.jsx` | 命名提示弹框 |
| `front/src/pages/AnonCreator/RegisteredView.jsx` | 已注册合集视图 |
| `front/src/pages/AnonCreator/SearchHistory.jsx` | 搜索历史记录 |
| `front/src/pages/AnonCreator/SourceFilters.jsx` | 数据源筛选器 |
| `front/src/pages/AnonCreator/SourceTabs.jsx` | 数据源标签页 |
| `front/src/pages/AnonCreator/SplitHandle.jsx` | 面板分割手柄（拖拽调整大小） |
| `front/src/pages/AnonCreator/SystemBrowse.jsx` | 系统文件浏览 |
| `front/src/pages/AnonCreator/TimelineView.jsx` | 时间线视图 |
| `front/src/pages/AnonCreator/Toast.jsx` | Toast 通知 |

#### pages/AnonExplorer/ — 匿名合集浏览页

| 文件 | 说明 |
|------|------|
| `front/src/pages/AnonExplorer/index.jsx` | AnonExplorer 页主入口 |
| `front/src/pages/AnonExplorer/utils.js` | 工具函数 |
| `front/src/pages/AnonExplorer/BreadcrumbNav.jsx` | 面包屑导航 |
| `front/src/pages/AnonExplorer/CollectionHeader.jsx` | 合集头信息展示 |
| `front/src/pages/AnonExplorer/EmptyState.jsx` | 空状态占位 |
| `front/src/pages/AnonExplorer/FileList.jsx` | 文件列表 |
| `front/src/pages/AnonExplorer/FileRow.jsx` | 文件列表行 |
| `front/src/pages/AnonExplorer/GenericFilePreview.jsx` | 通用文件预览（二进制/未知格式） |
| `front/src/pages/AnonExplorer/ImagePreview.jsx` | 图片预览组件 |
| `front/src/pages/AnonExplorer/NestedCollectionLink.jsx` | 嵌套合集链接（合集内引用合集） |
| `front/src/pages/AnonExplorer/PdfPreview.jsx` | PDF 预览组件 |
| `front/src/pages/AnonExplorer/SearchBar.jsx` | 搜索栏 |
| `front/src/pages/AnonExplorer/SingleFilePreview.jsx` | 单文件预览 |
| `front/src/pages/AnonExplorer/TextPreview.jsx` | 文本文件预览 |
| `front/src/pages/AnonExplorer/Toast.jsx` | Toast 通知 |

#### pages/ — 其他页面

| 文件 | 说明 |
|------|------|
| `front/src/pages/BTController.jsx` | BT DHT 控制器页面 |
| `front/src/pages/BTPanel.jsx` | BT 面板 — 种子/磁力链接管理 |
| `front/src/pages/DHTExplorer.jsx` | DHT 网络浏览器 |
| `front/src/pages/Explorer.jsx` | 文件管理器 — 全局文件浏览 |
| `front/src/pages/FileManager.jsx` | 文件管理页 — 上传/整理/删除 |
| `front/src/pages/IPFSPanel.jsx` | IPFS 面板 — CID 查询/内容管理 |
| `front/src/pages/P2PDashboard.jsx` | P2P 仪表盘 — 全局状态总览 |
| `front/src/pages/P2PPanel.jsx` | P2P 面板 — 节点/连接/传输控制 |
| `front/src/pages/P2PTopology.jsx` | P2P 拓扑图 — 网络可视化 |
| `front/src/pages/Plaza.jsx` | 合集广场 — 公开合集浏览与发现 |
| `front/src/pages/Settings.jsx` | 设置页面 |

#### storage/ — 本地存储

| 文件 | 说明 |
|------|------|
| `front/src/storage/localDB.js` | 本地数据库 — IndexedDB 封装、前端缓存 |
| `front/src/storage/syncManager.js` | 同步管理器 — 前端 ⇄ 后端数据同步 |

#### tests/ — 前端测试

| 文件 | 说明 |
|------|------|
| `front/tests/setup.js` | 测试环境初始化 (jsdom, mocks) |
| `front/tests/components.test.jsx` | 组件单元测试 |
| `front/tests/FileTree.test.jsx` | FileTree 组件单元测试 |
| `front/tests/smoke.test.jsx` | 前端冒烟测试 |
| `front/tests/playwright-smoke.mjs` | Playwright E2E 冒烟测试 |

#### dist/ — 构建产物

| 文件 | 说明 |
|------|------|
| `front/dist/index.html` | 构建后 HTML 入口 |
| `front/dist/manifest.json` | 构建 manifest |
| `front/dist/favicon.svg` | 构建后 favicon |
| `front/dist/icons.svg` | 构建后 icon sprite |
| `front/dist/service-worker.js` | 构建后 Service Worker |
| `front/dist/assets/index-C5vd8htV.css` | 构建后 CSS bundle |
| `front/dist/assets/index-D4NOIXnB.js` | 构建后 JS bundle |

---

## doc/ — 项目文档

### 入口与仪表盘

| 文件 | 说明 |
|------|------|
| `doc/README.md` | 文档总览 — 架构、分层设计、模块映射 |
| `doc/INDEX.md` | 文档完整索引 — 按分类列出所有文档 |
| `doc/DASHBOARD.md` | 项目仪表盘 — 进度/状态跟踪 |

### spec/ — 技术规范

| 文件 | 说明 |
|------|------|
| `doc/spec/REQUIREMENTS.md` | 全部需求总表 (130+ 项) |
| `doc/spec/API-REFERENCE.md` | 完整 API 参考 (105 端点) |
| `doc/spec/COLLECTION-LOGIC.md` | 合集逻辑完整追踪 |
| `doc/spec/USER-ROLES.md` | 用户角色模型 |
| `doc/spec/CODE-DOC-MAPPING.md` | 代码 ↔ 文档映射表 |
| `doc/spec/BACKEND_TASKS.md` | 后端任务清单 |
| `doc/spec/FRONTEND_TASKS.md` | 前端任务清单 |

#### spec/backend/ — 后端规范

| 文件 | 说明 |
|------|------|
| `doc/spec/backend/BACKEND_DOC.md` | 后端总览文档 |
| `doc/spec/backend/IMPLEMENTATION_SPEC.md` | 实现规范 |
| `doc/spec/backend/design.md` | 后端设计文档 |
| `doc/spec/backend/api-reference.md` | 后端 API 参考 |
| `doc/spec/backend/backend-reference.md` | 后端代码参考 |
| `doc/spec/backend/database.md` | 数据库设计（表结构/索引/迁移） |
| `doc/spec/backend/sha256-download.md` | SHA256 内容寻址下载设计 |
| `doc/spec/backend/upload.md` | 文件上传流程设计 |
| `doc/spec/backend/register.md` | 注册服务器交互设计 |
| `doc/spec/backend/anon-collection.md` | 匿名合集设计 |

#### spec/frontend/ — 前端规范

| 文件 | 说明 |
|------|------|
| `doc/spec/frontend/FRONTEND_DOC.md` | 前端总览文档 |
| `doc/spec/frontend/API_DOC.md` | 前端 API 调用文档 |

### modules/ — 模块设计文档

#### modules/auth/ — 认证模块

| 文件 | 说明 |
|------|------|
| `doc/modules/auth/README.md` | 认证模块总览 |
| `doc/modules/auth/API-DESIGN.md` | 认证 API 设计 |
| `doc/modules/auth/SECURITY-REVIEW.md` | 认证安全审查 |
| `doc/modules/auth/USER-ROLES.md` | 用户角色与权限 |

#### modules/bt/ — BT DHT 模块

| 文件 | 说明 |
|------|------|
| `doc/modules/bt/README.md` | BT 模块总览 |
| `doc/modules/bt/API-DESIGN.md` | BT API 设计 |
| `doc/modules/bt/bt-dht-protocol.md` | BT DHT 协议设计 |
| `doc/modules/bt/TEST-MATRIX.md` | BT 测试矩阵 |

#### modules/ipfs/ — IPFS 模块

| 文件 | 说明 |
|------|------|
| `doc/modules/ipfs/README.md` | IPFS 模块总览 |
| `doc/modules/ipfs/API-DESIGN.md` | IPFS API 设计 |
| `doc/modules/ipfs/ipfs-protocol.md` | IPFS 集成协议设计 |
| `doc/modules/ipfs/webrtc-architecture.md` | WebRTC 架构设计（浏览器 IPFS 直连） |

#### modules/p2p/ — P2P 模块

| 文件 | 说明 |
|------|------|
| `doc/modules/p2p/README.md` | P2P 模块总览 |
| `doc/modules/p2p/API-DESIGN.md` | P2P API 设计 |
| `doc/modules/p2p/p2p.md` | P2P 协议设计 |
| `doc/modules/p2p/dual-stack-protocol.md` | 双栈协议设计 (libp2p + BT DHT) |
| `doc/modules/p2p/grid.md` | P2P 网格拓扑设计 |

#### modules/storage/ — 存储模块

| 文件 | 说明 |
|------|------|
| `doc/modules/storage/README.md` | 存储模块总览 |
| `doc/modules/storage/API-DESIGN.md` | 存储 API 设计 |
| `doc/modules/storage/api-reference.md` | 存储 API 参考 |
| `doc/modules/storage/COLLECTION-LOGIC.md` | 合集逻辑详细设计 |
| `doc/modules/storage/database.md` | 数据库设计 |

### guide/ — 操作指南

| 文件 | 说明 |
|------|------|
| `doc/guide/API-USAGE.md` | API 使用手册 — 调用顺序/目的/条件 |
| `doc/guide/USER_MANUAL.md` | 用户使用手册 |
| `doc/guide/VPS_DEPLOY.md` | VPS 部署指南 |
| `doc/guide/docker.md` | Docker 部署指南 |
| `doc/guide/siliconflow-setup.md` | SiliconFlow LLM API 配置 |
| `doc/guide/操作说明.md` | 中文操作说明 |

### report/ — 项目报告

| 文件 | 说明 |
|------|------|
| `doc/report/REPORT-OVERVIEW.md` | 报告总览 |
| `doc/report/index.md` | 报告索引 |
| `doc/report/DEVELOPMENT_PLAN.md` | 开发计划 |
| `doc/report/ROADMAP.md` | 产品路线图 |
| `doc/report/MILESTONE-P2P.md` | P2P 里程碑 |
| `doc/report/MILESTONE-p2p-vps.md` | P2P VPS 部署里程碑 |
| `doc/report/changelog.md` | 变更日志 |
| `doc/report/refactor-report.md` | 重构报告 |
| `doc/report/SECURITY-REVIEW.md` | 安全审查报告 |
| `doc/report/grid.md` | 网格拓扑报告 |
| `doc/report/TASK-COMPLETION-2026-04-29.md` | 2026-04-29 任务完成报告 |
| `doc/report/测试报告-2026-04-29.md` | 2026-04-29 测试报告 |
| `doc/report/TXT-REPLY.md` | TXT 回复记录 |
| `doc/report/TXT-STATUS.md` | TXT 状态记录 |
| `doc/report/MEMO.md` | 开发备忘录 |
| `doc/report/memo-go.md` | Go 开发备忘录 |
| `doc/report/ISSUES_FOR_GEMINI.md` | 待向 Gemini 反馈的问题 |
| `doc/report/CI-FIXES.md` | CI 修复记录 |
| `doc/report/TODO-FIXES.md` | 待修复问题清单 |
| `doc/report/TODO-P2P-DUAL-STACK.md` | P2P 双栈待办 |

### testing/ — 测试文档

| 文件 | 说明 |
|------|------|
| `doc/testing/index.md` | 测试文档门户 |
| `doc/testing/README.md` | 测试总览 |
| `doc/testing/TESTING-HANDBOOK.md` | 测试手册 |
| `doc/testing/TESTING-METHODOLOGY.md` | 测试方法论 |
| `doc/testing/TEST-MATRIX.md` | 测试矩阵 |
| `doc/testing/TEST-PIPELINE.md` | 测试流水线设计 |
| `doc/testing/CHAOS_TESTING.md` | 混沌测试方案 |
| `doc/testing/测试方案.md` | 中文测试方案 |
| `doc/testing/如何测试.md` | 中文测试指南 |
| `doc/testing/reg-server-test-plan.md` | 注册服务器测试计划 |

### archive/ — 历史归档

| 文件 | 说明 |
|------|------|
| `doc/archive/方案.md` | 历史方案文档 |
| `doc/archive/测试方案.md` | 历史测试方案 |
| `doc/archive/知识库.md` | 历史知识库 |
| `doc/archive/AGENTS.md` | 历史 Agent 配置 |
| `doc/archive/reply.md` | 历史回复记录 |

---

## .github/workflows/ — CI/CD

| 文件 | 说明 |
|------|------|
| `.github/workflows/ci.yml` | 项目 CI 流水线 — 后端测试 + 前端测试 + 构建 |
| `.github/workflows/go-build.yml` | Go 多平台构建矩阵 (linux/macos/windows) |
| `.github/workflows/release.yml` | Release 发布流程 |
| `front/.github/workflows/ci.yml` | 前端 CI 流水线 |

---

## 统计

| 类别 | 数量 |
|------|------|
| Go 源文件 | ~75 |
| 前端源文件 (JSX/TS/JS/CSS) | ~70 |
| 测试脚本 (.sh/.mjs/.py) | ~25 |
| 文档 (.md) | ~65 |
| CI/CD 配置 | 4 |
| **总计** | **~240** |
