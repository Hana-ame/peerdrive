# 修改报告 — 2026-05-01 03:17:18 CST

## 修改内容

### 1. Navbar 重构：一级导航扁平化

**修改文件**: `front/src/components/Navbar.jsx`

**变更**:
- 原有 4 个一级项（文件管理 / 探索合集 / 创建合集 / P2P网络(含BT+IPFS子项)）改为 6 个一级项：
  - **本地文件管理** (`/files`) — 原"文件管理"，重命名
  - **创建合集** (`/create`) — 原 `/anon/create`
  - **P2P 网络** (`/p2p`) — 下拉：网络总览 / 网络拓扑 / DHT 查询
  - **BT** (`/bt`) — 独立一级，下拉：BT 下载器 / BT DHT
  - **IPFS** (`/ipfs`) — 独立一级，平链
  - **设置** (`/settings`) — 不变
- 移除"探索合集"按钮（品牌 logo 已指向 `/`）

### 2. 路由 Path 调整

**修改文件**: 9 个文件

**变更**:
- `/anon/create` → `/create`（App.jsx 路由 + 8 个文件中的 14 处引用）
- DHT 页面路由从 `/p2p/dht` 调整为 `/bt/dht`（App.jsx）
- 新增 `/ipfs/dht` 映射

### 3. 文档

**新增文件**: `doc/API-REFERENCE.md` — 90+ 端点完整 API 参考，按领域分 10 章，含前端页面→API 对照表

### 4. Memory 系统

**新增/更新文件**:
- `feedback_testing.md` — 更新为完整开发修改工作流（测试→回归→报告→commit→push）
- `project_overview.md` — 项目结构、启动命令、测试命令速查
- `frontend_conventions.md` — 组件规范、API 调用方式、路由架构

## 修改预期

| 预期 | 结果 |
|------|------|
| 一级导航 6 项扁平排列，BT/IPFS 不再藏在 P2P 下拉里 | 达成 |
| `/anon/create` → `/create` 全局无遗漏 | 达成（grep 验证零残留） |
| 全量测试通过 | 达成（20 passed） |
| DHT 页面可从 BT 菜单和 IPFS 菜单各自访问 | 达成 |

## 测试结果

```
frontend Vitest: Tests 20 passed (20)
grep '/anon/create': 零残留
```

## 提交记录

```
d5159ca refactor: Navbar 重构 — BT/IPFS 独立一级菜单，DHT 查询归入 BT
aa07e50 docs: 添加后端 API 参考文档，面向前端开发
a878c40 refactor: Navbar 一级导航扁平化，路径 /anon/create → /create
```

## 当前路由表

| Path | 组件 |
|------|------|
| `/` | Plaza |
| `/files` | FileManager |
| `/create` | AnonCreator |
| `/:username/:collName` | Explorer |
| `/anon/collections/:hash` | AnonExplorer |
| `/anon` | AnonExplorer |
| `/p2p` | P2PPanel |
| `/p2p/dashboard` | P2PDashboard |
| `/p2p/topology` | P2PTopology |
| `/ipfs` | IPFSPanel |
| `/ipfs/dht` | DHTExplorer |
| `/bt` | BTPanel |
| `/bt/controller` | BTController |
| `/bt/dht` | DHTExplorer |
| `/settings` | Settings |
