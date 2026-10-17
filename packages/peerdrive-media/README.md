# peerdrive-media

Loads URL resources from a Node.js side via **PeerJS signaling + WebRTC DataChannel**, and renders them as `<img>` / `<video>` in the web page. A standalone package accompanying peerdrive:

- **React components**: `<PeerImage>` / `<PeerVideo>` / `<PeerMedia>` (React 18+, framework-agnostic hook also usable separately)
- **Vanilla version**: IIFE/ESM build artifacts, reference directly via `<script>`
- **Node-side provider**: one function starts a peerjs node, `fetch` URL -> chunked streaming back

```
Browser (React / vanilla)
   │  peerjs DataConnection (serialization: 'raw')
   │  text frame = JSON header / binary frame = data block
   ▼
Node-side peer (createPeerMediaServer)
   │  fetch(URL)   <- allow() whitelist check
   ▼
Any HTTP resource (image / video / any file)
```

## Installation

```bash
# Preferred: GitHub standalone repo (dist checked in, use right after install, no build needed)
npm install github:Hana-ame/peerdrive-media

# Pinned version (tag kept in sync with package.json version)
npm install github:Hana-ame/peerdrive-media#v0.1.0

# Once published to npm registry: npm install peerdrive-media
```

> Why no `github:Hana-ame/peerdrive#path:packages/peerdrive-media`: npm does not support subdirectory syntax for git dependencies (only pnpm/yarn support `#path:`), hence the package is in its own repo.

