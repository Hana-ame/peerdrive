# Peerdrive 文档索引

## Monorepo 结构：`front/` `back/` `doc/`

> 所有路径相对于仓库根目录

## spec — 技术规范

| 文件 | 内容 |
|------|------|
| [API-USAGE.md](API-USAGE.md) | API 使用手册 — 按模块分章，调用顺序/目的/条件 |
| [spec/API-REFERENCE.md](spec/API-REFERENCE.md) | 完整 API 参考 (105 端点，Swagger 生成) |
| [spec/REQUIREMENTS.md](spec/REQUIREMENTS.md) | 全部需求总表 (130+ 项) |
| [spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) | 合集逻辑完整追踪 |
| [spec/USER-ROLES.md](spec/USER-ROLES.md) | 用户角色模型 |
| [spec/backend/](spec/backend/) | 后端 API / 数据库 / 设计规范 |
| [spec/frontend/](spec/frontend/) | 前端 API 文档 |
| [spec/p2p/](spec/p2p/) | P2P / BT / IPFS 协议规范 |
| [CODE-DOC-MAPPING.md](CODE-DOC-MAPPING.md) | 代码 ↔ 文档映射表 |

## guide — 操作指南

| 文件 | 内容 |
|------|------|
| [guide/操作说明.md](guide/操作说明.md) | 中文操作说明 |
| [guide/siliconflow-setup.md](guide/siliconflow-setup.md) | SiliconFlow LLM 配置 |
| [guide/USER_MANUAL.md](guide/USER_MANUAL.md) | 用户手册 |
| [guide/VPS_DEPLOY.md](guide/VPS_DEPLOY.md) | VPS 部署指南 |
| [guide/docker.md](guide/docker.md) | Docker 部署 |

## testing — 测试

| 文件 | 内容 |
|------|------|
| [testing/index.md](testing/index.md) | 测试文档门户 |
| [testing/TESTING-HANDBOOK.md](testing/TESTING-HANDBOOK.md) | 测试手册 — 环境搭建、运行、故障排查 |
| [testing/TEST-PIPELINE.md](testing/TEST-PIPELINE.md) | 测试管线 — 每个脚本的流程和预期行为 |
| [testing/TEST-MATRIX.md](testing/TEST-MATRIX.md) | 测试矩阵 — 109 项测试用例 |
| [testing/TESTING-METHODOLOGY.md](testing/TESTING-METHODOLOGY.md) | 测试方法论 |
| [testing/如何测试.md](testing/如何测试.md) | 中文测试操作指南 |
| [testing/测试方案.md](testing/测试方案.md) | 测试体系总览 |
| [testing/CHAOS_TESTING.md](testing/CHAOS_TESTING.md) | 混沌测试 — 恶劣网络模拟 |

## 测试快速命令

```bash
# Go 单元测试 (150 函数 / 17 文件)
cd back && go test ./... -count=1

# E2E 全端点 (32 脚本)
cd back && bash test/e2e-all.sh

# P2P 测试
cd back && bash test/p2p.sh

# 前端测试 (20 用例 / 5 文件)
cd front && npm test
```

## report — 报告

| 文件 | 内容 |
|------|------|
| [report/index.md](report/index.md) | 报告目录索引 |
| [report/REPORT-OVERVIEW.md](report/REPORT-OVERVIEW.md) | 项目全貌、模块状态、时间线 |
| [report/TODO-FIXES.md](report/TODO-FIXES.md) | 当前任务追踪 |
| [report/ROADMAP.md](report/ROADMAP.md) | 路线图 |
| [report/SECURITY-REVIEW.md](report/SECURITY-REVIEW.md) | 安全审查 |

## 代码路径速查

| 组件 | 路径 |
|------|------|
| 后端入口 | `back/cmd/server/main.go` |
| Gin 路由 | `back/internal/router/router.go` |
| 文件服务 | `back/internal/service/file_service.go` |
| 合集服务 | `back/internal/service/anon_service.go` |
| P2P 服务 | `back/internal/service/p2p.go` |
| BT DHT | `back/internal/p2p_bt/` |
| 前端入口 | `front/src/main.jsx` |
| 合集创建 | `front/src/pages/AnonCreator/` |
| 合集浏览 | `front/src/pages/AnonExplorer/` |
| API 客户端 | `front/src/api.js` |
| CI/CD | `.github/workflows/ci.yml` |

## archive — 归档

历史反馈、早期设计草案、测试结果等见 [archive/](archive/)。
