// client.js — Unified transport client (PeerJS WebRTC DataChannel priority + Local WebSocket /ws/peer + HTTP fallback)
// with real-time progress tracking (globalProgressHub) and order-independent queue safety.

import { setStatus, getStatus, onStatus, resetStatus } from './status.js'
import {
  STORAGE_KEY_API_BASE,
  STORAGE_KEY_AUTH_TOKEN,
  STORAGE_KEY_AUTH_KEY,
  STORAGE_KEY_AUTH_HEADER_ENABLED,
} from '../shared/storageKeys.js'
import { getNodeSession, onNodeSession } from '../../lib/nodeSession.js'
import {
  parseFrame,
  decodeBinaryHeader32,
  toUint8Array,
  globalProgressHub,
  AsyncQueueLane,
  evaluateAdminPermission,
  PermissionError,
  sanitizeFilename,
  sanitizeUntrustedText,
  sanitizeCollectionManifest,
} from '../../../../packages/peerdrive-core/src/index.js'

function wsUrl(base) {
  return (base.startsWith('https') ? 'wss://' : 'ws://') + base.replace(/^https?:\/\//, '')
}

let sock = null
let reqSeq = 0
const pending = new Map() // reqId → request state

const BIN_CHUNK = 64 * 1024

// Single-slot expectation for v1 contiguous binary frames; v2 32B header frames bypass this slot.
let binaryExpect = null

// Dedicated serial lane for multi-chunk WS binary uploads so concurrent upload() calls
// never interleave raw binary chunks on the same WebSocket connection.
const uploadSerialLane = new AsyncQueueLane(1)

let pausedByPeerSession = false
if (typeof onNodeSession === 'function') {
  onNodeSession((session) => {
    if (session?.client) {
      pausedByPeerSession = true
      if (retryTimer) {
        clearTimeout(retryTimer)
        retryTimer = null
      }
      stopHeartbeat()
      if (sock) {
        try {
          sock._suppressReconnect = true
          sock.close()
        } catch {}
        sock = null
      }
      setStatus('idle')
    } else if (pausedByPeerSession) {
      pausedByPeerSession = false
      connect()
    }
  })
}

// readToken prefers short-lived sessionStorage credentials (FULLSTACK-AUDIT §2 item 1)
// before falling back to localStorage for backward compatibility.
function readToken() {
  try {
    if (typeof sessionStorage !== 'undefined') {
      const sessFrag = sessionStorage.getItem(STORAGE_KEY_AUTH_TOKEN)
      if (sessFrag) return sessFrag
    }
    const frag = localStorage.getItem(STORAGE_KEY_AUTH_TOKEN)
    if (frag) return frag
    if (localStorage.getItem(STORAGE_KEY_AUTH_HEADER_ENABLED) === 'true') {
      return localStorage.getItem(STORAGE_KEY_AUTH_KEY) || ''
    }
  } catch {}
  return ''
}

// getWsBase resolves the backend endpoint in priority order:
//   1. Explicit user override in localStorage (STORAGE_KEY_API_BASE)
//   2. Build-time environment variable VITE_API_BASE (FULLSTACK-AUDIT §1 item 14 & §4)
//   3. Localhost loopback auto-detection (http://127.0.0.1:3000)
//   4. Production HTTPS fallback domain
function getWsBase() {
  try {
    const custom = localStorage.getItem(STORAGE_KEY_API_BASE)
    if (custom) return custom
  } catch {}
  try {
    if (typeof import.meta !== 'undefined' && import.meta.env && import.meta.env.VITE_API_BASE) {
      const envBase = String(import.meta.env.VITE_API_BASE).trim().replace(/\/+$/, '')
      if (envBase) return envBase
    }
  } catch {}
  if (
    typeof window !== 'undefined' &&
    window.location &&
    (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1')
  ) {
    return 'http://127.0.0.1:3000'
  }
  return 'https://wsl-3000.moonchan.xyz'
}

const HEARTBEAT_MS = 25000
const STALE_MS = 60000
const RETRY_MIN_MS = 1000
const RETRY_MAX_MS = 30000
const REQ_TIMEOUT_MS = 60000
const CONNECT_TIMEOUT_MS = 5000

let hbTimer = null
let retryTimer = null
let retryDelay = RETRY_MIN_MS
let lastRecv = 0

function abortPending(reqId, err) {
  const p = pending.get(reqId)
  if (!p) return
  if (p.timer) {
    clearTimeout(p.timer)
    p.timer = null
  }
  pending.delete(reqId)
  if (binaryExpect && binaryExpect.reqId === reqId) {
    binaryExpect = null
  }
  if (p.taskId) {
    if (err) {
      globalProgressHub.failTask(p.taskId, err)
    } else {
      globalProgressHub.cancelTask(p.taskId)
    }
  }
  if (err && p.reject) {
    try {
      p.reject(err)
    } catch {}
  }
}

function armTimeout(reqId) {
  const p = pending.get(reqId)
  if (!p) return
  if (p.timer) clearTimeout(p.timer)
  p.timer = setTimeout(() => {
    abortPending(reqId, new Error('ws: request timeout'))
  }, REQ_TIMEOUT_MS)
  p.timer.unref?.()
}

function setPending(reqId, entry) {
  pending.set(reqId, entry)
  armTimeout(reqId)
}

function ensureOpenSync() {
  if (getNodeSession?.()?.client) return false
  connect()
  return !!sock && sock.readyState === WebSocket.OPEN
}

function ensureOpen() {
  if (getNodeSession?.()?.client) {
    return Promise.resolve()
  }
  connect()
  if (sock && sock.readyState === WebSocket.OPEN) {
    return Promise.resolve()
  }
  if (sock && (sock.readyState === WebSocket.CONNECTING || getStatus() === 'connecting')) {
    return new Promise((resolve, reject) => {
      let timeoutTimer = null
      let unsubscribe = null

      const cleanup = () => {
        if (timeoutTimer) {
          clearTimeout(timeoutTimer)
          timeoutTimer = null
        }
        if (unsubscribe) {
          unsubscribe()
          unsubscribe = null
        }
      }

      timeoutTimer = setTimeout(() => {
        cleanup()
        reject(new Error('ws: connection timeout waiting for open'))
      }, CONNECT_TIMEOUT_MS)
      timeoutTimer.unref?.()

      unsubscribe = onStatus((newStatus) => {
        if (newStatus === 'open') {
          cleanup()
          resolve()
        } else if (newStatus === 'closed') {
          cleanup()
          reject(new Error('ws: connection closed before open'))
        }
      })
    })
  }
  return Promise.reject(new Error('ws: not connected'))
}

function stopHeartbeat() {
  if (hbTimer) {
    clearInterval(hbTimer)
    hbTimer = null
  }
}

function startHeartbeat() {
  if (!sock || !sock._wsOwned) return
  stopHeartbeat()
  lastRecv = Date.now()
  hbTimer = setInterval(() => {
    if (!sock || sock.readyState !== WebSocket.OPEN) return
    if (Date.now() - lastRecv > STALE_MS) {
      try {
        sock.close()
      } catch {}
      return
    }
    admin('GET', '/ping').catch(() => {})
  }, HEARTBEAT_MS)
  hbTimer.unref?.()
}

function scheduleReconnect() {
  if (retryTimer) return
  if (getNodeSession?.()?.client) return
  retryTimer = setTimeout(() => {
    retryTimer = null
    try {
      if (!getNodeSession?.()?.client) connect()
    } catch {}
  }, retryDelay)
  retryTimer.unref?.()
  retryDelay = Math.min(retryDelay * 2, RETRY_MAX_MS)
}

function connect() {
  if (getNodeSession?.()?.client) return
  if (!sock) {
    const s = new WebSocket(wsUrl(getWsBase()) + '/ws/peer')
    s._wsOwned = true
    sock = s
    setStatus('connecting')
  }
  if (sock._wsHandlers) return
  sock._wsHandlers = true

  sock.onopen = () => {
    retryDelay = RETRY_MIN_MS
    setStatus('open')
    startHeartbeat()
  }

  sock.onmessage = (ev) => {
    lastRecv = Date.now()
    if (typeof ev.data === 'string') {
      handleText(ev.data)
    } else {
      handleBinary(ev.data)
    }
  }
  sock.onclose = () => {
    const cur = sock
    const owned = !!cur?._wsOwned
    const suppress = !!cur?._suppressReconnect
    stopHeartbeat()
    for (const [, p] of pending) {
      if (p.timer) clearTimeout(p.timer)
      if (p.taskId) globalProgressHub.failTask(p.taskId, new Error('ws: connection closed'))
      p.reject(new Error('ws: connection closed'))
    }
    pending.clear()
    binaryExpect = null
    if (sock === cur) sock = null
    if (!suppress && !getNodeSession?.()?.client) {
      setStatus('closed')
      if (owned) scheduleReconnect()
    } else {
      setStatus('idle')
    }
  }
  sock.onerror = () => {
    try {
      sock.close()
    } catch {}
  }
}

function nextReqId() {
  reqSeq += 1
  return 'w' + Date.now().toString(36) + '-' + reqSeq.toString(36)
}

const messageListeners = new Set()

export function onMessage(fn) {
  messageListeners.add(fn)
  return () => messageListeners.delete(fn)
}

export function sendFrame(frame) {
  connect()
  if (sock && sock.readyState === WebSocket.OPEN) {
    sock.send(typeof frame === 'string' ? frame : JSON.stringify(frame))
    return true
  }
  return false
}

// handleText uses peerdrive-core's parseFrame so both v1 {type,reqId} and v2 {event,req_id,data} frames work.
function handleText(text) {
  const msg = parseFrame(text)
  if (!msg || !msg.type) return

  messageListeners.forEach((fn) => {
    try {
      fn(msg)
    } catch (e) {
      console.error('onMessage listener error:', e)
    }
  })

  switch (msg.type) {
    case 'admin-resp': {
      const p = pending.get(msg.reqId)
      if (!p) return
      if (p.timer) clearTimeout(p.timer)
      pending.delete(msg.reqId)
      if (msg.status >= 400) {
        const err = new Error(
          (msg.body && (msg.body.error || msg.body.message)) || `HTTP ${msg.status}`
        )
        err.status = msg.status
        err.data = msg.body
        if (p.taskId) globalProgressHub.failTask(p.taskId, err)
        p.reject(err)
      } else {
        if (p.taskId) globalProgressHub.completeTask(p.taskId)
        p.resolve(msg.body)
      }
      return
    }
    case 'admin-bin': {
      const p = pending.get(msg.reqId)
      if (!p) return
      armTimeout(msg.reqId)
      binaryExpect = { type: 'admin', reqId: msg.reqId, size: msg.size || 0, got: 0, chunks: [] }
      if (binaryExpect.size === 0) {
        finishBinaryExpect(p, binaryExpect)
        binaryExpect = null
      }
      return
    }
    case 'data': {
      const p = pending.get(msg.reqId)
      if (!p || (p.kind !== 'download' && p.kind !== 'stream')) return
      armTimeout(msg.reqId)
      binaryExpect = { type: p.kind, reqId: msg.reqId, size: msg.size || 0, got: 0, chunks: [] }
      if (binaryExpect.size === 0) {
        binaryExpect = null
      }
      return
    }
    case 'meta': {
      const p = pending.get(msg.reqId)
      if (!p) return
      if (p.kind === 'stat') {
        if (p.timer) clearTimeout(p.timer)
        pending.delete(msg.reqId)
        p.resolve(msg.total || 0)
        return
      }
      // For download/stream: record total bytes declared by server so progress bar shows exact %
      if (typeof msg.total === 'number' && msg.total >= 0) {
        p.total = msg.total
        if (p.taskId) {
          globalProgressHub.updateProgress(p.taskId, p.gotBytes || 0, p.total)
        }
        if (typeof p.onProgress === 'function') {
          try {
            p.onProgress({ received: p.gotBytes || 0, total: p.total })
          } catch {}
        }
      }
      return
    }
    case 'done': {
      const p = pending.get(msg.reqId)
      if (!p || (p.kind !== 'download' && p.kind !== 'stream')) return
      if (p.timer) clearTimeout(p.timer)
      pending.delete(msg.reqId)
      if (binaryExpect && binaryExpect.reqId === msg.reqId) {
        binaryExpect = null
      }
      if (p.taskId) {
        globalProgressHub.completeTask(p.taskId, { received: p.gotBytes || 0 })
      }
      if (p.kind === 'stream') {
        p.controller.close()
      } else {
        p.resolve(assemble(p))
      }
      return
    }
    case 'err': {
      const p = pending.get(msg.reqId)
      if (!p) return
      abortPending(msg.reqId, new Error(msg.msg || 'peer fetch failed'))
      return
    }
    default:
      return
  }
}

// handleBinary supports BOTH v2 32-byte self-routing headers ("PD02") and v1 contiguous binaryExpect.
function handleBinary(data) {
  let bytes
  try {
    bytes = toUint8Array(data)
  } catch {
    return
  }

  // 1. Check v2 32-byte self-routing binary header (#378 A7) — order-independent routing by reqId
  const hdr32 = decodeBinaryHeader32(bytes)
  if (hdr32 && pending.has(hdr32.reqId)) {
    const p = pending.get(hdr32.reqId)
    armTimeout(hdr32.reqId)
    const chunkBuf = hdr32.payload.buffer.slice(
      hdr32.payload.byteOffset,
      hdr32.payload.byteOffset + hdr32.payload.byteLength
    )
    p.gotBytes = (p.gotBytes || 0) + hdr32.payload.byteLength
    if (p.taskId) {
      globalProgressHub.updateProgress(p.taskId, p.gotBytes, p.total)
    }
    if (typeof p.onProgress === 'function') {
      try {
        p.onProgress({ received: p.gotBytes, total: p.total })
      } catch {}
    }
    if (p.kind === 'stream') {
      p.controller.enqueue(new Uint8Array(chunkBuf))
    } else if (p.chunks) {
      p.chunks.push(chunkBuf)
    }
    return
  }

  // 2. Fallback to v1 contiguous binaryExpect slot
  if (!binaryExpect) return
  const e = binaryExpect
  armTimeout(e.reqId)
  const byteLen = data.byteLength ?? bytes.byteLength
  e.got += byteLen
  e.chunks.push(data)

  const p = pending.get(e.reqId)
  if (p) {
    p.gotBytes = (p.gotBytes || 0) + byteLen
    if (p.taskId) {
      globalProgressHub.updateProgress(p.taskId, p.gotBytes, p.total)
    }
    if (typeof p.onProgress === 'function') {
      try {
        p.onProgress({ received: p.gotBytes, total: p.total })
      } catch {}
    }
  }

  if (e.size !== undefined && e.got >= e.size) {
    binaryExpect = null
    if (!p) return
    if (e.type === 'admin') {
      finishBinaryExpect(p, e)
    } else if (e.type === 'stream') {
      p.controller.enqueue(assemble(e))
    } else {
      p.chunks.push(...e.chunks)
    }
  }
}

function finishBinaryExpect(p, e) {
  if (p.timer) clearTimeout(p.timer)
  const arr = new Uint8Array(e.got)
  let off = 0
  for (const c of e.chunks) {
    const u8 = toUint8Array(c)
    arr.set(u8, off)
    off += u8.byteLength
  }
  if (p.taskId) globalProgressHub.completeTask(p.taskId, { received: e.got, total: e.got })
  pending.delete(e.reqId)
  p.resolve(arr)
}

function assemble(p) {
  const arr = new Uint8Array(
    p.chunks.length ? p.chunks.reduce((n, c) => n + (c.byteLength ?? toUint8Array(c).byteLength), 0) : 0
  )
  let off = 0
  for (const c of p.chunks) {
    const u8 = toUint8Array(c)
    arr.set(u8, off)
    off += u8.byteLength
  }
  return arr
}

/* ── Admin plane (admin / upload / stat) ── */

function sanitizePeerFiles(files) {
  if (!Array.isArray(files)) return []
  return files.map((f) => ({
    ...f,
    name: sanitizeFilename(f?.name || f?.filename || 'file.bin', 'file.bin'),
    filename: sanitizeFilename(f?.filename || f?.name || 'file.bin', 'file.bin'),
    path: sanitizeUntrustedText(f?.path || '', 512),
  }))
}

function sanitizePeerCollections(colls) {
  if (!Array.isArray(colls)) return []
  return colls.map((c) => {
    try {
      const clean = sanitizeCollectionManifest({
        ...c,
        friendly_name: c?.friendly_name || c?.name || '',
        entries: Array.isArray(c?.entries) ? c.entries : [],
      })
      return { ...c, ...clean }
    } catch {
      return {
        ...c,
        name: sanitizeUntrustedText(c?.name || '', 256),
        entries: [],
      }
    }
  })
}

export async function admin(method, path, body = null) {
  const session = getNodeSession?.()
  if (session?.client) {
    const perm = evaluateAdminPermission('peerjs', method, path)
    if (!perm.allowed) {
      throw new PermissionError(perm.reason, perm.code)
    }
    if (path === '/anon/collections' || path === '/collections') {
      try {
        const snap = await session.client.shares()
        return sanitizePeerCollections(snap?.collections || [])
      } catch {
        return []
      }
    }
    if (path.startsWith('/anon/collections/') || path.startsWith('/c/')) {
      const hash = path.split('/').pop()?.split('?')[0]
      try {
        const snap = await session.client.shares()
        const colls = sanitizePeerCollections(snap?.collections || [])
        const match = colls.find((c) => c.hash === hash || c.name === hash)
        return match || { entries: [] }
      } catch {
        return { entries: [] }
      }
    }
    if (path === '/files') {
      try {
        const snap = await session.client.shares()
        return sanitizePeerFiles(snap?.files || [])
      } catch {
        return []
      }
    }
    if (path === '/ping') {
      return { pong: 'pong' }
    }
    if (path === '/peerjs/node') {
      return { id: session.peerId, peers: [] }
    }
    if (path === '/p2p/auth/status') {
      return { authenticated: Boolean(session.psk), username: session.psk ? 'PSK' : 'Guest' }
    }
    if (path === '/tags/summary') {
      return { data: [], tags: [] }
    }
    if (path === '/tags/batch') {
      return { tags: {} }
    }
    if (
      path === '/display/screens' ||
      path === '/p2p/pull' ||
      path === '/bt/downloads' ||
      path === '/ipfs/pins' ||
      path === '/files/inbox'
    ) {
      return []
    }
    return null
  }

  const send = () => {
    const reqId = nextReqId()
    const frame = { type: 'admin', method, path, body, token: readToken(), reqId }
    return new Promise((resolve, reject) => {
      setPending(reqId, { resolve, reject })
      try {
        sock.send(JSON.stringify(frame))
      } catch (err) {
        abortPending(reqId, err)
      }
    })
  }
  if (ensureOpenSync()) return send()
  return ensureOpen().then(send)
}

// upload supports both PeerJS WebRTC upload (session.client.put) and local WS chunked upload,
// with live progress tracking in globalProgressHub and serial upload protection.
let activeWsUpload = false

export function upload(file, fileName, field = 'file', path = '/files/upload', opts = {}) {
  const name = fileName || (file && file.name) || 'file'
  const totalSize = (file && typeof file.size === 'number') ? file.size : -1
  const session = getNodeSession?.()

  // Mode 1: Active PeerJS WebRTC session — upload via PeerJS DataChannel put()
  if (session?.client && typeof session.client.put === 'function' && path === '/files/upload') {
    const task = globalProgressHub.createTask({
      kind: 'upload',
      transport: 'peerjs',
      name,
      total: totalSize,
      peerId: session.peerId || 'peerjs',
    })
    globalProgressHub.startTask(task.id, totalSize)
    return session.client
      .put(file, {
        name,
        signal: opts.signal,
        onProgress: (sent, tot) => {
          globalProgressHub.updateProgress(task.id, sent, tot)
          opts.onProgress?.({ received: sent, total: tot })
        },
      })
      .then((res) => {
        globalProgressHub.completeTask(task.id, { hash: res?.hash || '', received: res?.size ?? totalSize })
        return res
      })
      .catch((err) => {
        globalProgressHub.failTask(task.id, err)
        throw err
      })
  }

  // Mode 2: Local WebSocket upload
  const sendOne = () => {
    const reqId = nextReqId()
    const task = globalProgressHub.createTask({
      id: reqId,
      kind: 'upload',
      transport: 'ws',
      name,
      total: totalSize,
      peerId: 'local-ws',
      onCancel: () => abortPending(reqId, new Error('Upload cancelled')),
    })
    globalProgressHub.startTask(task.id, totalSize)
    activeWsUpload = true

    return new Promise((resolve, reject) => {
      setPending(reqId, {
        taskId: task.id,
        resolve: (val) => {
          activeWsUpload = false
          resolve(val)
        },
        reject: (err) => {
          activeWsUpload = false
          reject(err)
        },
      })
      const decl = {
        type: 'admin',
        method: 'POST',
        path,
        binary: true,
        filename: name,
        field,
        size: file.size,
        token: readToken(),
        reqId,
      }
      try {
        sock.send(JSON.stringify(decl))
        pumpBinary(file, reqId, task.id, opts.onProgress)
      } catch (err) {
        activeWsUpload = false
        abortPending(reqId, err)
      }
    })
  }

  // If no upload is currently active and socket is open, send synchronously (preserves unit test expectations);
  // if another binary upload is already in flight on the socket, serialize via uploadSerialLane to prevent frame interleaving.
  if (ensureOpenSync() && !activeWsUpload) {
    return uploadSerialLane.enqueue(() => sendOne())
  }
  if (ensureOpenSync()) {
    return uploadSerialLane.enqueue(() => sendOne())
  }
  return ensureOpen().then(() => uploadSerialLane.enqueue(() => sendOne()))
}

function pumpBinary(file, reqId, taskId, onProgress) {
  let sentBytes = 0
  const totalBytes = file.size || 0
  const send = (buf) => {
    if (!sock || sock.readyState !== WebSocket.OPEN) throw new Error('ws: closed during upload')
    sock.send(buf)
    const len = buf.byteLength ?? buf.length ?? 0
    sentBytes += len
    if (taskId) globalProgressHub.updateProgress(taskId, sentBytes, totalBytes)
    if (typeof onProgress === 'function') {
      try {
        onProgress({ received: sentBytes, total: totalBytes })
      } catch {}
    }
  }
  const fail = (err) => {
    abortPending(reqId, err)
  }
  if (file.stream && typeof file.stream === 'function') {
    const reader = file.stream().getReader()
    ;(async () => {
      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          send(value)
        }
      } catch (err) {
        fail(err)
      }
    })()
    return
  }
  const CH = BIN_CHUNK
  let off = 0
  const next = () => {
    if (off >= file.size) return
    const slice = file.slice(off, off + CH)
    off += CH
    const fr = new FileReader()
    fr.onload = () => {
      try {
        send(fr.result)
        next()
      } catch (err) {
        fail(err)
      }
    }
    fr.onerror = () => fail(fr.error)
    fr.readAsArrayBuffer(slice)
  }
  next()
}

