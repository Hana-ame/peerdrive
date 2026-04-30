# Peerdrive 前端代码地图

> 按模块列出每个界面元素对应的源文件位置

---

## 布局外壳 (Layout Shell)

| 模块 | 文件 |
|------|------|
| App 根组件 + 布局 + 路由 | `front/src/App.jsx` |
| React 入口挂载 | `front/src/main.jsx` |

---

## 菜单 (Menu)

| 模块 | 文件 |
|------|------|
| 顶部导航栏 (Desktop) | `front/src/components/Navbar.jsx` |
| 移动端底部导航 | `front/src/components/MobileNav.jsx` |
| LLM 浮窗 | `front/src/components/LLMAssistant.jsx` |

---

## 页面路由 (Pages)

| 路由 | 页面 | 文件 |
|------|------|------|
| `/` | Plaza 合集广场 | `front/src/pages/Plaza.jsx` |
| `/files` | FileManager 文件管理 | `front/src/pages/FileManager.jsx` |
| `/:username/:collName` | Explorer 合集浏览器 | `front/src/pages/Explorer.jsx` |
| `/anon` | AnonExplorer 入口 | `front/src/pages/AnonExplorer/index.jsx` |
| `/anon/collections/:hash` | AnonExplorer 浏览合集 | 同上 |
| `/anon/create` | AnonCreator 创建合集 | `front/src/pages/AnonCreator/index.jsx` |
| `/p2p` | P2PPanel 双栈总控 | `front/src/pages/P2PPanel.jsx` |
| `/p2p/dashboard` | P2PDashboard 仪表盘 | `front/src/pages/P2PDashboard.jsx` |
| `/p2p/topology` | P2PTopology 网络拓扑 | `front/src/pages/P2PTopology.jsx` |
| `/p2p/dht` | DHTExplorer 查询 | `front/src/pages/DHTExplorer.jsx` |
| `/ipfs` | IPFSPanel | `front/src/pages/IPFSPanel.jsx` |
| `/bt` | BTPanel 总览 | `front/src/pages/BTPanel.jsx` |
| `/bt/controller` | BTController 下载管理 | `front/src/pages/BTController.jsx` |
| `/settings` | Settings 设置 | `front/src/pages/Settings.jsx` |

---

## 共享组件 (Shared Components)

| 组件 | 文件 |
|------|------|
| FileTree 文件树 | `front/src/components/FileTree.jsx` |
| CollectionCard 合集卡片 | `front/src/components/CollectionCard.jsx` |
| CollectionBuilder 合集构建器 | `front/src/components/CollectionBuilder.jsx` |
| Sha256Manager SHA256 寻址 | `front/src/components/Sha256Manager.jsx` |
| VersionLog 版本历史 | `front/src/components/VersionLog.jsx` |
| CommentSection 评论 | `front/src/components/CommentSection.jsx` |
| P2PStatus P2P 状态 | `front/src/components/P2PStatus.jsx` |
| WebRTCPeer | `front/src/components/WebRTCPeer.jsx` |
| WebRTCTransfer | `front/src/components/WebRTCTransfer.jsx` |
| ServiceStatus 服务健康 | `front/src/components/ServiceStatus.jsx` |
| ActiveConnPanel 活跃连接 | `front/src/components/ActiveConnPanel.jsx` |
| PeerDetailPanel 节点详情 | `front/src/components/PeerDetailPanel.jsx` |
| VisibilityPicker 可见性 | `front/src/components/VisibilityPicker.jsx` |
| UserGroupPicker 用户组 | `front/src/components/UserGroupPicker.jsx` |
| PathRegistrar 路径注册 | `front/src/components/PathRegistrar.jsx` |
| SettingsSection 设置分组 | `front/src/components/SettingsSection.jsx` |
| AnonCollectionManager | `front/src/components/AnonCollectionManager.jsx` |

---

## 基础设施 (Infrastructure)

| 模块 | 文件 |
|------|------|
| API 通信层 (HTTP + localStorage) | `front/src/api.js` |
| IndexedDB 本地存储 | `front/src/storage/localDB.js` |
| 同步管理器 | `front/src/storage/syncManager.js` |
| 全局样式 (Tailwind) | `front/src/index.css` |

---

## AnonCreator 子组件

目录：`front/src/pages/AnonCreator/`

| 文件 | 说明 |
|------|------|
| `index.jsx` | AnonCreator 主入口（三栏布局容器） |
| `LeftPanel.jsx` | 左侧栏 — 文件源选择 |
| `MiddlePanel.jsx` | 中间栏 — 合集内容 |
| `RightPanel.jsx` | 右侧栏 — 预览/编辑 |
| `EditorPanel.jsx` | 文本编辑器 |
| `EditorToolbar.jsx` | 编辑器工具栏 |
| `CollBrowser.jsx` | 合集浏览器 |
| `CollBrowserNav.jsx` | 合集浏览导航 |
| `CollectionHeader.jsx` | 合集头部信息 |
| `CollectionRow.jsx` | 合集列表行 |
| `CollFileRow.jsx` | 合集内文件行 |
| `FileSourceRow.jsx` | 文件源行条目 |
| `SourceTabs.jsx` | 文件源 Tab 切换 |
| `SourceFilters.jsx` | 文件源过滤器 |
| `SystemBrowse.jsx` | 系统文件浏览 |
| `RegisteredView.jsx` | 已注册文件视图 |
| `SearchHistory.jsx` | 搜索历史 |
| `TimelineView.jsx` | 时间线视图 |
| `SplitHandle.jsx` | 面板分割拖拽手柄 |
| `NamePrompt.jsx` | 命名对话框 |
| `Toast.jsx` | 提示通知 |
| `constants.js` | 常量定义 |
| `utils.js` | 工具函数 |

---

## AnonExplorer 子组件

目录：`front/src/pages/AnonExplorer/`

| 文件 | 说明 |
|------|------|
| `index.jsx` | AnonExplorer 主入口 |
| `SearchBar.jsx` | 搜索栏 (Hash 输入) |
| `BreadcrumbNav.jsx` | 面包屑导航 |
| `CollectionHeader.jsx` | 合集头部信息 |
| `EmptyState.jsx` | 空状态占位 |
| `FileList.jsx` | 文件列表 |
| `FileRow.jsx` | 文件行 |
| `ImagePreview.jsx` | 图片预览 |
| `PdfPreview.jsx` | PDF 预览 |
| `TextPreview.jsx` | 文本预览 |
| `GenericFilePreview.jsx` | 通用文件预览 |
| `SingleFilePreview.jsx` | 单文件预览容器 |
| `NestedCollectionLink.jsx` | 嵌套合集链接 |
| `Toast.jsx` | 提示通知 |
| `utils.js` | 工具函数 |
