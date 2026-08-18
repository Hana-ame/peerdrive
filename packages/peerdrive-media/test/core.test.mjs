// core.test.mjs — PeerMediaClient 串行队列单元测试（mock peerjs，不依赖
// 真实信令/网络）。聚焦「串行槽释放」边界：连接就绪前的排队、在途请求
// 中止后队列必须继续、迟到帧不得卡死队列。
//
// 为什么 mock 而不走真实信令：这些场景依赖精确的事件时序（open/done/
// abort 的先后），真实信令下不可控且慢；mock 后逐帧驱动确定性验证。
// 浏览器/真实信令的端到端覆盖见 e2e-browser.mjs 与 e2e.test.mjs。

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mock } from 'node:test'

// ---- mock peerjs 模块（必须在 import core.js 之前注册）----

const peerEvents = [] // [type, id, ...] 测试驱动事件流

class MockConn {
  constructor() {
    this._events = {}
    this.sent = []
  }
  on(ev, fn) { (this._events[ev] ||= []).push(fn) }
  send(data) { this.sent.push(data) }
  emit(ev, ...args) { (this._events[ev] || []).forEach((fn) => fn(...args)) }
}

class MockPeer {
  constructor(id, opts) {
    MockPeer.last = this
    this._events = {}
    this.conns = []
  }
  on(ev, fn) { (this._events[ev] ||= []).push(fn) }
  connect(peerId, opts) {
    const c = new MockConn()
    this.conns.push(c)
    return c
  }
  destroy() {}
  emit(ev, ...args) { (this._events[ev] || []).forEach((fn) => fn(...args)) }
}

mock.module('peerjs', { namedExports: { Peer: MockPeer } })

const { PeerMediaClient } = await import('../src/core.js')
// Node 无 URL.createObjectURL（浏览器 API），resolve 路径需 stub
globalThis.URL.createObjectURL = () => 'blob:mock'

// makeClient 构造未连接客户端；Peer 实例是 open() 时 lazy 创建的
//（第一个 load 排队触发），openConn 驱动信令+连接 open 事件。
function makeClient(opts) {
  const client = new PeerMediaClient(opts)
  let conn = null
  const openConn = () => {
    assert.ok(MockPeer.last, 'load 应触发 new Peer')
    MockPeer.last.emit('open')
    const c = MockPeer.last.conns[0]
    assert.ok(c, 'peer.open 应建立 DataConnection')
    conn = c
    c.emit('open')
  }
  return { client, openConn, getConn: () => conn }
}

// sendMeta 向 conn 注入完整响应序列（meta+块+done），返回 reqId 归属的 promise。
// 从 conn.sent 中解析最后一次发出的请求帧 reqId。
function respondOk(conn, reqId, chunks = []) {
  conn.emit('data', JSON.stringify({ type: 'meta', mime: 'image/png', size: chunks.reduce((a, b) => a + b.length, 0), reqId }))
  for (const c of chunks) conn.emit('data', c)
  conn.emit('data', JSON.stringify({ type: 'done', reqId }))
}

test('在途请求被 abort 后，排队的下一个请求仍会发出（串行槽释放）', async () => {
  // 发现背景（代码审阅 2026-08-18）：onAbort 只删 pending + reject，
  // 不 flush——Node 端无法取消已发出的请求，处理完仍回 done/err，
  // handleData 因 pending 不存在直接 return，排队请求永久不发。
  const { client, openConn, getConn } = makeClient()
  const ac = new AbortController()
  const pA = client.load('http://x/a', { peer: 'n', signal: ac.signal })
  const pB = client.load('http://x/b', { peer: 'n' })
  openConn() // 连接就绪 → flush → A 发出（B 排队）

  const conn = getConn()
  assert.equal(conn.sent.length, 1, '就绪后只应发出 A')
  const reqA = JSON.parse(conn.sent[0]).reqId

  ac.abort() // 中止 A：串行槽释放 → B 必须立即发出
  await assert.rejects(pA, (e) => e.name === 'AbortError')

  assert.equal(conn.sent.length, 2, 'abort 后 B 应立即发出')
  const reqB = JSON.parse(conn.sent[1]).reqId
  assert.notEqual(reqB, reqA)

  // A 的迟到 done 到达（Node 端已处理完）：不得报错、不得影响 B
  conn.emit('data', JSON.stringify({ type: 'done', reqId: reqA }))
  // B 正常完成
  respondOk(conn, reqB, [new Uint8Array([1, 2, 3])])
  const res = await pB
  assert.equal(res.size, 3)
})

test('中止后迟到 err 帧同样不卡死队列', async () => {
  const { client, openConn, getConn } = makeClient()
  const ac = new AbortController()
  const pA = client.load('http://x/a', { peer: 'n', signal: ac.signal })
  const pB = client.load('http://x/b', { peer: 'n' })
  openConn()
  const conn = getConn()
  const reqA = JSON.parse(conn.sent[0]).reqId

  ac.abort()
  await assert.rejects(pA, (e) => e.name === 'AbortError')
  assert.equal(conn.sent.length, 2, 'abort 后 B 发出')

  // Node 端对 A 回 err（而不是 done）
  const reqB = JSON.parse(conn.sent[1]).reqId
  conn.emit('data', JSON.stringify({ type: 'err', msg: 'not found', reqId: reqA }))
  respondOk(conn, reqB, [new Uint8Array([9])])
  const res = await pB
  assert.equal(res.size, 1)
})

test('meta status>=400 拒绝后，排队请求继续（与 done/err 同语义）', async () => {
  // 发现背景（代码审阅 2026-08-18）：meta 400 分支 delete+reject 但不
  // flush，一旦上游经此路径拒绝（Node 端现发 err 帧，此分支为防御），
  // 队列同样卡死。与 done/err 分支对齐补 flush。
  const { client, openConn, getConn } = makeClient()
  const pA = client.load('http://x/a', { peer: 'n' })
  const pB = client.load('http://x/b', { peer: 'n' })
  openConn()
  const conn = getConn()
  const reqA = JSON.parse(conn.sent[0]).reqId

  conn.emit('data', JSON.stringify({ type: 'meta', status: 500, size: 0, reqId: reqA }))
  await assert.rejects(pA, /upstream 500/)
  assert.equal(conn.sent.length, 2, 'meta 400 拒绝后 B 应立即发出')

  const reqB = JSON.parse(conn.sent[1]).reqId
  respondOk(conn, reqB, [new Uint8Array([7, 8])])
  const res = await pB
  assert.equal(res.size, 2)
})

test('abort 的监听在触发后即移除（防 listener 泄漏）', async () => {
  const { client, openConn, getConn } = makeClient()
  const ac = new AbortController()
  const pA = client.load('http://x/a', { peer: 'n', signal: ac.signal })
  openConn()
  ac.abort()
  await assert.rejects(pA, (e) => e.name === 'AbortError')
  // abort 触发后监听应已移除：再 abort 一次不应报错/重复触发
  ac.abort() // 二次 abort：no-op，不抛异常即通过
  assert.equal(MockPeer.last.conns.length, 1)
})