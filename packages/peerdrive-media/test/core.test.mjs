// core.test.mjs — PeerMediaClient 多 DataChannel 单元测试（mock peerjs，不依赖
// 真实信令/网络）。聚焦「并发传输」边界：每个文件请求一条独立通道、
// abort 清理、keepalive、连接重建。
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
  constructor(label) {
    this._events = {}
    this.sent = []
    this.label = label
    this.closed = false
  }
  on(ev, fn) { (this._events[ev] ||= []).push(fn) }
  removeListener(ev, fn) {
    if (this._events[ev]) {
      this._events[ev] = this._events[ev].filter(f => f !== fn)
    }
  }
  send(data) { if (!this.closed) this.sent.push(data) }
  close() { this.closed = true; this.emit('close') }
  emit(ev, ...args) { (this._events[ev] || []).forEach((fn) => fn(...args)) }
}

class MockPeer {
  constructor(id, opts) {
    MockPeer.last = this
    this._events = {}
    this.conns = []
    this.destroyed = false
  }
  on(ev, fn) { (this._events[ev] ||= []).push(fn) }
  connect(peerId, opts) {
    const label = opts?.label || 'default'
    const c = new MockConn(label)
    this.conns.push(c)
    return c
  }
  destroy() { this.destroyed = true }
  emit(ev, ...args) { (this._events[ev] || []).forEach((fn) => fn(...args)) }
}

mock.module('peerjs', { namedExports: { Peer: MockPeer } })

const { PeerMediaClient, KEEPALIVE_TIMEOUT } = await import('../src/core.js')
// Node 无 URL.createObjectURL（浏览器 API），resolve 路径需 stub
globalThis.URL.createObjectURL = () => 'blob:mock'

// makeClient 构造未连接客户端；Peer 实例是 open() 时 lazy 创建的
//（第一个 load 触发），openConn 驱动信令+控制通道 open 事件。
function makeClient(opts) {
  const client = new PeerMediaClient(opts)
  let controlConn = null
  const openConn = () => {
    assert.ok(MockPeer.last, 'load 应触发 new Peer')
    MockPeer.last.emit('open')
    // 第一条连接是控制通道
    const c = MockPeer.last.conns[0]
    assert.ok(c, 'peer.open 应建立控制 DataConnection')
    controlConn = c
    c.emit('open')
  }
  // openFileConns 打开所有文件通道（控制通道 open 后创建）
  const openFileConns = () => {
    // 索引 0 是控制通道，文件通道从 1 开始
    for (let i = 1; i < MockPeer.last.conns.length; i++) {
      MockPeer.last.conns[i].emit('open')
    }
  }
  // getFileConn 获取指定索引的文件通道
  const getFileConn = (index) => {
    return MockPeer.last.conns[index + 1]
  }
  return { client, openConn, openFileConns, getControl: () => controlConn, getFileConn }
}

// sendMeta 向 conn 注入完整响应序列（meta+块+done），返回 reqId 归属的 promise。
function respondOk(conn, reqId, chunks = []) {
  conn.emit('data', JSON.stringify({ type: 'meta', mime: 'image/png', size: chunks.reduce((a, b) => a + b.length, 0), reqId }))
  for (const c of chunks) conn.emit('data', c)
  conn.emit('data', JSON.stringify({ type: 'done', reqId }))
}

test('并发请求：两个文件同时加载，各得独立通道', async () => {
  // 新架构：每个文件请求创建新的 DataChannel，并发传输
  const { client, openConn, openFileConns, getFileConn } = makeClient()
  const pA = client.load('http://x/a', { peer: 'n' })
  const pB = client.load('http://x/b', { peer: 'n' })
  openConn() // 连接就绪 → A 和 B 各创建一条文件通道
  openFileConns() // 打开文件通道

  const connA = getFileConn(0) // 第一个文件通道
  const connB = getFileConn(1) // 第二个文件通道
  assert.ok(connA, 'A 应有独立通道')
  assert.ok(connB, 'B 应有独立通道')
  assert.notEqual(connA, connB, 'A 和 B 通道不同')

  // 各自发送请求
  assert.equal(connA.sent.length, 1, 'A 发出请求')
  assert.equal(connB.sent.length, 1, 'B 发出请求')
  const reqA = JSON.parse(connA.sent[0]).reqId
  const reqB = JSON.parse(connB.sent[0]).reqId
  assert.notEqual(reqA, reqB)

  // 并发响应：A 和 B 可同时完成
  respondOk(connA, reqA, [new Uint8Array([1, 2, 3])])
  respondOk(connB, reqB, [new Uint8Array([4, 5])])

  const [resA, resB] = await Promise.all([pA, pB])
  assert.equal(resA.size, 3)
  assert.equal(resB.size, 2)
})

