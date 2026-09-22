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
  - `PEERDRIVE_SHARE_DIRS`：逗号分隔目录，目录内的已登记文件进共享。
    **位置不受限**：可以在 storage 根之外、另一块盘、另一个挂载点；
    反过来，没写进这里的目录一律拒绝（防任意文件读写）。见 §7.4；
  - `PEERDRIVE_SHARE_ENABLE`（默认 **false**）总开关——默认不共享任何东西。
  - 上面三项只是**首次启动的初值**：运行时可经 `GET/PUT /peerjs/share`、
    `POST /peerjs/share/files` 改（按 hash 勾单个文件、按目录、按合集），
    落盘 `storageDir/share_scope.json`，重启后仍在。见 §12。
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

# 专项：共享目录放在 storage 根之外（另一块盘/另一个挂载点）也必须能访问
./scripts/netdisk-sharedir-outside.sh
```

脚本做的事：编译 `back/cmd/server` → 起自托管信令 `peersignal :9100` → 起
`node-a`（`PEERDRIVE_SHARE_ENABLE=true`）和 `node-b`（纯消费）→ 依次验证
市场/加入/清单/拉取/内容校验，逐步 PASS/FAIL。完全脱外网（不碰公共信令与 IPFS）。

拓扑：

```
peersignal :9100（信令 + 内置 /discover 发现，仅转发 SDP/ICE）
   ├── node-a :3001  storage=/tmp/pddemo/a/root  download=<root>/downloads
   │                 SHARE_DIRS=<root>/downloads/shared   ← 共享这一个目录
   │                 （可换成任意路径，只要在 SHARE_DIRS 里声明过）
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

**内网多人用（推荐，ws 就够）**：```npm run serve:panel``` 起一个静态服务，
把 `http://<本机IP>:8124/` 发给同网段的人 —— **http 页面发起 `ws://` 不会被拦截**
（浏览器的混合内容规则只管 HTTPS 页面），所以信令不用配 TLS。

```bash
cd packages/peerdrive-client
PORT=8124 HOST=0.0.0.0 npm run serve:panel    # → http://<本机IP>:8124/
```

**在线版（公网，必须 wss）**：<https://hana-ame.github.io/peerdrive/>

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

1. **共享目录必须在 `PEERDRIVE_SHARE_DIRS` 里声明过——位置则随意。**
   登记侧（`FileService.isPathAllowed`）与读取侧（`FileIndexService.IsPathReadable`、
   `NodeShare.underShareDir`）都只认「运营者声明过的根目录」：storage 根 ∪
   `PEERDRIVE_SHARE_DIRS` ∪ `PEERDRIVE_DOWNLOAD_DIR`。没声明的目录 →
   `path outside storage root`（HTTP 400）；声明过的目录在**任何位置**都行
   ——storage 之外、另一块盘、另一个挂载点都可以。
   想验证这条跑 `scripts/netdisk-sharedir-outside.sh`（它把共享目录放在 storage
   根之外跑完整链路，并反向验证未声明目录仍被拒）。

   > 历史坑（2026-09-20 已修）：曾经读取侧复用了**写/登记**边界，于是共享目录
   > 不在下载目录下时会出现「登记成功、清单列得出、对端一拉 `read failed`」——
   > 读取侧把它判成越权，回退到并不存在的内容寻址副本。根因是同一个"路径是否
   > 越权"的判断在 `FileService` / `FileIndexService` / `NodeShare` 各写一份且
   > 已经跑偏。现在三处统一到 `back/internal/pathutil`（跨平台：分隔符、
   > Windows 大小写折叠、跨盘、符号链接），并把读/写两个边界显式拆开。

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
| 3 | **面板 http 托管 + ws 信令**（内网多人用） | `PORT=8124 HOST=0.0.0.0 npm run serve:panel` 然后 `PANEL_URL=http://<IP>:8124/ ... node scripts/verify-panel.mjs` | 9/9 PASS（连上 → 清单 → 保存 → 预览 → sha256） |
| 3b | **wss 信令**（仅在线 HTTPS 版需要） | 见 §8.3 | PASS（PeerJS 经 wss 完成握手拿到 id） |
| 4 | **节点市场 / 加入 / 清单 / 拉取**（HTTP API） | `bash scripts/netdisk-local-demo.sh` | 8/8 PASS（市场 → 加入 → 清单 → 拉取 → sha256 校验），连跑两次都绿 |
| 5 | **节点管理台 UI**（`front/`，需后端） | `curl http://<IP>:5173/` + §7.2.2 | 200；市场/清单/任务 API 均返回预期数据 |
| 6 | 单元测试/分层回归 | `bash scripts/test-layers.sh` | 9 层全绿 / 43s |
| 7 | **E2E CI** | `gh run list --workflow e2e.yml` | ✅ success（链路 8 项 + 面板浏览器 9 项，同一套环境顺序跑） |
| 8 | CI | `gh run list` | 四条 workflow（CI / Go Build Matrix / E2E / Deploy Pages）全 success |
| 9 | **发版门禁** | `gh workflow run release.yml --ref refactor -f dry_run=true` | ✅ success（gate 2m39s → 5 平台构建；dry_run 不发 release） |
| 10 | **PSK 门禁**（谁能连我的节点） | `bash scripts/netdisk-local-demo.sh`（第 [6] 步）+ `PSK=demo-psk ... verify-panel.mjs` | ✅ A 带密钥 → B 无密钥被拦 → B 带同一把密钥恢复；面板带密钥 9/9 |
| 11 | **面板下指令：本地入库（`put`）** | `PSK=demo-psk SIG_HOST=<IP> SIG_PORT=9100 NODE_ID=<node> node scripts/verify-panel.mjs` | ✅ 14/14（含"入库 → sha256 一致 → 按 hash 取回校验通过"） |
| 12 | **面板下指令：网络入库（`pull`）** | 同上，另给 `PULL_URL=<公网 URL>` | ✅ 公网 URL 抓回 75KB 并入库；内网地址被 SSRF 防护拒（`pull: 禁止拉向内网/本机地址`） |
| 13 | **面板自动搜索在线节点（`discoverNodes`）** | 同上（脚本第 [8] 步自动跑） | ✅ 列出 3 个在线节点，含 nodeType/集合数/心跳时间；跨域没开时错误信息点名 CORS |

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

