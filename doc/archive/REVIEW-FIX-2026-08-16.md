# Review Fix 2026-08-16

本文记录本轮充分 review 期间发现并修复的问题（前端 + 后端）。每个修复都带「为什么」。

## 前端

### 1. AnonExplorer 重复渲染评论区块
- **问题**：同一合集在根目录时 `<CommentSection>` 同时出现在文件列表上方和页面底部，两个组件共享同一接口，会导致重复评论框/重复请求。
- **修复**：删除文件列表上方的副本，只保留底部评论区。

### 2. 数据同意开关是“假上传”
- **问题**：`api.uploadConsent()` 只写 `localStorage`，后端没有 `/consent` 端点；设置页却提示“已上传同意记录”，且文案说会上传统计到注册服务器。
- **修复**：改名为 `saveConsentLocal()`，设置页提示改为“已保存同意记录（本地）”，并同步修正说明文案；旧名称保留为兼容别名。

### 3. WS 传输地址在模块加载时固化
- **问题**：`api.WS_TRANSFER_URL` 是模块加载时用当时的 `getApiBase()` 拼出来的快照，切换后端后仍指向旧地址。
- **修复**：新增 `getWSTransferURL()` 运行时计算；`P2PStatus` 改用新函数，旧常量保留兼容但注明不刷新。

### 4. 缺少全局错误边界 / 未知路由兜底
- **问题**：单个页面渲染异常会导致整个 React 树白屏；未知 URL 也没有重定向。
- **修复**：`App.jsx` 增加 `AppErrorBoundary`，并添加 `path="*"` 重定向回首页。

### 5. AnonExplorer 空路径/缺失字段防御
- **问题**：`entries[0].path` 可能为空时直接 `.includes/.split` 会抛异常；`FileList` 也有同样风险。
- **修复**：统一使用 `(e.path || '')`，并提供安全显示名回退。

### 6. 匿名合集 MIME 取错层级
- **问题**：后端 `AnonCollectionEntry` JSON 的 MIME 在 `providers[].mime_type`，部分前端组件只读顶层 `entry.mime_type`，导致图标/预览类型判断失效。
- **修复**：`FileRow` / `SingleFilePreview` 增加 `entry.providers?.[0]?.mime_type` 派生。

### 7. FileTree 死代码清理
- **问题**：`showNewFolder`、`newFolderName`、`newFolderRef`、`submitNewFolder` 已被内联新建文件夹 UI 取代但从未删除。
- **修复**：删除无用状态/函数。

## 后端

### 1. BrowseDir 默认根路径与前端不一致
- **问题**：`BrowseDir` 空路径默认使用系统 `/`，在安全边界（storage root 白名单）下永远 400；而前端文件管理器/创建页的本地浏览均以 `/` 表示 storage 根。
- **修复**：空路径或 `/` 都映射为 `.`（storage 根目录），并新增回归测试 `TestBrowseDir_SlashMeansStorageRoot`。

### 2. 测试 `TestBrowseDir_SpecificPath` 与安全边界矛盾
- **问题**：该测试以前创建了一个独立 temp dir 并当作可浏览路径，但安全修复后只允许 storage 根内路径，测试会失败。
- **修复**：让 `setupFileTestRouter` 返回 storage 根，测试在该根内创建文件再浏览。

### 3. PeerJS 文件信息路径脱敏不完整
- **问题**：`serveFile` 已对根外路径回退 CAS，但 `list/info/sync` 仍会把历史脏数据/恶意登记的根外绝对路径返回给对端。
- **修复**：新增 `redactDisallowedPath`，在 `list/info/sync` 响应中把根外 `Path` 置空。

### 4. 空文件无法从 PeerJS 对端拉取
- **问题**：`hashMatchesSHA256` 对 `len(data)==0` 直接返回 false，导致 sha256(空) 的合法空文件永远校验失败。
- **修复**：移除空数据特判，并新增 `TestHashMatchesSHA256_AllowsEmptyFile`。

### 5. 信令服务器写入统一走互斥锁
- **问题**：`HandleWS` 的 OPEN 帧直接用 `conn.WriteJSON`，与后续队列补发 `cl.send` 不是同一把锁，gorilla/websocket 在并发写时可能 panic（曾偶现 `concurrent write`）。
- **修复**：OPEN 也走 `cl.send`，readLoop 清理统一走 `cl.closeConn()`。

### 6. 迁移遗留测试文件归位
- **问题**：`internal/service` 中 `p2p_test.go`、`universal_downloader_test.go` 仍引用已迁到 `internal/legacy` 的符号，导致 `go test ./internal/...` 编译失败。
- **修复**：把两个测试移到 `internal/legacy` 并改包名。

---

## 第二轮：前端活跃代码 bug 修复（2026-08-16）

针对活跃主链路（AnonExplorer/AnonCreator/Explorer/Plaza/FileTree）的专项排查修复。

### 高严重度

1. **AnonExplorer 竞态：陈旧响应覆盖当前合集**
   - 切 hash 时旧请求慢响应会覆盖新合集内容。修复：`fetchSeqRef` 序号守卫，序号不匹配的响应直接丢弃（`AnonExplorer/index.jsx`）。

