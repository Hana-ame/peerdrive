// protocol.js — 帧协议定义（web ↔ node 两端共用）。
//
// 传输载体：peerjs DataConnection，serialization 必须为 'raw'——
// raw 模式下 string 原样走文本帧（PPID 51），ArrayBuffer 原样走二进制帧
// （PPID 53），对端 on('data') 收到的类型可直接区分。这复刻了 peerdrive
// Go 端「文本帧（JSON 头） vs 二进制帧（数据块）」的语义：
//   - 文本帧 = 控制头（JSON）
//   - 二进制帧 = 紧跟最近一个声明的数据块
//
// 帧序列（一次资源拉取）：
//   web → node:  {"type":"url","url":"...","reqId":"..."}
//   node → web:  {"type":"meta","status":200,"mime":"image/png","size":N,"reqId":"..."}
//                二进制帧×N（每块 chunkSize，不声明单独头——块属于最近 meta）
//                {"type":"done","reqId":"..."}
//   或失败:      {"type":"err","msg":"...","reqId":"..."}

export const PROTOCOL_VERSION = 1

// CHUNK_SIZE 数据块大小：64KB。
// 坑：raw 模式没有 peerjs binary 模式的 chunker（chunkedMTU 自动分片），
// 整块直接进 SCTP——块必须小于对端 maxMessageSize（Chrome/pion 均 256KB+），
// 64KB 在两端都安全。过小则帧开销大（每块一次 dc.send）。
export const CHUNK_SIZE = 64 * 1024

// makeUrlRequest 构造资源请求帧（web → node）。
export function makeUrlRequest(url, reqId) {
  return JSON.stringify({ type: 'url', url, reqId, v: PROTOCOL_VERSION })
}

// parseFrame 解析文本帧 JSON；非 JSON 返回 null。
export function parseFrame(text) {
  try {
    const o = JSON.parse(text)
    if (typeof o === 'object' && o !== null && typeof o.type === 'string') return o
  } catch {
    /* 非 JSON 文本：非本协议帧，返回 null */
  }
  return null
}

// isBinaryFrame 判断对端发来的数据是否二进制块。
// raw 模式下：string → 文本帧；ArrayBuffer/TypedArray/DataView → 二进制块。
// 注意：浏览器端 peerjs raw 模式收到的 Blob 会以 ArrayBuffer 呈现
// （DataConnection.binaryType 默认 arraybuffer），Node 端 @roamhq/wrtc 同理。
export function isBinaryFrame(data) {
  if (typeof data === 'string') return false
  if (data instanceof ArrayBuffer) return true
  if (ArrayBuffer.isView(data)) return true
  if (typeof Blob !== 'undefined' && data instanceof Blob) return true
  return false
}

// toUint8Array 把二进制帧统一为 Uint8Array（后续拼接 Blob）。
export function toUint8Array(data) {
  if (data instanceof Uint8Array) return data
  if (data instanceof ArrayBuffer) return new Uint8Array(data)
  if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  throw new Error('peerdrive-media: unsupported binary frame type')
}

// MIME 兜底表：Node 端响应缺 Content-Type 时按扩展名猜（video 必需才能渲染）。
const EXT_MIME = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif',
  webp: 'image/webp', avif: 'image/avif', svg: 'image/svg+xml', bmp: 'image/bmp',
  mp4: 'video/mp4', webm: 'video/webm', ogg: 'video/ogg', mov: 'video/quicktime',
  mkv: 'video/x-matroska', mp3: 'audio/mpeg', wav: 'audio/wav', m4a: 'audio/mp4',
  pdf: 'application/pdf',
}

export function guessMime(url, contentType) {
  if (contentType) {
    // 小写化：HTTP 头大小写不敏感（真实坑：上游回 "IMAGE/PNG" 时按原样
    // 返回，浏览器 <img> 仍能渲染但 isImageMime 判断会失败）
    const ct = contentType.split(';')[0].trim().toLowerCase()
    if (/^[a-z]+\/[a-z0-9.+-]+$/.test(ct)) return ct
  }
  const ext = (url.split('?')[0].split('.').pop() || '').toLowerCase()
  return EXT_MIME[ext] || 'application/octet-stream'
}

// nextReqId 生成请求 ID（时间戳+随机，避免多组件并发请求撞 ID）。
export function nextReqId() {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}