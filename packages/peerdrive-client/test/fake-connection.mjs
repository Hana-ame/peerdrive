// fake-connection.mjs — 测试用假连接：满足 PeerDriveClient 要求的
// { on(type, cb), send(data), open, close() }，并额外提供"扮演服务端"的能力。
//
// 为什么要假连接：真跑 WebRTC 需要信令服务器 + 两个运行时，测试会变成集成测试
// （慢、抖、CI 不稳）。假连接把「协议状态机」与「传输」解耦，让客户端的分支
// ——完整性校验、超时、取消、乱序帧——都能被确定性地覆盖。真 WebRTC 链路由
// back/test/integration 的 Go 侧集成测试与 demo 页面覆盖。

export class FakeConnection {
  constructor({ peer = 'peerdrive-fake', open = true, autoServe = true } = {}) {
    this.peer = peer
    this.open = open
    this.sent = []
    this.closed = false
    this._handlers = new Map()
    // hash → {bytes, chunkSize, total}：send() 收到 req 帧时自动扮演服务端
    this._serves = new Map()
    // 'share' 帧的应答快照；null = 回空
    this.shareSnapshot = null
    // 自动应答开关。关掉后由测试手动 emit 帧——测超时/取消/故障注入必须用它，
    // 否则请求会在 send() 里就被立刻答完，根本进不到被测分支。
    this.autoServe = autoServe
    // 观测钩子：每次收到请求帧时回调（测试用它做故障注入）
    this.onServe = null
  }

  // ── 连接接口 ──
  on(type, cb) {
    if (!this._handlers.has(type)) this._handlers.set(type, [])
    this._handlers.get(type).push(cb)
    return this
  }

  off(type, cb) {
    const list = this._handlers.get(type)
    if (list) this._handlers.set(type, list.filter((f) => f !== cb))
    return this
  }

  send(data) {
    if (this.closed) throw new Error('fake connection is closed')
    this.sent.push(data)
    if (this.autoServe && typeof data === 'string') this._serve(JSON.parse(data))
  }

  close() {
    if (this.closed) return
    this.closed = true
    this.emit('close')
  }

  // ── 测试辅助 ──
  emit(type, ...args) {
    for (const cb of [...(this._handlers.get(type) || [])]) cb(...args)
  }

  /** 文本帧（服务端 → 客户端）。 */
  replyText(obj) {
    this.emit('data', JSON.stringify(obj))
  }

  /** 二进制块：模拟 raw 序列化下 PeerJS 交付的 ArrayBuffer。 */
  replyChunk(bytes) {
    const u8 = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes)
    this.emit('data', u8.buffer.slice(u8.byteOffset, u8.byteOffset + u8.byteLength))
  }

  /** 收到的 JSON 帧（字符串帧解析后）。 */
  frames() {
    return this.sent.filter((s) => typeof s === 'string').map((s) => JSON.parse(s))
  }

  framesOf(type) {
    return this.frames().filter((f) => f.type === type)
  }

  lastFrame() {
    const all = this.frames()
    return all[all.length - 1]
  }

  /** 让某个 hash 可被拉取。total 传 -1 可模拟"对端不知大小"。 */
  serve(hash, bytes, { chunkSize = 8, total } = {}) {
    this._serves.set(hash, { bytes, chunkSize, total })
    return this
  }

  _serve(frame) {
    if (this.onServe) this.onServe(frame)
    if (frame.type === 'share') {
      const snap = this.shareSnapshot || { collections: [], files: [], dirs: [] }
      const total = (snap.collections?.length || 0) + (snap.files?.length || 0)
      this.replyText({ type: 'share-resp', ...snap, total, reqId: frame.reqId })
      return
    }
    if (frame.type !== 'req') return
    const entry = this._serves.get(frame.hash)
    if (!entry) {
      this.replyText({ type: 'err', hash: frame.hash, msg: 'not found', reqId: frame.reqId })
      return
    }
    const { bytes, chunkSize, total } = entry
    this.replyText({
      type: 'meta',
      hash: frame.hash,
      total: total === undefined ? bytes.length : total,
      reqId: frame.reqId,
    })
    for (let off = 0; off < bytes.length; off += chunkSize) {
      const block = bytes.subarray(off, Math.min(off + chunkSize, bytes.length))
      this.replyText({
        type: 'data',
        hash: frame.hash,
        offset: off,
        size: block.length,
        reqId: frame.reqId,
      })
      this.replyChunk(block)
    }
    this.replyText({
      type: 'done',
      hash: frame.hash,
      offset: frame.offset || 0,
      size: bytes.length,
      reqId: frame.reqId,
    })
  }
}

/** 确定性伪随机内容（避免测试里出现随机失败，但仍能覆盖多块边界）。 */
export function makeBytes(len, seed = 1) {
  const out = new Uint8Array(len)
  let x = seed | 0
  for (let i = 0; i < len; i++) {
    x = (x * 1103515245 + 12345) & 0x7fffffff
    out[i] = x & 0xff
  }
  return out
}

/** 独立于本包的 sha256 实现（用 node:crypto 做 oracle，避免自证自洽）。 */
export async function expectHash(bytes) {
  const { createHash } = await import('node:crypto')
  return createHash('sha256').update(bytes).digest('hex')
}
