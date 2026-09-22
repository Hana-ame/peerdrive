# Peerdrive 开发路线图（用户定序）

> 来源：用户 2026-09-20 明确指定的**严格优先级顺序**。本文件是排期与取舍的依据。
> 注意与 `doc/archive/report/ROADMAP.md` 区分：那份是 v3.0 历史路线图（已实现），本文件是当前排期。

## 顺序

| # | 阶段 | 一句话 | 状态 |
|---|------|--------|------|
| 1 | **基于 PeerJS 的互联** | 节点之间能互相发现并建立 WebRTC 直连 | 🟢 基本就绪（补了节点级存在房间 + 面向用户的节点目录/加入） |
| 2 | **文件** | 内容寻址存储 + 文件索引 + 文件传输原语 | 🟢 基本就绪 |
| 3 | **组合起来** | 互联 × 文件 = 跨节点拉取/服务文件 | 🟢 基本就绪 |
| 4 | **管理链路** | 管理面（控制通道）：对节点/文件下发指令 | 🟡 部分就绪 |
| 5 | **文件范围管理** | 哪些文件/目录纳入管理、以什么范围对外可见 | 🟢 本节点侧就绪（跨节点受限内容仍等第 7 阶段） |
| 6 | **上传下载保存** | 上传 / 下载 / 落盘保存的完整闭环 | 🟢 基本就绪（跨节点保存 + 进度/取消 + 无节点消费端） |
| 7 | **身份管理** | 账号、认证、授权（谁是谁、谁能做什么） | 🔴 设计就绪、未实现 |

先做上面的。下层不稳不越级——比如「文件范围管理」要依赖「管理链路」下发范围，
「身份管理」要依赖前面各阶段先把「归属」用节点身份跑通。

## 硬约束：第 7 阶段之前不引入账号依赖

「身份管理最后」不是"最后才写代码"，而是**前 6 阶段不得把账号当路径上的前置条件**：

- 互联、文件、管理链路一律按**节点级**（peerId）工作，不查询 regserver 也能全功能运行；
- 需要"归属"概念时先用 peerId 承担；第 7 阶段再把 `账号 ↔ peerId` 的映射叠加上去；
- 现存的一处体现：匿合集的「仅限指定权限」档位目前只在同节点内可用
  （远端同步请求还不带请求者身份），跨节点分享受限合集要等第 7 阶段
  —— 见 `doc/REFACTOR.md` §3.16 已知限制与 `doc/modules/auth/API-DESIGN.md` §6。

---

## 1. 基于 PeerJS 的互联

**目标**：两个节点在没有任何人工配置的情况下能互相发现并建立直连；
连接断开能自愈；连接数有上限；状态可观测。

**已就绪**
- 信令：`back/peerjs`（独立模块，PeerJS 协议客户端）+ `back/signalserver`（自托管信令，含内置发现 API）；
- 连接建立：`transport/peerjs_service.go` 的 `connectLoop`（拨号 + 指数退避重连 + 30s 连接超时）
  与 `onIncomingConnection`（被动接受）；`bindConn` 双向互拨去重（UUID 字典序，两端保留同一条物理连接）；
- 信令断线自愈：`Signaller.Done()` → `startLoop` 整轮重连；
- 发现（HTTP，自托管）：`transport/http_discovery.go` announce（30s 心跳）+ 10s 轮询。

**本阶段已修（2026-09-20）**
- **节点级互联缺失**（阻塞性）：发现是**内容分片制**——只 announce/查询自己关注的
  collection hash 房间。默认配置 `PEERDRIVE_MQTT_COLLECTIONS` 为空 → 既不 announce
  也不查询任何房间，**发现完全空转**，两个默认配置节点永远看不见对方（只有静态
  `PEERDRIVE_PEERJS_PEERS` 能互联）。
  修法：HTTP 发现额外加入固定「存在房间」`transport.PresenceRoom`（= `sha256("peerdrive/presence/v1")`，
  用 sha256 字面量而非可读名是为了兼容可能做 64hex 校验的信令实现），
  开关 `PEERDRIVE_DISCOVER_PRESENCE`（默认开）。
