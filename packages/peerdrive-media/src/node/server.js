// server.js — Node-side resource provider: peerjs node + fetch, streams URL resources in chunks
// via WebRTC DataChannel to the web side (React/vanilla components).
//
// Requirements:
//   1. Node >= 22 (native WebSocket/fetch; peerjs signaling depends on WebSocket)
//   2. @roamhq/wrtc (this package's optionalDependencies) — peerjs has no built-in
//      WebRTC implementation in Node, must inject global RTC. Module import fails if installation fails.
//   3. signaling must explicitly pass secure — peerjs's isSecure() reads location,
//      which causes ReferenceError in Node (real issue: crashes when using self-hosted host without secure).
//
// Security boundary: allow(url) whitelist must be explicitly configured (default deny all) — this
// node is a public peer connectable by any web page, no whitelist means open arbitrary URL fetching (SSRF).
//
// Multi-DataChannel architecture (2026-09-06):
//   - Control channel (label='control'): keepalive ping/ping-ack
//   - File channel (label='file-{reqId}'): one independent channel per file request
//   - Concurrent: multiple file channels can transfer simultaneously, non-blocking
import wrtc from '@roamhq/wrtc'
import { CHUNK_SIZE, guessMime, parseFrame } from '../protocol.js'

// Inject global RTC: peerjs uses global RTCPeerConnection directly (`new RTCPeerConnection(...)`)
// in its bundler, Node has no built-in implementation.
// Must execute before peerjs module loads (see dynamic import reason below).
globalThis.RTCPeerConnection = wrtc.RTCPeerConnection
globalThis.RTCSessionDescription = wrtc.RTCSessionDescription
globalThis.RTCIceCandidate = wrtc.RTCIceCandidate

// peerjs's supports detection (isWebRTCSupported etc.) is computed and cached **at module load time**
// via IIFE (util singleton). ESM imports are hoisted — static import of peerjs
// necessarily executes before this file's injection code, causing supports cache to be all false
// (runtime reports browser-incompatible, real issue: encountered on first E2E run).
// Therefore must use dynamic import: module body injects RTC first, then await import('peerjs').
// Note: if the host application already imported peerjs before this (Node without RTC), its supports
// is already cached as false, this package cannot fix it — documentation requires importing
// peerdrive-media/node first.
let Peer
async function loadPeerjs() {
  if (!Peer) {
    // CJS package dynamic import: named exports are statically analyzed by cjs-module-lexer,
    // peerjs's exports cannot be recognized — must use mod.default (module.exports object)
    const mod = await import('peerjs')
    Peer = mod.default.Peer
  }
  return Peer
}

// Self-hosted signaling defaults (must explicitly set secure=true, see file header comments for reason).
export const DEFAULT_SIGNALING = {
  host: '0.peerjs.com',
  port: 443,
  secure: true,
  key: 'peerjs',
  path: '/',
}

// LOW_WATER send backpressure threshold: pause reading fetch stream when DataChannel buffer exceeds threshold.
// Why needed: peerjs raw mode has no chunker, wrtc's send is non-blocking — without backpressure
// large files would stuff all blocks into SCTP buffer, unbounded memory growth.
const LOW_WATER = 4 * 1024 * 1024

