// client.js — peerdrive pure-browser consumer: connect to a peerdrive node,
// list what it shares, and fetch files. **Zero dependencies**; does not
// require running any backend locally.
//
// Positioning (complements the /peerjs/* client path in back):
//   - Backend nodes (Go) interconnect with the same frame protocol and are the
//     "producers/holders";
//   - This package is a **pure consumer**: a browser page (or any environment
//     with WebRTC DataChannel) connects directly to a node, reads the share
//     list via the share frame, and fetches content via the req frame. This
//     corresponds to the requirement that "a webrtc pure client consumer can
//     pull files from this p2p network".
//
// Transport-agnostic design: this class knows nothing about PeerJS. It only
// requires the passed-in object to satisfy
//   { on(type, cb), send(data), open?: boolean, close?() }
// — PeerJS's DataConnection naturally satisfies this. Benefits:
//   1. The package itself has zero dependencies and doesn't add peerjs to the
//     consumer's bundle size;
//   2. Tests cover the whole state machine with a fake connection (no real
//      WebRTC/signaling server needed);
//   3. Switching transports later (raw RTCPeerConnection / WebTransport)
//      requires no changes to this file.
//
// For frame-protocol details see the top-of-file comments in src/protocol.js
// (character-for-character aligned with the Go-side conn.go).

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
// Note: do NOT re-export PSK_REQUIRED here — index.js is
// `export * from './protocol.js'` + `export * from './client.js'`, and
// exporting the same name from two star-exports becomes an "ambiguous export"
// that actually disappears from the package entry.

/** Error codes. The UI branches on `code`; do NOT match the message text. */
export const ERR = {
  INVALID_HASH: 'INVALID_HASH', // hash isn't 64 lowercase hex chars (refused locally, no request sent)
  TOO_LARGE: 'TOO_LARGE', // exceeds maxBufferBytes / the protocol cap of 8GB
  HASH_MISMATCH: 'HASH_MISMATCH', // content doesn't match the hash (peer data is corrupt or tampered)
  INCOMPLETE: 'INCOMPLETE', // bytes in the peer's done declaration differ from what was actually received
  TIMEOUT: 'TIMEOUT', // idle timeout (peer hung / half-dead connection)
  CLOSED: 'CLOSED', // connection closed
  PROTOCOL: 'PROTOCOL', // frame is invalid (peer implemented a different protocol)
  PEER: 'PEER', // peer returned an err frame (not found / unauthorized / read failure…)
  CANCELLED: 'CANCELLED', // local cancellation (abort / early break of iteration)
  // PSK_REQUIRED: the peer has a pre-shared-key gate enabled, and I didn't
  // present one (or presented the wrong one).
  // It gets its own code because the remediation is "go fill in the key",
  // which is completely different from "file not found".
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
  // Idle timeout: how long without receiving any frame for this request before
  // it's judged dead. The Go side is 5 minutes (server patience); the consumer
  // takes a smaller value — having the user stare at a frozen progress bar for
  // 5 minutes is a worse experience.
  idleTimeoutMs: 120_000,
  openTimeoutMs: 20_000,
  verbTimeoutMs: 15_000, // matches Go-side verbWaitTimeout (small JSON replies like share)
  maxBufferBytes: DEFAULT_MAX_BUFFER_BYTES,
}

