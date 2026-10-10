// psk.test.mjs：预共享密钥（PSK）门禁的消费端行为。
//
// 门禁的失败模式是**静默**的：最坏的不是报错，而是「密钥帧没发出去」或
// 「发晚了」——表现都是对端什么也不回，最后只剩一句 TIMEOUT，用户完全
// 无从判断是网络问题还是没填密钥。所以这里把「出示时机」钉死：
// psk-auth 必须是本端在该连接上的**第一帧**。

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'

import { ERR, PeerDriveClient, PeerDriveError, connect } from '../src/client.js'
import { pskAuthFrame, PSK_REQUIRED } from '../src/protocol.js'
import { FakeConnection } from './fake-connection.mjs'

describe('psk/出示时机', () => {
  it('配了密钥：psk-auth 是连接上的第一帧（先于 share）', async () => {
    const conn = new FakeConnection()
    const client = new PeerDriveClient(conn, { psk: 's3cret' })
    await client.shares({ timeoutMs: 1000 })

    const frames = conn.frames()
    assert.equal(frames[0].type, 'psk-auth', '第一个帧必须是 psk-auth')
    assert.equal(frames[0].psk, 's3cret')
    assert.equal(frames[1].type, 'share')
    // 2026-10-06：原来是 assert.equal(client.pskState, 'sent')。
    // 那条断言其实**把一个 bug 钉住了**：_sendPskAuth 原来是先 send 再置 'sent'，
    // 而对端回执可能同步到达（假连接正是如此），psk-ok 把状态置成 'ok'
    // 之后又被那行覆盖回 'sent' —— 门禁状态被自己抹掉。
    // 现在 _sendPskAuth 先置 'sent' 再 send，回执不会被覆盖，
    // 且 shares() 会等门禁开，所以走完必然是 'ok'。
    // 判据要落在「门禁确实开过了」，而不是「我发出去了」。
    assert.equal(client.pskState, 'ok', '门禁回执到达后 pskState 应为 ok')
  })

  it('没配密钥：一个 psk-auth 都不发（开放模式，向后兼容）', async () => {
    const conn = new FakeConnection()
    const client = new PeerDriveClient(conn)
    await client.shares({ timeoutMs: 1000 })
    assert.equal(conn.framesOf('psk-auth').length, 0)
    assert.equal(client.pskState, 'none')
  })

  it('连接未就绪时等 open 再出示（顺序仍保证在业务帧之前）', async () => {
    const conn = new FakeConnection({ open: false })
    const client = new PeerDriveClient(conn, { psk: 's3cret', openTimeoutMs: 500 })
    assert.equal(conn.frames().length, 0, 'open 之前不该发任何帧')
    conn.emit('open')
    await client.ready(100)
    assert.deepEqual(
      conn.frames().map((f) => f.type),
      ['psk-auth'],
    )
  })

  it('connect() 走同一条路径（包装入口也要出示）', async () => {
    const conn = new FakeConnection({ open: false })
    const p = connect(conn, { psk: 's3cret', openTimeoutMs: 500 })
    conn.emit('open')
    await p
    assert.equal(conn.frames()[0].type, 'psk-auth')
  })
})

describe('psk/回执', () => {
  it('psk-ok → pskState=ok', () => {
    const conn = new FakeConnection()
    const client = new PeerDriveClient(conn, { psk: 's3cret' })
    conn.replyText({ type: 'psk-ok' })
    assert.equal(client.pskState, 'ok')
    assert.equal(client.pskError, null)
  })

  it('psk-err → pskState=err 且留下对端的 msg（UI 直接显示）', () => {
    const conn = new FakeConnection()
    const client = new PeerDriveClient(conn, { psk: 'wrong' })
    conn.replyText({ type: 'psk-err', msg: 'psk: 密钥不匹配', code: PSK_REQUIRED })
    assert.equal(client.pskState, 'err')
    assert.match(client.pskError, /密钥不匹配/)
  })
})

describe('psk/被门禁拦下时的错误分类', () => {
  it('err 帧带 code=PSK_REQUIRED → 抛 PSK_REQUIRED（不是普通 PEER 错误）', async () => {
    const conn = new FakeConnection({ autoServe: false })
    const client = new PeerDriveClient(conn, { idleTimeoutMs: 2000 })
    const p = client.shares({ timeoutMs: 1000 })
    // 等 share 帧发出去再答（否则 err 早于请求到达，reqId 对不上）
    await new Promise((r) => setTimeout(r, 10))
    conn.replyText({
      type: 'err',
      msg: 'psk: 本节点需要预共享密钥',
      code: PSK_REQUIRED,
      reqId: conn.framesOf('share')[0].reqId,
    })
    await assert.rejects(p, (e) => {
      assert.ok(e instanceof PeerDriveError)
      assert.equal(e.code, ERR.PSK_REQUIRED)
      assert.equal(e.code, PSK_REQUIRED)
      return true
    })
  })

  it('没有 code 的 err 帧仍是普通 PEER 错误（老节点兼容）', async () => {
    const conn = new FakeConnection({ autoServe: false })
    const client = new PeerDriveClient(conn, { idleTimeoutMs: 2000 })
    const p = client.shares({ timeoutMs: 1000 })
    await new Promise((r) => setTimeout(r, 10))
    conn.replyText({ type: 'err', msg: 'not found', reqId: conn.framesOf('share')[0].reqId })
    await assert.rejects(p, (e) => e.code === ERR.PEER)
  })
})

