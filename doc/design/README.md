# peerdrive 模块化设计文档（doc/design/）

> peerdrive（PeerJS/WebRTC P2P 文件网络，Go 后端 + React 前端）的**模块 + 连接**双视角设计文档。
>
> - 每个模块一份 [`modules/NN-*.md`](modules/)：逻辑 / 如何储存 / 何时储存 / 储存什么
> - 每对模块连接一份 [`connections/NN-*.md`](connections/)：连接方式 / 时序 / 情况处理
> - 连接总览（连接地图、启动/停机时序、典型主流程）→ [如何连接.md](如何连接.md)
> - 连接拓扑可视化（高清矢量 SVG，零连线交叉设计）：
>   - 🗺️ **全局总览**：14 模块 / 13 连接全景图 → [connections-map.svg](connections-map.svg)
>   - 🗄️ **存储与持久化域**：物理磁盘与 SQLite 元数据落盘 → [domain-storage.svg](domain-storage.svg)
>   - 🌐 **P2P 网络与直连域**：信令发现与跨节点/浏览器 DataChannel 直连 → [domain-p2p.svg](domain-p2p.svg)
>   - 🎛️ **前端控制与路由域**：Web 管理面、WS admin 代理与内部调度 → [domain-control.svg](domain-control.svg)
>
> 配套既有文档：分层视角 [doc/layers/](../layers/)、架构决策与帧协议 [doc/REFACTOR.md](../REFACTOR.md)、部署 [doc/PEERSIGNAL.md](../PEERSIGNAL.md)、网盘链路 [doc/NETDISK.md](../NETDISK.md)。
> 生成日期：2026-09-27（基于当日代码现状；文档与代码冲突时以代码为准并回来改文档）。

## 1. 模块清单（14）

| # | 文档 | 模块 | 代码位置 | 一句话 |
|---|------|------|----------|--------|
| 01 | [modules/01-config.md](modules/01-config.md) | config | `back/internal/config/` | 配置装配与启动校验，纯内存 |
| 02 | [modules/02-repository.md](modules/02-repository.md) | repository | `back/internal/repository/` | SQLite 元数据持久化 |
| 03 | [modules/03-storage.md](modules/03-storage.md) | storage | `back/internal/pathutil/` + `back/storage/` | 内容寻址文件存储 + 路径安全 |
| 04 | [modules/04-router.md](modules/04-router.md) | router | `back/internal/router/` | HTTP 路由 / 中间件 / admin 转发 |
| 05 | [modules/05-controller.md](modules/05-controller.md) | controller | `back/internal/controller/` | HTTP 处理器面 |
| 06 | [modules/06-service.md](modules/06-service.md) | service | `back/internal/service/` | 业务逻辑编排 |
| 07 | [modules/07-source.md](modules/07-source.md) | source | `back/internal/source/` | 文件来源 + FileRouter |
| 08 | [modules/08-downloader.md](modules/08-downloader.md) | downloader | `back/internal/downloader/` | 多协议下载器 |
| 09 | [modules/09-transport.md](modules/09-transport.md) | transport | `back/internal/transport/` | P2P 会话状态机 + verb 分派 |
| 10 | [modules/10-peerjs.md](modules/10-peerjs.md) | peerjs | `back/peerjs/` | PeerJS 协议库 |
| 11 | [modules/11-signalserver.md](modules/11-signalserver.md) | signalserver | `back/signalserver/` | 信令 + 发现（纯内存） |
| 12 | [modules/12-media-node.md](modules/12-media-node.md) | media-node | `back/cmd/media-node/` + `back/ech/` | ECH 媒体链 |
| 13 | [modules/13-frontend.md](modules/13-frontend.md) | frontend | `front/src/` | Web 消费者 |
| 14 | [modules/14-nodestate.md](modules/14-nodestate.md) | nodestate | `back/internal/nodestate/` | 运行时共享状态 |

## 2. 连接清单（13）

