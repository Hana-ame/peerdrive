# Peerdrive Auth Module API Design

## Overview

The Peerdrive auth system uses a **Registration Server** for centralized user management, JWT token issuance, relay node registry, comments, and stats. Peerdrive nodes use the registration server's `/auth/whoami` endpoint to validate Bearer tokens via a middleware layer.

| Component | Base URL | Purpose |
|---|---|---|
| Registration Server | `http://<host>:4000` | Auth, relay registry, comments, stats |
| Peerdrive Node | `http://<host>:3000` | P2P auth status, middleware validation |

---

## 1. Registration Server Endpoints

### 1.1 Health Check

```
GET /ping
```

**Response `200`**
```json
{
  "status": "ok",
  "service": "peerdrive-registration"
}
```

**Curl**
```bash
curl -s http://localhost:4000/ping
```

---

### 1.2 User Registration

```
POST /auth/register
```

**Request**
```json
{
  "username": "alice",
  "password": "securepass123",
  "role": "user"
}
```
- `username`: 3-32 chars, required
- `password`: 6+ chars, required
- `role`: optional, defaults to `"user"`; set `"admin"` for admin privileges

**Response `201`**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "username": "alice",
  "role": "user"
}
```

**Response `409`** (username taken)
```json
{
  "error": "username already exists"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"securepass123"}'
```

---

### 1.3 User Login

```
POST /auth/login
```

**Request**
```json
{
  "username": "alice",
  "password": "securepass123"
}
```

**Response `200`**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "username": "alice",
  "role": "user"
}
```

**Response `401`**
```json
{
  "error": "invalid username or password"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"securepass123"}'
```

---

### 1.4 Get Current User (WhoAmI)

```
GET /auth/whoami
Authorization: Bearer <token>
```

**Response `200`**
```json
{
  "username": "alice",
  "role": "user"
}
```

**Response `401`**
```json
{
  "error": "missing authorization header"
}
```
or
```json
{
  "error": "invalid or expired token"
}
```

**Curl**
```bash
# With valid token
curl -s http://localhost:4000/auth/whoami \
  -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiIs...'

# Without token (expect 401)
curl -s http://localhost:4000/auth/whoami
```

---

### 1.5 List Users (Admin only)

```
GET /auth/list
Authorization: Bearer <admin-token>
```

**Response `200`**
```json
{
  "users": [
    {
      "id": 1,
      "username": "alice",
      "role": "user",
      "created_at": "2026-04-28T12:00:00Z"
    }
  ],
  "total": 1
}
```

**Response `403`** (non-admin user)
```json
{
  "error": "admin role required"
}
```

**Curl**
```bash
curl -s http://localhost:4000/auth/list \
  -H 'Authorization: Bearer <admin-token>'
```

---

### 1.6 Get User Groups

```
GET /auth/group/:username
Authorization: Bearer <token>
```

**Response `200`**
```json
{
  "username": "alice",
  "groups": [
    {
      "user_id": 1,
      "group_id": 1,
      "username": "alice",
      "group_name": "peerdrive-users",
      "created_at": "2026-04-28T12:05:00Z"
    }
  ]
}
```

**Response `200`** (no groups → empty array)
```json
{
  "username": "alice",
  "groups": []
}
```

**Curl**
```bash
curl -s http://localhost:4000/auth/group/alice \
  -H 'Authorization: Bearer <token>'
```

---

### 1.7 Add User to Group

```
POST /auth/group/:username
Authorization: Bearer <token>
```

**Request**
```json
{
  "group_name": "peerdrive-users"
}
```
The group is created on the fly if it does not exist.

**Response `200`**
```json
{
  "status": "added to group",
  "username": "alice",
  "group": "peerdrive-users"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/auth/group/alice \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{"group_name":"peerdrive-users"}'
```

---

### 1.8 Register Relay Node

```
POST /p2p/relay/register
```

**Request**
```json
{
  "peer_id": "12D3KooW...",
  "addrs": ["/ip4/1.2.3.4/tcp/4001"],
  "storage_mb": 10240,
  "version": "1.0.0"
}
```
No authentication required. The registration server stores the node and sets `last_heartbeat`.

