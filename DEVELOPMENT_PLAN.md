# Peerdrive Phase 2 开发计划：P2P 网络、中转、认证

## 当前状态

| 模块 | 现状 |
|------|------|
| P2P 网络 | libp2p host + Kademlia DHT + mDNS 发现 + Hole Punch + 3 个自定义协议（Exchange/Announce/Request） |
| 中转(Relay) | libp2p circuit relay v2 支持（client/server/off 三种模式），静态 relay 地址配置，WebSocket relay hub |
| 认证 | **空白** — `users` 表已建但无登录/注册 handler，无 JWT/API Key 中间件 |

---

## 一、P2P 网络部分

### 1.1 当前实现

```
libp2p Host
  ├── Kademlia DHT (ModeServer)
  ├── mDNS 局域网发现 (DiscoveryServiceTag = "peerdrive-mdns")
  ├── NAT 穿透 (AutoNATv2 + HolePunch + NATPortMap)
  ├── Circuit Relay v2 (EnableRelay/EnableRelayService)
  └── 3 个自定义 Stream 协议:
        ProtocolExchange  = "/peerdrive/exchange/1.0.0"  — 文件请求/响应
        ProtocolAnnounce  = "/peerdrive/announce/1.0.0"  — 文件公告
        ProtocolRequest   = "/peerdrive/request/1.0.0"   — WS relay 请求触发
```

已实现功能：
- `AnnounceHash(hash)` → DHT Provide
- `FindProviders(hash)` → DHT FindProviders
- `FetchFile(ctx, hash, peers)` → 依次尝试 peers → requestData
- `FetchCollection(ctx, hash, peers)` → FetchFile + JSON unmarshal
- `SyncFiles(ctx, peerID, hashes, targetDir)` → 批量拉取 + 本地存储
- `BroadcastRequest(hash, peerIDs)` → 向所有 peers 广播请求
- `PingPeer(ctx, peerID)` → libp2p ping 延迟测量

HTTP 端点（`/p2p/*`）：
- `GET /p2p/status` — 节点状态 + peer count + relay mode
- `GET /p2p/node` — peer ID + 多地址
- `GET /p2p/peers` — 已连接 peers
- `GET /p2p/discovered` — mDNS 发现的 peers
- `GET /p2p/ping/:peer_id` — ping 延迟
- `POST /p2p/connect` — 手动连接 peer
- `POST /p2p/announce` — 公告文件 hash
- `POST /p2p/fetch` — 从 P2P 拉取合集
- `POST /p2p/sync` — 同步合集文件到本地
- `POST /p2p/push` — 推送合集到远端
- `POST /p2p/request-file` — 广播文件请求
- `GET /p2p/ws/info` — WS 连接信息

### 1.2 待开发

#### A. 合集 P2P 发现与拉取 UI

**目标**：Plaza 页面的"公开合集"Tab 能显示 P2P 网络上发现的合集。

**设计方案**：
```
前端:
  Plaza.jsx → GET /p2p/discover-collections
          → 显示 P2P 发现的合集列表 (hash + name_preview + peer count)

后端:
  1. 新增 endpoint: GET /p2p/collections
     实现: 定时 DHT scan → 查找 cid type = anon_collection 的 provider
     → 返回 [{hash, name_preview, entry_count, provider_count, discovered_at}]
     
  2. 新增 endpoint: GET /p2p/collections/:hash
     实现: FindProviders → FetchCollectionFromPeer → 返回合集 JSON

  3. 前端 AnonExplorer 集成:
     - 当合集 hash 不在本地时, 自动尝试 P2P 拉取
     - 💾 保存按钮在 P2P 合集上有效 (拉取到本地 → 注册)
     - 显示 "来源: P2P 网络 (N 个对等节点)"
```

**测试点**：
- 两个节点启动 → mDNS 发现 → DHT announce → 互相看到合集
- 从远程节点拉取合集 → 显示在 Plaza 公开合集
- 对 P2P 合集点 💾 → 下载到本地 → isLocal 变为 true

