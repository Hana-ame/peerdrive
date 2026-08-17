# peerdrive-media

通过 **PeerJS 信令 + WebRTC DataChannel** 从 Node.js 端加载 URL 资源，并以 `<img>` / `<video>` 形式渲染在网页上。配套 peerdrive 的 standalone package：

- **React 组件**：`<PeerImage>` / `<PeerVideo>` / `<PeerMedia>`（React 18+，与框架无关的 hook 也可单独用）
- **Vanilla 版本**：IIFE/ESM 构建产物，`<script>` 直接引用
- **Node 端提供者**：一个函数起一个 peerjs 节点，`fetch` URL → 分块流式回传

```
浏览器（React / vanilla）
   │  peerjs DataConnection（serialization: 'raw'）
   │  文本帧 = JSON 头 / 二进制帧 = 数据块
   ▼
Node 端 peer（createPeerMediaServer）
   │  fetch(URL)   ← allow() 白名单校验
   ▼
任意 HTTP 资源（图片 / 视频 / 任意文件）
```

## 安装

```bash
# 首选：GitHub 独立仓库（dist 已入库，装完即用，无需构建）
npm install github:Hana-ame/peerdrive-media

# 固定版本（tag 与 package.json 的 version 同步维护）
npm install github:Hana-ame/peerdrive-media#v0.1.0

# npm registry 发布后即可：npm install peerdrive-media
```

> 为什么没有 `github:Hana-ame/peerdrive#path:packages/peerdrive-media`：npm 不支持 git 依赖的子目录语法（pnpm/yarn 才支持 `#path:`），故包独立成仓。

React 入口还需要 `react` / `react-dom`（peerDependencies）。Node 端使用需要 **Node >= 22** 与 `@roamhq/wrtc`（本包 optionalDependencies，安装失败时 Node 端不可用）。

Vanilla `<script>` 免安装直引（jsDelivr CDN，需外网）：

```html
<script src="https://cdn.jsdelivr.net/gh/Hana-ame/peerdrive-media@v0.1.0/dist/vanilla/peerdrive-media.iife.js"></script>
```

## 快速开始

### 1. Node 端：资源提供者

```js
// server.mjs
import { createPeerMediaServer, allowPrefix } from 'peerdrive-media/node'

const server = await createPeerMediaServer({
  peerId: 'my-node',                          // 网页端连这个 id
  signaling: {                                // 默认公共云 0.peerjs.com；
    host: '127.0.0.1',                        // 自托管 peerjs-server 示例
    port: 9100,
    secure: false,                            // ⚠️ Node 端必须显式传（见下方坑）
    key: 'peerjs',
    path: '/',
  },
  // 安全边界：URL 白名单必须显式配置，默认全拒
  allow: allowPrefix(['https://my-cdn.example.com/']),
  onRequest: ({ url, peer, status, bytes, ms }) =>
    console.log(`${peer} <- ${url} [${status}] ${bytes}B ${ms}ms`),
})
// server.close() 关闭
```

> **坑（Node 端）**：`signaling.secure` 必须显式传 `true`/`false`——peerjs 的 `isSecure()` 读浏览器 `location`，Node 下会 ReferenceError。
> **坑（加载顺序）**：peerjs 的 WebRTC 能力检测在模块加载时一次性缓存；本包内部已处理（动态 import）。但请勿在引 `peerdrive-media/node` 之前 import peerjs。

### 2. React 组件

```jsx
import { PeerMediaProvider, PeerImage, PeerVideo, PeerMedia } from 'peerdrive-media'

function App() {
  return (
    <PeerMediaProvider peer="my-node">
      <PeerImage url="https://my-cdn.example.com/a.png" />
      <PeerVideo url="https://my-cdn.example.com/b.mp4" controls autoPlay muted />
      <PeerMedia url="https://my-cdn.example.com/c.webp" /> {/* 按 MIME 自动选 img/video */}
    </PeerMediaProvider>
  )
}
```

- `peer` / `signaling` 也可逐组件传（优先级高于 Provider）
- `loading` / `error` 属性可传自定义 ReactNode 覆盖三态渲染
- 非图/视频 MIME（如 pdf）`<PeerMedia>` 渲染为下载链接

#### hook 形式

```jsx
import { usePeerMedia } from 'peerdrive-media'

function MyImage({ url }) {
  const { status, src, error, reload } = usePeerMedia({ url, peer: 'my-node' })
  if (status === 'loading') return <span>loading…</span>
  if (status === 'error') return <button onClick={reload}>重试：{error.message}</button>
  return <img src={src} />
}
```