**Response `200`**
```json
{
  "status": "registered"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/p2p/relay/register \
  -H 'Content-Type: application/json' \
  -d '{"peer_id":"12D3KooW...","addrs":["/ip4/1.2.3.4/tcp/4001"],"storage_mb":10240,"version":"1.0.0"}'
```

---

### 1.9 List Relay Nodes

```
GET /p2p/relay/list
```

**Response `200`**
```json
{
  "relays": [
    {
      "peer_id": "12D3KooW...",
      "addrs": ["/ip4/1.2.3.4/tcp/4001"],
      "storage_mb": 10240,
      "load_pct": 0,
      "version": "1.0.0",
      "registered_at": "2026-04-28T12:00:00Z",
      "last_heartbeat": "2026-04-28T12:05:00Z"
    }
  ]
}
```
Only relays with heartbeat within the last 5 minutes are returned.

**Curl**
```bash
curl -s http://localhost:4000/p2p/relay/list
```

---

### 1.10 Relay Heartbeat

```
POST /p2p/relay/heartbeat
```

**Request**
```json
{
  "peer_id": "12D3KooW...",
  "load_pct": 42.5
}
```
Updates `last_heartbeat` and `load_pct` for the relay node.

**Response `200`**
```json
{
  "status": "ok"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/p2p/relay/heartbeat \
  -H 'Content-Type: application/json' \
  -d '{"peer_id":"12D3KooW...","load_pct":42.5}'
```

---

### 1.11 Get Comments (by collection hash)

```
GET /comments/:hash
```

No authentication required. Returns all comments for a given collection hash.

**Response `200`**
```json
{
  "hash": "abc123...",
  "comments": [
    {
      "id": 1,
      "hash": "abc123...",
      "username": "alice",
      "content": "Great collection!",
      "created_at": "2026-04-28T12:10:00Z"
    }
  ],
  "total": 1
}
```

**Curl**
```bash
curl -s http://localhost:4000/comments/abc123def456
```

---

### 1.12 Post Comment (auth required)

```
POST /comments/:hash
Authorization: Bearer <token>
```

**Request**
```json
{
  "content": "Great collection!"
}
```

**Response `201`**
```json
{
  "id": 1,
  "hash": "abc123...",
  "username": "alice",
  "content": "Great collection!",
  "created_at": "2026-04-28T12:10:00Z"
}
```

**Response `401`** (no auth)
```json
{
  "error": "authentication required"
}
```

**Curl**
```bash
curl -s -X POST http://localhost:4000/comments/abc123def456 \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <token>' \
  -d '{"content":"Great collection!"}'
```

---

### 1.13 Registration Server Stats

```
GET /stats
```

**Response `200`**
```json
{
  "total_users": 5,
  "active_relays": 2,
  "total_comments": 12
}
```

**Curl**
```bash
curl -s http://localhost:4000/stats
```

---

## 2. Peerdrive Node Auth Endpoints

### 2.1 P2P Auth Status

```
GET /p2p/auth/status
Authorization: Bearer <token> (optional)
```

Returns the authentication status as determined by the peerdrive node's auth middleware (which validates the token against the registration server's `/auth/whoami`). Optional auth — with or without a token the endpoint succeeds.

**Response `200`** (with valid token)
```json
{
  "authenticated": true,
  "username": "alice",
  "role": "user"
}
```

**Response `200`** (no token)
```json
{
  "authenticated": false,
  "username": "",
  "role": ""
}
```

**Curl**
```bash
# With token
curl -s http://localhost:3000/p2p/auth/status \
  -H 'Authorization: Bearer <token>'

# Without token
curl -s http://localhost:3000/p2p/auth/status
```

---

## 3. Auth Middleware Design

The peerdrive node runs a Gin middleware that intercepts every request and validates Bearer tokens:

```
AuthOptional() — sets authenticated=false when no/invalid token, never rejects
AuthRequired() — returns 401 when no/invalid token
```

Both middleware functions call `GET <reg-server>/auth/whoami` with the token to validate it. The validation result is cached in the Gin context as `authenticated`, `username`, and `role`.

