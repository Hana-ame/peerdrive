# Peerdrive 架构文档

> 基于内容寻址 + Git 风格版本管理的 P2P 文件分享系统 · v3.0

## Monorepo 结构

```
peerdrive/
├── front/                    React 前端 (Vite + TailwindCSS + Vitest)
│   ├── src/
│   │   ├── pages/
│   │   │   ├── AnonCreator/  合集创建页（三列布局：筛选 | 预览 | 编辑）
│   │   │   ├── AnonExplorer/ 合集浏览页
│   │   │   ├── Plaza/        合集广场
│   │   │   ├── FileManager/  文件管理
│   │   │   ├── P2PDashboard/ P2P 控制台
│   │   │   └── Settings/     设置
│   │   ├── components/       共享组件
│   │   └── api.js            API 客户端
│   └── tests/                前端测试
├── back/                     Go 后端 (Gin + SQLite + libp2p + BT DHT)
│   ├── cmd/server/           入口
│   ├── internal/
│   │   ├── controller/       HTTP 处理层
│   │   ├── service/          业务逻辑层 (P2P/文件/合集/下载)
│   │   ├── repository/       SQLite 持久化
│   │   ├── provider/         数据源抽象 (本地/HTTP/IPFS)
│   │   ├── router/           Gin 路由 & 中间件
│   │   ├── model/            数据模型
│   │   ├── config/           配置
│   │   └── p2p_bt/           BT DHT 实现
│   ├── pkg/hashutil/         哈希工具
│   └── test/                 集成/E2E 测试脚本
├── doc/                      项目文档（你在这里）
└── .github/workflows/        CI/CD
```

## 分层架构 (Backend)

```
HTTP API (Gin Router)
  → Controller (参数校验、响应格式化)
    → Service (业务逻辑)
      → Provider (数据源: local / http / ipfsgw)
      → Repository (SQLite)
      → P2P (libp2p / BT DHT / WebRTC)
      → IPFSService (boxo Bitswap + DHT)  ← 新增
```

| 层 | 位置 | 职责 |
|----|------|------|
| Router | `back/internal/router/` | 路由注册、CORS、Auth 中间件 |
| Controller | `back/internal/controller/` | HTTP 处理、参数解析 |
| Service | `back/internal/service/` | 核心逻辑：文件注册/下载、合集 CRUD/版本、P2P 传输/信令、**IPFSService (boxo Bitswap)** |
| Repository | `back/internal/repository/` | SQLite CRUD |
| Provider | `back/internal/provider/` | 数据源接口：`local` / `http` / `ipfsgw`（IPFS 优先走 Bitswap） |
| P2P BT | `back/internal/p2p_bt/` | Mainline DHT、BEP44/BEP51、torrent/magnet |

## 端口

| 端口 | 服务 | 仓库位置 | 说明 |
|------|------|----------|------|
| `:3000` | back (主 API) | `back/` | Gin HTTP，文件/合集/P2P 全部端点 |
| `:5173` | front (Dev) | `front/` | Vite HMR 开发服务器 |
| `:4000` | registration-server | 独立仓库 | 用户注册、JWT 认证 |

## 核心概念

| 概念 | 类比 Git | 说明 |
|------|----------|------|
| Collection | Repository | 文件合集 |
| Entry | Tree | `path → [{type, value, mime_type}]` |
| Version / Commit | Commit | 版本快照 |
| Fork | Fork | 基于现有合集创建新版本 |
| Merge | Merge | 三路合并 |
| Anonymous Collection | — | 无需注册，SHA256 hash 直接访问 |

## 模块

| 模块 | 前端 | 后端 |
|------|------|------|
| 文件管理 | `FileManager.jsx`, `Sha256Manager.jsx` | `controller/file.go`, `service/file_service.go` |
| 合集 | `AnonCreator/`, `AnonExplorer/`, `CollectionBuilder.jsx` | `controller/anon.go`, `controller/collection.go`, `service/anon_service.go` |
| P2P | `P2PDashboard.jsx`, `P2PTopology.jsx`, `WebRTCPeer.jsx` | `service/p2p.go`, `controller/p2p.go` |
| BT DHT | `BTController.jsx`, `BTPanel.jsx` | `p2p_bt/`, `controller/p2p.go` (BT 端点) |
| IPFS | `IPFSPanel.jsx` | `provider/ipfs.go`, `service/ipfs_compat.go` |
| WebRTC | `WebRTCTransfer.jsx` | `service/p2p.go` (WebRTC 信令) |
| 认证 | `UserGroupPicker.jsx`, `VisibilityPicker.jsx` | `controller/auth.go`, `service/auth_service.go` |