- **只有 HTTPS 托管的面板才需要 wss**：混合内容规则只拦 HTTPS 页面发起的 `ws://`。
  `file://`（已测 9/9）和 http 页面（已测 9/9）用 ws 完全没问题 —— 所以**内网/本机用 ws 就够**，
  公网 Pages 版（HTTPS）才必须 wss。
- `signalserver`(23) / `p2p_bt`(7) 是独立 go.mod，主 CI 无 job —— 改坏了 `Peerdrive CI` 照样绿。
  （发版门禁 `release.yml` 的 gate 会跑 signalserver，算半覆盖；`p2p_bt` 仍无 CI。）

## 9. PSK 门禁：谁能连我的节点（2026-09-20）

### 9.1 为什么需要它

面板是**公开的静态页面**（GitHub Pages / `file://`），信令只负责牵线、不做准入 ——
任何人拿到节点 id + 同一个自托管信令就能握手上来问 `share`、拉文件。
所以准入只能长在节点自己这条连接上，这就是 `PEERDRIVE_PSK`（预共享密钥）。

### 9.2 语义（对称、两端各自独立判定）

| 本节点 | 对端 | 结果 |
|---|---|---|
| 没配 | 没配 | 谁连上都服务（老行为，向后兼容，升级不会把存量对端全拒了） |
| 配了 | 同一把密钥 | 正常服务 |
| 配了 | 没配 / 错了 | 所有入站 verb 回 `err`，带 `code=PSK_REQUIRED` |
| 没配 | 配了 | 本端照常服务它（它多带了密钥，忽略） |

注意最后两行是**非对称**的：门禁保护的是「配了它的那个节点」的出站内容。
我配了而对端没配时，**我自己发起的拉取不受影响**（对端是开放的，它照常服务我）。
实现上只拦"对端要我干活"的 verb（`req/share/list/create/upload/…/fwd-*`），
**不拦**"对端对我请求的应答"（`meta/data/done/err`）—— 连 default 分支也上锁会自伤：
对端从没出示过密钥（它不需要），于是我自己的拉取被自己掐死，只剩超时，日志里看不出所以然。

### 9.3 握手形状

```
连接建立 → 配了 PSK 的一端立刻发 {"type":"psk-auth","psk":"<密钥>"}
          → 对端校验 → {"type":"psk-ok"} / {"type":"psk-err","code":"PSK_REQUIRED"}
```

两个刻意的选择：

1. **明文传密钥**。DataChannel 强制 DTLS，密钥不会在链路上裸奔；服务端本来就要
   存明文才能比对。要防的是"陌生人连上来"，不是窃听 —— 换挑战-应答只把明文挪出
   信道，却要引入 nonce 状态与双端发起时机，不值。
2. **不等 `psk-ok` 就发业务帧**。同一条 DataChannel 保序，出示方先发 auth 再发业务，
   服务端按序必然先看到 auth —— 于是**不给每次连接多加一个 RTT**。
   （这条是顺序契约，改实现时 auth 必须是本端第一帧。）

### 9.4 怎么用

```bash
# 节点侧：mesh 内要互通的节点配同一把（不设 = 开放模式）
PEERDRIVE_PSK=demo-psk ./peerdrive-server

# 面板：连接表单的「预共享密钥」框；或用链接预填 ?psk=xxx
#       —— 面板读入后立刻把 psk 从地址栏抹掉，不写回、也不进 localStorage
#       （地址栏会被历史/截图/分享带走，localStorage 是明文且跨会话常驻）

# 消费端包
PD.connectToPeer(Peer, id, { psk: 'demo-psk' })
```

状态自查：`GET /peerjs/node` 现在回 `{"psk":true,"psk_peers":N}` —— "对端拉不到东西"
时第一个该看的就是这里。

### 9.5 怎么验（都实测过）

