// client.js — local WS session transport client (/ws/peer).
//
// Separated into two operational planes:
// 1. Admin plane (admin / upload / stat): Sends {"type":"admin",...} frames.
//    The backend (back/internal/transport/admin.go) forwards internally to the
//    gin engine to reuse all HTTP controllers.
// 2. Data plane (download / downloadStream / downloadToFile): Pulls files using
//    the req / meta / data / done / err verbs (same wire protocol as WebRTC DataChannel).
//
// Frame protocol (agreed with the backend, do not change):
//   Admin request: {"type":"admin","method":"GET|POST|DELETE","path":"/files?x=1",
//             "body":<JSON object|null>,"token":"<optional>","reqId":"<uuid>"}
//             {"type":"admin",...,"binary":true,"filename":"a.bin","size":N,"reqId"}
//               → followed immediately by a binary frame (data chunk); once collected, forward as multipart to /files/upload
//   Admin response: {"type":"admin-resp","status":200,"body":<raw JSON>,"reqId"}
//             {"type":"admin-bin","status":200,"size":N,"reqId"} + binary frame (file stream)
//             {"type":"err","msg":"...","reqId"}
//   File download (goes through the req verb, same set as DataChannel):
//             {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId"}
//             ← {"type":"meta",...} {"type":"data","size":N,"reqId"}+binary chunk ...
//               {"type":"done",...} / {"type":"err","msg","reqId"}
//
// Key constraints (protocol correctness depends on these, do not break):
//   1. data/admin-bin headers and binary chunks are atomically contiguous (guaranteed by backend SendFrame) — the frontend
//      uses a single-slot "most recent binary declaration header" router (binaryExpect), consistent with the backend's
//      connection-level expect state machine semantics: a binary frame always belongs to the most recent data/admin-bin header
//   2. reqId routing: admin responses / download responses are paired by reqId; admin response status>=400
//      → reject Error(err.status/err.data), consistent with the api.js request() fetch version behavior
//      (409 conflict lists and other structured error bodies are usable)
//   3. Connection disconnect → reject all pending + clear and reconnect (auto-connect before the next request)
//   4. The admin plane goes only through local WS; peerjs/WebRTC does not implement admin verbs (to prevent privilege-plane
//      vulnerabilities, a user decision; the backend serveAdmin rejects non-local connections by session ID)

import { setStatus, getStatus, onStatus, resetStatus } from './status.js'

// apiBase → ws url (http→ws / https→wss), same source as api.js getApiBase().
function wsUrl(base) {
  return (base.startsWith('https') ? 'wss://' : 'ws://') + base.replace(/^https?:\/\//, '')
}

let sock = null
let reqSeq = 0
const pending = new Map() // reqId → request state (resolve/reject + download collection state)

// BIN_CHUNK upload binary chunk size: consistent with the backend protocol (uploadChunkSize / inbound
// chunkSize are both 64KB, see back/internal/transport/{file_index,inbound}.go).
// Discovery background: the FileReader fallback path (old browsers without the stream() API) referenced an undefined constant
// → ReferenceError, causing uploads to fail outright (found during code review 2026-08-18; modern browsers take the
// Streams API branch, so this wasn't triggered in production). The WS read limit is above 3*64KB, so 64KB chunks are safe.
const BIN_CHUNK = 64 * 1024

// binaryExpect "most recent binary declaration header" single slot: a binary frame always belongs to the most recently declared
// admin-bin or data header (guaranteed by backend SendFrame atomic contiguity, do not change).
let binaryExpect = null

import {
  STORAGE_KEY_API_BASE,
  STORAGE_KEY_AUTH_TOKEN,
  STORAGE_KEY_AUTH_KEY,
  STORAGE_KEY_AUTH_HEADER_ENABLED,
} from '../shared/storageKeys.js'

// Sync the token with api.js (see the localStorage key at api.js AUTH_TOKEN_KEY)
function readToken() {
  const frag = localStorage.getItem(STORAGE_KEY_AUTH_TOKEN)
  if (frag) return frag
  if (localStorage.getItem(STORAGE_KEY_AUTH_HEADER_ENABLED) === 'true') {
    return localStorage.getItem(STORAGE_KEY_AUTH_KEY) || ''
  }
  return ''
}

function getWsBase() {
  return localStorage.getItem(STORAGE_KEY_API_BASE) || 'https://wsl-3000.moonchan.xyz'
}

/* ── Connection state, heartbeat and auto-reconnect ──
 *
 * Why these three were added (none existed before):
 *   1. Heartbeat: intermediate devices (NAT/proxy/browser power-saving policies) silently drop idle connections, and TCP
 *      doesn't guarantee you'll know immediately — the browser might not fire onclose for several minutes. What the user
 *      sees is "clicking does nothing," when really the channel died long ago. Sending an admin /ping on a timer both
 *      probes and keeps the connection alive.
 *   2. Dead-connection detection: just sending a heartbeat isn't enough, you also have to check whether anything came back.
 *      If no frame is received within STALE_MS, proactively close() and go through the reconnect flow, rather than
 *      keep sending requests into a black hole.
 *   3. Auto-reconnect (exponential backoff): previously onclose only cleared sock and waited for the next request to reconnect.
 *      So "the node restarted briefly" would freeze the entire admin console — no requests were in flight, so nobody
 *      ever triggered a reconnect. Backoff cap of 30s: it won't flood the logs when the node is truly dead, and it
 *      comes back on its own within 30s after the node recovers.
 *
 * Why this only applies to connections this module created itself (_wsOwned):
 *   Unit tests inject a mock socket, which never fires onopen and shouldn't connect to a real network.
 */
const HEARTBEAT_MS = 25000 // heartbeat interval
const STALE_MS = 60000 // no frame received for this long → treat the connection as dead
const RETRY_MIN_MS = 1000
const RETRY_MAX_MS = 30000
const REQ_TIMEOUT_MS = 60000 // per-request timeout protection against lost frames
const CONNECT_TIMEOUT_MS = 5000 // timeout waiting for CONNECTING -> OPEN

let hbTimer = null
let retryTimer = null
let retryDelay = RETRY_MIN_MS
let lastRecv = 0

// abortPending cleans up a pending entry, clears its timeout timer, and releases any binaryExpect slot
// currently claimed by it (preventing late frames from contaminating subsequent binary transfers).
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
  if (err && p.reject) {
    try { p.reject(err) } catch {}
  }
}

