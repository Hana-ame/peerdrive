// client.js — peerdrive 纯浏览器消费端：连上一个 peerdrive 节点，列出它共享的
// 内容并拉取文件。**零依赖**，不要求本地跑任何后端。
//
// 定位（与 back 的 /peerjs/* 客户端路径互补）：
//   - 后端节点（Go）之间用同一套帧协议互联，是"生产方/持有方"；
//   - 本包是**纯消费端**：一个浏览器页面（或任何有 WebRTC DataChannel 的环境）
//     直连节点，走 share 帧看清单、走 req 帧拉内容。对应需求里那句
//     "webrtc 纯 client 消费端，可以从这个 p2p 网络拉取文件"。
//
// 传输无关设计：本类不认识 PeerJS。它只要求传进来的对象满足
//   { on(type, cb), send(data), open?: boolean, close?() }
// —— PeerJS 的 DataConnection 天然满足。这样做的好处：
//   1. 包本身零依赖，不把 peerjs 塞进使用方的打包体积；
//   2. 测试用假连接即可覆盖全部状态机（不需要真 WebRTC/信令服务器）；
//   3. 将来换传输（裸 RTCPeerConnection / WebTransport）不必改这个文件。
//
// 帧协议细节见 src/protocol.js 顶部注释（与 Go 侧 conn.go 逐字对齐）。

import { Sha256 } from './sha256.js'
import {
  DEFAULT_MAX_BUFFER_BYTES,
  MAX_FILE_BYTES,
  baseName as baseNameOf,
  concatChunks,
  guessMime,
  isBinaryFrame,
  isValidHash,
  nextReqId,
  parseFrame,
  pskAuthFrame,
  reqFrame,
  shareFrame,
  toUint8Array,
} from './protocol.js'
// 注意：不要在这里再 re-export PSK_REQUIRED —— index.js 是
// `export * from './protocol.js'` + `export * from './client.js'`，同一个名字
// 从两处 star-export 出来会变成"歧义导出"，反而从包入口消失。

/** 错误码。UI 按 code 分支，不要去匹配 message 文案。 */
export const ERR = {
  INVALID_HASH: 'INVALID_HASH', // hash 不是 64 位小写 hex（本地就拦，不发请求）
  TOO_LARGE: 'TOO_LARGE', // 超出 maxBufferBytes / 协议上限 8GB
  HASH_MISMATCH: 'HASH_MISMATCH', // 内容与 hash 不符（对端数据损坏或被篡改）
  INCOMPLETE: 'INCOMPLETE', // 对端 done 声明的字节数与实际收到的不一致
  TIMEOUT: 'TIMEOUT', // 空闲超时（对端卡死/连接半死）
  CLOSED: 'CLOSED', // 连接关闭
  PROTOCOL: 'PROTOCOL', // 帧不合法（对端实现了别的协议）
  PEER: 'PEER', // 对端回了 err 帧（not found / 越权 / 读失败…）
  CANCELLED: 'CANCELLED', // 本地取消（abort / 迭代提前 break）
  // PSK_REQUIRED：对端开了预共享密钥门禁，而我没有出示（或出示错了）。
  // 单独成码是因为它的修复动作是"去填密钥"，跟"文件不存在"完全不同。
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
  // 空闲超时：多久没收到该请求的任何帧就判死。Go 侧是 5 分钟（服务端耐心），
  // 消费端取小一些——用户盯着一个不动的进度条等 5 分钟是更差的体验。
  idleTimeoutMs: 120_000,
  openTimeoutMs: 20_000,
  verbTimeoutMs: 15_000, // 与 Go 侧 verbWaitTimeout 一致（share 这类小 JSON 应答）
  maxBufferBytes: DEFAULT_MAX_BUFFER_BYTES,
}

