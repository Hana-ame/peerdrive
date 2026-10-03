// client.js — peerdrive pure browser consumer: connects to a peerdrive node, lists its shared
// content and fetches files. **Zero dependencies**, no local backend required.
//
// Positioning (complements the back /peerjs/* client path):
//   - Backend nodes (Go) interconnect using the same frame protocol, are "producers/holders";
//   - This package is a **pure consumer**: a browser page (or any environment with WebRTC DataChannel)
//     connects directly to a node, uses share frames to see the manifest, and req frames to fetch content.
//     Corresponds to the requirement "webrtc pure client consumer, can fetch files from this p2p network".
//
// Transport-agnostic design: this class doesn't know PeerJS. It only requires the passed-in object to satisfy
//   { on(type, cb), send(data), open?: boolean, close?() }
// —— PeerJS's DataConnection naturally satisfies this. Benefits:
//   1. Package has zero dependencies, doesn't add peerjs to the consumer's bundle size;
//   2. Tests can cover the entire state machine with fake connections (no real WebRTC/signaling server needed);
//   3. Future transport swaps (raw RTCPeerConnection / WebTransport) don't require changing this file.
//
// Frame protocol details in src/protocol.js header comments (aligned word-for-word with Go-side conn.go).

import { Sha256 } from './sha256.js'
import {
  DEFAULT_MAX_BUFFER_BYTES,
  MAX_FILE_BYTES,
  UPLOAD_CHUNK,
  baseName as baseNameOf,
  concatChunks,
  guessMime,
  isBinaryFrame,
  isValidHash,
  nextReqId,
  parseFrame,
  pskAuthFrame,
  pullFrame,
  reqFrame,
  shareFrame,
  toUint8Array,
  uploadFrame,
} from './protocol.js'
// Note: don't re-export PSK_REQUIRED here — index.js is
// `export * from './protocol.js'` + `export * from './client.js'`, star-exporting the same name
// from two places creates "ambiguous export" and actually disappears from the package entry.

/** Error codes. UI branches by code, don't match message text. */
export const ERR = {
  INVALID_HASH: 'INVALID_HASH', // hash not 64 lowercase hex (blocked locally, no request sent)
  TOO_LARGE: 'TOO_LARGE', // exceeds maxBufferBytes / protocol limit 8GB
  HASH_MISMATCH: 'HASH_MISMATCH', // content doesn't match hash (peer data corrupted or tampered)
  INCOMPLETE: 'INCOMPLETE', // bytes declared in peer's done don't match actual received
  TIMEOUT: 'TIMEOUT', // idle timeout (peer stuck/connection half-dead)
  CLOSED: 'CLOSED', // connection closed
  PROTOCOL: 'PROTOCOL', // frame invalid (peer implements a different protocol)
  PEER: 'PEER', // peer returned err frame (not found / unauthorized / read failure…)
  CANCELLED: 'CANCELLED', // local cancellation (abort / early break in iteration)
  // PSK_REQUIRED: peer has pre-shared key auth, and we didn't present (or presented wrong) one.
  // Separate code because its remediation is "enter the key", completely different from "file not found".
  PSK_REQUIRED: 'PSK_REQUIRED',
}

export class PeerDriveError extends Error {
  constructor(message, code = ERR.PROTOCOL, extra = {}) {
    super(message)
    this.name = 'PeerDriveError'
    this.code = code
    Object.assign(this, extra)
  }
}

const DEFAULTS = {
  // Idle timeout: how long without receiving any frame for this request before declaring dead.
  // Go-side is 5 minutes (server patience), consumer side takes smaller — users staring at a
  // frozen progress bar for 5 minutes is worse UX.
  idleTimeoutMs: 120_000,
  openTimeoutMs: 20_000,
  verbTimeoutMs: 15_000, // same as Go-side verbWaitTimeout (share-type small JSON responses)
  maxBufferBytes: DEFAULT_MAX_BUFFER_BYTES,
}

/**
 * ChunkQueue bridges "push-mode" frame callbacks into "pull-mode" async iteration.
 * stream() uses it for backpressure: if the consumer doesn't take, chunks stay in the queue
 * (instead of infinitely filling memory).
 */
class ChunkQueue {
  constructor() {
    this.buf = []
    this.pending = []
    this.state = 'open'
    this.err = null
  }

  push(v) {
    if (this.state !== 'open') return
    const w = this.pending.shift()
    if (w) w({ value: v, done: false })
    else this.buf.push(v)
  }

  close() {
    this._settle(null)
  }

  abort(err) {
    // On failure discard cached chunks: no one will consume them, keeping them only holds memory
    // (typical scenario: TOO_LARGE triggers mid-stream, queue may already hold tens of MB)
    this.buf.length = 0
    this._settle(err)
  }

  _settle(err) {
    if (this.state !== 'open') return
    this.state = err ? 'failed' : 'ended'
    this.err = err
    while (this.pending.length) this.pending.shift()({ value: undefined, done: true, err })
  }

  shift() {
    if (this.buf.length) return Promise.resolve({ value: this.buf.shift(), done: false })
    if (this.state !== 'open') return Promise.resolve({ value: undefined, done: true, err: this.err })
    return new Promise((res) => this.pending.push(res))
  }
}