- **拨号预算**：存在房间让"任意节点发现任意节点"，不加限制会退化成 O(n²) 全互联；
  用一直闲置的 `PEERDRIVE_MAX_PEERS` 兜上限（静态 PEERS 不受限，那是运营者显式声明）。

**缺口（待办）**
- 连接状态可观测性弱：只有 `GET /peerjs/node`（id + peers 列表）与信令服务器的 dashboard；
  节点侧没有"互联健康度"（每对端的 RTT / 最后收帧时间 / 重连次数）。
- 发现结果不区分节点能力：`announce` 已有 `nodeType`/`loadInfo` 字段但节点端发的是常量
  `"go-persistent"`，无法按能力筛选。**部分改善**：`loadInfo.shares` 已带上共享**数量**
  （合集/文件/目录各几个，见第 5 阶段），市场卡片能显示能力摘要。
- `CollectionHashes()` 只读配置、不含本地存储合集（注释曾声称"配置 + 本地存储"）——
  ✅ **已定性解决（第 5 阶段）**：不再需要广播本地合集 hash。改为 announce 只报**数量**、
  具体清单只在对端直连后经 `share` 帧点对点获取——清单里带的是公开可见内容的 hash，
  不再等于向全网公开"本节点持有什么"。
- MQTT 发现（公共 broker）不加存在房间：公共 broker 上开全局房间等于向公网广播本节点。

**本阶段已做（2026-09-20 网盘目标 M1）**
- 面向用户的**节点目录**：`service.NodeDirectory` + `GET /peerjs/nodes`（在线 ∪ 已加入，
  离线也保留，避免"我加的节点消失了"的错觉）、`GET /peerjs/nodes/joined`、
  `POST/DELETE /peerjs/nodes/join`。
- **加入节点**是持久化的（`<storageDir>/joined_nodes.json`，临时文件 + rename 原子写）
  且成为常驻对端：`SetExtraPeers` 让它在信令重连后自动拨号，**不受发现拨号预算限制**
  （那是运营者显式声明的节点，不是发现撞见的陌生节点）；加入后立即 `EnsureConnection`
  拨号，不等下一次发现轮询。

**验收**：新增集成测试 `TestInterconnectViaPresenceRoom`（零共享 collection 的两个节点
仅靠存在房间互联）、`TestNodeMarketListsDiscoveredPeer`（互联后出现在对方市场列表，
且自身不在列表）；单元测试锁住存在房间常量、拨号预算语义、joined 清单持久化。

## 2. 文件

**已就绪**：内容寻址存储 `storage/{h[:2]}/{h}`、`file_index`（sha256 → 绝对路径 + seq 游标）、
帧协议 verb `create/upload/list/info/delete/sync`、`source` 统一多源路由。

**缺口**：见第 5、6 阶段（范围与上传下载闭环）。

## 3. 组合起来

**已就绪**：`serveFile` 多源路由（本地 → 对端 → URL 模板）；`FetchFromPeer` 出站拉取
（含连接 churn 重试，见 REFACTOR §3.17）。

**缺口**：跨节点读受限内容的身份传递（第 7 阶段）。

## 4. 管理链路

**已就绪**：本地 WS 会话（`/ws/peer`）+ admin verb（内部转发 gin engine，复用全部 HTTP controller）；
**仅本地 WS 可用**，WebRTC 上不实现 admin verb（防权限暴露）。

**缺口**：跨节点管理（把指令下发给**远端**节点）尚未有设计——第 7 阶段之前只能靠
节点级信任；`forward`（端口转发 v2）是目前唯一的跨节点受控通道，可作参考。

**本阶段已用到的**：网盘目标新增的全部管理端点（节点目录/加入移出、对方共享清单、
拉取任务）都挂在 `/ws/peer` 的 admin 帧上，即"浏览器 → 本节点"这一段走的是已有管理链路；
跨节点那一段走的是 `share`/`req` 帧（**不是** admin verb——WebRTC 上不实现 admin，
防权限暴露）。

## 5. 文件范围管理

**本节点侧已就绪（2026-09-20 网盘目标 M2）**。

之前只有防御性的 `IsPathAllowed`（拒绝越权路径），没有**面向用户的"我对外提供什么"声明模型**。
现在有了：

