# 互联框架：WS + PeerJS 双传输 + Session 抽象 + 信令装配

> 2026-08-16 · 对应代码 `back/internal/transport/`（peerjs_service.go / conn.go /
> inbound.go / outbound.go / ws_session.go / rtc_session.go / forward.go）
> 本文档描述「传输怎么抽象、连接怎么建立、装配怎么接线」。

---

## 1. 框架总览

```
                    ┌────────────────────────────────────────────┐
                    │              PeerJSService                 │
                    │   （装配层：信令生命周期 + 连接建立）        │
                    │                                            │
  ┌─ startLoop ─────┤  信令注册/断线重连（Signaller 抽象）        │
  │  connectLoop ───┤  主动拨号远端节点（含重连）                 │
  │ onIncoming ─────┤  被动接受浏览器/节点连接                   │
  │  BindLocal ─────┤  本地 WS 会话（仅管理，浏览器直连本节点）   │
  └─────────────────┴──────────────┬─────────────────────────────┘
                                   │ bindConn（统一分派）
                    ┌──────────────▼──────────────┐
                    │        connState            │  ← conn.go 共享核心
                    │  fetchState / uploadState   │     （reqId 状态机、
                    │  fwdStream / binCh/fwdCh    │       二进制块路由、
                    │                            │       流控、worker 投递）
                    └──────┬───────────────┬──────┘
              ┌────────────▼────┐   ┌──────▼────────────┐
              │  inbound.go     │   │  outbound.go      │
              │  入站角色       │   │  出站角色         │
              │  serveFile/     │   │  FetchFromPeer/   │
              │  serve*索引verb │   │  requestFile/     │
              │  uploadWorker   │   │  routeResponse    │
              └────────────┬────┘   └──────┬────────────┘
                           │               │
┌────────────▼───────────────▼──────┐
               │          Session 接口              │
               │  （传输抽象：两种实现语义一致）      │
               └───────┬─────────────────┬──────────┘
         ┌─────────────▼─────┐   ┌───────▼─────────────┐
         │  WSSession        │   │  rtcSession         │
         │  本地 WebSocket   │   │  WebRTC DataChannel │
         │  (仅管理，无传输)  │   │  (文件数据传输)     │
         └───────────────────┘   └─────────────────────┘
```

> **分工原则：WS 仅管理，WebRTC 传数据。** WS 本地会话只承载管理类 verb
> （文件索引 create/upload/list/info/delete/sync 的元数据面 + 控制），
> 不承载文件内容传输（req/meta/data/done/err 大文件拉取走 WebRTC）。

## 2. Session 抽象（方向中立传输，`ws_session.go` / `rtc_session.go`）

```go
type Session interface {
    ID() string
    SendJSON(v any) error              // 文本帧（JSON 控制头）
    SendFrame(header any, body []byte) error // 「JSON 头 + 二进制体」原子帧
    OnMessage(f func(peerjs.Frame))    // 帧回调（IsText 区分文本/二进制）
    OnClose(f func())
    Close()
}
```

**为什么抽象**：两种传输承载同一套帧协议（req/meta/data/done/err + 文件索引 verb +
forward verb），同一 reqId 状态机 / serveFile / FetchFromPeer 零分支复用——
浏览器端只需一套协议编解码。但**分工不同**：WS 仅管理（元数据/索引 verb），
WebRTC 传数据（大文件拉取/上传）。

| 实现 | 文件 | 用途 | 差异点 |
|---|---|---|---|
| `WSSession` | ws_session.go | 浏览器直连本节点（`/ws/peer`），**仅管理**：文件索引 verb 元数据面 + 控制 | 无信令/打洞，毫秒级；TCP 自带背压故无写缓冲流控；SetReadLimit(3×64KB) + 90s ping/pong 保活（M5） |
| `rtcSession` | rtc_session.go | 远端节点/浏览器经信令直连，**文件数据传输**（拉取/上传/转发） | 包一层 `*peerjs.Connection`：Connection.ID 是信令路由键（connectionId），与会话标识（peer id）语义不同；提供 DataChannel() 供 serveFile 水位流控 |

## 3. 信令装配（`peerjs_service.go`）

装配层只做四件事：**信令生命周期、连接建立、本地会话绑定、发现装配**。
连接建立的两条路径（accept/dial）都只是创建一条全双工 Session，随后经
`bindConn` 挂上同一套 inbound+outbound 角色——WebRTC 连接对称，无方向之分。

### 3.1 startLoop — 信令生命周期（断线自动重连）

```
Start() → startLoop 循环：
  1. 用 cfg（Host/Port/Secure/Key）构造 peerjs.NewPeer(id, opts)，注册信令
  2. p.Dial(ctx) 失败 → 指数退避重试（2s→60s）
  3. 成功 → 拨号配置对端（PeerJSPeers）→ 启动发现（DiscoverURL 优先于 MQTT）
  4. 阻塞直到：ctx 取消 / closed / 信令断线（Signaller.Done()，H7 修复）
     → 整轮重连（复用退避）
```

