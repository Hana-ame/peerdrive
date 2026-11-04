# AGENTS.md

## webrtc

在 PeerJS **信令**之上建立 DataChannel，通道里跑带类型的二进制帧：文件本体、metadata、控制信号、校验都能传。

**分工明确**：`peerjs` 只搬 SDP（OFFER / ANSWER / CANDIDATE），**不建 PeerConnection**；PeerConnection 与 DataChannel 全部由本模块建。

- `conn.go` — `Listen()` 注册信令 + 应答 offer、`Conn` 收发封装
- `frame.go` — 帧格式、分片与重组
- `meta.go` — `FileInfo` / `Manifest` / `Check` 等默认载荷，sha256 工具
- `cmd/webrtc-node` — 入口，打印 `PEER ID`，把收到的帧落盘
- `index.html` — 浏览器 demo，用原生 `RTCPeerConnection`，只借 PeerJS 信令
- `e2e/connect.mjs` — CI 用：抓 id → 建连 → ping/pong → metadata/ack → 8 MiB + sha256

### 契约

```go
srv, err := webrtc.Listen(ctx, webrtc.Config{
    OnConn: func(c *webrtc.Conn) {
        for {
            f, err := c.Next(ctx)          // Kind / Stream / Data
            if err != nil { return }
            if f.Kind == webrtc.KindControl { /* ... */ }
            _ = c.SendControl(f.Stream, webrtc.ActionPong, nil)
            _ = c.SendBytes(f.Stream, payload)   // 自动分片
            _ = c.SendJSON(webrtc.KindMetadata, 0, manifest)
        }
    },
})
id := srv.ID() // 就是打进日志的那条串
```

浏览器侧不需要私有握手，标准 PeerJS 建连即可，但**要自己发 OFFER**（因为 Go 侧不建连接，只应答）：

```js
const pc = new RTCPeerConnection({ iceServers: [...] });
const dc = pc.createDataChannel("peerdrive", { ordered: true });
dc.binaryType = "arraybuffer";
const offer = await pc.createOffer();
await pc.setLocalDescription(offer);
peer.socket.send({
  type: "OFFER",
  payload: { type: "data", connectionId: dc.label, label: dc.label,
             serialization: "binary", sdp: { type: "offer", sdp: pc.localDescription.sdp } },
  dst: goID,
});
```

### 帧格式

```text
+--------+-----------------+----------------------------+
| kind   | stream          | payload                    |
| 1 byte | 4 byte 大端     | 变长，可空                  |
+--------+-----------------+----------------------------+
```

超过 **64 KiB** 时切成多片，分片帧的 payload 前缀：

```text
flag(1)=1 + seq(4) + total(4) + data
```

| Kind | 值 | payload |
| --- | --- | --- |
| `hello` | 1 | JSON：`{role, version, proto}` |
| `control` | 2 | JSON：`{action, at, ...}` |
| `metadata` | 3 | JSON：`Manifest` / 任意结构 |
| `data` | 4 | **原始二进制**，不做任何编码 |
| `check` | 5 | JSON：`{algo:"sha256", digest, size, ok}` |
| `ack` | 6 | JSON：对某 stream 的确认 |
| `error` | 7 | JSON |

`stream` 由发起方自增分配，同一 stream 上的 metadata / data / check 逻辑上属于同一次传输。

### 约定

- **只出站**：不监听端口，只连 `0.peerjs.com:443`。
- **全二进制**：所有帧都走 `dc.send(Uint8Array)` / `dc.Send([]byte)`。PeerJS 的 json serializer 会把数据包成 `TextEncoder.encode(JSON.stringify(x))`，这里**不用**它，`serialization: "binary"`。
- **分片必须可靠**：DataChannel 用 `ordered: true`，否则重组会错位。
- **重组按 stream 独立**：一个连接一个 `Reassembler`，乱序到达会自动归位。
- **接收队列有上限**（64 帧），满了丢帧并打日志，避免阻塞 SCTP 读循环。
- **并发**：一个 `PEER ID` 可被多个浏览器同时连接，每个连接独立 `PeerConnection` + `DataChannel`。

### 与 peerjs 的边界

`webrtc` 通过 `peerjs.RegisterTransport(srv)` 接管入站 OFFER。接管后 `peerjs` 不再为该连接建 `PeerConnection`，`OnMessage` 也不会触发——字符串回显那套只属于 `peerjs/cmd/peerjs-node`。

### 链接

- 上层索引：`../AGENTS.md`
- 信令契约：`../peerjs/AGENTS.md`

## CI

`webrtc/**`、`peerjs/**` 或 `go.work` 变化时跑；push 不限分支，分支上直接能看到结果。
