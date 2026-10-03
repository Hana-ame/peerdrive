// core.js — browser-side core: PeerMediaClient.
//
// Responsibilities: maintain PeerJS connection to "Node-side peer", manage multiple DataChannels:
//   - Control channel (control): keepalive ping/ping-ack
//   - File channel (file-{reqId}): one independent channel per file request, supports concurrent transfer
//
// Multi-DataChannel architecture (2026-09-06):
//   - One PeerJS connection → multiple DataChannels
//   - Control channel: keepalive, no file requests sent
//   - File channel: each file request creates a new channel, Node side handles independently
//   - Concurrency: multiple files can transfer simultaneously (each channel independent)
//
// Does not depend on React — vanilla and React entry points share this module.
//
// peerjs is a CJS package (no exports field) — Node native ESM cannot statically resolve named
// exports, must default import then destructure. But caveat: peerjs's main(bundler.cjs) and
// module(bundler.mjs) builds have different default export semantics — Node resolves CJS as
// default = module.exports (includes Peer); vite builds resolve ESM as default being internal
// util object (no Peer), Peer is only on named exports. Empirical test (2026-08-18 browser E2E
// exposed): in IIFE, Peer destructured as undefined → "yt is not a constructor".
// Three-way fallback: namespace named export (vite/ESM) → CJS default (Node) → default.default.
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

// keepalive parameters: in no-STUN environments, WebRTC disconnection has no close event (connection-level
// error ignored after ready, see open() comments), real disconnection must wait for SCTP timeout (tens of seconds).
// Both ends (browser/Node) send a ping frame every KEEPALIVE_INTERVAL to generate traffic;
// receiving **any** frame (including ping) refreshes lastActive; exceeding KEEPALIVE_TIMEOUT without frames
// → teardown (slot closed, rebuilt on next load, failure detection reduced from tens of seconds to seconds).
// Discovery: code review 2026-08-18 (item 4 optimization).
export const KEEPALIVE_INTERVAL = 5000
export const KEEPALIVE_TIMEOUT = 15000

// signalingKey connection cache key: same signaling config + same peerId shares one connection.
function signalingKey(sig) {
  return `${sig.host}:${sig.port}:${sig.key}:${sig.path || '/'}`
}

class ConnectionSlot {
  // A connection to the peer and all its DataChannels.
  constructor(peerId, signaling) {
    this.peerId = peerId
    this.signaling = signaling
    this.peer = null          // peerjs Peer (signaling client)
    this.controlConn = null   // control channel (keepalive)
    this.ready = false
    this.closed = false
    this.opening = false
    this.pending = new Map()  // reqId → { resolve, reject, chunks, mime, size, got, conn, cleanup }
    this.lastActive = 0       // most recent time a frame was received (keepalive liveness)
    this.kaTimer = null       // keepalive timer (starts after controlConn opens)
    // Channel pool: reuse established DataChannels, pre-warm to reduce first-request latency
    this.pool = []            // idle channel list
    this.inUse = new Set()    // channels in use
    this.poolSize = 2         // pre-warm count
  }

