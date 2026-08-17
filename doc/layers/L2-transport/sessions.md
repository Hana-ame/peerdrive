# 会话抽象（back/internal/transport/ws_session.go + rtc_session.go）

> 层归属：AOP ② 帧协议层（见 doc/LAYERS.md §1）
> 帧协议定义见 doc/REFACTOR.md §4，本模块是「协议承载通道」的两种实现。

**一句话职责**：把两种物理通道（本地 WebSocket / WebRTC DataChannel）统一适配成
`Session` 接口——同一套帧协议（文本帧=JSON 控制头、二进制帧=数据块）、同一套
reqId 状态机，上层（conn.go 分派泵）零分支复用。

## 职责

### 解决什么问题

浏览器与节点之间存在两条互通路径（REFACTOR.md §3.5）：

```
浏览器 ──WS(/ws/peer)──────→ 本地 node：管理/元数据/小文件（毫秒级，无打洞）
浏览器 ──WebRTC(公共云信令)─→ 任意 node（含远端）：大文件、跨节点（打洞直连）
```

两条路径物理层完全不同（gorilla/websocket 的 `*websocket.Conn` vs
`*peerjs.Connection`），但帧协议必须完全一致——否则前端要维护两套编解码、
后端每个 verb 处理函数要写两份。`Session` 接口把差异收敛在适配层：

- **WSSession**（ws_session.go）：gorilla WebSocket 适配 + 保活（读超时/ping-pong）
- **rtcSession**（rtc_session.go）：`*peerjs.Connection` 适配（对端节点经公共云信令）

conn.go 的 `bindConn` 只认 `Session`：`fetchReader` 的收帧、serveFile 的发帧、
OnClose 清理全部与具体传输无关。

### 在 AOP ② 中的位置

```
① peerjs：*peerjs.Connection（DataChannel + SendFrame 原子帧 + 流控）
        ↑ rtc_session.go 适配
② transport
   ├── ws_session.go / rtc_session.go  ← 本文（Session 抽象，方向中立）
   ├── conn.go  bindConn（只消费 Session）
   ├── inbound.go / outbound.go（只消费 Session）
   └── peerjs_service.go 装配：connectLoop → newRTCSession；BindLocal → NewWSSession
```

- 上游：① peerjs 模块（`peerjs.Frame`、`peerjs.DataChannel` 类型被接口借用）；
  gorilla/websocket（本地 WS 的直接依赖）。
- 下游：conn.go 分派泵、inbound/outbound 角色、admin.go（serveAdmin 经
  `c.ID()=="local"` 区分本地会话）。

## 关键机制

### 1. Session 接口（ws_session.go:22-29）

```go
type Session interface {
    ID() string                          // 会话标识："local" 或远端 peer id
    SendJSON(v any) error                // 文本帧（JSON 控制头）
    SendFrame(header any, body []byte) error // 原子发送「头 + 二进制体」（REFACTOR.md §4 约束 2）
    OnMessage(f func(peerjs.Frame))      // 注册帧回调（IsText 区分文本/二进制）
    OnClose(f func())
    Close()
}
```

设计点：

- `peerjs.Frame{IsText, Data}` 作为统一帧类型——WSSession 把 WS 的
  `TextMessage/BinaryMessage` 映射为 `IsText`（ws_session.go:146），两实现帧语义
  完全一致。
- `SendFrame` 的原子性是协议正确性前提：data/admin-bin 头与数据块之间不允许
  插入其他帧（REFACTOR.md §4 约束 2、ws-client.md 的 binaryExpect 单槽依赖它）。

### 2. WSSession（ws_session.go:34-149）

```
结构：id + conn + sendMu + onMessage/onClose + closeOnce
线程：readLoop（读帧 → 分发）+ heartbeatLoop（30s ping）
```

- **NewWSSession**（ws_session.go:49-62）：
  - `SetReadLimit(3 * 64 * 1024)`：数据块 ≤64KB + JSON 控制头余量（两倍留余）
  - `SetReadDeadline(nowPlus(90))` + PongHandler 刷新：90s 无活动读超时
  - 启动 readLoop + heartbeatLoop
