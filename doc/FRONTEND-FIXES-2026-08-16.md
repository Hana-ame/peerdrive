# 前端 Bug 修复记录 — 2026-08-16

> 时间: 2026-08-16
> 背景: 对 `front/` 做一轮代码 review（对照后端控制器实现逐一核实），共修复 **9 处** bug。
> 高严重度的共同根因：`src/App.jsx` **没有 error boundary**，任何一个未捕获的
> TypeError/ReferenceError 都会把整个应用卸下（白屏），所以崩溃类问题优先修。
> 所有修复均已通过 `npx vitest run`（26/26）与 `npx vite build` 验证。

---

## 高严重度（崩溃 / 功能不可用）

### H1. Navbar SearchPanel 按键崩溃 — `src/components/Navbar.jsx`

- **问题**: 搜索结果面板展开时，keydown 处理器构造
  `const all = [...results.anon, ...results.public, ...results.files]`，
  但面板打开初始态 `results` 只有 `{ anon: [], public: [] }`（没有 `files` 字段），
  `...results.files` 对 `undefined` 展开 → **TypeError → 白屏**。
  另有死代码块引用从未声明的 `hasFiles` 变量。
- **发现背景**: 阅读 SearchPanel 状态初始值与 keydown 处理逻辑时发现状态字段不一致
  （`files` 仅在搜索完成后才写入）。
- **修复**: keydown 的 `all` 数组去掉 `...results.files`；删除 `hasFiles` 死代码块；
  catch 分支改回 `setResults({ anon: [], public: [] })`（原误加 `files: []` 会造成形态不一致）。

### H2. 上传同意开关失效 — `src/api.js` + `src/pages/Settings.jsx:407`

- **问题**: `uploadConsent()` 函数体没有 `return`（返回 `undefined`），
  `Settings.jsx` 里 `api.uploadConsent().then(...)` 对 `undefined` 调 `.then` → **同步抛错**，
  同意开关点了没反应，spinner 卡死。
- **发现背景**: 代码审阅时发现 `uploadConsent` 只有 `request(...)` 调用没有 `return`，
  与文件里其它 API 函数形态不一致。
- **修复**: 函数改为 `async` 并 return `request('GET', '/consent')`。

### H3. FileManager 分享按钮 ReferenceError — `src/pages/FileManager.jsx`

- **问题**: `handleShare` 在分享按钮 onClick 处被引用（旧 912/932 行）但从未定义 →
  点击即 ReferenceError。
- **发现背景**: 代码审阅发现引用处没有对应定义。
- **修复**: 实现 `handleShare(file, e)`：复制 `api.getDownloadUrl(file.hash)` 到剪贴板
  （`navigator.clipboard.writeText`，失败时回退 `document.execCommand('copy')`），
  成功后用现有 `setNotification` 提示。

### H4. FileManager Rules-of-Hooks 违规 — `src/pages/FileManager.jsx`

- **问题**: `useMemo(() => getDbDirContents(files, dbDirPath), [files, dbDirPath])`
  写在 `renderDbDirBrowser()` **渲染函数内部** → 条件渲染时 hook 数量变化 →
  React 报 "Rendered more hooks than during the previous render"。
- **发现背景**: 阅读 `renderDbDirBrowser` 时发现 `useMemo` 在渲染函数体内（React 硬性禁止）。
- **修复**: 把 `useMemo` 提升到组件顶层，结果存为 `dbDirContents`，渲染函数直接引用。

### H5. ActiveConnPanel 对端列表崩溃 — `src/components/ActiveConnPanel.jsx`

- **问题**: 后端 `GET /p2p/peers` 返回 `{ peers: [...] }`（包装对象，见
  `back/internal/controller/p2p.go:118`），但前端 `setPeers(p || [])` 把整个对象塞进
  `peers` state，随后 `peers.filter(...)`（inbound/outbound 统计处）对对象调 `.filter` → **TypeError**。
- **发现背景**: 对照后端 `GetPeers` 返回值核实前端消费处（5s 轮询刷新必触发）。
- **修复**: `setPeers(Array.isArray(p) ? p : (p?.peers || []))`，兼容数组与包装对象两种形态。

## 中严重度（数据/链接错误）

### M1. 合集下载链接路径未编码 — `src/api.js`

- **问题**: `getAnonFileDownloadUrl(hash, p)` 与 `getUserFileDownloadUrl(username, coll, filepath)`
  直接把原始路径拼进 URL，文件名含空格/`#`/`?` 时链接损坏（AnonExplorer FileRow /
  SingleFilePreview 的下载与 inline 预览都会受影响）。
