# Peerdrive 问题反馈与修复回复

## 这集神了.txt

| 问题 | 状态 |
|------|------|
| 设置按钮不该放菜单中间 | ✅ 已修复 — Navbar 精简，⚙ 移到最右侧，左侧仅保留导航链接 |
| P2P 指示灯意义不明 | ✅ 已修复 — 移除 Navbar 上的 P2P 状态点/文字，连接状态由各页面按需展示 |
| 文件注册用 prompt() 输入路径 | ✅ 已修复 — 新增"浏览文件系统"模态框，直接浏览节点目录选文件/文件夹 |
| Webapp 是纯前端，依赖 endpoint 设置 | ✅ Settings 页已存在，支持修改 endpoint + auth key + hash verifier，持久化到 localStorage |
| 一些链接点开后新页面了 | ⚠️ 待确认 — Explorer 里的下载链接用了 `target="_blank"`，这是有意的（文件下载）；其他若还有跳新页面需要具体指出 |

## 我要死了.txt

| 问题 | 状态 |
|------|------|
| Merge 自己填用户名/合集名 | ✅ 已修复 — Merge 弹窗改为从已知匿名合集+公开合集下拉选择，也可选"自定义..."手动输入 |
| Collection 界面应列出自己创建的 + 远端收集的 | ✅ 已修复 — Plaza 首页改为三 Tab：我的合集（匿名）、公开合集、搜索 |
| 下拉选单不如选项按钮直观 | ⚠️ 已改为 Tab/按钮的形式（Plaza 用三个 tab 按钮），部分地方仍用 select 是有意为之（选项多时 select 更紧凑） |
| 右上角不要填用户名 | ✅ 已修复 — Navbar 彻底移除用户名输入框 |
| 匿名是默认，不应该必填 | ✅ 已修复 — 合集创建改匿名优先，Plaza 默认显示匿名合集 |
| 匿名 collection 编辑按钮 | ✅ 已修复 — AnonExplorer 的 Fork 按钮文本改为"克隆并修改" |
| 首屏是 collection + 可以从注册服务器/p2p 发现 | ✅ 已修复 — Plaza 有"公开合集"Tab 从 registry 拉取，"我的合集"Tab 显示自己的匿名合集 |

## 我服了.txt

| 问题 | 状态 |
|------|------|
| 创建时自己填 hash | ⚠️ AnonCreator 的"按 hash 打开"输入框仍然存在（设计为高级功能：粘贴 hash 快速打开已知合集）。常规流程不需要手动填 hash — 从文件列表拖拽/点击添加，或从历史合集打开 |
| 文件管理 → 合集管理 → 分享的层级 | ✅ 文件管理器已重做，浏览+注册+自动创建合集一体化 |
| 操作习惯应该像网盘/资源管理器 | ⚠️ 方向正确但仍在完善中 — 文件管理器已有目录树，还需加入右键操作等 |

## collection.txt

| 问题 | 状态 |
|------|------|
| 匿名 collection 怎么又要必填了 | ✅ 已修复 — 匿名 collections 全流程无需用户名，Plaza/AnonCreator 均为匿名优先 |

## 文件管理器.txt

| 问题 | 状态 |
|------|------|
| 文件管理器是扁平的 | ✅ 已修复 — 改为按目录结构组织的树状视图 |
| 添加文件夹自动按目录结构组织一个 collection | ✅ 已修复 — 注册目录 / 选中文件后可选自动创建匿名合集 |
| 添加单个文件就单个文件组织 collection | ✅ 已修复 |
| 同时保留一个扁平视图（时间排序 + 分类筛选 + 搜索） | ⚠️ 待做 — 目前只有树状视图，需要增加 tab 切换或侧栏切换来同时展示扁平模式 |

## 合集界面.txt

