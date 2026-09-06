// core.js — 浏览器端核心：PeerMediaClient。
//
// 职责：维护到「Node 端 peer」的 PeerJS 连接，管理多条 DataChannel：
//   - 控制通道（control）：keepalive ping/ping-ack
//   - 文件通道（file-{reqId}）：每个文件请求一条独立通道，支持并发传输
//
// 多 DataChannel 架构（2026-09-06）：
//   - 一条 PeerJS 连接 → 多条 DataChannel
//   - 控制通道：keepalive，不发文件请求
//   - 文件通道：每个文件请求创建一条新通道，Node 端独立处理
//   - 并发：多个文件可同时传输（每条通道独立）
//
// 不依赖 React——vanilla 与 React 入口共用本模块。
//
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

// keepalive 参数：无 STUN 环境下 WebRTC 断线无 close 事件（连接级 error
// 在 ready 后被忽略，见 open() 注释），真断线要等 SCTP 超时（数十秒）。
// 两端（浏览器/Node）每 KEEPALIVE_INTERVAL 发一次 ping 帧制造流量；
// 收到**任何**帧（含 ping）刷新 lastActive；超过 KEEPALIVE_TIMEOUT 无帧
// → teardown（槽位 closed，下次 load 重建，失败感知从数十秒降到秒级）。
// 发现背景：代码审阅 2026-08-18（第 4 项优化）。
export const KEEPALIVE_INTERVAL = 5000
export const KEEPALIVE_TIMEOUT = 15000

// signalingKey 连接缓存键：同信令配置 + 同 peerId 共享一条连接。
function signalingKey(sig) {
  return `${sig.host}:${sig.port}:${sig.key}:${sig.path || '/'}`
}

class ConnectionSlot {
  // 一条到对端 peer 的连接及其上的全部 DataChannel。
  constructor(peerId, signaling) {
    this.peerId = peerId
    this.signaling = signaling
    this.peer = null          // peerjs Peer（信令客户端）
    this.controlConn = null   // 控制通道（keepalive）
    this.ready = false
    this.closed = false
    this.opening = false
    this.pending = new Map()  // reqId → { resolve, reject, chunks, mime, size, got, conn, cleanup }
    this.lastActive = 0       // 最近收到帧的时间（keepalive 判活）
    this.kaTimer = null       // keepalive 定时器（controlConn open 后启动）
  }

  // request 在连接上发起一次加载。创建新的文件 DataChannel 并发传输。
  request(url, resolve, reject, signal) {
    if (this.closed) {
      reject(new Error('peerdrive-media: connection closed'))
      return
    }
    // 连接未就绪时先打开，ready 后发送请求
    if (!this.ready) {
      if (!this.opening) {
        this.opening = true
        this.open()
      }
      // 排队等待连接就绪
      this._waitingForReady = this._waitingForReady || []
      const entry = { url, resolve, reject, signal }
      this._waitingForReady.push(entry)
      // 排队中 abort：立即从等待列表移除并 reject
      if (signal) {
        if (signal.aborted) {
          const idx = this._waitingForReady.findIndex(e => e === entry)
          if (idx >= 0) this._waitingForReady.splice(idx, 1)
          reject(new DOMException('aborted', 'AbortError'))
          return
        }
        const onAbort = () => {
          // 防御：_waitingForReady 可能已被清空（open 后处理完）
          if (this._waitingForReady) {
            const idx = this._waitingForReady.findIndex(e => e === entry)
            if (idx >= 0) {
              this._waitingForReady.splice(idx, 1)
              reject(new DOMException('aborted', 'AbortError'))
            }
          }
          signal.removeEventListener('abort', onAbort)
        }
        entry._onAbort = onAbort
        signal.addEventListener('abort', onAbort)
      }
      return
    }
    this.sendFileRequest(url, resolve, reject, signal)
  }

  // sendFileRequest 创建新的文件 DataChannel 并发送请求。
  sendFileRequest(url, resolve, reject, signal) {
    const reqId = nextReqId()
    // 创建新的文件通道
    const conn = this.peer.connect(this.peerId, {
      reliable: true,
      serialization: 'raw',
      label: `file-${reqId}`,
    })

    const rec = {
      resolve,
      reject,
      chunks: [],
      mime: null,
      size: 0,
      got: 0,
      conn,
      cleanup: () => {},
    }

    if (signal) {
      if (signal.aborted) {
        conn.close()
        reject(new DOMException('aborted', 'AbortError'))
        return
      }
      const onAbort = () => {
        this.pending.delete(reqId)
        rec.cleanup()
        try { conn.close() } catch { /* 幂等 */ }
        reject(new DOMException('aborted', 'AbortError'))
      }
      rec.cleanup = () => signal.removeEventListener('abort', onAbort)
      signal.addEventListener('abort', onAbort)
    }

    this.pending.set(reqId, rec)

    conn.on('open', () => {
      // 发送 url 请求
      try {
        conn.send(makeUrlRequest(url, reqId))
      } catch (err) {
        this.pending.delete(reqId)
        rec.cleanup()
        reject(err)
      }
    })
    conn.on('data', (data) => this.handleFileData(reqId, data))
    conn.on('close', () => {
      // 通道关闭：如果在途请求，reject
      const p = this.pending.get(reqId)
      if (p) {
        this.pending.delete(reqId)
        p.cleanup()
        p.reject(new Error('peerdrive-media: file channel closed'))
      }
    })
    conn.on('error', (err) => {
      const p = this.pending.get(reqId)
      if (p) {
        this.pending.delete(reqId)
        p.cleanup()
        p.reject(new Error(`peerdrive-media: file channel error: ${err?.type || err}`))
      }
    })
  }