- **readLoop**（ws_session.go:135-148）：`ReadMessage` 循环 → 按消息类型构造
  `peerjs.Frame` → 调注册的 OnMessage；出错即 `defer s.Close()`（触发 OnClose 清理）。
- **heartbeatLoop**（ws_session.go:67-78）：30s 一次 `WriteControl(PingMessage)`，
  10s 写超时；ping 失败（连接已关）直接退出，无泄漏。浏览器对 ping 自动回 pong
  （协议层行为），pong 刷新读 deadline。
- **发送**（SendJSON ws_session.go:84-89 / SendFrame ws_session.go:92-104）：
  `sendMu` 串行化（gorilla 不允许并发写），15s 写 deadline；SendFrame 先 WriteJSON
  头再 BinaryMessage 体，两者在同一锁内保证原子连续。
- **Close**（ws_session.go:121-132）：`closeOnce` 保证幂等；先关底层连接、再取
  onClose 回调在锁外执行（防持锁回调死锁）。

### 3. rtcSession（rtc_session.go:11-35）

`*peerjs.Connection` 的薄适配：`ID()` 返回缓存的 `PeerID`；其余方法直接透传
（SendFrame 的原子性与流控由 peerjs 模块实现）。额外暴露
`DataChannel() peerjs.DataChannel`（rtc_session.go:35）——serveFile 写缓冲流控
用（WSSession 无此能力：TCP 自带背压，不用水位控制；REFACTOR.md §3.5 的接口
断言语义即「只有 rtcSession 有 DataChannel 方法」）。

