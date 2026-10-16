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

---

## 八、逐步实现指南

### Phase 2.1: P2P 合集发现（3天）

#### Day 1: DHT Scan & Cache

**要做什么**：
1. 在 `internal/service/p2p_discovery.go` 中实现 `DiscoveryService` 结构体
2. DHT 定时扫描（每 30 秒）查找类型为 `anon_collection` 的文件
3. 扫描结果缓存在 `p2p_provider_cache` 表中
4. 返回 `P2PCollectionItem[]` 结构体

**Go 代码结构**：
```go
// internal/service/p2p_discovery.go
package service

import (
    "context"
    "sync"
    "time"
    "peerdrive/internal/model"
    "peerdrive/internal/repository"
)

type DiscoveryService struct {
    p2p      *P2PService
    mu       sync.RWMutex
    cache    map[string]*P2PCollectionItem
    stopCh   chan struct{}
}

type P2PCollectionItem struct {
    Hash           string   `json:"hash"`
    NamePreview    string   `json:"name_preview"`
    EntryCount     int      `json:"entry_count"`
    ProviderCount  int      `json:"provider_count"`
    DiscoveredAt   string   `json:"discovered_at"`
    Tags           []string `json:"tags,omitempty"`
}

func NewDiscoveryService(p2p *P2PService) *DiscoveryService {
    return &DiscoveryService{
        p2p:    p2p,
        cache:  make(map[string]*P2PCollectionItem),
        stopCh: make(chan struct{}),
    }
}

func (d *DiscoveryService) Start(ctx context.Context) {
    go d.scanLoop(ctx)
}

func (d *DiscoveryService) scanLoop(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            d.scan(ctx)
        case <-d.stopCh:
            return
        case <-ctx.Done():
            return
        }
    }
}

func (d *DiscoveryService) scan(ctx context.Context) {
    // 1. 查询本地已知的 anon_collection 类型的 hash
    rows, err := repository.DB.Query(
        `SELECT hash FROM file_meta WHERE type = ?`, repository.FileTypeAnonCollection)
    if err != nil { return }
    defer rows.Close()
    
    for rows.Next() {
        var hash string
        rows.Scan(&hash)
        
        // 2. 通过 DHT 查找 providers
        providers, err := d.p2p.FindProviders(hash)
        if err != nil || len(providers) == 0 { continue }
        
        // 3. 尝试从第一个 provider 拉取合集 JSON 获取 metadata
        coll, err := d.p2p.FetchCollection(ctx, hash, providers)
        if err != nil { continue }
        
        // 4. 构建 item 并缓存
        item := &P2PCollectionItem{
            Hash:          hash,
            NamePreview:   coll.FriendlyName,
            EntryCount:    len(coll.Entries),
            ProviderCount: len(providers),
            DiscoveredAt:  time.Now().UTC().Format(time.RFC3339),
            Tags:          coll.Tags,
        }
        // name_preview from entries
        if item.NamePreview == "" && len(coll.Entries) > 0 {
            names := make([]string, 0, 3)
            for i, e := range coll.Entries {
                if i >= 3 { break }
                names = append(names, e.Path)
            }
            item.NamePreview = strings.Join(names, ", ")
        }
        
        d.mu.Lock()
        d.cache[hash] = item
        d.mu.Unlock()
    }
}

func (d *DiscoveryService) GetCollections() []P2PCollectionItem {
    d.mu.RLock()
    defer d.mu.RUnlock()
    result := make([]P2PCollectionItem, 0, len(d.cache))
    for _, item := range d.cache {
        result = append(result, *item)
    }
    return result
}
```

**数据库迁移**：
```sql
-- internal/repository/db.go 新增
CREATE TABLE IF NOT EXISTS p2p_provider_cache (
    hash TEXT PRIMARY KEY,
    providers TEXT,        -- JSON array of peer info
    collection_data TEXT,  -- JSON of collection metadata
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

#### Day 2: HTTP 端点 + 前端集成

**要做什么**：
1. 实现 `GET /p2p/collections` 端点
2. 实现 `GET /p2p/collections/:hash` 端点
3. 在 Plaza.jsx 中集成 P2P 合集列表

**Go controller**：
```go
// internal/controller/p2p_collections.go
func ListP2PCollections(c *gin.Context) {
    items := discoverySvc.GetCollections()
    c.JSON(http.StatusOK, gin.H{"data": items})
}