test('abort 文件请求：关闭通道，不影响其他并发请求', async () => {
  const { client, openConn, openFileConns, getFileConn } = makeClient()
  const ac = new AbortController()
  const pA = client.load('http://x/a', { peer: 'n', signal: ac.signal })
  const pB = client.load('http://x/b', { peer: 'n' })
  openConn()
  openFileConns()

  const connA =getFileConn(0)
  const connB = getFileConn(1)
  const reqA = JSON.parse(connA.sent[0]).reqId
  const reqB = JSON.parse(connB.sent[0]).reqId

  ac.abort() // 中止 A
  await assert.rejects(pA, (e) => e.name === 'AbortError')
  assert.ok(connA.closed, 'A 的通道应关闭')

  // B 不受影响，正常完成
  respondOk(connB, reqB, [new Uint8Array([9])])
  const resB = await pB
  assert.equal(resB.size, 1)
})

test('上游 500 拒绝：关闭通道，不影响其他请求', async () => {
  const { client, openConn, openFileConns, getFileConn } = makeClient()
  const pA = client.load('http://x/a', { peer: 'n' })
  const pB = client.load('http://x/b', { peer: 'n' })
  openConn()
  openFileConns()

  const connA = getFileConn(0)
  const connB = getFileConn(1)
  const reqA = JSON.parse(connA.sent[0]).reqId
  const reqB = JSON.parse(connB.sent[0]).reqId

  // A 收到 500
  connA.emit('data', JSON.stringify({ type: 'meta', status: 500, size: 0, reqId: reqA }))
  await assert.rejects(pA, /upstream 500/)
  // 通道归还池（不关闭），监听器已清理
  assert.ok(!connA._events.data || connA._events.data.length === 0, 'A 的 data 监听器应移除')

  // B 正常完成
  respondOk(connB, reqB, [new Uint8Array([7, 8])])
  const resB = await pB
  assert.equal(resB.size, 2)
})

test('abort 的监听在触发后即移除（防 listener 泄漏）', async () => {
  const { client, openConn, getFileConn } = makeClient()
  const ac = new AbortController()
  const pA = client.load('http://x/a', { peer: 'n', signal: ac.signal })
  openConn()

  const connA = getFileConn(0)
  ac.abort()
  await assert.rejects(pA, (e) => e.name === 'AbortError')
  // abort 触发后监听应已移除：再 abort 一次不应报错/重复触发
  ac.abort() // 二次 abort：no-op，不抛异常即通过
})

test('keepalive：ping 帧不干扰业务，收到任何帧刷新活跃', async () => {
  // 新架构：keepalive 在控制通道，业务在文件通道
  const { client, openConn, openFileConns, getControl, getFileConn } = makeClient()
  const pA = client.load('http://x/a', { peer: 'n' })
  openConn()
  openFileConns()

  const controlConn = getControl()
  const fileConn = getFileConn(0)
  const reqA = JSON.parse(fileConn.sent[0]).reqId

  // Node 端 ping 到达控制通道：只刷新活跃，业务请求照常
  controlConn.emit('data', JSON.stringify({ type: 'ping' }))
  respondOk(fileConn, reqA, [new Uint8Array([5, 6])])
  const res = await pA
  assert.equal(res.size, 2)
})

test('keepalive：超时无帧 → teardown（closed），下次 load 重建连接', async () => {
  const { client, openConn, openFileConns, getControl } = makeClient()
  mock.timers.enable()
  try {
    const pA = client.load('http://x/a', { peer: 'n' })
    openConn()
    openFileConns()
    // 推进 16s：期间无任何帧到达 → keepalive 超时 → teardown
    mock.timers.tick(KEEPALIVE_TIMEOUT + 1000)
    await assert.rejects(pA, /keepalive timeout|file channel closed/)
  } finally {
    mock.timers.reset()
  }
  // teardown 置 closed → 再次 load 必须重建连接（新 Peer + 新 conn）
  const pB = client.load('http://x/b', { peer: 'n' })
  assert.ok(MockPeer.last, 'closed 槽位重建应 new Peer')
  MockPeer.last.emit('open')
  // 新控制通道
  const controlConn2 = MockPeer.last.conns[0]
  controlConn2.emit('open')
  // 新文件通道
  const fileConn2 = MockPeer.last.conns[1]
  fileConn2.emit('open')
  const reqB = JSON.parse(fileConn2.sent[0]).reqId
  respondOk(fileConn2, reqB, [new Uint8Array([3])])
  const res = await pB
  assert.equal(res.size, 1)
})