**Middleware config** (in peerdrive node env):
```
PEERDRIVE_REG_SERVER=http://localhost:4000
```

When `PEERDRIVE_REG_SERVER` is empty, auth is effectively disabled (all requests pass through as unauthenticated).

---

## 4. Relay Registry (peerdrive node → reg server)

Peerdrive nodes with P2P enabled and `PEERDRIVE_REG_SERVER_URL` set automatically:

1. **Register** as a relay via `POST /p2p/relay/register` at startup
2. **Heartbeat** every 60 seconds via `POST /p2p/relay/heartbeat`
3. **Bootstrap** connections by fetching the relay list via `GET /p2p/relay/list`

The registration server only returns relays with heartbeat within the last 5 minutes.

---

## 5. Test Users

For development and testing:

| Username | Password | Role |
|---|---|---|
| `testadmin` | `admin123` | `admin` |
| (dynamic test user) | `testpass123` | `user` |

---

## 6. 节点目录：用户 ↔ 节点（设计提案，2026-09-19）

**需求原文**：「Peerdrive Node 本身启动时就应该用用户账号登录注册服务器，把整个 node
（不只是 relay）的信息上报。其他 node 可以通过注册服务器查询『这个 peer 是谁在运营』。」
「node 有开放端点可以查询信息，其中一条就是运营者的账户。」

注意与第 4 节的区别：第 4 节只让 relay **匿名**登记；本节是**带账号**登记整个节点。

### 6.1 端点

```
POST /nodes/register      # 节点携带用户 token 登记自己（幂等 upsert）
POST /nodes/heartbeat     # 60s 心跳，刷新 last_seen / 地址 / 在线状态
DELETE /nodes/{peer_id}   # 注销（或心跳超时自动下线）
GET  /nodes/{peer_id}     # 公开查询：这个 peer 是谁在运营
GET  /users/{username}/nodes  # 某个账号有哪些在线节点
```

`POST /nodes/register` 请求体（字段都是节点自述，服务端只做校验与存储）：

```json
{
  "peer_id": "pd-7f3a1c2e",
  "endpoints": ["wss://node-a.example/peerjs"],
  "capabilities": { "relay": true, "bt": false, "ipfs": false },
  "version": "v0.4.0",
  "visibility": "public"
}
```

### 6.2 可见性（与匿合集三档同语义，便于前端复用同一套 UI 文案）

| visibility | 谁能查到 | 说明 |
|---|---|---|
| `public` | 任何人（含未认证） | 默认；`GET /nodes/{peer_id}` 返回运营者账号 |
| `registered` | 已认证用户 | 匿名查询只见「存在但不可见」 |
| `private` | 仅运营者本人 | 未授权查询返回 404（不确认存在性） |

### 6.3 关键决策（为什么这么做）

1. **未登录节点照常运行，只是不在目录里**。目录是「可选增值」而不是运行前提——
   与合集 `owner` 的语义一致（无 operator = 无主，但仍能用）。
2. **`peer_id` 是唯一键，账号不是**。同一账号可运营多节点；换账号登记同一
   `peer_id` 视为**转移归属**（需要旧账号在有效期内确认，否则等于任何人抢注别人的
   peer_id）。抢注是该模块最容易出的洞：注册接口必须校验「本次请求的 token 与
   该 peer_id 当前归属一致，或该 peer_id 尚未登记」。
3. **运营者账号是公开可查字段**，因此登记时必须由用户显式选择 visibility，
   不默认公开（默认值定为 `public` 便于发现，但 UI 上要明示「别人能看到你的账号」）。
4. 心跳超时（5 分钟，与 relay 一致）后从目录下线，避免僵尸节点被当成可信运营者。

---

## 7. 传输量统计与防谎报（设计提案，2026-09-19）

**需求原文**：「保存一些统计信息如上传下载量」「统计上传下载信息（需要防止谎报）」
「统计信息经过验证，会上传中心化注册认证服务器」「中继节点也可以统计中继流量」。

### 7.1 口径（先定义清楚，否则「防谎报」无从下手）

