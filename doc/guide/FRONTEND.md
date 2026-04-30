# Peerdrive 前端说明文档

> React 19 SPA — Vite 8 · React Router 7 · Tailwind CSS 3 · Vitest
> 更新: 2026-04-30

---

## 目录

1. [技术栈](#1-技术栈)
2. [项目结构](#2-项目结构)
3. [路由与页面](#3-路由与页面)
4. [组件树](#4-组件树)
5. [状态管理](#5-状态管理)
6. [API 通信层](#6-api-通信层)
7. [样式系统](#7-样式系统)
8. [PWA 支持](#8-pwa-支持)
9. [核心工作流](#9-核心工作流)

---

## 1. 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| 构建工具 | Vite | ^8.0 |
| UI 框架 | React | ^19.2 |
| 路由 | React Router DOM | ^7.14 |
| CSS | Tailwind CSS | ^3.4 |
| 测试框架 | Vitest | ^4.1 |
| 测试环境 | Happy DOM | ^20.9 |
| 组件测试 | Testing Library (React) | ^16.3 |

- **无 TypeScript** — 纯 JavaScript + JSX
- **无全局状态库** — 使用 React Context 管理全局状态
- **ES Module** — `"type": "module"`

---

## 2. 项目结构

```
front/
├── index.html              # HTML 入口 (含 PWA meta 标签)
├── package.json
├── vite.config.ts          # Vite 构建配置
├── vitest.config.ts        # 测试配置
├── tailwind.config.js      # Tailwind 配置
├── postcss.config.js
├── public/
│   ├── favicon.svg
│   ├── manifest.json       # PWA manifest
│   ├── service-worker.js   # Service Worker
│   └── icons/
│       └── icon-192.png
├── src/
│   ├── main.jsx            # React 入口：createRoot + render
│   ├── App.jsx             # 根组件：Context Provider + 路由定义
│   ├── index.css           # Tailwind 指令 + 全局样式
│   ├── api.js              # HTTP 客户端 + localStorage 配置
│   ├── components/         # 可复用组件 (15 个)
│   │   ├── Navbar.jsx          # 顶部导航栏 + 全局搜索 (Ctrl+K)
│   │   ├── MobileNav.jsx       # 移动端底部导航栏
│   │   ├── LLMAssistant.jsx    # LLM 对话助手浮窗
│   │   ├── FileTree.jsx        # 文件树展示组件
│   │   ├── CollectionCard.jsx  # 合集卡片
│   │   ├── CollectionBuilder.jsx # 合集构建器
│   │   ├── Sha256Manager.jsx   # SHA256 寻址管理
│   │   ├── VersionLog.jsx      # 版本历史
│   │   ├── CommentSection.jsx  # 评论区域
│   │   ├── P2PStatus.jsx       # P2P 网络状态
│   │   ├── WebRTCPeer.jsx      # WebRTC 对等连接
│   │   ├── WebRTCTransfer.jsx  # WebRTC 文件传输
│   │   ├── ServiceStatus.jsx   # 服务健康仪表盘
│   │   ├── ActiveConnPanel.jsx # 活跃连接面板
│   │   ├── PeerDetailPanel.jsx # 对等节点详情
│   │   ├── SettingsSection.jsx # 设置区域包装
│   │   ├── VisibilityPicker.jsx # 可见性选择器
│   │   ├── UserGroupPicker.jsx # 用户/组选择器
│   │   ├── PathRegistrar.jsx   # 路径注册器
│   │   └── AnonCollectionManager.jsx # 匿名合集管理器
│   ├── pages/              # 页面级组件 (9 个模块)
│   │   ├── Plaza.jsx           # 合集广场 (首页)
│   │   ├── Explorer.jsx        # 注册用户合集浏览器
│   │   ├── FileManager.jsx     # 文件管理
│   │   ├── Settings.jsx        # 设置页
│   │   ├── P2PPanel.jsx        # P2P 总览面板
│   │   ├── P2PDashboard.jsx    # P2P 仪表盘
│   │   ├── P2PTopology.jsx     # P2P 拓扑图
│   │   ├── DHTExplorer.jsx     # DHT 路由表浏览器
│   │   ├── IPFSPanel.jsx       # IPFS 面板
│   │   ├── BTPanel.jsx         # BT 总览面板
│   │   ├── BTController.jsx    # BT 下载控制器
│   │   ├── AnonCreator/        # 匿名合集创建器 (多个子组件)
│   │   │   ├── index.jsx
│   │   │   ├── LeftPanel.jsx       # 左侧文件源面板
│   │   │   ├── MiddlePanel.jsx     # 中间合集内容面板
│   │   │   ├── RightPanel.jsx      # 右侧编辑/预览面板
│   │   │   ├── EditorPanel.jsx     # 文本编辑器
│   │   │   ├── EditorToolbar.jsx   # 编辑器工具栏
│   │   │   ├── CollBrowser.jsx     # 合集浏览器
│   │   │   ├── CollBrowserNav.jsx  # 合集浏览导航
│   │   │   ├── CollectionHeader.jsx
│   │   │   ├── CollectionRow.jsx
│   │   │   ├── CollFileRow.jsx
│   │   │   ├── FileSourceRow.jsx
│   │   │   ├── SourceFilters.jsx   # 文件源过滤器
│   │   │   ├── SourceTabs.jsx      # 文件源 Tab 切换
│   │   │   ├── SystemBrowse.jsx    # 系统文件浏览
│   │   │   ├── RegisteredView.jsx  # 已注册文件视图
│   │   │   ├── SearchHistory.jsx   # 搜索历史
│   │   │   ├── TimelineView.jsx    # 时间线视图
│   │   │   ├── SplitHandle.jsx     # 面板分割手柄
│   │   │   ├── NamePrompt.jsx      # 命名对话框
│   │   │   ├── Toast.jsx           # 提示通知
│   │   │   ├── constants.js
│   │   │   └── utils.js
│   │   └── AnonExplorer/       # 匿名合集浏览器
│   │       ├── index.jsx
│   │       ├── BreadcrumbNav.jsx
│   │       ├── CollectionHeader.jsx
│   │       ├── EmptyState.jsx
│   │       ├── FileList.jsx
│   │       ├── FileRow.jsx
│   │       ├── SearchBar.jsx
│   │       ├── ImagePreview.jsx
│   │       ├── PdfPreview.jsx
│   │       ├── TextPreview.jsx
│   │       ├── GenericFilePreview.jsx
│   │       ├── SingleFilePreview.jsx
│   │       ├── NestedCollectionLink.jsx
│   │       ├── Toast.jsx
│   │       └── utils.js
│   └── storage/
│       ├── localDB.js         # IndexedDB 本地存储
│       └── syncManager.js     # 本地-远程同步
└── tests/
    ├── setup.js               # vitest 全局 setup (cleanup)
    ├── smoke.test.jsx         # 冒烟测试
    ├── components.test.jsx    # 组件渲染测试
    └── FileTree.test.jsx      # FileTree 单元测试
```

---

## 3. 路由与页面

路由定义在 `src/App.jsx:44-59`，使用 React Router v7 `<Routes>`：

| 路径 | 页面组件 | 说明 |
|------|----------|------|
| `/` | `Plaza` | 合集广场，浏览匿名合集和公开合集 |
| `/files` | `FileManager` | 文件管理，上传/删除/查看已注册文件 |
| `/:username/:collName` | `Explorer` | 注册用户合集详情与操作 |
| `/anon` | `AnonExplorer` | 匿名合集入口 (输入 Hash 浏览) |
| `/anon/create` | `AnonCreator` | 匿名合集创建器 (三栏布局) |
| `/anon/collections/:hash` | `AnonExplorer` | 查看特定匿名合集 |
| `/p2p` | `P2PPanel` | P2P 网络控制面板 |
| `/p2p/dashboard` | `P2PDashboard` | P2P 统计仪表盘 |
| `/p2p/topology` | `P2PTopology` | P2P 网络拓扑可视化 |
| `/p2p/dht` | `DHTExplorer` | DHT 分布式哈希表浏览器 |
| `/ipfs` | `IPFSPanel` | IPFS 网关管理与 Pin 操作 |
| `/bt` | `BTPanel` | BT 协议总览 |
| `/bt/controller` | `BTController` | BT 下载任务管理器 |
| `/settings` | `Settings` | 全局设置页 |

### 布局结构

所有页面共享外层布局 (`App.jsx:41-63`)：

```
┌──────────────────────────────────────┐
│  Navbar (顶部导航，固定)              │
├──────────────────────────────────────┤
│                                      │
│  <Routes> (页面内容，flex-1)         │
│                                      │
├──────────────────────────────────────┤
│  MobileNav (移动端底部导航，md+隐藏)  │
├──────────────────────────────────────┤
│  LLMAssistant (浮窗，右下角)          │
└──────────────────────────────────────┘
```

---

## 4. 组件树

```
App (AppContext.Provider + PageContext.Provider)
├── BrowserRouter
│   ├── Navbar
│   │   ├── Logo (Link → /)
│   │   ├── 用户名输入
│   │   ├── SearchPanel (Ctrl+K 全局搜索)
│   │   └── 在线状态指示
│   ├── Routes (页面内容区)
│   │   ├── Plaza
│   │   │   ├── 搜索栏 (Hash 直接跳转)
│   │   │   ├── Tab 切换 (本地/P2P公开合集)
│   │   │   ├── CollectionCard[] (合集卡片列表)
│   │   │   └── P2PStatus
│   │   ├── Explorer
│   │   │   ├── CollectionBuilder
│   │   │   │   ├── FileTree
│   │   │   │   └── Sha256Manager
│   │   │   ├── VersionLog
│   │   │   ├── CommentSection
│   │   │   ├── VisibilityPicker
│   │   │   └── UserGroupPicker
│   │   ├── AnonCreator (三栏布局)
│   │   │   ├── LeftPanel
│   │   │   │   ├── SourceTabs
│   │   │   │   ├── SourceFilters
│   │   │   │   ├── SearchHistory
│   │   │   │   ├── SystemBrowse
│   │   │   │   ├── RegisteredView
│   │   │   │   └── FileSourceRow[]
│   │   │   ├── MiddlePanel
│   │   │   │   ├── CollectionHeader
│   │   │   │   ├── CollBrowser
│   │   │   │   ├── CollectionRow[]
│   │   │   │   └── FileTree
│   │   │   └── RightPanel
│   │   │       ├── EditorToolbar
│   │   │       └── EditorPanel
│   │   ├── AnonExplorer
│   │   │   ├── SearchBar
│   │   │   ├── BreadcrumbNav
│   │   │   ├── FileList → FileRow[]
│   │   │   ├── ImagePreview / PdfPreview / TextPreview / GenericFilePreview
│   │   │   └── NestedCollectionLink
│   │   ├── FileManager
│   │   ├── Settings
│   │   │   ├── SettingsSection[] (API端点/LLM/P2P网络/TURN/)
│   │   │   └── ServiceStatus
│   │   ├── P2PPanel / P2PDashboard / P2PTopology
│   │   │   ├── ActiveConnPanel
│   │   │   ├── PeerDetailPanel
│   │   │   ├── WebRTCPeer
│   │   │   └── WebRTCTransfer
│   │   ├── IPFSPanel
│   │   ├── BTPanel / BTController
│   │   └── DHTExplorer
│   ├── MobileNav (移动端)
│   └── LLMAssistant (浮窗)
```

---

## 5. 状态管理

使用 React Context（非 Redux/Zustand），两个 Context：

### AppContext (`App.jsx:22`)

| 字段 | 类型 | 说明 |
|------|------|------|
| `username` | `string` | 当前用户名，持久化到 `localStorage.peerdrive_username` |
| `setUsername` | `(v) => void` | 设置用户名并持久化 |
| `nodeInfo` | `object\|null` | P2P 节点信息 |
| `setNodeInfo` | `(v) => void` | 更新节点信息 |

### PageContext (`App.jsx:23`)

| 字段 | 类型 | 说明 |
|------|------|------|
| `pageContext` | `any` | 当前页面的上下文数据 |
| `setPageContext` | `(v) => void` | 更新页面上下文 |

### 本地持久化 (localStorage)

配置项全部通过 `localStorage` 管理，key 常量定义在 `api.js`：

| Key | 用途 |
|-----|------|
| `peerdrive_api_base` | 后端 API 地址 |
| `peerdrive_auth_token` | JWT 认证令牌 |
| `peerdrive_auth_key` | 备用认证 Key |
| `peerdrive_auth_header_enabled` | 是否启用认证头 |
| `peerdrive_username` | 当前用户名 |
| `peerdrive_llm_endpoint` | LLM API 端点 |
| `peerdrive_llm_model` | LLM 模型名称 |
| `peerdrive_llm_apikey` | LLM API Key |
| `peerdrive_llm_body_template` | LLM 请求体模板 (JSON) |
| `peerdrive_data_consent` | 数据收集同意 |
| `peerdrive_follow_redirects` | 是否跟随重定向 |
| `peerdrive_bootstrap_peer` | 自定义 Bootstrap 节点 |
| `peerdrive_relay_server` | 自定义中继服务器 |
| `peerdrive_stun_url` | STUN 服务器地址 |
| `peerdrive_turn_url` | TURN 服务器地址 |
| `peerdrive_turn_credential` | TURN 凭证 |
| `peerdrive_ipfs_enabled` | IPFS 开关 |
| `peerdrive_reg_server_url` | 注册服务器 URL |

---

## 6. API 通信层

`src/api.js` 是前端与后端通信的唯一入口。

### 核心机制

```
request(method, path, body) → fetch → res.json()
     │
     ├── 自动拼接 API Base URL (localStorage 或默认值)
     ├── 自动注入 Authorization: Bearer <token>
     └── 非 2xx 响应抛出 Error
```

### API Base URL 默认值

```
https://wsl-3000.moonchan.xyz
```

可通过 URL fragment 传递 token：
```
http://host:3000#token123  → base = http://host:3000, token = token123
```

### API 分类

| 模块 | 函数前缀 | 端点示例 | 说明 |
|------|---------|---------|------|
| 文件操作 | `verifyFile`, `uploadFile`, `deleteFile`, `browseDir` | `/files/...` | 文件上传/验证/删除/浏览 |
| 文件注册 | `registerLocalFile`, `registerURL`, `registerFolder` | `/files/register_*` | 注册本地/URL/文件夹 |
| 匿名合集 | `createAnonCollection`, `getAnonCollection`, `forkAnonCollection`, `commitAnonCollection` | `/anon/collections/...` | 匿名合集 CRUD |
| 用户合集 | `createUserCollection`, `getUserCollections`, `commitCollection`, `rollbackVersion` | `/collections/...` | 注册用户合集管理 |
| 合集操作 | `forkUserCollection`, `mergeUserCollection`, `pullUserCollection` | `/actions/...` | Fork/Merge/Pull |
| P2P 状态 | `getP2PStatus`, `getP2PNode`, `getP2PPeers`, `getP2PDiscovered` | `/p2p/...` | P2P 网络状态查询 |
| P2P 操作 | `pingPeer`, `connectPeer`, `p2pAnnounce`, `p2pFetch`, `p2pSync`, `p2pPush` | `/p2p/...` | P2P 交互操作 |
| BT | `getBTStatus`, `btAnnounce`, `btMagnetResolve`, `btTorrentUpload` | `/bt/...` | BitTorrent 操作 |
| BT 下载 | `btGetDownloads`, `btPauseDownload`, `btResumeDownload`, `btSeedDownload` | `/bt/download/...` | BT 下载管理 |
| IPFS | `getIPFSCompatStatus`, `setIPFSCompatEnabled`, `pinCID`, `unpinCID` | `/ipfs/...` | IPFS 兼容层 |
| 搜索 | `searchCollections`, `listPublicCollections` | `/collections/search` | 合集搜索 |
| 访问控制 | `createAccessList`, `getAccessList` | `/access/...` | 权限管理 |
| 服务状态 | `getServiceStats` | 多端点聚合 | 服务健康检查 |

### 下载 URL 构造

文件下载不走 `request()`，直接拼接 URL：

```js
// SHA256 文件
getDownloadUrl(hash) → `${API_BASE}/sha256sum/${hash}`

// 匿名合集文件
getAnonFileDownloadUrl(hash, p) → `${API_BASE}/anon/collections/${hash}/${p}`

// 用户合集文件
getUserFileDownloadUrl(username, coll, filepath) → `${API_BASE}/${username}/${coll}/${filepath}`
```

---

## 7. 样式系统

### Tailwind CSS 3

- 暗色主题 (`bg-gray-950 text-gray-200`)
- 响应式断点：`md:` (768px) 隐藏移动端导航
- 移动端底部导航栏高度补偿：`pb-[72px] md:pb-0`
- 自定义动画：`animate-pulse` (骨架屏)

### 配置

```js
// tailwind.config.js
content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"]
```

### 全局样式

`src/index.css` 引入 Tailwind 指令：
```css
@tailwind base;
@tailwind components;
@tailwind utilities;
```

---

## 8. PWA 支持

`index.html` 包含完整的 PWA 配置：

| 特性 | 实现 |
|------|------|
| Service Worker | `/service-worker.js` (load 时注册) |
| Manifest | `/manifest.json` |
| 主题色 | `#0f172a` (slate-900) |
| Apple 兼容 | `apple-mobile-web-app-capable`, `apple-touch-icon` |
| Viewport | `viewport-fit=cover, user-scalable=no` |

```html
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover, user-scalable=no">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<link rel="manifest" href="/manifest.json">
```

---

## 9. 核心工作流

### 9.1 创建匿名合集

```
AnonCreator 页面 (三栏布局)
  ├── 左栏: 选择文件源
  │   ├── Tab: 所有文件 / 已注册 / 本地电脑
  │   ├── 搜索过滤
  │   ├── 浏览系统目录
  │   └── 勾选文件 → 添加到中栏
  ├── 中栏: 合集内容
  │   ├── 文件列表 (FileTree)
  │   ├── 调整路径/顺序
  │   └── 输入 friendly_name 和 tags
  └── 右栏: 预览/编辑
      ├── 文本编辑器 (EditorPanel)
      └── 图片/PDF 预览
```

保存时调用 `createAnonCollection(entries, name, tags, visibility, accessListHash)`。

### 9.2 浏览匿名合集

```
AnonExplorer 页面
  1. 输入合集 Hash (或从 URL :hash 参数)
  2. GET /anon/collections/:hash 获取合集内容
  3. FileList 展示文件
  4. 点击文件 → 右侧预览面板 (Image/Pdf/Text/Generic)
  5. 嵌套合集展示为 NestedCollectionLink
```

### 9.3 P2P 文件传输

```
下载流程 (客户端自动):
  本地存储 → 远程副本 → P2P (Bitswap)

P2P 面板操作:
  - 查看节点信息 (getP2PNode)
  - 查看对等节点列表 (getP2PPeers)
  - 手动连接节点 (connectPeer)
  - 宣布哈希 (p2pAnnounce)
  - 从对等节点获取文件 (p2pFetch)
  - 合集同步/推送 (p2pSync / p2pPush)
  - WebRTC 直连传输 (WebRTCTransfer)
```

### 9.4 LLM 助手

- 位于右下角浮窗 (`LLMAssistant`)
- 通过 OpenAI 兼容 API 调用 LLM
- 可配置端点、模型、API Key、请求体模板
- 支持硅基流动 (SiliconFlow) 免费模型列表
- 流式输出 (`stream: true`)

### 9.5 全局搜索 (Ctrl+K)

Navbar 内置 SearchPanel：
- 同时搜索匿名合集、公开合集、已注册文件
- 键盘导航 (↑↓ Enter Esc)
- 搜索结果点击直接跳转

---

## 本地开发

```bash
cd front
npm install
npm run dev       # 启动 Vite 开发服务器 (默认 :5173)
npm run build     # 生产构建 → dist/
npm run preview   # 预览生产构建
npm test          # 运行 vitest 测试
```
