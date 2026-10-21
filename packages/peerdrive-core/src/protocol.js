// protocol.js — Single Source of Truth (peerdrive-core) for Peerdrive frame protocol,
// binary chunk codecs, v2 envelopes (#378), and 32-byte self-routing binary headers (#378 A7).
//
// Shared by peerdrive-client, peerdrive-media, and front/ (Issue #396).

export { Sha256, sha256Hex } from './sha256.js'
import { safeJsonParse, MAX_MANIFEST_BYTES } from './security.js'

export const PROTOCOL_VERSION = 1

// MAX_FILE_BYTES matches Go-side maxPeerFetchSize (8GB).
export const MAX_FILE_BYTES = 8 * 1024 * 1024 * 1024

// DEFAULT_MAX_BUFFER_BYTES browser-side in-memory assembly gate (256MB).
export const DEFAULT_MAX_BUFFER_BYTES = 256 * 1024 * 1024

// UPLOAD_CHUNK and CHUNK_SIZE match Go-side 64KB transfer slice size.
export const UPLOAD_CHUNK = 64 * 1024
export const CHUNK_SIZE = 64 * 1024

// CLIENT_CAPABILITIES declared in psk-auth / cap frames (Issue #213).
export const CLIENT_CAPABILITIES = ['req', 'share', 'index', 'pull']

// PSK_REQUIRED machine-readable error code returned by Go-side PSK gate.
export const PSK_REQUIRED = 'PSK_REQUIRED'

const HASH_RE = /^[0-9a-f]{64}$/

export function isValidHash(hash) {
  return typeof hash === 'string' && HASH_RE.test(hash)
}

let _reqSeq = 0
export function nextReqId() {
  _reqSeq = (_reqSeq + 1) & 0xfffff
  return `${Date.now().toString(36)}-${_reqSeq.toString(36)}-${Math.random().toString(36).slice(2, 8)}`
}

export function reqFrame(hash, { offset = 0, size = -1, reqId } = {}) {
  return JSON.stringify({ type: 'req', hash, offset, size, reqId })
}

export function shareFrame(reqId) {
  return JSON.stringify({ type: 'share', reqId })
}

export function pskAuthFrame(psk, capabilities = CLIENT_CAPABILITIES) {
  const payload = { type: 'psk-auth', psk: String(psk ?? '') }
  if (Array.isArray(capabilities) && capabilities.length > 0) {
    payload.capabilities = capabilities
  }
  return JSON.stringify(payload)
}

export function capFrame(capabilities = CLIENT_CAPABILITIES) {
  return JSON.stringify({ type: 'cap', capabilities: capabilities || CLIENT_CAPABILITIES })
}

export function uploadFrame(reqId, name, size, offset = 0) {
  return JSON.stringify({
    type: 'upload', name, size, offset, reqId,
  })
}

export function pullFrame(reqId, url, name = '') {
  return JSON.stringify({ type: 'pull', url, name, reqId })
}

export function makeUrlRequest(url, reqId) {
  return JSON.stringify({ type: 'url', url, reqId, v: PROTOCOL_VERSION })
}

// v2 unified envelope builder (#378 A4): {"event":"file.meta","req_id":"...","code":0,"data":{...}}
export const EVENT_TO_TYPE = {
  'file.req': 'req',
  'file.meta': 'meta',
  'file.data': 'data',
  'file.done': 'done',
  'file.err': 'err',
  'share.req': 'share',
  'share.resp': 'share-resp',
  'auth.psk': 'psk-auth',
  'auth.psk_ok': 'psk-ok',
  'auth.psk_err': 'psk-err',
  'admin.request': 'admin',
  'admin.response': 'admin-resp',
  'admin.binary': 'admin-bin',
  'tunnel.open': 'fwd-open',
  'tunnel.auth': 'fwd-auth',
  'tunnel.data': 'fwd-data',
  'tunnel.close': 'fwd-close',
}

export function makeEnvelope(event, reqId, data = {}, code = 0) {
  return JSON.stringify({
    event,
    req_id: reqId || '',
    code,
    data: data || {},
  })
}

