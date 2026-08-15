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

## 文档索引

### 架构

| 文件 | 内容 |
|------|------|
| [DASHBOARD.md](DASHBOARD.md) | 项目仪表盘 |
| [FILE-REFERENCE.md](FILE-REFERENCE.md) | 全部项目文件路径与说明手册 (~240 文件) |

### spec — 技术规范

| 文件 | 内容 |
|------|------|
| [spec/API-REFERENCE.md](spec/API-REFERENCE.md) | 完整 API 参考 (105 端点) |
| [spec/REQUIREMENTS.md](spec/REQUIREMENTS.md) | 全部需求总表 (130+ 项) |
| [spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) | 合集逻辑完整追踪 |
| [spec/USER-ROLES.md](spec/USER-ROLES.md) | 用户角色模型 |
| [spec/CODE-DOC-MAPPING.md](spec/CODE-DOC-MAPPING.md) | 代码 ↔ 文档映射表 |
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
| [modules/p2p/](modules/p2p/) | P2P 模块：双栈协议、网格拓扑 |
| [modules/storage/](modules/storage/) | 存储模块：数据库、合集逻辑、API |

### guide — 操作指南

| 文件 | 内容 |
|------|------|
| [guide/API-USAGE.md](guide/API-USAGE.md) | API 使用手册 — 调用顺序/目的/条件 |
| [guide/USER_MANUAL.md](guide/USER_MANUAL.md) | 用户手册 |
| [guide/VPS_DEPLOY.md](guide/VPS_DEPLOY.md) | VPS 部署指南 |
| [guide/docker.md](guide/docker.md) | Docker 部署 |
| [guide/siliconflow-setup.md](guide/siliconflow-setup.md) | LLM 配置 |
| [guide/FRONTEND.md](guide/FRONTEND.md) | 前端界面说明 — 技术栈、路由、组件树、工作流、代码地图 |
| [guide/操作说明.md](guide/操作说明.md) | 中文操作说明 |

### testing — 测试

| 文件 | 内容 |
|------|------|
| [testing/index.md](testing/index.md) | 测试文档门户 |
| [testing/README.md](testing/README.md) | 测试总览 |
| [testing/TESTING-HANDBOOK.md](testing/TESTING-HANDBOOK.md) | 测试手册 — 环境搭建、运行、故障排查 |
| [testing/TEST-PIPELINE.md](testing/TEST-PIPELINE.md) | 测试管线 — 脚本流程和预期行为 |
| [testing/TEST-MATRIX.md](testing/TEST-MATRIX.md) | 测试矩阵 — 109 用例 |
| [testing/TESTING-METHODOLOGY.md](testing/TESTING-METHODOLOGY.md) | 测试方法论 |
| [testing/CHAOS_TESTING.md](testing/CHAOS_TESTING.md) | 混沌测试 |
| [testing/reg-server-test-plan.md](testing/reg-server-test-plan.md) | 注册服务器测试计划 |
| [testing/如何测试.md](testing/如何测试.md) | 中文测试指南 |
| [testing/FRONTEND-TESTING.md](testing/FRONTEND-TESTING.md) | 前端测试文档 — Vitest + Happy DOM + Testing Library |
| [testing/测试方案.md](testing/测试方案.md) | 测试体系总览 |

### report — 报告

| 文件 | 内容 |
|------|------|
| [report/index.md](report/index.md) | 报告目录 |
| [report/REPORT-OVERVIEW.md](report/REPORT-OVERVIEW.md) | 项目全貌 |
| [report/ROADMAP.md](report/ROADMAP.md) | 路线图 |
| [report/SECURITY-REVIEW.md](report/SECURITY-REVIEW.md) | 安全审查 |
| [report/DEVELOPMENT_PLAN.md](report/DEVELOPMENT_PLAN.md) | 开发计划 |
| [report/TODO-FIXES.md](report/TODO-FIXES.md) | 当前任务 |
| [report/changelog.md](report/changelog.md) | 变更日志 |
| [report/GIT-ANALYSIS.md](report/GIT-ANALYSIS.md) | Git 记录分析 — 328 提交的开发模式、高频 Bug、经验教训 |

### archive — 归档

历史设计草案和参考文档：[archive/](archive/)