export async function stat(hash) {
  const session = getNodeSession?.()
  if (session?.client) {
    try {
      const snap = await session.client.shares()
      const f = (snap?.files || []).find((it) => it.hash === hash)
      if (f && typeof f.size === 'number') {
        return { total: f.size }
      }
    } catch {}
    return { total: 0 }
  }

  const send = () => {
    const reqId = nextReqId()
    return new Promise((resolve, reject) => {
      setPending(reqId, { kind: 'stat', resolve, reject })
      try {
        sock.send(JSON.stringify({ type: 'req', hash, offset: 0, size: 0, reqId }))
      } catch (err) {
        abortPending(reqId, err)
      }
    })
  }
  if (ensureOpenSync()) return send()
  return ensureOpen().then(send)
}

/* ── Data plane (download / downloadStream / downloadToFile / httpDownload) ── */

/**
 * Direct HTTP download fallback via canonical URL /f/:sha256 with real-time progress bar tracking.
 */
export async function httpDownload(hash, { name = '', onProgress = null, signal = null } = {}) {
  const base = getWsBase().replace(/\/+$/, '')
  const url = `${base}/f/${hash}`
  const task = globalProgressHub.createTask({
    kind: 'download',
    transport: 'http',
    hash,
    name: name || hash.slice(0, 12),
    total: -1,
    peerId: 'http-direct',
  })
  globalProgressHub.startTask(task.id, -1)
  try {
    const resp = await fetch(url, { signal })
    if (!resp.ok) {
      throw new Error(`HTTP ${resp.status} fetching /f/${hash.slice(0, 12)}`)
    }
    const contentLen = Number(resp.headers.get('content-length')) || -1
    if (contentLen > 0) {
      globalProgressHub.updateProgress(task.id, 0, contentLen)
    }
    if (!resp.body || typeof resp.body.getReader !== 'function') {
      const buf = await resp.arrayBuffer()
      const u8 = new Uint8Array(buf)
      globalProgressHub.completeTask(task.id, { received: u8.byteLength, total: u8.byteLength })
      onProgress?.({ received: u8.byteLength, total: u8.byteLength })
      return u8
    }
    const reader = resp.body.getReader()
    const chunks = []
    let received = 0
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      if (value) {
        chunks.push(value)
        received += value.byteLength
        globalProgressHub.updateProgress(task.id, received, contentLen)
        onProgress?.({ received, total: contentLen })
      }
    }
    const out = new Uint8Array(received)
    let off = 0
    for (const c of chunks) {
      out.set(c, off)
      off += c.byteLength
    }
    globalProgressHub.completeTask(task.id, { received, total: received })
    return out
  } catch (err) {
    globalProgressHub.failTask(task.id, err)
    throw err
  }
}