**潜在问题**：
- DHT scan 开销大，需要本地缓存 (sqlite / memory)
- NAT 后节点可能无法直连，需要通过 relay
- 合集 JSON 较大 (含所有 entries)，拉取时间长

#### B. DHT 性能优化

**当前问题**：
- `AnnounceHash` 每次调 `DHT.Provide(ctx, cid, true)` — `true` 是阻塞式
- `FindProviders` 超时 30s 偏长
- 无本地 provider 缓存

**优化方案**：
```
1. AnnounceHash → 异步 goroutine + 重试队列
2. FindProviders → 10s 超时 + 本地 LRU 缓存 (hash → provider list)
3. 新增 provider_cache 表:
   CREATE TABLE p2p_provider_cache (
     hash TEXT PRIMARY KEY,
     providers TEXT,  -- JSON [{peer_id,addrs}]
     updated_at DATETIME
   )
4. 缓存过期策略: 5 分钟自动刷新，手动强制刷新
```

#### C. 文件交换流控

**当前问题**：
- `requestData` 30s 超时，大文件会超时
- 无进度回调
- 无断点续传

**优化方案**：
```
1. 分块传输协议:
   ProtocolExchange → 替换为分块版本
   请求: "CHUNKED <hash>\n"
   响应: "SIZE <total_size>\n" → "CHUNK <offset> <len>\n<data>" ... → "DONE\n"

2. 进度回调:
   type ProgressCallback func(hash string, downloaded, total int64)
   FetchFile(ctx, hash, peers, callback ProgressCallback)

3. 并发拉取:
   大文件从多个 peer 同时拉不同 chunk
```

### 1.3 P2P 前端界面

**目标**：P2PStatus 组件升级

**设计方案**：
```
P2PStatus.jsx 改造:
  ├── 节点信息卡片: peer_id, addrs, uptime
  ├── 网络拓扑图: 已连接 peers 的简单可视化
  ├── DHT 搜索: 输入 hash → 查询 providers → 拉取
  ├── 传输进度: 当前拉取的文件，进度条
  └── 公告操作: 选择本地合集 → 公告到 DHT
```

---

## 二、中转（Relay）部分

### 2.1 当前实现

```
P2PRelayMode:
  off     → 不使用 relay
  client  → 通过静态 relay 地址连接 (EnableAutoRelayWithStaticRelays)
  server  → 自己作为 relay 节点 (EnableRelayService + ForceReachabilityPublic)

WsHub:
  WebSocket 连接池 - 用于文件传输 relay
  ws/transfer 端点 - 用于跨节点文件交换
```

### 2.2 待开发

#### A. 强制 Relay 穿透

**目标**：当节点位于对称 NAT 后，自动通过 relay 完成文件交换。

**设计方案**：
```
1. 连接策略:
   Connect(peer) → try direct → try hole punch → try relay
   
2. 自动 relay 选择:
   - 定时测量到各 relay 节点的延迟
   - 选择延迟最低的 relay
   - 动态切换 relay

3. Relay 健康检查:
   P2PStatus 增加 relay_info:
   { relay_mode, relay_peer_id, relay_latency, relay_status }

4. 环境变量:
   PEERDRIVE_RELAY_PREFERRED=  # 优先使用的 relay peer ID
   PEERDRIVE_RELAY_MAX_HOPS=2  # 最大 relay 跳数
```

#### B. WS Relay 中转优化

**当前问题**：
- `wsHub` 是内存连接池，无持久化
- 单文件串行传输，无并发
- 无传输队列优先级

**优化方案**：
```
1. 传输队列:
   type TransferQueue struct {
     pending  []TransferTask
     active   map[string]*TransferTask
     maxConcurrent int (默认 4)
   }
   
2. TransferTask 优先级:
   - 当前页面浏览的文件 → HIGH
   - 后台批量同步 → LOW

3. 断点续传 (resume):
   - TransferTask 记录已传输字节
   - 连接断开后自动重连并续传

4. WS 心跳:
   - 30s 间隔 ping/pong
   - 超过 90s 无响应 → 断开并重新入队
```

