# 架构简化报告

> 2026-05-03T23:50:00 · 全部改动总结

---

## 一、改动总览

### 核心理念

```
Collection = name + entries[]
Entry     = path + providers[]
Provider  = { type: "sha256"|"url", value: hash|url }
```

剥离独立的"文件"概念，一切皆合集+provider。

### 改动清单

#### 后端路由 (router.go)

| 旧路径 | 新路径 | 操作 |
|--------|--------|------|
| `/anon/collections` | → `/collections` | 新增统一路由 + 301 redirect |
| `/anon/collections/:hash` | → `/collections/:hash` | 同上 |
| `/anon/collections/:hash/*` | → `/collections/:hash/*` | 同上 |
| `/files` | → `/collections` | 301 redirect |
| `/actions/fork\|merge\|pull` | → `/collections/fork\|merge\|pull` | 新增统一路由 + 301 redirect |
| `/files/upload` | → `/collections/upload` | 新增别名（同一 handler）|
| `/files/register_local` | → `/collections/register-local` | 新增别名 |
| `/files/register_url` | → `/collections/register-url` | 新增别名 |
| `/files/register_folder` | → `/collections/register-folder` | 新增别名 |
| `/download/:hash` | 参数改为 `:sha256`，指向 `DownloadBySHA256` | 已修复 |
| `/download/:hash` | 改回 `:hash`，指向 `UniversalDownload` | 最终方案 |
| `/sha256sum/:sha256/:filename` | 新增 | 保留带文件名的下载路径 |
| `/c/:hash` | 新增 | 统一合集查看路由 |

#### 下载端点区分

| 端点 | 行为 |
|------|------|
| `/sha256sum/:sha256` | **仅本地存储**，无 P2P/BT/IPFS 回退。新增 `:filename` 可选参数 |
| `/download/:hash` | **多协议回退**（P2P → BT → IPFS → 本地），带 `X-Protocol` 头 |

#### 模型 (model/anon.go)

| 类型 | 操作 |
|------|------|
| `AnonEntry {path, hash, url}` | ✅ **删除** — 已被 `AnonCollectionEntry {path, providers[]}` 取代 |
| `AnonCollectionEntry {path, providers[]}` | 保留 — 当前标准格式 |

#### 前端路由 (App.jsx)

| 旧路径 | 新路径 | 操作 |
|--------|--------|------|
| `/files` | → `/` | 重定向，FileManager 页面删除 |
| `/anon/collections/:hash` | 保留 + 新增 `/c/:hash` | 统一集合查看 |
| `/:username/:collName` | 自动检测 64 位 hex | 若 username 或 collName 是 hash，重定向到 `/c/:hash` |

#### 前端 API (api.js)

| 函数 | 旧端点 | 新端点 |
|------|--------|--------|
| `registerLocalFile` | `/files/register_local` | → `/collections/register-local` |
| `registerURL` | `/files/register_url` | → `/collections/register-url` |
| `registerFolder` | `/files/register_folder` | → `/collections/register-folder` |
| `createAnonCollection` | `/anon/collections` | → `/collections` |
| `getAnonCollection` | `/anon/collections/:hash` | → `/collections/:hash` |
| `getAnonFileDownloadUrl` | `/anon/collections/:hash/:p` | → `/collections/:hash/:p` |
| `createCollection` (统一) | `/anon/collections` | → `/collections` |
| `listCollections` (统一) | `/anon/collections` | → `/collections` |
| `getCollection` (统一) | `/anon/collections/:id` | → `/collections/:id` |

#### 前端组件

| 组件 | 操作 |
|------|------|
| `CollectionBuilder.jsx` | ✅ **删除**（未使用） |
| `AnonCollectionManager.jsx` | ✅ **删除**（未使用） |
| `FileManager.jsx` | 页面删除，路由改为 `/` 重定向 |
| Navbar "本地文件管理" | 改为"合集" |
| Navbar 搜索面板 | 移除"注册文件"分类 |
| AnonCreator LEFT_SORT_OPTS/LEFT_TYPE_FILTERS | 删除（对应已删的 tab）|
| AnonCreator SOURCE_TABS | 4 tab → 2 tab（本地电脑/合集）|
| 合集标签筛选 | 单选 → 复选 |

#### 向后兼容

所有旧路径通过 HTTP 301/308 redirect 保持兼容。旧 API 函数保留为 pass-through。