```bash
# 1) 真实网络（一键脚本第 [6] 步：A 带密钥重启 → B 无密钥被拦 → B 带密钥恢复）
bash scripts/netdisk-local-demo.sh
#   PASS A 报告已开启 PSK 门禁（/peerjs/node 里 psk:true）
#   PASS B 没带密钥 → 被门禁拦下（error: peerjs: psk: 本节点需要预共享密钥…）
#   PASS B 带同一把密钥 → 恢复（2 个文件）

# 2) 浏览器（面板带密钥过门禁）：9/9 PASS
SIG_HOST=<IP> SIG_PORT=9100 NODE_ID=node-a PSK=demo-psk node scripts/verify-panel.mjs
#   反例（不带 PSK）：清单 0 行 + "psk: 本节点需要预共享密钥" —— 门禁确实拦住了

# 3) 单元：back/internal/transport/psk_test.go（8 例）
#          packages/peerdrive-client/test/psk.test.mjs（9 例，钉"auth 必须是第一帧"）
```

### 9.6 边界（它不是什么）

- **不做身份、不做授权分级**：所有持钥者对这个节点有同等访问权。要按人区分权限
  得走注册服务器鉴权（`doc/modules/auth`），不是这里。
- **只管连接准入，不管信令准入**：谁都能在信令上看到这个节点的 id。
  想让节点不出现在公共目录里，关 `PEERDRIVE_DISCOVER_PRESENCE`。
- 密钥错了**不关连接**，而是每个 verb 都回一次 err —— 让对端能重发正确的密钥，
  也让它看到明确原因（关连接只会变成超时，更难排查）。

---

## 10. 面板"下指令"：把内容放进节点（2026-09-20）

前九节讲的都是**读**（拉别人的东西）。这一节是反方向：面板在一条已建立的
DataChannel 上向节点下三条指令 —— 自动搜索、本地入库、网络入库。

### 10.1 三条指令走什么

| 指令 | SDK 入口 | 协议动词 | 谁在干活 |
|---|---|---|---|
| 自动搜索在线节点 | `discoverNodes(sig)` | 无（信令 REST `GET /discover/nodes`） | 信令 —— 与 WebRTC 连接无关，**没连任何节点也能搜** |
| 本地入库 | `client.put(file)` | `upload`（文本头 `upload` + 二进制分片，服务端回 `meta`/`ack`/`uploaded`） | 浏览器按 64KB 分片逐步推送，节奏由节点说了算 |
| 网络入库 | `client.pull(url)` | `pull` → 应答 `pulled{hash,size,name,path}` | 节点替你去抓这个 URL 并入库 |

三条都返回 `{hash,size,name,path}`：`hash` 是内容地址，之后在任何地方都能用它取回。
`put` 失败时节点返回的 err 会原样透传（PSK 门禁没填密钥 → `PSK_REQUIRED`；
`pull` 撞上层 SSRF 防护 → `PEER` + 具体原因）。

### 10.2 两条硬约束（改之前先读）

1. **自动搜索要求信令能跨域**。面板是公共静态页（`file://` 原点为 `null`，
   托管在 Pages 又是另一域），而这一步是跨域 `fetch`。信令没回
   `Access-Control-Allow-Origin`，浏览器会连响应一起吞掉，端到端只剩一句
   `TypeError: Failed to fetch`（状态码根本拿不到）。面板的错误信息会直接点名这个头，
   并说明在此之前"手填节点 id 连接"仍然可用。自托管信令请升到 2026-09-20
   之后的 `go-peerserver`（已放开 CORS + OPTIONS 204）。
2. **一条连接同时只能有一个上传流**。串行传多个文件；并行会在第二份的第一个
   头就收到 `already in progress`。

### 10.3 网络入库的安全边界（`pull` 不是"节点替你翻墙"）

`pull` 是第一个"对外地址由别人指定"的动词，因此是默认最受管的一个：

- 只放行 `http`/`https`；带用户名密码、非 http(s) 一律拒；
- 拒绝 loopback / 私有网段 / link-local（含 IPv4-mapped IPv6 与 `169.254.169.254`）；
- **重定向逐跳重新校验**（`CheckRedirect`），跳到内网的重定向同样被拒；
- `Content-Length` 先行判断 + 写入侧长度上限（默认 100MB），超限是**报错**，
  不是静默截断；入库前失败会清理掉半成品。

所以"节点拒绝某个 URL"绝大多数是**策略性拒绝**，不是故障。面板会在这种情况
额外提示一句，避免用户对着一个注定失败的地址反复重试。

### 10.4 怎么验

```bash
# 前置：节点必须是**带 pull 的实现**（老二进制不认识这个动词，表现是不应答 →
# 面板显示 TIMEOUT 而不是拒绝）。重编：
#   cd back && go build -tags nosqlite -o /tmp/pddemo/bin/server-pull ./cmd/server

cd packages/peerdrive-client
npm test                 # 98/98（put/pull/discoverNodes 共 20 例）
npm run build:panel      # 改了 src/ 或 panel/ 必须重建（CI 的 check:panel 会拦）

# 端到端（真浏览器，点对点、node-c 这种开了 PSK 的节点要带密钥）
PSK=demo-psk SIG_HOST=<IP> SIG_PORT=9100 SIG_SECURE=0 NODE_ID=node-c \
  PULL_URL=https://hana-ame.github.io/peerdrive/ PW_CHANNEL=msedge \
  node scripts/verify-panel.mjs
# 断言：连上 → 清单 → 保存 → 预览 → sha256 一致
#     → 自动搜索列节点 → 本地文件入库且 hash 与本地一致 → 按 hash 取回校验通过
#     → 内网 pull 被 SSRF 拒 → 公网 pull 入库成功
```

