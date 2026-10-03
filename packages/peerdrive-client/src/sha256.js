// sha256.js — 零依赖 SHA-256（增量式），用于内容寻址校验。
//
// 为什么要自己实现而不只用 WebCrypto：`crypto.subtle.digest()` 是**一次性**的
// ——必须先把整份内容攒在内存里才能算摘要，这与本包的流式接口直接冲突
// （stream() 的卖点就是不驻留内存）。Go 侧 /peerjs/fetch 的语义是"全量请求读完后
// 校验 sha256，不匹配即报错"，消费端要给出同样的保证，就必须能边收边算。
//
// 另一个副作用是省钱：WebCrypto 只在**安全上下文**（https / localhost）可用，
// 纯 JS 实现在任意 http 页面都能跑（demo 直接 `python -m http.server` 就能验）。
//
// 性能：纯 JS 约 30–80 MB/s。一次性接口 sha256Hex() 优先走 WebCrypto（快一个
// 数量级），流式接口走这里的增量实现；两条路径在测试里互相校验（含 63/64/65
// 这类跨块边界长度）。

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

const H0 = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]

/** 增量 SHA-256。update() 可任意次调用（块边界自动处理），digestHex() 可重复调用。 */
export class Sha256 {
  constructor() {
    this.h = Int32Array.from(H0)
    this.buf = new Uint8Array(64)
    this.bufLen = 0
    this.total = 0 // 已 feed 的**字节**数（不是块数）
    this._w = new Int32Array(64)
  }

  /** update 追加一段内容。接受 Uint8Array / ArrayBuffer / TypedArray。 */
  update(data) {
    const bytes = toBytes(data)
    this.total += bytes.length
    let off = 0
    // 先把上次残留的半个块补满
    if (this.bufLen > 0) {
      const take = Math.min(64 - this.bufLen, bytes.length)
      this.buf.set(bytes.subarray(0, take), this.bufLen)
      this.bufLen += take
      off = take
      if (this.bufLen === 64) {
        this._block(this.buf, 0)
        this.bufLen = 0
      }
    }
    // 整块直接算（不复制）
    while (off + 64 <= bytes.length) {
      this._block(bytes, off)
      off += 64
    }
    // 尾巴留着
    if (off < bytes.length) {
      this.buf.set(bytes.subarray(off), 0)
      this.bufLen = bytes.length - off
    }
    return this
  }

  /**
   * digestHex 输出 64 位小写十六进制摘要。
   * 可重复调用：内部先快照 h 再填充/压缩，调用后对象状态不变（调用方可能在
   * 出错分支里重算一次用于日志）。
   */
  digestHex() {
    const saved = this.h.slice()
    const savedBufLen = this.bufLen
    const savedTotal = this.total
    try {
      const bitLen = this.total * 8
      // 补 0x80 + 长度：留 8 字节存 64 位长度。bufLen>=56 时不够放，需要两块
      const padLen = this.bufLen < 56 ? 64 : 128
      const pad = new Uint8Array(padLen)
      pad.set(this.buf.subarray(0, this.bufLen), 0)
      pad[this.bufLen] = 0x80
      const dv = new DataView(pad.buffer)
      // bitLen 用「高位 = floor(total / 2^29)」拆开，避免 total*8 溢出 2^53
      dv.setUint32(padLen - 8, Math.floor(this.total / 536870912))
      dv.setUint32(padLen - 4, bitLen >>> 0)
      for (let i = 0; i < padLen; i += 64) this._block(pad, i)

      let out = ''
      for (let i = 0; i < 8; i++) out += (this.h[i] >>> 0).toString(16).padStart(8, '0')
      return out
    } finally {
      this.h.set(saved)
      this.bufLen = savedBufLen
      this.total = savedTotal
    }
  }

  _block(p, off) {
    const w = this._w
    for (let i = 0; i < 16; i++) {
      w[i] = (p[off + 4 * i] << 24) | (p[off + 4 * i + 1] << 16) | (p[off + 4 * i + 2] << 8) | p[off + 4 * i + 3]
    }
    for (let i = 16; i < 64; i++) {
      const x = w[i - 15]
      const y = w[i - 2]
      const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3)
      const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10)
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) | 0
    }
    const h = this.h
    let a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7]
    for (let i = 0; i < 64; i++) {
      const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7))
      const ch = (e & f) ^ (~e & g)
      const t1 = (hh + S1 + ch + K[i] + w[i]) | 0
      const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10))
      const maj = (a & b) ^ (a & c) ^ (b & c)
      const t2 = (S0 + maj) | 0
      hh = g
      g = f
      f = e
      e = (d + t1) | 0
      d = c
      c = b
      b = a
      a = (t1 + t2) | 0
    }
    h[0] = (h[0] + a) | 0
    h[1] = (h[1] + b) | 0
    h[2] = (h[2] + c) | 0
    h[3] = (h[3] + d) | 0
    h[4] = (h[4] + e) | 0
    h[5] = (h[5] + f) | 0
    h[6] = (h[6] + g) | 0
    h[7] = (h[7] + hh) | 0
  }
}

function toBytes(data) {
  if (data instanceof Uint8Array) return data
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  }
  throw new TypeError('peerdrive-client: sha256 expects bytes')
}

/**
 * sha256Hex 一次性摘要：优先 WebCrypto（快），不可用时退回纯 JS。
 * 两条路径在同一次调用里不会混用，结果一致（测试里逐长度比对过）。
 */
export async function sha256Hex(bytes) {
  const subtle = globalThis.crypto && globalThis.crypto.subtle
  if (subtle && typeof subtle.digest === 'function') {
    try {
      const digest = await subtle.digest('SHA-256', toBytes(bytes))
      const view = new Uint8Array(digest)
      let out = ''
      for (let i = 0; i < view.length; i++) out += view[i].toString(16).padStart(2, '0')
      return out
    } catch {
      // 某些环境（老 Safari / 非安全上下文）会抛：静默退回纯 JS
    }
  }
  return new Sha256().update(bytes).digestHex()
}