export async function download(hash, offset = 0, size = -1, opts = {}) {
  const session = getNodeSession?.()
  if (session?.client) {
    const task = globalProgressHub.createTask({
      kind: 'download',
      transport: 'peerjs',
      hash,
      name: opts.name || `${hash.slice(0, 12)}…`,
      total: size > 0 ? size : -1,
      peerId: session.peerId || 'peerjs',
    })
    globalProgressHub.startTask(task.id, size > 0 ? size : -1)
    try {
      const chunks = []
      let received = 0
      for await (const chunk of session.client.stream(hash, {
        offset,
        size,
        signal: opts.signal,
        onProgress: (rec, tot) => {
          globalProgressHub.updateProgress(task.id, rec, tot)
          opts.onProgress?.({ received: rec, total: tot })
        },
      })) {
        chunks.push(chunk)
        received += chunk.byteLength
        globalProgressHub.updateProgress(task.id, received, -1)
      }
      const out = new Uint8Array(received)
      let cur = 0
      for (const c of chunks) {
        out.set(c, cur)
        cur += c.byteLength
      }
      globalProgressHub.completeTask(task.id, { received, total: received })
      return out
    } catch (err) {
      globalProgressHub.failTask(task.id, err)
      throw err
    }
  }

  const send = () => {
    const reqId = nextReqId()
    const task = globalProgressHub.createTask({
      id: reqId,
      kind: 'download',
      transport: 'ws',
      hash,
      name: opts.name || `${hash.slice(0, 12)}…`,
      total: size > 0 ? size : -1,
      peerId: 'local-ws',
      onCancel: () => abortPending(reqId, new Error('Download cancelled')),
    })
    globalProgressHub.startTask(task.id, size > 0 ? size : -1)
    const p = {
      kind: 'download',
      chunks: [],
      total: size > 0 ? size : -1,
      gotBytes: 0,
      taskId: task.id,
      onProgress: opts.onProgress || null,
    }
    return new Promise((resolve, reject) => {
      setPending(reqId, { ...p, resolve, reject })
      try {
        sock.send(JSON.stringify({ type: 'req', hash, offset, size, reqId }))
      } catch (err) {
        abortPending(reqId, err)
      }
    })
  }
  if (ensureOpenSync()) return send()
  return ensureOpen().then(send)
}

