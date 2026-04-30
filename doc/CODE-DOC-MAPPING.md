# Peerdrive 代码-文档映射

> 每个文档对应的代码文件，方便查找和验证
> 更新: 2026-04-29

---

## 核心代码文件 → 文档

### cmd/server/main.go (108行)
- **[spec/backend/BACKEND_DOC.md](spec/backend/BACKEND_DOC.md)** — 启动流程说明
- **[spec/backend/IMPLEMENTATION_SPEC.md](spec/backend/IMPLEMENTATION_SPEC.md)** — 初始化顺序

### internal/config/config.go (206行)
- **[spec/backend/BACKEND_DOC.md](spec/backend/BACKEND_DOC.md)** — 环境变量参考
- **[guide/VPS_DEPLOY.md](guide/VPS_DEPLOY.md)** — 生产环境配置
- **[guide/docker.md](guide/docker.md)** — Docker 环境变量

### internal/router/router.go (498行)
- **[spec/API-REFERENCE.md](spec/API-REFERENCE.md)** — 全部 105 端点文档
- **[spec/backend/BACKEND_DOC.md](spec/backend/BACKEND_DOC.md)** — 路由分组说明
- **go/docs/docs.go** — Swagger 自动生成 (96 路径)

### internal/controller/p2p.go (1438行) — P2P 核心
- **[spec/p2p/p2p.md](spec/p2p/p2p.md)** — P2P 协议规范
- **[modules/p2p/API-DESIGN.md](modules/p2p/API-DESIGN.md)** — P2P API 设计
- **[testing/TESTING-METHODOLOGY.md](testing/TESTING-METHODOLOGY.md)** Phase 2-7 — 测试方法

### internal/controller/collection.go (548行) — 合集管理
- **[spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md)** — 合集逻辑完整追踪
- **[spec/backend/anon-collection.md](spec/backend/anon-collection.md)** — 匿名合集规范
- **[testing/TEST-MATRIX.md](testing/TEST-MATRIX.md)** C-01~C-18 — 合集测试用例

### internal/controller/file.go (363行) — 文件上传/注册
- **[spec/backend/upload.md](spec/backend/upload.md)** — 上传流程
- **[spec/backend/register.md](spec/backend/register.md)** — 注册流程
- **[spec/backend/sha256-download.md](spec/backend/sha256-download.md)** — 下载流程
- **[testing/archive/register.md](testing/archive/register.md)** — 注册测试说明

### internal/controller/download.go (329行) — 下载
- **[spec/backend/sha256-download.md](spec/backend/sha256-download.md)** — SHA256 下载
- **[testing/TEST-MATRIX.md](testing/TEST-MATRIX.md)** F-15~F-22 — 下载测试

### internal/service/p2p.go (904行) — libp2p 服务
- **[modules/p2p/README.md](modules/p2p/README.md)** — P2P 模块概述
- **[modules/p2p/API-DESIGN.md](modules/p2p/API-DESIGN.md)** — P2P API
- **[spec/p2p/p2p.md](spec/p2p/p2p.md)** — 协议细节
- **[testing/archive/test-steps.md](testing/archive/test-steps.md)** — P2P 实测步骤

### internal/p2p_bt/ (4328行, 完全解耦) — BitTorrent
- **[modules/bt/README.md](modules/bt/README.md)** — BT 模块概述
- **[modules/bt/API-DESIGN.md](modules/bt/API-DESIGN.md)** — BT API 设计
- **[modules/bt/bt-dht-protocol.md](modules/bt/bt-dht-protocol.md)** — BT DHT 协议
- **[modules/bt/TEST-MATRIX.md](modules/bt/TEST-MATRIX.md)** — BT 测试矩阵
- **[report/TASK-COMPLETION-2026-04-29.md](report/TASK-COMPLETION-2026-04-29.md)** 任务6 — BT 端到端

### internal/service/ipfs_compat.go (574行) — IPFS 兼容
- **[modules/ipfs/README.md](modules/ipfs/README.md)** — IPFS 模块概述
- **[modules/ipfs/API-DESIGN.md](modules/ipfs/API-DESIGN.md)** — IPFS API
- **[modules/ipfs/ipfs-protocol.md](modules/ipfs/ipfs-protocol.md)** — IPFS 协议

