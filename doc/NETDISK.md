# Peerdrive 网盘目标：规划与模块划分

> 来源：用户 2026-09-20 的目标陈述。本文件是**目标定义 + 模块拆分 + 验收口径**，
> 与 `doc/ROADMAP.md`（开发顺序）配合读：ROADMAP 管"先做什么"，本文件管"做成什么样"。

## 0. 目标陈述（原文语义）

1. **前端就是一个普通网盘界面**：
   - 有**自己的节点**和**别人的节点**；
   - 可以在**市场**里**加入节点**；
   - 加入节点之后能看到**文件链接**（打包好的 collection，或者单独的文件）；
   - 选中文件 → **保存** → 从别人那里下载。
2. **还要有 WebRTC 纯 client 消费端**：不装节点也能从这套 P2P 网络拉取文件。

用户的类比：

- 前者（网盘 + 市场 + 加入 + 选文件保存）= **PT / BT / 快播**思路：
  有一个资源目录，选资源，下载到本地。
- 后者（纯 client 消费端）= **WebRTC / PeerJS 兼容**：只做消费，不做服务。

## 1. 目标 → 能力缺口对照

| 目标能力 | 现状 | 缺口 | 归属模块 |
|---|---|---|---|
| 自己的节点 | ✅ `GET /peerjs/node`（id/online/peers） | 无本节点"我提供了什么"视图 | M1/M2 |
| 别人的节点（市场） | 🟡 发现服务器有在线节点，但**没有面向用户的节点目录端点**；前端只能看到已直连 peerId 列表 | 市场列表（在线/离线、能力摘要、join 状态） | **M1** |
| 市场里"加入节点" | 🔴 无 | 已加入节点持久化 + 加入即拨号 + 退出 | **M1** |
| 看到对方"文件链接"（合集/单文件） | 🔴 无：帧 verb 只有本地管理清单 `list`（全量 file_index），**没有"共享范围"概念**；ROADMAP §5 明确"文件范围管理未做" | 节点显式共享范围 + `share` verb | **M2** |
| 选中文件保存 → 从对方下载 | 🟡 有 `FetchFromPeer`（内存整包、64MB 上限）/`OpenStream`（流式但不落盘、无进度） | 流式拉取→CAS 落盘→校验→注册索引，带进度/取消的任务面 | **M3** |
| 网盘式前端 | 🔴 现有页面是匿合集编辑器/浏览器（AnonCreator/AnonExplorer/Plaza），不是网盘信息架构 | 网盘 IA（我的文件/节点/市场/传输）+ 对接 M1-M3 | **M4** |
| WebRTC 纯 client 消费端 | 🟡 `peerdrive-media` 是**浏览器 PeerJS 客户端**，但帧协议是 `url`（叫节点去抓 URL），**不是** peerdrive 的 `req`（按 sha256 拉内容） | 独立的零依赖客户端包，说 `req`/`info`/`share` 协议 | **M5** |

**关键判断**：目标本身已经把 ROADMAP 阶段 3→6 串成了一条用户可见的链路
（互联 → 文件 → 组合 → 管理链路 → 范围 → 上传下载保存）。因此本目标**不改变**
ROADMAP 顺序，而是它的"验收形态"：阶段 5（范围）与阶段 6（上传下载保存）在此
第一次有了具体界面。

## 2. 硬约束（沿用 ROADMAP §硬约束）

- 第 7 阶段（身份管理）之前，**一切按节点级 peerId 工作**：市场、加入、共享、
  保存全链路不依赖账号/regserver。需要"归属"时先用 peerId。
- 共享范围默认**关**：不显式声明就不对外暴露任何清单（防"默认全盘分享"）。
- 前端**不引入新依赖**（环境 npm 网络不稳 + 仓库已有 Tailwind/React19/router7）；
  版式**参考成熟网盘的信息架构**（Nextcloud Files、Cloudreve、Alist），组件自行实现。
  满足"参考较好的设计、不从零发明交互"。

## 3. 模块划分

分支模型沿用 `AGENTS.md`：`module/<name>`，每分支自己跑通 CI，再 merge 回 `refactor`。

### M1 `module/node-market` — 节点市场与加入
- **模型** `internal/model/node.go`：`NodeSummary{PeerID,NodeType,Collections,Files,Joined,Connected,LastSeen}`。
- **服务** `internal/service/node_directory.go`：
  - 市场列表：查发现服务器（`DiscoverURL` 的 `/discover/nodes`，房间 = 内容分片房间 + 存在房间）
    聚合在线节点，与本地 `conns` 合并出 `Connected`，与 joined 清单合并出 `Joined`。
  - joined 清单持久化：`<storageDir>/joined_nodes.json`（原子写：临时文件 + rename）。
    为什么不用 SQLite：这是一份很小的运营者偏好，且要能在无 DB 场景（纯 client 模式/CI）工作。
- **端点**（经 `/ws/peer` admin 帧，前端只走 admin）：
  - `GET /peerjs/nodes` → `{self:{id,...}, nodes:[NodeSummary]}`（市场）
  - `GET /peerjs/nodes/joined` → 已加入列表
  - `POST /peerjs/nodes/join {peer}` → 落盘 + 立即拨号（`EnsureConnection`）
  - `DELETE /peerjs/nodes/join?peer=` → 移出（不主动断连，连接由发现/拨号预算自然收敛）
