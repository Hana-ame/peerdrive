// client.test.mjs：消费端状态机契约。
//
// 覆盖的重点是「错了用户看不出来」的那几类：完整性校验、字段名、连接级 expect
// 的交接、取消语义、内存闸。假连接扮演服务端，帧序列与 Go 侧 serveFile /
// serveShare 的输出逐字一致（见 test/fake-connection.mjs 的 _serve）。

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'

import { ERR, PeerDriveClient, PeerDriveError, connect } from '../src/client.js'
import { FakeConnection, expectHash, makeBytes } from './fake-connection.mjs'

const H = (n, c = 'a') => c.repeat(n)

async function makeClient(opts = {}, connOpts = {}) {
  const conn = new FakeConnection(connOpts)
  const client = new PeerDriveClient(conn, { idleTimeoutMs: 5000, ...opts })
  return { conn, client }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

describe('client/连接生命周期', () => {
  it('已就绪的连接 ready() 立即返回', async () => {
    const { conn, client } = await makeClient()
    assert.equal(await client.ready(10), client)
    assert.equal(client.isOpen, true)
    conn.close()
  })

  it('未就绪时等 open 事件（PeerJS 在 connecting 阶段 open=false）', async () => {
    const conn = new FakeConnection({ open: false })
    const client = new PeerDriveClient(conn, { openTimeoutMs: 200 })
    assert.equal(client.isOpen, false)
    let resolved = false
    const p = connect(conn, { openTimeoutMs: 200 }).then((c) => {
      resolved = true
      return c
    })
    assert.equal(resolved, false)
    conn.emit('open')
    const c = await p
    assert.equal(c.isOpen, true)
  })

  it('超时未 open → TIMEOUT，且不泄漏等待者', async () => {
    const conn = new FakeConnection({ open: false })
    const client = new PeerDriveClient(conn, { openTimeoutMs: 20 })
    await assert.rejects(client.ready(20), (e) => e.code === ERR.TIMEOUT)
  })

  it('构造参数校验：连接必须实现 on/send', () => {
    assert.throws(() => new PeerDriveClient({}), TypeError)
    assert.throws(() => new PeerDriveClient(null), TypeError)
  })

  it('close() 让在飞请求以 CLOSED 失败，并关闭连接', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    const p = client.fetch(H(64))
    // fetch 是 async 函数：调用即同步跑完 _startFetch（req 帧当场发出），
    // 之后才停在等块上。所以这里能立刻断言帧已发出。
    assert.equal(conn.framesOf('req').length, 1)
    client.close()
    await assert.rejects(p, (e) => e.code === ERR.CLOSED)
    assert.ok(conn.closed)
  })

  it('连接侧 close 事件让在飞请求失败', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    const p = client.fetch(H(64))
    conn.close()
    await assert.rejects(p, (e) => e.code === ERR.CLOSED)
  })
})

describe('client/shares', () => {
  it('解析 share-resp（与 Go 侧 ShareSnapshot 字段一致）', async () => {
    const { conn, client } = await makeClient()
    conn.shareSnapshot = {
      collections: [{ hash: H(64, 'b'), name: '打包合集', entries: [{ path: 'a.txt', hash: H(64, 'c') }] }],
      files: [{ hash: H(64, 'd'), name: 'x.bin', size: 3 }],
      dirs: ['/data/share'],
    }
    const snap = await client.shares()
    assert.equal(snap.collections.length, 1)
    assert.equal(snap.files[0].name, 'x.bin')
    assert.equal(snap.total, 2)
    assert.deepEqual(conn.lastFrame().type, 'share')
  })

  it('未开启共享时是空清单而不是错误（空态是合法业务状态）', async () => {
    const { conn, client } = await makeClient()
    conn.shareSnapshot = null
    const snap = await client.shares()
    assert.deepEqual(snap.collections, [])
    assert.deepEqual(snap.files, [])
    assert.equal(snap.total, 0)
  })

  it('对端回 err 帧 → PEER 并带对端文案', async () => {
    const { conn, client } = await makeClient({ verbTimeoutMs: 300 }, { autoServe: false })
    const p = client.shares({ timeoutMs: 300 })
    const reqId = conn.lastFrame().reqId
    conn.replyText({ type: 'err', msg: 'not found', reqId })
    await assert.rejects(p, (e) => e.code === ERR.PEER && /not found/.test(e.message))
  })

  it('对端不答 → TIMEOUT（不是永久挂起）', async () => {
    const { client } = await makeClient({ verbTimeoutMs: 30 }, { autoServe: false })
    await assert.rejects(client.shares({ timeoutMs: 30 }), (e) => e.code === ERR.TIMEOUT)
  })
})