### internal/service/signaling.go (362行) — WebRTC 信令
- **[modules/ipfs/webrtc-architecture.md](modules/ipfs/webrtc-architecture.md)** — WebRTC 架构
- **[testing/TESTING-METHODOLOGY.md](testing/TESTING-METHODOLOGY.md)** — Phase 8 信令测试

### internal/service/auth_service.go (103行) — 认证
- **[modules/auth/README.md](modules/auth/README.md)** — Auth 模块概述
- **[modules/auth/API-DESIGN.md](modules/auth/API-DESIGN.md)** — Auth API
- **[modules/auth/SECURITY-REVIEW.md](modules/auth/SECURITY-REVIEW.md)** — 认证安全
- **[modules/auth/USER-ROLES.md](modules/auth/USER-ROLES.md)** — 用户角色

### internal/repository/db.go (160行) — 数据库
- **[spec/backend/database.md](spec/backend/database.md)** — 数据库设计
- **[modules/storage/database.md](modules/storage/database.md)** — 表结构

### internal/service/file_service.go (607行) — 文件服务
- **[modules/storage/README.md](modules/storage/README.md)** — Storage 模块
- **[modules/storage/API-DESIGN.md](modules/storage/API-DESIGN.md)** — Storage API
- **[modules/storage/api-reference.md](modules/storage/api-reference.md)** — 文件 API

### react/src/pages/AnonCreator.jsx (400行) — 合集创建器
- **[spec/frontend/FRONTEND_DOC.md](spec/frontend/FRONTEND_DOC.md)** — 前端文档
- **[spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md)** — 合集逻辑
- **[report/TASK-COMPLETION-2026-04-29.md](report/TASK-COMPLETION-2026-04-29.md)** 任务3 — 前端修复

### react/src/pages/FileManager.jsx (1283行) — 文件管理
- **[spec/frontend/FRONTEND_DOC.md](spec/frontend/FRONTEND_DOC.md)** — 前端文档
- **[report/TASK-COMPLETION-2026-04-29.md](report/TASK-COMPLETION-2026-04-29.md)** 任务4 — 三种浏览模式

---

## 文档 → 代码文件（反向索引）

### spec/ 规范文档
| 文档 | 对应的代码文件 |
|------|---------------|
| [API-REFERENCE.md](spec/API-REFERENCE.md) | `internal/router/router.go`, `internal/controller/*.go` |
| [REQUIREMENTS.md](spec/REQUIREMENTS.md) | 全部 `internal/**/*.go`, `react/src/**/*.jsx` |
| [COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) | `internal/controller/collection.go`, `internal/service/anon_service.go` |
| [USER-ROLES.md](spec/USER-ROLES.md) | `internal/service/auth_service.go`, `internal/router/auth_middleware.go` |
| [backend/BACKEND_DOC.md](spec/backend/BACKEND_DOC.md) | `cmd/server/main.go`, `internal/config/config.go` |
| [backend/database.md](spec/backend/database.md) | `internal/repository/db.go` |
| [backend/anon-collection.md](spec/backend/anon-collection.md) | `internal/controller/anon.go`, `internal/service/anon_service.go` |
| [backend/upload.md](spec/backend/upload.md) | `internal/controller/file.go`, `internal/service/file_service.go` |
| [backend/register.md](spec/backend/register.md) | `internal/controller/file.go::RegisterLocalFile`, `internal/service/file_service.go` |
| [backend/sha256-download.md](spec/backend/sha256-download.md) | `internal/controller/download.go` |
| [backend/design.md](spec/backend/design.md) | `internal/model/*.go` |
| [frontend/FRONTEND_DOC.md](spec/frontend/FRONTEND_DOC.md) | `react/src/pages/*.jsx`, `react/src/components/*.jsx` |
| [frontend/API_DOC.md](spec/frontend/API_DOC.md) | `react/src/api.js` |
| [p2p/p2p.md](spec/p2p/p2p.md) | `internal/service/p2p.go`, `internal/controller/p2p.go` |
| [p2p/bt-dht-protocol.md](spec/p2p/bt-dht-protocol.md) | `internal/p2p_bt/bt_dht.go`, `internal/p2p_bt/bep44.go` |
| [p2p/ipfs-protocol.md](spec/p2p/ipfs-protocol.md) | `internal/service/ipfs_compat.go` |
| [p2p/dual-stack-protocol.md](spec/p2p/dual-stack-protocol.md) | `internal/service/p2p_dual.go` |
| [p2p/webrtc-architecture.md](spec/p2p/webrtc-architecture.md) | `internal/service/signaling.go` |