func GetP2PCollection(c *gin.Context) {
    hash := c.Param("hash")
    // 尝试本地 → 失败则 P2P 拉取
    coll, err := anonSvc.GetCollectionByHash(hash)
    if err != nil {
        ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
        defer cancel()
        coll, err = p2pSvc.FetchCollection(ctx, hash, nil)
        if err != nil {
            c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
            return
        }
    }
    c.JSON(http.StatusOK, coll)
}
```

**前端 Plaza.jsx 集成**：
```jsx
// 在 loadAll() 中新增 P2P 合集加载
const loadAll = async () => {
    setLoading(true);
    try {
        const [anon, pub, p2p] = await Promise.all([
            listAnonCollections().catch(() => []),
            listPublicCollections().catch(() => ({ collections: [] })),
            api.discoverP2PCollections().catch(() => ({ data: [] })),
        ]);
        const merged = [
            ...(Array.isArray(anon) ? anon.map(c => ({ ...c, _type: 'anon' })) : []),
            ...((pub.collections || pub.data || []).map(c => ({ ...c, _type: 'public' }))),
            ...((p2p.data || []).map(c => ({ ...c, _type: 'p2p', hash: c.hash, friendly_name: c.name_preview }))),
        ];
        setCollections(merged);
    } catch { setCollections([]); }
    setLoading(false);
};
// P2P 合集卡片增加标识
{c._type === 'p2p' && (
    <p className="text-[10px] text-purple-400/60 mt-2 flex items-center gap-1">
        <span>🌐</span> P2P 网络 · {c.provider_count || '?'} 节点
    </p>
)}
```

#### Day 3: 缓存优化 + 测试

**要做什么**：
1. 实现 `AnnounceHash` 异步化
2. 实现 provider 缓存过期策略
3. 测试双节点环境下的 P2P 合集发现

**AnnounceHash 异步化**：
```go
func (p *P2PService) AnnounceHashAsync(hash string) {
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        if err := p.AnnounceHash(hash); err != nil {
            logf("announce %s failed: %v", hash, err)
        }
    }()
}
```

**测试场景**：
```
节点 A (public IP)   ←→   节点 B (NAT behind)
  1. A 创建合集 → AnnounceHash → DHT Provide
  2. B 启动 DiscoveryService → DHT scan
  3. B GET /p2p/collections → 返回 A 的合集
  4. B 点击合集 → GET /p2p/collections/:hash → 从 A 拉取
  5. B 💾 保存 → 注册到本地 file_meta
```

---

### Phase 2.2: 前端 P2P UI（2天）

#### Day 1: P2PStatus 组件升级

**要做什么**：
1. 重新设计 P2PStatus.jsx — 从纯文本表格升级为可视化面板
2. 增加节点信息卡片、传输进度、DHT 搜索

**组件结构**：
```jsx
// components/P2PStatus.jsx 重写
export default function P2PStatus() {
  const [status, setStatus] = useState(null);
  const [transfers, setTransfers] = useState([]);
  const [dhtQuery, setDhtQuery] = useState('');
  const [dhtResults, setDhtResults] = useState([]);

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 5000);
    return () => clearInterval(interval);
  }, []);

  const fetchStatus = async () => {
    try { setStatus(await api.getP2PStatus()); } catch {}
  };

  // UI sections:
  return (
    <div className="space-y-4">
      {/* 1. 节点状态卡片 */}
      <NodeCard status={status} />

      {/* 2. 连接对等方列表 */}
      <PeerList peers={status?.peers} />

      {/* 3. DHT 搜索 */}
      <DHTSearch query={dhtQuery} results={dhtResults} onSearch={handleDHTSearch} />

      {/* 4. 传输进度 */}
      <TransferPanel transfers={transfers} />
    </div>
  );
}
```

#### Day 2: 合集 P2P 来源指示器

**要做什么**：
1. AnonExplorer 中标记 P2P 来源
2. 远程合集添加下载进度
3. 公告按钮功能

**AnonExplorer 集成**：
```jsx
// 在 collection header 中增加来源标识
{source === 'p2p' && (
  <span className="text-xs text-purple-400 bg-purple-400/10 px-2 py-0.5 rounded">
    🌐 P2P · {providerCount} 节点
  </span>
)}
{source === 'local' && (
  <span className="text-xs text-green-400 bg-green-400/10 px-2 py-0.5 rounded">
    ✓ 本地
  </span>
)}
```

---

### Phase 2.3-2.4: AuthKey + JWT 认证（4天）

#### 实现清单

**要做什么**：
```
├── internal/middleware/auth.go
│     ├── AuthMiddleware() — Bearer Token 校验
│     └── OptionalAuth() — 可选认证（用于读操作）
├── internal/repository/auth_repo.go
│     ├── CreateUser(username, passwordHash, authkeyHash)
│     ├── GetUserByUsername(username)
│     ├── GetUserByAuthKey(authkeyHash)
│     └── UpdateLastLogin(username)
├── internal/service/auth_service.go
│     ├── Register(username, password) → (user, authkey)
│     ├── Login(username, password) → JWT token
│     ├── ValidateAuthKey(key) → username
│     └── GenerateJWT(username) → token string
├── internal/controller/auth.go
│     ├── POST /auth/register
│     ├── POST /auth/login
│     └── GET /auth/me
└── internal/router/router.go
      ├── authGroup := r.Group("/auth")
      ├── authGroup.POST("/register", controller.Register)
      ├── authGroup.POST("/login", controller.Login)
      └── authGroup.GET("/me", controller.GetMe)