  // open 建立到 Node 端 peer 的完整链路（Peer 信令 + 控制 DataChannel）。
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
      // 创建控制通道（只用于 keepalive）
      const conn = peer.connect(this.peerId, {
        reliable: true,
        serialization: 'raw',
        label: 'control',
      })
      this.controlConn = conn
      conn.on('open', () => {
        clearTimeout(timeout)
        this.opening = false
        this.ready = true
        this.startKeepalive()
        // 处理排队等待的连接就绪请求
        if (this._waitingForReady && this._waitingForReady.length) {
          const entries = this._waitingForReady
          this._waitingForReady = null
          for (const entry of entries) {
            // 移除 abort 监听（已发送，不再需要排队中止）
            if (entry.signal) {
              entry.signal.removeEventListener('abort', entry._onAbort)
            }
            this.sendFileRequest(entry.url, entry.resolve, entry.reject, entry.signal)
          }
        }
      })
      conn.on('data', (data) => this.handleControlData(data))
      conn.on('close', () => this.teardown('control channel closed'))
      conn.on('error', (err) => {
        if (!this.ready && !this.closed) this.failAll(`control channel error: ${err?.type || err}`)
      })
    })
  }

  // handleControlData 处理控制通道数据（只接收 ping-ack）。
  handleControlData(data) {
    this.lastActive = Date.now()
    if (typeof data === 'string') {
      const msg = parseFrame(data)
      if (msg?.type === 'ping-ack') return // keepalive 响应
    }
  }

  // handleFileData 处理文件通道数据。
  handleFileData(reqId, data) {
    this.lastActive = Date.now()
    const p = this.pending.get(reqId)
    if (!p) return

    if (isBinaryFrame(data)) {
      // 二进制块
      const bytes = toUint8Array(data)
      p.chunks.push(bytes)
      p.got += bytes.length
      return
    }

    const msg = parseFrame(data)
    if (!msg) return

    switch (msg.type) {
      case 'meta':
        p.mime = msg.mime || 'application/octet-stream'
        p.size = msg.size || 0
        if (msg.status >= 400) {
          this.pending.delete(reqId)
          p.cleanup()
          try { p.conn.close() } catch { /* 幂等 */ }
          p.reject(new Error(`peerdrive-media: upstream ${msg.status}`))
        }
        break
      case 'done':
        this.pending.delete(reqId)
        const blob = new Blob(p.chunks, { type: p.mime })
        p.cleanup()
        try { p.conn.close() } catch { /* 幂等 */ }
        p.resolve({ blob, blobUrl: URL.createObjectURL(blob), mime: p.mime, size: p.got })
        break
      case 'err':
        this.pending.delete(reqId)
        p.cleanup()
        try { p.conn.close() } catch { /* 幂等 */ }
        p.reject(new Error(`peerdrive-media: ${msg.msg || 'request failed'}`))
        break
      case 'ping':
        // Node 端发来的 ping（keepalive），回复 ping-ack
        try { p.conn.send(JSON.stringify({ type: 'ping-ack' })) } catch { /* 通道已死 */ }
        break
      default:
        break
    }
  }

  // startKeepalive 启动断线感知定时器（controlConn open 后调用）。
  startKeepalive() {
    this.lastActive = Date.now()
    this.kaTimer = setInterval(() => {
      if (this.closed) { clearInterval(this.kaTimer); return }
      if (Date.now() - this.lastActive > KEEPALIVE_TIMEOUT) {
        clearInterval(this.kaTimer)
        this.teardown('keepalive timeout')
        return
      }
      try {
        if (this.controlConn) {
          this.controlConn.send(JSON.stringify({ type: 'ping' }))
        }
      } catch { /* 连接已死，超时兜底 */ }
    }, KEEPALIVE_INTERVAL)
  }

  failAll(msg) {
    // 连接级失败：reject 全部在途请求与排队等待者，然后清理槽位。
    this.closed = true
    this.opening = false
    if (this.kaTimer) { clearInterval(this.kaTimer); this.kaTimer = null }
    const err = new Error(`peerdrive-media: ${msg}`)
    for (const [, p] of this.pending) {
      p.cleanup()
      try { p.conn?.close() } catch { /* 幂等 */ }
      p.reject(err)
    }
    this.pending.clear()
    if (this._waitingForReady) {
      for (const q of this._waitingForReady) q.reject(err)
      this._waitingForReady = null
    }
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
    this.controlConn = null
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
  // signal：AbortSignal（组件卸载时取消——见 ConnectionSlot.request 的 abort 处理）。
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
