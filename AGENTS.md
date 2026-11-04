# AGENTS.md

> 本仓是 peerdrive **按模块拆分**后的入口 / 索引仓。默认分支 `main` 只承载说明与索引，不承载实现代码。

## peerjs

用 `0.peerjs.com` 公共 PeerServer 做信令，浏览器之间建 WebRTC DataChannel，**只互传字符串**。入口 `index.html`（可玩客户端 + 可视化说明），`peerjs.min.js` 是 vendored 的 PeerJS 1.5.5，页面不依赖 CDN 才能加载。

### API

```js
new Peer({ host, port, path, secure, key })     // 注册到信令，拿到本端 id
peer.on('connection', conn => …)                // 被动接入
peer.connect(id, { serialization: 'json' })     // 主动连出去
conn.send(text)                                 // 发字符串
conn.on('data', text => …)                      // 收字符串
```

两端绑定到同一套 `open / data / close / error`，主动方与被动方都能先发。

### 配置

| 参数           | 值                       | 说明                                                        |
| -------------- | ------------------------ | ----------------------------------------------------------- |
| `host`         | `0.peerjs.com`           | 云信令。备用 `peersignal.moonchan.xyz`（自建 PeerServer）    |
| `port`         | `443`                    | 云 PeerServer 只跑 443                                      |
| `path`         | `/`                      | 取 id 的 HTTP 端点是 `/peerjs/id`                           |
| `secure`       | `true`                   | https / wss；HTTPS 页面下必须开                             |
| `key`          | `peerjs`                 | 只有 `0.peerjs.com` 需要，自建服务不用                      |
| `serialization`| `json`                   | `connect()` 的选项，不是 `Peer()` 的选项；载荷是字符串      |

`.env`：`PEERDRIVE_PEERJS_URL=https://0.peerjs.com`、`PEERDRIVE_SERVER_URL=https://peersignal.moonchan.xyz`。页面「信令」下拉可切换两者。

### 约定

- 载荷只有字符串；不做文件分片、媒体流、二进制协议。
- 对端字符串不可信：消息与日志一律 `textContent`，不拼 `innerHTML`。
- 库本地优先，CDN 仅兜底；**信令仍然要联网**。
- `error` / `disconnected` / `close` 都要处理，断线用 `peer.reconnect()`。
- 公共信令无认证、id 随机分配，**id 不是身份**；生产换自建 PeerServer。

### 链接

- 本页 ↔ `m/hash-to-metadata` 的实现说明页 `../hash-to-metadata-impl.html`（工作区根目录，非仓库文件）。
- 详细说明放在 `index.html`，这里只留契约。