- **transport 新增**：`EnsureConnection(peerID string)`（幂等拨号，复用 `connectLoop` 的
  `connecting` 去重）；`ConnectedPeerIDs()`（已有 `Connections()`，补一个只取 id 的轻量版）。

### M2 `module/node-share` — 节点共享范围（ROADMAP 阶段 5）
- **模型** `internal/model/share.go`：`ShareScope{Collections []ShareCollection, Files []FileInfo, Dirs []string}`；
  `ShareCollection{Hash,Name,Size,Entries []ShareEntry}`。
- **服务** `internal/service/nodeshare.go`：按配置 + 本地状态解析共享范围：
  - `PEERDRIVE_SHARE_COLLECTIONS`：逗号分隔的合集 hash（或 `all` = 所有 public 合集）；
  - `PEERDRIVE_SHARE_DIRS`：逗号分隔目录，目录内的已登记文件进共享；
  - `PEERDRIVE_SHARE_ENABLE`（默认 **false**）总开关——默认不共享任何东西。
- **帧 verb** `share`（入站，`transport/share.go`）：
  `{type:"share"}` → `{type:"share-resp", collections:[...], files:[...], total}`
  - **不复用 `list`**：`list` 是本地管理清单（文件索引全量），语义是"我管理的"，
    不是"我共享的"。混用等于默认全盘暴露（也正是 ROADMAP 里"广播本地合集 hash
    等于公开本节点持有什么"那条待办的成因）。
  - `share` 只回**显式共享**内容；未开启共享时回空 `share-resp`（不是 err，前端好渲染空态）。
- **announce 扩展**：上报 `shares:{collections:n,files:n}` 摘要（**只报数量不报 hash**），
  市场卡片能显示"该节点共享了 3 个合集 12 个文件"，同时不泄露具体内容。

### M3 `module/peer-pull` — 跨节点拉取保存
- **服务** `internal/service/peerpull.go`：任务式拉取（形态参考 BT 下载：列表/进度/取消）。
  - `Start(peer, hash, filename) (jobID, error)`：后台 goroutine 用
    `transport.OpenStreamFrom` 流式读 → 写 `<storageDir>/tmp/<jobID>.part` →
    校验 sha256 → rename 到 CAS `<storageDir>/<h[:2]>/<h>` → 注册 file_index →
    `done`。任一步失败 → `failed` + 清理 `.part`。
  - 进度：`{jobID, peer, hash, name, total, received, status, error, startedAt, endedAt}`；
    `total` 从流的 meta 帧拿（`OpenStreamFrom` 的读侧首帧已含 total；取不到则 -1 未知）。
  - `StartCollection(peer, hash)`：拉 `share` 清单里的合集 → 展开 entries → 逐个 Start
    （并发 3），返回批量 job 列表。
- **端点**：`GET /p2p/pull`、`POST /p2p/pull`、`POST /p2p/pull/collection`、
  `POST /p2p/pull/:id/cancel`。
- **保存位置约定**：CAS 即"已保存到我的网盘"（内容寻址去重，同 hash 只存一份）；
  `file_index` 登记提供人类可读文件名 → 前端"我的文件"直接可见。

### M4 `module/netdisk-ui` — 网盘前端
参考信息架构（不照搬代码，照搬结构）：

| 区域 | 参考来源 | 本实现 |
|---|---|---|
| 左侧导航（网盘/市场/传输/设置） | Nextcloud Files、Cloudreve | `components/SideNav.jsx` |
| 主区文件表格（名称/大小/来源/时间/操作） | Cloudreve、Alist | `components/FileTable.jsx` |
| 顶部面包屑 + 操作条（上传/新建/刷新） | Cloudreve | `components/DriveToolbar.jsx` |
| 节点卡片（在线状态 + 能力摘要 + 加入按钮） | BT/PT 站点资源列表 | `components/NodeCard.jsx` |
| 传输任务列表（进度条 + 取消） | qBittorrent/Cloudreve | `components/TransferRow.jsx` |
| 预览（图片/视频/文本/PDF） | — | **复用** `AnonExplorer/ImagePreview|PdfPreview|TextPreview` |

页面：

- `pages/Drive/index.jsx` — 我的网盘：本地文件（`/files`）、上传、删除、预览、"我的合集"。
- `pages/Market/index.jsx` — 节点市场：在线节点、共享摘要、加入/退出。
- `pages/Peers/index.jsx` — 我的节点：已加入节点 + 直连状态。
- `pages/PeerDetail/index.jsx` — 对方节点：**文件链接**列表（合集可展开 → entries；单文件），
  勾选 → "保存到我的网盘" → M3 拉取 → 跳传输页看进度。
- `pages/Transfers/index.jsx` — 传输任务：进度/取消/重试。
- `App.jsx` 路由与 `Navbar` 分组调整；旧页面（Plaza/AnonCreator/AnonExplorer/BT/IPFS/Settings）
  保留在"高级"分组，不删。

### M5 `module/peer-client` — 纯 WebRTC 消费端（`packages/peerdrive-client`）
- **零运行时依赖**：`Peer` 构造函数**注入**（浏览器用 CDN 的 `window.Peer`，Node 测试注入替身）。
  理由：peerdrive-media 已踩过"peerjs CJS/ESM default 语义不同 → IIFE 里 Peer=undefined"的坑
  （见其 core.js 头注释），注入式接口从根上绕开该问题，且 CI 不需要 npm install。
