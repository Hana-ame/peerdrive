// ws.js — 本地 WS 会话客户端（/ws/peer），前端全面迁移后所有后端通信的通道。
//
// 为什么存在：帧协议（doc/REFACTOR.md §4）覆盖文件数据面（req/meta/data/done/err
// 拉取 + create/upload/list/info/delete/sync 索引 + fwd-* 转发），但不覆盖
// 集合/认证/BT/IPFS/任务等管理面。后端在本地 WS 会话上加 admin verb
// （back/internal/transport/admin.go），内部转发到 gin engine 复用全部
// HTTP controller——浏览器经此通道完成全部管理操作，不再直接 fetch HTTP。
//
// 帧协议（与后端约定，勿改）：
//   管理请求: {"type":"admin","method":"GET|POST|DELETE","path":"/files?x=1",
//             "body":<JSON 对象|null>,"token":"<可选>","reqId":"<uuid>"}
//             {"type":"admin",...,"binary":true,"filename":"a.bin","size":N,"reqId"}
//               → 声明后紧跟二进制帧（数据块），收齐后 multipart 转发 /files/upload
//   管理响应: {"type":"admin-resp","status":200,"body":<原始 JSON>,"reqId"}
//             {"type":"admin-bin","status":200,"size":N,"reqId"} + 二进制帧（文件流）
//             {"type":"err","msg":"...","reqId"}
//   文件下载（走 req verb，与 DataChannel 同一套）:
//             {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId"}
//             ← {"type":"meta",...} {"type":"data","size":N,"reqId"}+二进制块 ...
//               {"type":"done",...} / {"type":"err","msg","reqId"}
//
// 关键约束（协议正确性依赖，勿破坏）：
//   1. data/admin-bin 头与二进制块原子连续（后端 SendFrame 保证）——前端
//      「最近二进制声明头」单槽路由（binaryExpect），与后端连接级 expect
//      状态机语义一致：一个二进制帧必属于最近的 data/admin-bin 头
//   2. reqId 路由：管理响应/下载响应按 reqId 配对；admin 响应 status>=400
//      → reject Error(err.status/err.data)，与 api.js request() fetch 版行为
//      一致（409 冲突清单等结构化错误体可用）
//   3. 连接断开 → reject 全部 pending + 置空重连（下一请求前自动连接）
//   4. 管理面只走本地 WS；peerjs/WebRTC 不实现管理 verb（防权限面漏洞，
//      用户决策；后端 serveAdmin 按会话 ID 拒绝非本地连接）