### modules/ 模块文档
| 文档 | 对应的代码文件 |
|------|---------------|
| [p2p/README.md](modules/p2p/README.md) | `internal/service/p2p*.go` (8 files) |
| [bt/README.md](modules/bt/README.md) | `internal/p2p_bt/*.go` (11 files) |
| [ipfs/README.md](modules/ipfs/README.md) | `internal/service/ipfs_compat.go` |
| [storage/README.md](modules/storage/README.md) | `internal/service/file_service.go`, `internal/repository/file_repo.go` |
| [auth/README.md](modules/auth/README.md) | `internal/service/auth_service.go`, `internal/repository/user_repo.go` |

### report/ 报告文档
| 文档 | 对应的代码文件 |
|------|---------------|
| [DASHBOARD.md](DASHBOARD.md) | 全部模块 |
| [TODO-FIXES.md](report/TODO-FIXES.md) | 见每个条目的文件路径 |
| [SECURITY-REVIEW.md](report/SECURITY-REVIEW.md) | 见每个漏洞的 File 字段 |
| [DEVELOPMENT_PLAN.md](report/DEVELOPMENT_PLAN.md) | `internal/router/router.go`, `internal/service/auth_service.go` |
| [TASK-COMPLETION-2026-04-29.md](report/TASK-COMPLETION-2026-04-29.md) | 见"修改文件清单"节 |

### testing/ 测试文档
| 文档 | 对应的测试脚本/代码 |
|------|-------------------|
| [TESTING-HANDBOOK.md](testing/TESTING-HANDBOOK.md) | `go/test/*.sh`, `go/*_test.go` |
| [TEST-MATRIX.md](testing/TEST-MATRIX.md) | `go/test/*.sh` (每个测试ID对应脚本) |
| [TESTING-METHODOLOGY.md](testing/TESTING-METHODOLOGY.md) | `back/test/p2p.sh`, `back/test/relay.sh` |
| [test-steps.md](testing/archive/test-steps.md) | `back/test/p2p.sh` Phase 1-7 |
| [register.md](testing/archive/register.md) | `go/test/register.sh` |
| [CHAOS_TESTING.md](testing/CHAOS_TESTING.md) | `go/test/chaos-net.sh`, `go/test/test-under-chaos.sh` |

---

## 模块解耦状态

| 模块 | 代码位置 | 解耦状态 | 说明 |
|------|---------|---------|------|
| **base** | `cmd/`, `router/`, `config/`, `log/` | ✅ 独立 | Gin + SQLite 基座 |
| **p2p_bt** | `internal/p2p_bt/` | ✅ 完全解耦 | 无内部依赖，独立 BT 库 |
| **provider** | `internal/provider/` | ✅ 完全解耦 | 接口+实现，无内部依赖 |
| **hashutil** | `pkg/hashutil/` | ✅ 完全解耦 | 独立工具包 |
| **p2p** | `internal/service/p2p*.go` | ⚠️ 部分耦合 | 依赖 repository |
| **collection** | `internal/controller/anon/collection.go` | ⚠️ 部分耦合 | controller 直接访问 repository |
| **upload** | `internal/controller/file.go` | ⚠️ 部分耦合 | 通过 service 层（较好） |
| **auth** | `internal/service/auth_service.go` | ⚠️ 部分耦合 | 通过 service 层（较好） |
| **ipfs** | `internal/service/ipfs_compat.go` | ⚠️ 部分耦合 | 依赖 repository |
| **webrtc** | `internal/service/signaling.go` | ✅ 独立 | 仅依赖标准库 |
| **relay** | `internal/service/relay.go` | ⚠️ 部分耦合 | 依赖 repository |
| **webdav** | `internal/service/webdav.go` | ✅ 独立 | 仅依赖标准库 |
