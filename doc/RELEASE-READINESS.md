# 发布就绪度（Release Readiness）—— v1.0

> 本文档是 v1.0 发布评估的汇总页，按 2026-10-16 固化的「v1.0 发布规范与核心不变量」
> 维护。细节实现与坑见 `doc/REFACTOR.md`，测试清单唯一出处见 `doc/testing/README.md`。

## 三条核心不变量

1. **单一依赖源（SSOT）**：JS 端协议帧解析（兼容 `{type,reqId}` 与 `{event,req_id,code,data}` +
   32B 二进制头 `PD02`）、增量 SHA-256、自包含唯一 URL 生成与解析、多通道顺序无关传输队列
   （`TransportQueue`）与实时进度追踪（`TransferProgressHub`）统一维护于
   `packages/peerdrive-core`，由 `peerdrive-client`、`peerdrive-media` 与 `front` 直接引用或重导出。
2. **自包含唯一内容 URL**：任何 SHA256 文件均可通过 `/f/<sha256>`（后端）或 `#/f/<sha256>?peer=<peerId>`
   （前端）直接唯一定位访问；任何 Collection 均可通过 `/c/<hash>`（后端）或 `#/c/<hash>?peer=<peerId>`
   （前端）直接唯一定位访问。URL 生成/解析在 `peerdrive-core/src/url.js`（`buildFileUrl` /
   `buildCollectionUrl` / `parseDirectTarget`），测试见 `packages/peerdrive-core/test/core.test.mjs`。
3. **PeerJS 优先 + WS/HTTP 队列兼容 + 实时进度条 + 顺序无关保证**：传输队列与进度中枢在
   `peerdrive-core/src/queue.js`（`TransportQueue` / `TransferProgressHub` / `AsyncQueueLane`），
   front 的面板与 Transfers 页消费同一对单例（`globalProgressHub` / `globalTransportQueue`）。

## 已知缺口（2026-10-17 主干合入时记录）

- **不变量 #2 的后端半被临时切除**：`/f/:sha256` 与 `/c/:hash` 的 HTTP handler
  （`DirectFileBySHA256` / `DirectCollectionByHash`）从未在 v1.0-rc 源快照中出现，
  主干上对应路由已移除（记录见 `proj-peerdrive-v1.0rc-merge-20261017`）。前端 `#/f/` SPA 路由、
  Drive「复制文件链接」与 `/f/` `/c/` HTTP 兜底一并移除；`#/c/` SPA 路由保留。
  该特性后续按「Issue → feature 分支 → 实现 → 评审 → 合并」补回。
- 公共面板（`packages/peerdrive-client/panel`）的传输浮层 `TransferDock` 组件同样未在快照中，
  主干上已从 App 壳移除（传输进度仍可在 Transfers 页查看）。

## 分模块测试清单（入口）

| 组件 | 命令（入口） | 规模 |
|---|---|---|
| back（主模块） | `go vet` + `go test -tags nosqlite ./...` + 构建 `./cmd/peerdrive/` | ci.yml backend / go-build.yml 5 平台矩阵 |
| back 独立模块 | `back/p2p_bt`、`back/signalserver` 各自 vet/test/build（CGO_ENABLED=0） | go-build.yml submodules |
| integration | `go test -tags "nosqlite integration" ./test/integration/ -p 1` | ci.yml integration（脱外网） |
| peerjs | `back/peerjs` 独立 go.mod 全量 | ci.yml peerjs |
| peerdrive-core | `node --test`（core.test.mjs：协议/URL/队列/SHA-256） | ci.yml client-package |
| peerdrive-client | `npm test`（61 用例，含面板契约面板契约 panel-contract） + `npm run build:panel`（check:panel 拦漂移） | ci.yml client-package |
| peerdrive-media | `npm run build` + `npm test`（21）+ 浏览器 E2E（本机 Firefox） | ci.yml media-package + 手动 |
| front | `npm test`（vitest） + `npm run build` | ci.yml frontend |
| 端到端 | `.github/workflows/e2e.yml`：真起信令 + 两节点 8 项断言 + bundled chromium 点面板 9 项断言 | e2e.yml |
| 发布门禁 | `release.yml`（`dry_run` 默认开） | tag `v*` 或手动 |

> 完整对照表与「改了 X 该跑哪些」以 `doc/testing/README.md` 为准；本文不复制以免腐烂。