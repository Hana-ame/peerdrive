# peerdrive-client

peerdrive 的**纯浏览器消费端**：连上一个 peerdrive 节点，列出它共享的内容，把文件拉回来。

零依赖、纯 ESM、传输无关。对应需求里的「webrtc 纯 client 消费端，可以从这个 p2p 网络拉取文件」——
一个静态页面就能用，不需要本地跑任何后端。

```js
import Peer from 'peerjs'
import { connectToPeer } from 'peerdrive-client'

const client = await connectToPeer(Peer, 'peerdrive-1a2b3c4d', {
  peerOptions: { host: '0.peerjs.com', port: 443, secure: true },
})

const snap = await client.shares()            // { collections, files, dirs, total }
await client.saveAs(snap.files[0].hash, 'a.bin')   // 拉下来存到本地，sha256 已校验
client.close()
```

不想用打包器？直接把 `peerjs` 的 CDN `<script>` 放前面，然后在 `type="module"` 里用全局 `Peer`
——`demo/consumer.html` 就是这么做的。

## 它解决的问题

peerdrive 节点（`back/` 里的 Go 服务）既是持有方也是消费方：节点之间用同一套帧协议互联。
但「只想取一个文件」的客户端不应该被要求部署一个 Go 节点。本包把消费侧从服务端拆出来：

| 场景 | 用什么 |
| --- | --- |
| 节点 ↔ 节点 互联、互相提供内容 | `back/` 的 Go 服务（同一套帧协议） |
| 浏览器 ↔ 节点 拉文件 | **本包** |
| 浏览器 ↔ 节点 加载 URL 资源（img/video） | `packages/peerdrive-media`（另一套 url/meta 协议，面向媒体元素） |

## 安装

本包零运行时依赖。`peerjs` 由调用方提供（不写进 `dependencies`，避免污染使用方的打包体积）。

```bash
npm i peerdrive-client
```

## 快速开始

### 1. 一步到位（推荐）

```js
import Peer from 'peerjs'
import { connectToPeer, ERR } from 'peerdrive-client'

const client = await connectToPeer(Peer, nodeId, {
  peerOptions: { host, port, path, key, secure },
  connOptions: {},              // 透传给 peer.connect，一般不用改
  idleTimeoutMs: 120_000,       // 多久没收到该请求的帧就判死
  maxBufferBytes: 256 << 20,    // fetch/saveAs 的内存闸
})
```

内部等价于：建 Peer → 等信令 `open` → `peer.connect(nodeId, { serialization: 'raw' })` → 等 DataChannel `open`。

> ⚠️ `serialization` 必须是 `'raw'`。只有 raw 模式下 `string` 走文本帧、`ArrayBuffer` 走二进制帧，
> 才能复刻 Go 侧「文本帧 = JSON 头 / 二进制帧 = 数据块」的语义。用默认的 `binary` 序列化会让数据块
> 被 peerjs 自己的 chunker 包装，对端解析不出来。`connectToPeer` 已经设好，手写时别漏。

### 2. 自管连接（已有 PeerJS 连接 / 换传输）

```js
import { connect } from 'peerdrive-client'

const conn = peer.connect(nodeId, { serialization: 'raw' })   // 或任何满足 {on,send,open,close} 的对象
const client = await connect(conn, { idleTimeoutMs: 60_000 })
```

## API

### `connectToPeer(PeerCtor, peerId, opts?) → Promise<PeerDriveClient>`
建信令 + 拨号 + 等就绪。`PeerCtor` 是 PeerJS 的 `Peer` 构造函数（本包不 import peerjs）。

### `connect(conn, opts?) → Promise<PeerDriveClient>`
包装一个已建立的连接。`conn` 只需满足 `{ on(type, cb), send(data), open?, close?() }`。

### 客户端实例

