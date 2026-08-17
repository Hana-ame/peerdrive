// e2e.test.mjs — 双端 E2E：本地 peerjs-server 信令 + Node provider + 模拟
// 浏览器端 peer（peerjs raw 连接）。验证完整链路：信令 → WebRTC 建链 →
// url 帧 → fetch → 分块流 → 收齐 Blob。完全离线（127.0.0.1），不依赖公网。
//
// 发现背景：peerjs 在 Node 端无内置 WebRTC——@roamhq/wrtc 全局注入必须在
// import peerjs 之前（Peer 构造时 supports() 会实例化测试 PC）。
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import http from 'node:http'
import { once } from 'node:events'
import wrtc from '@roamhq/wrtc'
import { PeerServer } from 'peerjs-server'

globalThis.RTCPeerConnection = wrtc.RTCPeerConnection
globalThis.RTCSessionDescription = wrtc.RTCSessionDescription
globalThis.RTCIceCandidate = wrtc.RTCIceCandidate

// 与 src/node/server.js 同款坑：peerjs 的 supports 在模块加载时 IIFE 缓存，
// 静态 import 会先于注入执行 → browser-incompatible。必须动态 import。
// CJS 包：named export 不可用，走 mod.default。这里放模块体顶层 await
// （注入之后），模拟浏览器端 peer。
const { Peer } = (await import('peerjs')).default
import { createPeerMediaServer } from '../src/node/server.js'

// —— 本地资源服务器：生成确定性内容 + hash ——
function makeBytes(n, seed) {
  const b = new Uint8Array(n)
  for (let i = 0; i < n; i++) b[i] = (seed * 31 + i * 7) & 0xff
  return b
}
const PNG_1x1 = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)
const SMALL = makeBytes(128 * 1024, 3)      // 2 块
const LARGE = makeBytes(3 * 1024 * 1024, 7) // 48 块，验证流式+背压
const hashOf = (b) => createHash('sha256').update(b).digest('hex')

let httpServer, signalServer, provider, signalHttp
let httpPort, signalPort

before(async () => {
  // 资源服务器
  httpServer = http.createServer((req, res) => {
    const u = new URL(req.url, 'http://x')
    if (u.pathname === '/img.png') {
      res.writeHead(200, { 'content-type': 'image/png' })
      res.end(PNG_1x1)
    } else if (u.pathname === '/small.bin') {
      res.writeHead(200, { 'content-type': 'application/octet-stream', 'content-length': SMALL.length })
      res.end(SMALL)
    } else if (u.pathname === '/large.bin') {
      // 无 content-length：验证流式分块按读取块走
      res.writeHead(200, { 'content-type': 'application/octet-stream' })
      res.end(LARGE)
    } else if (u.pathname === '/video.mp4') {
      res.writeHead(200, { 'content-type': 'video/mp4' })
      res.end(LARGE) // 假视频内容，仅验证 MIME 分派
    } else if (u.pathname === '/missing') {
      res.writeHead(404)
      res.end('nope')
    } else {
      res.writeHead(500)
      res.end('boom')
    }
  })
  httpServer.listen(0, '127.0.0.1')
  await once(httpServer, 'listening')
  httpPort = httpServer.address().port

  // 本地信令（peerjs-server）。
  // 坑1：PeerServer({port}) 的 `port = options.port || 80`——传 0 会被当
  // falsy 吞掉去 listen 80（EACCES）。必须先探测空闲端口再传。
  // 坑2：返回对象无 address() 且不转发 'listening' 事件——用 callback
  // 参数拿内部 http server 取端口。
  // 坑3：本机 9000-9002 被其他服务（librefs/s3proxy）占用，必须随机端口。
  const freePort = await new Promise((resolve) => {
    const probe = http.createServer()
    probe.listen(0, '127.0.0.1', () => {
      const p = probe.address().port
      probe.close(() => resolve(p))
    })
  })
  const { wsServer: signalWs, httpServer: signalHttp2 } = await new Promise((resolve) => {
    const ws = PeerServer({ port: freePort, host: '127.0.0.1', path: '/' }, (h) => resolve({ wsServer: ws, httpServer: h }))
  })
  signalServer = signalWs
  signalHttp = signalHttp2
  signalPort = signalHttp.address().port

  // Node 资源提供者
  provider = await createPeerMediaServer({
    peerId: 'e2e-node',
    signaling: { host: '127.0.0.1', port: signalPort, secure: false, key: 'peerjs', path: '/' },
    allow: (url) => url.startsWith(`http://127.0.0.1:${httpPort}/`),
    chunkSize: 64 * 1024,
  })
})

