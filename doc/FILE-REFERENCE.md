# Peerdrive 项目文件清单

> 按当前实现生成。生成时间：**2026-10-06**（分支 `refactor`，对应 v0.3.2）。
> 规模数字是脚本实测的 `.go` 非测试文件数与行数，不是估计。

> ⚠️ **重写说明**：上一版是 **2026-04-30 的快照**，此后经历过单二进制合并（v0.3.0）、
> 注册服务并入、注册中心仓库归档（v0.3.1）、面板内嵌（v0.3.2）等多轮重构。
> 实测上一版列出的 151 个路径**已不存在**，例如 `back/cmd/server/`、`back/internal/p2p_bt/`、
> `back/test/*.sh` 一整套脚本、`front/src/components/` 整个目录
> （前端已改为 `front/src/pages/` 下的 8 个页面）。逐条修补 151 处既容易改错，
> 也不如按现状重建可靠，故重新生成。
> 需要了解**重构过程**（而非现状）请看 `doc/REFACTOR.md`；了解**旧结构**看 `doc/archive/`。

---

## 根目录

| 文件 / 目录 | 说明 |
|------|------|
| `README.md` | 项目总览 · 快速开始 · 端口 |
| `AGENTS.md` | 给 agent 的工作约定 |
| `doc/` | 全部文档（本文件所在处） |
| `back/` | Go 后端（主模块） |
| `front/` | React 节点管理员界面（Vite + TailwindCSS + Vitest） |
| `packages/peerdrive-client/` | 公共网盘面板（零依赖，内联单文件产物） |
| `packages/peerdrive-media/` | 媒体节点 |
| `scripts/` | 仓库级脚本（`netdisk-local-demo.sh` 全链路等） |

---

## back/ — Go 后端

### 入口与独立模块

| 路径 | 说明 |
|------|------|
| `back/cmd/peerdrive/` | **唯一入口**。子命令 `demo` / `serve` / `signal` / `reg` / `all` |
| `back/internal/serverapp/` | 启动装配 + HTTP 引导（原 `cmd/server`）。同时打印「下一步做什么」与 `/panel` 地址 |
| `back/internal/services/` | 信令 + 注册服务的 in-process handler 装配（`SignalHandler` / `UnifiedMux`） |
| `back/peerjs/` | PeerJS 客户端库（信令客户端 / DataConnection / 帧协议），独立目录，非 `internal` |
| `back/p2p_bt/` | BT DHT，**独立 go.mod**（`back/internal/p2p_bt/` 不存在） |
| `back/ech/` | 媒体节点 |

### back/internal/ 各包

行数为非测试 `.go` 文件实测值。

| 包 | 文件数 | 行数 | 职责 |
|------|------|------|------|
| `transport/` | 14 | 4695 | WebRTC 帧协议、会话管理、`ShareGate` 门禁 |
| `service/` | 9 | 3843 | 业务逻辑：文件登记 / 下载 / 集合 / P2P / 节点市场 |
| `controller/` | 15 | 3668 | HTTP handler 层 |
| `source/` | 8 | 1453 | 数据源：`local` / `peer`（按 hash 取内容） |
| `router/` | 6 | 1488 | Gin 路由与中间件（含 `/panel`、CSP 豁免） |
| `repository/` | 10 | 1387 | SQLite 持久化 |
| `pathutil/` | 12 | 1088 | 路径作用域校验（`os.Root` / 降级模式统一） |
| `downloader/` | 1 | 453 | 通用下载器（`local → ipfs → ipfsgw → btdht → http`） |
| `model/` | 8 | 603 | 数据模型 |
| `regserver/` | 3 | 557 | 注册 / 认证 / 中继登记（原独立仓，已并入） |
| `config/` | 1 | 351 | 配置加载（**端口读 `PORT`，没有 `PEERDRIVE_PORT`**） |
| `services/` | 1 | 285 | 信令 + 注册 mux 装配 |
| `nodestate/` | 1 | 107 | 进程级状态（无 init / 无 Close） |
| `panel/` | 1 | 115 | **v0.3.2 新增**：`go:embed` 面板 + peerjs，`/panel` 零输入自发现 |
| `log/` | 1 | 80 | 日志封装 |
| `version/` | 1 | 16 | 版本号（由 `-ldflags -X` 注入） |
| `provider/` | 1 | 223 | 数据源抽象接口 |

### back/test/

| 路径 | 说明 |
|------|------|
| `back/test/integration/` | 集成测试（build tag `integration`，**必须 `-p 1` 串行**） |

> ⚠️ 旧版列出的 `back/test/*.sh`（`e2e-all.sh` / `p2p.sh` / `upload.sh` / `relay.sh` 等
> 二十余个脚本）**已全部删除**。等价的全链路验证现在是 `peerdrive demo`
> 或 `scripts/netdisk-local-demo.sh`。

---

## front/ — 节点管理员界面

| 路径 | 说明 |
|------|------|
| `front/src/pages/` | 8 个页面：`Connect` `Drive` `Collections` `Transfers` `NodeControl` `BT` `IPFS` `Settings` |
| `front/src/lib/` | `nodeSession.js` / `swBridge.js` / `PeerJSConnect.jsx` / `pd-client/` |
| `front/src/api.js` | API 客户端，**第一行是后端地址**（本地开发改 `http://localhost:3000`） |
| `front/src/ws.js` | WebSocket 客户端 |
| `front/tests/` | `api.test.js` / `ws.test.js` / `smoke.test.jsx` / `e2e-admin-smoke.mjs` |

> ⚠️ 旧版列出的 `front/src/components/`（18 个组件）与 `front/src/pages/AnonCreator/`、
> `AnonExplorer/` 等页面**均已不存在** —— 前端在 v3 重构中改为上表这 8 个页面。

---

## packages/

| 路径 | 说明 |
|------|------|
| `packages/peerdrive-client/dist/panel.html` | 内联单文件面板产物（CI 用 `npm run check:panel` 校验与 `src/` 一致） |
| `packages/peerdrive-client/dist/peerjs.min.js` | 随二进制内嵌的 peerjs（CI 校验与 `back/internal/panel/` 副本**逐字节一致**） |
| `packages/peerdrive-client/scripts/` | 面板自检：`verify-panel.mjs`（常规）/ `verify-panel-zero-config.mjs`（零输入，v0.3.2）/ `verify-panel-share.mjs` |
| `packages/peerdrive-client/src/` | 面板源码（`build:panel` 由它生成 `dist/panel.html`） |

---

## 文档地图

| 我想… | 看 |
|------|------|
| 第一次用，零配置 | `doc/guide/single-binary-guide.md`（中文，推荐） |
| 分步教程 | `doc/tutorial/01-run-and-connect.md` |
| 改代码 | `doc/guide/operation-manual.md` |
| 了解网盘链路 / 踩坑 | `doc/NETDISK.md` |
| 了解重构过程 | `doc/REFACTOR.md` |
| 了解分层设计 | `doc/design/` · `doc/layers/` · `doc/LAYERS.md` |
| 测试怎么跑、覆盖了多少 | `doc/testing/README.md` |