### 10.5 边界

- **`put` 会把整个内容读进浏览器内存**再分片上传（超过 256MB 面板会先警告）。
  超大文件请自行切片。
- **入库 ≠ 自动出现在别人的清单里**。共享清单是"已登记到共享范围内的文件"
  （见 `nodeshare.filesSnapshot` 的前缀过滤），刚入库的内容未必在里面；
  要证明它真的可取回，用任务里的「取回校验」按 hash 拉一次。

---

## 11. 路径边界与穿透测试（2026-09-20）

节点对外暴露的接口里，**有五个入口接受调用方路径**，每一个都通向不同的系统调用：

| 入口 | 谁可控 | 通向 | 漏了会怎样 |
|---|---|---|---|
| `create` verb（PeerJS/WebRTC） | 对端 | `os.Stat` + 登记索引 | 任意文件读（登记后经 `req` 取） |
| `upload` / `write-file` 的 `name` | 对端 | `filepath.Join(uploadDir, ...)` 落盘 | 任意文件写 |
| `POST /files/register_local` | HTTP 调用方 | `os.Open` | 任意文件读 |
| `POST /files/register_folder` | HTTP 调用方 | `os.ReadDir` 遍历 | 任意目录列举 + 批量任意文件读 |
| `GET /files/browse` | HTTP 调用方 | `os.ReadDir` | 任意目录列举 |
| `POST /files/copy` 的 `dest_path` | HTTP 调用方 | `MkdirAll` + `WriteFile` | **任意文件写**（最危险） |

五个共用**同一份判定**：`back/internal/pathutil`（`Within` / `WithinAny`）。
判定集合分两套，不要合并：

- **写/登记边界** `FileIndexService.IsPathAllowed` / `FileService.isPathAllowed`
  = storage 根 ∪ `PEERDRIVE_SHARE_DIRS` ∪ `PEERDRIVE_DOWNLOAD_DIR`。
  其中 `IsPathAllowed`（verb 侧）只有 download 根——对端不能往你对外共享的目录里写。
- **读取边界** `FileIndexService.IsPathReadable` = download 根 ∪ storage 根 ∪
  运营者声明过的共享目录。

### 11.1 穿透测试怎么分布

不是"加几个用例"，而是**四层各一份矩阵**，共 162 条断言：

| 层 | 文件 | 覆盖 |
|---|---|---|
| 判定本身 | `internal/pathutil/traversal_test.go` | `..` 逃逸、兄弟目录同名前缀、NUL、软链（文件/目录/悬空/根自身）、跨盘、UNC、`\?\`、ADS、8.3 短名、`/proc/self/root`、空根、配置拆分 |
| verb 层 | `internal/transport/traversal_test.go` | `create` 拒绝、`write-file` 的 `name` 必须落在 uploadDir 内、共享根目录里的软链逃逸、`redactDisallowedPath` 脱敏 |
| service 层 | `internal/service/traversal_test.go` | 同一份 payload 打四个入口；`copy` 额外断言"磁盘上什么都没多出来" |
| HTTP 层 | `internal/controller/traversal_test.go` | **编码形态**（`%2e%2e%2f`、`....//`、`..%5c`、`\u002f`） |
| 真二进制 | `scripts/netdisk-traversal-probe.sh` | 对着跑起来的节点打 payload，含磁盘复核 |

```bash
cd back && go test -tags nosqlite -run TestTraversal ./internal/...
./scripts/netdisk-traversal-probe.sh
```

### 11.2 测出来的两个真问题（都已修）

1. **`CopyFile` 的边界不是第一道门。** 它先 `GetFileMeta(hash)` 再校验目标路径，
   于是越权 `dest_path` 返回的是 `source hash ... not found`——
   ① 拒绝原因被掩盖，运维照日志会当成数据问题；
   ② 安全边界不在最前面，将来谁在上面加一段"源不存在就自动去拉"的逻辑就退化成任意文件写；
   ③ 顺带泄露"某个 hash 存不存在"。
   现在 `isValidHash` + `isPathAllowed` 提到最前。

2. **`sanitizeName("..")` 返回 `..`**。`filepath.Base("../../x")` 是 `x` 没问题，
   但 `Base("..")` 还是 `..`，`Join(uploadDir, "..")` 指到 **uploadDir 的父目录**。
   靠 `Create` 的边界兜住了，但那层依赖顺序太脆——已在 `sanitizeName` 里直接挡掉。

顺带加固：`pathutil.normalize` 显式拒绝含 NUL 的路径（Go 的 `os.Open` 也会拒，
但纯字符串判定不认 NUL，不能让"碰巧靠系统调用挡住"成为唯一防线）。

### 11.3 四条"已知没防住"——已修（2026-09-20）

之前写在这里的四条，现已全部落地。**别再把它们当"没做"**：