after(async () => {
  provider?.close()
  // signalServer 是 express app（无 close），关底层 http server 即可
  signalHttp?.close()
  httpServer?.close()
})

// —— 模拟浏览器端：peerjs raw 连接 + 帧收集 ——
async function connectClient() {
  // 必须显式 id：peerjs-server 0.2.9 注释掉了 _initializeHTTP（无 GET
  // /:key/id 端点），客户端不传 id 时 retrieveId 走 HTTP → 404 → ServerError。
  // 传 id 后直接走 WS 注册（OPEN 消息），跳过 HTTP。
  const peer = new Peer(`client-${Math.random().toString(36).slice(2, 10)}`, {
    host: '127.0.0.1', port: signalPort, secure: false, key: 'peerjs', path: '/',
    debug: 0,
  })
  await new Promise((res, rej) => { peer.once('open', res); peer.once('error', rej) })
  const conn = peer.connect('e2e-node', { reliable: true, serialization: 'raw' })
  await new Promise((res, rej) => { conn.once('open', res); conn.once('error', rej) })
  return { peer, conn }
}

// requestOnce 发一个 url 请求，收集响应：{ status, mime, bytes: Buffer, error }
function requestOnce(conn, url) {
  return new Promise((resolve) => {
    let meta = null
    const chunks = []
    const onData = (data) => {
      if (typeof data === 'string') {
        const msg = JSON.parse(data)
        if (msg.type === 'meta') { meta = msg; return }
        if (msg.type === 'done') {
          conn.off('data', onData)
          resolve({ status: meta.status, mime: meta.mime, bytes: Buffer.concat(chunks), error: null })
          return
        }
        if (msg.type === 'err') {
          conn.off('data', onData)
          resolve({ error: msg.msg, status: null, mime: null, bytes: null })
          return
        }
        return
      }
      chunks.push(Buffer.from(data))
    }
    conn.on('data', onData)
    conn.send(JSON.stringify({ type: 'url', url, reqId: 't' + Math.random().toString(36).slice(2) }))
  })
}

test('E2E: 加载小 PNG（1 块）', async () => {
  const { peer, conn } = await connectClient()
  const r = await requestOnce(conn, `http://127.0.0.1:${httpPort}/img.png`)
  assert.equal(r.error, null)
  assert.equal(r.status, 200)
  assert.equal(r.mime, 'image/png')
  assert.ok(PNG_1x1.equals(r.bytes), '内容一致')
  peer.destroy()
})

test('E2E: 多块文件（128KB = 2 块）', async () => {
  const { peer, conn } = await connectClient()
  const r = await requestOnce(conn, `http://127.0.0.1:${httpPort}/small.bin`)
  assert.equal(r.error, null)
  assert.equal(r.bytes.length, SMALL.length)
  assert.equal(hashOf(r.bytes), hashOf(SMALL), '分块拼接后 hash 一致')
  peer.destroy()
})

test('E2E: 大文件流式（3MB，无 content-length，验证背压不丢块）', async () => {
  const { peer, conn } = await connectClient()
  const r = await requestOnce(conn, `http://127.0.0.1:${httpPort}/large.bin`)
  assert.equal(r.error, null)
  assert.equal(r.bytes.length, LARGE.length)
  assert.equal(hashOf(r.bytes), hashOf(LARGE), '流式分块+背压后字节完整')
  peer.destroy()
})

test('E2E: video MIME 分派', async () => {
  const { peer, conn } = await connectClient()
  const r = await requestOnce(conn, `http://127.0.0.1:${httpPort}/video.mp4`)
  assert.equal(r.mime, 'video/mp4')
  peer.destroy()
})

test('E2E: 上游 404 → err 帧', async () => {
  const { peer, conn } = await connectClient()
  const r = await requestOnce(conn, `http://127.0.0.1:${httpPort}/missing`)
  assert.match(r.error, /404/)
  peer.destroy()
})

test('E2E: 白名单拒绝 → err 帧', async () => {
  const { peer, conn } = await connectClient()
  const r = await requestOnce(conn, 'http://evil.example.com/secret.png')
  assert.match(r.error, /not allowed/)
  peer.destroy()
})

test('E2E: 连接串行复用（同一连接两请求）', async () => {
  const { peer, conn } = await connectClient()
  const r1 = await requestOnce(conn, `http://127.0.0.1:${httpPort}/img.png`)
  assert.equal(r1.error, null)
  const r2 = await requestOnce(conn, `http://127.0.0.1:${httpPort}/small.bin`)
  assert.equal(r2.error, null)
  assert.equal(r2.bytes.length, SMALL.length)
  peer.destroy()
})