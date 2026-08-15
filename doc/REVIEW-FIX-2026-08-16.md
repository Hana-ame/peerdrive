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