/**
 * ChunkQueue bridges the "push-mode" frame callback into a "pull-mode" async
 * iteration. stream() uses it for backpressure: if the consumer doesn't pull,
 * chunks stay in the queue (instead of piling up memory without limit).
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
    // On failure, drop already-buffered chunks: nobody will consume them, and
    // keeping them only holds memory hostage
    // (typical case: TOO_LARGE triggers mid-stream, with tens of MB already
    // queued)
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

    // PSK gate state (doc/NETDISK.md "PSK Gate"):
    //   none = no key configured (if the peer has a gate, it will receive a
    //          PSK_REQUIRED error)
    //   sent = key presented, awaiting the peer's receipt (business frames are
    //          sent without waiting for it, relying on DataChannel ordering)
    //   ok   = peer accepted; err = peer rejected (wrong key), pskError holds
    //          its msg
    this.psk = typeof opts.psk === 'string' ? opts.psk : ''
    this.pskState = this.psk ? 'pending' : 'none'
    this.pskError = null

    this._pend = new Map() // reqId → fetch state
    this._verbs = new Map() // reqId → one-shot reply wait slot (share/pull etc.)
    this._uploads = new Map() // reqId → upload session (local ingest)
    this._expect = null // connection-level expect: which request the next binary chunk belongs to (see protocol.js constraint 2)
    this._openWaiters = []
    this._closeErr = null
    this._openState = conn.open === true ? true : null // null = unknown
    this._ownedPeer = null
    this._bind()
    // A connection that was already open at construction time won't fire 'open'
    // again; send the PSK auth here
    if (conn.open === true) this._sendPskAuth()
  }

  /**
   * _sendPskAuth presents the pre-shared key (only sent when configured, and
   * must be this end's first frame).
   * See the pskAuthFrame comments in protocol.js: relies on DataChannel
   * ordering, doesn't wait for a receipt.
   */
  _sendPskAuth() {
    if (!this.psk || this.pskState !== 'pending') return
    try {
      this.conn.send(pskAuthFrame(this.psk))
      this.pskState = 'sent'
    } catch {
      this.pskState = 'none' // if it can't be sent, treat it as unconfigured: let the peer tell us via err
    }
  }

  get isOpen() {
    return this._openState === true && !this._closeErr
  }

  /**
   * localPeerId this end's temporary id on the signaling server — the UI uses
   * this to display "who am I".
   *
   * Discovery note: the first version of the panel displayed `conn.peer` as
   * the local id, which ended up showing the peer's node name (
   * DataConnection.peer refers to the **remote** side). When building the
   * connection via connectToPeer, the local Peer is held by this class, so we
   * take it from there; when the connection is created externally (conn passed
   * in), we can only return an empty string — the caller should hold the Peer
   * instance itself.
   */
  get localPeerId() {
    return (this._ownedPeer && this._ownedPeer.id) || ''
  }

  /** ready waits for the connection to be ready. Resolves immediately if already ready. */
  ready(timeoutMs = this.opts.openTimeoutMs) {
    if (this._closeErr) return Promise.reject(this._closeErr)
    if (this._openState === true) return Promise.resolve(this)
    return new Promise((resolve, reject) => {
      const entry = { resolve, reject, timer: null }
      this._openWaiters.push(entry)
      if (timeoutMs > 0) {
        entry.timer = setTimeout(() => {
          this._openWaiters = this._openWaiters.filter((w) => w !== entry)
          reject(new PeerDriveError(`connection did not establish within ${timeoutMs}ms`, ERR.TIMEOUT))
        }, timeoutMs)
      }
    })
  }

  /**
   * shares queries the peer's share list (the last step of "node market →
   * join node → see file links"). Returns {collections, files, dirs, total}.
   *
   * Semantic reminder: an empty list is a **valid result** (the peer hasn't
   * enabled sharing / hasn't declared any directories), not an error — the UI
   * should render "this node has no shared content". Only go PEER/TIMEOUT if
   * the peer genuinely fails.
   */
  async shares({ timeoutMs = this.opts.verbTimeoutMs } = {}) {
    const reqId = nextReqId()
    const frame = await this._requestVerb(reqId, shareFrame(reqId), timeoutMs)
    if (!frame || frame.type !== 'share-resp') {
      throw new PeerDriveError('peer share reply format is abnormal (peer may not be a peerdrive node)', ERR.PROTOCOL)
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
   * put places content into the node's store (**local ingest**).
   *
   * Uses the Go-side upload verb: send head → wait meta → send binary chunks →
   * wait ack, advancing chunk by chunk until the server returns
   * uploaded{hash,size,path}. The reason for this back-and-forth rather than
   * pushing it all at once: the receiver wants to write to disk at its own
   * pace (it has flow control authority, see the uploadFrame comments in
   * protocol.js), and only one upload can be in progress on this connection at
   * a time.
   *
   * Returns {hash,size,name,path} — hash is the content address; afterward
   * fetch() can pull it back.
   * Note the default maxPutBytes gate: for very large content, slice it
   * yourself first or use another channel.
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
    if (signal && signal.aborted) throw new PeerDriveError('cancelled', ERR.CANCELLED)

    const reqId = nextReqId()
    const state = { reqId, emit: null, timer: null }
    // One round = one server frame for this upload. Time each round
    // separately: an overall timeout can't distinguish "slow" from "hung", and
    // their handling is completely different (slow = keep waiting, hung = error
    // and retry).
    const round = () =>
      new Promise((resolve, reject) => {
        state.emit = { resolve, reject }
        state.timer = setTimeout(() => {
          this._uploads.delete(reqId)
          state.emit = null
          reject(new PeerDriveError(`upload got no response within ${chunkTimeoutMs}ms`, ERR.TIMEOUT))
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
    // Cancellation: make "the round currently in flight" fail immediately.
    // Previously the signal was only checked once at entry, so the UI's cancel
    // button became a no-op after the upload started (it had to wait for the
    // whole round to pass, looking like a hang).
    const onAbort = signal
      ? () => {
          const u = this._uploads.get(reqId)
          if (u) this._failUpload(u, new PeerDriveError('cancelled', ERR.CANCELLED))
        }
      : null
    if (onAbort) signal.addEventListener('abort', onAbort, { once: true })
    try {
      // First frame head. If size=0 (empty file), the server returns uploaded
      // directly, skipping meta.
      this._send(uploadFrame(reqId, name, size, 0))
      for (;;) {
        const frame = await round()
        if (state.timer) clearTimeout(state.timer)
        state.timer = null
        state.emit = null

        if (frame.type === 'err') {
          const code = frame.code === ERR.PSK_REQUIRED ? ERR.PSK_REQUIRED : ERR.PEER
          throw new PeerDriveError(frame.msg || 'upload failed', code)
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
          // Server authorization: from here, write n bytes (the server decides
          // the granularity, following UPLOAD_CHUNK).
          // The offset it gives is authoritative — if the same content was
          // partially uploaded before, the server asks to resume from the
          // breakpoint of the contiguous write; blindly incrementing would
          // write bytes to the wrong position, producing a corrupt copy (and
          // hanging at a spot that isn't your fault at all).
          const srv = Number(frame.offset)
          if (Number.isFinite(srv) && srv >= 0) offset = srv
          if (offset > size) throw new PeerDriveError(`server asks to resume from ${offset}, but content is only ${size} bytes`, ERR.PROTOCOL)
          const n = Math.min(UPLOAD_CHUNK, size - offset)
          if (n <= 0) throw new PeerDriveError('the server\'s meta offset has exceeded the file size', ERR.PROTOCOL)
          this.conn.send(bytes.subarray(offset, offset + n))
          offset += n
          this.stats.chunks++
          this.stats.bytes += n
          if (onProgress) onProgress(offset, size)
          continue // wait for this chunk's ack (or uploaded for the last chunk)
        }
        if (frame.type === 'ack') {
          if (offset >= size) continue // the last chunk's completion ack hasn't arrived yet, keep waiting
          this._send(uploadFrame(reqId, name, size, offset))
          continue
        }
        throw new PeerDriveError(`upload interrupted by an unknown frame: ${frame.type}`, ERR.PROTOCOL)
      }
    } catch (e) {
      stopRound()
      throw e instanceof Error ? e : new Error(String(e))
    } finally {
      if (onAbort) signal.removeEventListener('abort', onAbort)
    }
  }

  /**
   * pull asks the node to fetch a URL on your behalf and ingest it (**network
   * ingest**).
   *
   * Typical case: the panel has only the link, not the content (or the content
   * is on the far side of the internet), so ask a node with network
   * capability to get it. The node side does SSRF protection (only public
   * http/https, blocks intranet/localhost, verifies redirects hop by hop) and
   * has a size cap, so these failures are **expected behavior**, not a bug —
   * the error message is passed through to the caller as-is.
   *
   * @param {string} url
   * @param {{name?:string,timeoutMs?:number}} [opts]
   * @returns {Promise<{hash:string,size:number,name:string,path:string}>}
   */
  async pull(url, opts = {}) {
    if (typeof url !== 'string' || !url.trim()) {
      throw new PeerDriveError('pull needs a non-empty URL', ERR.INVALID_HASH)
    }
    const reqId = nextReqId()
    const frame = await this._requestVerb(reqId, pullFrame(reqId, url.trim(), opts.name || ''), opts.timeoutMs || this.opts.verbTimeoutMs)
    if (!frame || frame.type !== 'pulled') {
      throw new PeerDriveError('peer pull reply format is abnormal (version too old?)', ERR.PROTOCOL)
    }
    return { hash: frame.hash, size: Number(frame.total) || 0, name: frame.name || '', path: frame.path || '' }
  }

  /**
   * stream pulls in chunks, yielding Uint8Array blocks one at a time, and
   * **doesn't keep the whole content in memory**.
   * Suitable for large files (used together with the File System Access API /
   * StreamSaver to write straight to disk).
   *
   * A full request (offset=0 with no size specified) verifies sha256 on
   * completion and throws HASH_MISMATCH on mismatch — verification is
   * incremental (see sha256.js), so it doesn't need to re-read earlier chunks.
   *
   * opts: {offset, size, maxBytes, onProgress(received, total), signal}
   * An early break by the consumer cancels this request (later chunks are
   * discarded).
   */
  async *stream(hash, opts = {}) {
    this._assertHash(hash)
    const { offset = 0, size = -1, signal } = opts
    // An already-aborted signal is rejected **before sending the request**.
    // Sending first and checking after would fetch content for nothing (and
    // the peer keeps sending until done, wasting bandwidth).
    if (signal && signal.aborted) throw new PeerDriveError('cancelled', ERR.CANCELLED)
    const reqId = nextReqId()
    const p = this._startFetch(reqId, hash, { ...opts, offset, size })

    if (signal) {
      p.signal = signal
      p.onAbort = () => this._cancel(reqId, new PeerDriveError('cancelled', ERR.CANCELLED))
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
      // On normal completion/failure it's already gone from _pend; this is an
      // idempotent cleanup fallback. An early consumer break also reaches here,
      // releasing the buffered chunks along with the request.
      // Failure counting is handled by _failPending; don't double-count here
      // (a user-initiated cancel isn't a failure).
      this._cancel(reqId, new PeerDriveError('cancelled', ERR.CANCELLED))
    }
  }

  /**
   * fetch retrieves the whole content (Uint8Array). Equivalent to collecting
   * all of stream().
   * Has a memory gate: exceeding maxBytes (default 256MB) throws TOO_LARGE —
   * use stream() for large files.
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

  /** fetchBlob retrieves as a Blob (for browser preview/playback; MIME can be guessed from name). */
  async fetchBlob(hash, { name = '', mime = '', ...opts } = {}) {
    const bytes = await this.fetch(hash, opts)
    return new Blob([bytes], { type: mime || guessMime(name) })
  }

  /** fetchText retrieves as text (utf-8). */
  async fetchText(hash, opts = {}) {
    const bytes = await this.fetch(hash, opts)
    return new TextDecoder('utf-8').decode(bytes)
  }

  /**
   * saveAs triggers a browser download. Uses Blob + <a download>, so the
   * **whole content is in memory**; shares the same maxBytes gate as fetch.
   * For very large files, use stream() to write to disk yourself.
   */
  async saveAs(hash, filename = '', opts = {}) {
    if (typeof document === 'undefined') {
      throw new PeerDriveError('saveAs needs a DOM environment', ERR.PROTOCOL)
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
      // Revoking immediately makes some browsers (Safari) cancel the download:
      // give a little grace period
      setTimeout(() => URL.revokeObjectURL(url), 30_000)
    }
    return blob.size
  }

  /** Convenience method: fetch a specific entry/file directly from the share list and save it (auto-fills the filename). */
  async saveShare(item, opts = {}) {
    if (!item || !item.hash) throw new PeerDriveError('saveShare needs an item with a hash', ERR.PROTOCOL)
    const name = baseNameOf(item.name || item.path || '')
    return this.saveAs(item.hash, name, opts)
  }

  /** close closes the connection (and destroys the Peer object if this instance created it). */
  close() {
    this._unbind()
    this._failAll(new PeerDriveError('connection closed', ERR.CLOSED))
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

  // ── Internal: connection binding and frame dispatch ────────────────────────

  _bind() {
    this._onData = (data) => {
      // Frame order is protocol semantics (see protocol.js constraint 2); must
      // be handled synchronously, not queued
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
      // PSK: must be presented **before any business frame** (including the
      // first request sent after ready()).
      // Placed before flush, because flush resolves waiters whose subsequent
      // sends come after this send — order is the gate's semantics.
      this._sendPskAuth()
      this._flushOpenWaiters(null)
    }
    this._onClose = () => this._failAll(new PeerDriveError('connection closed', ERR.CLOSED))
    this._onError = (err) => {
      const msg = err && (err.message || err.type) ? err.message || err.type : String(err)
      this._failAll(new PeerDriveError(`connection error: ${msg}`, ERR.CLOSED))
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
    if (!frame) return // not a frame of this protocol: ignore (the same connection may serve other purposes)
    switch (frame.type) {
      case 'meta': {
        // meta is an ambiguous frame: it can mean either "fetch ready" or
        // "upload chunk authorized".
        // Route by reqId ownership — misrouting makes the uploader wait until
        // timeout, or makes the fetcher receive an unknown frame.
        const u = frame.reqId ? this._uploads.get(frame.reqId) : null
        return u ? this._settleUpload(u, frame) : this._onMeta(frame)
      }
      case 'ack':
      case 'uploaded': {
        const u = frame.reqId ? this._uploads.get(frame.reqId) : null
        if (u) return this._settleUpload(u, frame)
        return // not ours: forward compatible, ignore
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
        this.pskError = frame.msg || 'psk: the peer rejected the key'
        return
      default:
        // Forward compatibility for unknown frame types only applies to frames
        // "unrelated to me". An unknown frame **carrying a reqId I'm waiting
        // on** must be an error: the peer replied with a type I don't recognize
        // at the reply slot I explicitly requested; the only sensible
        // explanation is a protocol version mismatch. If we ignored it here,
        // the caller would only see TIMEOUT ("the peer hung up"), while the real
        // cause is that the peer is speaking a different protocol — sending
        // debugging in the completely wrong direction.
        return this._rejectOwner(frame.reqId, (t) => new PeerDriveError(
          `peer replied with unknown frame ${t} (protocol version mismatch?)`,
          ERR.PROTOCOL,
        ))
    }
  }

  /**
   * _rejectOwner finds the waiting session by reqId and settles it with a
   * failure.
   *
   * One-step (upload) / one-shot verb / fetch — three kinds of wait slots can
   * hold the same reqId space, so this checks them in order; if none is found,
   * it's a late or unrelated frame, which can be ignored.
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
    // total = -1 means the peer also can't determine the size (multi-source
    // back-to-origin case) — not an error
    if (Number.isFinite(total) && total > MAX_FILE_BYTES) {
      return this._failPending(p, new PeerDriveError(`peer declared file ${total} bytes, exceeds the protocol cap`, ERR.TOO_LARGE))
    }
    if (Number.isFinite(total) && total > p.maxBytes) {
      return this._failPending(
        p,
        new PeerDriveError(
          `file ${total} bytes exceeds the local memory gate ${p.maxBytes} (use stream() to write to disk, or raise maxBytes)`,
          ERR.TOO_LARGE,
        ),
      )
    }
    p.total = Number.isFinite(total) ? total : -1
    this._reportProgress(p)
  }

  _onDataHead(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return // late frame (already cancelled/finished): drop
    p.touch()
    const size = Number(frame.size)
    // Same cap as the Go-side H6: block size must be positive and within the
    // protocol cap, otherwise it's malicious or a wrong protocol
    if (!Number.isFinite(size) || size <= 0 || size > MAX_FILE_BYTES) {
      return this._failPending(p, new PeerDriveError(`illegal data block size ${frame.size}`, ERR.PROTOCOL))
    }
    p.blockSize = size
    p.blockGot = 0
    // Connection-level expect: attach the immediately following binary chunk
    // to this request
    this._expect = p
  }

  _onChunk(data) {
    const p = this._expect
    if (!p) return // a data chunk without a preceding data head: not protocol content, drop
    const bytes = toUint8Array(data)
    // Note: chunks are **not** stored on the request state. Chunks only go
    // into the bounded queue (backpressure) and are taken by the consumer —
    // that's where all the "streaming" benefit lies; storing an extra copy in
    // state would turn it back into whole-content memory residency.
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
        new PeerDriveError(`content exceeds the local memory gate ${p.maxBytes} (use stream())`, ERR.TOO_LARGE),
      )
    }
  }

  _onDone(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return
    p.touch()
    const declared = Number(frame.size)
    // Same integrity gate as the Go side: an early done from the peer returns
    // truncated content as success (silent corruption)
    if (Number.isFinite(declared) && declared >= 0 && p.received !== declared) {
      return this._failPending(
        p,
        new PeerDriveError(`transfer incomplete: received ${p.received} bytes, peer declared ${declared} bytes`, ERR.INCOMPLETE, {
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
          new PeerDriveError(`content doesn't match the hash (expected ${p.hash}, got ${got})`, ERR.HASH_MISMATCH, {
            expected: p.hash,
            actual: got,
          }),
        )
      }
    }
    this._finishPending(p)
  }

  _onErr(frame) {
    const msg = frame.msg || 'peer returned an error'
    // err frames may carry a code (the Go-side gate returns PSK_REQUIRED).
    // Classify by code, not by text: the text changes between versions, the
    // code is part of the protocol.
    const code = frame.code === ERR.PSK_REQUIRED ? ERR.PSK_REQUIRED : ERR.PEER
    const err = () => new PeerDriveError(msg, code)
    // The err frame's reqId may belong to a fetch, a one-shot verb, or an
    // upload — all three must be checked; missing the upload would leave put()
    // hanging until timeout instead of immediately handing the server's refusal
    // reason to the caller.
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

  /** _failUpload ends an upload step with an error, also cancelling that round's timer. */
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

  // ── Internal: request lifecycle ───────────────────────────────────────────

  _assertHash(hash) {
    if (!isValidHash(hash)) {
      throw new PeerDriveError(
        `hash must be a 64-char lowercase hex sha256, got ${JSON.stringify(hash)}`,
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
      // Only do content-address verification for "full requests": a range
      // request's fragment isn't the digest of the whole content (same
      // condition as Go-side fetchReader.verify)
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
        new PeerDriveError(`fetch ${p.hash.slice(0, 12)}… idle timeout (no data within ${this.opts.idleTimeoutMs}ms)`, ERR.TIMEOUT),
      )
    }, this.opts.idleTimeoutMs)
  }

  _reportProgress(p) {
    if (typeof p.onProgress === 'function') {
      try {
        p.onProgress(p.received, p.total, p.hash)
      } catch {
        /* a throwing progress callback shouldn't affect the transfer */
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
    // Gotcha: don't wait for the peer to stop sending. The protocol has no
    // "cancel" frame; the peer keeps sending this request through to the end,
    // we just drop the subsequent frames (_expect/_pend no longer find it).
    // Truly aborting requires closing the connection.
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
    // In-flight uploads must also end with an error: otherwise put() hangs
    // until its own timeout, while the real cause (connection dropped) is
    // masked as TIMEOUT, sending debugging in the completely wrong direction.
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
        reject(new PeerDriveError(`peer did not reply within ${timeoutMs}ms`, ERR.TIMEOUT))
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
 * readableBytes normalizes various "content sources" into Uint8Array.
 *
 * The types covered include what humans actually pass in: typed array /
 * ArrayBuffer / Blob / File / string. File is the only entry point for the
 * panel's "select file to upload" flow.
 */
async function readableBytes(data) {
  if (data instanceof Uint8Array) return data
  if (data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof data === 'string') return new TextEncoder().encode(data)
  // Blob / File / Response, and any Blob-like stand-in: duck-typed on "has
  // arrayBuffer()", not instanceof Blob — instanceof breaks across realms
  // (iframes / stand-in objects in unit tests), while "can get bytes" is the
  // only contract that actually matters here.
  if (data && typeof data === 'object' && typeof data.arrayBuffer === 'function') {
    return new Uint8Array(await data.arrayBuffer())
  }
  throw new TypeError('put: data must be a Uint8Array / ArrayBuffer / Blob / File / string')
}

/**
 * discoverNodes asks self-hosted signaling "which nodes are online right now"
 * — the consumer's auto-discovery entry point.
 *
 * Uses the signaling REST discovery endpoint GET /discover/nodes (built-in
 * discovery, replacing a public MQTT broker).
 * Returns [{peerId,lastSeen,nodeType,collections,uptime,loadInfo}].
 *
 * ⚠️ One inescapable prerequisite: **the signaling server must send
 * cross-origin responses**. The panel is a public static page; under file://
 * the browser-computed origin is `null`, and hosted on Pages it's a different
 * domain, and this step is a cross-origin request — if the signaling server
 * doesn't send Access-Control-Allow-Origin, the browser fails at the fetch
 * level outright, without returning any status code (NetworkError). This is
 * the most common reason online discovery doesn't work today, so when it
 * can't be diagnosed in isolation, give an explicit prompt (see the NOT_CORS
 * handling below).
 *
 * @param {{host:string,port?:number|string,secure?:boolean}} sig
 * @param {{coll?:string,timeoutMs?:number}} [opts]
 */
export async function discoverNodes(sig, opts = {}) {
  const host = sig?.host
  if (!host) throw new PeerDriveError('discoverNodes needs a signaling host', ERR.PROTOCOL)
  const port = sig.port === undefined || sig.port === '' ? (sig.secure === false ? 80 : 443) : Number(sig.port)
  const scheme = sig.secure === false ? 'http' : 'https'
  const timeoutMs = opts.timeoutMs || 8000
  const q = opts.coll ? `?coll=${encodeURIComponent(opts.coll)}` : ''

  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const res = await fetch(`${scheme}://${host}:${port}/discover/nodes${q}`, { signal: ctrl.signal, mode: 'cors' })
    if (!res.ok) throw new PeerDriveError(`discovery service returned HTTP ${res.status}`, ERR.PEER)
    const body = await res.json()
    return Array.isArray(body?.nodes) ? body.nodes : []
  } catch (e) {
    if (e instanceof PeerDriveError) throw e
    // A CORS failure in the browser is just a TypeError — you don't even get a
    // status code. Without calling this out, users just assume "the signaling
    // server is down" and keep debugging a healthy signaling server.
    throw new PeerDriveError(
      `auto-discovery failed: ${e?.message || e}. Most common cause is that the signaling server does not send a cross-origin response header (Access-Control-Allow-Origin)` +
        `— the panel is on origin ${typeof location !== 'undefined' ? location.origin : '(unknown)'}, which is a cross-origin request.` +
        'Please upgrade the signaling server to the latest go-peerserver (CORS has been enabled since 2026-09-20), or add the header at the reverse-proxy layer.',
      ERR.PEER,
    )
  } finally {
    clearTimeout(timer)
  }
}

/**
 * connect wraps an already-established connection, waits for it to be ready,
 * and returns the client.
 * This is the most common entry point: `const client = await connect(dataConnection)`
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
 * connectToPeer convenience entry: pass the PeerJS constructor and the peer's
 * node id to do it in one shot.
 *
 * This package does not import peerjs (staying zero-dependency, not polluting
 * the consumer's bundle size), so the constructor is passed in by the caller —
 * in a browser it's the global `Peer` (CDN <script> or `import Peer from
 * 'peerjs'`).
 *
 * ⚠️ serialization must be 'raw': only in raw mode does a string go as a text
 * frame and an ArrayBuffer as a binary frame, which is how the Go-side
 * "text frame = JSON head / binary frame = data chunk" semantics are
 * reproduced (see the top of back/internal/transport/conn.go). Using the
 * default binary serialization makes peerjs wrap the data chunk in its own
 * chunker, which the peer can't parse.
 */
/**
 * randomPeerId generates a local temporary peer id, with the same character
 * set as the server's /peerjs/id.
 *
 * Why not wait for the signaling server to assign one: PeerJS, when given no
 * id in `new Peer(opts)`, will itself issue
 * `GET {scheme}://{host}:{port}/{path}{key}/id` to the signaling server to
 * get an id.
 * But the consumer is a **public static panel** — under file:// the
 * browser-computed origin is `null`, and hosted on Pages/CDN it's another
 * domain, so this step is definitely cross-origin. Without
 * Access-Control-Allow-Origin on the signaling server, the response is
 * swallowed by same-origin policy, and PeerJS only reports a vague
 * `server-error: Could not get an ID from the server` — there's no way to tell
 * from that error that it's CORS (the online peersignal.moonchan.xyz
 * deployment is exactly the version without CORS enabled).
 *
 * Supplying our own id means this request is never sent: the signaling server
 * only relays OFFERs and doesn't care who defines the id anyway, plus we save
 * a round trip. Convention: if the caller doesn't provide peerOptions.id, use
 * the randomly generated one here.
 *
 * Character set matches back/signalserver's randomID() (lowercase letters +
 * digits, 16 chars); if it really collides with an existing id, the signaling
 * server returns ID-TAKEN, PeerJS reports an error, and a retry works.
 */
function randomPeerId() {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let out = ''
  for (let i = 0; i < 16; i++) out += chars[Math.floor(Math.random() * chars.length)]
  return out
}

export async function connectToPeer(PeerCtor, peerId, { peerOptions = {}, connOptions = {}, ...opts } = {}) {
  if (typeof PeerCtor !== 'function') {
    throw new TypeError('peerdrive-client: connectToPeer needs the PeerJS Peer constructor')
  }
  // id must be passed as a **positional argument**: empirically, peerjs@1.5.5
  // ignores `options.id` in `new Peer({..., id})` (it still GETs
  // /{path}{key}/id); only `new Peer(id, opts)` works. Supplying both
  // positions is compatible with other implementations.
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
        reject(new PeerDriveError(`signaling failed: ${e?.message || e?.type || e}`, ERR.CLOSED))
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