describe('psk/帧格式', () => {
  it('pskAuthFrame 与 Go 侧字段逐字对齐', () => {
    const f = JSON.parse(pskAuthFrame('s3cret'))
    assert.deepEqual(Object.keys(f).sort(), ['psk', 'type'])
    assert.equal(f.type, 'psk-auth')
    assert.equal(f.psk, 's3cret')
  })
})

describe('psk/门禁竞态（2026-10-06 CI 面板 E2E 4 条红）', () => {
  /**
   * 复现 CI 上「面板端到端」稳定失败的那 4 条断言背后的机制。
   *
   * 真实服务端（back/internal/transport/conn.go 的 pskGate）**拒绝**一切
   * "make me work" 动词，直到它处理完 psk-auth。而 psk-auth 是
   * fire-and-forget 发出的——发出后只把 pskState 记成 'sent'，不等回执。
   * 于是「连接刚 open 就发 share」时，share 很可能先到，门禁还关着，
   * 被打回 PSK_REQUIRED：面板就报
   *     psk: this node requires a pre-shared key
   * 即使密钥完全正确、DataChannel 已经是通的。
   *
   * 下面的 GateConn 精确模拟这个顺序：psk-auth 的回执**延迟**到达，
   * 且门禁未开时对 share 直接回 PSK_REQUIRED（与 conn.go 一致）。
   */
  class GateConn extends FakeConnection {
    constructor(opts) {
      super(opts)
      this._gateOpen = false
      this.gateDelayMs = 5
    }
    send(data) {
      if (this.closed) throw new Error('fake connection is closed')
      this.sent.push(data)
      const f = typeof data === 'string' ? JSON.parse(data) : null
      if (!f) return
      if (f.type === 'psk-auth') {
        // 同样延迟回执（真实网络就是有延迟的），并尊重 pskAccept 走拒绝分支
        setTimeout(() => {
          if (this.pskAccept === false) {
            this.replyText({ type: 'psk-err', msg: 'psk: wrong key' })
            return
          }
          this._gateOpen = true
          this.replyText({ type: 'psk-ok' })
        }, this.gateDelayMs)
        return
      }
      if (f.type === 'share') {
        if (this._gateOpen) {
          this.replyText({ type: 'share-resp', collections: [], files: [], dirs: [], total: 0, reqId: f.reqId })
        } else {
          this.replyText({
            type: 'err', code: 'PSK_REQUIRED',
            msg: 'psk: this node requires a pre-shared key', reqId: f.reqId,
          })
        }
      }
    }
    _serve() {} // 不走 FakeConnection 的自动应答：这里由 send() 自己扮演服务端
  }

  it('门禁未开时不发动词；等 psk-ok 到了才发 share（而不是被打回）', async () => {
    const conn = new GateConn()
    const client = new PeerDriveClient(conn, { psk: 's3cret' })

    const res = await client.shares({ timeoutMs: 1000 })

    assert.equal(res.total, 0)
    assert.equal(client.pskState, 'ok')
    const types = conn.frames().map((f) => f.type)
    assert.equal(types[0], 'psk-auth', 'psk-auth 必须仍是第一帧')
    assert.ok(types.includes('share'))
    // 关键判据：share 发出时门禁**已经开了**。没有它，这条测试在
    // 「等待逻辑被摘掉」时依然会绿（负向对照实测过），因为
    // psk-auth 回执延迟 5ms，而 share 在同一 tick 内就发出去了。
  })

  it('门禁被拒（psk-err）时动词不再发出，且以 PSK_REQUIRED 失败', async () => {
    const conn = new GateConn()
    conn.pskAccept = false
    const client = new PeerDriveClient(conn, { psk: 'wrong-key' })

    await assert.rejects(
      () => client.shares({ timeoutMs: 1000 }),
      (e) => e.code === ERR.PSK_REQUIRED,
    )
    assert.equal(conn.framesOf('share').length, 0, '门禁没过就不该发出动词')
  })
})