## 数据流

```
Upload:   front → POST /files/upload → Controller → FileService → Provider(local) → SQLite
Download: front → GET /sha256sum/:hash → Controller → Downloader → Provider → Response
P2P:      Node ← libp2p DHT → Discover Peers → WS/WebRTC Transfer
BT:       Node ← Mainline DHT → Find Peers → Torrent Download
Collection: front → POST /anon/collections → AnonService → CollectionRepo → JSON → SHA256
```

## 快速开始

```bash
# 后端
cd back
go run ./cmd/server/
# → http://localhost:3000 , Swagger at /swagger/index.html

# 前端
cd front
npm ci && npm run dev
# → http://localhost:5173
```

## 测试

```bash
# 后端单元测试
cd back && go test ./... -count=1

# E2E 全端点测试
cd back && bash test/e2e-all.sh

# P2P 双节点测试
cd back && bash test/p2p.sh

# 前端测试
cd front && npm test

# 前端构建
cd front && npm run build
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `3000` | 后端端口 |
| `PEERDRIVE_STORAGE` | `./storage` | 文件存储目录 |
| `PEERDRIVE_P2P_ENABLE` | `true` | 启用 libp2p |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | P2P 监听地址 |
| `PEERDRIVE_BT_DHT_ENABLE` | `true` | 启用 BT DHT |
| `PEERDRIVE_MDNS_ENABLE` | `true` | 局域网发现 |
| `PEERDRIVE_RELAY_ENABLE` | `false` | Relay 模式 |
| `PEERDRIVE_RELAY_MODE` | `client` | `server` / `client` |
| `PEERDRIVE_HOLE_PUNCH` | `true` | NAT 打洞 |
| `CORS_MODE` | 白名单 | `all` / `localhost` |
| `VITE_API_BASE` | `http://localhost:3000` | 前端 API 基地址 |
| `PEERDRIVE_DISCOVER_URL` | 空 | 自托管信令的发现 API（设置后优先于 MQTT） |
| `PEERDRIVE_DISCOVER_PRESENCE` | `true` | 节点级「存在房间」发现：零共享 collection 的节点也能互相发现（见 [ROADMAP.md](ROADMAP.md) 第 1 阶段、[REFACTOR.md](REFACTOR.md) §3.18） |
| `PEERDRIVE_MAX_PEERS` | `8` | 发现触发的拨号上限（防全互联退化）；静态 `PEERDRIVE_PEERJS_PEERS` 不受限 |

## 文档索引

### 架构

| 文件 | 内容 |
|------|------|
| [ROADMAP.md](ROADMAP.md) | **开发顺序（用户 2026-09 定序）**：PeerJS 互联 → 文件 → 组合 → 管理链路 → 文件范围管理 → 上传下载保存 → 身份管理（最后） |
| [REFACTOR.md](REFACTOR.md) | 重构手册：分层边界、迁移顺序、既有问题的修法 |
| [NETDISK.md](NETDISK.md) | 网盘（PeerJS 节点 + 面板）使用与实现手册 |
| [LAYERS.md](LAYERS.md) / [layers/](layers/) | 分层架构与逐层说明 |
| [NODE.md](NODE.md) / [NODE-API.md](NODE-API.md) | 节点与节点 API |
| [PEERSIGNAL.md](PEERSIGNAL.md) | 自托管信令（peersignal）部署与协议 |
| [PROJECT-VISION.md](PROJECT-VISION.md) | 产品愿景 |
| [HTTP_API_PROXY.md](HTTP_API_PROXY.md) | HTTP API 代理 |
| [TODO-SIMPLIFY.md](TODO-SIMPLIFY.md) | 简化待办 |
| [source-control.md](source-control.md) | 版本控制（合集 fork / merge） |
| [dht-wire-format.md](dht-wire-format.md) | DHT 线格式 |
| [api-reference.md](api-reference.md) | API 参考（简版） |
| [design/PEERDRIVE-DSH-INSPIRED.md](design/PEERDRIVE-DSH-INSPIRED.md) | 参考 dsh 的组合式架构设计（profile/bundle/patch） |
| [design/FRONTEND-DSH-INSPIRED.md](design/FRONTEND-DSH-INSPIRED.md) | 前端参考 dsh 的组合式设计（bundle manifest + registry + profile） |
| [design/FRONTEND-DSH-KERNEL.md](design/FRONTEND-DSH-KERNEL.md) | 前端参考 dsh 内核的引导/模块/slot/传输设计 |
| [FILE-REFERENCE.md](FILE-REFERENCE.md) | 全部项目文件路径与说明手册 (~240 文件) |

