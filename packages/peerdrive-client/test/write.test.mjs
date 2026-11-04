// write.test.mjs — 写方向的能力契约：本地入库 put、网络入库 pull、自动发现 discoverNodes。
//
// 为什么单开一个文件：包此前只有读能力（fetch/stream/shares），写路径一旦出问题
// 症状都是「静默」—— Put 卡住不报错、pull 把 404 页面当内容、发现失败被当成
// "信令挂了"。这些分辨不出来就修不了，所以三条路都按「失败该怎么报」钉死。

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'

import { PeerDriveClient, PeerDriveError, ERR, discoverNodes } from '../src/client.js'
import { UPLOAD_CHUNK } from '../src/protocol.js'
import { expectHash, makeBytes } from './fake-connection.mjs'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/**
 * UploadNode 扮演会讲 upload/pull 两个动词的节点。
 *
 * 之所以不直接复用 FakeConnection：那个是**读**侧的替身（应答 req/share），
 * 而 upload 是反方向的协议——发头、等 meta、收二进制、回 ack/uploaded，
 * 帧序与 Failure 注入点完全不同，硬塞进一个替身会让两边都难读。
 */
class UploadNode {
  constructor(opts = {}) {
    this.peer = opts.peer || 'node-writer'
    this.open = opts.open !== false
    this.sent = [] // 收到的所有帧/块
    this.heads = [] // 收到的 upload 头
    this.buf = []
    this.received = 0
    this.size = 0
    this._h = new Map()
    // 故障注入：
    //   silent   = 收到什么都不回（测超时）
    //   errCode  = 首帧就回 err（测门禁/服务端拒绝）
    //   weird    = 回一个未知帧类型
    //   immediateUploaded = 收头即回 uploaded（空文件语义）
    this.silent = opts.silent === true
    this.errCode = opts.errCode || ''
    this.errMsg = opts.errMsg || '被拒'
    this.weird = opts.weird === true
    this.immediateUploaded = opts.immediateUploaded === true
    // stray = 在正式应答之前先丢一个**不带 reqId** 的未知帧过来（前向兼容场景）
    this.stray = opts.stray === true
    // firstMetaOffset = 首个 meta 里回报的续写偏移（模拟"这份内容之前传过一半"）
    this.firstMetaOffset = opts.firstMetaOffset
    // base = 服务端称自己已经持有的前 base 字节（与 firstMetaOffset 配套使用）
    this.base = opts.base || 0
  }

  on(type, cb) {
    if (!this._h.has(type)) this._h.set(type, [])
    this._h.get(type).push(cb)
    return this
  }

  emit(type, ...args) {
    for (const cb of [...(this._h.get(type) || [])]) cb(...args)
  }

  reply(obj) {
    setTimeout(() => this.emit('data', JSON.stringify(obj)), 0)
  }

  close() {
    if (this.closed) return
    this.closed = true
    this.emit('close')
  }

  send(data) {
    if (this.closed) throw new Error('node closed')
    this.sent.push(data)
    if (typeof data !== 'string') {
      // 二进制分片：记账，按服务端语义回 ack 或 uploaded
      const bytes = new Uint8Array(data)
      this.buf.push(bytes)
      this.received += bytes.byteLength
      if (this.silent) return
      if (this.received + this.base >= this.size) {
        this.reply({ type: 'uploaded', hash: this.hash, total: this.size, name: this.name, path: '/files/' + this.name, reqId: this.reqId })
      } else {
        this.reply({ type: 'ack', offset: this.received + this.base, reqId: this.reqId })
      }
      return
    }
    const f = JSON.parse(data)
    if (this.silent) return
    if (f.type === 'upload') {
      this.reqId = f.reqId
      this.size = f.size
      this.name = f.name
      if (this.errCode) return this.reply({ type: 'err', msg: this.errMsg, code: this.errCode, reqId: f.reqId })
      if (this.weird) return this.reply({ type: 'gossip', reqId: f.reqId })
      if (this.immediateUploaded) {
        return this.reply({ type: 'uploaded', hash: this.hash, total: f.size, name: f.name, reqId: f.reqId })
      }
      if (this.stray) this.reply({ type: 'gossip-from-future' })
      this.heads.push(f)
      const replyOffset = this.firstMetaOffset !== undefined ? this.firstMetaOffset : f.offset
      delete this.firstMetaOffset
      this.reply({ type: 'meta', total: f.size, offset: replyOffset, reqId: f.reqId })
      return
    }
    // pull / 其它一次性动词的默认应答由各用例自备
    this.lastVerb = f
  }