2. **重命名/移动文件夹产生孤儿子条目**
   - 旧 `renameEntry` 只改精确匹配条目；文件夹改名后子条目路径不变、浮到根部。修复：`renameEntry` 支持目录前缀级联；新增 `moveEntry`（目录连带子条目、防拖进自己子目录），FileTree 树内拖拽统一走 `onMove`（移动语义去源），外部面板拖拽才走 `onDrop`（复制语义）；文件夹拖动补写 `application/peerdrive-entry` 数据（旧只有无人读的 `peerdrive-path`）。

3. **合并弹窗匿名合集无法作为合并源**
   - 选项 value 是 hash 而匹配键是 "user/coll" → 永远匹配不到、静默失败。修复：匹配键支持 hash；后端无匿名合并端点，前端用 `addCollectionEntry` 逐条实现匿名合并，语义对齐后端 strategy（ours/theirs/manual）。

4. **合并 manual 策略 409 无冲突 UI**
   - 后端 409 返回 `{conflicts}`，旧实现只 alert。修复：`api.js` 把 `status/data` 挂到 Error；Explorer 弹窗内展示冲突清单并支持「保留本地/采用远端」重试。

5. **Plaza 天线 tab 是死胡同**
   - BEP51 采样的是 torrent DHT 的 20 字节 infohash，永远取不到 peerdrive 合集 announce 的 256 位 hash → 打开的闭环不可达。修复：删除天线 tab 及轮询；同步删除 AnonExplorer「📡 广播」按钮（广播端无读取端）。

6. **查看空合集触发删除副作用**
   - GET 流程里 `deleteFile(h)` 会真实删除存储，401 被吞后提示语与事实不符。修复：只展示错误提示，不自动删除。

### 中严重度

7. `TextPreview` 加 `res.ok` 校验（错误体不再当文本展示）
8. `FileRow` 移除误导性的合集创建时间戳（anon 条目无逐文件时间）
9. `Explorer` 两个「保存」按钮语义区分：头部改名「同步到本地」、commit 栏改名「提交版本」
10. Settings 双 IPFS 开关（localStorage 假开关 vs 后端真开关共享同一 state）→ 删除假开关区
11. `LLMAssistant` 每轮清空对话 → 携带最近 20 条历史，UI 追加而非替换
12. `Navbar` 搜索结果空时 activeIdx 置 -1 → 钳到 0
13. AnonExplorer 返回按钮 `navigate(-1)` 无历史时直接离开 SPA → 历史不足回首页
14. `CommentSection` 拉评论无竞态保护 → seq 守卫（同 AnonExplorer 模式）
15. Settings 自定义连接后 `activeBackendId=null` → 改为落成正式后端（同 URL 复用 / 新建「自定义」）

---

## 追加：再 review 2026-08-19（Peerdrive 本地 WS 管理面与前端展示修正）

### 后端
1. **admin 二进制上传丢失 token**
   - 上传声明帧带 `token`，但 `adminUploadState` 未保存，内部 multipart 请求用空 token → 认证开启时上传 401。修复：状态保存 `ar.Token`，完成阶段回填；新增 `TestAdminBinaryUploadToken`。

2. **admin 二进制上传拒绝空文件**
   - 初版 `size <= 0` 直接拒绝；空文件声明 size=0 后没有二进制帧，永远收不齐。修复：允许 `size == 0`，立即从单槽摘除并触发 multipart 转发；新增 `TestAdminBinaryUploadEmpty`。

### 前端
3. **WS `admin-bin` 空响应残留 binaryExpect**
   - `admin-bin size=0` 没有清单槽，后续无关二进制帧会被误归给已完成请求。修复：空响应立即 finish + 清 expect；新增 ws 测试。

4. **预览 objectURL 泄漏**
   - `SingleFilePreview` 切换合集时只处理 cancel 不 revoke，Blob 内存只增不减。修复：objectURL 随 effect cleanup / 卸载 revoke。

5. **Settings 节点状态字段仍按 libp2p 旧契约**
   - `getNodeInfo` 已删，改用 `getPeerjsNode` 后 UI 仍读 `peer_id/p2p_enabled/relay_mode/num_peers` → 全部空白。修复：按新契约显示 `id/online/peers`。

6. **匿名合并把 URL-only 当 hash 提交**
   - `mergeAnonIntoLocal` 用 `providers[0].value` 取主 hash，若 URL provider 在前会把 URL 当 sha256 提交给后端 → 校验失败。修复：只取 `type === 'sha256'` 的 provider，URL-only 跳过。

7. **公开合集无法从广场创建副本**
   - `/collections/public` 列表只有 `current_hash` 没有 `hash`，`handleFork` 只认 hash → 副本按钮无反应。修复：回退 `current_hash`；AnonCreator fork 也用 `sourceHash || hash || current_hash` 拉全量。

8. **`createCollection` 别名匿名创建丢名字**
   - 统一别名仍发 `name`，后端匿名分派读 `friendly_name` → 匿名合集名丢失。修复：别名改发 `friendly_name`。

9. **数据同意记录写孤儿键**
   - `saveConsentLocal` 写 `peerdrive_consent`，而设置页生效键是 `peerdrive_data_consent` → “已写但读不到”。修复：委托 `setDataConsent(true)` 统一键。

10. **匿名合集 URL-only 条目下载到 HTML 错误页**
    - 后端对 URL-only 返回 302，WS admin 内部转发不跟随跳转，前端会保存 302 页面。修复：FileRow / SingleFilePreview 检测无 sha256 时直接渲染外部链接。
