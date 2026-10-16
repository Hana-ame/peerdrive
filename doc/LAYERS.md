# Architecture Layering and Membership Guide (LAYERS.md v2)

> 2026-10-09 · Master Architecture Specification · Version 2.0  
> Decision document for "which layer, which process, and which access modality new code belongs to".  
> One-line criterion: **mechanism → peerjs/wsconn; protocol semantics → transport; business → controller/service; management convergence → admin aspect**.  
> Related documents: `doc/ROADMAP.md` (development order), `doc/NETDISK.md` (drive requirements), `doc/REFACTOR.md` (refactoring log & protocol frame specs).

---

## 1. Architecture Multi-Perspective Model (三维正交视角)

Peerdrive 架构不再依赖单一的一维划分，而是由三个正交视角共同确立模块归属：

```
       [ 视角 B: 访问形态 Access Modality ]
       (Local / PeerJS / WS / HTTP)
                    │
                    ▼
┌────────────────────────────────────────────────────────┐
│         视角 A: AOP 职责切面 (Core Responsibility)      │
│  ① Primitives → ② Transport → ③ Admin → ④ Core        │
│  → ⑤ Data → ⑥ Discovery → ⑦ External → ⑧ Frontend      │
└────────────────────────────────────────────────────────┘
                    ▲
                    │
       [ 视角 C: 部署面 Deployment & Packaging ]
       (Standalone Go Modules / Single Binary / Packages)
```

### 1.1 视角 A：AOP 职责切面（8 Aspects）