/** Consumer client: one instance per connection. */
export class PeerDriveClient {
  constructor(conn, opts = {}) {
    if (!conn || typeof conn.send !== 'function' || typeof conn.on !== 'function') {
      throw new TypeError('peerdrive-client: conn must implement { on(type, cb), send(data) }')
    }
    this.conn = conn
    this.opts = { ...DEFAULTS, ...opts }
    this.peerId = conn.peer || conn.peerId || opts.peerId || ''
    this.stats = { requests: 0, chunks: 0, bytes: 0, failures: 0 }

    // PSK auth state (doc/NETDISK.md "PSK Auth"):
    //   none = no key configured (if peer has auth, will receive PSK_REQUIRED error)
    //   sent = presented, waiting for peer acknowledgment (don't wait for it before sending business frames,
    //          rely on DataChannel ordering)
    //   ok   = peer accepted; err = peer rejected (wrong key), pskError is its msg
    this.psk = typeof opts.psk === 'string' ? opts.psk : ''
    this.pskState = this.psk ? 'pending' : 'none'
    this.pskError = null

    this._pend = new Map() // reqId → fetch state
    this._verbs = new Map() // reqId → one-shot response wait slot (share/pull etc.)
    this._uploads = new Map() // reqId → upload session (local ingest)
    this._expect = null // connection-level expect: next binary block belongs to whom (see protocol.js constraint 2)
    this._openWaiters = []
    this._closeErr = null
    this._openState = conn.open === true ? true : null // null = unknown
    this._ownedPeer = null
    this._bind()
    // Connections already open when passed in won't fire 'open' event again, send here to compensate
    if (conn.open === true) this._sendPskAuth()
  }

  /**
   * _sendPskAuth presents pre-shared key (only if configured, and must be the first frame from this side).
   * See protocol.js's pskAuthFrame comments: relies on DataChannel ordering, don't wait for acknowledgment.
   */
  _sendPskAuth() {
    if (!this.psk || this.pskState !== 'pending') return
    try {
      this.conn.send(pskAuthFrame(this.psk))
      this.pskState = 'sent'
    } catch {
      this.pskState = 'none' // can't send, treat as not configured: let peer tell us via err
    }
  }

  get isOpen() {
    return this._openState === true && !this._closeErr
  }

  /**
   * localPeerId our temporary id on signaling — UI displays "who am I" using this.
   *
   * Discovery: the panel first version displayed `conn.peer` as its own id, but the screen showed the
   * peer's node name (DataConnection.peer refers to the **remote**). When using connectToPeer to
   * connect, our local Peer is held by this class, so we get it from there; when not created by this class
   * (externally passed conn), can only return empty string, caller should hold the Peer instance themselves.
   */
  get localPeerId() {
    return (this._ownedPeer && this._ownedPeer.id) || ''
  }

  /** ready waits for connection ready. Resolves immediately if already ready. */
  ready(timeoutMs = this.opts.openTimeoutMs) {
    if (this._closeErr) return Promise.reject(this._closeErr)
    if (this._openState === true) return Promise.resolve(this)
    return new Promise((resolve, reject) => {
      const entry = { resolve, reject, timer: null }
      this._openWaiters.push(entry)
      if (timeoutMs > 0) {
        entry.timer = setTimeout(() => {
          this._openWaiters = this._openWaiters.filter((w) => w !== entry)
          reject(new PeerDriveError(`Connection not established within ${timeoutMs}ms`, ERR.TIMEOUT))
        }, timeoutMs)
      }
    })
  }

  /**
   * shares queries peer's share manifest (the last step of "node marketplace → join node → see file links").
   * Returns {collections, files, dirs, total}.
   *
   * Semantics reminder: empty manifest is a **valid result** (peer hasn't enabled sharing / declared
   * any directories), not an error — UI should render "this node has no shared content". Peer really
   * fails only via PEER/TIMEOUT.
   */
  async shares({ timeoutMs = this.opts.verbTimeoutMs } = {}) {
    const reqId = nextReqId()
    const frame = await this._requestVerb(reqId, shareFrame(reqId), timeoutMs)
    if (!frame || frame.type !== 'share-resp') {
      throw new PeerDriveError('Peer share response format abnormal (peer may not be a peerdrive node)', ERR.PROTOCOL)
    }
    const collections = Array.isArray(frame.collections) ? frame.collections : []
    const files = Array.isArray(frame.files) ? frame.files : []
    return {
      peerId: this.peerId,
      collections,
      files,
      dirs: Array.isArray(frame.dirs) ? frame.dirs : [],
      total: Number.isFinite(frame.total) ? frame.total : collections.length + files.length,
    }
  }