| 问题 | 状态 |
|------|------|
| 右边的历史（VersionLog）是什么 | ⚠️ 这是合集的 Git 风格版本历史面板（Explorer 右侧栏），列出每次 Commit 的快照。UI 上可以考虑加标题说明"版本历史" |
| 创建合集时左边也能从其他合集选文件 | ⚠️ 待做 — AnonCreator 左侧目前只有注册文件列表 + 历史合集入口（点击历史合集目前是直接全部加载）。需要改为：点开历史合集 → 展示文件 + 复选框 → 选择后添加到当前工作区 |
| CORS 访问 /files/browse 失败 | ✅ 已修复代码 — Go router 的 Access-Control-Allow-Headers 改为动态回显浏览器 preflight 请求头（`upgrade-insecure-requests` 等），`Credentials` 仅在非 wildcard origin 时设置。**Go 服务器需要重新编译部署才能生效** |

## 创建合集.txt

| 问题 | 状态 |
|------|------|
| 历史合集点了之后把所有文件怼过来 | ⚠️ 待做 — 当前 AnonCreator 历史合集面板是点击直接加载全部条目到工作区。需要改为：展开合集 → 显示文件复选框 → 按钮"将选中文件添加到当前合集" |
| "如果不会改就问" | 正在逐项处理中，会继续跟进 |

## llm.txt

已另写 LLM 实现技术报告（见下方 LLM 技术细节部分）。

---

## LLM 助手技术细节

### 模型调用

使用项目自建的 LLM 代理 `siliconflow.moonchan.xyz`，该代理转发请求到 SiliconFlow API (`api.siliconflow.cn`)，并在代理层自动注入 API Key，前端无需携带 Authorization 头。

调用流程：
1. 前端构造 OpenAI 兼容的 chat completion 请求
2. 请求发送到 `{API_BASE}/llm/v1/chat/completions`
3. 响应支持两种模式：`stream: true`（SSE 流式）和 `stream: false`（标准 JSON）

### 上下文获取

LLM 助手通过 `PageContext` 获得当前页面信息。每个页面组件在 `useEffect` 中调用 `setPageContext()` 推送自己的上下文：

| 页面 | 上下文内容 |
|------|-----------|
| Plaza | `{ type: 'plaza', username, collectionCount, isSearching, searchQuery }` |
| Explorer | `{ type: 'explorer', username, collectionName, entryCount, entries }` |
| FileManager | `{ type: 'fileManager', fileCount, selectedCount, sortBy }` |
| AnonCreator | `{ type: 'anonCreator', fileCount, entryCount, friendlyName, openHash }` |
| Settings | `{ type: 'settings' }` |

每次用户发送消息时，LLMAssistant 构建 system prompt，内含当前 `route` + `timestamp` + `pageContext` JSON，让模型知道用户正在看哪个页面、有什么数据。

### Function Calling

定义了 17 个 tool：

`navigate_to`, `get_node_info`, `list_collections`, `list_public_collections`, `search_collections`, `create_collection`, `get_collection_info`, `add_file_to_collection`, `commit_collection`, `fork_collection`, `get_version_log`, `register_local_file`, `register_folder`, `get_tasks`, `get_p2p_status`, `create_anon_collection`, `verify_file`

执行流程：
1. 用户消息发送到 LLM，携带 tool 定义
2. LLM 可能返回 `tool_calls`（流式场景下 SSE 增量累积 delta.tool_calls）
3. 前端执行 `executeTool()` — 映射 tool name 到 api.js 函数
4. 执行结果作为 tool role 消息回喂 LLM
5. LLM 根据结果生成自然语言回复
6. 最多循环 5 轮（防死锁）

### 流式输出

使用 SSE（Server-Sent Events）读取，逐行解析 `data: {...}` JSON。流式处理逻辑：
- `reasoning_content`（Qwen 思考链）：先于 content 到达，仅日志记录
- `delta.content`：累积并实时更新 UI 显示
- `delta.tool_calls`：按 index 索引增量拼接，流结束后统一执行

### 闭包修复

`handleSend` 中使用本地 `let accMessages = [...]` 数组维护对话上下文，而非直接引用 React `messages` state — 避免多轮 tool-calling 时因 React state 异步更新导致的闭包读取过期值。

### 免费模型

代理支持 13 个 SiliconFlow 免费模型（`FREE_LLM_MODELS` 常量），在 Settings 页可通过下拉框选择（含"自定义..."手动输入其他模型）。