function armTimeout(reqId) {
  const p = pending.get(reqId)
  if (!p) return
  if (p.timer) clearTimeout(p.timer)
  p.timer = setTimeout(() => {
    abortPending(reqId, new Error('ws: request timeout'))
  }, REQ_TIMEOUT_MS)
}

function setPending(reqId, entry) {
  pending.set(reqId, entry)
  armTimeout(reqId)
}

function ensureOpenSync() {
  connect()
  return !!sock && sock.readyState === WebSocket.OPEN
}

function ensureOpen() {
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
  if (!sock || !sock._wsOwned) return // mocks injected by tests don't probe
  stopHeartbeat()
  lastRecv = Date.now()
  hbTimer = setInterval(() => {
    if (!sock || sock.readyState !== WebSocket.OPEN) return
    if (Date.now() - lastRecv > STALE_MS) {
      // Sent heartbeats but none came back: trigger close → go through reconnect
      try { sock.close() } catch {}
      return
    }
    // /ping is the lightest admin endpoint (backend controller/ping.go returns pong);
    // use it as an application-layer ping: it both probes and lets intermediate devices see the connection is alive.
    admin('GET', '/ping').catch(() => {})
  }, HEARTBEAT_MS)
}

function scheduleReconnect() {
  if (retryTimer) return
  retryTimer = setTimeout(() => {
    retryTimer = null
    try {
      connect()
    } catch {}
  }, retryDelay)
  retryDelay = Math.min(retryDelay * 2, RETRY_MAX_MS)
}

// connect establishes the WS connection (idempotent: returns immediately if already connected; already-initialized handlers aren't reattached).
// Single-connection reuse: the browser and the local node share one session, and all requests are routed concurrently by reqId.
function connect() {
  if (!sock) {
    sock = new WebSocket(wsUrl(getWsBase()) + '/ws/peer')
    // Only connections we created ourselves need heartbeat and auto-reconnect (test-injected mocks don't carry this flag)
    sock._wsOwned = true
    setStatus('connecting')
  }
  // Idempotent handler attachment: mocks/existing sockets (test-injected) can also go through the same init path
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
    // Connection dropped: reject all pending (callers treat it as a network error), clear sock to wait for reconnect.
    // owned must be captured before clearing: only connections we created ourselves schedule auto-reconnect; test-injected
    // mocks shouldn't connect to a real network after being disconnected.
    const owned = !!sock._wsOwned
    stopHeartbeat()
    for (const [, p] of pending) {
      if (p.timer) clearTimeout(p.timer)
      p.reject(new Error('ws: connection closed'))
    }
    pending.clear()
    binaryExpect = null
    sock = null
    setStatus('closed')
    if (owned) scheduleReconnect()
  }
  sock.onerror = () => {
    // After onerror, the browser will always follow up with onclose; the reconnect logic lives uniformly there, here we just clean up
    try { sock.close() } catch {}
  }
}

function nextReqId() {
  reqSeq += 1
  return 'w' + Date.now().toString(36) + '-' + reqSeq.toString(36)
}

const messageListeners = new Set()

// onMessage registers a callback for incoming text frames
export function onMessage(fn) {
  messageListeners.add(fn)
  return () => messageListeners.delete(fn)
}