  /** collected 把收到的分片按到达顺序拼起来，用于验证「发出去的字节一个不差」。 */
  collected() {
    const total = this.received
    const out = new Uint8Array(total)
    let off = 0
    for (const b of this.buf) {
      out.set(b, off)
      off += b.byteLength
    }
    return out
  }
}

function makeClient(node, opts = {}) {
  return new PeerDriveClient(node, { idleTimeoutMs: 5000, verbTimeoutMs: 1500, ...opts })
}

describe('put/本地入库', () => {
  it('单分片：发给节点的字节与本地 sha256 一致', async () => {
    const bytes = makeBytes(1024, 7)
    const node = new UploadNode()
    node.hash = await expectHash(bytes)
    const client = makeClient(node)

    const progress = []
    const res = await client.put(bytes, { name: 'note.txt', onProgress: (s, t) => progress.push([s, t]) })

    assert.equal(res.hash, node.hash, '返回的 hash 应等于内容 sha256')
    assert.equal(res.size, 1024)
    assert.equal(res.name, 'note.txt')
    // 传过去的必须一个字节不差——内容寻址的库一旦存错，之后所有人拉到的都是错的
    assert.equal(await expectHash(node.collected()), node.hash, '节点收到的字节应与原文一致')
    assert.deepEqual(progress.at(-1), [1024, 1024], '进度回调终点应是 [已发, 总长]')
    assert.equal(node.heads.length, 1, '1024B 只够一个分片')
  })

  it('多分片：offset 逐片推进，累计等于总长', async () => {
    const bytes = makeBytes(UPLOAD_CHUNK * 2 + 123, 9) // 跨 3 个分片
    const node = new UploadNode()
    node.hash = await expectHash(bytes)
    const client = makeClient(node)

    const res = await client.put(bytes, { name: 'big.bin' })

    assert.equal(res.hash, node.hash)
    assert.deepEqual(node.heads.map((h) => h.offset), [0, UPLOAD_CHUNK, UPLOAD_CHUNK * 2],
      'upload 头的 offset 必须逐片对齐 UPLOAD_CHUNK')
    assert.equal(node.received, bytes.byteLength)
    assert.equal(await expectHash(node.collected()), node.hash)
  })

  it('空文件：服务端直接回 uploaded 也要能收尾（不靠 meta）', async () => {
    const node = new UploadNode({ immediateUploaded: true })
    node.hash = await expectHash(new Uint8Array(0))
    const client = makeClient(node)
    const res = await client.put(new Uint8Array(0), { name: 'empty.txt' })
    assert.equal(res.size, 0)
    assert.equal(node.received, 0, '空文件不该有二进制块')
  })

  it('服务端拒绝（PSK 门禁）→ PSK_REQUIRED 而不是超时', async () => {
    const node = new UploadNode({ errCode: 'PSK_REQUIRED', errMsg: 'peerjs: psk: 本节点需要预共享密钥' })
    const client = makeClient(node)
    await assert.rejects(
      client.put(makeBytes(32, 3), { name: 'x' }),
      (e) => e.code === ERR.PSK_REQUIRED,
    )
  })

  it('无声的对端 → TIMEOUT（不要无限等待）', async () => {
    const node = new UploadNode({ silent: true })
    const client = makeClient(node, { verbTimeoutMs: 200 })
    await assert.rejects(client.put(makeBytes(16, 1), { name: 'x' }), (e) => e.code === ERR.TIMEOUT)
  })

  it('未知帧 → PROTOCOL', async () => {
    const node = new UploadNode({ weird: true })
    const client = makeClient(node)
    await assert.rejects(client.put(makeBytes(16, 1), { name: 'x' }), (e) => e.code === ERR.PROTOCOL)
  })

  it('不带 reqId 的未知帧 → 忽略（前向兼容不能被上一个用例连带改坏）', async () => {
    // 上面的 PROTOCOL 规则只适用于"应答了我正在等的请求"的帧；对端发个与本方
    // 无关的广播帧是前向兼容的正常情形，把它当错误会让新旧版本无法互通。
    const bytes = makeBytes(64, 5)
    const node = new UploadNode({ stray: true })
    node.hash = await expectHash(bytes)
    const client = makeClient(node, { verbTimeoutMs: 400 })
    const res = await client.put(bytes, { name: 'note.txt' })
    assert.equal(res.hash, node.hash, '杂帧之后的上传应照常完成')
  })

  it('连接中途关闭 → 在飞的 put 立刻以 CLOSED 失败（而不是等到超时）', async () => {
    const node = new UploadNode({ silent: true })
    const client = makeClient(node, { verbTimeoutMs: 5000 })
    const p = client.put(makeBytes(64, 2), { name: 'x' })
    await sleep(10)
    node.close()
    await assert.rejects(p, (e) => e.code === ERR.CLOSED)
  })

  it('meta 里的 offset 由服务端说了算（断点续传不能自顾自增）', async () => {
    // 服务端回报"我已有前 64KB"时，客户端必须从那个偏移继续写；自己从 0 数过去
    // 会把字节写到错的位置，最终落出一个 hash 对不上的副本——而错误要到别人
    // 拉取时才炸，那时已经分不清是上传写错还是对端坏了。
    const bytes = makeBytes(UPLOAD_CHUNK * 2 + 123, 11)
    const node = new UploadNode({ firstMetaOffset: UPLOAD_CHUNK, base: UPLOAD_CHUNK })
    node.hash = await expectHash(bytes)
    const client = makeClient(node)

    await client.put(bytes, { name: 'resume.bin' })

    assert.equal(node.received, bytes.byteLength - UPLOAD_CHUNK, '只补传服务端缺的那一段')
    assert.equal(await expectHash(node.collected()), await expectHash(bytes.subarray(UPLOAD_CHUNK)))
    assert.deepEqual(node.heads.map((h) => h.offset), [0, UPLOAD_CHUNK * 2],
      '第二个头应从服务端授权的断点继续，而不是从客户端自己的计数继续')
  })

  it('取消：abort 让在飞的这一轮立刻失败（而不是等整轮超时）', async () => {
    const node = new UploadNode({ silent: true })
    const client = makeClient(node, { verbTimeoutMs: 10_000 })
    const ctrl = new AbortController()
    const p = client.put(makeBytes(UPLOAD_CHUNK * 4, 4), { name: 'x', signal: ctrl.signal })
    await sleep(10)
    const started = Date.now()
    ctrl.abort()
    await assert.rejects(p, (e) => e.code === ERR.CANCELLED)
    assert.ok(Date.now() - started < 1000, '取消应当是立刻生效的')
  })

  it('File/Blob/string 类型的输入也能入库', async () => {
    const node = new UploadNode()
    const text = '来自 File 的一条内容'
    node.hash = await expectHash(new TextEncoder().encode(text))
    const client = makeClient(node)
    const res = await client.put({ name: 'upload.txt', arrayBuffer: async () => new TextEncoder().encode(text).buffer })
    assert.equal(res.hash, node.hash)
    assert.equal(res.name, 'upload.txt', '名字应取自输入对象的 name')
  })
})

