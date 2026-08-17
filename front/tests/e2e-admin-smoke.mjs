// E2E 冒烟：连接本地 WS /ws/peer，走 admin verb 验证管理面全链路
// （对应前端 ws.js admin/upload + 后端 admin.go 真实 gin 转发）。
// Node 22 原生 WebSocket；二进制用 ArrayBuffer（binaryType 设为 'arraybuffer'）。
const WS_URL = 'ws://localhost:3000/ws/peer'
const ws = new WebSocket(WS_URL)
ws.binaryType = 'arraybuffer'
const pending = new Map()
let seq = 0

function send(type, payload = {}) {
  const reqId = 'e2e-' + (++seq)
  return new Promise((resolve, reject) => {
    pending.set(reqId, { resolve, reject })
    ws.send(JSON.stringify({ type, reqId, ...payload }))
    setTimeout(() => {
      if (pending.has(reqId)) { pending.delete(reqId); reject(new Error('timeout: ' + type + ' ' + reqId)) }
    }, 15000)
  })
}

const checks = []
function ok(name, pass, detail = '') {
  checks.push({ name, pass, detail })
  console.log(`${pass ? '[PASS]' : '[FAIL]'} ${name}${detail ? ' — ' + detail : ''}`)
}

let binExpect = null
ws.addEventListener('message', (ev) => {
  if (typeof ev.data !== 'string') {
    // 二进制帧
    if (binExpect) {
      const chunk = Buffer.from(ev.data)
      binExpect.chunks.push(chunk)
      const total = Buffer.concat(binExpect.chunks)
      if (total.length >= binExpect.size) {
        const { resolve } = pending.get(binExpect.reqId)
        pending.delete(binExpect.reqId)
        resolve({ status: binExpect.status, body: total })
        binExpect = null
      }
    }
    return
  }
  const msg = JSON.parse(ev.data)
  if (msg.type === 'admin-resp') {
    const p = pending.get(msg.reqId)
    if (!p) return
    pending.delete(msg.reqId)
    if (msg.status >= 400) p.reject(new Error('admin-resp ' + msg.status + ': ' + JSON.stringify(msg.body)))
    else p.resolve({ status: msg.status, body: msg.body })
  } else if (msg.type === 'admin-bin') {
    binExpect = { reqId: msg.reqId, status: msg.status, size: msg.size || 0, chunks: [] }
    if (binExpect.size === 0) {
      const p = pending.get(msg.reqId)
      pending.delete(msg.reqId)
      p.resolve({ status: msg.status, body: Buffer.alloc(0) })
      binExpect = null
    }
  } else if (msg.type === 'err') {
    const p = pending.get(msg.reqId)
    if (p) { pending.delete(msg.reqId); p.reject(new Error('err: ' + msg.msg)) }
  }
})

ws.addEventListener('open', async () => {
  try {
    // 1. JSON admin: ping（health 检查，前端 BTController 用）
    const ping = await send('admin', { method: 'GET', path: '/ping' })
    ok('admin GET /ping', ping.status === 200 && ping.body === 'pong', JSON.stringify(ping.body))

    // 2. 二进制上传：声明 + 二进制帧 → 内容寻址存储（前端 uploadFile 路径）
    // 注意：声明帧不能 await（响应要等二进制块收齐才回），先发声明再发块
    const content = Buffer.from('hello from ws admin upload ' + Date.now())
    const upload = await (() => {
      const reqId = 'e2e-upload'
      return new Promise((resolve, reject) => {
        pending.set(reqId, { resolve, reject })
        ws.send(JSON.stringify({
          type: 'admin', reqId,
          method: 'POST', path: '/files/upload', binary: true,
          filename: 'e2e-admin.txt', size: content.length,
        }))
        ws.send(content)
      })
    })()
    ok('admin binary upload', upload.status === 201 && upload.body.hash, `hash=${upload.body.hash}`)
    const hash = upload.body.hash

    // 3. 下载文件（admin-bin 二进制响应，前端 downloadTorrentFile 同路径）
    const dl = await send('admin', { method: 'GET', path: `/download/${hash}` })
    ok('admin GET /download/:hash → admin-bin', dl.status === 200 && dl.body.equals(content), `size=${dl.body.length}`)

    // 4. 匿名集合管理（JSON admin，前端 AnonCreator 路径）
    const coll = await send('admin', { method: 'POST', path: '/anon/collections', body: { name: 'e2e-coll' } })
    ok('admin POST /anon/collections', coll.status === 201 || coll.status === 200, JSON.stringify(coll.body))
    const hash2 = coll.body.hash
    if (hash2) {
      const get = await send('admin', { method: 'GET', path: `/anon/collections/${hash2}` })
      ok('admin GET /anon/collections/:hash', get.status === 200, JSON.stringify(get.body))
    }

    // 5. 列表文件（JSON admin）
    const files = await send('admin', { method: 'GET', path: '/files' })
    ok('admin GET /files', files.status === 200 && Array.isArray(files.body), JSON.stringify(files.body))

    // 6. 未知名路由 → admin-resp 404 透传（send 对 >=400 reject，这里直接捕获验证）
    try {
      await send('admin', { method: 'GET', path: '/no-such-route' })
      ok('admin unknown route → 404', false, 'expected reject')
    } catch (e) {
      ok('admin unknown route → 404', e.message.includes('404'), e.message)
    }
  } catch (e) {
    ok('(exception)', false, e.message)
  } finally {
    const failed = checks.filter(c => !c.pass).length
    console.log(`\n${checks.length - failed}/${checks.length} passed`)
    ws.close()
    process.exit(failed ? 1 : 0)
  }
})