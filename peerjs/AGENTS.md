# AGENTS.md

> 本仓是 peerdrive **按模块拆分**后的入口 / 索引仓。默认分支 `main` 只承载说明与索引，不承载实现代码。

## peerjs

本机进程当 PeerJS peer：纯出站连 `0.peerjs.com`，拿一个 id 打在日志里，浏览器用这个 id 连过来互传字符串。**不监听任何端口，不提供 http / ws 接口。**

- `cmd/peerjs-node` — 入口，启动后打印 `PEER ID`
- `peerjs/` — 客户端实现（信令 + WebRTC 应答 + DataChannel）
- `index.html` — 浏览器侧 clean demo，标准 PeerJS 用法
- `e2e/connect.mjs` — CI 用：从 `peer.log` 抓 id，Playwright 打开 demo，断言收到回显

### 契约

```go
c, err := peerjs.Dial(ctx, peerjs.Config{})  // 取候选 id → wss 注册 → 等 OPEN
id := c.ID()                                 // 就是打进日志的那条串
c.OnMessage = func(remote, text string) {    // 收到字符串
    c.Send(remote, "echo: "+text)            // 回一条
}
```

浏览器侧不需要任何私有协议：

```js
const conn = peer.connect(id, { serialization: 'json' });
conn.on('open', () => conn.send('hello'));
conn.on('data', text => console.log(text));
```

### 约定

- **只出站**：不监听端口，只连 `0.peerjs.com:443`。防火墙只放行出站即可。
- **id 从 log 读**：`PEER ID` 那一行。`/peerjs/id` 返回的就是最终 id（`OPEN` 不回传 id），每次启动都是新串，wss 断开即失效。
- **serialization 必须两边一致**：目前只支持 `json`。PeerJS 的 json serializer 发送时是 `TextEncoder.encode(JSON.stringify(x))`，即**二进制帧**；Go 侧必须 `dc.Send([]byte(...))`，用 text frame 会被浏览器 `TextDecoder.decode(string)` 抛错并静默丢弃。
- **一条通道全双工**：浏览器主动 `connect`，Go 侧回 `ANSWER`；两端都能发能收，不需要互填 id。
- **并发**：一个 id 可被多个浏览器同时连接，每个连接一个 `PeerConnection` + `DataChannel`，`peerjs` 用 `map[connectionId]*session` 管理，没有计数上限。
- **消息只是数据**：浏览器发来的字符串不做任何解释，只按 UTF-8 字符串处理。
- 公共信令无认证，id 不是身份；生产换自建 PeerServer（`PEERDRIVE_SERVER_URL`）。

### 信令消息

| 消息 | 结构 |
| --- | --- |
| `OPEN` | `{"type":"OPEN"}`，无 payload |
| `OFFER` | `payload={type:"data",connectionId,label,serialization,sdp:{type:"offer",sdp}}` + `src` |
| `ANSWER` | `payload={sdp:{type:"answer",sdp},type:"data",connectionId}` + `dst` |
| `CANDIDATE` | `payload={candidate:{candidate,sdpMLineIndex,sdpMid},type:"data",connectionId}` |
| 心跳 | 每 5 秒 `{"type":"HEARTBEAT"}` |

### 链接

- 相邻模块说明页：`../hash-to-metadata-impl.html`（工作区根目录，非仓库文件）
- 图文说明在 `index.html`，这里只留契约。