describe('pull/网络入库', () => {
  it('正常返回 hash/size/name/path', async () => {
    const node = new UploadNode()
    const client = makeClient(node)
    node.onAssert = true
    // 让 pull 的手写应答在收到 Verb 帧后发出
    node.send = ((orig) => function patched(data) {
      orig.call(this, data)
      if (typeof data === 'string') {
        const f = JSON.parse(data)
        if (f.type === 'pull') {
          this.pullFrame = f
          this.reply({ type: 'pulled', hash: 'a'.repeat(64), total: 4242, name: 'remote.zip', path: '/files/remote.zip', reqId: f.reqId })
        }
      }
    })(node.send)

    const res = await client.pull('https://example.com/remote.zip')
    assert.equal(res.hash, 'a'.repeat(64))
    assert.equal(res.size, 4242)
    assert.equal(res.name, 'remote.zip')
    assert.equal(node.pullFrame.url, 'https://example.com/remote.zip')
  })

  it('空 URL 本地就拒（不发请求）', async () => {
    const node = new UploadNode()
    const client = makeClient(node)
    await assert.rejects(client.pull('   '), (e) => e instanceof PeerDriveError)
    assert.equal(node.lastVerb, undefined, '不该有帧发出')
  })

  it('对端不应答 → TIMEOUT', async () => {
    const node = new UploadNode({ silent: true })
    const client = makeClient(node, { verbTimeoutMs: 200 })
    await assert.rejects(client.pull('https://example.com/x'), (e) => e.code === ERR.TIMEOUT)
  })

  it('对端回 err（SSRF 拦截等）→ 原话透传给调用方', async () => {
    const node = new UploadNode()
    const client = makeClient(node)
    node.send = ((orig) => function patched(data) {
      orig.call(this, data)
      if (typeof data === 'string') {
        const f = JSON.parse(data)
        if (f.type === 'pull') this.reply({ type: 'err', msg: 'pull: 禁止拉向内网/本机地址（169.254.169.254）', reqId: f.reqId })
      }
    })(node.send)

    await assert.rejects(
      client.pull('http://169.254.169.254/latest/meta-data/'),
      (e) => e.code === ERR.PEER && /169\.254/.test(e.message),
    )
  })

  it('应答不是 pulled → PROTOCOL（版本不匹配时不能当成功）', async () => {
    const node = new UploadNode()
    const client = makeClient(node)
    node.send = ((orig) => function patched(data) {
      orig.call(this, data)
      if (typeof data === 'string') {
        const f = JSON.parse(data)
        if (f.type === 'pull') this.reply({ type: 'unknown-thing', reqId: f.reqId })
      }
    })(node.send)
    await assert.rejects(client.pull('https://example.com/x'), (e) => e.code === ERR.PROTOCOL)
  })
})

