import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// E2E 冒烟：连接本地 WS /ws/peer，走 admin verb 验证管理面全链路
// （对应前端 ws.js admin/upload + 后端 admin.go 真实 gin 转发）。
// Node 22 原生 WebSocket；二进制用 ArrayBuffer（binaryType 设为 'arraybuffer'）。
// 默认打本机默认端口（3000）；要打别的端口用 E2E_WS_URL 覆盖。
const WS_URL = process.env.E2E_WS_URL || 'ws://localhost:3000/ws/peer'
console.log('WS_URL =', WS_URL)
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

    // 6-8. 共享范围（doc/NETDISK.md M2.6）：读 → 勾一个文件 → 复核 → 拒卷根目录。
    //     「持有」不等于「共享」：上传进来只是自己能看到，对外提供要另行勾选。
    const scope = await send('admin', { method: 'GET', path: '/peerjs/share' })
    ok('admin GET /peerjs/share', scope.status === 200 && Array.isArray(scope.body.files) && Array.isArray(scope.body.selected), JSON.stringify(Object.keys(scope.body || {})))

    // 条目形如 {id, level}；selected 同时给一份纯 hash 列表方便前端渲染
    const pick = await send('admin', { method: 'POST', path: '/peerjs/share/files', body: { hashes: [hash], shared: true, level: 'public' } })
    const ids = (pick.body.selected || pick.body.files || []).map(it => (typeof it === 'string' ? it : it?.id))
    ok('admin POST /peerjs/share/files', pick.status === 200 && ids.includes(hash), JSON.stringify(pick.body.selected))

    const scope2 = await send('admin', { method: 'GET', path: '/peerjs/share' })
    const ids2 = (scope2.body.selected || []).map(it => (typeof it === 'string' ? it : it?.id))
    ok('勾选后进入 selected', ids2.includes(hash), JSON.stringify(scope2.body.selected))
    // 候选清单有 1000 条上限（索引很大时新文件可能不在那一页），但只要在页里
    // 就必须标成已共享——勾选本身由 selected 兜底，不受分页影响。
    const row = (scope2.body.files || []).find(f => f.hash === hash)
    ok('候选清单里标记为已共享', !row || row.shared === true, JSON.stringify(row))

    // 9-10. 共享级别（doc/NETDISK.md §12.6）：三档可写可读；拼错的级别整批拒绝。
    const lvl = await send('admin', { method: 'POST', path: '/peerjs/share/files', body: { hashes: [hash], shared: true, level: 'private' } })
    const lv = ((lvl.body.selected || []).map(it => (typeof it === 'string' ? { id: it } : it))).find(it => it?.id === hash)
    ok('级别可设为 private', lvl.status === 200 && lv?.level === 'private', JSON.stringify(lv))

    ok('GET /peerjs/share 回三档级别取值', Array.isArray(scope2.body.levels) && scope2.body.levels.join(',') === 'public,unlisted,private', JSON.stringify(scope2.body.levels))

    const fr = await send('admin', { method: 'PUT', path: '/peerjs/share', body: { friends: ['e2e-buddy'] } })
    ok('好友名单可写', fr.status === 200 && (fr.body.friends || []).includes('e2e-buddy'), JSON.stringify(fr.body.friends))

    try {
      await send('admin', { method: 'POST', path: '/peerjs/share/files', body: { hashes: [hash], shared: true, level: 'pubilc' } })
      ok('拼错的级别被拒（不兜成 public）', false, 'expected reject')
    } catch (e) {
      ok('拼错的级别被拒（不兜成 public）', e.message.includes('400'), e.message)
    }

    try {
      await send('admin', { method: 'PUT', path: '/peerjs/share', body: { dirs: ['/'] } })
      ok('卷根目录作共享目录被拒', false, 'expected reject')
    } catch (e) {
      ok('卷根目录作共享目录被拒', e.message.includes('400'), e.message)
    }

    // 8b. 共享目录（管理台「共享目录」那一条的背后）：加一个真实目录 → GET 读回
    //     → 清空恢复。PUT 的 dirs 是**整体替换**，这条只验往返，前端"加第二个要
    //     带上第一个"的规则由 netdisk.test.jsx 断言。
    const dirPath = path.join(os.tmpdir(), 'peerdrive-e2e-share-' + Date.now())
    fs.mkdirSync(dirPath, { recursive: true })
    const dirsOf = (body) => (body || []).map(it => (typeof it === 'string' ? { id: it, level: '' } : it))
    try {
      const put = await send('admin', { method: 'PUT', path: '/peerjs/share', body: { dirs: [{ id: dirPath, level: 'unlisted' }] } })
      const got = dirsOf(put.body.dirs).find(it => it?.id === dirPath)
      ok('共享目录可写且带级别', put.status === 200 && got?.level === 'unlisted', JSON.stringify(put.body.dirs))
      const back = await send('admin', { method: 'GET', path: '/peerjs/share' })
      ok('共享目录可读回', dirsOf(back.body.dirs).some(d => d?.id === dirPath), JSON.stringify(back.body.dirs))
      const clr = await send('admin', { method: 'PUT', path: '/peerjs/share', body: { dirs: [] } })
      ok('共享目录可清空（dirs 整体替换）', clr.status === 200 && dirsOf(clr.body.dirs).length === 0, JSON.stringify(clr.body.dirs))
      fs.rmSync(dirPath, { recursive: true, force: true })
    } catch (e) {
      ok('共享目录往返', false, e.message)
    }

    // 9. 未知名路由 → admin-resp 404 透传（send 对 >=400 reject，这里直接捕获验证）
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