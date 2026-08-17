# Peerdrive 节点（Go）功能与 P2P 连接体系

> 代码：`back/cmd/server/main.go`（入口）+ `back/internal/transport/`（互联层）+ `back/peerjs/`（传输原语）。
> 节点职责：**常驻在线 + 内容寻址存储（sha256）+ 双向文件服务 + 端口转发**，与浏览器/其它节点互联。

## 1. 节点功能总览

| 功能 | 入口 | 说明 |
|---|---|---|
| 信令注册 | `peerjs_service.go` Start/startLoop | PeerJS WS 连公共云或自托管；断线指数退避（2s→60s）整轮重连（H7） |
| 主动互联 | `connectLoop` | `PEERDRIVE_PEERJS_PEERS` 静态配置 + 发现回调；无限重试到 OnOpen |
| 被动接收 | `onIncomingConnection` | 等 OnOpen 才 bindConn（坑：过早绑定会拿未就绪连接） |
| 文件服务（入站） | `inbound.go` | serveFile（64KB 分块+流控）、索引 verb（create/upload/list/info/delete/sync）、上传分片 worker |
| 文件拉取（出站） | `outbound.go` | `OpenStream` 流式拉取（UUID reqId 路由+有界队列）+ sha256 校验兜底；`FetchFromPeer` 兼容封装 |
| 本地会话 | `/ws/peer` | 浏览器 WS 直连本节点，帧协议与 DataChannel 一致，毫秒级无需打洞 |
| 房间发现 | `http_discovery.go`/`mqtt_discovery.go` | 自托管发现 API 优先（`PEERDRIVE_DISCOVER_URL`），否则 MQTT 分片房间 |
| 端口转发 v2 | `forward.go` | HMAC 质询认证 + 端口白名单，DataChannel 上承载 TCP 隧道 |
| 统一 source | `main.go:98` | LocalSource（file_index+CAS）→ PeerSource（p2p 透传）→ URLSource（模板） |
| 管理端点 | `peerjs_routes.go`/`source_routes.go` | `/peerjs/node`、`/peerjs/fetch`、`/sources` |

启动流程：`InitDB → NewPeerJSService → SetupRouter → Gin :PORT`。

## 2. P2P 连接分类

### 2.1 按传输类型（2 类，`Session` 接口统一，`ws_session.go:22`）

| 类型 | 适配器 | id | 用途 |
|---|---|---|---|
| **WebRTC DataChannel** | `rtc_session.go` 包 `*peerjs.Connection` | 远端 peer id | 远端节点 / 浏览器经信令直连（NAT 打洞） |
| **本地 WebSocket** | `ws_session.go`（`NewWSSession`） | `"local"` | 浏览器直连本节点，无信令/打洞开销 |

两者语义完全一致：同一 reqId 状态机、同一帧协议（文本帧=JSON 头，二进制帧=数据块），
`Session` 接口抽象后 `FetchFromPeer("local", ...)` 与远端拉取零分支复用。

### 2.2 按建立方向（信令层角色，`peer.go`）

- **offerer（主动方）**：`connectLoop` → `peer.Connect(ctx, dst, label)` → 发 OFFER
- **answerer（被动方）**：收到 OFFER → `handleOffer` 创建 Connection → 回 ANSWER
- 约束：answerer 必须沿用 offerer 的 `connectionId`，否则 ANSWER 路由不到（REFACTOR §5 第一坑）
- 重连：connectLoop 无限循环 + 指数退避（EXPIRE / ICE 失败 / 对端断开都会触发）

### 2.3 按帧角色（inbound / outbound，`conn.go` 头注释）

WebRTC 连接全双工对称，同一条 Session **同时承载两角色**（可一边 serve 对端 req，
一边收集自己请求的响应），不共享任何可变状态（除 connState 内各自槽位）：

| 角色 | 归属 | 文件 |
|---|---|---|
| **inbound**（入站 =「别人问我答」） | 应答 verb：`req`（serveFile）、`create/upload/list/info/delete/sync`（serve* 索引）、`fwd-open/fwd-auth/fwd-data/fwd-close`（转发） | `inbound.go` + `forward.go` |
| **outbound**（出站 =「我问别人」） | 发起 `req`（requestFile/openStream）并收集 `meta/data/done/err`（routeResponse）；客户端侧转发握手 `OpenForward` | `outbound.go` + `forward.go` |
| **共享机制**（只此一份） | 帧类型、reqId 状态机、二进制块路由、流控 | `conn.go` |

### 2.4 按用途

