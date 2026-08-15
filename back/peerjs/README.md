# go-peerjs

Go 实现的 [PeerJS](https://peerjs.com) 兼容信令客户端 + WebRTC DataChannel 传输层。
基于 `pion/webrtc/v4`，与浏览器端 peerjs（`serialization: "raw"`）及 Go 节点互通。

**模块定位：传输原语（信令 + 数据面）。业务帧协议（verb）由上层定义** ——
与 [hana-link](https://github.com/Hana-ame/hana-link)（PeerJS+MQTT 传输层，格式无关）同一设计哲学。

```
浏览器(peerjs) ──公共云信令(0.peerjs.com)──┐
                                          ├── WebRTC DataChannel 直连
Go 节点(本模块) ──公共云信令──────────────┘
```

## 安装

```bash
go get github.com/Hana-ame/go-peerjs
```

## 快速开始

```go
import (
    "context"
    peerjs "github.com/Hana-ame/go-peerjs"
)

// 节点 A：被动服务（提供文件）
opts := peerjs.DefaultOptions()
opts.ID = "pd-node-a"
opts.ICEServers = []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}

p := peerjs.NewPeer("pd-node-a", opts)
if err := p.Dial(ctx); err != nil { log.Fatal(err) }

// 被动连接：对端连上来后收发数据
p.OnConnection(func(c *peerjs.Connection) {
    c.OnMessage(func(f peerjs.Frame) {
        if f.IsText { /* JSON 控制帧 */ } else { /* 二进制数据块 */ }
    })
})

// 节点 B：主动连接 A 并发送数据
p2 := peerjs.NewPeer("pd-node-b", peerjs.DefaultOptions())
p2.Dial(ctx)
conn, err := p2.Connect(ctx, "pd-node-a", "peerdrive")
conn.OnOpen(func(c *peerjs.Connection) {
    c.SendJSON(map[string]any{"type": "hello"})          // 文本帧
    c.SendFrame(map[string]any{"type": "data", "size": 4}, []byte("1234")) // 原子「头+体」帧
})
```

## 功能集（Function Set）

### 类型

| 类型 | 说明 |
|---|---|
| `Options` | 配置：`Host/Port/Secure/Path/Key`（信令）、`ID/Token`（身份）、`PingInterval`、`ICEServers`（STUN/TURN） |
| `MessageType` | 信令消息类型，开放 string：`OPEN/OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE/HEARTBEAT/...`，自定义类型直接用字面量 |
| `Message` | 信令消息 `{Type, Src, Dst, Payload}` |
| `Frame` | 数据帧 `{IsText, Data}`：`IsText=true` 文本帧（JSON 控制头），`false` 二进制块 |
| `Offer/Answer/CandidatePayload` | OFFER/ANSWER/CANDIDATE 负载（与 peerjs 协议逐字段对齐） |

### Peer（信令客户端）

| 方法 | 说明 |
|---|---|
| `NewPeer(id string, opts Options) *Peer` | 创建；`id` 空则服务端分配随机 ID |
| `NewPeerWithSignaller(s Signaller) *Peer` | **扩展点**：注入自定义信令实现 |
| `p.Dial(ctx) error` | 注册并连接信令服务器 |
| `p.OnConnection(fn)` | 被动连接回调（对端发起 OFFER） |
| `p.Connect(ctx, dst, label) (*Connection, error)` | 主动发起连接（offerer） |
| `p.Send(m Message) error` | 发送自定义信令消息（扩展点） |
| `p.ID()` / `p.Connected()` | 节点信息查询 |
| `p.Close()` | 关闭信令 + 全部连接 |

### Connection（数据连接）

| 方法 | 说明 |
|---|---|
| `c.OnOpen(fn)` / `c.OnClose(fn)` / `c.Done()` | 生命周期事件 |
| `c.OnMessage(fn(Frame))` | 数据回调（文本/二进制帧） |
| `c.Send(data []byte)` | 二进制帧（数据块） |
| `c.SendText(s)` / `c.SendJSON(v)` | 文本帧（JSON 控制头） |
| `c.SendFrame(header, body)` | **原子「头+体」帧**（并发安全 + 内置写缓冲流控，见协议约束） |
| `c.DataChannel() DataChannel` | 底层通道（高级用法：关闭等） |
| `c.Open()` / `c.Close()` | 状态与关闭 |

| 层 | 职责 | 扩展点 |
|---|---|---|
| `Signaller` | 信令通道：注册节点、转发信令消息 | 换信令（MQTT 房间、自托管 server）：实现 `Dial/ID/Send/Close` |
| `Peer` | 节点角色：路由 OFFER/ANSWER/CANDIDATE、连接注册表（一对多）、生命周期 | 复用；`NewPeer` / `NewPeerWithSignaller` |
| `Connection` | 一条数据连接：SDP 交换、ICE 转发、帧发送/流控、事件 | 换传输：实现 `DataChannel` 接口（WS/TCP 直连） |

分层理由：各自可独立替换/测试（fake signaller 测路由、fake dc 测帧协议）；
Peer 不依赖具体信令协议，Connection 不依赖具体传输。

扩展：`MessageType` / `Frame` 为开放类型——自定义 verb/消息类型无需改库。

> **`MessageHandler` / `Signaller.OnMessage` 已废弃（Deprecated）**：消息处理由
> `Peer` 内部路由完全接管（OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE 均自动处理），
> 使用者无需接触回调。实现自定义 `Signaller` 时，只需实现
> `Dial/ID/Send/Close`，收到信令消息后交给框架注入的回调即可
> （参照 `peerJSSignaller.readLoop` 的做法）。

## 协议与约束（勿破坏）

### 信令（PeerJS 公共云）

- 注册：指定 `ID` 直接连 `wss://host:port/peerjs?key=&id=&token=`；未指定先 `GET /id` 取随机 ID
- 消息经服务器按 `dst` 转发，`src` 由服务器覆盖
- **connectionId 由 offerer 定义，answerer 必须沿用**（曾因 answerer 新生成 ID 导致 ANSWER 路由不到、ICE 卡 checking）
- **ICE 候选必须手动经信令转发**（`OnICECandidate` → `CANDIDATE` 消息），pion 不会自动发送
- 心跳：客户端每 `PingInterval` 发 `HEARTBEAT` 保活

### 数据面（DataChannel）

- **JSON 控制头必须是文本帧，数据块必须是二进制帧**（`SendText` vs `Send`）——发反了对端把控制头当数据块吞掉
- **`SendFrame` 原子发送 + 内置流控**：接收端按「data 头 → 紧随的二进制块」状态机路由，头体交织会挂错请求；
  发送端 sendMu 串行 + bufferedAmount 低水位等待（回调 attach 时注册一次、lowWater 广播，
  连接关闭立即退出等待）。**调用方不要再自行注册 `OnBufferedAmountLow`**（pion 替换式回调，并发注册会互相覆盖 → 死等）
- 接收端同一连接同一时刻只有一个「期待数据块」状态（发送端原子帧保证）

### 业务帧协议（上层定义，库不管）

```jsonc
// 请求（任意端）；reqId 建议 UUID v4
{"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<uuid>"}
// 响应（回显 reqId）
{"type":"meta","hash","total","reqId"}
{"type":"data","hash","offset","size","reqId"}   // 后随 size 字节二进制
{"type":"done","hash","offset","size","reqId"}
{"type":"err","msg","reqId"}
```

## 参考实现

Peerdrive 后端 `internal/service/peerjs_service.go` 是本模块的完整业务用法示例：
文件服务（serveFile）+ 主动拉取（requestFile，reqId 路由状态机）+ MQTT 分片房间发现
（`internal/service/mqtt_discovery.go`，topic `peerdrive/v1/{collectionHash}/nodes`）。
