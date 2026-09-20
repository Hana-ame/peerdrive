# 测试组件总览

> 更新：2026-09-20 · 数据来自本分支（`refactor` @ `e4bd3ed`）实测
> 环境：WSL（`go1.22.2` / `node v22.23.1` / vitest 4）；CI 侧用 go 1.26。
> 相关：[分层文档](../layers/README.md) · [网盘手测手册](../NETDISK.md#7-本地跑通怎么亲手测这几个功能) · [AGENTS.md 构建与验证](../../AGENTS.md)

---

## 0. 一句话

项目有 **14 个测试组件**，分布在 4 个 go 模块（back 主模块 + peerjs / signalserver / p2p_bt
三个独立 go.mod）、3 个 npm 工程（front / peerdrive-client / peerdrive-media），
外加 2 个本地脚本和 3 条 CI workflow（其中只有 2 条实际执行测试，见 §3.15）。
它们是**不同层次的验证**，不能互相替代：单元测试普遍用注入的假依赖，
真正把整条链路从头到尾连起来跑一遍的只有端到端脚本（§3.14）。

> 2026-09-20 的教训（`NETDISK.md` §7.3）：CI 与全部单元测试都是绿的，但因为没有人跑
> 「登记文件 → 对方拉清单 → 跨节点拉取」这条组合，两个致命缺陷（join 落盘 ENOENT、
> `RegisterLocal` 不写 `file_index`）一直藏到手工端到端才发现。
> **缺的不是用例数量，是端到端组合覆盖。**

---

## 1. 我改了 X，该跑哪些？

| 你改了 | 必跑 | 建议加跑 |
|---|---|---|
| `back/internal/**` 任意单包逻辑 | `cd back && go test -tags nosqlite ./... -count=1`（308） | `bash scripts/test-layers.sh` 可定位到具体层 |
| `back/internal/transport/**` 帧协议 | 同上（transport 78） | 集成测试（21） |
| **网盘链路**（`nodes/share/pull/RegisterLocal`） | 集成测试 + **`./scripts/netdisk-local-demo.sh`**（手工，必须跑一遍） | 浏览器 UI 手测（`NETDISK.md` §7.2） |
| `back/peerjs/**` | `cd back/peerjs && go test ./... -count=1 -race`（23） | — |
| `back/signalserver/**`（peersignal） | `cd back/signalserver && go test ./...`（21）· **CI 不管它** | — |
| `back/p2p_bt/**` | `cd back/p2p_bt && go test ./...`（7）· **CI 不管它** | — |
| `front/src/**` | `cd front && npm test`（88）+ `npm run build` | `front/tests/*.mjs` 手动脚本（视改动面） |
| `packages/peerdrive-client/**` | `npm test`（61）+ `npm run check:panel` | `node scripts/verify-panel.mjs`（真实浏览器端到端） |
| `packages/peerdrive-media/**` | `npm test`（21）+ `npm run build` | `test/e2e-browser.mjs`、`test/media-node-e2e.mjs` |
| 准备 merge 进 `refactor` | 上表全部必跑项全绿（= CI 的同款命令） | 合并后在主干再跑一遍 |

---

## 2. 全景表（2026-09-20 实测）

单位「用例数」= go 侧 `-json` 里带 `Test` 的 pass 行（含子用例）；node 侧为框架自报。

| # | 组件 | 位置 | 命令 | 用例 | 进 CI | 需外网 |
|---|------|------|------|------|-------|--------|
| 1 | 后端单元/包测试 | `back/`（主模块） | `go test -tags nosqlite ./... -count=1` | **308**（10 包） | ✅ `backend` + `go-build`×4 | ❌ |
| 2 | 后端集成测试 | `back/test/integration/` | `go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1` | **21** 通过 / 4 跳过 | ✅ `integration` | ❌（自托管信令） |
| 3 | 外网集成（手动门控） | 同 2 | `PEERDRIVE_MQTT_TEST=1` / `PEERDRIVE_LIVE_TEST=1` | 4 个用例 | ❌ | ✅（直连，不走代理） |
| 4 | peerjs 模块 | `back/peerjs/`（独立 go.mod） | `go test ./... -count=1 -race` | **23** | ✅ `peerjs` | ❌ |
| 5 | signalserver 模块 | `back/signalserver/`（独立 go.mod） | `go test ./... -count=1` | **21** | ❌ **（盲区）** | ❌ |
| 6 | p2p_bt 模块 | `back/p2p_bt/`（独立 go.mod） | `go test ./... -count=1` | **7** | ❌ **（盲区）** | ❌ |
| 7 | 前端 vitest | `front/tests/*.test.{js,jsx}` | `npm test` | **88**（9 文件） | ✅ `frontend`（含 build） | npm ci 需要 |
| 8 | 前端手动脚本 | `front/tests/*.mjs`（3 个） | playwright / WS 冒烟 | — | ❌ **（盲区）** | ✅（线上站点） |
| 9 | client 包单测 | `packages/peerdrive-client/` | `npm test`（零依赖） | **61** | ✅ `client-package` | ❌ |
| 10 | client 公共面板 + 浏览器自检 | `packages/peerdrive-client/{dist,scripts}` | `npm run check:panel`·`node scripts/verify-panel.mjs` | 面板 8 项断言 | ✅ `check:panel`（产物一致性） | ❌（peerjs 取 CDN） |
| 11 | media 包单测 | `packages/peerdrive-media/` | `npm test` + `npm run build` | **21** | ✅ `media-package` | npm ci 需要 |
| 12 | media 浏览器/Node E2E | `packages/peerdrive-media/test/*.mjs`（非 `*.test.mjs`） | playwright runner / 直启 | 10 断言 + … | ❌ **（盲区）** | ❌ |
| 13 | 分层汇总脚本 | `scripts/test-layers.sh` | `bash scripts/test-layers.sh [--integration]` | 聚合 1/4/5/6/7 | ❌（本地聚合） | — |
| 14 | 网盘端到端脚本 | `scripts/netdisk-local-demo.sh` | 起 3 进程跑全链路 | 8 项断言 | ❌ **（建议进 CI）** | ❌ |

**覆盖范围合计**：自动化（CI）覆盖 308 + 21 + 23 + 88 + 61 + 21 = **522**；
另有 CI 之外的 21（signalserver）+ 7（p2p_bt）需手动，以及 4 个外网门控用例。

---

## 3. 逐个组件说明

### 3.1 后端单元/包测试（308）

- **职责**：单包行为正确性，依赖用注入/临时目录替身（如假 `fileList`、假 transport）。
- **命令**：`cd back && go build -tags nosqlite ./... && go test -tags nosqlite ./... -count=1`
- **包分布**：transport 78 · service 68 · controller 41 · source 32 · config 24 · model 13 · downloader 20 · provider 17 · repository 12 · router 3
- **前置**：`-tags nosqlite` 是硬约束（双 SQLite 驱动 CGO 冲突）。
- **它证明不了什么**：注入的假依赖让「A 写完的索引正好是 B 读的那张表」这类**跨模块组合**失效 —— 这正是 `file_index` 缺陷逃逸的原因。

### 3.2 后端集成测试（21 通过 / 4 跳过）

- **职责**：真实信令 + 真实帧 + 同机 WebRTC。`TestMain` 起一个全局自托管信令，**脱外网**。
- **命令**：`cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1`
- **⚠️ 必须 `-p 1`**：多组测试共享同一个全局信令，并行会互相干扰（`AGENTS.md` 硬性约束）。
- **当前跳过的 4 个**（门控未开，CI 默认不开外网门）：
  `TestMQTTDiscovery`、`TestMQTTDiscoverThenPeerJSInterop`、`TestLiveSignal_DiscoveryAndInterop`、`TestLiveSignal_ProtocolCompat`。
- **文件**：`interop/selfhosted/ws/ws_verbs/share_protocol/peer_pull/file_lifecycle/mqtt/live` 共 8 个 `_test.go`。

### 3.3 外网集成（手动门控）

```bash
PEERDRIVE_MQTT_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestMQTT -v   # 公共 broker（走代理）
PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v   # 线上信令（**直连，不走代理**）
PEERDRIVE_SKIP_RTC=1  ...                                                                          # 无 UDP 沙箱（如 docker 默认）
```
- cloudcone 443 **不走宿主机代理**（代理连它会超时），是这条链路唯一的例外。

### 3.4 `back/peerjs`（23）

- 独立 go.mod（`github.com/Hana-ame/go-peerjs`），主仓 `replace` 指向本地。
- **主仓 `go test ./...` 扫不到它** —— 必须单独跑。CI 有专门 job，本地别忘。
- 建议带 `-race`（流控/并发相关）。

### 3.5 `back/signalserver`（21）— **CI 盲区**

- 独立 go.mod（`github.com/Hana-ame/go-peersignal`），自托管信令服务的本体（`peersignal` 命令的来源）。
- 和 peerjs 一样被 `cd back && go test ./...` 跳过，但**没有对应的 CI job**。改了它 CI 不会红。
- 本地：`cd back/signalserver && go test ./... -count=1`。

### 3.6 `back/p2p_bt`（7）— **CI 盲区**

- 独立 go.mod（`github.com/Hana-ame/go-peerdrive-bt`），Mainline DHT / BEP44 / BEP51。
- 同样没有 CI job。`scripts/test-layers.sh` 的 L7 会跑到它。

### 3.7 前端 vitest（88 / 9 文件）

- **命令**：`cd front && npm test`（`vitest run`）+ `npm run build`（`vite build`）
- **配置**：`vitest.config.ts`（happy-dom + `@vitejs/plugin-react`，setup 文件 `tests/setup.js`）
- **文件明细**：`netdisk.test.jsx` 32 · `components.test.jsx` 18 · `ws.test.js` 14 · `smoke.test.jsx` 1 · `FileTree/LeftPanel/VisibilityPicker/api/api-mock-sync` 合计 23
- **⚠️ 只收 `*.test.{js,jsx}`**，`tests/` 下的 `.mjs` 不在 vitest 视野里（见 §3.8）。
- **不要**加 `--reporter=basic`（此版本 vitest 没有该 reporter）。

### 3.8 前端手动脚本（3 个 `.mjs`）— **CI 盲区**

| 脚本 | 做什么 | 前置 |
|---|---|---|
| `front/tests/e2e-admin-smoke.mjs` | Node 22 原生 WebSocket 打本地 `ws://localhost:3000/ws/peer`，走 admin verb 验证管理面全链路（含二进制帧，`binaryType='arraybuffer'`，15s 超时） | 本地起了后端在 `:3000` |
| `front/tests/playwright-smoke.mjs` | 对**线上** `https://peerdrive.pages.dev` 跑 UI 断言（三列布局、标签栏、合集交互） | playwright runner |
| `front/tests/pw-settings-mobile.mjs` | 对 preview 域名做移动端视口（375×667）截图/断言 | playwright runner |

跑法：`node ~/.claude/skills/playwright-test/scripts/test-runner.mjs front/tests/<script>`（本机 Firefox）。

### 3.9 `packages/peerdrive-client` 单测（61）

- **命令**：`cd packages/peerdrive-client && npm test`（`node --test "test/*.test.mjs"`，24 个 suite）
- **零运行时依赖** ⇒ 不需要 `npm ci`，也不需要网络。覆盖：协议状态机 + 增量 SHA-256 + client API。
- 自实现的**增量** SHA-256（`src/sha256.js`）别改成只用 `crypto.subtle.digest()`（一次性、与流式拉取冲突）。

### 3.10 公共面板（`dist/panel.html`）+ 浏览器自检

网盘 UI 的形态是**单文件公共面板**（file:// 或任意静态托管都能开，不需要本地服务器），
所以它不在 `npm test` 的覆盖范围里，另有两条：

| 命令 | 做什么 | 进 CI |
|------|--------|-------|
| `npm run build:panel` / `check:panel` | 从 `src/` 内联生成 `dist/panel.html`；`--check` 校验产物与源码一致（防漂移） | ✅ `client-package` 跑 `check:panel` |
| `node scripts/verify-panel.mjs` | 真实浏览器（默认复用本机 Edge）打开 `file://` 产物，断言：peerjs 加载 → 连上节点 → 清单 → 点「保存」真下载 → 点「预览」有内容 → sha256 与清单一致 | ❌ 手工 |

- 前置：`./scripts/netdisk-local-demo.sh` 起的节点 + 信令；装 `playwright-core`（不在本包依赖里）。
- 这两个坑只在真浏览器里暴露，所以必须用浏览器验：**peerjs CDN 加载失败**（已加多源回退 + `npm run vendor:peerjs` 离线化）、
  **信令没开 CORS**（file:// 的 origin 是 `null`，`GET /peerjs/id` 被吞，PeerJS 只报含混 `server-error`；
  已由 `back/signalserver` 的 `allowCORS` 处理）。

### 3.10.1 最小演示 `demo/consumer.html`（手工，需静态服务）

```bash
npm run demo        # node scripts/serve.mjs → http://127.0.0.1:8123/demo/consumer.html
npm run demo:signal # peerjs --port 9100（需要 devDependency 已安装才跑得起来）
```
「不跑本地节点也能从 P2P 网络取文件」的人工验收入口。

### 3.11 `packages/peerdrive-media` 单测 + 构建（21）

- `npm ci` → `npm test`（21）→ `npm run build`（IIFE/ESM/CJS 三构建，dist 入库）
- `test/protocol.test.mjs`、`test/e2e.test.mjs`（含 node↔node WebRTC E2E）、`test/core.test.mjs`（mock peerjs 队列边界）
- **⚠️ `npm ci` 对锁文件同步很敏感**：`@vitejs/plugin-react` 把 react 当必需 peer，只放在
  `peerDependencies` 会导致 `EUSAGE` —— 已把 `react`/`react-dom` 写进 devDependencies。

### 3.12 `packages/peerdrive-media` 手动 E2E（10+ 断言）

- `test/e2e-browser.mjs`：mount/load/视频/白名单/dispose 重建，需本机 Firefox + playwright runner
- `test/media-node-e2e.mjs`：Node 侧 E2E，**不符合 `*.test.mjs` glob**，所以 `npm test` 不跑它
- 坑与浏览器 E2E 七连（含串行槽空占三入口）见 `REFACTOR.md` §3.11

### 3.13 `scripts/test-layers.sh`（分层聚合）

按 AOP 分层（L1-L8 + LB + 可选 INT）逐层跑，最后汇总，任一层失败非零退出（不中途停）。
脚本内置 go 代理 env，也会自动把最高版本的 nvm node 补进 PATH；失败日志在 `/tmp/layer-test-<层>.log`。

```bash
bash scripts/test-layers.sh              # L1-L8 + LB
bash scripts/test-layers.sh --integration  # 追加真实信令集成段（-p 1 串行）
```

⚠️ 依赖 `${BASH_SOURCE[0]}` 定位仓库根，别用 `bash -s < 脚本` 的方式喂 stdin（会解析成 `/`）：
`cd <仓库根> && bash scripts/test-layers.sh`，或走 WSL 时在 WSL 里执行。

**2026-09-20 修正的三个问题（改前该脚本会静默 FAIL / 漏跑）**：

| # | 问题 | 现状 |
|---|---|---|
| 1 | **L6 路径写错**：脚本写 `./internal/signalserver/...`，而 signalserver 实际在 `back/signalserver`（独立 go.mod）⇒ pattern 不存在直接报错退出，该层恒 FAIL，这 21 个用例从未被执行过 | ✅ 改为 `cd back/signalserver && go test ./...` |
| 2 | **漏掉 57 个用例**：`config`(24) / `model`(13) / `provider`(17) / `router`(3) 不属于 L1-L8 任何一层，`go test ./...` 会跑而分层脚本不跑 | ✅ 新增 `LB-baseline` 层 |
| 3 | **WSL 里 L8 假 FAIL**：脚本没注入 nvm 的 PATH，`node: command not found` | ✅ 脚本自动补最高版本 nvm node 到 PATH |

修正后实测：**9 层全绿**（L1 23 / L2 66 / L3 12 / L4 161 / L5 12 / L6 21 / LB 57 / L7 7 / L8 88），1m20s。

当前分层规模（2026-09-20 实测）：

| 层 | 切面 | 命令 | 用例 |
|----|------|------|------|
| L1 | 信令/传输原语 | `cd back/peerjs && go test ./... -count=1 -race` | 23 |
| L2 | 帧协议 | `go test -tags nosqlite ./internal/transport/ -skip "^TestAdmin"` | 66 |
| L3 | 管理面 | `go test -tags nosqlite ./internal/transport/ -run "^TestAdmin"` | 12 |
| L4 | 业务核心 | `go test -tags nosqlite ./internal/{controller,service,source,downloader}/...` | 161 |
| L5 | 数据 | `go test -tags nosqlite ./internal/repository/...` | 12 |
| L6 | 发现 | `cd back/signalserver && go test ./...`（独立 go.mod，**cd 进去跑**） | 21 |
| LB | 基础包 | `go test -tags nosqlite ./internal/{config,model,provider,router}/...` | 57 |
| L7 | 外部能力 | `cd back/p2p_bt && go test ./...` | 7 |
| L8 | 前端 | `cd front && npm test` | 88 |
| INT | 真实信令集成 | `go test -tags "nosqlite integration" ./test/integration/ -p 1` | 21 / skip 4 |

### 3.14 `scripts/netdisk-local-demo.sh`（端到端，8 项断言）

唯一会**完整跑通网盘链路**的组件：起 `peersignal :9100` + `node-a :3001`（共享）+ `node-b :3002`（消费），
依次断言 市场发现 → 加入（含 `joined_nodes.json` 落盘）→ share 帧清单非空 → 跨节点拉取 → sha256 一致。

```bash
./scripts/netdisk-local-demo.sh          # 起环境并自动跑一遍
./scripts/netdisk-local-demo.sh --stop   # 停掉
```
完全脱外网。**改网盘链路必跑**（AGENTS.md 硬要求）。尚未接进 CI（`NETDISK.md` §7.5）。

### 3.15 CI workflow（本地↔CI 映射）

| workflow | 触发 | job | 覆盖的组件 |
|---|---|---|---|
| `ci.yml` | push 到 main/master/refactor/base/docs/`feat/*`/`fix/*`/`module/*`/`*-agent`/`*-frontend`；PR 到 main/master/refactor | 6 个 | ①后端单元（vet+test+build）②集成（`-p 1`）④peerjs（vet+test）⑦前端（test+build）⑨client ⑪media（ci+test+build） |
| `go-build.yml` | push / PR（不限分支） | 5 平台矩阵 | ①后端单元×4（非 Windows 才跑 test）+ 交叉构建产物 |
| `release.yml` | push tag `v*` | 2 个 | **只 build，不跑任何测试** |

**⚠️ 打 tag 发版时 `ci.yml` 不会触发**（它的 `on.push` 只列了 branches，不含 tags），
而 `release.yml` 又不跑测试 —— 也就是说**发版路径上没有任何测试门禁**。
发版前请手动跑一遍 §1 选表里相关组件的全套命令。

---

## 4. 自动化没覆盖的地方（盲区清单）

按风险从高到低：

| 盲区 | 为什么危险 | 现状 |
|---|---|---|
| **端到端组合**（登记→清单→拉取→校验） | 2026-09-20 两个致命缺陷都藏在这里，而所有单元/集成用例全绿 | 只有手工脚本 `netdisk-local-demo.sh`，**未进 CI** |
| **`back/signalserver`**（21） | 独立 go.mod + 无 CI job ⇒ 信令服务改坏了 CI 照样绿 | 手动跑 |
| **`back/p2p_bt`**（7） | 同上 | 手动跑（test-layers L7 会捎带） |
| **外网集成 4 用例** | 公共 broker / 线上信令的协议兼容性无人验证 | 门控手动 |
| `front/tests/*.mjs`（3 个） | UI 冒烟依赖人工触发；`playwright-smoke` 断言的是线上站点 | 手动 |
| media 的两个非 `*.test.mjs` E2E | 浏览器真实渲染路径不在 `npm test` 里 | 手动 |
| client demo 页面（:8123） | 消费端真人可用性的最后一道 | 手动 |
| **公共面板的浏览器自检** | `dist/panel.html` 是 file:///静态托管的单文件，单元测试完全碰不到；peerjs CDN 加载、信令 CORS、真实点击保存都只能在这里验 | 手动 `scripts/verify-panel.mjs`（8 项断言，已跑通） |
| **打 tag 发版** | `ci.yml` 的 `on.push` 只列 branches、不含 tags，`release.yml` 又只构建 —— **发版没有任何测试门禁**，见 §3.15 | 需人工把关 |

---

## 5. 跑之前：环境约定

```bash
# 代理（go 取包/下载依赖；cloudcone 443 例外，必须直连）
export HTTPS_PROXY=http://172.29.80.1:10809
export GOPROXY=https://goproxy.cn,direct
# WSL 里 node 不在默认 PATH（vite 8 要求 node ≥ 22.12）
export PATH="$HOME/.nvm/versions/node/v22.23.1/bin:$PATH"
```

- Windows 侧 bash 工具受限 ⇒ 经 WSL 跑：
  `ssh -i ~/.ssh/id_rsa lumin@127.0.0.1 "tr -d '\r' | bash -s" < 脚本`
  （脚本里的 CRLF 会让 bash 报 `$'\r': command not found`，所以必须 `tr -d '\r'`。）
- Go **必须** `-tags nosqlite`；Node 侧有依赖改动的包要先 `npm ci`（`media` / `front` 有 lock，`client` 零依赖不需要）。
- npm 经代理装可选原生依赖（如 rolldown 的 bindings）常被跳过，导致本地 build 失败而 CI 正常 ——
  本地构建失败时先看是不是这一步。

---

## 6. 维护约定

- 改了代码行为 → 同步该层测试 + 分层文档（`doc/layers/`，硬性要求）。
- 测试函数必须标注「发现背景」（全局 AGENTS.md 硬性要求）。
- **新增/删除测试集时更新本文件的 §1 选表、§2 全景表、§3 对应小节。**
- 用例数字会漂移：本表是 2026-09-20 的快照，重跑后如有出入以实测为准并顺手更新。
- 想继续补，优先级：**把 `netdisk-local-demo.sh` 接进 CI**（`NETDISK.md` §7.5）>
  给 signalserver / p2p_bt 各补一个 CI job（同 peerjs 那种做法）>
  给 `release.yml` 的发版路径加一道测试门禁（见 §3.15）。
  （`test-layers.sh` 自身的问题已于 2026-09-20 修完，见 §3.13。）
