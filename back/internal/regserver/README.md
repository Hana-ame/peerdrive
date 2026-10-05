# peerdrive-reg-server — 注册 / 认证 / 中继登记服务

peerdrive 的用户与中继登记服务。**本目录是唯一真相源**：2026-10-05 自独立仓
`github.com/Hana-ame/registration-server` 并入 peerdrive 主仓，此后不再从那个仓取码。

## 为什么并入主仓

它本来就是 peerdrive 认证链路的一环，此前却是仓外的一个仓：
`back/internal/router/auth_middleware.go` 通过 `GET {RegistrationServer}/auth/whoami`
校验 Bearer token，`RegistrationServer` 由 `PEERDRIVE_REG_SERVER` 配置
（`back/internal/config/config.go:35`）。即**主仓的生产部署一直依赖这个仓外服务**。
并入后这份依赖可以指向主仓自身，`peerdrive-server` + `peerdrive-reg-server` +
`peerdrive-signal` 三个二进制出自同一个 tag。

## 迁移兼容性（有测试保证）

既有部署不能因为「搬进主仓」而失效，故以下性质被 `compat_test.go` 钉住：

- **JWT 逐字节一致**：同密钥下新实现签出的 token 与原实现完全相同
  （`TestTokenByteIdenticalToOriginal`），包括 `iss`、`sub`、TTL=72h、claims 字段。
- **旧 token 仍可验**：`TestNewVerifiesOldTokens` 用原实现签发、新实现校验，
  正反两个方向都验（灰度切换期两版并存）。
- **拒绝行为一致**：`TestOldAndNewAgreeOnRejects` 保证新旧对「非法 token /
  异密钥 / 过期」的判定不分歧。
- **数据兼容**：直接复用既有 SQLite 库（表 `users` / `relay_nodes`），
  `CREATE TABLE IF NOT EXISTS` 不会动已有表；密码 bcrypt cost 10，与旧库一致。

> 这几项一旦破坏，后果是「在线用户全部被 401 踢下线」，且只在生产暴露。
> `compat_old_test.go` 里保留了原实现的副本供对拍——删之前先想清楚谁在证明兼容性。

## 接口

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| GET | `/ping` | 否 | 探活，`{"service":"peerdrive-registration","status":"ok"}` |
| GET | `/api/health` | 是 | 健康检查 |
| POST | `/auth/register` | 否 | 注册，返回 `{username, token}`；重名 409 |
| POST | `/auth/login` | 否 | 登录，返回 `{token}` |
| GET | `/auth/whoami` | 是 | 返回 `{username, role}`（peerdrive 的鉴权入口） |
| GET | `/auth/list` | 是 | 用户列表 |
| POST | `/p2p/relay/register` | 否 | 中继登记（按 `peer_id` 幂等 upsert） |
| POST | `/p2p/relay/heartbeat` | 否 | 心跳 |
| GET | `/p2p/relay/list` | 否 | 中继列表 |

## 构建与运行

必须带 `-tags nosqlite`，与主服务同理（两个 SQLite 驱动的 CGO 符号冲突）。

**v0.3.0 起本服务不再是独立二进制**，而是 `peerdrive` 的一个子命令：

```bash
# 从 back/ 目录构建单二进制
go build -tags nosqlite -o peerdrive ./cmd/peerdrive/

# 只起注册服务（原独立二进制的行为，地址仍由 PORT/HOST 决定）
JWT_SECRET=xxx PEERDRIVE_REG_DB=./reg.db ./peerdrive reg

# 或者与主服务、信令同进程同端口
JWT_SECRET=xxx ./peerdrive all
```

代码位置也一并调整：本包从 `back/cmd/reg-server/` 迁到
`back/internal/regserver/`，以便主二进制能把它作为库引入。迁移只改了
可见性（`package main` → `package regserver`）与状态归属（包级 `db`/`jwtSecret`
→ `Server` 实例字段），**JWT 签发与校验逐字节不变**，由 `compat_test.go` 保证。

跨平台发布用 `CGO_ENABLED=0`（此时走纯 Go 驱动 `modernc.org/sqlite`，
见 `driver_pure.go`；有 cgo 时走 `mattn/go-sqlite3`，见 `driver_cgo.go`——与
`back/internal/repository/` 的切分口径一致）。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `JWT_SECRET` | 无 | **必填**，缺失直接退出。留空会让任何人伪造 token |
| `PORT` | `4000` | 监听端口 |
| `HOST` | 空 | 监听 IP；空 = 全网卡 |
| `DB_PATH` | `./reg.db` | SQLite 路径 |
| `PEERDRIVE_REG_DB` | — | `DB_PATH` 的别名（主仓配置项统一带 `PEERDRIVE_` 前缀） |

## 与 peerdrive-server 配合

```bash
# 1) 注册服务
JWT_SECRET=xxx PEERDRIVE_REG_DB=./reg.db ./peerdrive-reg-server &

# 2) 主服务指向它（留空 = 关闭认证，本机单节点模式）
PEERDRIVE_REG_SERVER=http://127.0.0.1:4000 ./peerdrive-server
```

`PEERDRIVE_REG_SERVER` 为空时 `authDisabled()` 返回 true、认证整体放宽
（见 `auth_middleware.go:28`）——那是本机单节点模式的既定行为，**公网部署必须配置**。
