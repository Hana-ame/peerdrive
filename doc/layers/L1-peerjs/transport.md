# 传输抽象与消息类型（back/peerjs/transport.go + message.go）

> 一句话职责：数据面传输抽象（`DataChannel` 接口 + `Frame` 帧类型 + pion 适配）与信令消息协议（`Message` + 各 payload + `Options` 配置）。

## 职责

- **transport.go**：定义 `Frame`（文本/二进制帧）与 `DataChannel` 接口——Connection 只依赖此接口，后续可替换为 WebSocket / TCP 直连等传输；提供 `pionChannel` 把 pion/webrtc.DataChannel 适配为接口
- **message.go**：定义信令消息结构（与 peerjs-server 协议兼容）、消息类型枚举、连接类型、各 payload 结构与客户端配置 `Options`

## 关键机制

### 1. Frame（transport.go:5-10）

```go
type Frame struct {
    IsText bool
    Data   []byte
}
```

库自定类型、传输实现无关。`IsText=true` 为文本帧（控制头/JSON），false 为二进制帧（数据块）。pion `OnMessage` 回调里 `m.IsString` 直接映射（transport.go:50-53）。

### 2. DataChannel 接口（transport.go:14-26）

```go
type DataChannel interface {
    SendText(string) error
    Send([]byte) error
    OnOpen(func())
    OnMessage(func(Frame))
    OnClose(func())
    Open() bool
    BufferedAmount() uint64
    SetBufferedAmountLowThreshold(uint64)
    OnBufferedAmountLow(func())
    Close()
}
```

接口覆盖了上层所需的全部数据面操作，**包括流控三件套**（BufferedAmount / SetBufferedAmountLowThreshold / OnBufferedAmountLow）——上层 transport 包的水位流控（sessions.md）依赖它们。换传输（如 TCP 直连）时实现该接口即可，Connection 层零改动。

`pionChannel` 是薄适配：字段转发 + `OnMessage` 里把 `webrtc.DataChannelMessage` 转成 `Frame`。

### 3. 信令消息协议（message.go）

```go
type Message struct {
    Type    MessageType     `json:"type"`               // 开放 string 类型
    Src     string          `json:"src,omitempty"`      // 服务端覆盖为客户端 id
    Dst     string          `json:"dst,omitempty"`
    Payload json.RawMessage `json:"payload,omitempty"`  // 类型决定结构
}
```

消息类型与 peerjs-server 枚举一致：`OPEN/LEAVE/CANDIDATE/OFFER/ANSWER/EXPIRE/HEARTBEAT/ID-TAKEN/ERROR`。连接类型 `ConnData="data"` / `ConnMedia="media"`。

三个 payload 结构（SDP 用 `*webrtc.SessionDescription`，与 pion 直接互操作）：

| Payload | 关键字段 |
|---|---|
| `OfferPayload` | sdp / type / **connectionId**（offerer 定义、双方共用）/ label / reliable / serialization="raw" / metadata |
| `AnswerPayload` | sdp / type / connectionId |
| `CandidatePayload` | candidate（`webrtc.ICECandidateInit`）/ type / connectionId |

### 4. Options（message.go:83-92）

| 字段 | 默认 | 说明 |
|---|---|---|
| Host | 0.peerjs.com | 信令服务器 |
| Port | 443 | — |
| Secure | — | wss/https |
| Path | "/" | 自托管 server 路径前缀 |
| Key | "peerjs" | API key |
| ID | — | 节点 ID；空则服务端分配 |
| Token | 随机生成 | 认证 token |
| PingInterval | 5s | 心跳间隔 |
| ICEServers | — | WebRTC ICE/TURN 服务器 |

约束：新增配置项保持向后兼容（默认值不改变既有行为）。

## 与其它模块的关系

- `Connection.Send/SendText/SendFrame` 全部经 `DataChannel` 接口（connection.md）
- `Peer` 用 `Message`/各 payload 与信令服务器通信（peer.md）；`Options` 由 `NewPeer`/`normalizeOptions` 消费
- 上层（internal/transport）通过 `Connection.DataChannel()` 拿到接口做流控——**WebRTC 特有的水位流控只在这里暴露**（WSSession 是 TCP 自带背压，见 sessions.md）

## 坑与设计决策

| 坑 | 说明 |
|---|---|
| 文本/二进制帧区分是协议基础 | SendText=PPID 51 vs Send=PPID 53，上层「控制头 vs 数据块」状态机依赖它 |
| 替换式流控回调 | `OnBufferedAmountLow` 只能注册一次（connection.md 详述） |
| 接口宽度 | DataChannel 接口故意含流控 API——换传输（如 TCP）时可无实现返回 0/空回调，但原语边界明确 |
| 协议兼容 | Message/payload 字段名与 peerjs-server 对齐（camelCase JSON），自托管 signalserver 才能互通 |

## 测试

- `flowcontrol_test.go`：SendFrame 流控行为（低水位等待/超时/关闭路径）
- `peer_test.go`：经内存信令桩验证消息构造/路由（含 payloadConnectionID 提取）
- 无独立测试文件——本模块是被测对象的类型层，覆盖经 Connection/Peer 测试实现

## 文件清单

| 文件 | 说明 |
|---|---|
| `transport.go` | Frame + DataChannel 接口 + pionChannel（57 行） |
| `message.go` | Message/枚举/payload/Options + package 文档注释（93 行） |