- 配置声明共享范围（**默认全部关闭**，不显式开启就不对外暴露任何清单）：
  `PEERDRIVE_SHARE_ENABLE`（默认 `false`）、`PEERDRIVE_SHARE_COLLECTIONS`（hash 列表或 `all`）、
  `PEERDRIVE_SHARE_DIRS`（目录列表，空 = **不共享文件**，不是"共享全部"）。
- `share` 帧（`transport/share.go`）：`{type:"share"}` → `{type:"share-resp",
  collections:[…], files:[…], dirs:[…], total:N}`。**不复用 `list`**——`list` 是本地管理索引
  （file_index 全量、含本机绝对路径），语义是"我在管理哪些文件"；把 `list` 当共享清单用
  等于默认全盘对外公开。未开启共享时回**空数组**而不是 err（空态是合法业务状态）。
- 受限/私有合集一律跳过：`share` 帧不带请求者身份（硬约束：第 7 阶段前不引入账号依赖），
  放出去等于公开，所以只回"本来就允许公开"的内容。
- 文件只回 basename + 相对路径，不回本机绝对路径。
- `announce` 上报共享**数量**摘要（`loadInfo.shares`），市场卡片有引导信息但不泄露内容。

**仍未做（等第 7 阶段）**：跨节点受限内容的身份传递——远端请求不带请求者身份，
所以「仅限指定权限」档位目前只在同节点内可用。

**验收**：集成 `TestShareProtocolContract`——A 显式共享合集 → B 经 `share` 帧拿到完全一致的
清单（含条目 path/hash/mime）→ 用条目 hash 直接拉取内容成功 → 未开启共享的节点回空清单。
单元 14 个覆盖默认关 / `all` 只带 public / 跳过受限私有 / 非法 hash 忽略 /
目录前缀边界（`/data/share2` 不被 `/data/share` 命中）/ 帧字段名契约。

## 6. 上传下载保存

**上传**已有：`upload` verb（连接级流式 + 落盘 + hash 校验）+ 管理面 multipart 上传。

**跨节点下载保存已就绪（2026-09-20 网盘目标 M3）**——

- `service.PeerPuller`：任务式拉取（形态参考 BT 下载：列表/进度/取消）。
  流 → `<DownloadDir>/pulled/<相对路径>.part` → 校验 sha256 → rename → `file_index.Create`
  登记。登记后既能出现在"我的文件"，也能被本节点继续 `serveFile` 服务给别的节点。
- 本地已有同 hash → 跳过（内容寻址去重，不白下一遍）。
- 路径清洗（`../`、绝对路径、Windows 保留字符）+ 目标绝对路径前缀双保险。
- 并发闸 3、任务表上限 200（只裁已结束的）、取消 = ctx + `Close(reader)`。
- 合集批量保存：`StartCollection` 当场向对端要一次 `share` 清单再逐条建任务，
  坏条目独立失败不拖垮整批。
- 端点：`GET /p2p/pull`、`POST /p2p/pull`、`POST /p2p/pull/collection`、
  `POST /p2p/pull/cancel`（全静态路径——gin 不允许同级路由同时有静态段与参数段）。
- 无节点消费端：`packages/peerdrive-client`（纯浏览器、零依赖，走同一套 `share`/`req` 帧）。

**仍未做**：断点续传（协议支持 `offset`，续传逻辑未实现）、失败自动重试策略、
落盘位置可配置（当前固定 `<DownloadDir>/pulled/`）。

**验收**：集成 `TestPeerPullSavesToLocalDrive`——A 持有内容 → B 拉取保存 → 内容一致 +
索引可查 + 二次保存跳过 + B 能把保存下来的文件继续服务给 C。单元 10 组覆盖
落盘/跳过/hash 不符/路径穿越/取消/批量部分失败/入参校验/清洗规则/列表顺序/任务表裁剪。

## 7. 身份管理

**设计就绪、未实现**：`doc/modules/auth/API-DESIGN.md`（JWT/账号、用户↔节点目录、
流量统计防谎报、加密信道）+ `README.md` 索引。落地顺序建议见该文档 §10。
