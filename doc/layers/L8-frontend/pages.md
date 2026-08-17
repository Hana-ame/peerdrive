# 页面 / 路由 / 导航（front/src/pages + App.jsx + Navbar）

> 层归属：AOP ⑧ 前端切面（doc/LAYERS.md §1）。React 19 + react-router-dom 7 单页
> 应用：App.jsx 定义全部路由与全局状态，pages/ 是业务页面，components/Navbar.jsx
> 与 LLMAssistant.jsx 是跨页全局组件。所有数据经 api.js（→ ws.js）与本地节点通信。

## 职责

- **App.jsx**：路由表、`AppContext`（username/nodeInfo）、`PageContext`
  （页面上下文，供 LLM 助手感知当前页）、`AppErrorBoundary` 全局错误边界。
- **pages/**：12 个页面组件 + 2 个页面级组件树（AnonCreator、AnonExplorer），
  各自聚合业务 UI 与 api.js 调用。
- **components/**：Navbar（导航 + Ctrl+K 全局搜索）、LLMAssistant（AI 助手）、
  FileTree/VersionLog/CollectionCard/CommentSection/SettingsSection（跨页复用件）。

## 关键机制

### 路由表（App.jsx:96-118，全部真实路由）

| 路由 | 组件 | 职责 |
|---|---|---|
| `/` | Plaza | 合集广场 |
| `/:username/:collName` | HashRedirect → Explorer | 用户合集浏览器（64hex 参数自动重定向） |
| `/create` | AnonCreator | 匿名合集创建器 |
| `/c/:hash` | AnonExplorer | 匿名合集查看器（统一路由） |
| `/anon/collections/:hash` | AnonExplorer | 匿名合集查看器（legacy 路径） |
| `/anon` | AnonExplorer | 空 hash 查看器（显示 EmptyState） |
| `/p2p` | P2PPanel | P2P 双栈总控 |
| `/ipfs` | IPFSPanel | IPFS 总览 |
| `/bt` | BTController | BT 下载控制器 |
| `/bt/controller` | Navigate → `/bt` | 旧路径重定向 |
| `/bt/status` | BTPanel | BT DHT 状态 |
| `/bt/dht` | DHTExplorer | BT DHT 查询 |
| `/p2p/dashboard` | P2PDashboard | P2P 网络仪表盘 |
| `/p2p/topology` | P2PTopology | 网络拓扑 SVG |
| `/ipfs/dht` | DHTExplorer | IPFS DHT 查询（共用组件） |
| `/settings` | Settings | 设置页（7 分区） |
| `*` | Navigate → `/` | 未知路径兜底，避免空白页 |

### HashRedirect（App.jsx:25-36）

`/u/coll` 双段路由中任一段匹配 64 位 hex（`HASH_RE = /^[a-f0-9]{64}$/i`）→ 声明式
`<Navigate to={/c/<hash>} replace />` 转统一合集路由。

**坑**：旧写法在 render 期间调 `navigate()`（副作用），react-router 告警且
StrictMode 下双重触发；声明式 `<Navigate>` render 期间只返回元素，无副作用。

### 全局状态（App.jsx:20-21、89-90）

- `AppContext`：`username`（localStorage `peerdrive_username` 持久化）、`nodeInfo`。
- `PageContext`：`pageContext`/`setPageContext`——各页面挂载时上报当前页语义数据
  （Explorer 报 entries 摘要、AnonCreator 报 fileCount/entryCount、AnonExplorer 报
  合集名），LLMAssistant 消费它生成上下文感知回答。
- `AppErrorBoundary`（App.jsx:39-74）：单页面/组件抛错不白屏整树，显示「重试」按钮
  （`getDerivedStateFromError` + `handleRetry` 清 error 状态）。

### 页面清单（文件名 → 一句话职责）

| 文件 | 职责 |
|---|---|
| `Plaza.jsx` | 合集广场：本机匿名合集 + P2P 公开合集双 tab（grid/list），hash 搜索直跳，fork 源传给 `/create`；无后端时展示 DUMMY 示例卡 |
| `Explorer.jsx` | 用户合集浏览器：条目文件夹导航、上传（uploadFile + addEntry）、提交版本、合并（弹窗，含匿名源客户端合并）、保存快照到本地（/local/save）、右侧 VersionLog |
| `AnonCreator/index.jsx` | 匿名合集创建器：三栏（左：文件源筛选/合集浏览/系统目录浏览；中：预览；右：条目编辑器），拖拽/批量保存/URL 注册，移动端 tab 切换 |
| `AnonExplorer/index.jsx` | 匿名合集查看器：hash 搜索（打字即跳）、文件夹导航、单文件/多文件视图、预览（图片/文本/PDF/通用）、嵌套合集链接、评论、保存副本 |
| `Settings.jsx` | 设置页 7 分区：节点连接（多后端）/ 认证 / 存储管理 / IPFS 兼容 / LLM 配置 / WebDAV（挂载信息）/ 关于（版本与数据） |
| `P2PPanel.jsx` | P2P 双栈总控：IPFS DHT + BT DHT 双网宣布/查找/文件共享生命周期 |
| `P2PDashboard.jsx` | P2P 网络仪表盘：节点/对等连接/文件宣布与查找状态，RTT 着色 |
| `P2PTopology.jsx` | 网络拓扑：SVG 展示已连接对等节点及状态 |
| `BTPanel.jsx` | BT DHT 状态监控：Infohash 宣布与查找 |
| `BTController.jsx` | BT 下载控制器：类 qBittorrent 的下载管理（magnet/种子上传/暂停/续传/做种/删除） |
| `DHTExplorer.jsx` | DHT 查询工具：`/bt/dht` 与 `/ipfs/dht` 共用 |
| `IPFSPanel.jsx` | IPFS 面板：CID pin、pin 列表、网关状态、libp2p 监控 |

### 页面共同模式（三个合集页共用套路）

1. **文件夹式导航**（Explorer / AnonExplorer / AnonCreator CollBrowser 三处独立
   实现）：`navPath` state + 条目 path 前缀切分当前层 dirs/files；
   `navIn(dir)`/`navBack()` 进出目录。条目 path 即合集内相对路径，文件夹是
   隐式的（无独立 dir 对象）。
2. **PageContext 上报**：页面挂载/数据变化时 `setPageContext({type, ...})`
   向 LLMAssistant 提供当前页语义（Explorer 报 `type:'explorer'` + entries 摘要，
   AnonCreator 报 `type:'anonCreator'` + 计数，见 App.jsx:21）。
3. **toast 提示模式**：AnonCreator/AnonExplorer 各自维护 `toastMsg`/`toastErr` +
   `toastTimerRef`（坑 10），无全局通知系统。
4. **hash 提取**：Plaza/AnonExplorer/SearchBar 共用 `extractHash` 正则
   `/\b([a-f0-9]{64})\b/i`，粘贴/输入即识别跳转。

### 页面级子组件树

**AnonCreator/**（17 文件）：`LeftPanel`（筛选+列表，聚合 CollectionRow/CollFileRow/
CollBrowser/SystemBrowse/SearchHistory）、`MiddlePanel`（预览）、`RightPanel`
（编辑器，聚合 EditorPanel/EditorToolbar/FileTree/NamePrompt）、`CollBrowser`
（合集浏览，聚合 CollBrowserNav）、`constants.js`（SOURCE_TABS/排序选项）、
`utils.js`（fileIcon/fmtSize/collDisplayName/搜索历史）。

**AnonExplorer/**（16 文件）：`SearchBar`（hash 输入）、`CollectionHeader`（名称/
标签/可见性/保存副本）、`BreadcrumbNav`、`FileList`+`FileRow`、`SingleFilePreview`
（单文件直出预览，聚合 ImagePreview/TextPreview/PdfPreview/GenericFilePreview/
NestedCollectionLink）、`Toast`、`EmptyState`、`utils.js`（extractHash）。

预览分发逻辑（SingleFilePreview.jsx:41-46）：MIME 前缀 + 扩展名双判——`image/*`
→ ImagePreview、`text/*` 或常见代码扩展名 → TextPreview、`application/pdf` →
PdfPreview、其余 → GenericFilePreview；条目 hash 命中本机合集集合
（`allCollHashes`）→ NestedCollectionLink（嵌套合集跳转）。预览数据一律经
`api.downloadAnonFile`（admin-bin 二进制响应）→ Blob objectURL，**不用 HTTP URL**。

### 移动端适配

- AnonCreator：三栏在 `md` 断点以下折叠为单栏 + 顶部 tab 切换
  （files/preview/editor，`mobilePanel` state，index.jsx:461-478）；选择文件后
  自动切到预览 tab。
- Navbar：桌面链接折叠为汉堡抽屉（Portal 到 body，`mobileMenuOpen` state，
  Navbar.jsx:355-383）；NavDropdown 在触摸端退化为 click 切换。

### 全局组件

- **Navbar.jsx**（388 行）：顶部导航（桌面链接 + 移动端抽屉 Portal）、`NavDropdown`
  下拉（Portal 到 body 防 overflow 裁剪，hover/click 双交互）、`SearchPanel`
  全局搜索（Ctrl+K 或 `/` 快捷键；200ms 防抖；↑↓ 选择 + Enter 打开；本机/公开
  合集分栏）。
- **LLMAssistant.jsx**（654 行）：右下角 AI 助手面板，17 个 function tool
  （navigate_to / get_node_info / list_collections / list_public_collections /
  search_collections / create_collection / get_collection_info /
  add_file_to_collection / commit_collection / fork_collection / get_version_log /
  register_local_file / register_folder / get_tasks / get_p2p_status /
  create_anon_collection / verify_file），直连 LLM 端点（
  `POST {endpoint}/v1/chat/completions`，api.js 的 LLM 配置），工具执行调 api.js。
- **FileTree.jsx**：条目目录树（拖拽/重命名/移动/新建文件夹），导出 `buildFlatTree`。
- **VersionLog.jsx**：版本历史 + 回滚。
- **CollectionCard.jsx**：合集卡片（grid/list 两模式）。
- **CommentSection.jsx**：合集评论（直连 regserver）。
- **SettingsSection.jsx**：设置分区卡片（标题/描述/子内容/保存）。

## 与其它模块的关系

```
main.jsx（挂载 #root）
  → App.jsx（路由 + Context + ErrorBoundary）
      ├─ Navbar / LLMAssistant（全局，常驻）
      ├─ pages/*（路由组件，import * as api）
      │     └─ components/*（复用件）
      └─ api.js → ws.js → /ws/peer（唯一后端通道）
```

- **上（入口）**：`main.jsx` 仅 `createRoot(...).render(<App />)`。
- **下（api.js）**：页面全部数据经 api.js；预览/下载经 `downloadAnonFile`/
  `downloadUserFile`/`getBlobUrl` 拿 Blob（WS 通道，非 HTTP URL）。
- **横（组件间）**：`PageContext` 是页面 → LLMAssistant 的唯一数据通道；导航全部
  经 react-router `useNavigate`/`Link`（SPA 内，不整页刷新）。
- **对端（后端）**：页面只感知 api.js 具名函数，不感知 WS/HTTP 传输（LAYERS.md
  §5：前端禁止直接 fetch 本地 HTTP 端点）。

## 坑与设计决策

1. **HashRedirect 声明式 Navigate**：render 期间副作用 navigate 在 StrictMode 下
   双重触发（App.jsx:27-28）。
2. **AnonExplorer 请求序号守卫（fetchSeqRef）**：打字即跳/粘贴/卡片点击快速切换
   hash 时，旧请求慢响应不得覆盖当前合集（index.jsx:29-32、49-53）。
3. **空合集不自动删除**：`fetchCollection` 中注释明确不要 `api.deleteFile` 删空
   合集——读接口带写副作用，无注册服务器时真实删除存储文件、有注册服务器时 401
   后消息与实际不符（AnonExplorer/index.jsx:53-55）。
4. **navigate(-1) 深链坑**：新标签直接打开的深链无历史，`history.length≈1` 时回退
   首页而非离开 SPA（AnonExplorer SearchBar 回退逻辑）。
5. **Plaza「天线」tab 已删**：BEP51 只能采样 torrent DHT 的 20 字节 infohash，
   永远取不到 peerdrive 合集 announce 的 64 位 hash，点击打开的闭环不可达
   （Plaza.jsx:86-87，git log 有发现背景）。
6. **AnonCreator forkFrom 无 entries**：Plaza 卡片传来的是 AnonCollectionSummary
   （只有 hash/friendly_name/entry_count），直接 `setEntries(c.entries||[])` 恒为空
   合集——必须按 hash 拉全量（index.jsx:73-83）。
7. **addEntry 去掉 500ms lastClick 防抖**：程序化批量 add（handleSysAddFolder/
   拖拽）会被时间闸静默丢弃（加了 N 个实际只进 1-2 个）——改为 reducer 内按
   path+hash 去重（AnonCreator/index.jsx:204-206）。
8. **URL provider 识别**：合集条目可能只有 URL provider（无 sha256），一律当
   sha256 会生成非法 64 位 hash 被后端拒绝——按 `^https?://` 形态识别存 url
   provider（index.jsx:207-211）。
9. **目录重命名/移动必须连带子条目**：只改 path 精确相等的条目会把
   `dir/a.txt` 变孤儿条目浮到根部；移动是「去源」语义不是复制；目标不能是自身/
   子目录；防呆检查放 `setEntries` 回调外（state updater 保持纯函数，StrictMode
   下副作用重复触发）（index.jsx:227-263）。
10. **toast 定时器必须复用 ref**：每次新开 setTimeout 会让前一个 toast 的定时器
    在未到 3s 时清掉消息（快速连续操作提示一闪而过）（index.jsx:172-179）。
11. **匿名合集合并是客户端实现**：后端 `/actions/merge` 只接受用户合集对，没有
    匿名源端点——`mergeAnonIntoLocal` 在浏览器端按 path 对比 hash 实现 ours/theirs/
    manual 三策略；manual 的 409 冲突清单在弹窗内展示并支持换策略重试
    （Explorer.jsx:111-165）。
12. **SearchPanel activeIdx 负数**：total=0 时旧实现 `Math.min(i+1, total-1)` →
    -1，高亮无意义——空结果钳到 0（Navbar.jsx:146-148）。
13. **路由兜底 `*`**：未知路径 Navigate 回 `/`，避免空白页（App.jsx:117）。

## 测试

无页面级 E2E 框架（vitest + happy-dom + testing-library），覆盖如下：

| 文件 | 覆盖 |
|---|---|
| `tests/smoke.test.jsx` | App 渲染不崩（`screen.getByText('Peerdrive')`） |
| `tests/components.test.jsx` | 页面/组件基础渲染（App/Plaza/AnonCreator/AnonExplorer/Settings/VersionLog/Navbar/EditorToolbar/FileTree） |
| `tests/FileTree.test.jsx` | FileTree 渲染 + `buildFlatTree` 平铺逻辑 |

- 所有组件测试经 `tests/setup.js` 全局 `vi.mock('../src/api.js')`（见 api-layer.md
  测试节），页面不产生真实网络请求。
- 手动验证：`npm run dev` 起前端 + 本地节点，走全流程（创建合集 → 上传 → 浏览 →
  下载 → P2P/BT/IPFS 面板 → 设置）。Playwright 冒烟脚本在 `front/tests/`
  （playwright-smoke.mjs、pw-settings-mobile.mjs），连宿主机 CDP 浏览器用
  （见全局 AGENTS.md playwright-test 技能）。

## 文件清单

- `front/src/main.jsx`（7 行）—— 入口
- `front/src/App.jsx`（127 行）—— 路由/Context/ErrorBoundary
- `front/src/pages/`（10 顶层页面 + 2 页面组件树，共 41 文件）—— 见「页面清单」表
- `front/src/components/`（7 文件：Navbar/LLMAssistant/FileTree/VersionLog/
  CollectionCard/CommentSection/SettingsSection）
- `front/tests/`（smoke.test.jsx / components.test.jsx / FileTree.test.jsx +
  playwright 冒烟脚本）
- 相关文档：`doc/layers/L8-frontend/ws-client.md`（数据通道）、`api-layer.md`（API 面）