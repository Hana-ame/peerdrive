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

## 验证

```
cd front
npx vitest run   # 3 files / 26 tests 全部通过
npx vite build   # 74 modules，dist/assets/index-Q_yuX32y.js
```

## 修复文件清单

- `front/src/api.js` — H2, M1
- `front/src/components/Navbar.jsx` — H1
- `front/src/pages/FileManager.jsx` — H3, H4
- `front/src/components/ActiveConnPanel.jsx` — H5
- `front/src/pages/P2PPanel.jsx`, `front/src/pages/BTPanel.jsx` — M2
- `front/src/components/UserGroupPicker.jsx` — M3
- `front/src/components/CommentSection.jsx` — M4
