// server.js — Node 端资源提供者：peerjs 节点 + fetch，把 URL 资源经
// WebRTC DataChannel 流式分块发给网页端（React/vanilla 组件）。
//
// 运行要求：
//   1. Node >= 22（原生 WebSocket/fetch；peerjs 信令依赖 WebSocket）
//   2. @roamhq/wrtc（本包 optionalDependencies）——peerjs 在 Node 无内置
//      WebRTC 实现，必须注入全局 RTC。安装失败时本模块 import 即报错。
//   3. signaling 必须显式传 secure —— peerjs 的 isSecure() 读 location，
//      Node 下会 ReferenceError（真实坑：自托管 host 且未传 secure 时崩）。
//
// 安全边界：allow(url) 白名单必须显式配置（默认全拒）——本节点是任意
// 网页端都能连的公共 peer，不设白名单等于开放任意 URL 抓取（SSRF）。
import wrtc from '@roamhq/wrtc'
import { CHUNK_SIZE, guessMime, parseFrame } from '../protocol.js'

// 注入全局 RTC：peerjs 直接用全局 RTCPeerConnection（bundler 里
// `new RTCPeerConnection(...)`），Node 没有内置实现。
// 必须在 peerjs 模块加载之前执行（见下方动态 import 原因）。
globalThis.RTCPeerConnection = wrtc.RTCPeerConnection
globalThis.RTCSessionDescription = wrtc.RTCSessionDescription
globalThis.RTCIceCandidate = wrtc.RTCIceCandidate

// peerjs 的 supports 检测（isWebRTCSupported 等）在**模块加载时**以 IIFE
// 一次性计算并缓存（util 单例）。ESM import 会 hoist——静态 import peerjs
// 必然先于本文件的注入代码执行，导致 supports 缓存为全 false（运行时
// 报 browser-incompatible，真实坑：E2E 首次跑通前踩中）。
// 因此必须动态 import：模块体先注入 RTC，再 await import('peerjs') 加载。
// 注意：若宿主程序在此之前已 import 过 peerjs（Node 无 RTC），其 supports
// 已缓存 false，本包无法补救——文档要求先引 peerdrive-media/node。
let Peer
async function loadPeerjs() {
  if (!Peer) {
    // CJS 包动态 import：named export 由 cjs-module-lexer 静态分析，peerjs
    // 的导出无法被识别——必须走 mod.default（module.exports 对象）
    const mod = await import('peerjs')
    Peer = mod.default.Peer
  }
  return Peer
}

// 自托管信令默认值（必须显式 secure=true，原因见文件头注释）。
export const DEFAULT_SIGNALING = {
  host: '0.peerjs.com',
  port: 443,
  secure: true,
  key: 'peerjs',
  path: '/',
}

// LOW_WATER 发送背压阈值：DataChannel 缓冲超阈值时暂停读 fetch 流。
// 为什么需要：peerjs raw 模式无 chunker、wrtc 的 send 不阻塞——无背压时
// 大文件会把全部块塞进 SCTP 缓冲，内存无界增长。
const LOW_WATER = 4 * 1024 * 1024