- **发现背景**: 阅读 API 构造 URL 的代码时发现无任何编码处理。
- **修复**: 新增 `encodePath = (p) => (p||'').split('/').map(encodeURIComponent).join('/')`，
  hash/username/coll 也用 `encodeURIComponent`。
  **坑（已验证）**: 不能把 `/` 也编码成 `%2F` —— gin 的 `*filepath` 路由匹配用的是
  **已解码的** `c.Request.URL.Path`，`%2F` 会被先解码成 `/` 破坏 wildcard 分段；
  逐段编码保留 `/` 分隔符，后端 `strings.TrimPrefix(c.Param("filepath"), "/")`
  （`back/internal/controller/anon.go:105`）与 `Entries[].Path` 比对仍然正确。

### M2. BT 节点数字段名错 — `src/pages/P2PPanel.jsx` + `src/pages/BTPanel.jsx`

- **问题**: 读取 `btStatus.node_count`，后端实际返回 `num_nodes`
  （`back/internal/controller/p2p.go:471`）→ 节点数永远显示 `-`。
- **发现背景**: 对照 `p2p.go` BT status 响应结构逐字段核实。
- **修复**: `node_count` → `num_nodes`（共 4 处：P2PPanel 3 处 + BTPanel 1 处）。

### M3. UserGroupPicker 头像空串崩溃 — `src/components/UserGroupPicker.jsx`

- **问题**: `(u.nickname || u.username)[0].toUpperCase()`，两者皆空时对 `''[0]`
  （`undefined`）调 `.toUpperCase` → **TypeError**。
- **发现背景**: 阅读过滤后用户列表渲染时发现无空值兜底。
- **修复**: 兜底 `(u.nickname || u.username || '?')[0]`。

### M4. CommentSection 重复请求 — `src/components/CommentSection.jsx`

- **问题**: 两个 `useEffect`（`[regServerUrl, hash]` 与 `[hash]`）在挂载/换 hash 时
  都调用 `fetchComments()` → 每次切合集发两次请求。
- **发现背景**: 阅读 effect 列表时发现两个 effect 功能重叠。
- **修复**: 合并为单一 effect，deps 为 `[regServerUrl, hash]`，内部先重置
  `comments/content/error` 再拉取。

---

## 第二轮修复（后端契约核实 + AnonCreator / 页面路由）

> 背景: 对照后端控制器/路由逐一核实前端 API 调用形态（round 2）。共修复 **13 处**。
> 注: `App.jsx` 其实已有 `AppErrorBoundary`（子代理报告的"No ErrorBoundary"为过期结论，未采纳）。

### R2-1. P2PStatus 对端列表崩溃 — `src/components/P2PStatus.jsx`

- **问题**: 后端 `/p2p/peers` 返回 `{ peers: [...] }`，`setPeers(p || [])` 把包装对象塞进 state → `.map` 崩溃。
- **修复**: `Array.isArray(p) ? p : (p?.peers || [])`（discovered 同）。

### R2-2. connectPeer 请求体错误 — `src/api.js`

- **问题**: 发送 `{peer_id, addrs}`，后端 `ConnectPeer` 绑定的是 `{addr}`（p2p.go），且不拼 `/p2p/<peer_id>` → 必然 500/连不上。
- **修复**: 构造 `{addr}`，含完整 multiaddr（无 `/p2p/` 后缀时自动补）。

### R2-3. p2pSync/p2pPush 请求体错误 — `src/api.js`

- **问题**: 发送 `{collection_name}`，后端 `SyncFromPeer` 绑定 `{peer_id, hash, file_hashes, target_dir}`、`PushSync` 绑定 `{hash, entries, target_dir}` → 400。
- **修复**: sync 发 `{peer_id, hash}`；push 发 `{hash, entries, target_dir}`。

### R2-4. Explorer 错误 API — `src/pages/Explorer.jsx`

- **问题**: 用 `api.getCollection(username, collName)`（后端 `GET /collections/:username/:coll` 返回含 `data` 数组，无 `entries`）；真正返回 `{collection, entries}` 的是 `getUserCollection`。39/119 行两处 → 目录永远为空。
- **修复**: 两处改 `api.getUserCollection`；`(e.path||'').startsWith(prefix)` 防空。
- **发现背景**: 对照 `back/internal/controller/collection.go` GetCollection 的返回结构（含 `data` 而非 `entries`）核实。

### R2-5. removeCollectionEntry 路径未编码 — `src/api.js`

- **修复**: 路径过 `encodePath(path)`。

### R2-6. FileManager 不可达 / 无兜底路由 — `src/App.jsx`

- **问题**: `/files` 路由指向 `<Navigate to="/" replace />`，FileManager 永远渲染不了；无 `*` 兜底；HashRedirect 在渲染期调 `navigate()`。
- **修复**: `/files` 改 `<FileManager />`；加 `<Route path="*" element={<Navigate to="/" replace />} />`；HashRedirect 用声明式 `<Navigate>`；删无用 `useNavigate`。