describe('client/fetch 正常路径', () => {
  it('完整拉取：内容一致、stats 计数、请求帧字段与 Go 侧对齐', async () => {
    const { conn, client } = await makeClient()
    const content = makeBytes(300, 11)
    const hash = await expectHash(content)
    conn.serve(hash, content, { chunkSize: 64 })

    const out = await client.fetch(hash)
    assert.deepEqual([...out], [...content])
    assert.equal(await expectHash(out), hash) // 内容寻址：结果自证
    assert.equal(client.stats.requests, 1)
    assert.equal(client.stats.bytes, 300)
    assert.equal(client.stats.failures, 0)

    const req = conn.framesOf('req')[0]
    assert.deepEqual(Object.keys(req).sort(), ['hash', 'offset', 'reqId', 'size', 'type', 'v'])
    assert.equal(req.type, 'req')
    assert.equal(req.hash, hash)
    assert.equal(req.offset, 0)
    assert.equal(req.size, -1)
    assert.ok(typeof req.reqId === 'string' && req.reqId.length > 0)
  })

  it('多块内容逐块产出（stream 不驻留内存）', async () => {
    const { conn, client } = await makeClient()
    const content = makeBytes(20, 5)
    const hash = await expectHash(content)
    conn.serve(hash, content, { chunkSize: 8 })

    const sizes = []
    for await (const chunk of client.stream(hash)) sizes.push(chunk.byteLength)
    assert.deepEqual(sizes, [8, 8, 4])
  })

  it('fetchBlob / fetchText 走同一条链路', async () => {
    const { conn, client } = await makeClient()
    const text = new TextEncoder().encode('hello peerdrive')
    const hash = await expectHash(text)
    conn.serve(hash, text)
    const blob = await client.fetchBlob(hash, { name: 'a.txt' })
    assert.equal(await blob.text(), 'hello peerdrive')
    assert.equal(blob.type, 'text/plain;charset=utf-8')
    const s = await client.fetchText(hash)
    assert.equal(s, 'hello peerdrive')
  })

  it('onProgress 报告已收/总量', async () => {
    const { conn, client } = await makeClient()
    const content = makeBytes(20, 7)
    const hash = await expectHash(content)
    conn.serve(hash, content, { chunkSize: 8 })
    const seen = []
    await client.fetch(hash, { onProgress: (received, total) => seen.push([received, total]) })
    assert.ok(seen.length >= 3)
    assert.deepEqual(seen[seen.length - 1], [20, 20])
  })

  it('range 请求不做整份 sha256 校验（片段摘要本来就不等于全量摘要）', async () => {
    const { conn, client } = await makeClient()
    // 用一个与内容不匹配的 hash + range 请求：仍然应当成功
    const hash = H(64, 'e')
    conn.serve(hash, makeBytes(10, 1), { chunkSize: 4 })
    const out = await client.fetch(hash, { offset: 2, size: 4 })
    assert.equal(out.byteLength, 10)
    assert.equal(client.stats.failures, 0)
  })
})