export function downloadStream(hash, offset = 0, size = -1, opts = {}) {
  const session = getNodeSession?.()
  if (session?.client) {
    let iterator = null
    const task = globalProgressHub.createTask({
      kind: 'stream',
      transport: 'peerjs',
      hash,
      name: opts.name || `${hash.slice(0, 12)}…`,
      total: size > 0 ? size : -1,
      peerId: session.peerId || 'peerjs',
    })
    globalProgressHub.startTask(task.id, size > 0 ? size : -1)
    let received = 0
    return new ReadableStream({
      start() {
        iterator = session.client
          .stream(hash, {
            offset,
            size,
            onProgress: (rec, tot) => {
              globalProgressHub.updateProgress(task.id, rec, tot)
              opts.onProgress?.({ received: rec, total: tot })
            },
          })
          [Symbol.asyncIterator]()
      },
      async pull(controller) {
        try {
          const { value, done } = await iterator.next()
          if (done) {
            globalProgressHub.completeTask(task.id, { received })
            controller.close()
          } else {
            received += value.byteLength
            globalProgressHub.updateProgress(task.id, received, -1)
            controller.enqueue(value)
          }
        } catch (err) {
          globalProgressHub.failTask(task.id, err)
          controller.error(err)
        }
      },
      cancel() {
        globalProgressHub.cancelTask(task.id)
        try {
          iterator?.return?.()
        } catch {}
      },
    })
  }

  const reqId = nextReqId()
  const task = globalProgressHub.createTask({
    id: reqId,
    kind: 'stream',
    transport: 'ws',
    hash,
    name: opts.name || `${hash.slice(0, 12)}…`,
    total: size > 0 ? size : -1,
    peerId: 'local-ws',
    onCancel: () => abortPending(reqId),
  })
  globalProgressHub.startTask(task.id, size > 0 ? size : -1)

  return new ReadableStream({
    start(controller) {
      const send = () => {
        setPending(reqId, {
          kind: 'stream',
          taskId: task.id,
          gotBytes: 0,
          total: size > 0 ? size : -1,
          onProgress: opts.onProgress || null,
          controller,
          resolve: () => {},
          reject: (err) => controller.error(err),
        })
        try {
          sock.send(JSON.stringify({ type: 'req', hash, offset, size, reqId }))
        } catch (err) {
          abortPending(reqId, err)
        }
      }
      if (ensureOpenSync()) {
        send()
      } else {
        ensureOpen()
          .then(send)
          .catch((err) => {
            globalProgressHub.failTask(task.id, err)
            controller.error(err)
          })
      }
    },
    cancel() {
      abortPending(reqId)
    },
  })
}

