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
// export，必须 default import 后解构。但坑：peerjs 的 main(bundler.cjs) 与
// module(bundler.mjs) 两个构建 default export 语义不同——Node 解析 CJS 时
// default = module.exports（含 Peer）；vite 构建解析 ESM 时 default 是内部
// util 对象（无 Peer），Peer 只挂在命名导出上。实测（2026-08-18 浏览器 E2E
// 暴露）：IIFE 里 Peer 解构出 undefined → "yt is not a constructor"。
// 三路兜底：namespace 命名导出（vite/ESM）→ CJS default（Node）→ default.default。
import peerjsPkg from 'peerjs'
import * as peerjsNS from 'peerjs'
const Peer =
  peerjsNS.Peer || peerjsPkg.Peer || (peerjsPkg.default && peerjsPkg.default.Peer)
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
    // 两个条件都排队：(1) 连接未就绪；(2) 已有在途请求——Node 端协议是
    // 连接级串行（一次只服务一个 url 请求，并发回 err 'another request in
    // flight'，见 server.js busy 标志），客户端必须自排队等 done/err 再发。
    // 浏览器 E2E 2026-08-18 暴露：4 个组件并发 load → 3 个被 Node 拒绝。
    if (!this.ready || this.closed || this.pending.size > 0) {
      this.queue.push(entry)
      // 守卫 opening：并发多个请求排队时只建一次连接（第一个请求触发
      // open，其余等待 ready 后统一 flush）——否则 N 个请求 = N 个 Peer
      // 实例，Node 端收到 N 条 DataConnection（浏览器 E2E 2026-08-18 暴露：
      // 4 个 box 建了 4 条连接，且竞态下请求乱发）。
      if (!this.opening && !this.closed) {
        this.opening = true
        this.open()
      }
      return
    }
    this.send(entry)
  }

  // flush 按序发送排队请求；有在途请求时停下（串行协议）。
  flush() {
    if (!this.ready || this.closed) return
    while (this.queue.length && this.pending.size === 0) {
      this.send(this.queue.shift())
    }
  }

  // open 建立到 Node 端 peer 的完整链路（Peer 信令 + DataConnection）。
  // 必须传显式随机 id：peerjs-server 0.2.9 无 HTTP GET /:key/id（retrieveId
  // 404 → ServerError）；带 id 直接走 WS 注册。随机前缀避免与信令上其他
  // 节点撞 id（浏览器 E2E 2026-08-18 暴露，见 test/e2e.test.mjs connectClient）。
  // debug 可经全局 __PDM_DEBUG 提到 3（输出 ICE/信令日志，排查连接问题）。
  // config：默认空 iceServers——本包典型场景是浏览器↔Node 本地/内网互联，
  // host candidate 即可连通；peerjs 默认 STUN(stun.l.google.com) 在无外网
  // UDP 环境（如 WSL 直连/企业网）会卡 gathering 导致 ICE 永远连不上
  // （2026-08-18 浏览器 E2E 暴露）。需要公网穿透时经 signaling.config 传入。
  open() {
    const sig = this.signaling
    const dbg = typeof window !== 'undefined' && window.__PDM_DEBUG ? 3 : 0
    const peer = new Peer(`pd-b-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`, {
      host: sig.host, port: sig.port, secure: sig.secure, key: sig.key, path: sig.path,
      config: sig.config || { iceServers: [] },
      debug: dbg,
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
        this.opening = false
        this.ready = true
        this.flush()
      })
      conn.on('data', (data) => this.handleData(data))
      conn.on('close', () => this.teardown('connection closed'))
      conn.on('error', (err) => {
        // 只处理「未就绪」时的连接错误；ready 后连接级 error 多为瞬态
        //（典型：DC open 瞬间 flush 队列触发 peerjs NotOpenYet 竞态），
        // 若此处无条件 teardown 会把刚建立的连接拆掉——浏览器 E2E
        // 2026-08-18 实测：Firefox 端 DC open 后立即 error → teardown →
        // destroy → 'connection closed'，实际 ICE/DC 均已连通。
        // 真断线由 close 事件兜底。
        if (!this.ready && !this.closed) this.failAll(`connection error: ${err?.type || err}`)
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
        this.flush() // 串行协议：上一个完成，发下一个排队请求
        break
      case 'err':
        this.pending.delete(msg.reqId)
        if (this.curReqId === msg.reqId) this.curReqId = null
        p.cleanup()
        p.reject(new Error(`peerdrive-media: ${msg.msg || 'request failed'}`))
        this.flush()
        break
      default:
        break
    }
  }

  failAll(msg) {
    // 连接级失败：reject 全部在途请求与排队等待者，然后清理槽位。
    this.closed = true
    this.opening = false
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
    // 断线后重建：teardown/failAll 置 closed=true 但槽位仍在缓存中，
    // 若沿用 closed 槽位，request() 的「closed 不再 open()」守卫会让新请求
    // 永久排队、Promise 永不 settle（加载中无错误）。
    // 发现背景：代码审阅 2026-08-18（Node 端重启/断网后页面恢复场景）。
    if (!slot || slot.closed) {
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