#### C. Relay 节点部署手册

**目标**：编写 relay 节点的标准部署方案。

```
1. 公网 VPS 部署 relay server:
   PEERDRIVE_P2P_RELAY_MODE=server
   PEERDRIVE_P2P_RELAY_ENABLE=true
   PEERDRIVE_P2P_PUBLIC_REACHABLE=true

2. 防火墙开放:
   TCP 4001 (libp2p 默认)
   可配置为 443 以绕过企业防火墙

3. 负载策略:
   单 relay 支持 ~200 并发连接
   多 relay 通过 PEERDRIVE_P2P_STATIC_RELAYS 配置多个地址
```

---

## 三、认证部分

### 3.1 当前状态

**空白** — `users` 表在 DB schema 中已定义但无任何代码使用：
```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  authkey TEXT UNIQUE,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### 3.2 认证设计层次

```
三层认证体系:
  Level 1: 节点级认证 (AuthKey)    — 前端 ↔ 后端 API 访问控制
  Level 2: 用户级认证 (JWT)        — 注册/登录/会话管理  
  Level 3: P2P 级认证 (Peer Auth)  — 节点间互信
```

### 3.3 Level 1: AuthKey 节点认证

**目标**：前端请求 API 时携带 AuthKey header，后端校验。

**设计方案**：
```
1. 生成 AuthKey:
   POST /auth/register
     body: { username, password }
     → 创建用户 → 生成 32 字节随机 authkey → sha256 存储
     response: { username, authkey }

2. AuthKey 校验中间件:
   func AuthMiddleware() gin.HandlerFunc {
     key := c.GetHeader("Authorization")
     // Bearer <authkey> 格式
     // 查 users 表中 authkey 的 SHA256
     // 匹配 → c.Set("username", username) → c.Next()
     // 不匹配 → 401 Unauthorized
   }

3. 前端存储:
   localStorage.setItem('peerdrive_auth_key', authkey)
   Settings.jsx 已有 auth key 输入框