  // request initiates a load on the connection. Creates a new file DataChannel for concurrent transfer.
  request(url, resolve, reject, signal) {
    if (this.closed) {
      reject(new Error('peerdrive-media: connection closed'))
      return
    }
    // Open first if not ready, send request after ready
    if (!this.ready) {
      if (!this.opening) {
        this.opening = true
        this.open()
      }
      // Queue waiting for connection readiness
      this._waitingForReady = this._waitingForReady || []
      const entry = { url, resolve, reject, signal }
      this._waitingForReady.push(entry)
      // Abort while queued: immediately remove from wait list and reject
      if (signal) {
        if (signal.aborted) {
          const idx = this._waitingForReady.findIndex(e => e === entry)
          if (idx >= 0) this._waitingForReady.splice(idx, 1)
          reject(new DOMException('aborted', 'AbortError'))
          return
        }
        const onAbort = () => {
          // Defensive: _waitingForReady may have been cleared (processed after open)
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

  // sendFileRequest gets a DataChannel from the pool (or creates a new one) and sends the request.
  sendFileRequest(url, resolve, reject, signal) {
    const reqId = nextReqId()
    // Get channel from pool, create new if none available
    let conn
    let fromPool = false
    if (this.pool.length > 0) {
      conn = this.pool.shift()  // reuse idle channel
      fromPool = true
    } else {
      // Create new channel
      conn = this.peer.connect(this.peerId, {
        reliable: true,
        serialization: 'raw',
        label: `file-${reqId}`,
      })
    }
    this.inUse.add(conn)

    const rec = {
      resolve,
      reject,
      chunks: [],
      mime: null,
      size: 0,
      got: 0,
      conn,
      _listeners: {},  // save listener references for cleanup
      cleanup: () => {
        this.inUse.delete(conn)
        // Remove listeners
        if (rec._listeners.data) {
          conn.removeListener('data', rec._listeners.data)
        }
        if (rec._listeners.close) {
          conn.removeListener('close', rec._listeners.close)
        }
        if (rec._listeners.error) {
          conn.removeListener('error', rec._listeners.error)
        }
        if (rec._listeners.open) {
          conn.removeListener('open', rec._listeners.open)
        }
        // Return channel to pool (if not closed)
        if (!conn.closed) {
          this.pool.push(conn)
        }
      },
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
        try { conn.close() } catch { /* idempotent */ }
        reject(new DOMException('aborted', 'AbortError'))
      }
      signal.addEventListener('abort', onAbort)
      rec._onAbort = onAbort
    }

    this.pending.set(reqId, rec)

    // If from pool, already open, send request directly
    if (fromPool) {
      try {
        conn.send(makeUrlRequest(url, reqId))
      } catch (err) {
        this.pending.delete(reqId)
        rec.cleanup()
        reject(err)
      }
    } else {
      // New channel, wait for open
      const onOpen = () => {
        try {
          conn.send(makeUrlRequest(url, reqId))
        } catch (err) {
          this.pending.delete(reqId)
          rec.cleanup()
          reject(err)
        }
      }
      conn.on('open', onOpen)
      rec._listeners.open = onOpen
    }

    // Listen for data
    const onData = (data) => this.handleFileData(reqId, data)
    conn.on('data', onData)
    rec._listeners.data = onData

    // Listen for close
    const onClose = () => {
      const p = this.pending.get(reqId)
      if (p) {
        this.pending.delete(reqId)
        p.cleanup()
        p.reject(new Error('peerdrive-media: file channel closed'))
      }
    }
    conn.on('close', onClose)
    rec._listeners.close = onClose

    // Listen for error
    const onError = (err) => {
      const p = this.pending.get(reqId)
      if (p) {
        this.pending.delete(reqId)
        p.cleanup()
        p.reject(new Error(`peerdrive-media: file channel error: ${err?.type || err}`))
      }
    }
    conn.on('error', onError)
    rec._listeners.error = onError
  }

  // open establishes the full link to Node-side peer (Peer signaling + control DataChannel).
  open() {
    const sig = this.signaling
    const dbg = typeof window !== 'undefined' && window.__PDM_DEBUG ? 3 : 0
    const peer = new Peer(`pd-b-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`, {
      host: sig.host, port: sig.port, secure: sig.secure, key: sig.key, path: sig.path || '/',
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
      // Create control channel (only for keepalive)
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
        // Process queued connection-ready requests
        if (this._waitingForReady && this._waitingForReady.length) {
          const entries = this._waitingForReady
          this._waitingForReady = null
          for (const entry of entries) {
            // Remove abort listener (already sent, no longer need queued abort)
            if (entry.signal) {
              entry.signal.removeEventListener('abort', entry._onAbort)
            }
            this.sendFileRequest(entry.url, entry.resolve, entry.reject, entry.signal)
          }
        }
        // Pre-warm channel pool (delay one tick to ensure request channels are created first)
        setTimeout(() => this.warmUp(), 0)
      })
      conn.on('data', (data) => this.handleControlData(data))
      conn.on('close', () => this.teardown('control channel closed'))
      conn.on('error', (err) => {
        if (!this.ready && !this.closed) this.failAll(`control channel error: ${err?.type || err}`)
      })
    })
  }

  // handleControlData handles control channel data (only receives ping-ack).
  handleControlData(data) {
    this.lastActive = Date.now()
    if (typeof data === 'string') {
      const msg = parseFrame(data)
      if (msg?.type === 'ping-ack') return // keepalive response
    }
  }

  // handleFileData handles file channel data.
  handleFileData(reqId, data) {
    this.lastActive = Date.now()
    const p = this.pending.get(reqId)
    if (!p) return

    if (isBinaryFrame(data)) {
      // Binary block
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
          p.reject(new Error(`peerdrive-media: upstream ${msg.status}`))
        }
        break
      case 'done':
        this.pending.delete(reqId)
        const blob = new Blob(p.chunks, { type: p.mime })
        p.cleanup()  // return channel to pool
        p.resolve({ blob, blobUrl: URL.createObjectURL(blob), mime: p.mime, size: p.got })
        break
      case 'err':
        this.pending.delete(reqId)
        p.cleanup()  // return channel to pool
        p.reject(new Error(`peerdrive-media: ${msg.msg || 'request failed'}`))
        break
      case 'ping':
        // Ping from Node side (keepalive), reply with ping-ack
        try { p.conn.send(JSON.stringify({ type: 'ping-ack' })) } catch { /* channel dead */ }
        break
      default:
        break
    }
  }