describe('client/fetch 故障路径', () => {
  it('hash 不是 64 位小写 hex → 本地即拒，不发任何帧', async () => {
    const { conn, client } = await makeClient()
    await assert.rejects(client.fetch('A'.repeat(64)), (e) => e.code === ERR.INVALID_HASH)
    await assert.rejects(client.fetch('short'), (e) => e.code === ERR.INVALID_HASH)
    assert.equal(conn.sent.length, 0)
  })

  it('内容与 hash 不符 → HASH_MISMATCH（内容寻址的核心保证）', async () => {
    const { conn, client } = await makeClient()
    const hash = H(64, 'a') // 与实际内容无关
    conn.serve(hash, makeBytes(50, 2))
    await assert.rejects(client.fetch(hash), (e) => e.code === ERR.HASH_MISMATCH && e.actual !== e.expected)
    assert.equal(client.stats.failures, 1)
  })

  it('对端 done 声明的字节数与实收不符 → INCOMPLETE（防截断静默损坏）', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    const hash = H(64, 'f')
    const p = client.fetch(hash)
    const reqId = conn.lastFrame().reqId
    conn.replyText({ type: 'meta', hash, total: 999, reqId })
    conn.replyText({ type: 'data', hash, offset: 0, size: 4, reqId })
    conn.replyChunk(new Uint8Array([1, 2, 3, 4]))
    conn.replyText({ type: 'done', hash, offset: 0, size: 999, reqId })
    await assert.rejects(p, (e) => e.code === ERR.INCOMPLETE && e.received === 4 && e.declared === 999)
  })

  it('非法块大小（0 / 超上限）→ PROTOCOL', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    const p = client.fetch(H(64, 'a'))
    const reqId = conn.lastFrame().reqId
    conn.replyText({ type: 'data', hash: H(64, 'a'), offset: 0, size: 0, reqId })
    await assert.rejects(p, (e) => e.code === ERR.PROTOCOL)
  })

  it('对端声明的大小超过内存闸 → TOO_LARGE（meta 阶段就拦，不白下）', async () => {
    const { conn, client } = await makeClient({ maxBufferBytes: 100 })
    const hash = H(64, 'b')
    conn.serve(hash, makeBytes(500, 1), { chunkSize: 50 })
    await assert.rejects(client.fetch(hash), (e) => e.code === ERR.TOO_LARGE)
  })

  it('对端未知大小但实际超出内存闸 → 中途 TOO_LARGE', async () => {
    const { conn, client } = await makeClient({ maxBufferBytes: 100 }, { autoServe: false })
    const hash = H(64, 'c')
    const p = client.fetch(hash)
    const reqId = conn.lastFrame().reqId
    conn.replyText({ type: 'meta', hash, total: -1, reqId }) // -1 = 对端也不知大小
    for (let i = 0; i < 3; i++) {
      conn.replyText({ type: 'data', hash, offset: i * 64, size: 64, reqId })
      conn.replyChunk(makeBytes(64, i + 1))
    }
    await assert.rejects(p, (e) => e.code === ERR.TOO_LARGE)
  })

  it('对端 not found（err 帧）→ PEER', async () => {
    const { conn, client } = await makeClient()
    await assert.rejects(client.fetch(H(64, 'd')), (e) => e.code === ERR.PEER && /not found/.test(e.message))
  })

  it('空闲超时 → TIMEOUT', async () => {
    const { client } = await makeClient({ idleTimeoutMs: 20 }, { autoServe: false })
    await assert.rejects(client.fetch(H(64, 'a')), (e) => e.code === ERR.TIMEOUT)
  })
})

describe('client/连接级 expect 与乱序帧', () => {
  it('两个并发请求的块按 data 头正确归属（协议约束 2）', async () => {
    const { conn, client } = await makeClient({ idleTimeoutMs: 5000 }, { autoServe: false })
    const contentA = new TextEncoder().encode('AAAAAAAAAA') // 10 字节
    const contentB = new TextEncoder().encode('BBBB') // 4 字节
    const hashA = await expectHash(contentA)
    const hashB = await expectHash(contentB)

    const pa = client.fetch(hashA)
    const pb = client.fetch(hashB)
    const [reqA, reqB] = conn.framesOf('req').map((f) => f.reqId)
    assert.ok(reqA && reqB && reqA !== reqB)

    // 交错发送：A 头+块 → B 头+块 → 一个无头的游离块（应被丢弃）→ A 头+块+done → B done
    conn.replyText({ type: 'meta', hash: hashA, total: 10, reqId: reqA })
    conn.replyText({ type: 'data', hash: hashA, offset: 0, size: 4, reqId: reqA })
    conn.replyChunk(contentA.subarray(0, 4))

    conn.replyText({ type: 'meta', hash: hashB, total: 4, reqId: reqB })
    conn.replyText({ type: 'data', hash: hashB, offset: 0, size: 4, reqId: reqB })
    conn.replyChunk(contentB.subarray(0, 4))

    // 游离块：没有前置 data 头，必须被丢弃而不是错挂到某个请求上
    conn.replyChunk(new Uint8Array([0xde, 0xad, 0xbe, 0xef]))

    conn.replyText({ type: 'data', hash: hashA, offset: 4, size: 6, reqId: reqA })
    conn.replyChunk(contentA.subarray(4))
    conn.replyText({ type: 'done', hash: hashA, offset: 0, size: 10, reqId: reqA })
    conn.replyText({ type: 'done', hash: hashB, offset: 0, size: 4, reqId: reqB })

    const [outA, outB] = await Promise.all([pa, pb])
    assert.deepEqual([...outA], [...contentA])
    assert.deepEqual([...outB], [...contentB])
    assert.equal(client.stats.failures, 0)
  })

  it('未知 reqId 的帧被忽略（迟到的响应/别人的请求）', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    conn.replyText({ type: 'meta', hash: H(64, 'a'), total: 5, reqId: 'not-mine' })
    conn.replyText({ type: 'data', hash: H(64, 'a'), offset: 0, size: 5, reqId: 'not-mine' })
    conn.replyChunk(new Uint8Array([1, 2, 3, 4, 5]))
    conn.replyText({ type: 'done', hash: H(64, 'a'), offset: 0, size: 5, reqId: 'not-mine' })
    conn.replyText('{"type":"meta"}') // 缺 reqId 的帧
    conn.replyText('not json at all') // 非协议帧
    assert.equal(client.stats.bytes, 0)
    assert.equal(client.stats.failures, 0)
  })
})