- **无构建**：直接发 ESM 源码（`src/*.js`）+ 浏览器 demo 用 `<script type="module">`。
  CI job 只需 `node --test`。
- `src/protocol.js`：`req`/`info`/`list`/`share` 请求帧与响应帧的构造/解析（与 Go 端
  `conn.go` 的 dcReq/dcResp 字段名逐字对齐）。
- `src/client.js`：`PeerDriveClient`
  - `connect(nodeId)`：PeerJS 连接（`serialization:'raw'`）+ 控制通道 keepalive；
  - `shares()` / `info(hash)` / `list()`；
  - `fetch(hash, {offset,size,onProgress,signal})` → `Uint8Array`；
  - `download(hash, {name,onProgress})` → Blob + 触发浏览器保存；
  - `saveToFile(hash,{name})`：File System Access API 流式落盘，不可用时降级 Blob。
- `demo/consumer.html`：纯静态页（CDN peerjs + 本包）→ 填信令/节点 ID → 列共享 → 下载。
  **无后端**，即"纯 client 消费端"。
- **协议契约由 Go 集成测试钉死**：`back/test/integration` 加 `TestShareProtocolContract`，
  用与 JS 客户端完全相同的帧序列（`share` → `req`）断言字段名与语义
  （`share-resp`/`collections`/`files`/`meta`/`done`）。JS 端单测覆盖同一批字段名常量，
  两边共同构成"互通性"证据。

### M6 — 文档与合并
- 文档：本文件（目标/模块/验收）+ `doc/FRONTEND-DRIVE.md`（IA 与页面说明）+
  `packages/peerdrive-client/README.md`（消费端用法）+ `AGENTS.md`/`ROADMAP.md` 状态更新。
- 合并：各 `module/*` 分支 CI 绿后逐个 merge 回 `refactor`，合并后再跑一遍全量
  （back 单测 + 集成 + front 测试/构建 + client 测试），确认"各自绿、合起来也绿"。

## 4. 验收口径

| 模块 | 验收 |
|---|---|
| M1 | 单元：joined 清单持久化/原子写/去重；市场聚合去重与自身过滤。集成：两节点仅靠存在房间发现后出现在对方市场列表。 |
| M2 | 单元：共享范围解析（开关关 → 空；`all` → 只含 public；目录过滤）；`share` 帧序列化字段名。集成：A 显式共享合集 → B 用 `share` verb 拉到 → 字段与 entries 一致（**协议契约测试**）。 |
| M3 | 单元：拉取落盘 + sha256 校验 + 失败清理；进度状态机（running→done/failed/cancelled）。集成：A 有内容 → B `pull` → B 的 CAS 出现同 hash 文件且 `file_index` 可查。 |
| M4 | vitest：市场/节点详情/传输页渲染与交互（mock api）；`npm run build` 通过。 |
| M5 | `node --test`：协议帧构造/解析、字段名与 Go 侧一致、fetch 分块重组、abort；demo 可打开（真实浏览器人工验证）。 |
| M6 | 四个 job（back/integration/frontend/client）在 refactor 合并后全绿。 |

## 5. 明确不做（本轮）

- 身份/账号接入（ROADMAP 阶段 7，最后做）。
- 跨节点**写**（把文件推给对端、远端目录管理）——本轮只做"读 + 保存到本地"。
- 跨节点受限内容的身份传递（等阶段 7）。
- BT/快播式的**分片并行 + 多源选择**（本轮是单源流式拉取；多源调度留待后续，
  当前 `source.Manager` 已具备多源骨架，接上即可）。

## 6. 实施状态（2026-09-20，M1-M6 全部落地并合并回 `refactor`）

各模块分支、提交与最终合并点：

| 模块 | 分支 | 提交 |
|---|---|---|
| M1 节点市场与加入 | `module/node-market` | `feat(nodes): 节点市场与加入（网盘目标 M1）` |
| M2 共享范围 + share 帧 | `module/node-share` | `feat(share): 节点共享范围 + share 帧（网盘目标 M2 / ROADMAP 阶段 5）` |
| M3 跨节点拉取保存 | `module/peer-pull` | `feat(pull): 跨节点拉取保存（网盘目标 M3 / ROADMAP 阶段 6）` |
| M4 网盘前端 | `module/netdisk-ui` | `feat(ui): 网盘前端界面 + 路由接线（网盘目标 M4）` |
| M5 纯 WebRTC 消费端 | `module/peer-client` | `feat(client): 纯 WebRTC 消费端包（网盘目标 M5）` |
| M6 文档与合并 | `refactor`（直接改） | merge 五个模块 + 本文档更新 |

### 6.1 计划 vs 实际（有偏差的都写在这里）