  // startKeepalive starts the disconnect detection timer (called after controlConn opens).
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
      } catch { /* connection dead, timeout fallback */ }
    }, KEEPALIVE_INTERVAL)
  }

  // warmUp pre-warms the channel pool to reduce first-request latency.
  warmUp() {
    const promises = []
    for (let i = 0; i < this.poolSize; i++) {
      promises.push(new Promise((resolve) => {
        const conn = this.peer.connect(this.peerId, {
          reliable: true,
          serialization: 'raw',
          label: `file-pool-${Date.now()}-${i}`,
        })
        conn.on('open', () => {
          this.pool.push(conn)
          resolve()
        })
        conn.on('close', () => resolve())  // resolve even on failure, avoid blocking
        conn.on('error', () => resolve())
      }))
    }
    // Non-blocking, background pre-warm
    Promise.all(promises).then(() => {})
  }

  failAll(msg) {
    // Connection-level failure: reject all in-flight requests and queue waiters, then clean up slot.
    this.closed = true
    this.opening = false
    if (this.kaTimer) { clearInterval(this.kaTimer); this.kaTimer = null }
    const err = new Error(`peerdrive-media: ${msg}`)
    for (const [, p] of this.pending) {
      p.cleanup()
      try { p.conn?.close() } catch { /* idempotent */ }
      p.reject(err)
    }
    this.pending.clear()
    // Clean up channels in pool
    for (const conn of this.pool) {
      try { conn.close() } catch { /* idempotent */ }
    }
    this.pool = []
    this.inUse = new Set()
    if (this._waitingForReady) {
      for (const q of this._waitingForReady) q.reject(err)
      this._waitingForReady = null
    }
    this.closePeer()
  }

  teardown(msg) {
    // Connection closed (peer disconnect/network failure): same handling as failAll, slot stays closed.
    if (this.closed) return
    this.failAll(msg)
  }

  closePeer() {
    try { this.peer?.destroy() } catch { /* idempotent cleanup */ }
    this.peer = null
    this.controlConn = null
  }
}