**为什么包一层而不是让 Connection 直接实现 Session**（rtc_session.go:7-10 注释）：
`Connection.ID` 字段是信令路由键（connectionId），与会话标识（远端 peer id）
语义不同且字段名冲突；adapter 在 service 层收敛差异，peerjs 模块保持传输原语
职责（LAYERS.md §1：① 不依赖 internal/*）。

### 4. 生命周期协作

```
建立：peerjs_service.go
  connectLoop 拨号成功 / onIncomingConnection 接受 → newRTCSession → bindConn
  router /ws/peer 升级 → NewWSSession("local", conn) → BindLocal → bindConn
运行：bindConn 注册 OnMessage/OnClose（conn.go:165, 294）
关闭：WSSession 读超时/ping 失败 → readLoop 退出 → Close → OnClose → bindConn 清理
      peerjs 连接断开 → Connection.OnClose → rtcSession.OnClose 包装 → bindConn 清理
```

## 与其它模块的关系

- **conn.go**：`bindConn` 是 Session 的唯一消费者（注册 OnMessage/OnClose）；
  会话标识（`ID()`）同时是 `conns` map 的 key 与 admin 权限判断依据
  （`c.ID()!="local"` 拒绝 admin，admin.go:113）。
- **peerjs_service.go**：`connectLoop`（peerjs_service.go:318-326）与
  `onIncomingConnection`（peerjs_service.go:352-357）创建 rtcSession；
  `BindLocal`（peerjs_service.go:243-246）绑定 WSSession——**两条路径都只是创建
  全双工 Session 再 bindConn**，连接无方向之分（peerjs_service.go:13-14 注释）。
- **inbound/outbound**：serveFile/routeResponse 只经 SendJSON/SendFrame/OnMessage
  与 Session 交互，不感知传输类型。
- **router（/ws/peer）**：peerjs_routes.go 负责 WS 升级 + Origin 白名单校验后
  `NewWSSession`；本地会话 id 固定 "local"。

## 坑与设计决策

| # | 坑 | 设计/修复 | 来源 |
|---|---|---|---|
| 1 | gorilla/websocket 不允许并发写——心跳/ICE 候选/ANSWER 多 goroutine 并发写会 panic（3 节点互通测试触发） | WSSession `sendMu` 串行化所有写（含 ping 的 WriteControl） | ws_session.go:38, 70-73, 85-103；REFACTOR.md §5 |
| 2 | 无读限制：恶意/故障浏览器发超大帧无限占内存 | `SetReadLimit(3*64KB)`（M5） | ws_session.go:46-47, 51 |
| 3 | 无保活：浏览器标签页死掉 → readLoop + 会话常驻，连接 map 永不清理，pending fetch 挂 5 分钟 | 90s 读 deadline + 30s ping/pong 刷新（M5） | ws_session.go:47-57, 64-78 |
| 4 | pion 的 Connection.ID 是信令路由键（connectionId），与「远端 peer id」语义冲突 | rtcSession 包一层，ID() 缓存 PeerID | rtc_session.go:7-14, 17 |
| 5 | WS 无写缓冲水位概念（TCP 自带背压） | DataChannel() 只在 rtcSession 暴露——serveFile 的流控断言只对 DataChannel 生效，WSSession 零流控代码 | rtc_session.go:34-35；REFACTOR.md §3.5 |
| 6 | 关闭回调若持锁执行可能死锁 | Close 取回调后锁外执行；closeOnce 幂等（重复 Close 不重复触发） | ws_session.go:121-132 |
| 7 | OnMessage/OnClose 注册时机竞态（bindConn 在构造后注册） | 注册用 sendMu 保护、readLoop 取回调时也持锁拷贝 | ws_session.go:107-118, 142-145 |

## 测试

transport 包内无独立的 ws_session_test.go / rtc_session_test.go——会话适配的正确性
由两类测试覆盖：

**单元测试**：`peerjs_service_test.go` 的 `fakeSession`（peerjs_service_test.go:24-77）
实现同一 `Session` 接口（内存记录发送帧、手动注入帧），驱动 conn.go 泵与
inbound/outbound 全链路。发现背景（文件头注释）：H1/H6/M6 修复需要「无网络注入
恶意帧」的手段，fakeSession 是协议级测试的基石。

**集成测试**（`back/test/integration/`，需外网+代理，`-p 1` 串行）：

| 测试 | 覆盖 | 发现背景 |
|---|---|---|
| ws_test.go TestLocalWSSessionFetch | 真实 WS 升级 → NewWSSession("local") → BindLocal → 拉取 | 架构决策（§3.5）：本地走 WS 无打洞 |
| ws_test.go TestLocalWSSession_FetchFromPeerReuse | "local" 注册后 FetchFromPeer 零分支复用 | BindLocal 设计验证 |
| ws_verbs_test.go TestFrameVerbs_* | WS 会话上 create/upload/list/info/download/sync 全链路 | 功能验收 |
| interop_test.go Test* | rtcSession（真实 DataChannel）双/三/四节点互通 | 功能验收 + 流控死锁回归 |
| live_test.go TestLive* | 线上信令 + WS/WebRTC 全链路 | 线上验证 |

> 注意：gorilla 的读超时/保活行为（坑 #2/#3）无专门单测，属「协议层行为」依赖
> （浏览器自动回 pong）；若引入断线单测需 mock websocket.Conn，当前以集成
> ws_test.go 的存活判定覆盖。

## 文件清单

| 文件 | 说明 |
|---|---|
| `ws_session.go`（149 行） | Session 接口定义 + WSSession（gorilla 适配：读循环/保活/写锁） |
| `rtc_session.go`（35 行） | rtcSession（*peerjs.Connection 适配 + DataChannel 流控入口） |
| 相关消费方：`conn.go`（bindConn）、`peerjs_service.go`（装配/绑定）、`inbound.go`/`outbound.go`（角色实现）、`back/internal/router/peerjs_routes.go`（/ws/peer 升级） | 见对应模块文档 |
| 测试：`peerjs_service_test.go`（fakeSession）、`back/test/integration/ws_test.go` + `ws_verbs_test.go` | 会话行为验证（见上表） |