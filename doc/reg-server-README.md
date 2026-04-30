# Registration Server

Peerdrive 中心权限管理服务器 — 用户注册、登录、JWT 令牌签发。

## 运行

```bash
cp .env.example .env
# 编辑 .env 中的 JWT_SECRET

go run ./cmd/server
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `4000` | 监听端口 |
| `DB_PATH` | `./registration.db` | SQLite 数据库路径 |
| `JWT_SECRET` | `change-me-in-production` | JWT 签名密钥（必须修改） |
| `REGISTRATION_HOST` | `https://localhost:4000` | JWT issuer 域名 |

## API

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/auth/register` | 无 | 注册新用户，返回 JWT |
| POST | `/auth/login` | 无 | 登录，返回 JWT |
| GET | `/auth/whoami` | Bearer | 返回当前用户名和角色 |
| GET | `/api/health` | Bearer | 健康检查（需认证） |

## 与 Peerdrive 集成

Peerdrive 后端在收到请求时，通过 `Authorization: Bearer <token>` 调用本服务器的 `/auth/whoami` 验证用户身份。

（未来可增加公钥验证，Peerdrive 节点本地校验 JWT，无需每请求回源）
