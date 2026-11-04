// dispatcher.js — Connection-level frame dispatcher and slot manager (Single Source of Truth).
//
// Supports BOTH:
//   1. v2 32-byte self-routing binary headers ("PD02" + reqId + offset + flags, #378 A7),
//      allowing out-of-order / interleaved binary chunks from multiple concurrent requests
//      to route directly by reqId without head-of-line blocking;
//   2. v1 contiguous data/admin-bin text header + binary payload via connection-level _expect.
import { parseFrame, isBinaryFrame, toUint8Array, decodeBinaryHeader32 } from './protocol.js'

export class FrameDispatcher {
  constructor(options = {}) {
    this.options = options
    this._pending = new Map() // reqId -> handler record
    this._expect = null       // v1 connection-level expect: attached binary block routing
  }

  register(reqId, handler) {
    this._pending.set(reqId, handler)
  }

  unregister(reqId) {
    this._pending.delete(reqId)
    if (this._expect?.reqId === reqId) {
      this._expect = null
    }
  }

  has(reqId) {
    return this._pending.has(reqId)
  }

  get(reqId) {
    return this._pending.get(reqId)
  }

  handleIncoming(data) {
    if (isBinaryFrame(data)) {
      const bytes = toUint8Array(data)

      // 1. Check v2 32-byte self-routing header ("PD02") first — completely order-independent!
      const hdr32 = decodeBinaryHeader32(bytes)
      if (hdr32 && this._pending.has(hdr32.reqId)) {
        const h = this._pending.get(hdr32.reqId)
        if (this._expect?.reqId === hdr32.reqId) {
          this._expect = null
        }
        if (h && typeof h.onDataChunk === 'function') {
          h.onDataChunk(hdr32.payload, {
            reqId: hdr32.reqId,
            offset: hdr32.offset,
            size: hdr32.payload.byteLength,
            flags: hdr32.flags,
          })
          return true
        }
      }

      // 2. Fallback to v1 connection-level contiguous expect slot
      if (!this._expect) {
        return false // orphan binary chunk
      }
      const exp = this._expect
      exp.got = (exp.got || 0) + bytes.byteLength
      if (!exp.size || exp.got >= exp.size) {
        this._expect = null
      }
      const h = this._pending.get(exp.reqId)
      if (h && typeof h.onDataChunk === 'function') {
        h.onDataChunk(bytes, exp)
        return true
      }
      return false
    }

    const frame = parseFrame(data)
    if (!frame) return false

    // If frame declares incoming binary block (data or admin-bin header)
    if ((frame.type === 'data' || frame.type === 'admin-bin') && frame.reqId) {
      if (frame.size > 0) {
        this._expect = {
          type: frame.type,
          reqId: frame.reqId,
          offset: frame.offset || 0,
          size: frame.size,
          got: 0,
        }
      } else {
        this._expect = null
      }
      const h = this._pending.get(frame.reqId)
      if (h && typeof h.onDataHeader === 'function') {
        h.onDataHeader(frame)
      }
      return true
    }

    if (frame.reqId && this._pending.has(frame.reqId)) {
      const h = this._pending.get(frame.reqId)
      if (typeof h.onControlFrame === 'function') {
        h.onControlFrame(frame)
        return true
      }
    }

    return false
  }

  closeAll(err) {
    for (const [, h] of this._pending.entries()) {
      if (typeof h.onError === 'function') {
        h.onError(err || new Error('connection closed'))
      }
    }
    this._pending.clear()
    this._expect = null
  }
}