// apiBase → ws url（http→ws / https→wss），与 api.js getApiBase() 同源。
function wsUrl(base) {
  return (base.startsWith('https') ? 'wss://' : 'ws://') + base.replace(/^https?:\/\//, '')
}

let sock = null
let reqSeq = 0
const pending = new Map() // reqId → 请求状态（resolve/reject + 下载收集态）

// BIN_CHUNK 上传二进制分块大小：与后端协议一致（uploadChunkSize / inbound
// chunkSize 均 64KB，见 back/internal/transport/{file_index,inbound}.go）。
// 发现背景：FileReader 回退路径（旧浏览器无 stream() API）引用未定义常量
// → ReferenceError，上传直接失败（代码审阅 2026-08-18 发现；现代浏览器走
// Streams API 分支所以线上未触发）。WS 读限 3*64KB 之上，64KB 块安全。
const BIN_CHUNK = 64 * 1024

// binaryExpect 「最近二进制声明头」单槽：一个二进制帧必属于最近声明的
// admin-bin 或 data 头（后端 SendFrame 原子连续保证，勿改）。
let binaryExpect = null

// 与 api.js 同步 token（localStorage key 见 api.js AUTH_TOKEN_KEY）
function readToken() {
  const frag = localStorage.getItem('peerdrive_auth_token')
  if (frag) return frag
  if (localStorage.getItem('peerdrive_auth_header_enabled') === 'true') {
    return localStorage.getItem('peerdrive_auth_key') || ''
  }
  return ''
}

function getWsBase() {
  return localStorage.getItem('peerdrive_api_base') || 'https://wsl-3000.moonchan.xyz'
}

// connect 建立 WS 连接（幂等：已有连接直接返回；已初始化 handlers 不重复挂）。
// 单连接复用：浏览器与本地节点只有一条会话，所有请求并发经 reqId 路由。
function connect() {
  if (!sock) {
    sock = new WebSocket(wsUrl(getWsBase()) + '/ws/peer')
  }
  // handlers 幂等挂载：mock/已有 sock（测试注入）也能走同一初始化路径
  if (sock._wsHandlers) return
  sock._wsHandlers = true

  sock.onmessage = (ev) => {
    if (typeof ev.data === 'string') {
      handleText(ev.data)
    } else {
      handleBinary(ev.data)
    }
  }
  sock.onclose = () => {
    // 连接断开：reject 所有 pending（调用方按网络错误处理），sock 置空等重连
    for (const [, p] of pending) p.reject(new Error('ws: connection closed'))
    pending.clear()
    binaryExpect = null
    sock = null
  }
  sock.onerror = () => {
    try { sock.close() } catch {}
  }
}

function nextReqId() {
  reqSeq += 1
  return 'w' + Date.now().toString(36) + '-' + reqSeq.toString(36)
}

// handleText 文本帧分发：admin 响应按 reqId 路由；req 拉取响应（meta/data 头/
// done/err）路由到对应下载请求。
function handleText(text) {
  let msg
  try {
    msg = JSON.parse(text)
  } catch {
    return
  }
  if (!msg || !msg.type) return

  switch (msg.type) {
    case 'admin-resp': {
      const p = pending.get(msg.reqId)
      if (!p) return
      pending.delete(msg.reqId)
      if (msg.status >= 400) {
        const err = new Error((msg.body && (msg.body.error || msg.body.message)) || `HTTP ${msg.status}`)
        err.status = msg.status
        err.data = msg.body
        p.reject(err)
      } else {
        p.resolve(msg.body)
      }
      return
    }
    case 'admin-bin': {
      // 二进制文件流响应头：声明「下一二进制帧归本次管理下载」
      const p = pending.get(msg.reqId)
      if (!p) return
      binaryExpect = { type: 'admin', reqId: msg.reqId, size: msg.size || 0, got: 0, chunks: [] }
      if (binaryExpect.size === 0) {
        finishBinaryExpect(p, binaryExpect)
      }
      return
    }
    case 'data': {
      // 下载数据块头：声明「下一二进制帧归本次下载，大小 size」
      // （与服务端连接级 expect 语义一致；data 头+块原子连续）
      const p = pending.get(msg.reqId)
      if (!p || p.kind !== 'download') return
      binaryExpect = { type: 'download', reqId: msg.reqId, size: msg.size || 0, got: 0, chunks: [] }
      if (binaryExpect.size === 0) {
        // 空块（罕见）：直接清期待，等下一帧
        binaryExpect = null
      }
      return
    }
    case 'meta':
    case 'done': {
      const p = pending.get(msg.reqId)
      if (!p || p.kind !== 'download') return
      if (msg.type === 'done') {
        // done 帧：传输完成，收集齐的数据已在上一个 data 头声明 size
        pending.delete(msg.reqId)
        binaryExpect = null
        p.resolve(assemble(p))
      }
      return
    }
    case 'err': {
      const p = pending.get(msg.reqId)
      if (!p) return
      pending.delete(msg.reqId)
      p.reject(new Error(msg.msg || 'peer fetch failed'))
      return
    }
    default:
      return
  }
}

// handleBinary 二进制帧：归 binaryExpect（最近 data/admin-bin 头的归属）。
// admin-bin：收齐 size 即 resolve（响应体单块）。
// download：data 块收齐只清期待（等 done 帧才 resolve——完整性由 done 保证）。
function handleBinary(data) {
  if (!binaryExpect) return
  const e = binaryExpect
  e.got += data.byteLength
  e.chunks.push(data)
  if (e.size !== undefined && e.got >= e.size) {
    const p = pending.get(e.reqId)
    binaryExpect = null
    if (!p) return
    if (e.type === 'admin') {
      finishBinaryExpect(p, e)
    } else {
      // download：块收齐，等待下一个 data 头或 done 帧
      p.chunks.push(...e.chunks)
    }
  }
}

// finishBinaryExpect 收齐二进制响应：组装 Uint8Array 并 resolve。
function finishBinaryExpect(p, e) {
  const arr = new Uint8Array(e.got)
  let off = 0
  for (const c of e.chunks) {
    arr.set(new Uint8Array(c), off)
    off += c.byteLength
  }
  pending.delete(e.reqId)
  p.resolve(arr)
}

// assemble 下载收集态 → Uint8Array。
function assemble(p) {
  const arr = new Uint8Array(p.chunks.length ? p.chunks.reduce((n, c) => n + c.byteLength, 0) : 0)
  let off = 0
  for (const c of p.chunks) {
    arr.set(new Uint8Array(c), off)
    off += c.byteLength
  }
  return arr
}

// admin 通用管理请求（JSON）→ 响应 JSON 对象。
export function admin(method, path, body = null) {
  connect()
  if (!sock || sock.readyState !== WebSocket.OPEN) {
    return Promise.reject(new Error('ws: not connected'))
  }
  const reqId = nextReqId()
  const frame = { type: 'admin', method, path, body, token: readToken(), reqId }
  return new Promise((resolve, reject) => {
    pending.set(reqId, { resolve, reject })
    sock.send(JSON.stringify(frame))
  })
}

// upload 分片上传：admin binary 声明帧 + 连续二进制块（复用同一 WS 连接）。
// field：multipart 字段名（默认 "file"）；path：上传端点（默认 /files/upload；
// BT torrent 上传用 /bt/torrent + field "torrent"）。
export function upload(file, fileName, field = 'file', path = '/files/upload') {
  connect()
  const reqId = nextReqId()
  const name = fileName || (file && file.name) || 'file'
  return new Promise((resolve, reject) => {
    pending.set(reqId, { resolve, reject })
    const decl = {
      type: 'admin', method: 'POST', path,
      binary: true, filename: name, field, size: file.size,
      token: readToken(), reqId,
    }
    sock.send(JSON.stringify(decl))
    pumpBinary(file, reqId)
  })
}

// pumpBinary 流式发送文件二进制块（Streams API 优先，FileReader 回退）。
function pumpBinary(file, reqId) {
  const send = (buf) => {
    if (sock.readyState !== WebSocket.OPEN) throw new Error('ws: closed during upload')
    sock.send(buf)
  }
  const fail = (err) => {
    const p = pending.get(reqId)
    if (p) {
      pending.delete(reqId)
      p.reject(err)
    }
  }
  if (file.stream && typeof file.stream === 'function') {
    const reader = file.stream().getReader()
    ;(async () => {
      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          send(value)
        }
      } catch (err) {
        fail(err)
      }
    })()
    return
  }
  // FileReader 回退：整体切片逐块发送
  const CH = BIN_CHUNK
  let off = 0
  const next = () => {
    if (off >= file.size) return
    const slice = file.slice(off, off + CH)
    off += CH
    const fr = new FileReader()
    fr.onload = () => {
      try {
        send(fr.result)
        next()
      } catch (err) {
        fail(err)
      }
    }
    fr.onerror = () => fail(fr.error)
    fr.readAsArrayBuffer(slice)
  }
  next()
}