- **文件传输**：拉取（req 流式）+ 分片上传（upload，64KB chunk 位图）+ 索引同步（sync，seq 游标增量）
- **端口转发隧道**：同一连接单槽 `connState.fwd`（同时一条活跃转发流）；fwd-data 块路由优先于文件数据（二进制块按「fwd-data 头声明归属」先行）

## 3. 连接建立流程（全链路）

```
发现（HTTP 轮询 10s / MQTT 心跳 / PEERS 静态配置）
   │ onDiscoveredPeer / 配置解析
   ▼
connectLoop(peerID)  ──去重（connecting map）──►  peer.Connect
   │ OFFER {dst, connectionId, SDP} ──信令──► 对端 handleOffer
   │ ◄── ANSWER（沿用 connectionId）        对端回 ANSWER
   │ CANDIDATE ⇄ ICE 候选交换（STUN 打洞）
   ▼
WebRTC DataChannel 打开 → OnOpen → bindConn(newRTCSession)
   │ 注册 conns[peerID] + connState（fetches/pendingUpload/fwd 槽位）
   │ OnMessage 泵：文本帧按 type 分派（verb→inbound 角色 / 其余→outbound 响应路由）
   │ 二进制块按 expect 状态路由（fetch 队列 / upload worker / 转发隧道）
   ▼
双工并发：serveFile 应答对端 req 的同时可 OpenStream 拉对端文件
```

## 4. 验证信息的产生

### 4.1 信令层（身份，`peer.go`）

| 凭证 | 产生方式 | 作用 |
|---|---|---|
| **key** | 共享配置（`PEERDRIVE_PEERJS_KEY`，默认 peerjs） | WS URL 参数；服务端校验不匹配即拒绝（防陌生人注册） |
| **token** | 客户端启动 `randomToken()` = 16 字节随机 hex（`peer.go:302`） | 同 id 重连时 token 匹配才允许接管旧连接；不匹配回 `ID-TAKEN`（防 ID 劫持） |
| **id** | 自定（`PEERDRIVE_PEERJS_ID`）或 `GET /peerjs/id` 服务端分配 | 节点在信令网络的标识 |

### 4.2 业务层（端口转发 HMAC 质询，`forward.go`）

```
服务端                                   客户端（OpenForward）
  │ ←── fwd-open {port, reqId} ────────────
  │ rand.Read → 16B nonce（一次性+5min 过期+上限64）
  │ ── fwd-challenge {nonce} ──►
  │                                     hmac = HMAC-SHA256(key, nonce) → hex
  │ ◄── fwd-auth {hmac} ────────────────
  │ 遍历规则表 key 原文重算 HMAC，hmac.Equal 常量时间比较
  │ 通过 → 校验端口 ∈ key 授权白名单 → dial 127.0.0.1:port（SSRF 防护）
  │ ── fwd-ok ──►  隧道建立，fwd-data 双向透传
```

- key 即凭证：`PEERDRIVE_FORWARD_RULES="key1:8080,key2:8443"` 或运行时 `POST /p2p/forward/create` 动态追加（不持久化）
- key 明文永不落线（客户端本地持有，落线只传 HMAC；DataChannel 本身 DTLS 加密双保险）
- 失败只回 `fwd-err`，不泄露规则细节；防重放：nonce 取出即标 used

### 4.3 传输层（WebRTC 自带）

- **DTLS 加密**：pion 自动生成自签名证书，信令交换 SDP 后协商
- **ICE**：STUN/TURN 打洞（`parseICEServers`，`PEERDRIVE_WEBRTC_STUN/TURN` 配置）

### 4.4 本地 WS 会话（无 token）

- 仅 HTTP **Origin 白名单**校验（`peerjs_routes.go:99`，同 CORS 配置 `IsOriginAllowed`）；
  无 Origin（curl）放行，白名单外 Origin 拒绝升级

## 5. 安全边界小结

| 层 | 防护 |
|---|---|
| 信令注册 | key 校验 + token 防 ID 劫持 |
| 文件服务 | hash 严格 64hex（H1）；file_index 路径必须落在允许根内，越权回退 CAS（H2）；远端声明上限 8GB（H6） |
| HTTP 拉取端点 | 认证 + 单次 64MB 上限（H4） |
| 上传 | size ≤8GB、文件名净化（防路径穿越）、offset chunk 对齐（REFACTOR §4） |
| 转发 | HMAC 质询 + 端口白名单 + 仅 loopback + nonce 一次性 |
| WS 会话 | Origin 白名单 + 读限制 192KB + ping/pong 90s 保活（M5） |
| 信令服务器 | key 校验、ID-TAKEN、队列上限 100/dst、40KB 读限制、body 1KB（见 doc/PEERSIGNAL.md） |
