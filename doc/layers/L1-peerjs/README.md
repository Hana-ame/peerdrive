# ① 信令/传输原语层 — back/peerjs/（模块总览）

> 一句话：PeerJS 信令 + WebRTC DataChannel 传输原语，**零业务知识**——业务帧协议（verb）由上层（AOP ② transport 包）定义。

## 模块清单

| 模块 | 文件 | 文档 |
|---|---|---|
| Connection | `connection.go` | [connection.md](connection.md) |
| Peer + 信令客户端 | `peer.go` + `signaller.go` | [peer.md](peer.md) |
| 传输抽象 + 消息类型 | `transport.go` + `message.go` | [transport.md](transport.md) |

## 三模块协作

```
上层（internal/service/peerjs_service.go，AOP ②）
  │  NewPeer / Connect / OnConnection / SendFrame / OnMessage
  ▼
Peer（信令：注册/路由/心跳/重连）          Connection（单连接：帧原子发送/流控/生命周期）
  │  OFFER/ANSWER/CANDIDATE 路由             │  SendFrame(头, 体) / SendText / Send
  ▼                                          ▼
peerJSSignaller（WS 信令，H7 断线通知）    DataChannel 接口（pionChannel 适配）
```

## 关键事实

- 独立 go.mod（`github.com/Hana-ame/go-peerjs`），主 go.mod `replace` 引用——独立可演进
- 三个扩展点：`Signaller` 接口（换信令）、`DataChannel` 接口（换传输）、开放 string `MessageType`（加消息）
- 文本帧（PPID 51）vs 二进制帧（PPID 53）的区分是上层帧协议的基础（控制头 vs 数据块）
- 流控三件套（BufferedAmount/LowThreshold/OnBufferedAmountLow）在此层暴露，上层 serveFile 水位流控依赖（sessions.md）
- 无业务知识：不知道 req/upload/admin 等任何 verb

## 测试

- `peer_test.go`（内存信令桩，不依赖公网）+ `flowcontrol_test.go`（流控路径）
- 集成测试：`cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v`（真实公共信令 0.peerjs.com，需代理，必须 `-p 1` 串行）

## 文件清单

| 文件 | 行数 | 说明 |
|---|---|---|
| `peer.go` | 560 | Peer + peerJSSignaller（信令客户端/心跳/H7） |
| `connection.go` | 335 | Connection（帧原语/流控/生命周期） |
| `transport.go` | 57 | Frame + DataChannel 接口 + pionChannel |
| `message.go` | 93 | Message/payload/Options |
| `signaller.go` | 39 | Signaller 接口 |
| `peer_test.go` | 450 | 单测 |
| `testutil_test.go` | 174 | 内存信令桩 |
| `flowcontrol_test.go` | 77 | 流控单测 |
| `README.md` | — | 库自身 README |