// download 经 req verb 拉取文件（sha256 内容寻址），返回 Uint8Array。
// 服务端按 64KB 块发 data 头+二进制帧；前端按 data 头声明 size 收集。
export function download(hash, offset = 0, size = -1) {
  connect()
  if (!sock || sock.readyState !== WebSocket.OPEN) {
    return Promise.reject(new Error('ws: not connected'))
  }
  const reqId = nextReqId()
  const p = { kind: 'download', chunks: [], total: 0 }
  return new Promise((resolve, reject) => {
    pending.set(reqId, { ...p, resolve, reject })
    sock.send(JSON.stringify({ type: 'req', hash, offset, size, reqId }))
  })
}

// downloadToFile 下载并触发浏览器保存（<a download> 模拟）。
export async function downloadToFile(hash, filename) {
  const data = await download(hash)
  const blob = new Blob([data])
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename || hash
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 5000)
}

// ws.js 专用测试钩子（vitest 用；生产不导出）
// __test 钩子仅供单测：注入 mock socket / 强制重连 / 清理残留 pending
export const __test = {
  connect,
  readToken,
  getWsBase,
  handleText,
  handleBinary,
  pending,
  _setSock: (s) => { sock = s },
  _reset: () => { sock = null; pending.clear(); binaryExpect = null },
}