调用条件：`PEERDRIVE_PEERJS_ENABLE=true`（默认），main 装配时 `Start()`。

### 3.2 connectLoop — 主动拨号（含自动重连）

```
connectLoop(peerID)：
  1. connecting 去重（低危 3：配置 PEERS 与发现回调可能同时触发）
  2. peer.Connect(ctx, peerID, "peerdrive")
  3. OnOpen → bindConn(newRTCSession(c))，随后阻塞等 conn.Done()
  4. 失败/超时/断开 → 指数退避重试，直到服务关闭
```

调用条件：对端可能未上线（OFFER 入队 EXPIRE）或 ICE 失败——必须循环重试
直到真正 OnOpen；`connecting` 去重防双连接。

### 3.3 onIncomingConnection — 被动接受

```
对端发起连接 → OnOpen 后 bindConn（必须等 open：answerer 回调在 handleOffer
时立即触发，过早注册会让 FetchFromPeer 拿到未就绪连接）
```

### 3.4 BindLocal — 本地 WS 会话（仅管理）

```
BindLocal(sess Session) → bindConn(sess)
注册 key="local"：浏览器经 /ws/peer 直连本节点，承载管理 verb
（文件索引 create/upload/list/info/delete/sync 元数据面 + 控制），
不承载文件内容传输——文件数据一律走 WebRTC（rtcSession）
```

调用条件：`/ws/peer` 升级成功后由路由调 BindLocal（浏览器直连）。
安全边界：Upgrade 前 CheckOrigin 仅放行配置的 Origin（同 HTTP CORS 白名单，
peerjs_routes.go:98-110）——本地会话是「浏览器可管理本机文件」的通道，
Origin 白名单是唯一防线。

### 3.5 发现装配（多路并取）

```
DiscoverURL 非空 → HTTPDiscovery（自托管发现 API，10s 轮询，优先于 MQTT）
否则 MQTTEnable → MQTTDiscovery（分片房间 peerdrive/v1/{collHash}/nodes）
两者回调 → onDiscoveredPeer → connectLoop（已连接则跳过）
```

## 4. bindConn — 连接统一分派（`conn.go`）

bindConn 是**共享连接核心（禁止复制）**：一条连接全双工复用，同一条 Session
同时承载 inbound+outbound 两角色，靠「帧类型 + reqId」区分：

| 帧 | 归属 | 处理 |
|---|---|---|
| 文本帧 type = verb（req/create/upload/list/info/delete/sync + fwd-open/auth/data/close） | 入站角色 | `go s.serveXxx(c, ...)`（fork 出 goroutine 应答） |
| 文本帧 type = fwd-challenge/ok/err | 出站角色（forward 客户端侧） | `routeForwardResponse`（forward.go:351，独立于文件响应路由） |
| 文本帧其它 type | 出站角色 | `routeResponse` 按 reqId 路由（meta/data/done/err 文件拉取响应） |
| 二进制帧 | 数据块 | 泵内路由决策：fwd 隧道（pending 标记）→ expect（下载）→ upload（分片）；落盘 IO 交连接级 worker（H5，防头-of-line 阻塞） |

**三条协议约束（勿破坏）**：
1. JSON 控制头必须是**文本帧**，数据块是**二进制帧**
2. data 头与数据块必须**原子连续**（SendFrame 的 sendMu）
3. Go 端始终携带 reqId（浏览器可不传，向后兼容）

## 5. 装配调用顺序（main → router → transport）

```
cmd/server/main.go
  ├─ cfg := config.Load()
  ├─ repository.InitDB / SetAnonStorageDir
  ├─ peerjsSvc = transport.NewPeerJSService(cfg, storageDir)  // 装配点
  │    ├─ SetForwardRules（PEERDRIVE_FORWARD_RULES）
  │    └─ source 注册：local(fileIndex) → peer → url
  ├─ router.SetPeerJSService / SetPeerJSConfig / SetSourceManager
  └─ router.SetupRouter(cfg)
       └─ /ws/peer 升级 → peerjsService.BindLocal(sess)
       └─ /peerjs/* 路由（节点发现 + fetch 验证）
```

## 6. 扩展点（后续扩展不动核心）

- 换信令：`peerjs.NewPeerWithSignaller()` 注入自定义 Signaller
- 换传输：实现 `Session` 接口（现有 WSSession/rtcSession 两例）
- 加 verb：`MessageType`/`Frame` 开放类型 + bindConn 分派加 case
- 加发现：HTTPDiscovery/MQTTDiscovery 之外实现同回调接口

## 7. 验证

```bash
cd back/peerjs && go test ./... -count=1 -race     # 传输原语
cd back && go test -tags nosqlite ./internal/transport/ -race  # 装配+角色
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v
```