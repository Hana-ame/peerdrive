# Debug Round 3 — 测试、修复与前端体验优化

> 2026-04-29 · 基于 `archive/TODO.txt` 22 项需求的全模块回归

---

## 1. 概括 Prompt（本轮任务汇总）

| # | 任务 | 类型 | 结果 |
|---|------|------|------|
| 1 | `go test ./...` 零失败 | 测试 | ✅ 全部通过 |
| 2 | `npm run build` 零错误 | 编译 | ✅ 构建成功 |
| 3 | 前端 API 路由同步（`/p2p/bt/*` → `/bt/*`, `/p2p/ipfs/*` → `/ipfs/*`） | Bug | ✅ 修复 19 处引用 |
| 4 | 新建文件夹无法保存 | Bug | ✅ 修复后端验证 + 前端过滤 |
| 5 | MIME 多行显示 | Bug | ✅ Navbar 截断 |
| 6 | Git 术语翻译为网盘语言（"提交"→"保存"，"Fork"→"创建副本"） | UX | ✅ 10 处文本替换 |
| 7 | archive/TODO.txt 22 项需求状态审计 | 审计 | ✅ 18 已实现 + 3 修复 + 1 设计确认 |

---

## 2. 各模块描述

### 2.1 Storage（存储模块）

**职责**：文件存储、上传、注册、下载、合集逻辑、数据库。

- `service/file_service.go` — 文件服务（存储/URL 解析/MIME 嗅探）
- `repository/file_repo.go` — 文件元数据仓库（SQLite）
- `repository/db.go` — 数据库初始化 + 迁移（含 `providers_json` 列）
- `controller/file.go` — 文件管理 handlers（上传/注册/验证/删除/复制）
- `controller/download.go` — 下载 handlers（SHA256 / CID / 通用下载）

**本轮修复**：
- 文件夹条目（path 以 `/` 结尾, providers 为空）此前被 `isValidProviders()` 拒绝，导致新建文件夹无法保存。改为：目录条目跳过 providers 验证。

### 2.2 P2P 模块

**职责**：libp2p 主机、DHT、mDNS 发现、Relay、端口转发、WebSocket 传输。

- `service/p2p.go` — libp2p 主机 + DHT + 流处理（~904 行）
- `service/p2p_transfer.go` — 文件传输协议
- `service/p2p_resume.go` — 断点续传
- `service/p2p_multipeer.go` — 多源并行下载
- `controller/p2p.go` — P2P HTTP handlers（~1615 行）

**本轮修复**：
- 后端路由早已从 `/p2p/bt/*` 迁移到 `/bt/*`、`/p2p/ipfs/*` 迁移到 `/ipfs/*`，但前端 `api.js` 未同步更新，导致 BT/IPFS 全部功能在前端瘫痪。已修复。

### 2.3 BT 模块（BitTorrent）

**职责**：Mainline Kademlia DHT + 完整 BT 下载客户端。

- `p2p_bt/bt_dht.go` — Mainline DHT 节点
- `p2p_bt/bep44.go` — BEP44 不可变/可变数据存储
- `p2p_bt/bt_client.go` — BT 下载客户端（torrent/magnet）
- 路由组: `bt := r.Group("/bt")`

### 2.4 IPFS 模块

**职责**：IPFS 兼容层 — libp2p Kademlia DHT + Bitswap + CID 转换 + WebRTC 信令。

- `service/ipfs_compat.go` — IPFS Bitswap + CID/SHA256 转换
- `service/signaling.go` — WebRTC 信令 Hub
- 路由组: `ipfs := r.Group("/ipfs")`

### 2.5 Auth 模块

**职责**：用户认证、JWT 验证、节点注册、服务策略管理。

- `service/auth_service.go` — JWT 验证、注册服务器通信
- `router/auth_middleware.go` — Bearer token 中间件
- `controller/p2p.go#L1449-L1530` — Node operator / register handlers

### 2.6 Collections（合集系统）

**职责**：匿名合集 + 用户合集，Git 风格版本管理（commit / rollback / fork / merge / pull）。

- `service/anon_service.go` — 匿名合集 CRUD + 版本管理
- `repository/collection_repo.go` — 合集/版本条目持久化（含 `providers_json` 支持）
- `model/anon.go` — Provider 模型 + 旧格式兼容（`{path, hash}` 自动升级为 `{path, providers}`）
- `controller/anon.go` — 匿名合集 handlers
- `controller/collection.go` — 用户合集 handlers（版本日志/回滚/差异对比）

---

## 3. 设计回应（核心决策与修复记录）

### 3.1 前端路由同步（Bug #1 — 严重）

