// protocol.js — peerdrive 节点帧协议的纯函数实现（零依赖，浏览器 / Node 通用）。
//
// 与 Go 侧 back/internal/transport/conn.go 的帧定义**逐字对齐**：
//
//	请求: {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"..."}
//	响应: {"type":"meta","hash","total","reqId"}
//	      {"type":"data","hash","offset","size","reqId"} + 紧随 size 字节二进制块
//	      {"type":"done","hash","offset","size","reqId"}
//	      {"type":"err","msg","reqId"}
//	共享: {"type":"share","reqId"}
//	      → {"type":"share-resp","collections":[…],"files":[…],"dirs":[…],"total":N,"reqId"}
//	门禁: {"type":"psk-auth","psk":"<密钥>"}
//	      → {"type":"psk-ok"} / {"type":"psk-err","msg":"...","code":"PSK_REQUIRED"}
//	      （节点设了 PEERDRIVE_PSK 才要；未设 = 开放模式，见 pskAuthFrame）
//
// 三条不能改的约束（破坏了不会报错，只会静默错位）：
//  1. data 头是**文本帧**、数据块是**二进制帧**。raw 序列化下 string 走 PPID 51、
//     ArrayBuffer 走 PPID 53，对端才能按类型区分；把数据块发成 JSON 数组之类
//     会在对端被当成控制帧解析
//  2. data 头与其数据块必须**连续**。接收端用的是"连接级 expect"——把二进制块
//     挂到**最近一个** data 头所属的请求上，而不是在块里带 reqId。所以不能把
//     两个请求的块交错发送（Go 侧 SendFrame 靠连接级 sendMu 保证原子性）
//  3. 字段名逐字对齐。改 `reqId` → `req_id` 不会报错，只会让对端路由不到、
//     请求一直挂到超时

export const PROTOCOL_VERSION = 1

// MAX_FILE_BYTES 与 Go 侧 maxPeerFetchSize 一致（8GB）：对端声明超过这个数
// 直接拒绝，防"恶意对端声明 1<<62 再持续发块"把接收方撑爆。
export const MAX_FILE_BYTES = 8 * 1024 * 1024 * 1024

// DEFAULT_MAX_BUFFER_BYTES 浏览器侧的内存闸。
// 与 8GB 的协议上限分开：协议上限是"服务端允许传多大"，这里是"本页面愿意
// 在内存里攒多大"。消费端 fetch() 是整体驻留内存的，一个 2GB 的文件在手机上
// 会直接崩标签页——默认 256MB，超了报 TOO_LARGE 并提示改用流式。
export const DEFAULT_MAX_BUFFER_BYTES = 256 * 1024 * 1024

// isValidHash 必须与 Go 侧 hashutil.IsStrictSHA256 同语义：**仅** 64 位小写 hex。
// 为什么在本地就拦：大写 hex 在 Go 侧会被判 invalid hash 回 err 帧，错误信息
// 出现在几百毫秒后的一次网络往返之后，排查时容易误判成"对端没这个文件"。
const HASH_RE = /^[0-9a-f]{64}$/

export function isValidHash(hash) {
  return typeof hash === 'string' && HASH_RE.test(hash)
}