describe('client/取消', () => {
  it('消费端取了一块后中断：后续帧被丢弃、不抛错、bytes 不增长、不计失败', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    const hash = H(64, 'a')
    const it = client.stream(hash)[Symbol.asyncIterator]()
    const first = it.next() // 触发 req 帧（生成器体在首次 next 时同步跑到第一个 await）
    const reqId = conn.lastFrame().reqId

    conn.replyText({ type: 'data', hash, offset: 0, size: 2, reqId })
    conn.replyChunk(new Uint8Array([1, 2]))
    const r = await first
    assert.equal(r.value.byteLength, 2)
    const bytesBefore = client.stats.bytes

    await it.return() // 消费端中断（等价于 for await 里 break）
    assert.equal(client.stats.failures, 0)

    // 协议里没有"取消"帧：对端会继续把这次请求发完，我们只是丢帧。
    // 不能因此抛错，也不能把迟到内容算进统计。
    conn.replyText({ type: 'data', hash, offset: 2, size: 2, reqId })
    conn.replyChunk(new Uint8Array([3, 4]))
    conn.replyText({ type: 'done', hash, offset: 0, size: 4, reqId })
    assert.equal(client.stats.bytes, bytesBefore)
    assert.equal(client._pend.size, 0)
    assert.equal(client._expect, null)
  })

  it('AbortSignal 取消在飞请求', async () => {
    const { conn, client } = await makeClient({}, { autoServe: false })
    const ac = new AbortController()
    const p = client.fetch(H(64, 'a'), { signal: ac.signal })
    ac.abort()
    await assert.rejects(p, (e) => e.code === ERR.CANCELLED)
    assert.equal(conn.framesOf('req').length, 1) // 取消不涉及重发
  })

  it('已 abort 的 signal 直接拒（不发请求）', async () => {
    const { conn, client } = await makeClient()
    const ac = new AbortController()
    ac.abort()
    await assert.rejects(client.fetch(H(64, 'a'), { signal: ac.signal }), (e) => e.code === ERR.CANCELLED)
    assert.equal(conn.framesOf('req').length, 0)
  })
})

describe('client/saveAs 环境要求', () => {
  it('无 DOM 时明确报错（Node 里误用 saveAs 的常见坑）', async () => {
    const { client } = await makeClient()
    await assert.rejects(client.saveAs(H(64, 'a'), 'a.bin'), (e) => e.code === ERR.PROTOCOL)
  })

  it('saveShare 需要带 hash 的条目', async () => {
    const { client } = await makeClient()
    await assert.rejects(client.saveShare({}), (e) => e.code === ERR.PROTOCOL)
    await assert.rejects(client.saveShare(null), (e) => e.code === ERR.PROTOCOL)
  })
})

describe('client/错误类型', () => {
  it('PeerDriveError 带 code（UI 按 code 分支，不匹配文案）', () => {
    const e = new PeerDriveError('boom', ERR.TIMEOUT, { extra: 1 })
    assert.equal(e.name, 'PeerDriveError')
    assert.equal(e.code, ERR.TIMEOUT)
    assert.equal(e.extra, 1)
    assert.ok(e instanceof Error)
    assert.ok(e instanceof PeerDriveError)
  })
})

describe('client/空闲计时器不泄漏', () => {
  it('成功完成后计时器已清（进程能自然退出）', async () => {
    const { conn, client } = await makeClient()
    const content = makeBytes(10, 1)
    const hash = await expectHash(content)
    conn.serve(hash, content)
    await client.fetch(hash)
    assert.equal(client._pend.size, 0)
    assert.equal(client._expect, null)
    await sleep(5)
  })
})
