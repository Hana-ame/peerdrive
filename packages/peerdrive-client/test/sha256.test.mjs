// sha256.test.mjs：增量 SHA-256 的正确性。
// oracle 用 node:crypto（**不**用本包的 WebCrypto 分支），避免"两处同错"自证自洽。

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'

import { Sha256, sha256Hex } from '../src/sha256.js'
import { makeBytes } from './fake-connection.mjs'

const oracle = (bytes) => createHash('sha256').update(bytes).digest('hex')

describe('sha256/已知向量', () => {
  it('空内容与 NIST 标准串', async () => {
    assert.equal(new Sha256().digestHex(), 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')
    assert.equal(await sha256Hex(new Uint8Array(0)), oracle(new Uint8Array(0)))
    const abc = new TextEncoder().encode('abc')
    assert.equal(
      await sha256Hex(abc),
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    )
    const long = new TextEncoder().encode('abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq')
    assert.equal(
      new Sha256().update(long).digestHex(),
      '248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1',
    )
  })
})

describe('sha256/与 node:crypto 逐长度比对', () => {
  it('覆盖块边界 0..200 与 55/56/57/63/64/65/127/128/129', () => {
    const lens = new Set()
    for (let i = 0; i <= 200; i++) lens.add(i)
    for (const n of [255, 256, 257, 511, 512, 513, 1000, 4096, 65536, 65537]) lens.add(n)
    for (const n of lens) {
      const bytes = makeBytes(n, n + 7)
      assert.equal(new Sha256().update(bytes).digestHex(), oracle(bytes), `len=${n}`)
    }
  })

  it('一次性接口与 oracle 一致', async () => {
    for (const n of [0, 1, 63, 64, 65, 1000, 100000]) {
      const bytes = makeBytes(n, n + 3)
      assert.equal(await sha256Hex(bytes), oracle(bytes), `len=${n}`)
    }
  })
})

describe('sha256/增量 update', () => {
  it('任意切分方式结果一致（含把同一块切成 1 字节的极端情形）', () => {
    const bytes = makeBytes(1000, 42)
    const want = oracle(bytes)

    const whole = new Sha256().update(bytes).digestHex()
    assert.equal(whole, want)

    const parts = new Sha256()
    for (let i = 0; i < bytes.length; i += 7) parts.update(bytes.subarray(i, Math.min(i + 7, bytes.length)))
    assert.equal(parts.digestHex(), want)

    const byteByByte = new Sha256()
    for (let i = 0; i < 300; i++) byteByByte.update(bytes.subarray(i, i + 1))
    // 只喂了前 300 字节，应当等于前 300 字节的摘要（状态没有错位）
    assert.equal(byteByByte.digestHex(), oracle(bytes.subarray(0, 300)))
  })

  it('跨块边界切分（64 处切开）结果一致', () => {
    const bytes = makeBytes(300, 9)
    const a = new Sha256()
    a.update(bytes.subarray(0, 64))
    a.update(bytes.subarray(64))
    assert.equal(a.digestHex(), oracle(bytes))
  })

  it('接受 ArrayBuffer 与 DataView', () => {
    const bytes = makeBytes(100, 5)
    assert.equal(new Sha256().update(bytes.buffer).digestHex(), oracle(bytes))
    assert.equal(new Sha256().update(new DataView(bytes.buffer)).digestHex(), oracle(bytes))
  })

  it('update 返回 this，可链式', () => {
    const s = new Sha256()
    assert.equal(s.update(new Uint8Array([1])), s)
  })
})

describe('sha256/digestHex 幂等', () => {
  it('重复调用结果一致，且调用后还能继续 update（内部快照不留副作用）', () => {
    const s = new Sha256().update(new TextEncoder().encode('abc'))
    const a = s.digestHex()
    const b = s.digestHex()
    assert.equal(a, b)
    // 快照恢复后继续喂数据，应当等价于一次性喂 "abcd"
    const c = s.update(new TextEncoder().encode('d')).digestHex()
    assert.equal(c, oracle(new TextEncoder().encode('abcd')))
  })

  it('非字节输入抛 TypeError', () => {
    assert.throws(() => new Sha256().update('abc'), TypeError)
  })
})