  /**
   * put puts content into the node's library (**local ingest**).
   *
   * Uses Go-side upload verb: send header → wait meta → send binary chunks → wait ack,
   * progressing chunk by chunk until server returns uploaded{hash,size,path}. The back-and-forth
   * acknowledgment rather than one-shot: the receiver needs to flush to disk at its own pace
   * (flow control is the receiver's call, see protocol.js's uploadFrame comments), and only one
   * upload can be in progress on this connection at a time.
   *
   * Returns {hash,size,name,path} — hash is the content address, can fetch back with fetch() later.
   * Note default maxPutBytes gate: for oversized content, slice it yourself first or use another channel.
   *
   * @param {Uint8Array|ArrayBuffer|Blob|File} data
   * @param {{name?:string,onProgress?:(sent:number,total:number)=>void,
   *          signal?:AbortSignal,timeoutMs?:number,chunkTimeoutMs?:number}} [opts]
   */
  async put(data, opts = {}) {
    const bytes = await readableBytes(data)
    const name = opts.name || (typeof data === 'object' && data !== null && data.name) || 'upload.bin'
    const { onProgress, signal, chunkTimeoutMs = this.opts.verbTimeoutMs } = opts
    if (this._closeErr) throw this._closeErr
    if (signal && signal.aborted) throw new PeerDriveError('Cancelled', ERR.CANCELLED)

    const reqId = nextReqId()
    const state = { reqId, emit: null, timer: null }
    // One round = one server frame for this upload. Each round times out separately: overall timeout
    // can't distinguish "slow" from "stuck", and their remediation is completely different (slow can keep
    // waiting, stuck must error and retry).
    const round = () =>
      new Promise((resolve, reject) => {
        state.emit = { resolve, reject }
        state.timer = setTimeout(() => {
          this._uploads.delete(reqId)
          state.emit = null
          reject(new PeerDriveError(`Upload ${chunkTimeoutMs}ms no response`, ERR.TIMEOUT))
        }, chunkTimeoutMs)
      })
    const stopRound = () => {
      if (state.timer) clearTimeout(state.timer)
      state.timer = null
      state.emit = null
      this._uploads.delete(reqId)
    }
    this._uploads.set(reqId, state)

    let offset = 0
    const size = bytes.byteLength
    // Cancellation: fail the "in-flight round" immediately. Previously only checked signal once at entry,
    // result: UI cancel button after transfer starts was useless (had to wait full round to take effect,
    // looked like it was stuck).
    const onAbort = signal
      ? () => {
          const u = this._uploads.get(reqId)
          if (u) this._failUpload(u, new PeerDriveError('Cancelled', ERR.CANCELLED))
        }
      : null
    if (onAbort) signal.addEventListener('abort', onAbort, { once: true })
    try {
      // First frame header. size=0 (empty file) → server directly returns uploaded, skips meta.
      this._send(uploadFrame(reqId, name, size, 0))
      for (;;) {
        const frame = await round()
        if (state.timer) clearTimeout(state.timer)
        state.timer = null
        state.emit = null

        if (frame.type === 'err') {
          const code = frame.code === ERR.PSK_REQUIRED ? ERR.PSK_REQUIRED : ERR.PEER
          throw new PeerDriveError(frame.msg || 'Upload failed', code)
        }
        if (frame.type === 'uploaded') {
          stopRound()
          return {
            hash: frame.hash || '',
            size: Number(frame.total) || size,
            name: frame.name || name,
            path: frame.path || '',
          }
        }
        if (frame.type === 'meta') {
          // Server authorization: from here write n bytes (it decides granularity, follows UPLOAD_CHUNK).
          // Its offset is authoritative — if same content was partially sent before, server requests
          // continuation from the contiguous write breakpoint; self-incrementing writes bytes to the wrong
          // position, producing a mismatched duplicate (and hangs in a place where we're not even at fault).
          const srv = Number(frame.offset)
          if (Number.isFinite(srv) && srv >= 0) offset = srv
          if (offset > size) throw new PeerDriveError(`Server requests resume from ${offset}, but content is only ${size} bytes`, ERR.PROTOCOL)
          const n = Math.min(UPLOAD_CHUNK, size - offset)
          if (n <= 0) throw new PeerDriveError('Server meta offset exceeds file size', ERR.PROTOCOL)
          this.conn.send(bytes.subarray(offset, offset + n))
          offset += n
          this.stats.chunks++
          this.stats.bytes += n
          if (onProgress) onProgress(offset, size)
          continue // wait for this chunk's ack (or last chunk's uploaded)
        }
        if (frame.type === 'ack') {
          if (offset >= size) continue // last chunk's completion acknowledgment not arrived yet, keep waiting
          this._send(uploadFrame(reqId, name, size, offset))
          continue
        }
        throw new PeerDriveError(`Upload interrupted by unknown frame: ${frame.type}`, ERR.PROTOCOL)
      }
    } catch (e) {
      stopRound()
      throw e instanceof Error ? e : new Error(String(e))
    } finally {
      if (onAbort) signal.removeEventListener('abort', onAbort)
    }
  }

  /**
   * pull asks node to fetch a URL for you and ingest (**network ingest**).
   *
   * Typical scenario: panel has only a link, no content (or content is on the other end of the net),
   * let a network-capable node fetch it. Node side does SSRF protection (public http/https only,
   * no internal/localhost, per-hop redirect validation) with size limits, so these failures are
   * **expected behavior**, not bugs — error messages pass through directly to caller.
   *
   * @param {string} url
   * @param {{name?:string,timeoutMs?:number}} [opts]
   * @returns {Promise<{hash:string,size:number,name:string,path:string}>}
   */
  async pull(url, opts = {}) {
    if (typeof url !== 'string' || !url.trim()) {
      throw new PeerDriveError('pull requires a non-empty URL', ERR.INVALID_HASH)
    }
    const reqId = nextReqId()
    const frame = await this._requestVerb(reqId, pullFrame(reqId, url.trim(), opts.name || ''), opts.timeoutMs || this.opts.verbTimeoutMs)
    if (!frame || frame.type !== 'pulled') {
      throw new PeerDriveError('Peer pull response format abnormal (version too old?)', ERR.PROTOCOL)
    }
    return { hash: frame.hash, size: Number(frame.total) || 0, name: frame.name || '', path: frame.path || '' }
  }