- 计数对象是**已校验的内容字节**：只有通过 sha256 校验的完整/分片传输才计账
  （`FetchFromPeer` 的 `fetchReader` 已经在 EOF 校验，天然是计账锚点）。
- 三个维度：`direction`（up / down）× `channel`（direct / relay）× `peer_id`。
  `channel=relay` 的流量同时被两端与中继方各记一次，用于 7.2 的交叉校验。
- 只上报**聚合值**（per-peer per-5min 窗口的字节数与会话数），不上报文件 hash 或
  文件名 —— 统计不等于内容监控。

### 7.2 防谎报：四道闸（核心设计）

单方面节点总能虚报自己的流量，所以**任何单方数据都不可信**，必须交叉：

| # | 机制 | 做法 | 挡住的谎报 |
|---|---|---|---|
| 1 | **对端会签** | 每 5 分钟窗口（或会话结束）双方互换计数摘要，用节点 Ed25519 私钥签名（`countersign`）。上报时携带对端签名。服务端只接受**双方数量差在容差内**的记录，入账取 `min(A上报, B上报)` | 单方虚报（虚报需要串通对端，成本大幅提高） |
| 2 | **中继兜底** | `channel=relay` 的流量由中继节点（第三方）独立统计上报，与两端三方比对 | 两端合谋虚报（中继是独立观察者） |
| 3 | **抽样挑战** | 服务端随机要求某节点对指定 hash 的随机 range 返回数据 + 计算 sha256；节点若根本没这条数据/没做过这些传输，挑战必然失败 | 纯凭空捏造（没有真实传输记录） |
| 4 | **物理上限 + 漂移检测** | 单窗口流量 ≤ 带宽上限 × 窗口时长；对节点的历史基线做移动平均，超阈值（如 3σ）先不入账并人工/自动复核 | 数量级级别的离谱值 |

**信誉与后果**：连续 N 个窗口校验失败 → 标记 `untrusted`（不派 relay 任务、不参与
流量榜），并在节点目录里体现。**不做自动封号**——误判代价高于收益。

### 7.3 上报接口

```
POST /stats/report          # 节点 → reg server，批量窗口
Authorization: Bearer <node token>
{
  "node_id": "pd-7f3a1c2e",
  "windows": [
    {
      "start": "2026-09-19T12:00:00Z", "end": "2026-09-19T12:05:00Z",
      "peer_id": "pd-9c11ab20",
      "direction": "up", "channel": "direct",
      "bytes": 104857600, "sessions": 3,
      "countersign": "base64(Ed25519 sig by peer_id over (window, direction, bytes))"
    }
  ]
}
```

```
GET /stats/me               # 自己的累计上/下行、按 peer 分解
GET /stats/nodes/{peer_id}  # 节点公开流量（仅 public 节点）
POST /stats/challenge       # 服务端发起抽样挑战（见 7.2 第 3 条）
```

### 7.4 待定决策（需要拍板）

1. **OAuth 要不要**：本设计**暂不引入**。中心化服务器自己就是唯一账号源，
   OAuth 的价值在第三方联合登录（GitHub/微信等），对「唯一公网服务器 + 自有账号」
   没有增量；真要做，按 **Authorization Code + PKCE**、以「一种登录方式」接入，
   不承担授权范围（scope）语义。现阶段 HTTP 只走 `Bearer <JWT>`。
2. **JWT 算法**：建议 **EdDSA/RS256**（非对称）而非 HS256——节点侧只需内置公钥
   即可验签，regserver 可轮换密钥（`kid` 头 + JWKS 端点）；HS256 需要把对称密钥
   发到每个节点，泄露即全盘沦陷。有效期 access 15min / refresh 7d。
3. **计账是否与激励挂钩**：若将来做流量奖励，7.1 的「只上报聚合」需要放宽到
   「聚合 + 内容 hash 前缀」，这会引入内容侧隐私问题，届时单独评审。
4. 中继流量的**计量起点**由中继决定（它看到的是加密包大小），与两端看到的
   明文大小会有固定开销差（帧头/分片），容差阈值必须容忍这个系统性偏差。

---