### spec — 技术规范

| 文件 | 内容 |
|------|------|
| [spec/API-REFERENCE.md](spec/API-REFERENCE.md) | 完整 API 参考 (105 端点) |
| [spec/REQUIREMENTS.md](spec/REQUIREMENTS.md) | 全部需求总表 (130+ 项) |
| [spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) | 合集逻辑完整追踪 |
| [spec/USER-ROLES.md](spec/USER-ROLES.md) | 用户角色模型 |
| [spec/BACKEND_TASKS.md](spec/BACKEND_TASKS.md) | 后端任务清单 |
| [spec/FRONTEND_TASKS.md](spec/FRONTEND_TASKS.md) | 前端任务清单 |
| [spec/backend/](spec/backend/) | 后端 API / 数据库 / 设计规范 |
| [spec/frontend/](spec/frontend/) | 前端 API 文档 |

### modules — 模块设计

| 目录 | 内容 |
|------|------|
| [modules/auth/](modules/auth/) | 认证模块：API 设计、安全审查、用户角色 |
| [modules/bt/](modules/bt/) | BT DHT 模块：协议、测试矩阵、API 设计 |
| [modules/ipfs/](modules/ipfs/) | IPFS 模块：协议、WebRTC 架构 |
| [modules/p2p/](modules/p2p/) | P2P 模块：当前互联框架（TRANSPORT）+ API 设计；旧栈文档见 [modules/p2p/archive/](modules/p2p/archive/) |
| [modules/storage/](modules/storage/) | 存储模块：数据库、合集逻辑、API |

### guide — 操作指南

| 文件 | 内容 |
|------|------|
| [guide/API-USAGE.md](guide/API-USAGE.md) | API 使用手册 — 调用顺序/目的/条件 |
| [guide/USER_MANUAL.md](guide/USER_MANUAL.md) | 用户手册 |
| [guide/siliconflow-setup.md](guide/siliconflow-setup.md) | LLM 配置 |
| [guide/FRONTEND.md](guide/FRONTEND.md) | 前端界面说明 — 技术栈、路由、组件树、工作流、代码地图 |
| [guide/操作说明.md](guide/操作说明.md) | 中文操作说明 |

### testing — 测试

| 文件 | 内容 |
|------|------|
| [testing/README.md](testing/README.md) | **测试组件总览（入口）** — 14 个组件的选表/命令/规模/CI 映射/盲区清单（2026-09-20 实测：308+21+23+21+7+88+60+21） |
| [testing/index.md](testing/index.md) | 测试文档门户（指向上面那份 + 分层文档） |
| [testing/archive/](testing/archive/) | 旧栈时代测试文档（2026-04~05，libp2p / e2e-all.sh / reg-server），仅历史参考 |