```

**AuthService 核心代码**：
```go
// internal/service/auth_service.go
package service

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "golang.org/x/crypto/bcrypt"
    "peerdrive/internal/repository"
)

var (
    ErrUserExists     = errors.New("username already taken")
    ErrInvalidCreds   = errors.New("invalid username or password")
    ErrInvalidAuthKey = errors.New("invalid auth key")
)

type AuthService struct {
    jwtSecret []byte
}

func NewAuthService(secret string) *AuthService {
    return &AuthService{jwtSecret: []byte(secret)}
}

func (s *AuthService) Register(username, password string) (string, error) {
    // 1. 检查用户名是否已存在
    existing, _ := repository.GetUserByUsername(username)
    if existing != nil {
        return "", ErrUserExists
    }

    // 2. bcrypt 密码
    hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
    if err != nil {
        return "", err
    }

    // 3. 生成 32 字节 authkey
    authkey := make([]byte, 32)
    rand.Read(authkey)
    authkeyStr := hex.EncodeToString(authkey)

    // 4. SHA256(authkey) 存储
    authkeyHash := sha256Hex(authkey)

    // 5. 写入数据库
    if err := repository.CreateUser(username, string(hash), authkeyHash); err != nil {
        return "", err
    }

    return authkeyStr, nil
}

func (s *AuthService) Login(username, password string) (string, error) {
    user, err := repository.GetUserByUsername(username)
    if err != nil || user == nil {
        return "", ErrInvalidCreds
    }

    if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
        return "", ErrInvalidCreds
    }

    repository.UpdateLastLogin(username)

    return s.GenerateJWT(username)
}

func (s *AuthService) GenerateJWT(username string) (string, error) {
    claims := jwt.MapClaims{
        "username": username,
        "exp":      time.Now().Add(7 * 24 * time.Hour).Unix(),
        "iat":      time.Now().Unix(),
    }
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString(s.jwtSecret)
}

func (s *AuthService) ValidateAuthKey(key string) (string, error) {
    authkeyHash := sha256Hex([]byte(key))
    user, err := repository.GetUserByAuthKey(authkeyHash)
    if err != nil || user == nil {
        return "", ErrInvalidAuthKey
    }
    return user.Username, nil
}

func sha256Hex(data []byte) string {
    h := sha256.Sum256(data)
    return hex.EncodeToString(h[:])
}
```

**AuthMiddleware**：
```go
// internal/middleware/auth.go
package middleware

import (
    "net/http"
    "strings"
    "github.com/gin-gonic/gin"
    "github.com/golang-jwt/jwt/v5"
    "peerdrive/internal/service"
)

func AuthRequired(authSvc *service.AuthService) gin.HandlerFunc {
    return func(c *gin.Context) {
        header := c.GetHeader("Authorization")
        if header == "" {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
            return
        }

        // Bearer <token>
        parts := strings.SplitN(header, " ", 2)
        if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
            return
        }
        token := parts[1]

        // 尝试 JWT 解析
        if username, ok := parseJWT(token, authSvc); ok {
            c.Set("username", username)
            c.Next()
            return
        }

        // 尝试 AuthKey 校验
        if username, err := authSvc.ValidateAuthKey(token); err == nil {
            c.Set("username", username)
            c.Next()
            return
        }

        c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
    }
}
```

**路由保护**：
```go
// router.go
authGroup := r.Group("/auth")
{
    authGroup.POST("/register", controller.Register)
    authGroup.POST("/login", controller.Login)
}