// nextReqId 生成请求 ID。Go 侧用 UUID v4；这里用"时间戳+随机"也能满足唯一性
// 要求（reqId 只用于单条连接内的响应路由，不跨连接、不持久化）。
export function nextReqId() {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

// reqFrame 构造文件拉取请求帧（出站角色发起）。
// size 用 -1 表示"读到文件末尾"，与 Go 侧语义一致（负数即不限量）。
export function reqFrame(hash, { offset = 0, size = -1, reqId } = {}) {
  return JSON.stringify({ type: 'req', hash, offset, size, reqId, v: PROTOCOL_VERSION })
}

// shareFrame 构造共享清单查询帧（与 list 帧不同：list 是本地管理索引全量，
// 只对可信对端开放；share 是运营者**显式声明**的对外共享范围）。
export function shareFrame(reqId) {
  return JSON.stringify({ type: 'share', reqId, v: PROTOCOL_VERSION })
}

// pskAuthFrame 构造预共享密钥帧（PSK 门禁，见 doc/NETDISK.md 的「PSK 门禁」）。
//
// 语义：节点可以设一个预共享密钥（PEERDRIVE_PSK），设了之后对端必须在**连接
// 上的第一帧**出示同样的密钥，否则所有请求都回 err（code=PSK_REQUIRED）。
// 没设密钥的节点是开放模式，不发这帧也照常服务（向后兼容）。
//
// 两个实现约定（与 Go 侧 back/internal/transport/psk.go 对齐）：
//  1. **连接建立后立刻发，且必须是第一帧**。DataChannel 保序，服务端按序处理
//     必然先看到 auth 再看到业务帧——所以本包**不等** psk-ok 就发业务请求，
//     不给每次连接多加一个 RTT。
//  2. **明文传密钥**。DataChannel 是强制 DTLS 的，密钥不会在链路上裸奔；
//     而服务端本来就要存明文才能比对。要防的是"陌生人连上来"，不是窃听。
export function pskAuthFrame(psk) {
  return JSON.stringify({ type: 'psk-auth', psk: String(psk ?? ''), v: PROTOCOL_VERSION })
}

// PSK_REQUIRED 是 Go 侧门禁在 err 帧里带的机器可读错误码。
// UI 按它提示"请填密钥"，**不要**去匹配 err 的 msg 文案（文案会改）。
export const PSK_REQUIRED = 'PSK_REQUIRED'

// UPLOAD_CHUNK 上传分片大小，必须与 Go 侧 uploadChunkSize（64KB）一致：
// 服务端按这个粒度维护到位位图，粒度不一致会被判成非法偏移。
export const UPLOAD_CHUNK = 64 * 1024

// uploadFrame 构造上传头帧，请求服务端准备接收 offset 处的一个分片。
//
// 协议（与 Go 侧 inbound.go 的 serveUploadBegin 对齐）：每次只授权一个分片，
// 所以上传是「发头 → 等 meta → 发二进制 → 等 ack/uploaded」的循环。
// 看起来啰嗦，但这是服务端流控的方式：它决定每次收多少，调用方照做即可。
export function uploadFrame(reqId, name, size, offset = 0) {
  return JSON.stringify({
    type: 'upload', name, size, offset, reqId, v: PROTOCOL_VERSION,
  })
}

// pullFrame 构造「网络入库」请求帧：把 URL 交给节点，由它下载并登记。
//
// 与 upload 的分工：upload 是"我有内容推给你"，pull 是"我只有地址，你去取"。
// 这是节点侧唯一一个由外部指定目标地址的动词，因此带 SSRF 防护（只走公网
// http/https、大小上限），并且始终受 PSK 门禁约束（back/internal/transport/pull.go）。
export function pullFrame(reqId, url, name = '') {
  return JSON.stringify({ type: 'pull', url, name, reqId, v: PROTOCOL_VERSION })
}

// parseFrame 解析文本帧。返回 null 表示"不是本协议的控制帧"（非 JSON、
// 或缺 type）——调用方应当忽略而不是报错：同一条连接上可能有别的用途的帧。
export function parseFrame(text) {
  if (typeof text !== 'string') return null
  try {
    const o = JSON.parse(text)
    if (o && typeof o === 'object' && typeof o.type === 'string') return o
  } catch {
    /* 非 JSON：不是本协议帧 */
  }
  return null
}

// isBinaryFrame 判断收到的数据是不是数据块。
// raw 序列化下浏览器端收到的是 ArrayBuffer（或 Blob，视 binaryType 而定），
// Node 端可能是 Uint8Array/DataView——全部按二进制处理，字符串则是文本帧。
export function isBinaryFrame(data) {
  if (typeof data === 'string') return false
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return true
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) return true
  if (typeof Blob !== 'undefined' && data instanceof Blob) return true
  return false
}

// toUint8Array 把任意二进制帧规整为 Uint8Array（要保留 byteOffset/byteLength，
// 直接 new Uint8Array(view.buffer) 会把整个底层 buffer 带进来）。
export function toUint8Array(data) {
  if (data instanceof Uint8Array) return data
  if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) return new Uint8Array(data)
  if (typeof ArrayBuffer !== 'undefined' && ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
  }
  throw new TypeError('peerdrive-client: unsupported binary frame type')
}

// concatChunks 按已知总长拼块。传 total 可以一次性分配（避免逐块扩容复制），
// 但 total 未定时（对端 meta 里是 -1）必须走"先收集后合并"。
export function concatChunks(chunks, total = -1) {
  if (chunks.length === 1) return chunks[0]
  let len = 0
  for (const c of chunks) len += c.byteLength
  const n = total >= 0 ? total : len
  const out = new Uint8Array(n)
  let off = 0
  for (const c of chunks) {
    if (off + c.byteLength > n) break // 对端多发了：按声明长度截断
    out.set(c, off)
    off += c.byteLength
  }
  return off === n ? out : out.subarray(0, off)
}

// sha256Hex 计算内容的 sha256 十六进制摘要（内容寻址校验用）。
// 实现放在 sha256.js：一次性路径优先 WebCrypto，流式路径用纯 JS 增量实现——
// 需要边收边算的调用方（流式拉取）直接用 Sha256 类。
export { Sha256, sha256Hex } from './sha256.js'

// guessMime 按文件名猜 MIME（meta 帧不带 mime，浏览器 Blob 需要它才能正确
// 预览/播放）。猜错只影响预览体验，不影响内容正确性。
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

// baseName 从共享清单的 path 取文件名（对端给的是相对路径，可能带目录）。
export function baseName(p) {
  const s = String(p || '')
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'))
  return i >= 0 ? s.slice(i + 1) : s
}
