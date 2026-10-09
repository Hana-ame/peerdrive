// protocol.js — pure frame serialization and parsing for peerdrive protocols.
export const PROTOCOL_VERSION = 1

export function parseFrame(text) {
  if (typeof text !== 'string') return null
  try {
    const o = JSON.parse(text)
    if (o && typeof o === 'object' && typeof o.type === 'string') return o
  } catch {
    /* Non-JSON: not a control frame */
  }
  return null
}

export function isBinaryFrame(data) {
  if (typeof data === 'string') return false
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return true
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) return true
  if (typeof Blob !== 'undefined' && data instanceof Blob) return true
  return false
}

export function toUint8Array(data) {
  if (data instanceof Uint8Array) return data
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  }
  throw new TypeError('peerdrive-core: unsupported binary frame type')
}

export function nextReqId() {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