protected := r.Group("")
protected.Use(middleware.AuthRequired(authSvc))
{
    protected.POST("/collections", controller.CreateCollection)
    protected.POST("/collections/:username/:collection_name/commit", controller.CommitCollection)
    protected.DELETE("/files/:hash", controller.DeleteFile)
    protected.POST("/actions/merge", controller.MergeFromSource)
    protected.POST("/actions/fork", controller.ForkCollection)
}
```

---

### Phase 2.5: Relay 优化（2天）

**要做什么**：
1. 实现传输队列 (TransferQueue)
2. 实现断点续传
3. 实现 relay 自动故障转移

**TransferQueue 实现**：
```go
// internal/service/transfer_queue.go
package service

import (
    "context"
    "sync"
)

type Priority int
const (
    PriorityHigh   Priority = 0
    PriorityNormal Priority = 1
    PriorityLow    Priority = 2
)

type TransferTask struct {
    ID         string
    Hash       string
    PeerID     string
    Priority   Priority
    Progress   int64
    Total      int64
    Status     string // "pending","active","paused","done","failed"
    ResumeFrom int64
    CreatedAt  int64
}

type TransferQueue struct {
    mu            sync.Mutex
    pending       []*TransferTask
    active        map[string]*TransferTask
    maxConcurrent int
    onProgress    func(task *TransferTask)
}

func NewTransferQueue(maxConcurrent int) *TransferQueue {
    return &TransferQueue{
        pending:       make([]*TransferTask, 0),
        active:        make(map[string]*TransferTask),
        maxConcurrent: maxConcurrent,
    }
}

func (q *TransferQueue) Enqueue(task *TransferTask) {
    q.mu.Lock()
    defer q.mu.Unlock()
    q.pending = append(q.pending, task)
    q.tryProcess()
}

func (q *TransferQueue) tryProcess() {
    for len(q.active) < q.maxConcurrent && len(q.pending) > 0 {
        task := q.pending[0]
        q.pending = q.pending[1:]
        task.Status = "active"
        q.active[task.ID] = task
        go q.executeTask(task)
    }
}

func (q *TransferQueue) executeTask(task *TransferTask) {
    // 实际传输逻辑——分块拉取 + 进度回调
    defer func() {
        q.mu.Lock()
        delete(q.active, task.ID)
        q.mu.Unlock()
        q.tryProcess()
    }()
    // ... 调用 p2pSvc.FetchFile with progress callback
}
```

**Relay 故障转移**：
```go
func (p *P2PService) ConnectWithFallback(ctx context.Context, peerID peer.ID) error {
    strategies := []func(context.Context, peer.ID) error{
        p.tryDirectConnect,
        p.tryHolePunch,
        p.tryRelayConnect,
    }
    
    for _, strategy := range strategies {
        if err := strategy(ctx, peerID); err == nil {
            return nil
        }
    }
    return fmt.Errorf("all connection strategies failed for %s", peerID)
}
```

---

## 九、安全考量

### AuthKey 安全
- authkey 在 DB 中存储 SHA256（不存储明文）
- 支持重新生成 authkey（旧 key 立即失效）
- 支持多个 authkey（便于切换客户端）

### JWT 安全
- Secret 通过环境变量注入，不硬编码
- 短有效期（7 天），支持 refresh token
- Claims 包含 username + exp + iat
- 拒绝算法混淆攻击——只允许 HS256

### P2P 安全
- 未认证 peer 拒绝自动同步
- 文件 hash 校验防篡改
- 限制同时传输文件数量防 DoS

---

## 十、前端认证流程

```
Settings.jsx 认证面板:
  ┌─────────────────────────────┐
  │ 认证状态: [未登录]          │
  │                             │
  │ 用户名: [________]         │
  │ 密码:   [________]         │
  │ [注册] [登录]              │
  │                             │
  │ ── 或使用 AuthKey ──      │
  │ AuthKey: [____________]    │
  │ [保存 Key]                 │
  └─────────────────────────────┘