### 3. Vanilla（`<script>` 直引）

```html
<script src="node_modules/peerdrive-media/dist/vanilla/peerdrive-media.iife.js"></script>
<script>
  PeerMedia.mount({ peer: 'my-node', url: 'https://my-cdn.example.com/a.png' })
    .then(({ node, blobUrl, mime }) => console.log('已挂载', mime))
</script>
```

ESM 版：`import { load, mount } from 'peerdrive-media/vanilla'`（`dist/vanilla/peerdrive-media.es.js`）。

## API

### 帧协议（自定，raw 序列化）

| 方向 | 帧 | 说明 |
|---|---|---|
| web → node | `{"type":"url","url":"…","reqId":"…"}` | 文本帧（JSON） |
| node → web | `{"type":"meta","status":200,"mime":"image/png","size":N,"reqId"}` | 文本帧 |
| node → web | 二进制帧 ×N（64KB/块，流式） | 属于最近的 meta |
| node → web | `{"type":"done","reqId"}` / `{"type":"err","msg":"…","reqId"}` | 文本帧收尾 |

`serialization: 'raw'` 是协议基础：string 原样走文本帧（SCTP PPID 51）、ArrayBuffer 走二进制帧（PPID 53），两端可区分「控制头 vs 数据块」——与 peerdrive Go 端帧语义一致。

### 浏览器核心

| API | 说明 |
|---|---|
| `client.load(url, { peer, signaling, signal })` | → `Promise<{ blob, blobUrl, mime, size }>`；`blobUrl` 用完需 `URL.revokeObjectURL()` |
| `client.dispose(peer, signaling)` | 手动释放连接（通常不需要：连接按 peerId+信令缓存复用） |
| `DEFAULT_SIGNALING` | 公共云 `0.peerjs.com:443` |

多个组件加载同一 peer 共享一条 DataConnection（reqId 多路复用），连接级失败会 reject 全部在途请求。

### Node 端

| API | 说明 |
|---|---|
| `createPeerMediaServer({ peerId, signaling, allow, fetchImpl, chunkSize, onRequest })` | → `{ peerId, peer, close() }` |
| `allowPrefix(prefixes)` | 便捷白名单：URL 以任一前缀开头 |

- `fetchImpl` 可注入（测试/代理场景）；默认 `globalThis.fetch`
- 并发：同一连接一次只服务一个请求（二进制块无头标识，靠「最近 meta」归属）；并发请求回 `err` 帧
- 流式 + 背压：`bufferedAmount > 4MB` 时暂停读上游流

## 构建

```bash
npm run build    # dist/react（ESM）+ dist/vanilla（IIFE + ESM）
npm test         # 协议单测 + 双端 E2E（本地信令，离线可跑）
```

## Demo

```bash
npm run demo:signal   # 终端1：本地信令 peerjs-server @ 9100
npm run demo:server   # 终端2：资源服务(9090) + peer "demo-node"
npm run demo:serve    # 终端3：静态服务 @ 5175
# 浏览器打开 http://localhost:5175/demo/vanilla.html
```

## 已踩的坑（代码注释处均有「发现背景」）

1. **peerjs 无 `exports` 字段**（CJS）：Node ESM named import 失败——`import pkg from 'peerjs'; const { Peer } = pkg` 或动态 import 后取 `.default`
2. **supports 检测在模块加载时缓存**：静态 import peerjs 先于 RTC 注入执行 → `browser-incompatible`——本包用动态 import 保证注入先行
3. **`isSecure()` 读 `location`**：Node 崩——`signaling.secure` 强制显式
4. **peerjs-server 0.2.9 无 HTTP `/id` 端点**（`_initializeHTTP` 被注释）：客户端不带 id 时 `retrieveId` 404——显式传 id
5. **peerjs-server `port: 0` 被 `\|\| 80` 吞掉**：随机端口需先探测空闲端口
6. **peerjs-server 返回对象无 `address()`/不转发 `listening`**：用 callback 参数拿内部 http server
7. **raw 模式无 chunker**：大块直接进 SCTP，块大小需 < 对端 maxMessageSize（取 64KB）+ 发送侧 bufferedAmount 背压

## 局限与后续

- 大文件整体驻留内存（Blob 方案）；后续可加 `cancel` 帧与流式分片（MediaSource）
- 无自动重连：连接断开后下次 load 会新建（当前槽位 closed 后重建）
- 浏览器端依赖公共信令可用性；生产建议自托管 peerjs-server（如 peerdrive 的 `peerserver`）