// PeerMediaClient browser core: module-level singleton, shared by React/vanilla.
// slots cache key = signaling config + peerId; all components to the same peer share the connection.
export class PeerMediaClient {
  constructor() {
    this.slots = new Map()
  }

  // load loads URL resource, returns { blob, blobUrl, mime, size }.
  // peer: Node-side peer id (required); signaling: signaling config (defaults to public cloud);
  // signal: AbortSignal (cancel on component unmount — see ConnectionSlot.request's abort handling).
  async load(url, { peer, signaling = DEFAULT_SIGNALING, signal } = {}) {
    if (!peer) throw new Error('peerdrive-media: peer (node peer id) is required')
    if (!url || typeof url !== 'string') throw new Error('peerdrive-media: url is required')
    const key = `${signalingKey(signaling)}|${peer}`
    let slot = this.slots.get(key)
    // Rebuild after disconnect: teardown/failAll sets closed=true but slot remains in cache,
    // if reusing closed slot, request()'s "closed doesn't open()" guard causes new requests
    // to queue permanently, Promise never settles (no error during loading).
    // Discovery: code review 2026-08-18 (page recovery scenario after Node restart/disconnect).
    if (!slot || slot.closed) {
      slot = new ConnectionSlot(peer, signaling)
      this.slots.set(key, slot)
    }
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError')

    return new Promise((resolve, reject) => {
      slot.request(url, resolve, reject, signal)
    })
  }

  // dispose actively releases the connection to a peer (called on global component unmount; usually not needed).
  dispose(peer, signaling = DEFAULT_SIGNALING) {
    const key = `${signalingKey(signaling)}|${peer}`
    const slot = this.slots.get(key)
    if (slot) {
      slot.failAll('disposed')
      this.slots.delete(key)
    }
  }
}

// client module-level singleton.
export const client = new PeerMediaClient()
export default client

// ====== Service Worker support ======

// SW message handling: main thread ↔ SW
let swMessagePort = null
let swConfig = null

// registerSW registers Service Worker, enables media interception.
// Returns Promise, resolves when SW is ready.
export async function registerSW({ peer, signaling = DEFAULT_SIGNALING, allow } = {}) {
  if (!navigator.serviceWorker) {
    throw new Error('peerdrive-media: Service Worker not supported')
  }
  
  // Register SW
  const reg = await navigator.serviceWorker.register('/sw.js')
  await navigator.serviceWorker.ready
  
  // Establish message channel
  const channel = new MessageChannel()
  channel.port1.start()
  swMessagePort = channel.port1
  
  // Set SW configuration
  swConfig = { peer, signaling, allow }
  
  // Send configuration to SW
  channel.port2.postMessage({
    type: 'pdm-config',
    config: {
      peer,
      signaling,
      allow,
    },
  })
  
  // Listen for SW messages
  channel.port1.onmessage = (event) => {
    const data = event.data || {}
    if (data.type === 'pdm-load-request') {
      handleSWLoadRequest(data.url, data.reqId)
    }
  }
  
  return {
    unregister: async () => {
      await reg.unregister()
      swMessagePort = null
    },
  }
}

// Handle SW's load request
async function handleSWLoadRequest(url, reqId) {
  try {
    // Use client to load resource
    const result = await client.load(url, { peer: swConfig.peer, signaling: swConfig.signaling })
    
    // Return result to SW
    if (swMessagePort) {
      swMessagePort.postMessage({
        type: 'pdm-load-response',
        reqId,
        blob: result.blob,
        mime: result.mime,
        size: result.size,
      })
    }
  } catch (err) {
    if (swMessagePort) {
      swMessagePort.postMessage({
        type: 'pdm-load-response',
        reqId,
        error: err.message,
      })
    }
  }
}

// ====== Monkey-Patch auto-interception ======

// Intercept img/video src setting, auto-load via WebRTC
let mpConfig = null
let mpOrigSetters = {}