  /**
   * stream streaming fetch: yields Uint8Array chunks one by one, **doesn't keep entire content in memory**.
   * Suitable for large files (with File System Access API / StreamSaver for direct disk write).
   *
   * Full request (offset=0 without size) verifies sha256 at end, throws HASH_MISMATCH on mismatch —
   * verification is incremental (see sha256.js), no need to look back at previous chunks.
   *
   * opts: {offset, size, maxBytes, onProgress(received, total), signal}
   * Consumer-side early break cancels this request (subsequent arriving chunks are discarded).
   */
  async *stream(hash, opts = {}) {
    this._assertHash(hash)
    const { offset = 0, size = -1, signal } = opts
    // Already-aborted signal: reject **before sending request**. Sending first then checking wastes a fetch
    // (and peer keeps sending to done, wasting bandwidth).
    if (signal && signal.aborted) throw new PeerDriveError('Cancelled', ERR.CANCELLED)
    const reqId = nextReqId()
    const p = this._startFetch(reqId, hash, { ...opts, offset, size })

    if (signal) {
      p.signal = signal
      p.onAbort = () => this._cancel(reqId, new PeerDriveError('Cancelled', ERR.CANCELLED))
      signal.addEventListener('abort', p.onAbort, { once: true })
    }

    try {
      for (;;) {
        const { value, done, err } = await p.queue.shift()
        if (done) {
          if (err) throw err
          return
        }
        yield value
      }
    } finally {
      // On normal end/failure _pend no longer has it, this is idempotent cleanup fallback;
      // consumer-side early break also reaches here, releasing cached chunks with the request.
      // Failure counting is handled by _failPending, don't double-count here (user-initiated cancel doesn't count).
      this._cancel(reqId, new PeerDriveError('Cancelled', ERR.CANCELLED))
    }
  }

  /**
   * fetch fetch entire content (Uint8Array). Equivalent to collecting all of stream().
   * Has memory gate: exceeding maxBytes (default 256MB) throws TOO_LARGE — use stream() for large files.
   */
  async fetch(hash, opts = {}) {
    const chunks = []
    let received = 0
    for await (const chunk of this.stream(hash, opts)) {
      chunks.push(chunk)
      received += chunk.byteLength
    }
    return concatChunks(chunks, received)
  }

  /** fetchBlob fetch as Blob (for browser preview/playback; MIME can be guessed from name). */
  async fetchBlob(hash, { name = '', mime = '', ...opts } = {}) {
    const bytes = await this.fetch(hash, opts)
    return new Blob([bytes], { type: mime || guessMime(name) })
  }

  /** fetchText fetch as text (utf-8). */
  async fetchText(hash, opts = {}) {
    const bytes = await this.fetch(hash, opts)
    return new TextDecoder('utf-8').decode(bytes)
  }

  /**
   * saveAs triggers browser download. Uses Blob + <a download>, so **entire content is in memory**;
   * shares the same maxBytes gate as fetch. Use stream() for oversized files and write to disk yourself.
   */
  async saveAs(hash, filename = '', opts = {}) {
    if (typeof document === 'undefined') {
      throw new PeerDriveError('saveAs requires DOM environment', ERR.PROTOCOL)
    }
    const blob = await this.fetchBlob(hash, { name: filename, ...opts })
    const url = URL.createObjectURL(blob)
    try {
      const a = document.createElement('a')
      a.href = url
      a.download = filename || hash.slice(0, 12)
      a.rel = 'noopener'
      document.body.appendChild(a)
      a.click()
      a.remove()
    } finally {
      // Immediate revoke causes some browsers (Safari) to cancel download: give some margin
      setTimeout(() => URL.revokeObjectURL(url), 30_000)
    }
    return blob.size
  }

  /** Convenience method: directly fetch and save a share manifest entry/file (auto-extracts filename). */
  async saveShare(item, opts = {}) {
    if (!item || !item.hash) throw new PeerDriveError('saveShare requires an entry with hash', ERR.PROTOCOL)
    const name = baseNameOf(item.name || item.path || '')
    return this.saveAs(item.hash, name, opts)
  }

  /** close closes the connection (if this instance created the Peer object, destroy it too). */
  close() {
    this._unbind()
    this._failAll(new PeerDriveError('Connection closed', ERR.CLOSED))
    try {
      if (typeof this.conn.close === 'function') this.conn.close()
    } catch {
      /* connection already dead */
    }
    if (this._ownedPeer && typeof this._ownedPeer.destroy === 'function') {
      try {
        this._ownedPeer.destroy()
      } catch {
        /* already destroyed */
      }
    }
    this._openState = false
  }

  // ── Internal: connection binding and frame dispatch ────────────────────────────────────────

