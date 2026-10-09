// dispatcher.js — Connection-level sequential frame dispatcher and slot manager.
import { parseFrame, isBinaryFrame, toUint8Array } from './protocol.js'

export class FrameDispatcher {
  constructor(options = {}) {
    this.options = options
    this._pending = new Map() // reqId -> handler record
    this._expect = null      // connection-level expect: attached binary block routing
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
      if (!this._expect) {
        return false // orphan binary chunk
      }
      const exp = this._expect
      this._expect = null
      const h = this._pending.get(exp.reqId)
      if (h && typeof h.onDataChunk === 'function') {
        h.onDataChunk(toUint8Array(data), exp)
        return true
      }
      return false
    }

    const frame = parseFrame(data)
    if (!frame) return false

    // If frame declares incoming binary block (e.g. data header)
    if (frame.type === 'data' && frame.reqId) {
      this._expect = { reqId: frame.reqId, offset: frame.offset, size: frame.size }
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
    for (const [reqId, h] of this._pending.entries()) {
      if (typeof h.onError === 'function') {
        h.onError(err || new Error('connection closed'))
      }
    }
    this._pending.clear()
    this._expect = null
  }
}
