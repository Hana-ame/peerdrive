# 架构分层与归属指南（AOP 切面）

> 2026-08-18 · 新增代码「放哪一层」的决策文档。判断标准一句话：**机制 → peerjs；
> 协议语义 → transport；业务 → controller/service；管理收口 → admin 切面**。
> 与本项目其他文档的关系：REFACTOR.md 是重构记录（坑/决策），本文是「层归属规则」。

---

## 1. 分层总览（8 个切面）

```
┌─────────────────────────────────────────────────────┐
│  ① 信令/传输原语层  back/peerjs（独立 go.mod）       │
│     PeerJS 信令、WS/WebRTC 连接、流控、帧发送         │
│     ← 零业务知识，可独立复用（github.com/Hana-ame/   │
│       go-peerjs，主 go.mod replace 引用）            │
├─────────────────────────────────────────────────────┤
│  ② 帧协议层  internal/transport                     │
│     会话状态机、verb 分派、req 拉取、上传、端口转发    │
│     ← 知道「帧/verb」，不知道「业务语义」             │
├─────────────────────────────────────────────────────┤
│  ③ 管理面切面  transport/admin.go + router 装配      │
│     仅本地会话 → 内部转发 gin engine                 │
│     ← 横切：把管理操作统一收口，复用全部 controller   │
├─────────────────────────────────────────────────────┤
│  ④ 业务核心  controller → service → repository      │
│     HTTP 语义业务（文件/合集/认证/BT/IPFS/同步）      │
│     ← 不感知自己在被 WS 帧转发还是 HTTP 直接调        │
├─────────────────────────────────────────────────────┤
│  ⑤ 数据切面  repository（SQLite file_index 等）      │
├─────────────────────────────────────────────────────┤
│  ⑥ 发现切面  mqtt_discovery / http_discovery /      │
│     signalserver（自托管信令 cmd/peerserver）         │
├─────────────────────────────────────────────────────┤
│  ⑦ 外部能力切面  p2p_bt（go-peerdrive-bt 独立库）     │
├─────────────────────────────────────────────────────┤
│  ⑧ 前端切面  front/src/ws.js（WS 客户端）            │
│     api.js（业务语义封装）                           │
└─────────────────────────────────────────────────────┘
```

## 2. 依赖方向（单向、无环，勿破坏）

```
peerjs ← transport ← router ← controller ← service ← repository
   ↑                        ↑
   └──── admin 内部转发 ─────┘    （③横切 ②④，方向单向：transport→router→controller）

⑥ 发现  ←  transport 消费（PeerJSService 装配）
⑦ 外部  ←  service/controller 消费
⑧ 前端  ←  独立进程，只与 ② 的 WS 会话 + ③ admin verb 通信
```

**规则**：高层可依赖低层；低层绝不依赖高层。若新增代码需要「反向引用」（如
transport 里 import controller），说明归错层了。

## 3. 判断标准（三问定层）

新增功能先回答三个问题：

| 问题 | 答案 → 归属 |
|---|---|
| 它改变「**怎么传**」（通道/缓冲/流控/帧发送原语）？ | → **① peerjs** |
| 它定义「**传什么**」（新 verb、帧格式、连接状态机）？ | → **② transport** |
| 它实现「**为什么传**」（具体能力、业务规则）？ | → **④ controller/service** |
| 它是「**管理操作收口**」（本地管理本节点，非数据流）？ | → **③ admin 切面** |

典型追问：**这个功能需要改帧协议吗？**
- 需要 → 它属于 ② transport（帧协议是 transport 层的私有物）
- 不需要、只动缓冲/通道 → ① peerjs
- 纯规则/数据组织 → ④

## 4. 归位决策表（常见功能落点）

| 功能 | 归属层 | 落点（文件/模式） |
|---|---|---|
| 节点间文件互传（req 拉取） | ② transport | 已有：inbound.go（应答）+ outbound.go（发起）+ conn.go（分派） |
| 新增数据 verb（push/对端上传/续传） | ② transport | conn.go 分派加 case；inbound/outbound 各自实现角色 |
| 分片/断点续传（协议级） | ② transport | 扩展现有 verb 语义，保持「头+二进制块原子连续」约束 |
| 底层流控/缓冲改进 | ① peerjs | connection.go 的 SendFrame（已内置低水位流控） |
| 新传输通道（QUIC/直接 UDP 替代 WebRTC） | ① peerjs | transport.go 的 DataChannel 接口——新实现只换底层，上层零改动 |
| BT 数据经 peerjs 互传 | ④ + ② | service 组织业务，经 ② 的 FetchFromPeer/requestFile 语义 API 走数据面 |
| 管理面发起的传输（「推送给某节点」） | ③ 入口 + ② 执行 | admin verb 只收口管理操作，实际数据仍走 ② 的 verb（admin 有 64MB 上限，不承载持续数据流） |
| 新发现方式 | ⑥ | 实现 Discovery 接口，PeerJSService 装配 |
| 前端新管理界面 | ⑧ | api.js 封装 → ws.js 的 admin()/upload()/download() |
| 认证/权限细化（角色） | ③ + ④ | admin 切面加会话级校验；业务规则在 ④ |