| 项 | 计划 | 实际 | 原因 |
|---|---|---|---|
| M3 落盘位置 | CAS `<storageDir>/<h[:2]>/<h>` | `<DownloadDir>/pulled/<相对路径>.part` → 校验 → rename → `file_index.Create` 登记 | 登记后既能出现在"我的文件"，也能被本节点继续 `serveFile` 服务给别的节点（集成测试验证了 C 从 B 拉取）。再写一份 CAS 是同一份内容的第二次落盘，没收益 |
| M3 取消端点 | `POST /p2p/pull/:id/cancel` | `POST /p2p/pull/cancel {id}` | gin 不允许同级路由同时有静态段与参数段；改成静态路径 + body 传 id。同理 `GET/POST /p2p/pull` 不加 `:id` |
| M3 并发 | 并发 3 | 并发 3（信号量）+ 任务表上限 200（只裁已结束的） | 长跑节点上任务表无界增长会吃内存 |
| M4 组件 | `components/SideNav/FileTable/DriveToolbar/NodeCard/TransferRow` | 少 `DriveToolbar`（工具条内联进 Drive 页）；多 `format.js`（三处共用的展示规则） | 工具条只有一页用，抽出去反而多一层间接；展示规则散着写必然出现"文件页 1.5MB、传输页 1572864 字节" |
| M4 预览 | 复用 `AnonExplorer/*Preview` | 复用 `api.getBlobUrl` + 新窗口打开 | 既有 Preview 组件与合集条目结构耦合；网盘的文件是 file_index 条目，形状不同，硬套要改造两处 |
| M4 路由容器 | — | 新增 `Fill` 包裹层 | 网盘页是 `flex flex-1 min-h-0`，而 `<Routes>` 父级是块级容器——不加这层 `overflow-y-auto` 拿不到确定高度，内容会被裁掉而不是滚动（既有页面自带 `h-full`，故不改造） |
| M5 keepalive | "控制通道 keepalive" | 不做 | 文件协议里没有 `ping` verb（心跳只在 `peerdrive-media` 那套 `url/meta` 协议里）；WebRTC 自身有 DTLS/SCTP 保活，应用层再加一层没有对应端点 |
| M5 `list()` / `info()` | 计划实现 | **不做**，只实现 `share` + `req` | `list` 是节点的本地管理索引（含本机绝对路径），按设计只对可信对端开放，消费端不应有它；`info` 的用途（拿文件大小）已由 `meta` 帧的 `total` 覆盖 |
| M5 `download()` / `saveToFile()` | `download()` 返回 Blob、`saveToFile()` 用 File System Access API | `saveAs()`（Blob + `<a download>`）+ `stream()`（异步迭代器，调用方自行落盘） | File System Access API 只有 Chromium 系支持，做成默认路径会在 Safari/Firefox 上直接不可用。把"流"暴露出来、让调用方选落盘方式更诚实（README 已写明这条限制） |
| M5 SHA-256 | 未提及 | 新增 `src/sha256.js`（增量） | `crypto.subtle.digest()` 是一次性的，与流式拉取冲突（必须先攒满整份内容才能算摘要）。Go 侧对全量请求读完即校验 sha256，消费端要给出同样的保证就必须能边收边算 |
| M5 内存 | 未提及 | `maxBytes` 默认 256MB | `fetch()/saveAs()` 是整体驻留内存的（浏览器 Blob 下载只能这样），2GB 文件会直接崩标签页。对端声明的大小时在 `meta` 阶段就拦 |
| 测试基建 | 未提及 | 修 `front/src/__mocks__/api.js` 漂移（缺 8 个、多 18 个僵尸导出）+ 新增 `tests/api-mock-sync.test.js` 双向守卫 | 手写 mock 不同步会让页面在测试里拿到 `undefined`，死在离原因很远的调用点；这次就是被守卫测试揪出来的 |

### 6.2 验证结果

| 项 | 结果 |
|---|---|
| 后端单测 | `go test -tags nosqlite ./...` 全绿 |
| 后端集成 | `go test -tags "nosqlite integration" ./test/integration/ -p 1` 全绿，新增 4 个：`TestNodeMarketListsDiscoveredPeer`、`TestShareProtocolContract`、`TestPeerPullSavesToLocalDrive`（M1/M2/M3 各一）+ 既有的自托管信令用例 |
| 前端单测 | `vitest run` 88/88（新增 `netdisk.test.jsx` 34 个 + `api-mock-sync.test.js` 2 个） |
| 前端构建 | `vite build` 成功 |
| 消费端单测 | `node --test` 60/60（protocol / sha256 / client 三个文件） |
| CI | `.github/workflows/ci.yml` 新增 `client-package` job；`refactor` 上 Peerdrive CI 六个 job 与 Go Build Matrix 五个平台全绿（详见 §6.4） |

### 6.3 与 ROADMAP 的对应

本目标的五个模块**不改** ROADMAP 顺序，而是把阶段 1/5/6 从"骨架"推到"用户可见"：

- 阶段 1（互联）：M1 补上了"面向用户的节点目录 + 加入动作"，ROADMAP 里
  "广播本地合集 hash 等于公开本节点持有什么"那条待办由 M2 的 `share` 帧定性解决
  （只报数量、不报 hash；具体清单只在点对点直连后给）。
- 阶段 5（文件范围管理）：M2。
- 阶段 6（上传下载保存）：M3（跨节点）+ M4（界面）+ M5（无节点消费端）。
- 阶段 2/3/4（文件 / 组合 / 管理链路）在 M4 里第一次有了界面承载
  （Drive / 合集卡片 / 传输任务），但**做深**（目录树、批量重命名、重试策略等）仍待后续。
