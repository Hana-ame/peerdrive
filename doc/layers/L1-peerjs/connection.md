# Connection 模块（back/peerjs/connection.go）

> 一句话职责：一条 WebRTC DataConnection 的封装——SDP/ICE 经由信令，数据面为 DataChannel，提供「文本帧（JSON 头）+ 二进制帧（数据块）」的原子发送原语与写缓冲流控。

## 职责

- 封装 `pion/webrtc` 的 PeerConnection + DataChannel，不绑定具体传输类型（依赖 `DataChannel` 接口，见 transport.md）
- 提供三类发送原语：`Send`（纯二进制块）、`SendText`（JSON 头）、`SendFrame`（头+体原子帧）
- 处理信令消息中与「本连接」相关的部分（ANSWER/CANDIDATE 设置远端 SDP、加 ICE 候选）
- 生命周期：offerer 主动建链（`makeOffer`）或 answerer 被动应答（`handleOffer`），ICE 失败自动清理
- **不包含任何业务帧协议知识**（verb 由上层 transport 包定义）——模块定位是传输原语

## 关键机制

### 1. 帧模型：文本帧 vs 二进制帧

`Send` 走 SCTP PPID 53（二进制），`SendText` 走 PPID 51（文本）。接收端（pion `OnMessage` → `Frame{IsText}`）据此区分「控制头 vs 数据块」——**协议依赖此区分**：

- 控制头必须用 `SendText`/`SendJSON`。若误用 `Send([]byte)` 发 JSON，对端会把头判为二进制数据块而丢弃/错配（connection.go:86-87 注释）。

### 2. SendFrame 原子性 + 流控（connection.go:114-148）

```go
c.sendMu.Lock()          // ① 串行化：头+体必须连续落线
dc := c.dc               // ② 持 sendMu 期间读一次 dc 快照（M9，避免每次取锁）
dc.SendText(headerJSON)  // ③ 头
for dc.BufferedAmount() > 512KB {  // ④ 内置背压
    select { <-lowWater / <-done / 30s 超时 }
}
dc.Send(body)            // ⑤ 体
```

- **为什么原子**：上层状态机按「data 头 → 紧随二进制块」路由数据；多 goroutine 并发发送时若头体交织，数据块会挂到错误请求上。
- **流控**：`defaultBufferLowThreshold = 512KB`。发送缓冲超阈值时等待低水位事件再发下一块，防止慢消费者撑爆 pion 缓冲。
- **坑（注释原文）**：pion 的 `OnBufferedAmountLow` 是**替换式回调**——若每个并发发送方各自注册，只有最后一个注册者能收到事件，其余死等（曾导致并发 serveFile 卡死）。因此回调在 `attach` 时全局注册一次，等待统一走 `lowWater` 通道（容量 1 防堆积）。
- 30s 流控超时（低危 1 修复）：之前只等 done/lowWater，慢消费者时依赖 ICE disconnected（~30s）兜底——现在显式封顶，超时返回错误由上层断开/重试。

### 3. M9：dc 字段的数据竞争防护

`dc`（DataChannel）在 `attach` 时**无锁写入**（pion 的 `OnDataChannel` 回调跑在 PC goroutine），而 `Open/Send/SendFrame/Close` 从任意 goroutine 并发读——-race 必现、极端下读到 nil 半初始化。`dcMu sync.RWMutex` 保护读写；attach 只调用一次，锁开销可忽略。

### 4. 生命周期

- **建链**（offerer，`newConnection` → `makeOffer`）：
  1. `NewPeerConnection` + 注册 ICE 状态/候选/DataChannel 回调
  2. `CreateDataChannel(label, {Ordered:true})` → `attach`
  3. `CreateOffer` → `SetLocalDescription` → 信令 `OFFER`（payload 含 connectionId/label/reliable/serialization=raw）
