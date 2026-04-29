# Registration Server

Peerdrive 中心管理服务器 — 用户注册/登录、JWT 令牌、群组管理、服务策略、Relay 操作者关联、存储追踪、评论系统。

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
| `JWT_SECRET` | `change-me-in-production` | JWT 签名密钥（生产必须修改） |
| `REGISTRATION_HOST` | `https://localhost:4000` | JWT issuer 域名 |

## API 端点 (24 个)

### 认证 (auth)

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/ping` | 无 | 服务连通性检查 |
| POST | `/auth/register` | 无 | 注册新用户，返回 JWT |
| POST | `/auth/login` | 无 | 登录，返回 JWT |
| GET | `/auth/whoami` | Bearer | 返回当前用户名和角色 |
| GET | `/auth/list` | Bearer + Admin | 列出所有用户 |
| GET | `/api/health` | Bearer | 健康检查 |

### 群组管理 (6.6)

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/auth/groups` | Bearer | 列出所有群组（含成员数） |
| GET | `/auth/group/:username` | Bearer | 查询用户所属群组 |
| POST | `/auth/group/:username` | Bearer | 将用户加入群组（群组不存在则自动创建） |
| DELETE | `/auth/group/:username/:groupname` | Bearer | 将用户从群组移除 |
| GET | `/auth/groups/:groupname/members` | Bearer | 查询群组成员列表 |

### 服务策略 (6.7)

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/auth/service-policy/:username` | Bearer | 查询用户 relay/P2P 服务许可 |
| POST | `/auth/service-policy/:username` | Bearer | 设置 relay/P2P 许可和备注 |

### 存储追踪 (6.9)

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/auth/storage/:username` | Bearer | 查询用户存储使用量和配额 |
| POST | `/auth/storage/:username` | Bearer | 更新用户存储使用量/配额 |

### Node 操作者 (6.8)

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/auth/relays/:username` | Bearer | 查询用户运营的所有 relay node |
| GET | `/p2p/relay/:peer_id/operator` | Bearer | 查询 relay node 的操作者信息 |
| POST | `/p2p/relay/:peer_id/operator` | Bearer | 绑定 relay node 到用户账号 |

### Relay 节点

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| POST | `/p2p/relay/register` | 无 | 注册 relay node（节点自注册） |
| GET | `/p2p/relay/list` | 无 | 列出活跃 relay 节点 |
| POST | `/p2p/relay/heartbeat` | 无 | 发送心跳（保持活跃状态） |

### 评论 & 统计

| 方法 | 路径 | 认证 | 说明 |
|------|------|------|------|
| GET | `/comments/:hash` | 无 | 读取合集评论（匿名可读） |
| POST | `/comments/:hash` | Bearer | 发表评论（需认证） |
| GET | `/stats` | 无 | 节点统计（用户数/活跃relay/评论数） |

## 与 Peerdrive 集成

Peerdrive Node 通过 `Authorization: Bearer <token>` 调用本服务器验证用户身份，并根据服务策略判断是否向对等节点提供 relay/P2P 服务。

```
Peerdrive Node ──→ POST /p2p/relay/register    (注册为 relay)
               ──→ POST /p2p/relay/heartbeat    (心跳保活)
               ──→ GET  /p2p/relay/:id/operator (查询 relay 操作者)
               ──→ GET  /auth/service-policy/:user (查询服务许可)
其他 Peerdrive Node ──→ GET /p2p/relay/list    (发现可用 relay)
```

### 典型流程

1. 用户运行 Peerdrive Node → 自动注册 relay（无需认证）
2. 用户登录 → 获取 JWT → 绑定 relay 到账号
3. 其他 node 查询 relay operator → 决定是否信任/连接
4. 管理员可设置服务策略 → 限制某用户的 relay/P2P 权限
5. Node 定期 heartbeat → 注册服务器追踪活跃 relay