React entry also needs `react` / `react-dom` (peerDependencies). Node-side usage needs **Node >= 22** and `@roamhq/wrtc` (this package's optionalDependencies; if install fails the Node side is unavailable).

Vanilla `<script>` no-install direct reference (jsDelivr CDN, needs internet):

```html
<script src="https://cdn.jsdelivr.net/gh/Hana-ame/peerdrive-media@v0.1.0/dist/vanilla/peerdrive-media.iife.js"></script>
```

## Quick start

### 1. Node side: resource provider

```js
// server.mjs
import { createPeerMediaServer, allowPrefix } from 'peerdrive-media/node'

const server = await createPeerMediaServer({
  peerId: 'my-node',                          // the web side connects to this id
  signaling: {                                // default public cloud 0.peerjs.com;
    host: '127.0.0.1',                        // self-hosted peerjs-server example
    port: 9100,
    secure: false,                            // ⚠️ Node side must pass this explicitly (see pitfall below)
    key: 'peerjs',
    path: '/',
  },
  // Security boundary: URL whitelist must be configured explicitly, default is all rejected
  allow: allowPrefix(['https://my-cdn.example.com/']),
  onRequest: ({ url, peer, status, bytes, ms }) =>
    console.log(`${peer} <- ${url} [${status}] ${bytes}B ${ms}ms`),
})
// server.close() to close
```

> **Pitfall (Node side)**: `signaling.secure` must be explicitly passed `true`/`false` -- peerjs's `isSecure()` reads the browser `location`, in Node it will ReferenceError.
> **Pitfall (load order)**: peerjs's WebRTC capability detection is cached once at module load; this package handles it internally (dynamic import). But do not import peerjs before `peerdrive-media/node`.

### 2. React components

```jsx
import { PeerMediaProvider, PeerImage, PeerVideo, PeerMedia } from 'peerdrive-media'

function App() {
  return (
    <PeerMediaProvider peer="my-node">
      <PeerImage url="https://my-cdn.example.com/a.png" />
      <PeerVideo url="https://my-cdn.example.com/b.mp4" controls autoPlay muted />
      <PeerMedia url="https://my-cdn.example.com/c.webp" /> {/* auto-pick img/video by MIME */}
    </PeerMediaProvider>
  )
}
```

- `peer` / `signaling` can also be passed per-component (higher priority than Provider)
- `loading` / `error` props can be given a custom ReactNode to override the three-state rendering
- Non-image/video MIME (like pdf) `<PeerMedia>` renders as a download link

#### hook form

```jsx
import { usePeerMedia } from 'peerdrive-media'

function MyImage({ url }) {
  const { status, src, error, reload } = usePeerMedia({ url, peer: 'my-node' })
  if (status === 'loading') return <span>loading…</span>
  if (status === 'error') return <button onClick={reload}>Retry: {error.message}</button>
  return <img src={src} />
}
```

### 3. Vanilla (`<script>` direct reference)

```html
<script src="node_modules/peerdrive-media/dist/vanilla/peerdrive-media.iife.js"></script>
<script>
  PeerMedia.mount({ peer: 'my-node', url: 'https://my-cdn.example.com/a.png' })
    .then(({ node, blobUrl, mime }) => console.log('mounted', mime))
</script>
```

ESM version: `import { load, mount } from 'peerdrive-media/vanilla'` (`dist/vanilla/peerdrive-media.es.js`).

## API

### Frame protocol (custom, raw serialization)

| Direction | Frame | Description |
|---|---|---|
| web -> node | `{"type":"url","url":"…","reqId":"…"}` | text frame (JSON) |
| node -> web | `{"type":"meta","status":200,"mime":"image/png","size":N,"reqId"}` | text frame |
| node -> web | binary frame ×N (64KB/block, streaming) | belongs to the most recent meta |
| node -> web | `{"type":"done","reqId"}` / `{"type":"err","msg":"…","reqId"}` | text frame to wrap up |

`serialization: 'raw'` is the protocol foundation: string goes raw over a text frame (SCTP PPID 51), ArrayBuffer over a binary frame (PPID 53), both ends can distinguish "control header vs data block" -- consistent with peerdrive's Go-side frame semantics.

### Browser core

| API | Description |
|---|---|
| `client.load(url, { peer, signaling, signal })` | -> `Promise<{ blob, blobUrl, mime, size }>`; `blobUrl` needs `URL.revokeObjectURL()` after use |
| `client.dispose(peer, signaling)` | Manually release the connection (usually not needed: connections are cached and reused by peerId+signaling) |
| `DEFAULT_SIGNALING` | Public cloud `0.peerjs.com:443` |

Multiple components loading the same peer share one DataConnection (reqId multiplexed), a connection-level failure rejects all in-flight requests.

### Node side

| API | Description |
|---|---|
| `createPeerMediaServer({ peerId, signaling, allow, fetchImpl, chunkSize, onRequest })` | -> `{ peerId, peer, close() }` |
| `allowPrefix(prefixes)` | Convenient whitelist: URL starts with any prefix |

- `fetchImpl` can be injected (test/proxy scenarios); default `globalThis.fetch`
- Concurrency: one connection serves one request at a time (binary blocks have no header marker, attributed to "most recent meta"); concurrent requests return `err` frame
- Streaming + backpressure: pause reading upstream when `bufferedAmount > 4MB`

## Build

```bash
npm run build    # dist/react (ESM) + dist/vanilla (IIFE + ESM)
npm test         # protocol unit tests + dual-end E2E (local signaling, runs offline)
```

## Demo

```bash
npm run demo:signal   # terminal 1: local signaling peerjs-server @ 9100
npm run demo:server   # terminal 2: resource service (9090) + peer "demo-node"
npm run demo:serve    # terminal 3: static service @ 5175
# open http://localhost:5175/demo/vanilla.html in browser
```

## Pitfalls stepped on (code comments have "discovery background")

1. **peerjs has no `exports` field** (CJS): Node ESM named import fails -- `import pkg from 'peerjs'; const { Peer } = pkg` or dynamic import then take `.default`
2. **supports detection cached at module load**: static import peerjs runs before RTC injection -> `browser-incompatible` -- this package uses dynamic import to ensure injection comes first
3. **`isSecure()` reads `location`**: Node crashes -- `signaling.secure` forced explicit
4. **peerjs-server 0.2.9 has no HTTP `/id` endpoint** (`_initializeHTTP` commented out): client without id gets `retrieveId` 404 -- pass id explicitly
5. **peerjs-server `port: 0` swallowed by `\|\| 80`**: random port needs a free-port probe first
6. **peerjs-server returned object has no `address()` / does not forward `listening`**: use callback param to get the internal http server
7. **raw mode has no chunker**: large blocks go directly into SCTP, block size must be < peer's maxMessageSize (take 64KB) + sender-side bufferedAmount backpressure

## Limitations and next steps

- Large files reside entirely in memory (Blob approach); later can add `cancel` frame and streaming chunks (MediaSource)
- No auto-reconnect: after a connection drops, next load creates a new one (current slot rebuilds after closed)
- Browser side depends on public signaling availability; production recommends self-hosting peerjs-server (like peerdrive's `peerserver`)
