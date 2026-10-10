// protocol.js — frame protocol definition (shared between web and node sides).
//
// Transport carrier: peerjs DataConnection, serialization must be 'raw' —
// in raw mode string goes as text frame (PPID 51), ArrayBuffer goes as binary frame
// (PPID 53), so the receiving side's on('data') can directly distinguish types. This replicates the
// peerdrive Go side's "text frame (JSON header) vs binary frame (data block)" semantics:
//   - Text frame = control header (JSON)
//   - Binary frame = data block following the most recent declaration
//
// Multi-DataChannel architecture (2026-09-06):
//   - Control channel (control): keepalive ping/ping-ack
//   - File channel (file-{reqId}): one independent channel per file request, supports concurrent transfer
//   - File channel protocol: url → meta → blocks×N → done/err
//
// Control frame sequence (control channel):
//   web → node:  {"type":"ping"}
//   node → web:  {"type":"ping-ack"}
//
// File frame sequence (file channel):
//   web → node:  {"type":"url","url":"...","reqId":"..."}
//   node → web:  {"type":"meta","status":200,"mime":"image/png","size":N,"reqId":"..."}
//                Binary frames×N (each block chunkSize, no separate header declaration — blocks belong to this channel)
//                {"type":"done","reqId":"..."}
//   or failure:  {"type":"err","msg":"...","reqId":"..."}
//
// Note: PROTOCOL_VERSION (Issue #278) was previously included as `v: 1` in
// url frames. Removed because Go's dcReq/dcResp never had a version field —
// the field was dead payload on the Go side. Version negotiation is handled
// by the capability handshake (Issue #213, `cap` frames), not frame-level v.

export const PROTOCOL_VERSION = 1

// CHUNK_SIZE data block size: 64KB.
// Caveat: raw mode doesn't have peerjs binary mode's chunker (chunkedMTU auto-fragmentation),
// the entire block goes into SCTP directly — blocks must be smaller than the peer's maxMessageSize
// (Chrome/pion both 256KB+), 64KB is safe on both ends. Too small increases frame overhead
// (one dc.send per block).
export const CHUNK_SIZE = 64 * 1024

// makeUrlRequest constructs resource request frame (web → node).
export function makeUrlRequest(url, reqId) {
  return JSON.stringify({ type: 'url', url, reqId })
}

// parseFrame parses text frame JSON; returns null for non-JSON.
export function parseFrame(text) {
  try {
    const o = JSON.parse(text)
    if (typeof o === 'object' && o !== null && typeof o.type === 'string') return o
  } catch {
    /* Non-JSON text: not this protocol's frame, return null */
  }
  return null
}

// isBinaryFrame determines if data received from peer is a binary block.
// In raw mode: string → text frame; ArrayBuffer/TypedArray/DataView → binary block.
// Note: browser-side peerjs raw mode receives Blob as ArrayBuffer
// (DataConnection.binaryType defaults to arraybuffer), same for Node-side @roamhq/wrtc.
export function isBinaryFrame(data) {
  if (typeof data === 'string') return false
  if (data instanceof ArrayBuffer) return true
  if (ArrayBuffer.isView(data)) return true
  if (typeof Blob !== 'undefined' && data instanceof Blob) return true
  return false
}

// toUint8Array normalizes binary frames to Uint8Array (for subsequent Blob concatenation).
export function toUint8Array(data) {
  if (data instanceof Uint8Array) return data
  if (data instanceof ArrayBuffer) return new Uint8Array(data)
  if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  throw new Error('peerdrive-media: unsupported binary frame type')
}

// MIME fallback table: when Node-side response lacks Content-Type, guess by extension (required for video to render).
const EXT_MIME = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif',
  webp: 'image/webp', avif: 'image/avif', svg: 'image/svg+xml', bmp: 'image/bmp',
  mp4: 'video/mp4', webm: 'video/webm', ogg: 'video/ogg', mov: 'video/quicktime',
  mkv: 'video/x-matroska', mp3: 'audio/mpeg', wav: 'audio/wav', m4a: 'audio/mp4',
  pdf: 'application/pdf',
}

export function guessMime(url, contentType) {
  if (contentType) {
    // Lowercase: HTTP headers are case-insensitive (real issue: upstream returns "IMAGE/PNG" as-is,
    // browser <img> can still render but isImageMime check would fail)
    const ct = contentType.split(';')[0].trim().toLowerCase()
    if (/^[a-z]+\/[a-z0-9.+-]+$/.test(ct)) return ct
  }
  const ext = (url.split('?')[0].split('.').pop() || '').toLowerCase()
  return EXT_MIME[ext] || 'application/octet-stream'
}

// nextReqId generates request ID (timestamp+random, avoids ID collision in concurrent multi-component requests).
export function nextReqId() {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}
