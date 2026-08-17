// core.js — 浏览器端核心：PeerMediaClient。
//
// 职责：维护到「Node 端 peer」的 DataConnection（按 peerId+信令配置缓存复用），
// 发送 url 请求帧、按 reqId 路由响应、把二进制块拼成 Blob 并产出 objectURL。
// 不依赖 React——vanilla 与 React 入口共用本模块。
//
// 并发模型：一条连接多路复用——多个组件同时 load 时每个请求独立 reqId，
// 二进制块归属「最近声明的 meta 帧」（与 peerdrive 后端连接级 expect 同语义，
// 见 doc/layers/L2-transport/conn.md）。Node 端按请求顺序逐块回数据，
// 一请求完成（done/err）后才处理下一个，因此块归属无歧义。
// peerjs 是 CJS 包（无 exports 字段）——Node 原生 ESM 无法静态解析 named
// export，必须 default import 后解构；vite 构建对 CJS 的 interop 同样走
// default（module.exports 对象），此写法两端兼容。
import peerjsPkg from 'peerjs'
const { Peer } = peerjsPkg
import {
  makeUrlRequest, parseFrame, isBinaryFrame, toUint8Array, nextReqId,
} from './protocol.js'

export const DEFAULT_SIGNALING = {
  host: '0.peerjs.com',
  port: 443,
  secure: true,
  key: 'peerjs',
  path: '/',
}

// signalingKey 连接缓存键：同信令配置 + 同 peerId 共享一条连接。
function signalingKey(sig) {
  return `${sig.host}:${sig.port}:${sig.key}:${sig.path || '/'}`
}

class ConnectionSlot {
  // 一条到对端 peer 的连接及其上的全部请求状态。
  constructor(peerId, signaling) {
    this.peerId = peerId
    this.signaling = signaling
    this.peer = null          // peerjs Peer（信令客户端）
    this.conn = null          // DataConnection（raw 序列化）
    this.ready = false
    this.closed = false
    this.queue = []           // 连接就绪前的 load 等待者（{url, resolve, reject}）
    this.pending = new Map()  // reqId → { resolve, reject, chunks, mime, size, current }
    this.curReqId = null      // 最近 meta 帧的 reqId（二进制块归属）
  }

  // request 在连接上发起一次加载。连接未就绪时排队，就绪后按序发送。
  // signal 中止时：从 pending 删除并 reject——迟到帧在 handleData 里因
  // pending 不存在被丢弃（数据块归属 `curReqId` 会被下一个 meta 覆盖，
  // 若当前请求正好是 curReqId 的归属者，其迟到块会挂到下一个请求上——
  // 见 handleData 的 curReqId 覆盖注释，Node 端逐请求串行所以实际不会错乱）。
  request(url, resolve, reject, signal) {
    const entry = { url, resolve, reject, signal }
    if (!this.ready || this.closed) {
      this.queue.push(entry)
      if (!this.closed) this.open()
      return
    }
    this.send(entry)
  }

  open() {
    const sig = this.signaling
    const peer = new Peer({
      host: sig.host, port: sig.port, secure: sig.secure, key: sig.key, path: sig.path,
      debug: 0,
    })
    this.peer = peer
    const timeout = setTimeout(() => {
      if (!this.ready && !this.closed) this.failAll('peerjs signaling timeout')
    }, 15000)
    peer.on('error', (err) => {
      clearTimeout(timeout)
      if (!this.ready && !this.closed) this.failAll(`peerjs error: ${err?.type || err}`)
    })
    peer.on('open', () => {
      // 主动连对端；失败时 DataConnection 侧 error 事件兜底
      const conn = peer.connect(this.peerId, {
        reliable: true,
        serialization: 'raw', // 必须 raw：文本/二进制帧可区分（见 protocol.js）
      })
      this.conn = conn
      conn.on('open', () => {
        clearTimeout(timeout)
        this.ready = true
        for (const q of this.queue.splice(0)) this.send(q)
      })
      conn.on('data', (data) => this.handleData(data))
      conn.on('close', () => this.teardown('connection closed'))
      conn.on('error', (err) => {
        if (!this.ready) this.failAll(`connection error: ${err?.type || err}`)
        this.teardown('connection error')
      })
    })
  }

