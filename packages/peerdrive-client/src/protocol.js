// protocol.js — pure function implementation of peerdrive node frame protocol (zero-dependency, browser / Node compatible).
//
// Aligned **word-for-word** with the frame definitions in Go-side back/internal/transport/conn.go:
//
//	Request: {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"..."}
//	Response: {"type":"meta","hash","total","reqId"}
//	          {"type":"data","hash","offset","size","reqId"} + size bytes binary block immediately following
//	          {"type":"done","hash","offset","size","reqId"}
//	          {"type":"err","msg","reqId"}
//	Share: {"type":"share","reqId"}
//	          → {"type":"share-resp","collections":[…],"files":[…],"dirs":[…],"total":N,"reqId"}
//	Auth: {"type":"psk-auth","psk":"<key>"}
//	          → {"type":"psk-ok"} / {"type":"psk-err","msg":"...","code":"PSK_REQUIRED"}
//	          (only needed when node has PEERDRIVE_PSK set; unset = open mode, see pskAuthFrame)
//
// Three unchangeable constraints (breaking them won't cause errors, just silent misalignment):
//  1. data header is a **text frame**, data block is a **binary frame**. In raw serialization,
//     string goes via PPID 51, ArrayBuffer goes via PPID 53, so the peer can distinguish by type;
//     sending data blocks as JSON arrays etc. would be parsed as control frames on the peer side
//  2. data header and its data block must be **contiguous**. The receiver uses "connection-level
//     expect" — attaching the binary block to the request owned by the **most recent** data header,
//     rather than carrying reqId in the block. So you cannot interleave blocks from two requests
//     (Go-side SendFrame guarantees atomicity via connection-level sendMu)
//  3. Field names are aligned word-for-word. Changing `reqId` → `req_id` won't error, just make
//     the peer unable to route, causing requests to hang until timeout

export const PROTOCOL_VERSION = 1

// MAX_FILE_BYTES matches Go-side maxPeerFetchSize (8GB): peer declaring more than this is
// rejected immediately, preventing "malicious peer declares 1<<62 then keeps sending blocks" from
// overwhelming the receiver.
export const MAX_FILE_BYTES = 8 * 1024 * 1024 * 1024

// DEFAULT_MAX_BUFFER_BYTES browser-side memory gate.
// Separate from the 8GB protocol limit: the protocol limit is "how big can the server allow transfer",
// this is "how big this page is willing to hold in memory". Consumer-side fetch() keeps the entire
// content in memory — a 2GB file would crash the tab on a phone — default 256MB, exceeding throws
// TOO_LARGE and suggests using streaming instead.
export const DEFAULT_MAX_BUFFER_BYTES = 256 * 1024 * 1024

// isValidHash must have the same semantics as Go-side hashutil.IsStrictSHA256: **only** 64 lowercase hex.
// Why block locally: uppercase hex is judged as invalid hash on the Go side and returns an err frame,
// the error message appears after a network round trip of a few hundred milliseconds, easily misdiagnosed
// as "peer doesn't have this file" during debugging.
const HASH_RE = /^[0-9a-f]{64}$/

export function isValidHash(hash) {
  return typeof hash === 'string' && HASH_RE.test(hash)
}