> 旧索引里列过一批 `TESTING-HANDBOOK.md` / `TEST-PIPELINE.md` / `TEST-MATRIX.md` /
> `FRONTEND-TESTING.md` 等文件名，**当前目录下已不存在**（2026-08-18 重写后清理），
> 内容统一并入 `testing/README.md`。测试现状以它为准，勿再引用旧文件名。
>
> 相关：[NETDISK.md §7 本地跑通手册](NETDISK.md#7-本地跑通怎么亲手测这几个功能)、
> `scripts/test-layers.sh`（分层聚合）、`scripts/netdisk-local-demo.sh`（端到端）。

### tutorial — 教程（面向使用者的正路）

| 文件 | 内容 |
|------|------|
| [tutorial/README.md](tutorial/README.md) | 教程总入口与阅读顺序 |
| [tutorial/01-run-and-connect.md](tutorial/01-run-and-connect.md) | 跑起来 & 连上：装、启动、看到对方 |
| [tutorial/02-add-local-files.md](tutorial/02-add-local-files.md) | 把本机文件放进网盘 |
| [tutorial/03-share-levels.md](tutorial/03-share-levels.md) | 共享级别 public / unlisted / private |
| [tutorial/04-choose-what-to-share.md](tutorial/04-choose-what-to-share.md) | 挑要共享的东西（目录 / 单文件 / 合集） |
| [tutorial/05-save-from-other-nodes.md](tutorial/05-save-from-other-nodes.md) | 从别的节点保存内容 |
| [tutorial/appendix-build-from-source.md](tutorial/appendix-build-from-source.md) | 附录：从源码构建 |

### archive — 归档（旧文档 / 旧结构，**不再维护**）

> 这里的文档描述的是**重构前的结构**（`registration-server/`、`go/cmd/server/`、
> `p2p-dual-stack/`、`libp2p` 主栈、`e2e-all.sh` 等）。留着只为追溯决策与历史，
> **不要照着做**——路径与端点现在都不存在。

| 文件 / 目录 | 内容 |
|------|------|
| [archive/report/](archive/report/) | 旧「报告」目录（36 份：REPORT-OVERVIEW / ROADMAP / SECURITY-REVIEW / DEVELOPMENT_PLAN / TODO-FIXES / changelog / GIT-ANALYSIS / MILESTONE-* / 各次测试与修改报告） |
| [archive/TUTORIAL.md](archive/TUTORIAL.md) | 旧教程（已被 [tutorial/](tutorial/README.md) 取代） |
| [archive/DASHBOARD.md](archive/DASHBOARD.md) | 旧项目仪表盘 |
| [archive/LEGACY.md](archive/LEGACY.md) | 旧栈遗留说明 |
| [archive/VPS_DEPLOY.md](archive/VPS_DEPLOY.md) | 旧结构下的 VPS 部署（`registration-server/` 等路径已不存在） |
| [archive/docker.md](archive/docker.md) | 旧结构下的 Docker 部署 |
| [archive/p2p-discovery-flow.md](archive/p2p-discovery-flow.md) | 旧 libp2p 发现流程 |
| [archive/TRANSPORT-REVIEW-2026-08-15.md](archive/TRANSPORT-REVIEW-2026-08-15.md)、[archive/TRANSPORT-REVIEW2-2026-08-16.md](archive/TRANSPORT-REVIEW2-2026-08-16.md) | 传输层两轮 review |
| [archive/HTTP-REVIEW-2026-08-16.md](archive/HTTP-REVIEW-2026-08-16.md)、[archive/REVIEW-FIX-2026-08-16.md](archive/REVIEW-FIX-2026-08-16.md) | HTTP 层 review 与修复清单 |
| [archive/FRONTEND-FIX-2026-08-16.md](archive/FRONTEND-FIX-2026-08-16.md)、[archive/FRONTEND-FIXES-2026-08-16.md](archive/FRONTEND-FIXES-2026-08-16.md) | 前端两次修复记录 |
| [archive/SESSION-REPORT-20260503T135000.md](archive/SESSION-REPORT-20260503T135000.md) | 2026-05-03 会话报告 |
| [archive/CODE-DOC-MAPPING.md](archive/CODE-DOC-MAPPING.md) | 2026-04 的代码 ↔ 文档映射表（多数目标文档现已不存在） |
| [archive/AGENTS.md](archive/AGENTS.md)、[archive/方案.md](archive/方案.md)、[archive/测试方案.md](archive/测试方案.md)、[archive/知识库.md](archive/知识库.md)、[archive/reply.md](archive/reply.md) | 早期设计草案与参考资料 |
| [modules/p2p/archive/](modules/p2p/archive/) | 旧 libp2p/BT-DHT 栈：p2p / dual-stack-protocol / grid |
| [testing/archive/](testing/archive/) | 旧栈时代的测试文档（libp2p / e2e-all.sh / reg-server 测试） |