  send(entry) {
    if (this.closed) { entry.reject(new Error('peerdrive-media: connection closed')); return }
    const reqId = nextReqId()
    const rec = {
      resolve: entry.resolve,
      reject: entry.reject,
      chunks: [],
      mime: null,
      size: 0,
      got: 0,
      cleanup: () => {}, // 默认 no-op；signal 存在时替换为移除 abort 监听
    }
    if (entry.signal) {
      if (entry.signal.aborted) {
        entry.reject(new DOMException('aborted', 'AbortError'))
        return
      }
      const onAbort = () => {
        // 中止：删除 pending（迟到帧被 handleData 丢弃）并 reject。
        this.pending.delete(reqId)
        entry.reject(new DOMException('aborted', 'AbortError'))
      }
      rec.cleanup = () => entry.signal.removeEventListener('abort', onAbort)
      entry.signal.addEventListener('abort', onAbort)
    }
    this.pending.set(reqId, rec)
    try {
      this.conn.send(makeUrlRequest(entry.url, reqId))
    } catch (err) {
      this.pending.delete(reqId)
      entry.reject(err)
    }
  }

  handleData(data) {
    if (isBinaryFrame(data)) {
      // 二进制块：属于最近 meta 帧的请求（Node 端逐请求串行回块）
      const p = this.curReqId ? this.pending.get(this.curReqId) : null
      if (!p) return // 无归属请求：丢弃（防御错序）
      const bytes = toUint8Array(data)
      p.chunks.push(bytes)
      p.got += bytes.length
      return
    }
    const msg = parseFrame(data)
    if (!msg) return
    const p = this.pending.get(msg.reqId)
    if (!p) return // 响应迟到（已 abort）：忽略
    switch (msg.type) {
      case 'meta':
        p.mime = msg.mime || 'application/octet-stream'
        p.size = msg.size || 0
        this.curReqId = msg.reqId
        if (msg.status >= 400) {
          this.pending.delete(msg.reqId)
          if (this.curReqId === msg.reqId) this.curReqId = null
          p.cleanup()
          p.reject(new Error(`peerdrive-media: upstream ${msg.status}`))
        }
        break
      case 'done':
        this.pending.delete(msg.reqId)
        if (this.curReqId === msg.reqId) this.curReqId = null
        const blob = new Blob(p.chunks, { type: p.mime })
        p.cleanup()
        p.resolve({ blob, blobUrl: URL.createObjectURL(blob), mime: p.mime, size: p.got })
        break
      case 'err':
        this.pending.delete(msg.reqId)
        if (this.curReqId === msg.reqId) this.curReqId = null
        p.cleanup()
        p.reject(new Error(`peerdrive-media: ${msg.msg || 'request failed'}`))
        break
      default:
        break
    }
  }

  failAll(msg) {
    // 连接级失败：reject 全部在途请求与排队等待者，然后清理槽位。
    this.closed = true
    const err = new Error(`peerdrive-media: ${msg}`)
    for (const [, p] of this.pending) { p.cleanup(); p.reject(err) }
    this.pending.clear()
    for (const q of this.queue.splice(0)) q.reject(err)
    this.curReqId = null
    this.closePeer()
  }

  teardown(msg) {
    // 连接关闭（对端断开/网络失败）：与 failAll 相同处理，槽位保持 closed。
    if (this.closed) return
    this.failAll(msg)
  }

  closePeer() {
    try { this.peer?.destroy() } catch { /* 幂等清理 */ }
    this.peer = null
    this.conn = null
  }
}

// PeerMediaClient 浏览器核心：模块级单例，React/vanilla 共用。
// slots 缓存 key = 信令配置 + peerId；同一对端的所有组件共享连接。
export class PeerMediaClient {
  constructor() {
    this.slots = new Map()
  }

  // load 加载 URL 资源，返回 { blob, blobUrl, mime, size }。
  // peer：Node 端 peer id（必填）；signaling：信令配置（默认公共云）；
  // signal：AbortSignal（组件卸载时取消——见 ConnectionSlot.send 的 abort 处理）。
  async load(url, { peer, signaling = DEFAULT_SIGNALING, signal } = {}) {
    if (!peer) throw new Error('peerdrive-media: peer (node peer id) is required')
    if (!url || typeof url !== 'string') throw new Error('peerdrive-media: url is required')
    const key = `${signalingKey(signaling)}|${peer}`
    let slot = this.slots.get(key)
    if (!slot) {
      slot = new ConnectionSlot(peer, signaling)
      this.slots.set(key, slot)
    }
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError')

    return new Promise((resolve, reject) => {
      slot.request(url, resolve, reject, signal)
    })
  }

  // dispose 主动释放到某 peer 的连接（组件全局卸载时调用；通常不需要）。
  dispose(peer, signaling = DEFAULT_SIGNALING) {
    const key = `${signalingKey(signaling)}|${peer}`
    const slot = this.slots.get(key)
    if (slot) {
      slot.failAll('disposed')
      this.slots.delete(key)
    }
  }
}

// client 模块级单例。
export const client = new PeerMediaClient()
export default client