  _bind() {
    this._onData = (data) => {
      // Frame order IS protocol semantics (see protocol.js constraint 2), must process synchronously, no queuing
      try {
        if (isBinaryFrame(data)) this._onChunk(data)
        else this._onText(data)
      } catch (e) {
        this.stats.failures++
        this._failAll(e instanceof Error ? e : new Error(String(e)))
      }
    }
    this._onOpen = () => {
      this._openState = true
      // PSK: must be presented **before any business frame** (including first request after ready()).
      // Placed before flush because flush resolves waiters whose subsequent sends are queued after
      // this send — order IS auth semantics.
      this._sendPskAuth()
      this._flushOpenWaiters(null)
    }
    this._onClose = () => this._failAll(new PeerDriveError('Connection closed', ERR.CLOSED))
    this._onError = (err) => {
      const msg = err && (err.message || err.type) ? err.message || err.type : String(err)
      this._failAll(new PeerDriveError(`Connection error: ${msg}`, ERR.CLOSED))
    }
    this.conn.on('data', this._onData)
    this.conn.on('open', this._onOpen)
    this.conn.on('close', this._onClose)
    this.conn.on('error', this._onError)
  }

  _unbind() {
    const off = this.conn.off || this.conn.removeListener
    if (typeof off !== 'function') return
    off.call(this.conn, 'data', this._onData)
    off.call(this.conn, 'open', this._onOpen)
    off.call(this.conn, 'close', this._onClose)
    off.call(this.conn, 'error', this._onError)
  }

  _flushOpenWaiters(err) {
    const waiters = this._openWaiters
    this._openWaiters = []
    for (const w of waiters) {
      if (w.timer) clearTimeout(w.timer)
      if (err) w.reject(err)
      else w.resolve(this)
    }
  }

  _onText(text) {
    const frame = parseFrame(text)
    if (!frame) return // not this protocol's frame: ignore (same connection may have other purposes)
    switch (frame.type) {
      case 'meta': {
        // meta is ambiguous: could be "fetch ready" or "upload chunk authorized".
        // Route by reqId ownership — misrouting makes uploader wait for timeout, or fetcher gets unknown frame.
        const u = frame.reqId ? this._uploads.get(frame.reqId) : null
        return u ? this._settleUpload(u, frame) : this._onMeta(frame)
      }
      case 'ack':
      case 'uploaded': {
        const u = frame.reqId ? this._uploads.get(frame.reqId) : null
        if (u) return this._settleUpload(u, frame)
        return // not ours: forward compatibility, ignore
      }
      case 'data':
        return this._onDataHead(frame)
      case 'done':
        return this._onDone(frame)
      case 'err':
        return this._onErr(frame)
      case 'share-resp':
      case 'pulled':
        return this._onVerbReply(frame)
      case 'psk-ok':
        this.pskState = 'ok'
        this.pskError = null
        return
      case 'psk-err':
        this.pskState = 'err'
        this.pskError = frame.msg || 'psk: peer rejected the key'
        return
      default:
        // Forward compatibility for unknown frame types only holds for frames "not about me".
        // Unknown frames **carrying my pending reqId** must error: peer replied with an unknown type
        // at my explicitly requested response slot, the only reasonable explanation is protocol version
        // mismatch. If ignored here, caller only sees TIMEOUT ("peer is stuck"), while the real cause
        // is peer speaking a different language — debugging direction completely wrong.
        return this._rejectOwner(frame.reqId, (t) => new PeerDriveError(
          `Peer responded with unknown frame ${t} (protocol version mismatch?)`,
          ERR.PROTOCOL,
        ))
    }
  }

  /**
   * _rejectOwner finds the waiting session by reqId and makes it fail.
   *
   * All three wait slots (upload step/one-shot verb/fetch) may hold the same reqId space,
   * so check them in order here; if none found, it's a late or unrelated frame, ignore.
   */
  _rejectOwner(reqId, makeErr) {
    if (!reqId) return
    const u = this._uploads.get(reqId)
    if (u) return this._failUpload(u, makeErr(u.reqId))
    const v = this._verbs.get(reqId)
    if (v) return this._settleVerb(reqId, null, makeErr(reqId))
    const p = this._pend.get(reqId)
    if (p) return this._failPending(p, makeErr(reqId))
  }

  _onMeta(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return
    p.touch()
    const total = Number(frame.total)
    // total of -1 means peer also can't determine size (multi-source origin-pull scenario) — not an error
    if (Number.isFinite(total) && total > MAX_FILE_BYTES) {
      return this._failPending(p, new PeerDriveError(`Peer declares file ${total} bytes, exceeds protocol limit`, ERR.TOO_LARGE))
    }
    if (Number.isFinite(total) && total > p.maxBytes) {
      return this._failPending(
        p,
        new PeerDriveError(
          `File ${total} bytes exceeds local memory gate ${p.maxBytes} (use stream() for streaming disk write, or increase maxBytes)`,
          ERR.TOO_LARGE,
        ),
      )
    }
    p.total = Number.isFinite(total) ? total : -1
    this._reportProgress(p)
  }

  _onDataHead(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return // late frame (cancelled/ended): discard
    p.touch()
    const size = Number(frame.size)
    // Same limit as Go-side H6: block size must be positive and not exceed protocol limit, otherwise
    // it's malicious/wrong protocol
    if (!Number.isFinite(size) || size <= 0 || size > MAX_FILE_BYTES) {
      return this._failPending(p, new PeerDriveError(`Invalid data block size ${frame.size}`, ERR.PROTOCOL))
    }
    p.blockSize = size
    p.blockGot = 0
    // Connection-level expect: attach the following binary block to this request
    this._expect = p
  }