| 成员 | 说明 |
| --- | --- |
| `ready(timeoutMs?)` | 等连接就绪；已就绪立即 resolve |
| `isOpen` | 是否已就绪 |
| `peerId` | 对端 id（从连接上读） |
| `shares({timeoutMs?})` | 查共享清单 → `{collections, files, dirs, total, peerId}` |
| `stream(hash, opts?)` | **异步迭代器**，逐块产出 `Uint8Array`（不驻留内存） |
| `fetch(hash, opts?)` | 整体取回 `Uint8Array`（受 `maxBytes` 限制） |
| `fetchBlob(hash, opts?)` | 取回 `Blob`（按文件名猜 MIME） |
| `fetchText(hash, opts?)` | 取回 utf-8 文本 |
| `saveAs(hash, filename?, opts?)` | 触发浏览器下载；返回字节数 |
| `saveShare(item, opts?)` | 从清单条目直接保存（自动取文件名） |
| `stats` | `{requests, chunks, bytes, failures}` |
| `close()` | 关连接（自己建的 Peer 一并销毁） |

`stream` / `fetch` 的 opts：

| 选项 | 默认 | 说明 |
| --- | --- | --- |
| `offset` | `0` | 起始偏移（range 请求） |
| `size` | `-1` | 读取长度，`-1` = 到文件末尾 |
| `maxBytes` | `256MB` | 内存闸，超出抛 `TOO_LARGE` |
| `onProgress(received, total, hash)` | — | `total` 为 `-1` 表示对端未声明大小 |
| `signal` | — | `AbortSignal`，可取消 |

### 流式拉取（大文件）

```js
let received = 0
for await (const chunk of client.stream(hash)) {
  received += chunk.byteLength
  await fileHandle.write(chunk)     // File System Access API / StreamSaver / 直接落盘
}
// 全量请求在结束时已校验 sha256，不匹配会抛 HASH_MISMATCH
```

`stream()` 的块只进**有界队列**，消费端不取就自然形成背压；提前 `break` 会取消该请求
（后续到达的帧被丢弃）。

### 错误码

UI 请按 `err.code` 分支，不要去匹配 `message` 文案。

| code | 含义 | 常见处理 |
| --- | --- | --- |
| `INVALID_HASH` | hash 不是 64 位小写 hex | 输入校验，本地即拒（不发请求） |
| `TOO_LARGE` | 超出 `maxBytes` / 协议上限 8GB | 改用 `stream()` 或调大闸 |
| `HASH_MISMATCH` | 内容与 hash 不符 | 丢弃内容并提示（数据损坏/被篡改） |
| `INCOMPLETE` | 对端 done 的字节数与实收不符 | 重试 |
| `TIMEOUT` | 空闲超时 / verb 无应答 | 提示对方离线或网络不稳 |
| `CLOSED` | 连接关闭 | 重连 |
| `PROTOCOL` | 帧不合法（对端不是 peerdrive 节点？） | 报错，不要重试 |
| `PEER` | 对端回了 `err` 帧（not found 等） | 展示对端文案 |
| `CANCELLED` | 本地取消（`signal` / 迭代中断） | 静默 |

## 协议

与 `back/internal/transport/conn.go` 的定义**逐字对齐**（字段名、大小写、`-1` 语义都一致）：

```
请求   {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"…"}
响应   {"type":"meta","hash":"…","total":N,"reqId":"…"}
       {"type":"data","hash":"…","offset":N,"size":N,"reqId":"…"} + 紧随 size 字节二进制块
       {"type":"done","hash":"…","offset":N,"size":N,"reqId":"…"}
       {"type":"err","msg":"…","reqId":"…"}
共享   {"type":"share","reqId":"…"}
       → {"type":"share-resp","collections":[…],"files":[…],"dirs":[…],"total":N,"reqId":"…"}
```

三条不能破坏的约束：

1. **data 头是文本帧、数据块是二进制帧**。发反了对端会把 JSON 当数据吞掉。
2. **data 头与其数据块必须连续**。接收端用的是「连接级 expect」——把二进制块挂到**最近一个**
   data 头所属的请求上，块里不带 `reqId`。所以不能把两个请求的块交错发送（Go 侧靠连接级
   `sendMu` 保证 `SendFrame` 原子）。本包在测试里专门覆盖了这条（见
   `test/client.test.mjs` 的「连接级 expect 与乱序帧」）。