### R2-7. AnonCreator addEntry 防抖吞程序化添加 — `src/pages/AnonCreator/index.jsx`

- **问题**: `addEntry` 里 500ms `lastClick` 时间闸，`handleSysAddFolder`/拖拽等循环批量 add 被静默丢弃（walkDir 不重试，加 N 个实际只进 1-2 个，toast 还谎报"已添加 N 个文件"）。
- **发现背景**: 读 `handleSysAddFolder` 循环时发现对 `addEntry` 的高频调用会被防抖闸掉。
- **修复**: 删除时间闸，改为 reducer 内按 `path + hash` 去重（防连点重复）；`useRef` 不再需要，一并移除。

### R2-8. AnonCreator fork-from-Plaza 空合集 — `src/pages/AnonCreator/index.jsx`

- **问题**: `forkFrom` 来自 `/anon/collections` 的 **AnonCollectionSummary**（只有 hash/friendly_name/entry_count，无 `entries` 字段，见 `back/internal/model/anon.go` summary），`setEntries(c.entries || [])` → fork 出来永远是空合集。
- **发现背景**: 对照后端 summary 结构发现其没有 entries。
- **修复**: 按 hash 拉 `api.getAnonCollection(c.hash)` 取 `.entries`。

### R2-9. AnonCreator mime 字段名错误 — `src/api.js` + `CollBrowser.jsx` + `CollFileRow.jsx`

- **问题**: 后端 `RegisterURL` 返回 `{hash,size,mime,filename}`（`back/internal/controller/file.go:120`），字段是 `mime` 不是 `mime_type`；`RegisterLocalFile` 只返回 `{hash,filename}` 无 size/mime。前端读 `res.mime_type` → 类型图标/大小全错。`AnonCollectionEntry` 顶层也无 `mime_type`/`size`（在 `providers[].mime_type`）。
- **修复**: api.js `registerURL` 归一化补 `mime_type`/`size`；CollBrowser/CollFileRow 从 `entry.providers?.[0]?.mime_type` 派生。

### R2-10. AnonCreator 文件夹条目渲染空行 — `src/pages/AnonCreator/CollBrowser.jsx`

- **问题**: 编辑器里的文件夹条目以 `/` 结尾（`"dir/"`），浏览时 prefix 匹配后 `rel` 为空串 → 渲染一行无名文件。
- **修复**: `rel.endsWith('/')` 时剥离斜杠归入 dirs，跳过空名行。

### R2-11. AnonCreator onFileSelect 未接线 — `src/pages/AnonCreator/LeftPanel.jsx`

- **问题**: LeftPanel 收了 `onFileSelect` 但没传给 `CollBrowser` → 点击合集内文件不进入预览。
- **修复**: 转发给 `CollBrowser`。

### R2-12. AnonCreator 左侧默认 tab 错 — `src/pages/AnonCreator/index.jsx`

- **问题**: 默认 `leftSourceTab='all'`，但 `SOURCE_TABS` 只有 `local`/`collections`，'all' 时左栏什么都不渲染。
- **修复**: 默认改 `'local'`。

### R2-13. AnonCreator 搜索历史非数组崩溃 — `src/pages/AnonCreator/utils.js`

- **问题**: `loadSearchHistory` 若 localStorage 里是对象 JSON，返回非数组 → `SearchHistory` 组件 `.map` 崩溃。
- **发现背景**: 读 loadSearchHistory 时发现只包 try/catch 不校验数组。
- **修复**: `Array.isArray(v)` 校验并过滤非字符串项。

---

## 验证

```
cd front
npx vitest run   # 3 files / 26 tests 全部通过
npx vite build   # 74 modules，dist/assets/index-Q_yuX32y.js
```

## 修复文件清单

- `front/src/api.js` — H2, M1, R2-2, R2-3, R2-5, R2-9
- `front/src/components/Navbar.jsx` — H1
- `front/src/pages/FileManager.jsx` — H3, H4
- `front/src/components/ActiveConnPanel.jsx` — H5
- `front/src/pages/P2PPanel.jsx`, `front/src/pages/BTPanel.jsx` — M2
- `front/src/components/UserGroupPicker.jsx` — M3
- `front/src/components/CommentSection.jsx` — M4
- `front/src/components/P2PStatus.jsx` — R2-1
- `front/src/pages/Explorer.jsx` — R2-4
- `front/src/App.jsx` — R2-6
- `front/src/pages/AnonCreator/index.jsx` — R2-7, R2-8, R2-12
- `front/src/pages/AnonCreator/CollBrowser.jsx` — R2-9, R2-10
- `front/src/pages/AnonCreator/CollFileRow.jsx` — R2-9
- `front/src/pages/AnonCreator/LeftPanel.jsx` — R2-11
- `front/src/pages/AnonCreator/utils.js` — R2-13