export async function downloadToFile(hash, filename, opts = {}) {
  const session = getNodeSession?.()
  const name = filename || hash
  if (session?.client && typeof session.client.saveAs === 'function') {
    const task = globalProgressHub.createTask({
      kind: 'download',
      transport: 'peerjs',
      hash,
      name,
      total: opts.size > 0 ? opts.size : -1,
      peerId: session.peerId || 'peerjs',
    })
    globalProgressHub.startTask(task.id, opts.size > 0 ? opts.size : -1)
    try {
      const res = await session.client.saveAs(hash, name, {
        onProgress: ({ received, total }) => {
          globalProgressHub.updateProgress(task.id, received, total)
          opts.onProgress?.({ received, total })
        },
      })
      globalProgressHub.completeTask(task.id)
      return res
    } catch (err) {
      globalProgressHub.failTask(task.id, err)
      throw err
    }
  }

  if (typeof window !== 'undefined' && window.showSaveFilePicker) {
    const handle = await window.showSaveFilePicker({ suggestedName: name })
    const writable = await handle.createWritable()
    try {
      for await (const chunk of downloadStream(hash, 0, -1, { name, onProgress: opts.onProgress })) {
        await writable.write(chunk)
      }
    } finally {
      await writable.close()
    }
    return
  }

  let data
  try {
    data = await download(hash, 0, -1, { name, onProgress: opts.onProgress })
  } catch (wsErr) {
    // Automatic HTTP direct fallback (/f/:sha256) if WS is unavailable
    data = await httpDownload(hash, { name, onProgress: opts.onProgress })
  }
  const blob = new Blob([data])
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 5000)
}

export const __test = {
  connect,
  readToken,
  getWsBase,
  handleText,
  handleBinary,
  pending,
  abortPending,
  REQ_TIMEOUT_MS,
  CONNECT_TIMEOUT_MS,
  _setSock: (s) => {
    sock = s
  },
  _reset: () => {
    stopHeartbeat()
    if (retryTimer) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
    retryDelay = RETRY_MIN_MS
    resetStatus()
    sock = null
    activeWsUpload = false
    for (const [, p] of pending) {
      if (p.timer) clearTimeout(p.timer)
    }
    pending.clear()
    messageListeners.clear()
    binaryExpect = null
    globalProgressHub.clearCompleted()
  },
  _binaryExpect: () => binaryExpect,
}