// nextReqId generates request ID. Go-side uses UUID v4; here "timestamp+random" also satisfies
// uniqueness requirements (reqId is only used for response routing within a single connection,
// not across connections, not persisted).
export function nextReqId() {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

// reqFrame constructs file fetch request frame (initiated by the requesting role).
// size uses -1 to mean "read to end of file", consistent with Go-side semantics (negative = unlimited).
export function reqFrame(hash, { offset = 0, size = -1, reqId } = {}) {
  return JSON.stringify({ type: 'req', hash, offset, size, reqId, v: PROTOCOL_VERSION })
}

// shareFrame constructs share manifest query frame (different from list frame: list is the local
// management index full set, only open to trusted peers; share is the operator's **explicitly
// declared** external sharing scope).
export function shareFrame(reqId) {
  return JSON.stringify({ type: 'share', reqId, v: PROTOCOL_VERSION })
}

// pskAuthFrame constructs pre-shared key frame (PSK auth, see doc/NETDISK.md's "PSK Auth").
//
// Semantics: a node can set a pre-shared key (PEERDRIVE_PSK), once set the peer must present the
// same key on the **first frame** of the connection, otherwise all requests return err (code=PSK_REQUIRED).
// Nodes without a key are in open mode, not sending this frame still works normally (backward compatible).
//
// Two implementation conventions (aligned with Go-side back/internal/transport/psk.go):
//  1. **Send immediately after connection is established, and must be the first frame**.
//     DataChannel preserves order, the server processes in order and necessarily sees auth before
//     business frames — so this package **doesn't wait** for psk-ok before sending business requests,
//     not adding an extra RTT per connection.
//  2. **Key transmitted in plaintext**. DataChannel enforces DTLS, the key won't be exposed on the wire;
//     the server needs to store plaintext for comparison anyway. What we're guarding against is "strangers
//     connecting", not eavesdropping.
export function pskAuthFrame(psk) {
  return JSON.stringify({ type: 'psk-auth', psk: String(psk ?? ''), v: PROTOCOL_VERSION })
}

// PSK_REQUIRED is the machine-readable error code that Go-side auth returns in err frames.
// UI uses it to prompt "please enter key", **don't** match the err msg text (text may change).
export const PSK_REQUIRED = 'PSK_REQUIRED'

// UPLOAD_CHUNK upload chunk size, must match Go-side uploadChunkSize (64KB):
// the server maintains a bitmap at this granularity, mismatched granularity is judged as invalid offset.
export const UPLOAD_CHUNK = 64 * 1024

// uploadFrame constructs upload header frame, requesting the server to prepare to receive a chunk at offset.
//
// Protocol (aligned with Go-side inbound.go's serveUploadBegin): each time only one chunk is authorized,
// so upload is a "send header → wait meta → send binary → wait ack/uploaded" loop.
// Seems verbose, but this is how the server does flow control: it decides how much to receive each time,
// the caller just follows along.
export function uploadFrame(reqId, name, size, offset = 0) {
  return JSON.stringify({
    type: 'upload', name, size, offset, reqId, v: PROTOCOL_VERSION,
  })
}

// pullFrame constructs "network ingest" request frame: hand URL to the node, it downloads and registers.
//
// Division with upload: upload is "I have content, pushing to you", pull is "I only have the address, you fetch".
// This is the only verb on the node side where an external target address is specified, therefore with SSRF
// protection (public http/https only, size limit), and always subject to PSK auth
// (back/internal/transport/pull.go).
export function pullFrame(reqId, url, name = '') {
  return JSON.stringify({ type: 'pull', url, name, reqId, v: PROTOCOL_VERSION })
}

// parseFrame parses text frames. Returns null for "not this protocol's control frame" (non-JSON,
// or missing type) — the caller should ignore rather than error: the same connection may carry
// frames for other purposes.
export function parseFrame(text) {
  if (typeof text !== 'string') return null
  try {
    const o = JSON.parse(text)
    if (o && typeof o === 'object' && typeof o.type === 'string') return o
  } catch {
    /* Non-JSON: not this protocol's frame */
  }
  return null
}

// isBinaryFrame determines if received data is a data block.
// In raw serialization, the browser receives ArrayBuffer (or Blob, depending on binaryType),
// Node-side may receive Uint8Array/DataView — all treated as binary, strings are text frames.
export function isBinaryFrame(data) {
  if (typeof data === 'string') return false
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return true
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) return true
  if (typeof Blob !== 'undefined' && data instanceof Blob) return true
  return false
}

// toUint8Array normalizes any binary frame to Uint8Array (must preserve byteOffset/byteLength,
// directly new Uint8Array(view.buffer) would bring in the entire underlying buffer).
export function toUint8Array(data) {
  if (data instanceof Uint8Array) return data
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  }
  throw new TypeError('peerdrive-client: unsupported binary frame type')
}

// concatChunks concatenates blocks by known total length. Passing total allows one-shot allocation
// (avoids per-block expansion copying), but when total is undetermined (peer meta has -1),
// must use "collect first then merge".
export function concatChunks(chunks, total = -1) {
  if (chunks.length === 1) return chunks[0]
  let len = 0
  for (const c of chunks) len += c.byteLength
  const n = total >= 0 ? total : len
  const out = new Uint8Array(n)
  let off = 0
  for (const c of chunks) {
    if (off + c.byteLength > n) break // peer sent extra: truncate to declared length
    out.set(c, off)
    off += c.byteLength
  }
  return off === n ? out : out.subarray(0, off)
}

// sha256Hex computes content sha256 hex digest (for content-address verification).
// Implementation in sha256.js: one-shot path prefers WebCrypto, streaming path uses pure JS
// incremental implementation — callers needing compute-while-receiving (streaming fetch) use the Sha256 class directly.
export { Sha256, sha256Hex } from './sha256.js'

// guessMime guesses MIME by filename (meta frame doesn't carry mime, browser Blob needs it for proper
// preview/playback). Wrong guess only affects preview experience, not content correctness.
const EXT_MIME = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif',
  webp: 'image/webp', avif: 'image/avif', svg: 'image/svg+xml', bmp: 'image/bmp',
  mp4: 'video/mp4', webm: 'video/webm', ogg: 'video/ogg', mov: 'video/quicktime',
  mkv: 'video/x-matroska', mp3: 'audio/mpeg', wav: 'audio/wav', flac: 'audio/flac',
  m4a: 'audio/mp4', pdf: 'application/pdf', zip: 'application/zip',
  txt: 'text/plain;charset=utf-8', md: 'text/markdown;charset=utf-8',
  json: 'application/json', csv: 'text/csv;charset=utf-8',
}

export function guessMime(name, fallback = 'application/octet-stream') {
  const ext = String(name || '').split('?')[0].split('.').pop()?.toLowerCase()
  return EXT_MIME[ext] || fallback
}

// baseName extracts filename from share manifest path (peer gives relative path, may include directories).
export function baseName(p) {
  const s = String(p || '')
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'))
  return i >= 0 ? s.slice(i + 1) : s
}
