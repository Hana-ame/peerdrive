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