// sendFrame sends a raw frame over the WebSocket connection
export function sendFrame(frame) {
  ensureConnected()
  if (sock && sock.readyState === WebSocket.OPEN) {
    sock.send(typeof frame === 'string' ? frame : JSON.stringify(frame))
    return true
  }
  return false
}

// handleText text frame dispatch: admin responses are routed by reqId; req pull responses (meta/data headers/
// done/err) are routed to the corresponding download request.
function handleText(text) {
  let msg
  try {
    msg = JSON.parse(text)
  } catch {
    return
  }
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
        const err = new Error((msg.body && (msg.body.error || msg.body.message)) || `HTTP ${msg.status}`)
        err.status = msg.status
        err.data = msg.body
        p.reject(err)
      } else {
        p.resolve(msg.body)
      }
      return
    }
    case 'admin-bin': {
      // Binary file stream response header: declares "the next binary frame belongs to this admin download"
      const p = pending.get(msg.reqId)
      if (!p) return
      armTimeout(msg.reqId)
      binaryExpect = { type: 'admin', reqId: msg.reqId, size: msg.size || 0, got: 0, chunks: [] }
      if (binaryExpect.size === 0) {
        // Empty file / empty response has no following binary frame; the expect must be cleared immediately;
        // otherwise the leftover single slot would misattribute the next unrelated binary frame to this already-completed request.
        finishBinaryExpect(p, binaryExpect)
        binaryExpect = null
      }
      return
    }
    case 'data': {
      // Download data chunk header: declares "the next binary frame belongs to this download, size size"
      // (consistent with the server-side connection-level expect semantics; data header + chunk are atomically contiguous)
      const p = pending.get(msg.reqId)
      if (!p || (p.kind !== 'download' && p.kind !== 'stream')) return
      armTimeout(msg.reqId)
      binaryExpect = { type: p.kind, reqId: msg.reqId, size: msg.size || 0, got: 0, chunks: [] }
      if (binaryExpect.size === 0) {
        // Empty chunk (rare): clear the expectation immediately, wait for the next frame
        binaryExpect = null
      }
      return
    }
    case 'meta': {
      // req pull response header. download/stream are ignored (size is guaranteed by the data header + done);
      // stat requests (offset=0 size=0, only probing size, no data sent) take total and resolve
      const p = pending.get(msg.reqId)
      if (!p || p.kind !== 'stat') return
      if (p.timer) clearTimeout(p.timer)
      pending.delete(msg.reqId)
      p.resolve(msg.total || 0)
      return
    }
    case 'done': {
      const p = pending.get(msg.reqId)
      if (!p || (p.kind !== 'download' && p.kind !== 'stream')) return
      if (msg.type === 'done') {
        // done frame: transfer complete, the collected data's size was declared by the previous data header
        if (p.timer) clearTimeout(p.timer)
        pending.delete(msg.reqId)
        binaryExpect = null
        if (p.kind === 'stream') {
          // Streaming download: all chunks are enqueued, close the stream
          p.controller.close()
        } else {
          p.resolve(assemble(p))
        }
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

// handleBinary binary frame: belongs to binaryExpect (ownership of the most recent data/admin-bin header).
// admin-bin: once size is collected, resolve immediately (the response body is a single chunk).
// download: once data chunks are collected, only clear the expectation (wait for the done frame to resolve — completeness is guaranteed by done).
function handleBinary(data) {
  if (!binaryExpect) return
  const e = binaryExpect
  armTimeout(e.reqId)
  e.got += data.byteLength
  e.chunks.push(data)
  if (e.size !== undefined && e.got >= e.size) {
    const p = pending.get(e.reqId)
    binaryExpect = null
    if (!p) return
    if (e.type === 'admin') {
      finishBinaryExpect(p, e)
    } else if (e.type === 'stream') {
      // Streaming download: chunks collected (one binary frame = one chunk), enqueue immediately without caching the accumulation
      p.controller.enqueue(assemble(e))
    } else {
      // download: chunks collected, wait for the next data header or the done frame
      p.chunks.push(...e.chunks)
    }
  }
}

// finishBinaryExpect finishes collecting a binary response: assembles a Uint8Array and resolves.
function finishBinaryExpect(p, e) {
  if (p.timer) clearTimeout(p.timer)
  const arr = new Uint8Array(e.got)
  let off = 0
  for (const c of e.chunks) {
    arr.set(new Uint8Array(c), off)
    off += c.byteLength
  }
  pending.delete(e.reqId)
  p.resolve(arr)
}

// assemble download collection state → Uint8Array.
function assemble(p) {
  const arr = new Uint8Array(p.chunks.length ? p.chunks.reduce((n, c) => n + c.byteLength, 0) : 0)
  let off = 0
  for (const c of p.chunks) {
    arr.set(new Uint8Array(c), off)
    off += c.byteLength
  }
  return arr
}

/* ── Admin plane (admin / upload / stat) ── */

// admin generic admin request (JSON) → response JSON object.
export function admin(method, path, body = null) {
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

// upload chunked upload: admin binary declaration frame + contiguous binary chunks (reusing the same WS connection).
// field: multipart field name (default "file"); path: upload endpoint (default /files/upload;
// BT torrent upload uses /bt/torrent + field "torrent").
export function upload(file, fileName, field = 'file', path = '/files/upload') {
  const send = () => {
    const reqId = nextReqId()
    const name = fileName || (file && file.name) || 'file'
    return new Promise((resolve, reject) => {
      setPending(reqId, { resolve, reject })
      const decl = {
        type: 'admin', method: 'POST', path,
        binary: true, filename: name, field, size: file.size,
        token: readToken(), reqId,
      }
      try {
        sock.send(JSON.stringify(decl))
        pumpBinary(file, reqId)
      } catch (err) {
        abortPending(reqId, err)
      }
    })
  }
  if (ensureOpenSync()) return send()
  return ensureOpen().then(send)
}

// pumpBinary streams out file binary chunks (Streams API preferred, FileReader fallback).
function pumpBinary(file, reqId) {
  const send = (buf) => {
    if (sock.readyState !== WebSocket.OPEN) throw new Error('ws: closed during upload')
    sock.send(buf)
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
  // FileReader fallback: slice the whole thing and send chunk by chunk
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

// stat queries the total file size: sends a req request for 0 bytes (offset=0 size=0), the server replies
// meta{total} then goes straight to done without sending data (see the size semantics in back inbound.go).
// Used to probe size before download (e.g. getBlobUrl's large-file preview threshold). The local WS connection
// routes concurrently by reqId, so it doesn't interfere with the subsequent download.
export function stat(hash) {
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

/* ── Data plane (download / downloadStream / downloadToFile) ── */

// download pulls a file via the req verb (sha256 content addressing), returning a Uint8Array.
// The server sends data headers + binary frames in 64KB chunks; the frontend collects according to the size declared by the data header.
// Note: full in-memory assembly; for large files use downloadStream (emit-as-you-receive) or downloadToFile.
export function download(hash, offset = 0, size = -1) {
  const send = () => {
    const reqId = nextReqId()
    const p = { kind: 'download', chunks: [], total: 0 }
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

// downloadStream: streaming download (ReadableStream). Data chunks are enqueued as they arrive (once per 64KB chunk);
// the full data never sits in memory; used for large-file saves/transfer (the FS Access API path in downloadToFile,
// future pipeline consumption). The server frame sequence is the same as download
// (meta → data header+chunks… → done), differing only in collection-side semantics.
// Consumer cancel (e.g. the save dialog is cancelled) → immediately clean up pending + binaryExpect,
// preventing pending leaks and late frames contaminating subsequent requests (leak protection from the same source as
// upload abort; discovery background: code review 2026-08-18).
export function downloadStream(hash, offset = 0, size = -1) {
  const reqId = nextReqId()
  return new ReadableStream({
    start(controller) {
      const send = () => {
        setPending(reqId, {
          kind: 'stream',
          controller,
          resolve: () => {},
          // stream reject = pump the error into the stream (await reader.read() throws)
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
        ensureOpen().then(send).catch(err => controller.error(err))
      }
    },
    cancel() {
      abortPending(reqId)
    },
  })
}

// downloadToFile downloads and triggers a browser save. Prefers the File System Access API
// (showSaveFilePicker → createWritable, streaming write-as-you-receive, never holding everything in memory);
// when unsupported, falls back to <a download> (full in-memory Blob, fine for small-to-medium files).
// Discovery background: code review 2026-08-18 — the original implementation download() assembled everything in memory,
// which would OOM directly on large-file saves under the 8GB upload limit.
export async function downloadToFile(hash, filename) {
  const name = filename || hash
  if (window.showSaveFilePicker) {
    const handle = await window.showSaveFilePicker({ suggestedName: name })
    const writable = await handle.createWritable()
    try {
      for await (const chunk of downloadStream(hash)) {
        await writable.write(chunk)
      }
    } finally {
      await writable.close()
    }
    return
  }
  const data = await download(hash)
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

// Test hooks for vitest
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
  _setSock: (s) => { sock = s },
  _reset: () => {
    stopHeartbeat()
    if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
    retryDelay = RETRY_MIN_MS
    resetStatus()
    sock = null
    for (const [, p] of pending) {
      if (p.timer) clearTimeout(p.timer)
    }
    pending.clear()
    messageListeners.clear()
    binaryExpect = null
  },
  _binaryExpect: () => binaryExpect,
}