// parseFrame parses a control text frame using safeJsonParse (protecting against
// Prototype Pollution __proto__/constructor/prototype, deep nesting bombs, and oversized frames).
// Preserves exact shape for v1 {"type":...} frames (so deepEqual assertions pass),
// and transparently normalizes v2 {"event":...,"req_id":...,"data":{...}} envelopes (#378).
export function parseFrame(text) {
  if (typeof text !== 'string') return null
  const o = safeJsonParse(text, { maxBytes: MAX_MANIFEST_BYTES, throwOnError: false })
  if (!o || typeof o !== 'object' || Array.isArray(o)) return null
  if (typeof o.type === 'string') return o
  if (typeof o.event === 'string') {
    const mappedType = EVENT_TO_TYPE[o.event] || o.event.replace(/\./g, '-')
    const dataObj = o.data && typeof o.data === 'object' && !Array.isArray(o.data) ? o.data : {}
    const out = { ...dataObj, ...o, type: mappedType }
    if (o.req_id && !out.reqId) out.reqId = o.req_id
    return out
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

// 32-byte binary frame header (#378 A7):
// [0..4]:   "PD02" magic (0x50, 0x44, 0x30, 0x32)
// [4..20]:  req_id ASCII padded with NUL (16 bytes)
// [20..28]: offset uint64 big-endian (8 bytes)
// [28..32]: flags uint32 big-endian (4 bytes)
// Followed immediately by raw payload bytes.
export function encodeBinaryHeader32(reqId, offset, payload, flags = 0) {
  const body = toUint8Array(payload)
  const out = new Uint8Array(32 + body.byteLength)
  out[0] = 0x50 // 'P'
  out[1] = 0x44 // 'D'
  out[2] = 0x30 // '0'
  out[3] = 0x32 // '2'
  const idStr = String(reqId || '').slice(0, 16)
  for (let i = 0; i < idStr.length; i++) {
    out[4 + i] = idStr.charCodeAt(i) & 0xff
  }
  const dv = new DataView(out.buffer, out.byteOffset, 32)
  const hi = Math.floor(Number(offset || 0) / 4294967296) >>> 0
  const lo = Number(offset || 0) >>> 0
  dv.setUint32(20, hi, false)
  dv.setUint32(24, lo, false)
  dv.setUint32(28, flags >>> 0, false)
  out.set(body, 32)
  return out
}

export function decodeBinaryHeader32(data) {
  const bytes = toUint8Array(data)
  if (
    bytes.byteLength < 32 ||
    bytes[0] !== 0x50 ||
    bytes[1] !== 0x44 ||
    bytes[2] !== 0x30 ||
    bytes[3] !== 0x32
  ) {
    return null
  }
  let reqId = ''
  for (let i = 4; i < 20; i++) {
    if (bytes[i] === 0) break
    reqId += String.fromCharCode(bytes[i])
  }
  if (!reqId) return null
  const dv = new DataView(bytes.buffer, bytes.byteOffset, 32)
  const hi = dv.getUint32(20, false)
  const lo = dv.getUint32(24, false)
  const offset = hi * 4294967296 + lo
  const flags = dv.getUint32(28, false)
  const payload = bytes.subarray(32)
  return { reqId, offset, flags, payload }
}

export function concatChunks(chunks, total = -1) {
  if (chunks.length === 1) return chunks[0]
  let len = 0
  for (const c of chunks) len += c.byteLength
  const n = total >= 0 ? total : len
  const out = new Uint8Array(n)
  let off = 0
  for (const c of chunks) {
    if (off + c.byteLength > n) break
    out.set(c, off)
    off += c.byteLength
  }
  return off === n ? out : out.subarray(0, off)
}

const EXT_MIME = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif',
  webp: 'image/webp', avif: 'image/avif', svg: 'image/svg+xml', bmp: 'image/bmp',
  mp4: 'video/mp4', webm: 'video/webm', ogg: 'video/ogg', mov: 'video/quicktime',
  mkv: 'video/x-matroska', mp3: 'audio/mpeg', wav: 'audio/wav', flac: 'audio/flac',
  m4a: 'audio/mp4', pdf: 'application/pdf', zip: 'application/zip',
  txt: 'text/plain;charset=utf-8', md: 'text/markdown;charset=utf-8',
  json: 'application/json', csv: 'text/csv;charset=utf-8',
}

// Unified guessMime supporting both (name, fallback) and (url, contentTypeHeader).
export function guessMime(nameOrUrl, contentTypeOrFallback = 'application/octet-stream') {
  if (typeof contentTypeOrFallback === 'string' && contentTypeOrFallback.trim()) {
    const ct = contentTypeOrFallback.split(';')[0].trim().toLowerCase()
    if (ct !== 'application/octet-stream' && /^[a-z]+\/[a-z0-9.+-]+$/.test(ct)) {
      return ct
    }
  }
  const clean = String(nameOrUrl || '').split('?')[0].split('#')[0]
  const dotIdx = clean.lastIndexOf('.')
  const ext = dotIdx >= 0 ? clean.slice(dotIdx + 1).toLowerCase() : ''
  if (ext && EXT_MIME[ext]) {
    return EXT_MIME[ext]
  }
  return contentTypeOrFallback || 'application/octet-stream'
}

export function baseName(p) {
  const s = String(p || '')
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'))
  return i >= 0 ? s.slice(i + 1) : s
}
