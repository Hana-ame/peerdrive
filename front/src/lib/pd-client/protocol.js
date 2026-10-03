// protocol.js — pure-function implementation of the peerdrive node frame protocol
// (zero dependencies, works in browser / Node).
//
// **Character-for-character aligned** with the Go-side frame definitions in
// back/internal/transport/conn.go:
//
//	Request: {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"..."}
//	Response: {"type":"meta","hash","total","reqId"}
//	      {"type":"data","hash","offset","size","reqId"} + immediately following size bytes of binary chunk
//	      {"type":"done","hash","offset","size","reqId"}
//	      {"type":"err","msg","reqId"}
//	Share: {"type":"share","reqId"}
//	      → {"type":"share-resp","collections":[…],"files":[…],"dirs":[…],"total":N,"reqId"}
//	Gate:  {"type":"psk-auth","psk":"<secret>"}
//	      → {"type":"psk-ok"} / {"type":"psk-err","msg":"...","code":"PSK_REQUIRED"}
//	      (only required if the node sets PEERDRIVE_PSK; unset = open mode, see pskAuthFrame)
//
// Three constraints that cannot be broken (breaking them won't error, they will
// only silently misalign):
//  1. The data head is a **text frame** and the data chunk is a **binary frame**.
//     Under raw serialization, string goes via PPID 51 and ArrayBuffer via
//     PPID 53, so the peer can distinguish by type; sending the data chunk as a
//     JSON array or similar would be parsed as a control frame on the peer side
//  2. The data head and its chunk must be **contiguous**. The receiver uses
//     "connection-level expect" — attaching the binary chunk to the request of
//     the **most recent** data head, rather than carrying reqId in the chunk.
//     So chunks of two requests must not be interleaved (the Go-side SendFrame
//     guarantees atomicity via the connection-level sendMu)
//  3. Field names must be character-for-character aligned. Changing `reqId` →
//     `req_id` won't error, it will just cause the peer to fail to route and
//     the request to hang until timeout

export const PROTOCOL_VERSION = 1

// MAX_FILE_BYTES matches Go-side maxPeerFetchSize (8GB): if the peer declares
// more than this, refuse directly, preventing a "malicious peer declares 1<<62
// and keeps sending chunks" from overwhelming the receiver.
export const MAX_FILE_BYTES = 8 * 1024 * 1024 * 1024

// DEFAULT_MAX_BUFFER_BYTES: the memory gate on the browser side.
// Kept separate from the 8GB protocol cap: the protocol cap is "how large the
// server allows to be sent", while this is "how large this page is willing to
// buffer in memory". Consumer-side fetch() keeps the whole content in memory,
// and a 2GB file would crash the tab on a phone — default 256MB, exceeding it
// reports TOO_LARGE and suggests using the streaming path.
export const DEFAULT_MAX_BUFFER_BYTES = 256 * 1024 * 1024

// isValidHash must have the same semantics as Go-side hashutil.IsStrictSHA256:
// **only** 64 lowercase hex characters.
// Why catch locally: uppercase hex is judged invalid hash on the Go side and
// returns an err frame, with the error message appearing after a network round
// trip hundreds of ms later — easy to misdiagnose as "peer doesn't have the
// file" during debugging.
const HASH_RE = /^[0-9a-f]{64}$/

export function isValidHash(hash) {
  return typeof hash === 'string' && HASH_RE.test(hash)
}