| 切面 | 范围与代表包 | 职责边界与核心原则 |
|---|---|---|
| **① 传输原语层** | `back/peerjs` (`github.com/Hana-ame/go-peerjs`), `back/signalframe`, `internal/wsconn` | WebRTC DataChannel 与 WebSocket 连接管理、帧编解码、底层读写调度。**零业务语义，零 internal/* 依赖**。 |
| **② 帧协议层** | `internal/transport` (`conn.go`, `inbound.go`, `outbound.go`, `share.go`, `forward.go`) | 帧动词分发 (`req/meta/data/done/err`, `share/share-resp`, `fwd-*`)、状态机泵、背压。知晓帧格式，不知晓业务模型。 |
| **③ 管理收敛切面** | `internal/transport/admin.go`, `internal/router` 挂载 | 仅限本地 WS 会话；将 `admin` 帧内部转交 Gin 引擎 `ServeHTTP`，复用全部控制器。**严禁暴露给 WebRTC**。 |
| **④ 业务核心层** | `internal/controller`, `internal/service`, `internal/source`, `internal/downloader`, `internal/model` | HTTP/业务语义业务编排（文件/合集/分享范围 `nodeshare`/多源调度/任务）。不感知上层是直接 HTTP 还是 WS 转发。 |
| **⑤ 数据持久化层** | `internal/repository` (SQLite `file_index`, `collections`, `file_meta`), 本地 CAS (`storage/{h[:2]}/{h}`) | 数据模型 CRUD、文件索引、CAS 存储根。无网络传输逻辑。 |
| **⑥ 节点发现切面** | `internal/transport/http_discovery.go`, `internal/transport/mqtt_discovery.go`, `back/signalserver` | 内容房间与 Presence 房间发现、心跳与节点在线清单。不携带业务正文。 |
| **⑦ 外部协议切面** | `back/p2p_bt` (`github.com/Hana-ame/go-peerdrive-bt`), `internal/echcore`, `internal/echproxy`, `internal/provider` | 外部网络互通（BitTorrent BEP 44/51、ECH 出口代理、IPFS 客户端）。 |
| **⑧ 前端表现切面** | `front/src/platform/transport-ws/`, `front/src/features/`, `packages/peerdrive-client`, `packages/peerdrive-media` | 纯客户端 UI、WebRTC 消费端面板、媒体播放组件。仅通过 WS 会话与 WebRTC DataChannel 交互。 |

### 1.2 视角 B：访问形态（4 Access Modalities）

系统内的所有数据访问严格归纳为四种形态：
1. **Local（本地直读）**：
   - 本地 CAS 存储 (`storage/`)、本地 SQLite 索引 (`file_index`) 与本地声明目录；
   - 零网络协议封装，通过 `os.Root` / `pathutil` 安全边界保护。
2. **PeerJS（P2P 直连数据面）**：
   - 基于 PeerJS 信令协商的 WebRTC DataChannel 连接；
   - 承载 `req/meta/data/done`（文件拉取）、`share/share-resp`（共享清单浏览）与 `fwd-*`（安全端口转发）；
   - **绝对禁止**响应任何 `admin` 管理动词。
3. **WS（本地浏览器管理面）**：
   - 浏览器与本地节点之间的 `/ws/peer` 长连接（经 `internal/wsconn` 升级与调度）；
   - 标识为 `session.ID() == "local"`，具有执行 `admin/admin-resp/admin-bin` 动词的权威。
4. **HTTP（传统 REST / 媒体直连）**：
   - 由 `internal/httpd` 托管的标准 HTTP/1.1 端点（`/status`, `/discover/*`, `/files/download`）；
   - 主要用于服务初始化、探针与向后兼容旧客户端，前端生产环境全面收敛于 WS 与 PeerJS。

> **统一出口抽象 (Unified Egress Abstraction)**：数据输出到消费端（HTTP / WS / PeerJS 三选一）的统一分发管线与流泵抽象详见 `doc/UNIFIED-EGRESS-ABSTRACTION.md`。

### 1.3 视角 C：部署面与打包边界（Deployment Surface）

1. **主二进制可执行程序 (`cmd/peerdrive`)**：
   - 单二进制产物（`-tags nosqlite`，5 平台跨平台构建）；
   - 支持 `serve` (节点服务), `signal` (内嵌信令), `reg` (注册服务), `all` (全家桶) 四大子命令。
2. **独立 Go 模块（独立的 `go.mod` 仓库镜像）**：
   - `back/peerjs` (`github.com/Hana-ame/go-peerjs`, tag 同步)
   - `back/signalserver` (`github.com/Hana-ame/go-peersignal`, tag 同步)
   - `back/p2p_bt` (`github.com/Hana-ame/go-peerdrive-bt`, tag 同步)
   - `back/signalframe` (共享协议帧基础库)
3. **独立 NPM 客户端分发包**：
   - `packages/peerdrive-client`：零依赖，可单文件构建为独立公共面板 `dist/panel.html`；
   - `packages/peerdrive-media`：独立 WebRTC 媒体播放组件。

---

## 2. 依赖方向与单向无环原则

```text
[① 原语层: peerjs / wsconn]
       ▲
       │ (implements Session & Frame transport)
[② 协议层: internal/transport]
       ▲
       │ (assembles HTTP engine)
[③ 管理切面: transport/admin.go] ──forwards──► [internal/router]
                                                    │
                                                    ▼
                                           [④ 业务层: controller]
                                                    │
                                                    ▼
                                           [④ 核心: service / source]
                                                    │
                                                    ▼
                                           [⑤ 数据层: repository]
```

### 规则：
- **单向无环**：高层可以依赖低层，低层绝对禁止反向导入高层。
- **同层隔离**：`internal/source` 不得直接依赖 `internal/controller`；`internal/transport` 不得导入 `internal/service` 或 `internal/repository`。

---

## 3. 冲突裁决优先级（Arbitration Rules）

当新模块的设计在多个视角下发生归属冲突时，按以下优先级决断：

1. **第 1 优先级：安全性与访问形态隔离（视角 B）**
   - 任何涉及管理控制、配置修改的代码必须受限于 Local WS 形态；绝不能因为方便而暴露在 WebRTC 协议帧中。
2. **第 2 优先级：AOP 单向依赖与纯粹性（视角 A）**
   - 原语层（`peerjs`, `wsconn`）绝对不能掺杂业务语义（禁止出现 file/collection/bt 概念）；业务层不能直接操纵裸网络 Socket。
3. **第 3 优先级：部署单元解耦（视角 C）**
   - 若某组件仅服务于当前主程序且无外部复用诉求（如 `internal/wsconn`, `internal/httpd`），优先作为 `internal/*` 包；若被多个独立二进制或开源项目共享，必须剥离为独立 `go.mod`。

---

## 4. 近期新增关键模块归位录（6 个月增量回填）

| 模块 / 包 | 物理路径 | 视角 A 归属 | 视角 B 归属 | 视角 C 归属 | 核心设计意图说明 |
|---|---|---|---|---|---|
| `httpd` | `back/internal/httpd` | 基础设施宿主 | HTTP 承载 | 主程序内部包 | 封装 TCP 监听、优雅停机、健康探针与端口自协商 |
| `wsconn` | `back/internal/wsconn` | ① 传输原语层 | WS 传输 | 主程序内部包 | WebSocket 握手升级、二进制帧分包、心跳与读写调度 |
| `source` | `back/internal/source` | ④ 业务核心层 | Local / Peer / HTTP | 主程序内部包 | 统一多源内容寻址契约 (`Source` interface: local/sha/peer/url/ech/openlist/webdav) |
| `nodeshare` | `back/internal/service` | ④ 业务核心层 | PeerJS / Local | 主程序内部包 | 显式共享范围管控（目录、合集、单文件），三级权限分级 (`public`/`unlisted`/`private`) |
| `echcore` / `echproxy` | `back/internal/echcore` | ⑦ 外部协议切面 | HTTP 出口代理 | 主程序内部包 | 规避上游特定网络封锁的 ECH 出口与 IP 族策略控制 |
| `peerjs/xor` | `back/peerjs/xor.go` | ① 传输原语层 | PeerJS | 独立模块子功能 | 数据面轻量混淆，防止被深度包检测拦截 |
| 前端 WS 客户端 | `front/src/platform/transport-ws` | ⑧ 前端表现切面 | WS 客户端 | 前端内部架构 | 前端底层长连接与帧分派引擎，提供 `admin()`, `upload()`, `download()` |
| 前端功能域 | `front/src/features/*` | ⑧ 前端表现切面 | 业务交互 | 前端内部架构 | 按网盘、市场、传输等业务域组织的高层组件与状态机 |

---

## 5. 新功能落点四维判定流程

在增加任何新代码前，按以下流程判定：

```text
[ 新功能/新需求 ]
       │
       ├─► 1. [访问形态判断]：它是给谁用的？
       │      ├─ 浏览器本地管理？ ──► 走 WS admin 动词
       │      ├─ 跨节点 P2P 数据互传？ ──► 走 PeerJS 数据帧
       │      └─ 本地文件/索引？ ──► 走 Local / Repository
       │
       ├─► 2. [传输与协议判断]：是否改变了传输机制或协议帧？
       │      ├─ 改变了连接/流控/底层分包？ ──► ① 原语层 (peerjs / wsconn)
       │      ├─ 增加了新的跨节点帧动词？ ──► ② 协议层 (transport)
       │      └─ 否（复用现有帧或本地操作） ──► ④ 业务层 (controller/service/source)
       │
       ├─► 3. [业务逻辑分类]：
       │      ├─ 数据源取数？ ──► 接入 internal/source.Source
       │      ├─ 共享与网盘控制？ ──► 接入 internal/service/nodeshare
       │      └─ 数据库表结构变更？ ──► 接入 internal/repository + schema_migrations
       │
       └─► 4. [打包边界判断]：
              ├─ 是否需跨独立模块共享？ ──► 独立 go.mod 模块
              └─ 否 ──► back/internal/*
```

---

## 6. 各层绝对禁令（Checklist）

- ❌ **① peerjs / wsconn**：禁止 import 任何 `internal/*` 业务包；禁止出现业务词汇（如 file, collection, nodeshare）。
- ❌ **② transport**：禁止 import `controller`, `service`, `repository`；禁止在帧分派中直接执行 SQL 或业务修改。
- ❌ **③ admin**：禁止承载长期大二进制数据流（超过 64MB 的文件拉取必须走 ② `req` 动词）；禁止允许非 local 会话调用。
- ❌ **④ controller/service**：禁止直接操作原生 WebSocket 或 WebRTC 连接；必须通过 `transport.Session` 抽象。
- ❌ **⑥ discovery**：禁止传输业务文件内容；仅允许交换节点存在性、在线心跳与网络拓扑。
- ❌ **⑧ 前端**：禁止在生产环境下直接向后端发散发起传统 HTTP API 请求；所有管理面操作统一收敛至 WS 会话的 `admin` 帧。