| 原来的缺口 | 现在怎么做 | 代码 |
|---|---|---|
| **TOCTOU**（`EvalSymlinks` 之后才 `os.Open`，中间能换软链） | 解析与打开合成一步：先 `normalize` 归一化，再用 **Go 1.24 的 `os.Root`** 打开（`openat2(RESOLVE_BENEATH)`，其它平台按目录 fd 逐个分量走 `O_NOFOLLOW`）——这就是当初说的"得上 `O_NOFOLLOW`/`openat`" | `pathutil.SafeOpen` / `SafeOpenAny`；四个读取入口全部改用它 |
| **硬链接**（没有方向，`EvalSymlinks` 认不出来） | 拒绝"有多个名字"的普通文件（已开 fd 上的 `fstat` 取 `nlink > 1` 即拒）。登记入口有两个（对端 `create` 与 HTTP `register_local`），**两处共用 `pathutil.RejectHardlink`**，只判一处等于留口子 | `pathutil.RejectHardlink`；`PEERDRIVE_ALLOW_HARDLINKS=1` 放行（pnpm `node_modules`、`cp -l` 备份这类目录需要） |
| **root 配成 `/`**（`Within("/", "/etc/passwd")` 是 true——那是配置意图，不是穿透） | 只能在**启动期**按配置意图拦：`checkUnsafeRoots` 检查 storage / download / 每个 share dir，配置成卷根就直接拒绝启动并点名是哪个环境变量 | `cmd/server/main.go` + `pathutil.IsUnsafeRoot`；`PEERDRIVE_ALLOW_UNSAFE_ROOT=1` 可越过 |
| **Windows 专属 payload 在 Linux CI 上 `t.Skip`** | CI 的 Windows 那一格**不再跳过 `go test`**（`.github/workflows/go-build.yml`），并把写死的 POSIX payload（`/etc/passwd`）换成按平台取值——否则 Windows 上它不是绝对路径，会被拼进 storage 根内，判定放行、断言假绿 | `systemAbsolutePath()`（controller）、platform 分支（service） |

> §11.3 那条硬链接防线后续又补了一刀：Windows 原先**没有**硬链接数可用（详见
> §11.5 第一项），现已通过对打开句柄调 `GetFileInformationByHandle` 补齐。

### 11.4 真机 Windows 第一次跑，挖出来的四个 bug

Linux CI 全绿不代表 Windows 没事。把测试在 Windows 上真跑一遍，当天就抓出四个
**只在 Windows 上成立**的问题（都是"Linux 上根本看不出症状"的类型）：

1. **`filepath.Rel` 在 Windows 上自己就折叠大小写。** `path/filepath` 的
   `sameWord` 在 Windows 上是 `strings.EqualFold`，逐分量比较时 `C:\Temp` 与
   `c:\tEMP` 算同一个。所以"折叠大小写"这套逻辑在 Windows 上是 Rel 干的，
   `foldCase` 取什么值都不改变结果（与 NTFS 一致，是正确的）。
   原来那条断言"折叠关闭时不应放行"在 Windows 上永远不可能成立。