| # | 文档 | 连接对 | 一句话 |
|---|------|--------|--------|
| 01 | [connections/01-frontend-backend.md](connections/01-frontend-backend.md) | frontend ↔ backend | 本地 WS 会话：admin verb + 文件数据帧 |
| 02 | [connections/02-router-controller.md](connections/02-router-controller.md) | router ↔ controller | HTTP 分发 + admin 内部转发 |
| 03 | [connections/03-controller-service.md](connections/03-controller-service.md) | controller ↔ service | 业务调用与错误映射 |
| 04 | [connections/04-service-repository.md](connections/04-service-repository.md) | service ↔ repository | DB 读写与事务边界 |
| 05 | [connections/05-router-source.md](connections/05-router-source.md) | router ↔ source | 来源管理注入与端点 |
| 06 | [connections/06-service-transport.md](connections/06-service-transport.md) | service ↔ transport | P2P 业务（pull/share/目录） |
| 07 | [connections/07-transport-peerjs.md](connections/07-transport-peerjs.md) | transport ↔ peerjs | 连接生命周期 / DataChannel |
| 08 | [connections/08-transport-signalserver.md](connections/08-transport-signalserver.md) | transport ↔ signalserver | 注册 / 心跳 / 转发 / 发现 |
| 09 | [connections/09-controller-downloader.md](connections/09-controller-downloader.md) | controller ↔ downloader | 多协议下载 |
| 10 | [connections/10-controller-storage.md](connections/10-controller-storage.md) | controller ↔ storage | 上传写盘 |
| 11 | [connections/11-transport-storage.md](connections/11-transport-storage.md) | transport ↔ storage | P2P 拉取落盘 + 索引 |
| 12 | [connections/12-frontend-signalserver.md](connections/12-frontend-signalserver.md) | frontend ↔ signalserver | 消费端拨号 + 发现 |
| 13 | [connections/13-media-node-ech.md](connections/13-media-node-ech.md) | media-node ↔ ech | ECH 域前置媒体链 |

> 注：media-node 与 ech 同属模块 12（同一代码目录），连接 13 描述其**内部**接线。

## 3. 阅读顺序建议

1. 先读 [如何连接.md](如何连接.md) —— 拿到连接地图、启动/停机时序与典型主流程。
2. 想深入某个模块 → `modules/NN-*.md`（每份末尾有「对外连接」指路）。
3. 想改某段交互 → 对应的 `connections/NN-*.md`（连接方式/时序/情况处理）。

## 4. 文档写作规范（维护时遵守）

**模块文档**（`modules/NN-*.md`）固定小节：

1. 逻辑 —— 职责、核心类型、主要流程、生命周期
2. 如何储存 —— 介质/位置/格式/命名；纯内存态要明说「不持久化」与构成；委托下层要说明
3. 何时储存 —— 启动/请求/事件/定时/关闭五个时机的准确触发点
4. 储存什么 —— 字段/表/文件清单，标注默认值与关键约束
5. 边界与坑 —— 不变式、失败处理、已知坑
6. 对外连接 —— 指向相关 `connections/NN-*.md`

**连接文档**（`connections/NN-*.md`）固定小节：

1. 连接方式 —— 通道/协议帧/鉴权/建立方
2. 时序 —— ASCII 或 mermaid + 逐步骤说明（带代码依据）
3. 情况处理 —— 超时/断连/并发/校验失败/鉴权失败/半开/重启 等场景表
4. 相关文档 —— 交叉引用

**共同纪律**：事实必须标注代码位置（相对仓库根，可带行号）；信息不足时如实写「未核实」；改代码后行为变化要同步更新对应文档（同 AGENTS.md 编码规范）。

## 5. 相关文档

- [doc/layers/](../layers/README.md) —— 分层（L1-L8）视角的模块文档与分层测试
- [doc/REFACTOR.md](../REFACTOR.md) —— 架构决策记录、帧协议/verb 定义（§4）
- [doc/PEERSIGNAL.md](../PEERSIGNAL.md) —— 信令服务器部署与协议
- [doc/NETDISK.md](../NETDISK.md) —— 网盘链路（节点市场/共享/跨节点保存）
- [doc/NODE.md](../NODE.md)、[doc/NODE-API.md](../NODE-API.md) —— 节点概念与节点 API