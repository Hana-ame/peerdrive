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
    assert.equal(client.pskState, 'sent')
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
    assert.deepEqual(Object.keys(f).sort(), ['psk', 'type', 'v'])
    assert.equal(f.type, 'psk-auth')
    assert.equal(f.psk, 's3cret')
  })
})