/**
 * ChunkQueue 把「推模式」的帧回调桥接成「拉模式」的 async 迭代。
 * stream() 用它做背压：消费端不取，块就停在队列里（而不是无限堆内存）。
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
    // 失败时把已缓存块丢掉：不会再有人消费它们，留着只会拖住内存
    // （典型场景：TOO_LARGE 在中途触发，队列里可能已经压着几十 MB）
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

/** 消费端客户端：一条连接一个实例。 */
export class PeerDriveClient {
  constructor(conn, opts = {}) {
    if (!conn || typeof conn.send !== 'function' || typeof conn.on !== 'function') {
      throw new TypeError('peerdrive-client: conn 需实现 { on(type, cb), send(data) }')
    }
    this.conn = conn
    this.opts = { ...DEFAULTS, ...opts }
    this.peerId = conn.peer || conn.peerId || opts.peerId || ''
    this.stats = { requests: 0, chunks: 0, bytes: 0, failures: 0 }

    // PSK 门禁状态（doc/NETDISK.md「PSK 门禁」）：
    //   none = 没配密钥（对端若开了门禁，会收到 PSK_REQUIRED 错误）
    //   sent = 已出示，等对端回执（不等它就开始发业务帧，靠 DataChannel 保序）
    //   ok   = 对端认可；err = 对端拒绝（密钥不对），pskError 是它的 msg
    this.psk = typeof opts.psk === 'string' ? opts.psk : ''
    this.pskState = this.psk ? 'pending' : 'none'
    this.pskError = null

    this._pend = new Map() // reqId → 拉取状态
    this._verbs = new Map() // reqId → 一次性应答等待槽（share 等）
    this._expect = null // 连接级 expect：下一个二进制块归谁（见 protocol.js 约束 2）
    this._openWaiters = []
    this._closeErr = null
    this._openState = conn.open === true ? true : null // null = 未知
    this._ownedPeer = null
    this._bind()
    // 传入时就已经打开的连接不会再来一次 'open' 事件，这里补发
    if (conn.open === true) this._sendPskAuth()
  }

  /**
   * _sendPskAuth 出示预共享密钥（配了才发，且必须是本端第一帧）。
   * 见 protocol.js 的 pskAuthFrame 注释：靠 DataChannel 保序，不等回执。
   */
  _sendPskAuth() {
    if (!this.psk || this.pskState !== 'pending') return
    try {
      this.conn.send(pskAuthFrame(this.psk))
      this.pskState = 'sent'
    } catch {
      this.pskState = 'none' // 发不出去就当没配：让对端用 err 告诉我们
    }
  }

  get isOpen() {
    return this._openState === true && !this._closeErr
  }

  /**
   * localPeerId 本端在信令上的临时 id —— UI 显示"我是谁"要用这个。
   *
   * 发现背景：面板第一版把 `conn.peer` 当成自己的 id 显示，结果屏幕上写着对方的
   * 节点名（DataConnection.peer 指的是**远端**）。用 connectToPeer 建连时本端 Peer
   * 由本类持有，所以这里从它取；不是本类创建时（外部传入 conn）只能返回空串，
   * 调用方应自行持有 Peer 实例。
   */
  get localPeerId() {
    return (this._ownedPeer && this._ownedPeer.id) || ''
  }

  /** ready 等连接就绪。已就绪则立即 resolve。 */
  ready(timeoutMs = this.opts.openTimeoutMs) {
    if (this._closeErr) return Promise.reject(this._closeErr)
    if (this._openState === true) return Promise.resolve(this)
    return new Promise((resolve, reject) => {
      const entry = { resolve, reject, timer: null }
      this._openWaiters.push(entry)
      if (timeoutMs > 0) {
        entry.timer = setTimeout(() => {
          this._openWaiters = this._openWaiters.filter((w) => w !== entry)
          reject(new PeerDriveError(`连接未在 ${timeoutMs}ms 内建立`, ERR.TIMEOUT))
        }, timeoutMs)
      }
    })
  }