**问题**：后端 `router.go` 已定义：
```go
bt   := r.Group("/bt")     // BT 路由
ipfs := r.Group("/ipfs")   // IPFS 路由
```
但 `react/src/api.js` 仍使用：
```js
request('GET', '/p2p/bt/status')   // → 应为 /bt/status
request('GET', '/p2p/ipfs')        // → 应为 /ipfs
```

**影响**：BT 下载管理、IPFS pin/网关、DHT 查询全部 404。

**修复**：`api.js` 中 19 处引用全部替换（`/p2p/bt/` → `/bt/`，`/p2p/ipfs/` → `/ipfs/`）。`/p2p/auth/status` 保持不变（后端路由未变）。

### 3.2 新建文件夹（Bug #2 — 高）

**问题链**：
1. 前端 `AnonCreator.jsx` 通过 `addEntry('', 'folder/')` 创建目录条目，providers 为空 `[]`
2. 后端 `anon_service.go` 的 `isValidProviders()` 断言 `len(providers) == 0 → false`
3. 前端 `handleSave` 的 `valid` 过滤也排除了 `!e.hash && !e.providers?.[0]?.value` 的条目

**修复**：
- **后端**（`anon_service.go`）：`CreateCollection` 和 `CommitCollection` 两处验证循环中，对以 `/` 结尾的路径跳过 `isValidProviders` 检查
- **前端**（`AnonCreator.jsx`）：`valid` 过滤增加 `e.path.endsWith('/')` 条件；保存按钮 disabled 判断同步更新

```go
// anon_service.go — 修复后
if !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
    // ...
}
```
```js
// AnonCreator.jsx — 修复后
const valid = entries.filter(e => 
  e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value)
);
```

### 3.3 MIME 多行显示（Bug #3 — 低）

**问题**：`Navbar.jsx` 搜索结果直接拼接 `{f.mime_type}`，长 MIME（如 `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`）溢出行高。

**修复**：与 `FileManager.jsx` 对齐 — 取分号前的主类型，再取子类型，加 CSS 截断：
```jsx
{(f.mime_type || '').split(';')[0].split('/').pop() || '-'}
// + overflow-hidden text-ellipsis whitespace-nowrap
```

### 3.4 前端语言网盘化

**原则**：后端保持 Git 语义（commit/fork/rollback），前端翻译为用户语言。

| Git 术语 | 网盘语言 | 涉及文件 |
|----------|----------|----------|
| 提交 (Commit) | 保存 | Explorer.jsx |
| 提交信息 | 版本说明（可选） | Explorer.jsx |
| Fork | 创建副本 | CollectionCard / AnonCollectionManager / CollectionBuilder |
| 未提交的工作区 | 未保存的更改 | VersionLog.jsx |
| (fork) 后缀 | (副本) | AnonCreator.jsx |
| Fork 失败 | 创建副本失败 | CollectionBuilder / AnonCollectionManager |

### 3.5 archive/TODO.txt 22 项审计结论

- **18 项已实现**：合集搜索/排序/标签过滤/历史记录、Android 式导航、选择模式批量保存、LLM 命名提示、文件拖拽、移动到按钮、500ms 防双击、三种文件源标签（时间线/已注册/本机）、SHA256 隐藏/友好名称显示、保存到本机按钮
- **3 项已修复**：新建文件夹、MIME 显示、API 路由同步
- **1 项设计确认**：「提交」和「克隆并修改」→ 保留功能，前端术语网盘化

---

## 4. 验证结果

| 项目 | 命令 | 结果 |
|------|------|------|
| Go 测试 | `go test ./... -count=1` | 8/8 包通过 |
| React 编译 | `npm run build` | 零错误构建 |
| 前端旧路由残留 | `grep -rn "/p2p/bt\|/p2p/ipfs" react/src/` | 0 处 |
| Git 术语残留 | `grep -rn "提交\|Fork" react/src/` | 仅代码注释/变量名保留 |

---

## 5. 变更文件清单

```
# Bug 修复
react/src/api.js                              (+19 处路由修正)
go/internal/service/anon_service.go           (+2 处目录条目验证跳过)
react/src/pages/AnonCreator.jsx               (+3 处文件夹条目过滤)
react/src/components/Navbar.jsx               (+1 处 MIME 截断)

# UX 网盘化
react/src/components/CollectionCard.jsx       (📋 Fork → 📋 创建副本 ×2)
react/src/pages/Explorer.jsx                  (提交→保存, 版本说明)
react/src/components/VersionLog.jsx           (未提交→未保存)
react/src/components/AnonCollectionManager.jsx (Fork→创建副本 ×3)
react/src/components/CollectionBuilder.jsx    (Fork→创建副本 ×3)
react/src/pages/AnonCreator.jsx               ((fork)→(副本))
```