认证流程:
  1. 用户输入 username + password → POST /auth/login
  2. 后端返回 JWT token
  3. 前端存储: localStorage.setItem('peerdrive_token', token)
  4. 后续请求: api.js 的 request() 函数自动附加 Authorization header
  5. Token 过期 → 自动跳转登录页

api.js 改造:
  function request(method, path, body = null) {
    const opts = { method, headers: {} };
    const token = localStorage.getItem('peerdrive_token');
    const authkey = localStorage.getItem('peerdrive_auth_key');
    if (token) opts.headers['Authorization'] = `Bearer ${token}`;
    else if (authkey) opts.headers['Authorization'] = `Bearer ${authkey}`;
    if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    ...
  }
```

---

## 十一、数据库迁移脚本

```sql
-- migration_002_auth.sql
ALTER TABLE users ADD COLUMN last_login DATETIME;
ALTER TABLE users ADD COLUMN jwt_version INTEGER DEFAULT 1;

-- migration_003_p2p.sql
CREATE TABLE IF NOT EXISTS p2p_provider_cache (
    hash TEXT PRIMARY KEY,
    providers TEXT,
    collection_data TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS peer_identities (
    peer_id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    verified_at DATETIME,
    FOREIGN KEY (username) REFERENCES users(username)
);

CREATE TABLE IF NOT EXISTS peer_permissions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    peer_id TEXT NOT NULL,
    permission TEXT NOT NULL,
    FOREIGN KEY (peer_id) REFERENCES peer_identities(peer_id),
    UNIQUE(peer_id, permission)
);
```

---

## 十二、目录结构（Phase 2 完成后）

```
go/
├── internal/
│   ├── config/config.go          (新增 Auth 相关 env vars)
│   ├── controller/
│   │   ├── auth.go               (新建: register/login/me)
│   │   ├── p2p_collections.go    (新建: P2P collection discovery)
│   │   └── ...
│   ├── middleware/
│   │   └── auth.go                (新建: AuthMiddleware + JWTAuth)
│   ├── model/
│   │   ├── peer.go                (新建: PeerIdentity + Permission)
│   │   └── ...
│   ├── repository/
│   │   ├── auth_repo.go           (新建: users 表 CRUD)
│   │   └── db.go                  (修改: 新增表 migration)
│   ├── service/
│   │   ├── auth_service.go        (新建: Register/Login/JWT)
│   │   ├── p2p_discovery.go       (新建: DHT scan + cache)
│   │   ├── transfer_queue.go      (新建: 传输队列)
│   │   └── p2p.go                 (修改: 异步 Announce + 分块)
│   └── router/
│       └── router.go               (修改: 新增路由组)
```

---

## 十三、前端新增/修改文件

```
react/src/
├── api.js                          (修改: 新增 auth + p2p 端点)
├── pages/
│   ├── Settings.jsx                (修改: 认证面板)
│   ├── Plaza.jsx                   (修改: P2P 合集集成)
│   ├── AnonExplorer.jsx            (修改: P2P 来源指示器)
│   └── ...
├── components/
│   ├── P2PStatus.jsx               (重写: 可视化面板)
│   ├── AuthPanel.jsx               (新建: 注册/登录面板)
│   └── TransferProgress.jsx        (新建: 传输进度条)
└── ...
```

---

## 十四、全部 phases 完成后的 TODO 检查清单

- [ ] 两个节点能通过 mDNS 发现彼此
- [ ] 两个节点能通过 DHT 发现彼此的合集
- [ ] Plaza 公开合集 Tab 能显示 P2P 合集
- [ ] P2P 合集能通过 DHT 拉取到本地
- [ ] 远程文件通过 WS relay 中转传输
- [ ] 大文件分块传输 + 进度回调
- [ ] 断点续传：断开后从上次位置继续
- [ ] 用户能注册账号（bcrypt 密码）
- [ ] 用户能登录并获取 JWT token
- [ ] 受保护的 API 端点要求 Authorization header
- [ ] AuthKey 校验：Bearer key → 识别用户
- [ ] Peer 身份绑定到注册用户
- [ ] Verified peer 自动接受同步请求
- [ ] P2PStatus 面板显示节点状态 + 传输进度
- [ ] Settings 页有完整的认证面板
- [ ] Frontend 自动携带 token 发起请求
- [ ] `go build ./...` 编译通过
- [ ] `npm run build` 编译通过
- [ ] 双节点集成测试通过
