# 前端 Review 修复报告 (2026-08-16)

> 审阅时间: 2026-08-16
> 背景: 用户要求 review `/mnt/d/Workplace/peerdrive/front` 前端代码并修复 bug
> (该目录当时**不是 git 仓库**, 改动不可回滚)。
> 结论: 修复 **5 处 HIGH(运行崩溃)** + **4 处 MEDIUM(链接/字段/重复请求)**, 共 9 个问题。
> 所有修复均对照后端实现核实(`back/internal/controller/*.go`)。
> 验证: `npx vite build` 成功, `npx vitest run` 3 文件 / 26 用例全过。

---

## 高严重度 (会崩溃 / 功能不可用)

### H1. Navbar SearchPanel — `results.files` undefined 崩溃 — `front/src/components/Navbar.jsx`

SearchPanel 展开后按任意键 → keydown 处理器里 `[...results.anon, ...results.public, ...results.files]`:
`results.files` 是 undefined(搜索结果 API 从不返回该字段), 展开运算符抛 TypeError。

危险: `src/App.jsx` **没有 error boundary**, 未捕获错误会让整个 React 树卸载 → 白屏。

修法:
- keydown 的 `all` 数组去掉 `...results.files`
- 删除引用未定义变量 `hasFiles` 的死代码块(该变量从未声明, 条件恒为 ReferenceError)
- catch 块统一 `setResults({ anon: [], public: [] })`(原含 `files: []` 与死代码)

### H2. `uploadConsent()` 返回 undefined — `front/src/api.js` + `front/src/pages/Settings.jsx:407`

`uploadConsent` 函数体只 `return request(...)` 的上一级被漏写, 实际返回 `undefined`。
Settings 里 `api.uploadConsent().then(...)` 对 undefined 调 `.then` → **同步 TypeError**,
上传同意开关完全失效 + spinner 卡死。

修法: 改为 `async` 函数返回请求 Promise(`return request(...)`)。

### H3. FileManager `handleShare` 未定义 — `front/src/pages/FileManager.jsx`

按钮 onClick 引用 `handleShare` 但组件内从未实现 → 点击即 ReferenceError(整页崩)。

修法: 实现 `handleShare(file, e)`, 复制 `api.getDownloadUrl(file.hash)` 到剪贴板
(`navigator.clipboard` + `document.execCommand('copy')` 兜底), 成功后 `setNotification` 提示。

### H4. FileManager `useMemo` 在渲染函数内 — `front/src/pages/FileManager.jsx`

`useMemo(() => getDbDirContents(...))` 写在 `renderDbDirBrowser()` 里 → Rules of Hooks 违规
(条件渲染导致 "Rendered more hooks than during the previous render", 切目录时崩溃)。

修法: 提升到组件顶层 `const dbDirContents = useMemo(...)`, 渲染函数直接引用。

### H5. ActiveConnPanel `setPeers` 塞进包装对象 — `front/src/components/ActiveConnPanel.jsx`

`GET /p2p/peers` 后端返回 `{"peers": [...str]}`(见 `back/internal/controller/p2p.go:118 GetPeers`),
前端 `setPeers(p || [])` 把整个对象存进 `peers`, 随后 `peers.filter(...)`(第 116 行)→
`peers.filter is not a function` 崩溃。

修法: `setPeers(Array.isArray(p) ? p : (p?.peers || []))`, 兼容裸数组/包装对象。

---

## 中严重度 (链接/字段错误/重复请求)

### M1. 下载 URL 未做路径编码 — `front/src/api.js`

`getAnonFileDownloadUrl(hash, p)` / `getUserFileDownloadUrl(username, coll, filepath)`
裸拼路径 → 文件名含空格/`#`/`?` 时 URL 语义被破坏(下载 404/错误文件)。

修法: 新增 `const encodePath = (p) => (p || '').split('/').map(encodeURIComponent).join('/')`,
hash/username/coll 也单独 `encodeURIComponent`。

**为什么不能连 `/` 一起编码**: gin 的 `*filepath` wildcard 匹配的是**已解码**的 `c.Request.URL.Path`
(`back/internal/router/collection_dispatch.go` dispatchGetTree、`back/internal/controller/anon.go:103`
DownloadAnonFile)。逐段编码保留 `/` 分隔符, 服务端 `strings.TrimPrefix(c.Param("filepath"), "/")`
与 `coll.Entries[i].Path` 比对仍正确; 若把 `/` 编成 `%2F`, 会在路由匹配前被解码破坏 wildcard 分段。

### M2. BT 节点数读了不存在的字段 — `front/src/pages/P2PPanel.jsx` + `front/src/pages/BTPanel.jsx`

显示 `btStatus?.node_count`, 后端实际返回 `num_nodes`(`back/internal/controller/p2p.go:471`)
→ 节点数永远显示 `-`。

修法: 三处 `node_count` → `num_nodes`(P2PPanel 151/186/256 行, BTPanel 125 行)。

### M3. UserGroupPicker 空字符串崩溃 — `front/src/components/UserGroupPicker.jsx`

头像 `{(u.nickname || u.username)[0].toUpperCase()}`: nickname/username 都为空时
`''[0]` 是 `undefined`, 调 `.toUpperCase()` 崩溃。

修法: 兜底 `((u.nickname || u.username || '?')[0] || '?').toUpperCase()`。

### M4. CommentSection 重复拉取 — `front/src/components/CommentSection.jsx`

两个 effect(`[regServerUrl, hash]` 与 `[hash]`)在挂载/换 hash 时**各触发一次** `fetchComments`
→ 同一合集加载两遍评论, 第二个 effect 还会清空刚加载的列表。

修法: 合并为单个 effect, 依赖 `[regServerUrl, hash]`, 内部分支判断。

---

## 有意保留 (非 bug)

- AnonExplorer 空合集自动 `api.deleteFile(hash)`: 后端 `DeleteFile`
  (`back/internal/controller/file.go`) 校验 64 位 hex SHA256, 属设计意图。
- 前端大量 legacy 组件(P2PPanel/BTPanel/DHTExplorer 等旧 libp2p/BT 面板):
  后端已迁移 PeerJS, 前端尚无 peerjs 客户端。REFACTOR.md 标注约 4000 行死代码, 清理是独立任务。

---

## 验证

```
cd /mnt/d/Workplace/peerdrive/front
npx vite build    # ✅ 74 modules, dist/assets/index-Q_yuX32y.js
npx vitest run    # ✅ 3 文件 / 26 用例全过
```

## 涉及文件

| 文件 | 修复 |
|---|---|
| `front/src/components/Navbar.jsx` | H1 |
| `front/src/api.js` | H2, M1 |
| `front/src/pages/Settings.jsx` | H2 (间接, 经 api.js) |
| `front/src/pages/FileManager.jsx` | H3, H4 |
| `front/src/components/ActiveConnPanel.jsx` | H5 |
| `front/src/pages/P2PPanel.jsx`, `front/src/pages/BTPanel.jsx` | M2 |
| `front/src/components/UserGroupPicker.jsx` | M3 |
| `front/src/components/CommentSection.jsx` | M4 |

> 后端对照依据: `back/internal/controller/p2p.go`(GetPeers:118 / BT status:471)、
> `back/internal/router/collection_dispatch.go`(dispatchGetTree wildcard 解码)、
> `back/internal/controller/anon.go`(DownloadAnonFile)、`back/internal/controller/file.go`(DeleteFile)。