## 8. 注册用户的加密信道与 HTTP 鉴权（设计提案，2026-09-19）

**需求原文**：「如果是注册用户传输，p2p 时会进行简单加密信道，http 时也会带 auth」。

- **HTTP**：节点 → 节点、节点 → regserver 一律 `Authorization: Bearer <token>`。
  regserver 是**唯一且必须公网可达、仅 HTTP(S)** 的中心服务（不做 P2P 入口）；
  节点到 regserver 必须 TLS（HTTPS），否则 JWT 在链路上裸奔。
- **P2P**：WebRTC DataChannel 本身是 DTLS 加密（SCTP over DTLS），已经有链路级
  加密；本设计在此之上再加一层**应用层信封**，目的不是「更加密」而是：
  1. **绑定账号身份**：握手时双方用节点 Ed25519 密钥互签（nonce 挑战），
     把 `peer_id ↔ username` 的绑定做实，`restricted` 合集的 `requester` 才有依据
     （当前 P2P 同步路径传的是空 requester，见 REFACTOR §3.16 已知限制）。
  2. **中继场景不泄露**：`channel=relay` 时数据经过中继节点，
     应用层信封保证中继最多看到密文与计量元数据，不能顺手拿走内容。
- **算法**：X25519 ECDH 派生会话密钥 → ChaCha20-Poly1305；每会话随机数 + 定期
  rekey。不做前向保密之外的额外花活（需求是「简单加密信道」）。
- **匿名节点**：不强制加密信封（保持兼容），但**受限合集要求必须认证**
  （无身份者拿不到 restricted/private 内容），这是权限三档能成立的前提。

---

## 9. Relay 的位置：在 node 里，不在服务器里（设计提案，2026-09-19）

**需求原文**：「拥有账户可以在 relay 节点拖数据」「中继节点也可以统计中继流量」
「relay 集成在 node 中，而不是这个服务器中」。

- relay 是**节点自带能力**（`capabilities.relay = true` 登记进第 6 节目录），
  不是中心服务器的一部分 —— 服务器只负责目录、鉴权、统计与校验，不转发文件，
  因此服务器带宽/合规压力与网络规模解耦。
- 「拥有账户可以在 relay 节点拖数据」= 用账号身份经中继节点拉取自己（或被授权）
  的合集；中继只转发不落盘（或按配置做有限缓存，缓存命中仍要计账并标注
  `cache_hit=true`，避免把缓存当成真实端到端流量）。
- 中继流量按 7.1 的 `channel=relay` 口径统计，并作为 7.2 第 2 条的独立观察者上报；
  中继自己的运营者账号同样出现在节点目录里（谁在提供中继是可查的）。

---

## 10. 与现有代码的衔接点

| 需求 | 现状 | 缺口 |
|---|---|---|
| 用户注册/登录/JWT | regserver 已有 `/auth/register`、`/auth/login`、`/auth/whoami` | JWT 算法未定（见 7.4）；OAuth 未做（建议不做） |
| 节点侧鉴权中间件 | `AuthOptional`/`AuthRequired`，token 转发给 regserver 校验（第 3 节） | 每请求远程校验 → 应加本地缓存（TTL = token 剩余有效期） |
| 账号 ↔ 节点目录 | 只有 relay 匿名登记（第 4 节） | **第 6 节整套未做**；合集 `Owner` 目前取 `nodestate.GetOperator()`，来源就是这一层 |
| 流量统计 | 无 | **第 7 节整套未做**（含会签、挑战、容差） |
| 受限合集跨节点 | P2P 同步不携带 requester → 仅本节点可读（REFACTOR §3.16） | 需要第 8 节的身份绑定把 requester 带进同步请求 |
| relay | 端口转发 v2 已实现（REFACTOR §3.9）+ relay 登记/心跳/列表（第 4 节） | relay 流量统计与节点目录可见性 |

**落地顺序建议**：6（目录，依赖最小、能立刻支撑 owner 语义）→ 8（身份绑定，
解锁受限合集跨节点）→ 7（统计，依赖 6 的节点身份与会签密钥）→ 8/9 的 relay 统计。