## 5. 各层绝对禁区（违反即归错层）

| 层 | 禁止 |
|---|---|
| ① peerjs | 认识任何业务 verb、import internal/*、出现「文件/合集/BT」等业务词 |
| ② transport | import controller/service/repository；实现具体业务规则 |
| ③ admin | 承载持续二进制数据流（>64MB 直接拒，大文件走 ② req verb） |
| ④ 业务核心 | 直接操作 WebSocket/peerjs 连接（只能经 ② 的语义 API） |
| ⑥ 发现 | 传输业务数据（只交换 peerId/连接信息） |
| 前端 | 直接 fetch 本地 HTTP 端点（除 /ws/peer 升级外；LEGACY 路由仅供旧客户端/curl） |

## 6. 现有代码的边界核对（2026-08-18 现状）

- **admin.go**（③）内部构造 *http.Request → gin engine ServeHTTP，复用全部
  controller——这是「收口」而非「业务」，符合 ③ 的定义
- **conn.go**（②）的 serveAdmin 按会话 ID 拒绝非本地连接——权限决策在协议层
  入口，业务层无感知，正确
- **peerjs_service.go**（② 装配）持 peerjs.Peer + 连接管理——不写业务逻辑，正确
- **controller 双入口**（HTTP 直接调 + admin 内部转发）是设计意图（零重复），
  不是违规：业务层只认 *http.Request，不感知来源
- 唯一要注意的边界：**端口转发（forward.go）** 语义上偏「机制」，但协议级
  verb（fwd-open/challenge/...）在 ② 是合理的——它的 verb 语义属于 transport，
  底层隧道实现若需独立可抽到 peerjs 之上、transport 之下

## 7. 改动流程建议

新增能力时按此顺序动代码：

1. 三问定层（§3）→ 确定落点
2. 若落 ②：先在 conn.go 头注释核对帧协议约束（atomic 头+块、expect 状态机、
   请求超时/中止语义），再写分派 + 角色实现
3. 若落 ①：只动 peerjs 模块（独立 go.mod），跑 `cd back/peerjs && go test ./... -race`
4. 若涉及管理面：admin 只做入口转发，数据流仍走 ②
5. 测试与文档：对应层单测 + REFACTOR.md 补记录（坑/决策），本表如有新功能
   类型一并补录
## 8. 维护者视角的五组模块（2026-08-19 文档分组）

> 该分组不改变代码结构，也不改变 §1–§7 的分层规则；只用于日常讨论、
> 仓库索引和 PR 归类时快速定位。它与 §1 的 8 个切面是“同一系统的两个视图”。

| 分组 | 主要代码范围 | 对应 §1 分层 | 职责摘要 |
|---|---|---|---|
| **文件 Source 模块** | `internal/source`、`internal/downloader`、`internal/transport/file_index.go` | ② + ⑤ 为主 | 文件从哪来/去哪：本地、Peer、URL、统一管理器、下载、索引、上传会话 |
| **控制模块** | `internal/controller`、`internal/service`、`internal/repository`、`internal/model` | ④ + ⑤ | 业务控制、服务编排、持久化、模型定义；不感知自己是否被 WS 帧转发 |
| **Peer 模块** | `internal/transport` 的 PeerJS 核心：`peerjs_service.go`、`conn.go`、`inbound.go`、`outbound.go`、`file_index.go` | ② | 节点身份、帧协议、入站/出站请求语义、文件索引同步 |
| **网络连接模块** | `internal/transport` 的连接载体：`ws_session.go`、`rtc_session.go`、`http_discovery.go`、`mqtt_discovery.go`、`forward.go`；`back/peerjs`、`back/signalserver` | ① + ⑥ | 底层连接、信令、发现、端口转发隧道 |
| **路由（核心）** | `internal/router`、`internal/transport/admin.go` 的装配关系 | ③ + 路由本质 | 把所有模块组合起来：HTTP 路由、集合/文件分派、admin 内部转发、源路由装配 |

边界备注：

- `file_index.go` 物理位于 `internal/transport`，但它语义上更偏文件 Source/存储索引；
  文档分组归到 Source，代码位置暂不迁移。
- `admin.go` 物理也在 `internal/transport`，但它是“管理操作通过本地 WS 收口到
  gin/controller”的横切面，文档分组归到路由/装配。
- `Peer` 与 `网络连接` 的边界是：Peer 讲“协议和语义”，网络连接讲“底层连接和信令”。