  _onChunk(data) {
    const p = this._expect
    if (!p) return // data block without preceding data header: not protocol content, discard
    const bytes = toUint8Array(data)
    // Note: **don't** store the block in request state here. Blocks only enter the bounded queue
    // (backpressure), consumed by the consumer — "streaming" benefit is all here; storing an extra
    // copy in state equals reverting to keeping everything in memory.
    p.queue.push(bytes)
    p.received += bytes.byteLength
    p.blockGot += bytes.byteLength
    if (p.blockGot >= p.blockSize) this._expect = null
    this.stats.chunks++
    this.stats.bytes += bytes.byteLength
    p.touch()
    if (p.hasher) p.hasher.update(bytes)
    this._reportProgress(p)
    if (p.maxBytes > 0 && p.received > p.maxBytes) {
      this._failPending(
        p,
        new PeerDriveError(`Content exceeds local memory gate ${p.maxBytes} (use stream())`, ERR.TOO_LARGE),
      )
    }
  }

  _onDone(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return
    p.touch()
    const declared = Number(frame.size)
    // Same integrity gate as Go-side: peer sending early done returns truncated content as success (silent corruption)
    if (Number.isFinite(declared) && declared >= 0 && p.received !== declared) {
      return this._failPending(
        p,
        new PeerDriveError(`Incomplete transfer: received ${p.received} bytes, peer declared ${declared} bytes`, ERR.INCOMPLETE, {
          received: p.received,
          declared,
        }),
      )
    }
    if (p.verify && p.hasher) {
      const got = p.hasher.digestHex()
      if (got !== p.hash) {
        return this._failPending(
          p,
          new PeerDriveError(`Content doesn't match hash (expected ${p.hash}, got ${got})`, ERR.HASH_MISMATCH, {
            expected: p.hash,
            actual: got,
          }),
        )
      }
    }
    this._finishPending(p)
  }

  _onErr(frame) {
    const msg = frame.msg || 'Peer returned error'
    // err frame may carry code (Go-side auth returns PSK_REQUIRED). Classify by code not text:
    // text changes with versions, code is part of the protocol.
    const code = frame.code === ERR.PSK_REQUIRED ? ERR.PSK_REQUIRED : ERR.PEER
    const err = () => new PeerDriveError(msg, code)
    // err frame's reqId may belong to fetch, one-shot verb, or upload — check all three,
    // missing upload means put() hangs until timeout instead of immediately delivering server's rejection reason.
    if (frame.reqId) {
      const u = this._uploads.get(frame.reqId)
      if (u) return this._settleUpload(u, frame)
      const v = this._verbs.get(frame.reqId)
      if (v) return this._settleVerb(frame.reqId, null, err())
      const p = this._pend.get(frame.reqId)
      if (p) return this._failPending(p, err())
    }
  }

  _settleUpload(state, frame) {
    if (state.timer) {
      clearTimeout(state.timer)
      state.timer = null
    }
    state.emit?.resolve(frame)
    state.emit = null
  }

  /** _failUpload makes upload step end with error, also cancels this round's timer. */
  _failUpload(state, err) {
    if (state.timer) {
      clearTimeout(state.timer)
      state.timer = null
    }
    this._uploads.delete(state.reqId)
    state.emit?.reject(err)
    state.emit = null
  }

  _onVerbReply(frame) {
    if (!frame.reqId) return
    this._settleVerb(frame.reqId, frame, null)
  }

  // ── Internal: request lifecycle ───────────────────────────────────────────────────────────

  _assertHash(hash) {
    if (!isValidHash(hash)) {
      throw new PeerDriveError(
        `hash must be 64 lowercase hex sha256, received ${JSON.stringify(hash)}`,
        ERR.INVALID_HASH,
      )
    }
  }

  _send(frameText) {
    if (this._closeErr) throw this._closeErr
    this.conn.send(frameText)
  }

  _startFetch(reqId, hash, opts) {
    const { offset = 0, size = -1, maxBytes, onProgress } = opts
    const p = {
      reqId,
      hash,
      offset,
      size,
      queue: new ChunkQueue(),
      received: 0,
      total: -1,
      blockSize: 0,
      blockGot: 0,
      // Only verify content-address for "full requests": range request fragments don't equal the entire
      // content's digest (same condition as Go-side fetchReader.verify)
      verify: offset === 0 && size < 0,
      hasher: null,
      maxBytes: Number.isFinite(maxBytes) ? maxBytes : this.opts.maxBufferBytes,
      onProgress,
      idleTimer: null,
      onAbort: null,
      settledBy: null,
    }
    if (p.verify) p.hasher = new Sha256()
    p.touch = () => this._armIdle(p)
    this._pend.set(reqId, p)
    this._armIdle(p)
    this.stats.requests++
    try {
      this._send(reqFrame(hash, { offset, size, reqId }))
    } catch (e) {
      this._cancel(reqId, e instanceof Error ? e : new Error(String(e)))
    }
    return p
  }

  _armIdle(p) {
    if (p.idleTimer) clearTimeout(p.idleTimer)
    p.idleTimer = setTimeout(() => {
      this._failPending(
        p,
        new PeerDriveError(`Fetch ${p.hash.slice(0, 12)}… idle timeout (${this.opts.idleTimeoutMs}ms no data)`, ERR.TIMEOUT),
      )
    }, this.opts.idleTimeoutMs)
  }