- 阶段 7（身份管理）：仍未开始，硬约束保持——全链路不依赖账号，归属先用 peerId。

### 6.4 CI 转绿：顺带修掉的三个既有红灯

合并后推 `refactor` 触发 CI，才发现仓库里有**两条** workflow，且各有红灯——
成因都在 `9e4ede3`（网盘模块之前的基础提交）上就已存在，与本次目标无关，
但"验证（通过 gh ci）"要求主干是绿的，所以一并修掉。

最终状态（`67a60b9`）：Peerdrive CI 六个 job + Go Build Matrix 五个平台**全绿**。

| # | 红灯 | 根因 | 修法 | 提交 |
|---|---|---|---|---|
| 1 | Peerdrive CI → `media-package` | `packages/peerdrive-media` 把 react/react-dom 只声明为 **optional peerDependencies**，但 `@vitejs/plugin-react` 把 react 当**必需** peer → npm 7+ 自动补装 peer，理想树含 `react@19.3.0`，而旧 lockfile 没有 → `npm ci` 的同步检查报 EUSAGE | react/react-dom 补进 `devDependencies`，`npm install --package-lock-only` 重算锁文件（diff 仅 +react/react-dom/scheduler 三条，并清掉根部陈旧的 `peerDependenciesMeta`） | `4fce69f` |
| 2 | Go Build Matrix → `macos-latest/darwin-arm64` | `internal/source` 的 `TestPeerSource_WinnerPeerLockReleased` 竞速轮次之间没等收割 goroutine 释放输家锁 | 新增 `waitPeersIdle()`（TryLock 探测 + deadline），替换原来只覆盖第一轮的固定 `sleep(50ms)`；**生产代码未改** | `ccd1af9` |
| 3 | Peerdrive CI → `media-package`（修好 #1 后暴露） | `.gitignore` 的 `react/` 规则**不带前导斜杠**，匹配任意层级同名目录 → `packages/peerdrive-media/src/react/` 整个被忽略，5 个 React 源文件从未入库 → CI 全新 checkout 报 `Cannot resolve entry module src/react/index.js` | 规则锚定为 `/react/`（同段 `/go/` 一并锚定，与 `/docs/` 一致）；漏掉的 5 个源文件补入版本库 | `67a60b9` |

第 2 项的定位过程值得记一笔：**它不是 macOS 专属** —— 本地 Linux
`go test -count=400` 就能复现约 1%~2%（与 CI 报错同一行 `peer_test.go:424`），
慢机器只是放大了概率。关键证据来自临时插桩取到的轨迹：

```
recv #1 pid=peerA success=true / winner=peerA returning      ← 第二轮胜出即返回
collectPeers: peerB BUSY (lock held) -> skipped              ← 第三轮看到 peerB 仍被占
Open collectPeers -> [peerA] / single-path peerA FAILED      ← 退化成单对端路径 → 报错
candidate peerB ok                                            ← 晚到的第二轮候选，之后才被收割
```

即：`raceOpen` 胜出即返回、输家由后台收割 goroutine 关流释放锁（**刻意设计**，
见 `peer.go` 注释），用例却在第二轮之后立刻开第三轮，于是断言前提被错位的
"单对端路径"污染。插桩本身也有坑：往 stderr 打日志会改变 goroutine 调度，
把 1~2% 的竞态直接掩盖（600 次全绿）——要改成**内存环形缓冲**、失败时再 dump。

验证：`-count=2000` 全绿（旧失败率下期望 20~40 次失败）、`-race -count=300` 全绿；
`npm ci` + `npm test` 21/21 + `npm run build` 全链通过。

---

## 7. 本地跑通：怎么亲手测这几个功能

CI 绿 ≠ 链路可用。这一节是**真正把网盘跑起来**的手册，也是本次
运行时缺陷被发现的地方（CI 全绿但 `files=[]`，见 §7.3）。

### 7.1 一键起环境

```bash
./scripts/netdisk-local-demo.sh          # 起信令 + node-a + node-b，自动跑一遍全链路
./scripts/netdisk-local-demo.sh --stop   # 停掉
```

脚本做的事：编译 `back/cmd/server` → 起自托管信令 `peersignal :9100` → 起
`node-a`（`PEERDRIVE_SHARE_ENABLE=true`）和 `node-b`（纯消费）→ 依次验证
市场/加入/清单/拉取/内容校验，逐步 PASS/FAIL。完全脱外网（不碰公共信令与 IPFS）。

拓扑：

```
peersignal :9100（信令 + 内置 /discover 发现，仅转发 SDP/ICE）
   ├── node-a :3001  storage=/tmp/pddemo/a/root  download=<root>/downloads
   │                 SHARE_DIRS=<root>/downloads/shared   ← 共享这一个目录
   └── node-b :3002  storage=/tmp/pddemo/b/root  download=<root>/downloads
        └── 市场发现 node-a → join → share 帧取清单 → pull 落盘到 <root>/downloads/pulled
```

### 7.2 分功能手工测试清单

设 `B=http://127.0.0.1:3002`（消费方视角）。

