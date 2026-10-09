# 后端按访问形态分层架构地图

> 本文档对应 Issue #88：整理后端按访问形态分层的模块地图与接缝分析。只做现状架构梳理与规范指引，不侵入改动运行时代码。

## 1. 四种访问形态总览

| 形态 | 承载协议 / 介质 | 信任级别与边界 | 典型传输链路 | 核心用途 |
|---|---|---|---|---|
| **1. 本地访问 (Local)** | 进程内调用 / FS / SQLite / CAS | **高信任**（仅限本地进程与已授权的存储读写集合） | 直接调用 `repository` / `pathutil` / `source.Source` | 索引增删改查、CAS 文件读写、本地 Collection 解析 |
| **2. P2P 访问 (PeerJS)** | PeerJS 信令 + WebRTC DataChannel | **可信/非可信对端网络**（受 PSK / ShareScope 门禁约束） | 帧协议 (`req/meta/data/done`, `share`, `pull`) | 跨节点文件分发、数据点对点直传、节点间索引同步 |
| **3. WebSocket 管理 (WS)** | 本机 WebSocket (`/ws/peer`) | **严格仅限本地**（Admin 仅绑定 Local WS Session ID） | WS 帧协议 (`admin`, `admin-resp`, `admin-bin`) 转发 Gin 路由 | Web UI 管理面、前端流式交互、运维状态查看 |
| **4. HTTP 端点面 (HTTP)** | HTTP / HTTPS REST API | **按需认证**（JWT / AdminToken / Anon 公开） | Gin Router → Middleware → Controller → Service | 外部 HTTP API、第三方集成、静态资源与公开合集消费 |

---

## 2. 后端包与访问形态映射表

| 目录/包路径 | 主归属形态 | 是否跨形态 | 跨形态接缝 / 依赖说明 | 备注 |
|---|---|---|---|---|
| `back/internal/repository/` | **本地访问** | 是 | 被 Service/Controller/MCP 调用；含 `file_index` 与 SQLite 存储 | 基础持久化层 |
| `back/internal/pathutil/` | **本地访问** | 是 | 全局唯一的路径安全与边界判定 (`Within`, `Safe*Any`) | 防目录穿越核心护栏 |
| `back/internal/collection/` | **本地访问** | 否 | 纯文档模型与校验逻辑 | 内部纯计算 |
| `back/internal/downloader/` | **本地访问** | 是 | 统一调度底层 Source 获取流并落盘 | 取数调度引擎 |
| `back/internal/source/` | **本地访问 / 接缝** | **核心接缝** | 定义 `source.Source` 接口；将 Local, URL, PeerJS, ECH 统一为取数源 | 访问形态的关键抽象接缝 |
| `back/peerjs/` (独立模块) | **P2P 访问** | 否 | 独立 PeerJS 协议客户端实现与 DataChannel 管理 | 独立 repo: `go-peerjs` |
| `back/internal/transport/` | **P2P 访问 / 混装** | **是 (历史混装)** | 承载 `peerjs_service.go` (P2P)、`psk.go` (准入)，同时也承载了 `admin.go` (WS 管理面转发) | 历史包膨胀，未来建议按通道拆分 |
| `back/internal/wsconn/` | **WS 管理** | 否 | WebSocket 会话生命周期与底层帧通道管理 | 已从 transport 拆出 |
| `back/internal/httpd/` | **HTTP 端点** | 否 | HTTP 监听地址规范化、端口绑定基础设施 | 监听网络层 |
| `back/internal/router/` | **HTTP 端点** | 是 | 注册全局 HTTP 路由；挂载 `/ws/peer` WS 升级点 | 路由总装配 |
| `back/internal/controller/`| **HTTP 端点** | 是 | HTTP 处理句柄；被 `admin.go` (WS 管理面) 通过虚拟 Gin 请求无缝复用 | 控制器层 |
| `back/internal/service/` | **业务领域服务** | 是 | 业务用例（NodeMarket, Puller, AnonService 等），供 Controller / WS 复用 | 纯业务逻辑层 |
| `back/internal/services/` | **基础设施装配** | 是 | `peerdrive all` 的 UnifiedMux、反代与多服务端口合并 | 与 `service` 名字近似（历史痕迹） |
| `back/internal/serverapp/`| **进程总控** | 是 | `peerdrive` 单二进制的依赖组装、生命周期启动与安全自检 | 启动总入口 |

---

## 3. 核心接缝与架构规范

### 接缝 1: `source.Source` 数据源接口
- **定义**：无论底层是本地文件、HTTP 远端链接、还是跨 WebRTC 节点的 Peer 传输，统一收敛为标准 `Source`（`Fetch`, `Size`, `Available`）。
- **收益**：上层下载调度与媒体流播放只认数据源接口，避免直接感知下层 P2P 或网络协议。

### 接缝 2: 本地 WS 管理面与 HTTP 控制器复用 (`transport/admin.go`)
- **原则**：WebRTC 数据通道严禁开通 `admin` verb（防止远端 peer 越权控制本地节点）。
- **接缝**：`admin` verb 仅限回环/本地 WebSocket 握手建立的会话。WS 请求通过内存构造 Gin Context 直接打到现有 HTTP Controller，保证管理 API「只写一份」，杜绝 HTTP 与 WS 接口实现漂移。

### 遗留债务与演进建议（暂不改动）
1. `internal/service/` 与 `internal/services/` 并存：前者为领域业务用例，后者为多服务合并基础设施，未来宜将后者重命名为 `infraservices` 或并入 `serverapp`。
2. `internal/transport/` 历史职责混杂：包内既有 P2P PeerJS 业务，又有 WS admin 转发。未来宜拆分为 `internal/transport/peer/` 与 `internal/transport/adminws/`。