- **应答**（answerer，`handleOffer`）：`SetRemoteDescription` → `CreateAnswer` → `SetLocalDescription` → 信令 `ANSWER`
- **清理**：`pc.OnICEConnectionStateChange` 对 Closed/Failed/Disconnected 三态调用 `conn.Close()`（peerjs-client negotiator 同款行为，防泄漏）
- **坑（connection.go:201-203）**：answerer 必须沿用 offerer 的 connectionId。曾因 answerer 新生成 ID 导致 ANSWER 在信令路由（按 connectionId）时找不到对端 conn，ICE 永远停在 checking。
- **坑（connection.go:216-221）**：重复 OFFER 时旧连接必须走完整 `Close`（closeOnce 幂等）——只关 pc 会泄漏：旧连接残留在 conns map、done 永不关闭、onClose 不触发。且必须在 `p.mu` 解锁后调用（Close→forgetConnection 需要同一把锁，Go mutex 非重入）。

### 5. 信令消息处理（handleMessage）

只处理 ANSWER（SetRemoteDescription）与 CANDIDATE（AddICECandidate）。解析错误**静默忽略**：对端可能发来乱序/过期候选，失败仅意味本轮协商失败，由 ICE 状态回调负责最终清理。

### 6. attach 回调布局

- `OnOpen` → 上层 `onOpen`（连接就绪通知）
- `OnMessage` → 上层 `onMessage(Frame)`（数据面）
- `OnClose` → `c.Close()`：**远端主动关 dc 时本端立即清理**（Close 幂等）。否则本端连接悬挂，依赖 ICE disconnected 兜底（秒级~分钟级，太慢）
- `OnBufferedAmountLow` → `lowWater` 广播（全局一次）

### 7. ICE 候选转发（connection.go:234-244）

pion 不会自动发送候选——必须手动 `OnICECandidate` + 信令 CANDIDATE 消息（`CandidatePayload{Type: ConnData, ConnectionID}`），否则双方停在 checking 永远连不上。

## 与其它模块的关系

```
Peer（信令路由/注册表）
  └─ Connection（本模块）
       ├─ 信令消息：ANSWER/CANDIDATE（入）/ OFFER/CANDIDATE（出，经 Peer.Send）
       ├─ DataChannel 接口（transport.go）：pionChannel 适配
       └─ 上层（internal/transport）：OnOpen/OnMessage/OnClose 回调 + SendFrame 原语
```

- `Peer.Connect` → `newConnection(offered=true)`；`Peer.handleOffer` → `newConnection(offered=false)`
- 上层 transport 包通过 `DataChannel()` 获取底层通道做高级流控（水位流控只对 WebRTC 生效，见 sessions.md）

## 坑与设计决策

| # | 坑 | 修复 | 来源 |
|---|---|---|---|
| M9 | attach 无锁写 dc vs 并发读，-race 必现 | dcMu 读写锁 | connection.go:30-34 |
| — | OnBufferedAmountLow 替换式回调，并发注册互相覆盖 → 死等 | attach 时注册一次 + lowWater 通道 | connection.go:14-17, 276-283 |
| — | answerer 新生成 connectionId → ICE 停在 checking | 沿用 offerer 的 connID | connection.go:201-203 |
| — | 重复 OFFER 只关 pc → conns map 泄漏 + done 永不关 | 走完整 Close（closeOnce 幂等），且锁外调用 | connection.go:216-221 |
| — | pion 不自动发 ICE 候选 | 手动 OnICECandidate + 信令转发 | connection.go:232-233 |
| — | 慢消费者无限等流控 | 30s 超时封顶（低危 1） | connection.go:134-144 |
| — | 远端关 dc 本端悬挂 | dc.OnClose → Close（幂等） | connection.go:295-297 |
| — | 用二进制帧发 JSON 头 → 对端误判为数据块 | SendText 专用于控制头 | connection.go:86-87 |
| 低危 7 | ID-TAKEN 静默忽略 → 同 ID 双节点失联无痕迹 | 记日志（peer.go 侧） | peer.go:183-194 |

## 测试

- `flowcontrol_test.go`（77 行）：SendFrame 流控行为验证（低水位等待/超时/关闭退出路径）
- `peer_test.go`（450 行，含 `testutil_test.go` 174 行的内存信令桩）：连接建立/消息路由/生命周期——内存 signaller 使单测不依赖公网

## 文件清单

| 文件 | 说明 |
|---|---|
| `connection.go` | 本模块（335 行） |
| `flowcontrol_test.go` | 流控单测 |
| `peer_test.go` + `testutil_test.go` | 连接级测试与测试工具（内存信令桩） |