| 功能 | 怎么测 | 期望 |
|---|---|---|
| 节点市场 | `curl $B/peerjs/nodes` | `nodes[]` 含 `node-a`，带 `online/connected/joined/shares` |
| 加入节点 | `curl -X POST $B/peerjs/nodes/join -d '{"peer":"node-a"}'` | `{"status":"joined"}`；`<b-storage>/joined_nodes.json` 落盘，重启 node-b 后仍在 |
| 我的节点 | `curl $B/peerjs/nodes/joined` | 含 `node-a`，`joined=true` |
| 对方文件链接 | `curl $B/peerjs/nodes/node-a/shares` | `files[]` 非空（这是 share 帧，跨 WebRTC 取） |
| 拉取单文件 | `curl -X POST $B/p2p/pull -d '{"peer":"node-a","hash":"<h>","name":"demo.txt"}'` | 返回 job，`status=running` |
| 传输任务 | `curl $B/p2p/pull` | `status=done`、`received==total`、`saved_to` 指向 `downloads/pulled/<name>` |
| 取消 | `curl -X POST $B/p2p/pull/cancel -d '{"id":"<job id>"}'` | 任务转 `canceled` |
| 内容正确 | `sha256sum <saved_to>` | 与 share 帧里的 `hash` 完全一致 |
| 本地已有 exemption | 重复 pull 同一 hash | `skipped:true`（不重复传，见 `peerpull.go:283`） |

### 7.2.1 网盘 UI 的形态：**公共面板**（不是本地起的 web 服务）

网盘的 UI **不是**一个需要你自己起 http 服务的前端工程，而是一个**公共静态面板**：

**在线版（推荐直接用）**：<https://hana-ame.github.io/peerdrive/>

由 `.github/workflows/pages.yml` 在每次 push（且面板相关文件有变动）时自动构建部署，
产物就是下面这个单文件面板。**注意在线版是 HTTPS，会触发下面的前提 3（信令必须 wss）。**

本地也随时可以自己生成一份：`packages/peerdrive-client/dist/panel.html`（单文件，58 KB）。

```bash
cd packages/peerdrive-client && npm run build:panel      # 生成产物
# 然后直接双击 dist/panel.html —— file:// 就能用，不需要任何服务器
```

它通过 PeerJS 拨号直连节点，走 share 帧看清单、走 req 帧拉内容（逐块校验 sha256）。
把连接参数写进 URL 就能当"某个节点的面板"发出去：

```
panel.html?node=node-a&host=<信令 host>&port=9100&path=/&key=peerjs&secure=0&auto=1
```

`auto=1` 打开即连。面板支持多节点并存、传输任务列表（进度/速率/取消）、
图片/视频/文本预览、最近连接记录。

三个必须知道的前提：

1. **信令必须开 CORS**。面板多半以 `file://` 或别的域打开（origin 是 `null`／异构源），
   而它第一件事就是 `GET /peerjs/id` 取临时 id；信令不返回跨域头时浏览器直接吞掉响应，
   PeerJS 只会报含混的 `server-error`，看不出是 CORS。
   自托管信令 `back/signalserver` 已处理（`allowCORS`：HandleID / Announce / Leave / Nodes / Status
   加跨域头 + OPTIONS 预检短路）。
2. **peerjs 走回退链加载**：同目录 `peerjs.min.js` → jsdelivr → unpkg。
   内网/离线先跑 `npm run vendor:peerjs` 把它下到 `dist/` 同目录，即可完全不联网。
3. **HTTPS 页面只能用 wss 信令**（GitHub Pages 强制 HTTPS）。浏览器会把 HTTPS 页面发起的
   `ws://` 当**混合内容**直接拦掉，PeerJS 侧只表现为"连不上"，没有任何提示。
   - 信令在自己机器上、面板用 `localhost`：多数浏览器对 `ws://localhost` 网开一面；
   - 信令在局域网 IP：一律被拦，必须 wss。
   面板会在这种配置下直接拦住并给出提示。自托管信令开 wss 的方式：

   ```bash
   peersignal -addr :9100 -tls-cert cert.pem -tls-key key.pem
   # 或在信令前挂一层 TLS 反代（caddy / nginx / Cloudflare Tunnel）
   ```

   只给 `-tls-cert`、`-tls-key` 其中一个会**直接报错退出**（不静默降级回 http——
   那会让对面 HTTPS 面板被混合内容拦掉，而服务端日志看着一切正常，极难排查）。

端到端自检（真实浏览器 + 真实点击，需要先起 §7.1 的环境）：

```bash
cd packages/peerdrive-client
SIG_HOST=<WSL LAN IP> SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs
# 断言：连上 → 清单 → 点「保存」真的下载 → 点「预览」渲染内容 → sha256 与清单一致
```

线上托管自检（不需要节点，只验部署链路）：

```bash
node scripts/verify-pages.mjs                 # 默认验 https://hana-ame.github.io/peerdrive/
# 断言：面板骨架 / bundle 注入 / peerjs 可取到 / HTTPS+ws:// 混内容提示 / 无 JS 报错
```

> 旧的 `npm run demo`（`demo/consumer.html`）还在，但它是**最小演示**：
> 靠相对路径 `import '../src/index.js'`，必须有 HTTP 服务提供整个包目录，做不了单文件分享。

### 7.2.2 节点管理台（`front/`，给节点运营者用）

`front/` 是**节点自身的控制台**（我的网盘 / 节点市场 / 我的节点 / 传输任务），
它调节点的 HTTP API（`/peerjs/nodes`、`/p2p/pull` …），因此需要后端在跑：