  _reportProgress(p) {
    if (typeof p.onProgress === 'function') {
      try {
        p.onProgress(p.received, p.total, p.hash)
      } catch {
        /* progress callback errors shouldn't affect transfer */
      }
    }
  }

  _finishPending(p) {
    if (p.settledBy) return
    p.settledBy = 'done'
    this._detach(p)
    p.queue.close()
  }

  _failPending(p, err) {
    if (p.settledBy) return
    p.settledBy = 'fail'
    this.stats.failures++
    this._detach(p)
    p.queue.abort(err)
  }

  _cancel(reqId, err) {
    const p = this._pend.get(reqId)
    if (!p) return
    if (p.settledBy) return
    p.settledBy = 'cancel'
    this._detach(p)
    p.queue.abort(err)
  }

  _detach(p) {
    if (p.idleTimer) {
      clearTimeout(p.idleTimer)
      p.idleTimer = null
    }
    if (p.onAbort && p.signal) p.signal.removeEventListener('abort', p.onAbort)
    // Gotcha: don't wait for peer to stop sending. Protocol has no "cancel" frame, peer continues
    // sending this request to completion, we just drop subsequent frames (_expect/_pend no longer find it).
    // To truly interrupt, must close the connection.
    if (this._expect === p) this._expect = null
    this._pend.delete(p.reqId)
  }

  _failAll(err) {
    this._closeErr = this._closeErr || err
    this._openState = false
    this._flushOpenWaiters(err)
    for (const [, v] of this._verbs) {
      if (v.timer) clearTimeout(v.timer)
      v.reject(err)
    }
    this._verbs.clear()
    for (const p of [...this._pend.values()]) this._failPending(p, err)
    // In-flight uploads must also end with error: otherwise put() waits for its own timeout,
    // while the real cause (connection dead) is masked as TIMEOUT, debugging in completely wrong direction.
    for (const u of [...this._uploads.values()]) {
      clearTimeout(u.timer)
      u.emit?.reject(err)
      u.emit = null
    }
    this._uploads.clear()
  }

  _settleVerb(reqId, frame, err) {
    const v = this._verbs.get(reqId)
    if (!v) return
    this._verbs.delete(reqId)
    if (v.timer) clearTimeout(v.timer)
    if (err) v.reject(err)
    else v.resolve(frame)
  }

  _requestVerb(reqId, frameText, timeoutMs) {
    return new Promise((resolve, reject) => {
      const entry = { resolve, reject, timer: null }
      this._verbs.set(reqId, entry)
      entry.timer = setTimeout(() => {
        this._verbs.delete(reqId)
        reject(new PeerDriveError(`Peer ${timeoutMs}ms no response`, ERR.TIMEOUT))
      }, timeoutMs)
      try {
        this._send(frameText)
      } catch (e) {
        this._verbs.delete(reqId)
        clearTimeout(entry.timer)
        reject(e instanceof Error ? e : new Error(String(e)))
      }
    })
  }
}

/**
 * readableBytes normalizes various "content sources" to Uint8Array.
 *
 * Supported types cover what humans actually put in: typed array / ArrayBuffer /
 * Blob / File / string. File is the only entry point for the panel's "select file upload" path.
 */
async function readableBytes(data) {
  if (data instanceof Uint8Array) return data
  if (data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof data === 'string') return new TextEncoder().encode(data)
  // Blob / File / Response and any Blob-like proxy: duck-type by "has arrayBuffer()"
  // instead of instanceof Blob —— cross-realm (iframe / unit test proxies)
  // instanceof fails, but "can get bytes" is the only contract we care about here.
  if (data && typeof data === 'object' && typeof data.arrayBuffer === 'function') {
    return new Uint8Array(await data.arrayBuffer())
  }
  throw new TypeError('put: data must be Uint8Array / ArrayBuffer / Blob / File / string')
}

/**
 * discoverNodes asks self-hosted signaling "which nodes are online now" — consumer-side auto-discovery entry.
 *
 * Uses signaling's REST discovery endpoint GET /discover/nodes (built-in discovery, replacing public MQTT broker).
 * Returns [{peerId,lastSeen,nodeType,collections,uptime,loadInfo}].
 *
 * ⚠️ An unavoidable prerequisite: **signaling must serve cross-origin responses**. The panel is a public
 * static page, file:// gives origin `null`, hosted on Pages is another domain, and this step is a cross-origin
 * request — if signaling doesn't have Access-Control-Allow-Origin, the browser fails at the fetch level,
 * with no status code at all (NetworkError). This is the most common reason auto-discovery doesn't work,
 * so when it fails independently it needs a clear message (see NOT_CORS handling below).
 *
 * @param {{host:string,port?:number|string,secure?:boolean}} sig
 * @param {{coll?:string,timeoutMs?:number}} [opts]
 */