describe('discoverNodes/自动搜索在线节点', () => {
  let real
  const stubFetch = (impl) => { real = globalThis.fetch; globalThis.fetch = impl }
  const restore = () => { globalThis.fetch = real }

  it('解析 nodes 列表，并按 secure 推导 https/wss 端口', async () => {
    let seenUrl = ''
    stubFetch(async (url) => {
      seenUrl = String(url)
      return { ok: true, json: async () => ({ nodes: [{ peerId: 'node-a', lastSeen: 1 }, { peerId: 'node-b', lastSeen: 2 }], links: [] }) }
    })
    try {
      const nodes = await discoverNodes({ host: 'sig.example', secure: true })
      assert.equal(seenUrl, 'https://sig.example:443/discover/nodes')
      assert.deepEqual(nodes.map((n) => n.peerId), ['node-a', 'node-b'])
    } finally { restore() }
  })

  it('coll 过滤拼进查询串', async () => {
    let seenUrl = ''
    stubFetch(async (url) => { seenUrl = String(url); return { ok: true, json: async () => ({ nodes: [] }) } })
    try {
      await discoverNodes({ host: 'sig.example', port: 9100, secure: false }, { coll: 'abc def' })
      assert.equal(seenUrl, 'http://sig.example:9100/discover/nodes?coll=abc%20def')
    } finally { restore() }
  })

  it('跨域失败：错误信息必须点破是 CORS，而不是含糊的失败', async () => {
    // 浏览器的真实表现就是 TypeError: Failed to fetch —— 状态码都拿不到
    stubFetch(async () => { throw new TypeError('Failed to fetch') })
    try {
      await assert.rejects(
        discoverNodes({ host: 'sig.example' }),
        (e) => e.code === ERR.PEER && /Access-Control-Allow-Origin/.test(e.message),
        '信令没开 CORS 是自动发现用不了的头号原因，必须在错误里说清楚',
      )
    } finally { restore() }
  })

  it('非 2xx → PEER 且带上状态码', async () => {
    stubFetch(async () => ({ ok: false, status: 503, json: async () => ({}) }))
    try {
      await assert.rejects(discoverNodes({ host: 'sig.example' }), (e) => e.code === ERR.PEER && /503/.test(e.message))
    } finally { restore() }
  })

  it('缺 host → 本地 TypeError 之前先给可读错误', async () => {
    await assert.rejects(discoverNodes({}), (e) => e.code === ERR.PROTOCOL)
  })
})