```bash
cd front && npm run dev -- --host 0.0.0.0 --port 5173
```

浏览器打开后在**设置里把后端改成 `http://<本机IP>:3002`**（默认后端是远端
`wsl-3000.moonchan.xyz`，不是本地）。WSL 场景用 WSL 的 LAN IP 而不是
`localhost`——本机回环到 WSL 的转发不一定通，而 LAN IP 稳定可达。
然后按 我的网盘 → 节点市场（加入 node-a）→ 我的节点 → 点开 node-a →
选中文件保存 → 传输任务 看进度。

> 这是「运营者视野」的界面：它能做的事（加入节点、看市场、管理本机共享）
> 都建立在**本地有节点**之上。纯消费者不该被要求部署节点 —— 那是 §7.2.1 面板的职责。

### 7.3 首次跑通时暴露的两个运行时缺陷（已修）

CI 和单元测试全绿，但真实跑起来 `files` 一直是空的。根因不在命令而在代码：

| # | 现象 | 根因 | 修法 |
|---|---|---|---|
| 1 | `join` 返回 `{"error":"persist joined nodes: ... joined_nodes.json.tmp: no such file or directory"}`（内存里加了、重启即丢） | `saveLocked()` 直接 `WriteFile` 到 `<storageDir>/joined_nodes.json.tmp`，而 storageDir 在节点刚启动、还没上传/拉取过时**尚未创建** | `saveLocked()` 落盘前 `os.MkdirAll(filepath.Dir(d.path), 0o755)`（`back/internal/service/node_directory.go`） |
| 2 | 对方 `shares` 永远 `files:[]`，登记/上传的文件在自己 UI 里看得到、对端看不到 | `FileService.RegisterLocal()` 只写 `file_meta` + `file_providers`，**从不写 `file_index`**；而共享清单（`nodeshare.filesSnapshot` → `transport.FileIndexService.List`）和 M3 拉取的"本地已有"判定都读 `file_index` —— 清单在这一环断掉 | `RegisterLocal()` 同步 `repository.UpsertFileIndex(hash, absPath, filename, size, false)`，失败只告警不阻塞登记（`back/internal/service/file_service.go`） |

第 2 项尤值一提：它让"M2 共享清单"这条链路在**任何**部署下都拿不到文件，
但单元测试用的是注入的假 `fileList`，集成测试也没走"登记 → 清单"这条组合，
所以一直没红。**缺的是端到端组合覆盖，不是用例数量。**

### 7.4 两条配置陷阱（配错了会静默失败）

这两条来自代码里的安全边界，日志只有一行 WARN，很容易被当成噪音：

1. **共享目录必须同时在 storage 根内 AND download 根内。**
   `RegisterFolder` 校验 `cfg.StorageDir`（否则 `path outside storage root`）；
   `serveFile` 经 local source 校验 allowed root（实际是 download 根，否则
   `index path outside allowed root` → 回退按 hash 去内容寻址存储读 → 文件没
   进过 content store → `peerjs: read failed`）。
   所以把共享目录放在 `<download_root>/shared`，并让 storage 覆盖它。

2. **同一台机器上跑多个节点，必须给它们不同的 cwd。**
   `main.go:51` 硬编码 `repository.InitDB("./peerdrive.db")`，路径随进程 cwd
   解析。两个节点同 cwd ⇒ 共用同一个 SQLite ⇒ file_index 互相可见 ⇒ 拉取被判
   成 `skipped:true (already local)`，**看起来像"拉取成功"但其实没传数据**。
   这也是 §7.1 脚本给 node-a / node-b 各建一个 `run/` 目录的原因。

### 7.5 建议加入 CI 的部分

`scripts/netdisk-local-demo.sh` 目前是手工/本地用的，但它覆盖的正是单元与集成
测试都没覆盖的组合（登记 → 清单 → 拉取 → 校验）。下一步可以把它接到 CI：
起两节点、跑断言、断言必须出现 `files` 非空与 sha256 一致。这样 §7.3 的两个
缺陷下次会在 PR 阶段就红，而不是靠人跑一遍才发现。

### 7.6 这几个功能各由哪些测试组件兜底

上面每个功能点，对应仓库里的哪些自动化测试：

| 功能（§7.2） | 兜底的测试组件 | 命令 / 文件 | 规模 |
|---|---|---|---|
| 节点市场（`GET /peerjs/nodes`） | service 单测 `node_directory_test.go` + 端到端脚本 | `go test -tags nosqlite ./internal/service/`、`scripts/netdisk-local-demo.sh` | service 68 / 脚本 8 断言 |
| 加入节点（含 `joined_nodes.json` 持久化） | service 单测 + 端到端脚本 | 同上（`node_directory_test.go`） | 7 |
| 我的节点 / 已加入列表 | 同上 | 同上 | — |
| 对方文件清单（share 帧） | transport 单测 `share_test.go` + service `nodeshare_test.go` + 集成 `share_protocol_test.go` + 端到端脚本 | `go test -tags nosqlite ./internal/transport/`、`./test/integration/` | transport 78 · service 68 · 集成 21 |
| 跨节点拉取（`POST /p2p/pull`） | service `peerpull_test.go` + 集成 `peer_pull_test.go` + **端到端脚本（含 sha256 校验）** | 同上 | — |
| 传输任务 / 进度 / 取消 | controller 单测 + 集成 + 端到端 | `go test -tags nosqlite ./internal/controller/` | 41 |
| 前端网盘界面 | front vitest `tests/netdisk.test.jsx` | `cd front && npm test` | 32（前端合计 88） |
| 纯浏览器消费端 | `packages/peerdrive-client` node --test + demo 页面 | `npm test` / `npm run demo` | 60 |

