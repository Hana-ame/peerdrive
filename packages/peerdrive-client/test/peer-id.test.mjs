// peer-id.test.mjs — connectToPeer 的本端 id 策略。
//
// 为什么单开一个文件钉它：这条策略是「面板能不能连上一个没开 CORS 的信令」的
// 唯一开关，而它失效时的症状极难定位——PeerJS 只报含混的
// `server-error: Could not get an ID from the server`，用户只会以为是节点离线。
// 这里把它固化成契约：没给 id 就自己生成，绝不向信令要。

import { describe, it, beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'

import { connectToPeer } from '../src/client.js'
import { FakeConnection } from './fake-connection.mjs'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 最小 PeerJS 替身：只需满足 connectToPeer 用到的 surface
// （构造拿 id + opts、on/off、open 事件、connect、destroy）。
// 签名必须与真实 PeerJS 一致：`new Peer(id, opts)` —— 只有位置参数上的 id 会被认，
// options 里的 id 是被忽略的（peerjs@1.5.5 实测），这也是本文件存在的理由。
class FakePeer {
  static instances = []

  constructor(id, opts) {
    FakePeer.instances.push(this)
    this.argId = id
    this.opts = opts || {}
    this.id = id || this.opts.id || ''
    this.destroyed = false
    this.conn = null
    this._h = new Map()
    // PeerJS 的 open 是异步的（等信令握手），落后一个 tick 才发。
    // _fail 非空则改发 error —— 真实 PeerJS 里二者互斥，这里必须互斥，
    // 否则 open 先到就 resolve 了，故障注入永远测不到。
    this._fail = null
    setTimeout(() => {
      if (this._fail) this.emit('error', this._fail)
      else this.emit('open', this.id)
    }, 0)
  }

  on(type, cb) {
    if (!this._h.has(type)) this._h.set(type, [])
    this._h.get(type).push(cb)
    return this
  }

  off(type, cb) {
    const list = this._h.get(type)
    if (list) this._h.set(type, list.filter((f) => f !== cb))
    return this
  }

  emit(type, ...args) {
    for (const cb of [...(this._h.get(type) || [])]) cb(...args)
  }

  connect(peerId, connOptions) {
    this.connOptions = connOptions
    // DataConnection 也要走连线过程，open 之前送什么都该排队。
    const c = new FakeConnection({ peer: peerId, open: false })
    this.conn = c
    setTimeout(() => c.emit('open'), 0)
    return c
  }

  destroy() {
    this.destroyed = true
  }
}

const lastPeer = () => FakePeer.instances[FakePeer.instances.length - 1]

describe('connectToPeer/本端 id 不再依赖信令分配', () => {
  let realFetch = null
  let fetchCalls = []

  beforeEach(() => {
    FakePeer.instances = []
    fetchCalls = []
    realFetch = globalThis.fetch
    // 谁偷偷去 GET /peerjs/id 谁就会踩到这个桩——这就是「不再请求信令」的可执行证据。
    globalThis.fetch = (...args) => {
      fetchCalls.push(args)
      throw new Error('测试环境禁用了网络请求')
    }
  })

  afterEach(() => {
    globalThis.fetch = realFetch
  })

  it('未给 id → 自带 16 位合法 id（走位置参数），全程零 HTTP 请求', async () => {
    const client = await connectToPeer(FakePeer, 'target-node', { idleTimeoutMs: 2000 })
    const peer = lastPeer()

    assert.match(peer.id, /^[a-z0-9]{16}$/, 'id 必须是 PeerJS 可用的字符集')
    // 钉住这一条的理由：peerjs@1.5.5 会忽略 `new Peer({..., id})` 里的 options.id，
    // 照样去 GET /{path}{key}/id 向信令要号。只有位置参数认得。
    assert.equal(peer.argId, peer.id, 'id 必须同时出现在位置参数上')
    assert.equal(peer.opts.id, peer.id, 'options.id 也带上，兼容其它实现')
    assert.equal(client.localPeerId, peer.id, 'UI 显示的"我是谁"应取自本端 Peer')
    assert.equal(fetchCalls.length, 0, '不许向信令发任何 REST 请求（GET /peerjs/id 要跨域）')
    client.close()
  })

  it('显式给了 id → 原样采用，不被随机值覆盖', async () => {
    const client = await connectToPeer(FakePeer, 'target-node', {
      peerOptions: { id: 'my-fixed-id' },
      idleTimeoutMs: 2000,
    })
    assert.equal(lastPeer().id, 'my-fixed-id')
    assert.equal(lastPeer().argId, 'my-fixed-id')
    assert.equal(client.localPeerId, 'my-fixed-id')
    client.close()
  })

  it('每次连接的临时 id 互不相同', async () => {
    const a = await connectToPeer(FakePeer, 'node-1', { idleTimeoutMs: 2000 })
    const b = await connectToPeer(FakePeer, 'node-2', { idleTimeoutMs: 2000 })
    assert.notEqual(a.localPeerId, b.localPeerId, '硬编码常量会让多标签页互相顶掉')
    a.close()
    b.close()
  })

  it('信令参数（host/port/path/key/secure）原样透传', async () => {
    const sig = { host: 'peersignal.example', port: 443, path: '/peerjs', key: 'pd-signal-x', secure: true }
    const client = await connectToPeer(FakePeer, 'target-node', {
      peerOptions: sig,
      idleTimeoutMs: 2000,
    })
    const opts = lastPeer().opts
    for (const [k, v] of Object.entries(sig)) {
      assert.equal(opts[k], v, `信令参数 ${k} 必须透传`)
    }
    assert.match(opts.id, /^[a-z0-9]{16}$/, '透传的同时仍要补上 id')
    client.close()
  })

  it('拨号参数必须 serialization=raw（文本帧=JSON 头 / 二进制帧=数据块）', async () => {
    const client = await connectToPeer(FakePeer, 'target-node', { idleTimeoutMs: 2000 })
    assert.equal(lastPeer().connOptions.serialization, 'raw')
    assert.equal(lastPeer().connOptions.reliable, true)
    client.close()
  })

  it('connOptions 可覆盖默认拨号参数', async () => {
    const client = await connectToPeer(FakePeer, 'target-node', {
      connOptions: { reliable: false, metadata: { hello: 'world' } },
      idleTimeoutMs: 2000,
    })
    assert.equal(lastPeer().connOptions.reliable, false)
    assert.deepEqual(lastPeer().connOptions.metadata, { hello: 'world' })
    assert.equal(lastPeer().connOptions.serialization, 'raw', 'serialization 不允许被改掉')
    client.close()
  })

  it('构造失败（信令错误）→ 销毁 peer 并抛 CLOSED', async () => {
    class BrokenPeer extends FakePeer {
      constructor(opts) {
        super(opts)
        this._fail = { type: 'server-error', message: 'boom' }
      }
    }
    await assert.rejects(
      connectToPeer(BrokenPeer, 'target-node', { idleTimeoutMs: 2000 }),
      (e) => e.code === 'CLOSED' && /boom/.test(e.message),
    )
    await sleep(5)
    assert.equal(lastPeer().destroyed, true, '失败的 peer 必须销毁，否则信令连接泄漏')
  })
})