// setupMP enables monkey-patch auto-interception.
// After this, JS setting img.src / video.src will auto-load via WebRTC.
export function setupMP({ peer, signaling = DEFAULT_SIGNALING, allow } = {}) {
  if (!peer) throw new Error('peerdrive-media: peer is required for setupMP')
  
  mpConfig = { peer, signaling, allow }
  
  // Intercept HTMLImageElement.src
  if (!mpOrigSetters.img) {
    const desc = Object.getOwnPropertyDescriptor(HTMLImageElement.prototype, 'src')
    mpOrigSetters.img = desc?.set
    
    Object.defineProperty(HTMLImageElement.prototype, 'src', {
      get: desc?.get,
      set: function(url) {
        if (mpConfig && shouldInterceptMP(url)) {
          loadAndSetSrc(this, url, mpConfig)
        } else if (mpOrigSetters.img) {
          mpOrigSetters.img.call(this, url)
        }
      },
      configurable: true,
    })
  }
  
  // Intercept HTMLVideoElement.src
  if (!mpOrigSetters.video) {
    const desc = Object.getOwnPropertyDescriptor(HTMLVideoElement.prototype, 'src')
    mpOrigSetters.video = desc?.set
    
    Object.defineProperty(HTMLVideoElement.prototype, 'src', {
      get: desc?.get,
      set: function(url) {
        if (mpConfig && shouldInterceptMP(url)) {
          loadAndSetSrc(this, url, mpConfig)
        } else if (mpOrigSetters.video) {
          mpOrigSetters.video.call(this, url)
        }
      },
      configurable: true,
    })
  }
  
  // Intercept HTMLAudioElement.src
  if (typeof HTMLAudioElement !== 'undefined' && !mpOrigSetters.audio) {
    const desc = Object.getOwnPropertyDescriptor(HTMLAudioElement.prototype, 'src')
    mpOrigSetters.audio = desc?.set
    
    Object.defineProperty(HTMLAudioElement.prototype, 'src', {
      get: desc?.get,
      set: function(url) {
        if (mpConfig && shouldInterceptMP(url)) {
          loadAndSetSrc(this, url, mpConfig)
        } else if (mpOrigSetters.audio) {
          mpOrigSetters.audio.call(this, url)
        }
      },
      configurable: true,
    })
  }
  
  return {
    teardown: () => {
      mpConfig = null
      // Restore original setters
      for (const [type, setter] of Object.entries(mpOrigSetters)) {
        if (setter) {
          const proto = type === 'img' ? HTMLImageElement.prototype :
                         type === 'video' ? HTMLVideoElement.prototype :
                         HTMLAudioElement.prototype
          const desc = Object.getOwnPropertyDescriptor(proto, 'src')
          Object.defineProperty(proto, 'src', {
            get: desc?.get,
            set: setter,
            configurable: true,
          })
        }
      }
      mpOrigSetters = {}
    },
  }
}

// Check if URL is in the whitelist
function shouldInterceptMP(url) {
  if (!mpConfig?.allow) return false
  if (typeof mpConfig.allow === 'function') {
    return mpConfig.allow(url)
  }
  if (Array.isArray(mpConfig.allow)) {
    return mpConfig.allow.some(prefix => url.startsWith(prefix))
  }
  return false
}

// Load resource and set src
async function loadAndSetSrc(el, url, config) {
  try {
    const result = await client.load(url, { peer: config.peer, signaling: config.signaling })
    if (mpOrigSetters[el.tagName?.toLowerCase()]) {
      mpOrigSetters[el.tagName?.toLowerCase()].call(el, result.blobUrl)
    } else {
      el.src = result.blobUrl
    }
  } catch (err) {
    console.error('peerdrive-media: load failed', err)
    // Fallback to original src
    if (mpOrigSetters[el.tagName?.toLowerCase()]) {
      mpOrigSetters[el.tagName?.toLowerCase()].call(el, url)
    } else {
      el.src = url
    }
  }
}