**关键提醒**：上表右边任何一列全绿，都**不等于**这个功能真的能用 ——
「登记 → 清单 → 拉取」这条跨模块组合只有端到端脚本会跑（§7.3 就是这么漏的）。
改这一条链路时，`scripts/netdisk-local-demo.sh` 是必跑项，不是可选项。

全部测试组件（含怎么选、命令、CI 映射、盲区）见
**[`doc/testing/README.md`](testing/README.md)**。

---

## 8. 现在能做到什么、怎么验证（2026-09-20 实测）

这一节是**实测记录**，不是设计说明：下面每条都写了「用什么命令验」和「这次跑出来的结果」，
换了机器/改了代码请照着命令重跑，别照着结论信。

### 8.1 能力矩阵

| # | 能力 | 验证方式 | 本次结果 |
|---|---|---|---|
| 1 | **公共面板在线可用**（GitHub Pages） | `node packages/peerdrive-client/scripts/verify-pages.mjs` | 5/5 PASS（骨架 / bundle 20 API / peerjs 取到 / 混合内容提示 / 无 JS 报错） |
| 2 | **面板 file:// 直连节点拉文件** | `SIG_HOST=<IP> SIG_PORT=9100 NODE_ID=node-a node packages/peerdrive-client/scripts/verify-panel.mjs` | 9/9 PASS（连上 → 清单 2 项 → 点保存真下载 → 预览有内容 → sha256 一致） |
| 3 | **wss 信令**（HTTPS 页面唯一可行路径） | 见 §8.3 | PASS（PeerJS 经 wss 完成握手拿到 id） |
| 4 | **节点市场 / 加入 / 清单 / 拉取**（HTTP API） | `bash scripts/netdisk-local-demo.sh` | 8/8 PASS（市场 → 加入 → 清单 → 拉取 → sha256 校验），连跑两次都绿 |
| 5 | **节点管理台 UI**（`front/`，需后端） | `curl http://<IP>:5173/` + §7.2.2 | 200；市场/清单/任务 API 均返回预期数据 |
| 6 | 单元测试/分层回归 | `bash scripts/test-layers.sh` | 9 层全绿 / 43s |
| 7 | CI | `gh run list` | 三条 workflow（CI / Go Build Matrix / Deploy Pages）全 success |
| 8 | 发版门禁 | — | ❌ **没有**（`ci.yml` 不触发 tag，`release.yml` 不跑测试） |

### 8.2 一键链路脚本的坑：重跑前必须先清干净

`scripts/netdisk-local-demo.sh` 假设自己是 9100/3001/3002 的唯一主人。
**如果上一次的进程还在跑**，脚本新起的 server 会因端口占用直接退出，而后面的
curl 全部打到**旧进程**上 —— 于是你看到的现象是：

- 市场里能看到 node-a（其实是旧进程），join 也"成功"；
- 但共享清单是旧的（hash 对不上刚登记的文件）；
- 拉取任务显示 `done 300000/300000`，落盘路径也打印了，**文件却不存在** → 脚本报「未落盘」。

这种失败极具误导性（2026-09-20 实测踩到）。已在脚本开头加了端口清理，
现在重跑是幂等的（连续两次全绿）；如果手写命令起环境，记得先 `scripts/netdisk-local-demo.sh --stop`。

> 另一个坑：脚本用 `setsid nohup` 起的服务会**持有调用方的管道写端**，
> 所以 `bash scripts/netdisk-local-demo.sh | tail` 这种写法可能到脚本跑完还不返回
> （实测挂了 9 分钟才回显）。要拿到输出就重定向到文件：
> `( bash scripts/netdisk-local-demo.sh > /tmp/demo.out 2>&1 </dev/null & )`。

### 8.3 wss 怎么验

```bash
# 1) 自签证书 + 起 wss 信令（生产请用真证书或 TLS 反代）
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -keyout key.pem -out cert.pem   -subj "/CN=<IP>" -addext "subjectAltName=IP:<IP>"
peersignal -addr :9101 -key peerjs -tls-cert cert.pem -tls-key key.pem

# 2) 浏览器侧探针（自签证书要 ignoreHTTPSErrors）
#    断言 PeerJS 能经 wss 完成握手拿到 id —— 这一步通了，HTTPS 面板就能连自托管信令
```

### 8.4 明确的边界（做不到 / 未做）

- **在线面板连不到 `ws://` 信令**：HTTPS 页面的混合内容规则，需 wss（§7.2.1 前提 3）。
- **端到端脚本未进 CI**：只能在本地跑（§4 盲区清单里风险最高的一项）。
- **发版无测试门禁**：打 tag 时 `ci.yml` 不触发。
- `signalserver`(23) / `p2p_bt`(7) 是独立 go.mod 且无 CI job —— 改坏了 CI 照样绿。