// nextReqId generates a request ID. Go side uses UUID v4; here "timestamp +
// random" satisfies uniqueness (reqId is only used for response routing within
// a single connection, not across connections and not persisted).
export function nextReqId() {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

// reqFrame builds a file-fetch request frame (outbound role initiates).
// size = -1 means "read to end of file", matching Go-side semantics (negative
// means unbounded).
export function reqFrame(hash, { offset = 0, size = -1, reqId } = {}) {
  return JSON.stringify({ type: 'req', hash, offset, size, reqId, v: PROTOCOL_VERSION })
}

// shareFrame builds a share-list query frame (different from the list frame:
// list is the full local management index, only exposed to trusted peers;
// share is the operator's **explicitly declared** outward sharing scope).
export function shareFrame(reqId) {
  return JSON.stringify({ type: 'share', reqId, v: PROTOCOL_VERSION })
}

// pskAuthFrame builds a pre-shared-key frame (PSK gate, see the "PSK Gate"
// section in doc/NETDISK.md).
//
// Semantics: a node can set a pre-shared key (PEERDRIVE_PSK); once set, the
// peer must present the same key in the **very first frame** of the connection,
// otherwise all requests return err (code=PSK_REQUIRED). A node without a key
// is in open mode and serves normally without this frame (backward compatible).
//
// Two implementation conventions (aligned with Go-side
// back/internal/transport/psk.go):
//  1. **Send immediately after the connection is established, and it must be
//     the first frame**. DataChannel preserves order; the server processes in
//     order, so it will see auth before business frames — hence this package
//     does **not wait** for psk-ok before sending business requests, avoiding
//     an extra RTT per connection.
//  2. **The key is sent in cleartext**. DataChannel is DTLS-mandatory, so the
//     key does not travel naked on the link; the server also stores it in
//     cleartext for comparison. What's being protected against is "strangers
//     connecting", not eavesdropping.
export function pskAuthFrame(psk) {
  return JSON.stringify({ type: 'psk-auth', psk: String(psk ?? ''), v: PROTOCOL_VERSION })
}

// PSK_REQUIRED is the machine-readable error code the Go-side gate carries in
// err frames.
// The UI uses it to prompt "please fill in the key"; **do not** match the err
// msg text (the text will change).
export const PSK_REQUIRED = 'PSK_REQUIRED'

// UPLOAD_CHUNK upload chunk size, must match Go-side uploadChunkSize (64KB):
// the server maintains a bitset at this granularity; mismatched granularity is
// judged as an invalid offset.
export const UPLOAD_CHUNK = 64 * 1024

// uploadFrame builds an upload head frame, asking the server to prepare to
// receive a chunk at offset.
//
// Protocol (aligned with Go-side inbound.go's serveUploadBegin): each round
// only authorizes one chunk, so upload is a "send head → wait meta → send
// binary → wait ack/uploaded" loop. It looks verbose, but this is the server's
// flow control: it decides how much to receive each round, the caller just
// follows.
export function uploadFrame(reqId, name, size, offset = 0) {
  return JSON.stringify({
    type: 'upload', name, size, offset, reqId, v: PROTOCOL_VERSION,
  })
}

// pullFrame builds a "network ingest" request frame: hand a URL to the node,
// which downloads and registers it.
//
// Division of labor with upload: upload is "I have content, pushing it to you",
// pull is "I only have an address, you go fetch it". This is the only verb on
// the node side where an external party specifies the target address, hence it
// has SSRF protection (only public http/https, size cap), and is always subject
// to the PSK gate (back/internal/transport/pull.go).
export function pullFrame(reqId, url, name = '') {
  return JSON.stringify({ type: 'pull', url, name, reqId, v: PROTOCOL_VERSION })
}

// parseFrame parses a text frame. Returns null meaning "not a control frame of
// this protocol" (non-JSON, or missing type) — the caller should ignore rather
// than error: the same connection may carry frames for other purposes.
export function parseFrame(text) {
  if (typeof text !== 'string') return null
  try {
    const o = JSON.parse(text)
    if (o && typeof o === 'object' && typeof o.type === 'string') return o
  } catch {
    /* non-JSON: not a frame of this protocol */
  }
  return null
}

// isBinaryFrame determines whether received data is a data chunk.
// Under raw serialization the browser receives ArrayBuffer (or Blob, depending
// on binaryType), and Node may receive Uint8Array/DataView — all treated as
// binary; strings are text frames.
export function isBinaryFrame(data) {
  if (typeof data === 'string') return false
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return true
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) return true
  if (typeof Blob !== 'undefined' && data instanceof Blob) return true
  return false
}

// toUint8Array normalizes any binary frame to Uint8Array (must preserve
// byteOffset/byteLength; directly `new Uint8Array(view.buffer)` would pull in
// the whole underlying buffer).
export function toUint8Array(data) {
  if (data instanceof Uint8Array) return data
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  }
  throw new TypeError('peerdrive-client: unsupported binary frame type')
}

// concatChunks concatenates chunks according to a known total length. Passing
// total allows one-shot allocation (avoiding per-chunk growth copying), but if
// total is undetermined (peer's meta is -1), must use "collect then merge".
export function concatChunks(chunks, total = -1) {
  if (chunks.length === 1) return chunks[0]
  let len = 0
  for (const c of chunks) len += c.byteLength
  const n = total >= 0 ? total : len
  const out = new Uint8Array(n)
  let off = 0
  for (const c of chunks) {
    if (off + c.byteLength > n) break // peer over-sent: truncate to declared length
    out.set(c, off)
    off += c.byteLength
  }
  return off === n ? out : out.subarray(0, off)
}

// sha256Hex computes the sha256 hex digest of content (for content-address
// verification).
// The implementation lives in sha256.js: one-shot path prefers WebCrypto, the
// streaming path uses a pure-JS incremental implementation — callers needing
// to compute as they receive (streaming fetch) use the Sha256 class directly.
export { Sha256, sha256Hex } from './sha256.js'

// guessMime guesses MIME by filename (the meta frame doesn't carry mime; the
// browser Blob needs it for correct preview/playback). A wrong guess only
// affects preview experience, not content correctness.
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

// baseName extracts the filename from a share-list path (the peer gives a
// relative path, which may include directories).
export function baseName(p) {
  const s = String(p || '')
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'))
  return i >= 0 ? s.slice(i + 1) : s
}