4. API 保护:
   需要认证的路由组:
   - POST /collections/*  (创建/修改合集)
   - DELETE /files/*      (删除文件)
   - POST /actions/*      (merge/fork/pull)
   不需要认证的:
   - GET /*               (读取操作公开)
```

**测试点**：
- 无 Authorization header → 401
- 错误 authkey → 401
- 正确 authkey → 通过，c.Get("username") 有值
- Settings 页面输入 authkey → 保存 → 后续请求自动携带

### 3.4 Level 2: JWT 用户认证

**目标**：用户注册、登录、会话管理。

**设计方案**：
```
1. 注册:
   POST /auth/register
     body: { username, password }
     → bcrypt(password) → INSERT users
     → 返回 JWT token (包含 username, exp)
     response: { token, username }

2. 登录:
   POST /auth/login
     body: { username, password }
     → SELECT users WHERE username → bcrypt.Compare
     → 成功 → 生成 JWT → 更新 last_login
     response: { token, username }

3. JWT 中间件:
   func JWTAuth() gin.HandlerFunc {
     token := c.GetHeader("Authorization") // Bearer <jwt>
     → jwt.Parse(token, secretKey)
     → 验证 exp + issuer
     → c.Set("user_id", claims.UserID) → c.Next()
   }

4. 环境变量:
   PEERDRIVE_JWT_SECRET=<random 64 char>
   PEERDRIVE_JWT_EXPIRY=168h  # 7 days

5. 密码要求:
   - 最小 8 字符
   - 至少包含字母和数字
   - bcrypt cost = 12
```

**测试点**：
- 注册 → 返回 token → 可用 token 访问受保护端点
- 登录 → 返回 token
- token 过期 → 401
- 错误密码 → 401
- 重复用户名 → 409 Conflict

### 3.5 Level 3: P2P 节点互信

**目标**：节点之间互相识别为可信对等方，允许自动同步。

**设计方案**：
```
1. Peer 身份绑定:
   libp2p 的 PeerID 是可自签名的，需要绑定到注册用户
   新增表: peer_identities
     peer_id TEXT PRIMARY KEY,
     username TEXT NOT NULL,
     verified_at DATETIME,
     FOREIGN KEY (username) REFERENCES users(username)

2. Peer 注册:
   POST /p2p/register-peer
     body: { peer_id, signed_challenge }
     → 验证签名 (用户私钥签名 challenge)
     → INSERT peer_identities
     → 该 peer 被标记为 verified

3. 互信策略:
   - verified peers: 自动接受 sync/fetch 请求
   - unverified peers: 需要手动确认
   - blocked peers: 拒绝所有请求

4. 权限控制:
   新增表: peer_permissions
     peer_id TEXT,
     permission TEXT,  -- 'read', 'sync', 'announce'
     FOREIGN KEY (peer_id) REFERENCES peer_identities(peer_id)
```

### 3.6 认证前端 UI

**目标**：提供用户注册/登录界面。

**设计方案**：
```
Settings.jsx 扩展:
  ├── 认证状态: "未登录" / "已登录: username"
  ├── 登录表单: username + password → POST /auth/login
  ├── 注册表单: username + password + confirm → POST /auth/register
  ├── AuthKey 显示: 复制按钮 + 重新生成
  └── 节点绑定: 当前 PeerID + 绑定状态
```

---

## 四、开发时间线

| 阶段 | 内容 | 预估 |
|------|------|------|
| Phase 2.1 | P2P 合集发现 (DHT scan + 缓存) | 3天 |
| Phase 2.2 | P2P 前端 UI (Plaza P2P collections) | 2天 |
| Phase 2.3 | AuthKey 认证 (middleware + 注册) | 2天 |
| Phase 2.4 | JWT 登录体系 | 2天 |
| Phase 2.5 | Relay 优化 (队列 + 断点续传) | 2天 |
| Phase 2.6 | P2P 节点互信 | 2天 |
| Phase 2.7 | 集成测试 + 文档 | 2天 |

---

## 五、潜在技术风险

| 风险 | 应对 |
|------|------|
| libp2p DHT 不稳定 | 降级为直连 + mDNS + Relay 混合策略 |
| NAT 穿透成功率低 | 强制所有 non-public 节点通过 relay 通信 |
| JWT secret 泄露 | 支持 revocation list + 短有效期 |
| relay 单点故障 | 多 relay 冗余 + 自动故障转移 |
| SQLite 并发写入 | P2P 缓存表使用 WAL 模式 + 写入队列 |

---

## 六、api.js 新增端点

```javascript
// Auth
export const register = (username, password) => request('POST', '/auth/register', { username, password });
export const login = (username, password) => request('POST', '/auth/login', { username, password });
export const getMe = () => request('GET', '/auth/me');

// P2P Discovery
export const discoverP2PCollections = () => request('GET', '/p2p/collections');
export const getP2PCollection = (hash) => request('GET', `/p2p/collections/${hash}`);

// P2P Peer Auth
export const registerPeer = (peerId, signedChallenge) => request('POST', '/p2p/register-peer', { peer_id: peerId, signed_challenge });
```

---

## 七、Go 后端新增文件

| 文件 | 功能 |
|------|------|
| `internal/service/auth_service.go` | AuthKey + JWT + bcrypt |
| `internal/controller/auth.go` | register/login/me handlers |
| `internal/middleware/auth.go` | AuthMiddleware + JWTAuth |
| `internal/service/p2p_discovery.go` | DHT scan + 缓存 + collection discovery |
| `internal/controller/p2p_collections.go` | GET /p2p/collections |
| `internal/model/peer.go` | PeerIdentity + PeerPermission |
| `internal/repository/auth_repo.go` | users 表 CRUD |
