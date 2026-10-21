// protocol.test.mjs — 帧协议纯逻辑单测（无网络/无 WebRTC）。
// 发现背景：协议是与 Node 端共享的契约，编解码错误会导致帧错配
// （二进制块挂错请求）——先测纯函数再测 E2E。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  makeUrlRequest, parseFrame, isBinaryFrame, toUint8Array, guessMime, nextReqId,
} from '../src/protocol.js'
import { createSwRequestRouter } from '../src/sw.js'

test('makeUrlRequest 生成合法请求帧', () => {
  const s = makeUrlRequest('https://x/a.png', 'r1')
  const o = JSON.parse(s)
  assert.equal(o.type, 'url')
  assert.equal(o.url, 'https://x/a.png')
  assert.equal(o.reqId, 'r1')
})

test('parseFrame 解析合法帧 / 拒绝非 JSON', () => {
  assert.deepEqual(parseFrame('{"type":"meta","reqId":"a"}'), { type: 'meta', reqId: 'a' })
  assert.equal(parseFrame('not json'), null)
  assert.equal(parseFrame('"just a string"'), null)
  assert.equal(parseFrame('42'), null)
})

test('isBinaryFrame 区分文本/二进制', () => {
  assert.equal(isBinaryFrame('hello'), false)
  assert.equal(isBinaryFrame(new Uint8Array([1, 2])), true)
  assert.equal(isBinaryFrame(new Uint8Array([1, 2]).buffer), true)
  assert.equal(isBinaryFrame(new DataView(new ArrayBuffer(4))), true)
})

test('toUint8Array 统一二进制帧类型', () => {
  const a = toUint8Array(new ArrayBuffer(4))
  assert.ok(a instanceof Uint8Array)
  assert.equal(a.length, 4)
  const b = toUint8Array(new Uint8Array([9, 9]))
  assert.deepEqual([...b], [9, 9])
  const dv = new DataView(new Uint8Array([1, 2, 3]).buffer, 1, 2)
  const c = toUint8Array(dv)
  assert.deepEqual([...c], [2, 3]) // 尊重 byteOffset/byteLength
})

test('guessMime 优先 Content-Type，缺失时按扩展名兜底', () => {
  assert.equal(guessMime('https://x/a.png', 'image/png'), 'image/png')
  assert.equal(guessMime('https://x/a.png', 'IMAGE/PNG; charset=utf-8'), 'image/png')
  assert.equal(guessMime('https://x/a.mp4', ''), 'video/mp4')
  assert.equal(guessMime('https://x/a.unknown', null), 'application/octet-stream')
  assert.equal(guessMime('https://x/a.bin?x=1', 'video/webm'), 'video/webm')
})

test('nextReqId 唯一且可排序（并发请求不撞 ID）', () => {
  const ids = new Set()
  for (let i = 0; i < 1000; i++) ids.add(nextReqId())
  assert.equal(ids.size, 1000)
})
test('createSwRequestRouter 并发多请求按 reqId 独立归属，不受无关消息干扰', async () => {
  const router = createSwRequestRouter(500)
  const sent = []
  const p1 = router.waitForResponse('req-1', () => sent.push('req-1'))
  const p2 = router.waitForResponse('req-2', () => sent.push('req-2'))
  assert.equal(router.pendingCount, 2)

  // 无关消息不得摘除监听器
  assert.equal(router.handleResponseMessage({ type: 'pdm-config', config: {} }), false)
  assert.equal(router.pendingCount, 2)

  // 乱序响应：先回 req-2 再回 req-1
  assert.equal(
    router.handleResponseMessage({
      type: 'pdm-load-response',
      reqId: 'req-2',
      blob: new Blob(['img2'], { type: 'image/png' }),
      mime: 'image/png',
      size: 4,
    }),
    true
  )
  assert.equal(
    router.handleResponseMessage({
      type: 'pdm-load-response',
      reqId: 'req-1',
      blob: new Blob(['img1'], { type: 'image/jpeg' }),
      mime: 'image/jpeg',
      size: 4,
    }),
    true
  )

  const [r1, r2] = await Promise.all([p1, p2])
  assert.equal(r1.headers.get('Content-Type'), 'image/jpeg')
  assert.equal(r2.headers.get('Content-Type'), 'image/png')
  assert.equal(router.pendingCount, 0)
})
