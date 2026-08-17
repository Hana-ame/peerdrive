# Peer 模块（back/peerjs/peer.go + signaller.go）

> 一句话职责：PeerJS 兼容的信令客户端——注册到信令服务器、收发 OFFER/ANSWER/CANDIDATE 等消息、维护连接注册表，支持主动发起（Connect）与被动接收（OnConnection）WebRTC 数据连接。

## 职责

- 向信令服务器注册节点（指定 ID 或服务端分配随机 ID），维持 WS 连接与心跳
- 路由信令消息：OFFER → 建 answerer 连接；ANSWER/CANDIDATE → 按 connectionId 投递到既有连接；LEAVE/EXPIRE → 清理连接
- 维护连接注册表（`conns map[string]*Connection`），负责连接生命周期清理
- 三个扩展点：`Signaller` 接口（换信令）、`DataChannel` 接口（换传输）、开放 string 的 `MessageType`（加消息）
- **不包含业务逻辑**：数据面由 Connection + 上层回调处理

## 关键机制

### 1. 信令连接与心跳（peerJSSignaller）

- WS URL：`wss://host:port/{path}peerjs?key=&id=&token=&version=1.5.4`（仿 peerjs-client 的 version 参数）
- 未指定 ID 时先 HTTP `GET /id?ts=...&version=...` 取服务端分配 ID（`retrieveID`，校验 `validID`：首尾字母数字，中间允许 `- _ 空格`，长度 1-256）
- `heartbeatLoop` 每 `PingInterval`（默认 5s）发 HEARTBEAT 保活——公共云信令空闲超时依赖此心跳
- **M7**：`conn.SetReadLimit(1 << 20)`——信令消息（SDP/ICE 文案）很小，1MB 上限覆盖合法负载；云端信令若被攻破回超大帧，不设限直接 OOM

### 2. 消息路由（route, peer.go:150-197）

| 消息 | 处理 |
|---|---|
| OFFER | `handleOffer`：建 answerer Connection 并回 ANSWER |
| ANSWER/CANDIDATE | 按 `connectionId`（payloadConnectionID）投递到 `conns` 中既有连接 |
| LEAVE | 关闭所有 `PeerID == m.Src` 的连接（收集后解锁再 Close，防死锁） |
| EXPIRE | OFFER 在信令服务器入队后过期（对端未及时上线）→ 关闭连接让上层 connectLoop 重连 |
| HEARTBEAT | 忽略（客户端主动 ping 已保活） |
| ID-TAKEN/ERROR | 记日志（低危 7：之前静默忽略，同 ID 两节点双双失联无痕迹） |

### 3. H7：信令断线通知与重连

**坑**：之前 readLoop 出错静默退出、`connected=false`，但没有任何信号通知上层——startLoop 只 select ctx/closed 两个永不触发的信号，**公网 WS 掉一次后节点永久失聪直到重启**。

**修复**：`Done() <-chan struct{}`——readLoop 因网络错误/EOF 退出时关闭 `done` 通道（doneOnce 保证只关一次）。上层（peerjs_service 的 H7 重连循环）依赖它触发整轮重连。

**细节**：主动 `Close()` 会先置 `s.conn=nil` 再关 conn，readLoop defer 中 `s.conn == conn` 不命中 → done 不关闭，由 ctx/closed 分支收尾（避免 Close 后误触发重连循环）。

### 4. 并发模型

- `p.mu`：保护 conns map 与 onConn/iceServers
- **writeMu（peer.go:349-351）**：串行化 `conn.WriteJSON`——gorilla/websocket 不允许并发写，多 goroutine（心跳/ICE 候选/ANSWER）并发发送会 panic（**3 节点互通集成测试真实触发过**）
- `Send` 带 15s 写 deadline
- **死锁防线（多处）**：`Close → forgetConnection` 需要 `p.mu`——所有「先收集、后清理」路径（handleOffer 的旧连接、handleLeave）都必须在 `p.mu` 解锁后再调 `Close`（Go mutex 非重入）

### 5. validID 与 ID 生命周期

- `validID` 校验服务端返回 ID 合法性（retrieveID 后必须校验，防止信令服务器异常返回垃圾 ID）
- ID 被占用（ID-TAKEN）是配置错误，重连无解——只记日志

## 与其它模块的关系

```
上层（internal/service/peerjs_service.go）
  ├─ NewPeer / NewPeerWithSignaller → Peer
  ├─ OnConnection(handler)      ← 被动连接回调
  ├─ Connect(ctx, dst, label)   → *Connection（主动）
  ├─ Dial(ctx) / Done() / Close()
  └─ Send(m)                    ← 自定义消息类型扩展点
```

- `Connection` 经 `p.conns` 注册表管理，路由消息按 connectionId 投递（见 connection.md）
- `Signaller` 接口（signaller.go）：`Dial/ID/Send/OnMessage/Done/Close`。`NewPeerWithSignaller` 注入自定义实现；`OnMessage` 由框架内部注入 `p.route`，实现者只需在收到消息时调用注入的回调
- `Options`（message.go:83-92）：Host/Port/Secure/Path/Key/ID/Token/PingInterval/ICEServers——`SetICEServers` 仅对自定义信令路径生效（NewPeer 走 Options.ICEServers）；**自定义信令必须调 SetICEServers，否则 WebRTC 只有局域网 host 候选**

## 坑与设计决策

| # | 坑 | 修复 |
|---|---|---|
| H7 | readLoop 出错静默退出 → 节点永久失聪 | Done() 通道 + 上层重连循环 |
| — | gorilla WS 并发写 panic | writeMu 串行化 |
| — | 重复 OFFER 旧连接泄漏 | 锁外完整 Close（见 connection.md） |
| — | Close 与 readLoop 退出竞态误触发重连 | conn 身份比对（`s.conn == conn`）+ doneOnce |
| 低危 7 | ID-TAKEN 静默 → 同 ID 失联无痕迹 | 日志 |
| M7 | 信令大帧 OOM | 1MB ReadLimit |
| — | 自定义信令无 ICE 服务器 | SetICEServers 强制说明 |

## 测试

- `peer_test.go`（450 行）：内存信令桩（testutil_test.go）下测试注册/路由/建链/清理，不依赖公网
- 集成测试（`back/test/integration/`，`-tags integration`）：真实公共信令 0.peerjs.com + 公共 broker，`-p 1` 串行（多组并行会互相干扰）；H7 重连、writeMu 并发均由 3 节点互通集成测试暴露

## 文件清单

| 文件 | 说明 |
|---|---|
| `peer.go` | 本模块（560 行：Peer + peerJSSignaller 实现） |
| `signaller.go` | Signaller 接口 + MessageHandler/SignallerFactory（39 行） |
| `message.go` | Message/消息类型/各 payload/Options（93 行，见 transport.md） |
| `peer_test.go` / `testutil_test.go` | 测试与内存信令桩 |