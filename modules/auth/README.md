# Auth 模块

用户认证、JWT 验证、节点注册、服务策略管理。

## 源码

| 文件 | 说明 |
|------|------|
| [service/auth_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/auth_service.go) | JWT 验证、注册服务器通信 |
| [router/auth_middleware.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/auth_middleware.go) | Bearer token 中间件 |
| [controller/p2p.go#L1449-L1530](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1449) | Node operator / register handlers |
| [registration-server/](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/registration-server/) | 注册服务器 (独立服务, port 4000) |

## 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/node/operator` | 查询节点运营者 |
| POST | `/node/register` | 注册节点身份 |
| GET | `/p2p/auth/status` | 当前认证状态 |

## 子文档

- [USER-ROLES.md](USER-ROLES.md) — 用户角色模型
- [SECURITY-REVIEW.md](SECURITY-REVIEW.md) — 安全审查
- [API-DESIGN.md](API-DESIGN.md) — API 设计