2. **`filepath.Base("/")` 在 Windows 上返回 `\`，不是 `/`。** `sanitizeName`
   只挡了 `"/"`，于是 `Join(uploadDir, "\")` = uploadDir 自己 → "is a directory"。
   现在两种分隔符都挡（`pathutil` 之外还在 `sanitizeName` 里补了 `\`）。
3. **删文件前没关句柄。** Windows 上打开着的文件删不掉
   （"being used by another process"），`os.Remove` 静默失败：
   - 管理面上传的临时文件：`os.Remove(au.path)` 写在 `au.f.Close()` **之前**
     → 每个被中止的上传在磁盘上留一份垃圾（收口到 `adminUploadState.cleanupTemp`）；
   - 分片上传 `Complete()` 之后句柄要留到 10 分钟后的 reap 才关 → 文件删不掉、
     移不动。现在完成后立刻关句柄，并新增 `FileIndexService.Close()` 供测试收摊。
4. **发布出去的二进制在 Windows/macOS 上一启动就死。** 发布流程对所有平台都
   `CGO_ENABLED=0`，而 `mattn/go-sqlite3` 在无 cgo 时退化成 stub（第一次执行
   SQL 才报错），于是 `InitDB` 建表失败、进程直接退出。现在按 cgo 是否可用
   二选一：有 cgo 用 mattn，没有就用纯 Go 的 `modernc.org/sqlite`
   （`repository/db_driver_cgo.go` / `db_driver_pure.go`，驱动名分别是
   `sqlite3` / `sqlite`，所以 `sql.Open` 不能再写死）。
   顺带：CI 的 Windows 那格也就不需要 mingw 了。

Windows 上跑测试的方法（本机没装 Go 也能验：交叉编译出 `.test.exe` 直接跑）：

```bash
cd back
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -tags nosqlite -c -o /tmp/x.test.exe ./internal/pathutil/
# 把 x.test.exe 拷到 Windows 上，cd 到该包目录后直接执行（测试里的相对路径依赖 cwd）
```

### 11.5 第二条没防住的清单——也已修（2026-09-20 晚）

上一版写在这里的四条同样已经落地。**别再把它们当"没做"**：

| 原来的缺口 | 现在怎么做 | 代码 / 怎么验 |
|---|---|---|
| **Windows 上拿不到硬链接数**（那条防线在 Windows 上是空的） | 不再从 `os.FileInfo` 取（`Win32FileAttributeData` 里根本没有 nlink），改成对**已打开的句柄**调 `GetFileInformationByHandle` 拿 `NumberOfLinks`。`RejectHardlink` 的入参由 `os.FileInfo` 换成 `*os.File`，两个登记入口本来就有开着的 fd，顺手还少一次路径解析 | `pathutil.NlinkOf` / `links_windows.go` / `links_unix.go`；Windows 真机 `TestTraversal_CreateHardlinkRejected`、`TestTraversal_RegisterLocalHardlinkRejected` 现在**不再 `t.Skip`** |
| **写路径上的 TOCTOU**（copy / delete / upload 落盘仍按路径操作） | 写也 Root 化：新增 `SafeWriteFileAny` / `SafeOpenFileAny` / `SafeMkdirAllAny` / `SafeRemoveAny`，在允许根上开 `os.Root` 后**解析与落盘一次完成**；`FileService.Delete` 的 `os.Remove(p.Path)`、`CopyFile` 的 `MkdirAll+WriteFile`、`Upload` 的 `Rename`、分片上传的 `Abort/reap` 全部改用它 | `pathutil/safewrite.go`；`safeopen_write_test.go` 里每条都是**竞态形状**（先判合法 → 换成软链 → 再落盘），且都带一条**对照臂**证明旧的 `os.WriteFile` 确实会写穿软链 |
| **8.3 短名没有专项用例** | 新增 Windows 专有用例，并顺手修出一个真 bug：Windows 的 `EvalSymlinks` 只会替**已存在**的路径还原短名，写操作的路径最后一段不存在 → `Within` 会把"同一个目录的另一个名字"判成越权（配长名共享目录、用短名访问时文件读不出来）。现由 `GetLongPathName`（逐段回退：最深的成功前缀用长名，后面不存在的部分原样保留）统一到长名 | `pathutil/shortname_windows.go` + `shortname_other.go`（非 Windows 恒等）；`shortname_windows_test.go`。本机实测 8.3 生成是开着的（`C:\Program Files → C:\PROGRA~1`） |
| **`os.Root` 打不开 exotic 文件系统时静默 fail closed**（表现为"这个目录共享不了"） | 三件事：**分类**（ unsupported / 不存在 / 没权限，各自不同下一步）、**启动自检**（`warnUnsupportedRoots`，起不来/共享不了在日志里说清楚并给 remedy）、**显式的逃生阀** `PEERDRIVE_ROOT_FALLBACK=1`（默认关闭；开了会在日志里持续告警"已退回按路径判定，存在 TOCTOU 窗口"） | `pathutil/rootprobe.go` + `scoped.go`（Root 与降级两种模式统一成一个操作面）；`cmd/server/main.go` 启动自检 |

关于最后一条的实测结论（本机 WSL2，2026-09-20）：`/tmp`、`/home`、DrvFs 的
`/mnt/c`、`/mnt/d`、`/mnt/e`、procfs、sysfs、devtmpfs **全部**能建立 `os.Root`——
唯一失败项是 `/root`（permission denied）。也就是说现实里最常见的"该目录共享不
了"其实是**权限**问题，以前日志只有一句 `open root ...: permission denied`，
现在会告诉你去查 owner/ACL。真正不支持的金属 host 请按上面的逃生阀处理。

#### 11.5.1 两个只在别人机器上才红的形状（darwin / windows runner）

这类 bug 的共同点：**本机跑不出来，只有 CI 那格红**。两处都是"同一个目录被写成
两种形式"，只是成因不同：

**(a) 软链根 + 叶子不存在**（`macos-latest/arm64` 红）

`TestTraversal_RedactDisallowedPath` 在 `macos-latest/arm64` 上失败、其它平台全绿。
根因不是 macOS 的判定语义不同，而是**它的临时目录本身是软链**：

- macOS 的 `t.TempDir()` 是 `/var/folders/...`，而 `/var` → `/private/var` 是软链；
- `normalize(root)`：root 存在 → `EvalSymlinks` 成功 → `/private/var/...`；
- `normalize(path)`：path 的最后一段（`a.txt`）**还不存在** → `EvalSymlinks` 失败 →
  保持 `/var/...` 原样；
- 于是同一个目录被写成两种形式，`filepath.Rel` 算出一串 `..` → 判成越权。

这在真实部署里也是 bug：共享目录配在软链路径下时，目录里的文件会"看得见却读不出来"。
修法是按**最长存在前缀**解析（`pathutil.resolveBestEffort`）：能解析多深就解析多深，
后面不存在的部分原样拼回，保证 root 与 path 永远落在同一套写法上。
回归用例 `TestTraversal_SymlinkedRootMissingLeaf`（自建软链，Linux 上也能复现；
摘掉修复它会红，已实测）。

**(b) 允许根自己是 8.3 短名**（`windows-latest` 红 3 个用例）

Windows runner 的 `t.TempDir()` 是 `C:\Users\RUNNER~1\AppData\Local\Temp\...`。
写路径的 `pickRoot` 只还原了 **path** 的短名、没还原 **root** 的 →
`RUNNER~1` 与 `runneradmin` 被当成两棵树 → 明明在根内的写被判
`path outside allowed root`。修法：`pickRoot` 对允许根也做同样的短名还原
（读路径 `SafeOpen` 两侧都走 `normalize`，本来就是一致的）。

回归用例 `TestSafeWriteFile_ShortNameRootAlias`（自建"共享目录的短名"当允许根，
本机用户名没有 8.3 别名也能复现；摘掉修复会红）。

> 两条教训：① 跨平台判定要**自己造形状**（软链、短名），不能指望平台自带——
> 本机恰好不具备那个条件时，用例会恒绿地测了个寂寞；② 修完一条要**看全部格子**，
> 前面几格被 cancel 掩盖的失败会在下一次跑的时候才冒出来。

补一条务实的经验：**默认 fail closed 是对的**（宁可少给不能多给），但把"不支持"
与"没权限/不存在"混为一谈才是排障成本的大头——`RootUnavailable` 只认
`EINVAL`/`ENOSYS`/`ENOTSUP` 这一类，`ENOENT` 与 `EACCES` 都不许被归成
"文件系统不支持"，否则运维会往 OS 兼容性方向白查半天。

## 12. 共享范围：自由选择共享什么（2026-09-22）

M2 落地时共享范围只能靠 `PEERDRIVE_SHARE_*` 在启动时定死：想多共享一个文件，
要么把它挪进共享目录，要么改环境变量**重启节点**。这节把它改成运行时可改。

### 12.1 为什么必须能运行时改

"我愿意把哪些内容给出去"本来就是随手的决定：刚上传一个文件想立刻给朋友、某
个目录不想再给了。要求重启等于把这个决定变成一次运维动作，实际结果只有两种——
长期共享一个过宽的目录（图省事），或者干脆不开共享（嫌麻烦）。两种都比"随手勾
选"更糟。

### 12.2 三条来源取并集

| 来源 | 粒度 | 怎么选 |
| --- | --- | --- |
| `dirs` | 整个目录 | `PUT /peerjs/share {"dirs":[...]}`；file_index 里路径落在其中的文件全部共享 |
| `files` | 单个文件（按 hash） | `POST /peerjs/share/files {"hashes":[...],"shared":true}`；**不必在任何共享目录里** |
| `collections` | 合集 | 64hex 合集 hash，或 `all` = 全部 public 合集 |

`enable` 是总开关：关 = 对外完全空清单（**已勾选的内容保留**，再开不用重选）。

### 12.3 环境变量降为"初值"

- 首次启动（storage 下没有 `share_scope.json`）时，环境变量播种进运行时状态并落盘；
- 之后**以落盘状态为准**：运营者取消掉的共享项不会在重启后复活
  （"我明明取消了共享"是最难自查的一类反馈）。
- 落盘文件损坏：保留原文件（人工可查），按"没保存过"处理，不阻塞启动。

### 12.4 端点

```
GET  /peerjs/share        当前范围 + 可选文件清单（每行带 shared / by_dir / level）
PUT  /peerjs/share        局部更新 {enable?,dirs?,files?,collections?,friends?}（未传的保持原样）
POST /peerjs/share/files  {hashes:[...], shared:bool, level?}  单行勾选/改级别
```

条目形如 `{"id":"<目录路径|文件hash|合集hash>","level":"public|unlisted|private"}`；
也接受纯字符串（级别按 public，兼容本次升级前落盘的 `share_scope.json`）。
`GET` 额外回 `selected`（纯 id 列表，方便渲染）与 `levels`（三档取值）。

三个端点都挂 `AuthRequired`（未配注册服务器时放行 = 单机模式）。命名与
"问对端要清单"的 `GET /peerjs/nodes/:peer/shares` 刻意区分开。

### 12.5 两个不改就会出事的点

1. **新增的共享目录必须注册成 file_index 可读根**（`main.go` 的 `SetDirHook`）。
   只进范围不注册可读根 → "清单列得出、对端一拉 read failed"，与 §11 那个坑同源。
2. **卷根目录在入口就被拒**（`PUT {"dirs":["/"]}` → 400）。
   `pathutil.Within` 会把 `/etc/passwd` 判成"在根内"——判定没错，是配置意图错了，
   而这里的值来自 HTTP 请求体，一次误填就是共享整个盘。

### 12.6 共享级别：public / unlisted / private（2026-09-22）

每条共享声明（目录 / 单文件 / 合集）都带一档级别，回答"给谁"：

| 级别 | 出现在共享清单里 | 谁能下载 |
|---|---|---|
| `public` | 是 | 连得上就行 |
| `unlisted` | **否** | 连得上就行（知道 hash，即"链接分享"） |
| `private` | 否（好友除外） | **只有自己和好友** |

一句话记法：**public = 列出来也给；unlisted = 不列出来但给；private = 只给认识的人。**

判定与接线：

- 常量与合并规则在 `internal/model/share_level.go`；**多条来源命中同一内容时取
  最宽松**（`LoosestLevel`）：目录 `unlisted` + 单文件 `public` ⇒ 该文件 `public`。
  取最严会让"我特意放宽了这一个"静默失效。
- 清单侧：`NodeShare.SnapshotFor(peerID)`。share 帧走的是已建立的连接，对端 id
  已知，所以**好友能看到 private 条目**（否则给了权限却没给目录）。`unlisted`
  永不列出。`announce` 上报的数量用匿名视角（`SnapshotFor("")`），别把"我有几个
  只给好友的东西"广播出去。
- 下载侧：`transport.ShareGate` → `serveFile` 里判。**只有 private 挡人**：
  public / unlisted / **未声明** 一律放行。
- "自己" = 不经 P2P 的本地通道（HTTP 管理 API / 本机 WS 直连，
  `WSSession.IsLocal`）；"好友" = `ShareScope.Friends`（节点 ID 白名单，大小写不
  敏感），环境变量 `PEERDRIVE_SHARE_FRIENDS` 只是初值。

两条边界，别放宽：

1. **peer id 是对端自报的，信令不校验**（第 7 阶段前没有账号体系）。所以好友名单
   只在**设了 PSK** 的网络里才可靠——没设 PSK 时谁都能连上并自称是好友。PSK 是
   准入门禁，级别是在进门之后的分级。
2. **"未声明"仍可下载**：内容寻址取回（知道 hash 就能取）是这套系统的既有行为。
   改成"必须声明才能取"会连"上传 → 按 hash 取回校验"这种基本自检都过不去。
   `unlisted` 与"没声明"的差别是**它被显式声明了**：管理台看得见、能审计、能统计，
   将来真要收紧下载时也不会被误伤。
3. 级别写错（`"pubilc"`）**整批拒绝**（400），绝不兜成 public——那等于把本想限制
   的内容公开出去。显式改级别是**覆盖**（`public → private` 必须改得动），
   不是取并集。
4. 合集自身的 `visibility` 非 public（restricted/private）时按 **private** 处理：
   AccessList 是账号列表，无身份校验不了，只给好友。

### 12.6.1 消费者侧：固定身份与分享链接

三档级别要有用，还得补两个"人能用得上手"的口子（2026-09-22）：

1. **面板必须有固定 id**。好友名单认的是对端自报的 peer id，而 peerjs 默认每次
   `new Peer` 都随机一个——名单里填一个明天就变的 id 等于没填，`private` 会退化成
   "谁都拿不到，包括你答应给的那个人"。所以面板自己生成 `pd-panel-<随机>` 存进
   localStorage，右上角可复制、可更换；id 撞车（同一浏览器开两页）时信令报
   `unavailable-id`，面板换一个重试并在日志里说明原因。
   ⚠️ 启动早期（`var $ = …` 还没赋值）**不能碰 DOM**：那时调 `renderMyId()`
   会抛 `$ is not a function` 并中断整个 IIFE，面板整页失效。`persistMyId`（只落盘）
   与 `saveMyId`（落盘 + 重画）因此分开。
2. **unlisted 要有出口**。它的语义是"不列出、凭 hash 可取"，得有个把 hash 交出去
   的动作：清单每行有「链接」按钮，生成 `?node=<id>&hash=<64hex>&auto=1`。
   收到链接的人连上后，这条内容单独列在共享清单**上方**——它**故意不依赖清单**
   （unlisted 按定义不在清单里，只在清单里找的话，用户看到"该节点没有共享内容"
   就走了，而东西一直拿得到）。链接里不含 PSK。

### 12.7 兜底测试

- `back/internal/service/nodeshare_scope_test.go`：运行时选择压过环境变量、
  卷根/非法 hash 整批拒绝、单文件勾选与目录共享互不干扰、`by_dir` 标记、
  局部更新不改未传项、目录回调只通知新增、损坏文件回退配置。
- `back/internal/service/nodeshare_level_test.go`：三档边界（public 列出 /
  unlisted 不列但能取 / private 挡陌生人、放好友与自己）、取最宽松、
  非法级别整批拒、级别与好友随重启保留、历史字符串条目读作 public、
  合集 visibility 降级。
- `back/internal/transport/peerjs_service_test.go`
  `TestServeFile_PrivateDeniedToStrangers`：门禁接线在 `serveFile` 上真的生效
  （陌生人 err、好友与自己拿到数据）。
- `front/tests/netdisk.test.jsx` `pages/Drive 共享勾选`：勾一个文件发的是 hash
  列表（不是整份范围）、总开关只发 `enable`、端点不可用时隐藏控件；
  `pages/Drive 共享级别`：未共享不显示下拉、改级别保持 `shared=true`、
  好友名单按逗号拆分、下拉回填当前级别。
- `front/tests/e2e-admin-smoke.mjs`：真实节点上 读 → 勾 → 复核 → 改级别 →
  写好友 → 拒拼错的级别 → 拒卷根。
- `packages/peerdrive-client/test/panel-contract.test.mjs`：面板的两条契约——
  连出去必须带本端 id、分享链接绝不拼进 psk、复制要有降级路径、启动早期不碰 DOM。
  （面板是 file:// 下的 IIFE，跑不起来，只能这样钉住"改坏了静默失效"的那几条。）
- `packages/peerdrive-client/scripts/verify-panel-share.mjs`：真浏览器端到端
  （需要节点 + 信令 + playwright）：固定 id → unlisted 不在清单但链接可取 →
  private 拒陌生人 → 固定 id 进好友名单后同一个页面立刻能取。