// keepalive parameters (same protocol as core.js): Node side sends ping every 5s to generate traffic,
// any frame (including browser-side pings) refreshes active; 15s without frames → actively close connection —
// when web page crashes/disconnects, Node side doesn't hang (previously could only wait for
// SCTP timeout, which can be tens of seconds in no-STUN environments). Discovery: code review 2026-08-18 (item 4).
const KEEPALIVE_INTERVAL = 5000
const KEEPALIVE_TIMEOUT = 15000

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// createPeerMediaServer starts the Node-side resource provider.
//
// options:
//   peerId    — this node's ID on the signaling server (web side connects to this id)
//   signaling — signaling configuration (defaults to public cloud 0.peerjs.com)
//   allow     — URL whitelist (url) => boolean, default deny all, must be explicitly configured
//   fetchImpl — custom fetch (for test injection; default globalThis.fetch)
//   chunkSize — data block size (default CHUNK_SIZE 64KB)
//   onRequest — logging hook ({url, peer, status, bytes, ms})
//
// Returns { peer, close }. close() destroys peer (disconnect + cleanup).
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
    // Node has no location, peerjs isSecure() causes ReferenceError — force explicit
    // (note: don't use !signaling.secure to check: explicit secure:false is a valid value)
    throw new Error('peerdrive-media: signaling.secure must be explicitly set (true/false) in Node')
  }

  const PeerCtor = await loadPeerjs()

  const peer = new PeerCtor(peerId, {
    host: signaling.host,
    port: signaling.port,
    secure: signaling.secure,
    key: signaling.key,
    path: signaling.path || '/',
    // Diagnostics: output peerjs ICE/signaling logs when PEERDRIVE_MEDIA_DEBUG=1
    debug: process.env.PEERDRIVE_MEDIA_DEBUG ? 3 : 0,
  })

  await new Promise((resolve, reject) => {
    const to = setTimeout(() => reject(new Error(`peerdrive-media: signaling connect timeout (${signaling.host}:${signaling.port})`)), 15000)
    peer.once('open', () => { clearTimeout(to); resolve() })
    peer.once('error', (err) => { clearTimeout(to); reject(new Error(`peerdrive-media: signaling error: ${err?.type || err}`)) })
  })

  // serveConnection handles a single web-side DataConnection.
  // Multi-DataChannel architecture: each connection independently handles one file request, supporting concurrency.
  const serveConnection = (conn) => {
    let lastActive = Date.now()
    // keepalive: send ping to generate traffic + actively disconnect on timeout (see KEEPALIVE comments in file header)
    const ka = setInterval(() => {
      if (Date.now() - lastActive > KEEPALIVE_TIMEOUT) {
        clearInterval(ka)
        try { conn.close() } catch { /* idempotent */ }
        return
      }
      try { conn.send(JSON.stringify({ type: 'ping' })) } catch { /* connection dead */ }
    }, KEEPALIVE_INTERVAL)

    conn.on('data', (data) => {
      lastActive = Date.now() // any frame refreshes active
      if (typeof data !== 'string') return // binary frames should not be sent by web side
      const msg = parseFrame(data)
      if (!msg) return

      // Control frame: ping-ack (browser replies to Node's ping)
      if (msg.type === 'ping-ack') return

      // File request: url
      if (msg.type === 'url') {
        handleUrlRequest(conn, msg).catch((err) => {
          try { conn.send(JSON.stringify({ type: 'err', msg: err.message, reqId: msg.reqId })) } catch { /* channel dead */ }
        })
      }
    })
    conn.on('close', () => { clearInterval(ka) })
  }
  peer.on('connection', serveConnection)

  // handleUrlRequest fetches URL and streams response: meta → blocks×N → done.
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
        // Streaming read + chunking + backpressure: blocks sent directly (raw mode has no chunker fragmentation).
        // chunk is Uint8Array (Node fetch stream), wrtc send accepts ArrayBufferView.
        for await (const chunk of resp.body) {
          const bytes = chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk)
          for (let off = 0; off < bytes.length; off += chunkSize) {
            const piece = bytes.subarray(off, off + chunkSize)
            conn.send(piece)
            sent += piece.length
            while (conn.dataChannel && conn.dataChannel.bufferedAmount > LOW_WATER) {
              await sleep(10) // backpressure: pause stream reading when peer is slow
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
      try { peer.destroy() } catch { /* idempotent */ }
    },
  }
}

// allowHttp convenient whitelist: allows http(s) URLs with specified prefixes.
export const allowPrefix = (prefixes) => (url) =>
  prefixes.some((p) => url.startsWith(p))

export default createPeerMediaServer