// keepalive 参数（与 core.js 同协议）：Node 端每 5s 发 ping 制造流量，
// 任何帧（含浏览器端 ping）刷新活跃；15s 无帧 → 主动 close 连接——
// 网页端崩溃/断网时 Node 端不悬挂（此前只能等 SCTP 超时，无 STUN 环境
// 可达数十秒）。发现背景：代码审阅 2026-08-18（第 4 项优化）。
const KEEPALIVE_INTERVAL = 5000
const KEEPALIVE_TIMEOUT = 15000

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// createPeerMediaServer 启动 Node 端资源提供者。
//
// options:
//   peerId    — 本节点在信令服务器上的 ID（网页端连这个 id）
//   signaling — 信令配置（默认公共云 0.peerjs.com）
//   allow     — URL 白名单 (url) => boolean，默认全拒，必须显式配置
//   fetchImpl — 自定义 fetch（测试注入；默认 globalThis.fetch）
//   chunkSize — 数据块大小（默认 CHUNK_SIZE 64KB）
//   onRequest — 日志钩子 ({url, peer, status, bytes, ms})
//
// 返回 { peer, close }。close() 销毁 peer（断连 + 清理）。
export async function createPeerMediaServer({
  peerId,
  signaling = DEFAULT_SIGNALING,
  allow = () => false,
  fetchImpl = globalThis.fetch,
  chunkSize = CHUNK_SIZE,
  onRequest,
} = {}) {
  if (!peerId) throw new Error('peerdrive-media: peerId is required')
  if (typeof signaling.secure !== 'boolean') {
    // Node 无 location，peerjs isSecure() 会 ReferenceError——强制显式
    // （注意不能用 !signaling.secure 判断：显式 secure:false 是合法值）
    throw new Error('peerdrive-media: signaling.secure must be explicitly set (true/false) in Node')
  }

  const PeerCtor = await loadPeerjs()

  const peer = new PeerCtor(peerId, {
    host: signaling.host,
    port: signaling.port,
    secure: signaling.secure,
    key: signaling.key,
    path: signaling.path || '/',
    // 诊断：PEERDRIVE_MEDIA_DEBUG=1 时输出 peerjs ICE/信令日志
    debug: process.env.PEERDRIVE_MEDIA_DEBUG ? 3 : 0,
  })

  await new Promise((resolve, reject) => {
    const to = setTimeout(() => reject(new Error(`peerdrive-media: signaling connect timeout (${signaling.host}:${signaling.port})`)), 15000)
    peer.once('open', () => { clearTimeout(to); resolve() })
    peer.once('error', (err) => { clearTimeout(to); reject(new Error(`peerdrive-media: signaling error: ${err?.type || err}`)) })
  })

  // serveConnection 处理一条网页端 DataConnection（raw 序列化）。
  // 逐请求串行：一次只服务一个 url 请求——二进制块无头部标识，靠
  // 「最近 meta」归属（与 peerdrive 后端连接级 expect 同语义）。若并发
  // 处理多请求，块会交叉错配，因此用 busy 标志拒绝并发请求。
  const serveConnection = (conn) => {
    let busy = false
    let lastActive = Date.now()
    // keepalive：发 ping 制造流量 + 超时主动断开（见文件头 KEEPALIVE 注释）
    const ka = setInterval(() => {
      if (Date.now() - lastActive > KEEPALIVE_TIMEOUT) {
        clearInterval(ka)
        try { conn.close() } catch { /* 幂等 */ }
        return
      }
      try { conn.send(JSON.stringify({ type: 'ping' })) } catch { /* 连接已死 */ }
    }, KEEPALIVE_INTERVAL)
    conn.on('data', (data) => {
      lastActive = Date.now() // 任何帧都刷新活跃（含浏览器端 ping）
      if (typeof data !== 'string') return // 二进制帧不应由网页端发出
      const msg = parseFrame(data)
      if (!msg || msg.type !== 'url') return
      if (busy) {
        conn.send(JSON.stringify({ type: 'err', msg: 'another request in flight', reqId: msg.reqId }))
        return
      }
      busy = true
      handleUrlRequest(conn, msg).finally(() => { busy = false })
    })
    conn.on('close', () => { clearInterval(ka); busy = false })
  }
  peer.on('connection', serveConnection)

  // handleUrlRequest 拉取 URL 并流式回发：meta → 块×N → done。
  async function handleUrlRequest(conn, msg) {
    const { url, reqId } = msg
    const t0 = Date.now()
    const sendErr = (m) => conn.send(JSON.stringify({ type: 'err', msg: m, reqId }))
    try {
      if (!url || typeof url !== 'string') { sendErr('invalid url'); return }
      if (!allow(url)) { sendErr('url not allowed'); return }

      const resp = await fetchImpl(url)
      if (!resp.ok) {
        sendErr(`upstream ${resp.status}`)
        return
      }
      const mime = guessMime(url, resp.headers.get('content-type'))
      const size = Number(resp.headers.get('content-length') || 0)
      conn.send(JSON.stringify({ type: 'meta', status: resp.status, mime, size, reqId }))

      let sent = 0
      if (resp.body) {
        // 流式读取 + 分块 + 背压：块直接 send（raw 模式无 chunker 分片）。
        // chunk 是 Uint8Array（Node fetch 流），wrtc send 接受 ArrayBufferView。
        for await (const chunk of resp.body) {
          const bytes = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk)
          for (let off = 0; off < bytes.length; off += chunkSize) {
            const piece = bytes.subarray(off, off + chunkSize)
            conn.send(piece)
            sent += piece.length
            while (conn.dataChannel && conn.dataChannel.bufferedAmount > LOW_WATER) {
              await sleep(10) // 背压：对端消费慢时暂停读流
            }
          }
        }
      }
      conn.send(JSON.stringify({ type: 'done', reqId }))
      onRequest?.({ url, peer: conn.peer, status: resp.status, bytes: sent, ms: Date.now() - t0 })
    } catch (err) {
      sendErr(`fetch failed: ${err.message}`)
    }
  }

  return {
    peerId,
    peer,
    close() {
      try { peer.destroy() } catch { /* 幂等 */ }
    },
  }
}

// allowHttp 便捷白名单：允许指定前缀的 http(s) URL。
export const allowPrefix = (prefixes) => (url) =>
  prefixes.some((p) => url.startsWith(p))

export default createPeerMediaServer