3. **字段名逐字对齐**。改成 `req_id` 不会报错，只会让对端路由不到、请求挂到超时。

## 设计取舍

**为什么传输无关。** `PeerDriveClient` 只要求 `{ on, send, open, close }`，不认识 PeerJS。
收益：包本身零依赖；测试用假连接即可覆盖全部状态机（不需要信令服务器与真 WebRTC）；
将来换裸 `RTCPeerConnection` 或 WebTransport 不用动这个文件。

**为什么自己写 SHA-256。** `crypto.subtle.digest()` 是一次性的——必须先把整份内容攒进内存
才能算摘要，和「流式拉取」直接冲突。所以流式路径用纯 JS 增量实现（`src/sha256.js`），
一次性路径 `sha256Hex()` 仍优先走 WebCrypto。另一个好处是纯 JS 不要求安全上下文
（https/localhost），http 页面也能用。两条路径在测试里互相校验。

**为什么有内存闸。** `fetch()`/`saveAs()` 是整体驻留内存的（浏览器下载只能走 Blob），
一个 2GB 文件在手机上会直接崩标签页。所以默认 256MB 上限、超了抛 `TOO_LARGE` 并提示
改用 `stream()`；对端声明的大小时在 `meta` 阶段就拦，不白下。

**为什么取消只是丢帧。** 协议里没有「取消」帧（Go 侧也没有对应实现），对端会继续把这次
请求发完。本包的做法是本地不再收集并释放已缓存块；要真正中断只能关掉整条连接。

**为什么 `shares()` 的空清单不是错误。** 对方没开启对外共享是合法业务状态
（`PEERDRIVE_SHARE_ENABLE` 默认关闭），UI 应当渲染「该节点没有共享内容」而不是红色报错。

## 演示

```bash
cd packages/peerdrive-client
npm run demo      # http://127.0.0.1:8123/demo/consumer.html
```

demo 是单个静态页面（`demo/consumer.html`）：填信令配置 + 对方节点 id → 连接 → 列出共享清单
→ 逐个保存或按 hash 直接拉取。要连自托管信令就把 host/port/path/key 填成节点启动时的
`PEERDRIVE_PEERJS_*` 配置（注意 `secure` 要和 `ws://`/`wss://` 对上）。

> 本地直接 `file://` 打开不行：ESM 模块与 WebCrypto 都需要 http(s) 上下文。用 `npm run demo`。

## 测试

```bash
npm test          # node --test，60 个用例
```

- `test/protocol.test.mjs` — 帧格式/字段名/纯函数（钉死与 Go 侧对齐的字面形状）
- `test/sha256.test.mjs` — 增量 SHA-256 vs `node:crypto` 逐长度比对（含 55/56/57/63/64/65 边界）
- `test/client.test.mjs` — 状态机：正常路径、6 类故障、连接级 expect 与乱序帧、取消、内存闸

CI 见仓库根 `.github/workflows/ci.yml` 的 `client-package` job（零依赖，所以不需要 `npm ci`）。

## 已知限制

- **大文件落盘需要调用方自理**：`saveAs()` 整份驻留内存；真正的大文件应当用 `stream()`
  配合 File System Access API 或 StreamSaver 写盘（浏览器不允许 `<a download>` 流式写盘）。
- **没有断点续传**：连接断开后重新拉取会从头开始。协议支持 `offset`，续传逻辑在调用方。
- **`list` 帧没有实现**：`list` 是节点的本地管理索引（含本机绝对路径），只对可信对端开放。
  消费端只走 `share`（运营者显式声明的共享范围）。
- **身份校验**：当前协议不校验请求者身份（见 `doc/ROADMAP.md` 的阶段顺序，身份管理排在最后）。
  因此节点只会对外提供本来就允许公开的内容。