  /**
   * shares 查询对端的共享清单（「节点市场 → 加入节点 → 看到文件链接」里的
   * 最后一步）。返回 {collections, files, dirs, total}。
   *
   * 语义提醒：空清单是**合法结果**（对方没开启共享 / 没声明任何目录），不是
   * 错误——UI 应当渲染"该节点没有共享内容"。对端真的失败才走 PEER/TIMEOUT。
   */
  async shares({ timeoutMs = this.opts.verbTimeoutMs } = {}) {
    const reqId = nextReqId()
    const frame = await this._requestVerb(reqId, shareFrame(reqId), timeoutMs)
    if (!frame || frame.type !== 'share-resp') {
      throw new PeerDriveError('对端 share 应答格式异常（对端可能不是 peerdrive 节点）', ERR.PROTOCOL)
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
   * stream 流式拉取：逐个产出 Uint8Array 块，**不把整份内容驻留内存**。
   * 适合大文件（配合 File System Access API / StreamSaver 直接落盘）。
   *
   * 全量请求（offset=0 且不指定 size）会在结束时校验 sha256，不匹配则抛
   * HASH_MISMATCH——校验是增量的（见 sha256.js），不需要回看前面的块。
   *
   * opts: {offset, size, maxBytes, onProgress(received, total), signal}
   * 消费端提前 break 会取消本请求（后续到达的块被丢弃）。
   */
  async *stream(hash, opts = {}) {
    this._assertHash(hash)
    const { offset = 0, size = -1, signal } = opts
    // 已 abort 的 signal：在**发请求之前**就拒。先发后判会白白拉一次内容
    // （而且对端会一直发到 done，白费带宽）。
    if (signal && signal.aborted) throw new PeerDriveError('已取消', ERR.CANCELLED)
    const reqId = nextReqId()
    const p = this._startFetch(reqId, hash, { ...opts, offset, size })

    if (signal) {
      p.signal = signal
      p.onAbort = () => this._cancel(reqId, new PeerDriveError('已取消', ERR.CANCELLED))
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
      // 正常结束/失败时 _pend 里已经没有它了，这里是幂等的兜底清理；
      // 消费端提前 break 也会走到这，把那批已缓存块连同请求一起放掉。
      // 失败计数由 _failPending 负责，这里不重复计（用户主动取消不算失败）。
      this._cancel(reqId, new PeerDriveError('已取消', ERR.CANCELLED))
    }
  }

  /**
   * fetch 整体取回（Uint8Array）。等价于把 stream() 全收集起来。
   * 有内存闸：超过 maxBytes（默认 256MB）抛 TOO_LARGE——大文件请用 stream()。
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

  /** fetchBlob 取回为 Blob（浏览器预览/播放用；MIME 可由 name 猜）。 */
  async fetchBlob(hash, { name = '', mime = '', ...opts } = {}) {
    const bytes = await this.fetch(hash, opts)
    return new Blob([bytes], { type: mime || guessMime(name) })
  }

  /** fetchText 取回为文本（utf-8）。 */
  async fetchText(hash, opts = {}) {
    const bytes = await this.fetch(hash, opts)
    return new TextDecoder('utf-8').decode(bytes)
  }

  /**
   * saveAs 触发浏览器下载。走 Blob + <a download>，因此**整份内容在内存里**；
   * 与 fetch 共用同一个 maxBytes 闸。超大文件请用 stream() 自己落盘。
   */
  async saveAs(hash, filename = '', opts = {}) {
    if (typeof document === 'undefined') {
      throw new PeerDriveError('saveAs 需要 DOM 环境', ERR.PROTOCOL)
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
      // 立刻 revoke 会让部分浏览器（Safari）取消下载：给一点余量
      setTimeout(() => URL.revokeObjectURL(url), 30_000)
    }
    return blob.size
  }

  /** 便捷方法：从共享清单里直接取某个 entry/file 并保存（自动取文件名）。 */
  async saveShare(item, opts = {}) {
    if (!item || !item.hash) throw new PeerDriveError('saveShare 需要带 hash 的条目', ERR.PROTOCOL)
    const name = baseNameOf(item.name || item.path || '')
    return this.saveAs(item.hash, name, opts)
  }

  /** close 关闭连接（若本实例自己创建了 Peer 对象，一并销毁）。 */
  close() {
    this._unbind()
    this._failAll(new PeerDriveError('连接已关闭', ERR.CLOSED))
    try {
      if (typeof this.conn.close === 'function') this.conn.close()
    } catch {
      /* 连接已死 */
    }
    if (this._ownedPeer && typeof this._ownedPeer.destroy === 'function') {
      try {
        this._ownedPeer.destroy()
      } catch {
        /* 已销毁 */
      }
    }
    this._openState = false
  }

  // ── 内部：连接绑定与帧分派 ────────────────────────────────────────────────

  _bind() {
    this._onData = (data) => {
      // 帧顺序即协议语义（见 protocol.js 约束 2），必须同步处理、不排队
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
      // PSK：必须在**任何业务帧之前**出示（含 ready() 之后发的第一个请求）。
      // 放在 flush 之前，是因为 flush 会 resolve 等待者，它们的后续 send
      // 排在本次 send 之后 —— 顺序即门禁语义。
      this._sendPskAuth()
      this._flushOpenWaiters(null)
    }
    this._onClose = () => this._failAll(new PeerDriveError('连接已关闭', ERR.CLOSED))
    this._onError = (err) => {
      const msg = err && (err.message || err.type) ? err.message || err.type : String(err)
      this._failAll(new PeerDriveError(`连接错误：${msg}`, ERR.CLOSED))
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
    if (!frame) return // 非本协议帧：忽略（同连接可能有别的用途）
    switch (frame.type) {
      case 'meta':
        return this._onMeta(frame)
      case 'data':
        return this._onDataHead(frame)
      case 'done':
        return this._onDone(frame)
      case 'err':
        return this._onErr(frame)
      case 'share-resp':
        return this._onVerbReply(frame)
      case 'psk-ok':
        this.pskState = 'ok'
        this.pskError = null
        return
      case 'psk-err':
        this.pskState = 'err'
        this.pskError = frame.msg || 'psk: 对端拒绝了密钥'
        return
      default:
        return // 未知帧类型：前向兼容，忽略
    }
  }

  _onMeta(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return
    p.touch()
    const total = Number(frame.total)
    // total 为 -1 表示对端也无法确定大小（多源回源场景）——不是错误
    if (Number.isFinite(total) && total > MAX_FILE_BYTES) {
      return this._failPending(p, new PeerDriveError(`对端声明文件 ${total} 字节，超出协议上限`, ERR.TOO_LARGE))
    }
    if (Number.isFinite(total) && total > p.maxBytes) {
      return this._failPending(
        p,
        new PeerDriveError(
          `文件 ${total} 字节超过本地内存闸 ${p.maxBytes}（改用 stream() 流式落盘，或调大 maxBytes）`,
          ERR.TOO_LARGE,
        ),
      )
    }
    p.total = Number.isFinite(total) ? total : -1
    this._reportProgress(p)
  }

  _onDataHead(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return // 迟到的帧（已取消/已结束）：丢弃
    p.touch()
    const size = Number(frame.size)
    // 与 Go 侧 H6 同样设上限：块大小必须为正且不超过协议上限，否则是恶意/错协议
    if (!Number.isFinite(size) || size <= 0 || size > MAX_FILE_BYTES) {
      return this._failPending(p, new PeerDriveError(`非法数据块大小 ${frame.size}`, ERR.PROTOCOL))
    }
    p.blockSize = size
    p.blockGot = 0
    // 连接级 expect：把紧随其后的二进制块挂到这个请求上
    this._expect = p
  }

  _onChunk(data) {
    const p = this._expect
    if (!p) return // 没有前置 data 头的数据块：非协议内内容，丢弃
    const bytes = toUint8Array(data)
    // 注意：这里**不**把块存到请求状态里。块只进有界队列（背压），由消费端
    // 取走——"流式"的收益全在这；多存一份到 state 等于把它变回整体驻留内存。
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
        new PeerDriveError(`内容超过本地内存闸 ${p.maxBytes}（改用 stream()）`, ERR.TOO_LARGE),
      )
    }
  }

  _onDone(frame) {
    const p = this._pend.get(frame.reqId)
    if (!p) return
    p.touch()
    const declared = Number(frame.size)
    // 与 Go 侧一致的完整性闸：对端提前 done 会把截断内容当成功返回（静默损坏）
    if (Number.isFinite(declared) && declared >= 0 && p.received !== declared) {
      return this._failPending(
        p,
        new PeerDriveError(`传输不完整：收到 ${p.received} 字节，对端声明 ${declared} 字节`, ERR.INCOMPLETE, {
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
          new PeerDriveError(`内容与 hash 不符（期望 ${p.hash}，实得 ${got}）`, ERR.HASH_MISMATCH, {
            expected: p.hash,
            actual: got,
          }),
        )
      }
    }
    this._finishPending(p)
  }

  _onErr(frame) {
    const msg = frame.msg || '对端返回错误'
    // err 帧可能带 code（Go 侧门禁回 PSK_REQUIRED）。按 code 而不是文案分类：
    // 文案会随版本改，code 是协议的一部分。
    const code = frame.code === ERR.PSK_REQUIRED ? ERR.PSK_REQUIRED : ERR.PEER
    const err = () => new PeerDriveError(msg, code)
    // err 帧的 reqId 可能属于拉取，也可能属于一次性 verb
    const v = frame.reqId ? this._verbs.get(frame.reqId) : null
    if (v) return this._settleVerb(frame.reqId, null, err())
    const p = frame.reqId ? this._pend.get(frame.reqId) : null
    if (p) return this._failPending(p, err())
  }

  _onVerbReply(frame) {
    if (!frame.reqId) return
    this._settleVerb(frame.reqId, frame, null)
  }

  // ── 内部：请求生命周期 ───────────────────────────────────────────────────

  _assertHash(hash) {
    if (!isValidHash(hash)) {
      throw new PeerDriveError(
        `hash 必须是 64 位小写十六进制 sha256，收到 ${JSON.stringify(hash)}`,
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
      // 只对"全量请求"做内容寻址校验：range 请求拿到的片段本来就不等于整份
      // 内容的摘要（与 Go 侧 fetchReader.verify 同一条件）
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
        new PeerDriveError(`拉取 ${p.hash.slice(0, 12)}… 空闲超时（${this.opts.idleTimeoutMs}ms 无数据）`, ERR.TIMEOUT),
      )
    }, this.opts.idleTimeoutMs)
  }

  _reportProgress(p) {
    if (typeof p.onProgress === 'function') {
      try {
        p.onProgress(p.received, p.total, p.hash)
      } catch {
        /* 进度回调抛错不该影响传输 */
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
    // 坑：不等对端停止发送。协议里没有"取消"帧，对端会继续把这次请求发完，
    // 我们只是丢掉后续帧（_expect/_pend 里已查不到它）。要真正中断只能关连接。
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
        reject(new PeerDriveError(`对端 ${timeoutMs}ms 内没有应答`, ERR.TIMEOUT))
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
 * connect 包装一个已建立的连接，等它就绪后返回客户端。
 * 这是最常用的入口：`const client = await connect(dataConnection)`
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
 * connectToPeer 便捷入口：给出 PeerJS 构造函数与对方节点 id，一步到位。
 *
 * 本包不 import peerjs（保持零依赖、不污染使用方打包体积），所以构造函数由
 * 调用方传入——浏览器里就是全局的 `Peer`（CDN <script> 或 `import Peer from 'peerjs'`）。
 *
 * ⚠️ serialization 必须是 'raw'：只有 raw 模式下 string 走文本帧、ArrayBuffer
 * 走二进制帧，才能复刻 Go 侧「文本帧=JSON 头 / 二进制帧=数据块」的语义
 * （见 back/internal/transport/conn.go 顶部）。用默认的 binary 序列化会让
 * 数据块被 peerjs 自己的 chunker 包装，对端解析不出来。
 */
export async function connectToPeer(PeerCtor, peerId, { peerOptions = {}, connOptions = {}, ...opts } = {}) {
  if (typeof PeerCtor !== 'function') {
    throw new TypeError('peerdrive-client: connectToPeer 需要 PeerJS 的 Peer 构造函数')
  }
  const peer = new PeerCtor(peerOptions)
  try {
    await new Promise((resolve, reject) => {
      const onOpen = () => {
        peer.off?.('error', onError)
        resolve()
      }
      const onError = (e) => {
        peer.off?.('open', onOpen)
        reject(new PeerDriveError(`信令失败：${e?.message || e?.type || e}`, ERR.CLOSED))
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
      /* 已销毁 */
    }
    throw e
  }
}