export async function discoverNodes(sig, opts = {}) {
  const host = sig?.host
  if (!host) throw new PeerDriveError('discoverNodes requires signaling host', ERR.PROTOCOL)
  const port = sig.port === undefined || sig.port === '' ? (sig.secure === false ? 80 : 443) : Number(sig.port)
  const scheme = sig.secure === false ? 'http' : 'https'
  const timeoutMs = opts.timeoutMs || 8000
  const q = opts.coll ? `?coll=${encodeURIComponent(opts.coll)}` : ''

  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const res = await fetch(`${scheme}://${host}:${port}/discover/nodes${q}`, { signal: ctrl.signal, mode: 'cors' })
    if (!res.ok) throw new PeerDriveError(`Discovery service returned HTTP ${res.status}`, ERR.PEER)
    const body = await res.json()
    return Array.isArray(body?.nodes) ? body.nodes : []
  } catch (e) {
    if (e instanceof PeerDriveError) throw e
    // CORS failure in browser is just a TypeError, no status code — without pointing this out,
    // users only think "signaling is down" and repeatedly debug a healthy signaling.
    throw new PeerDriveError(
      `Auto-discovery failed: ${e?.message || e}. Most common cause is signaling missing cross-origin response header (Access-Control-Allow-Origin)` +
        `— panel is at origin ${typeof location !== 'undefined' ? location.origin : '(unknown)'}, this is a cross-origin request.` +
        'Please upgrade signaling to latest go-peerserver (CORS enabled since 2026-09-20), or add the header at reverse proxy layer.',
      ERR.PEER,
    )
  } finally {
    clearTimeout(timer)
  }
}

/**
 * connect wraps an established connection, waits for ready then returns client.
 * This is the most commonly used entry: `const client = await connect(dataConnection)`
 */
export async function connect(conn, opts = {}) {
  const client = new PeerDriveClient(conn, opts)
  try {
    await client.ready(opts.openTimeoutMs)
  } catch (e) {
    client.close()
    throw e
  }
  return client
}

/**
 * connectToPeer convenience entry: provide PeerJS constructor and peer node id, one step to connect.
 *
 * This package doesn't import peerjs (keeping zero dependencies, not polluting consumer bundle size),
 * so the constructor is passed by the caller — in browser it's the global `Peer` (CDN <script> or `import Peer from 'peerjs'`).
 *
 * ⚠️ serialization must be 'raw': only raw mode sends string as text frame, ArrayBuffer as binary frame,
 * replicating Go-side "text frame=JSON header / binary frame=data block" semantics
 * (see back/internal/transport/conn.go header). Default binary serialization causes
 * data blocks to be wrapped by peerjs's own chunker, which peer can't parse.
 */
/**
 * randomPeerId generates our temporary peer id, character set consistent with server /peerjs/id.
 *
 * Why not wait for signaling to assign: PeerJS without id in `new Peer(opts)` sends
 * `GET {scheme}://{host}:{port}/{path}{key}/id` to signaling for an id.
 * But consumer-side is a **public static panel** —— file:// gives origin `null`, hosted on
 * Pages/CDN is another domain, this step is definitely cross-origin. Without Access-Control-Allow-Origin,
 * the response is swallowed by same-origin policy, PeerJS only reports a vague
 * `server-error: Could not get an ID from the server`, from this error you can't tell it's CORS
 * (the online peersignal.moonchan.xyz deployment is exactly the pre-CORS version).
 *
 * Self-generating id completely skips this request: signaling only forwards OFFER, doesn't care who sets
 * the id, plus saves a round trip. Convention: if caller doesn't provide peerOptions.id, use randomly
 * generated one here.
 *
 * Character set aligned with back/signalserver's randomID() (lowercase letters + digits, 16 chars);
 * if it actually collides with existing id, signaling returns ID-TAKEN, PeerJS reports error, just retry.
 */
function randomPeerId() {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let out = ''
  for (let i = 0; i < 16; i++) out += chars[Math.floor(Math.random() * chars.length)]
  return out
}

export async function connectToPeer(PeerCtor, peerId, { peerOptions = {}, connOptions = {}, ...opts } = {}) {
  if (typeof PeerCtor !== 'function') {
    // Use double quotes instead of single quotes: the message text contains a bare apostrophe in "PeerJS's",
    // writing it into a single-quoted string would prematurely close the string, causing the entire file to throw
    // "missing ) after argument list", while node reports the line number pointing to where the engine gave up
    // (this throw itself). The real issue is the apostrophe in the middle of the string.
    throw new TypeError("peerdrive-client: connectToPeer requires PeerJS's Peer constructor")
  }
  // id must go as **positional parameter**: empirically peerjs@1.5.5 ignores `new Peer({..., id})`
  // options.id (still GETs /{path}{key}/id), only `new Peer(id, opts)` works. Provide both positional
  // args for compatibility with other implementations.
  const myId = peerOptions.id || randomPeerId()
  const peer = new PeerCtor(myId, { ...peerOptions, id: myId })
  try {
    await new Promise((resolve, reject) => {
      const onOpen = () => {
        peer.off?.('error', onError)
        resolve()
      }
      const onError = (e) => {
        peer.off?.('open', onOpen)
        reject(new PeerDriveError(`Signaling failed: ${e?.message || e?.type || e}`, ERR.CLOSED))
      }
      peer.on('open', onOpen)
      peer.on('error', onError)
    })
    const conn = peer.connect(peerId, { serialization: 'raw', reliable: true, ...connOptions })
    const client = new PeerDriveClient(conn, { ...opts, peerId })
    client._ownedPeer = peer
    await client.ready(opts.openTimeoutMs)
    return client
  } catch (e) {
    try {
      peer.destroy()
    } catch {
      /* already destroyed */
    }
    throw e
  }
}
