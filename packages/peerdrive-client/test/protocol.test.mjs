// protocol.test.mjs：帧格式与纯函数契约。
// 这里断言的是**字段名与字面形状**——它们必须与 Go 侧 back/internal/transport
// 的 dcReq/dcResp/ShareSnapshot 逐字一致，改名字不会报错，只会让对端静默失败。

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'

import {
  MAX_FILE_BYTES,
  baseName,
  concatChunks,
  guessMime,
  isBinaryFrame,
  isValidHash,
  nextReqId,
  parseFrame,
  reqFrame,
  shareFrame,
  toUint8Array,
} from '../src/protocol.js'

const HASH = 'a'.repeat(64)

describe('protocol/reqFrame', () => {
  it('字段名与 Go 侧 dcReq 逐字一致', () => {
    const f = JSON.parse(reqFrame(HASH, { offset: 0, size: -1, reqId: 'r1' }))
    assert.deepEqual(f, { type: 'req', hash: HASH, offset: 0, size: -1, reqId: 'r1', v: 1 })
  })

  it('默认 offset=0 / size=-1（读到文件末尾）', () => {
    const f = JSON.parse(reqFrame(HASH, { reqId: 'r2' }))
    assert.equal(f.offset, 0)
    assert.equal(f.size, -1)
  })

  it('range 请求带上 offset/size', () => {
    const f = JSON.parse(reqFrame(HASH, { offset: 1024, size: 4096, reqId: 'r3' }))
    assert.equal(f.offset, 1024)
    assert.equal(f.size, 4096)
  })
})

describe('protocol/shareFrame', () => {
  it('share 帧只有 type/reqId（go 侧 dcReq 的 hash/size 留零值不影响解析）', () => {
    const f = JSON.parse(shareFrame('r9'))
    assert.equal(f.type, 'share')
    assert.equal(f.reqId, 'r9')
  })
})

describe('protocol/parseFrame', () => {
  it('合法控制帧原样解析', () => {
    assert.deepEqual(parseFrame('{"type":"meta","total":3}'), { type: 'meta', total: 3 })
  })

  it('非 JSON / 非对象 / 缺 type 都返回 null（同连接可能有别的用途的帧）', () => {
    assert.equal(parseFrame('hello'), null)
    assert.equal(parseFrame('[1,2]'), null)
    assert.equal(parseFrame('{"hash":"x"}'), null)
    assert.equal(parseFrame(null), null)
    assert.equal(parseFrame(undefined), null)
  })
})

describe('protocol/isBinaryFrame', () => {
  it('字符串是文本帧，ArrayBuffer/TypedArray/DataView 是数据块', () => {
    assert.equal(isBinaryFrame('{"type":"done"}'), false)
    assert.equal(isBinaryFrame(new ArrayBuffer(4)), true)
    assert.equal(isBinaryFrame(new Uint8Array(4)), true)
    assert.equal(isBinaryFrame(new DataView(new ArrayBuffer(4))), true)
  })

  it('Blob 也算二进制块（binaryType=blob 时）', () => {
    assert.equal(isBinaryFrame(new Blob([new Uint8Array(2)])), true)
  })
})

describe('protocol/toUint8Array', () => {
  it('保留 view 的 byteOffset/byteLength（不能把整个底层 buffer 带进来）', () => {
    const back = new Uint8Array([9, 9, 1, 2, 3, 9])
    const view = new Uint8Array(back.buffer, 2, 3)
    const out = toUint8Array(view)
    assert.deepEqual([...out], [1, 2, 3])
    const fromViewLike = toUint8Array(new DataView(back.buffer, 2, 3))
    assert.deepEqual([...fromViewLike], [1, 2, 3])
  })

  it('非法类型抛 TypeError', () => {
    assert.throws(() => toUint8Array({ not: 'bytes' }), TypeError)
  })
})

describe('protocol/concatChunks', () => {
  it('单块直接返回同一个对象（不做无谓复制）', () => {
    const only = new Uint8Array([1, 2, 3])
    assert.equal(concatChunks([only]), only)
  })

  it('多块按序拼接，total 未定时用实际长度', () => {
    const out = concatChunks([new Uint8Array([1, 2]), new Uint8Array([3])], -1)
    assert.deepEqual([...out], [1, 2, 3])
  })

  it('对端多发了字节时按声明长度截断（不产生超出 done 声明的长度）', () => {
    const out = concatChunks([new Uint8Array([1, 2, 3]), new Uint8Array([4, 5])], 3)
    assert.deepEqual([...out], [1, 2, 3])
  })

  it('空块列表 → 零长', () => {
    assert.equal(concatChunks([], -1).byteLength, 0)
  })
})

describe('protocol/isValidHash', () => {
  it('只接受 64 位小写 hex（与 Go 侧 IsStrictSHA256 同语义）', () => {
    assert.equal(isValidHash(HASH), true)
    assert.equal(isValidHash('0'.repeat(64)), true)
    // 大写：Go 侧会回 err 帧，本地必须提前拦（否则错误信息晚一个往返才出现）
    assert.equal(isValidHash('A'.repeat(64)), false)
    assert.equal(isValidHash('a'.repeat(63)), false)
    assert.equal(isValidHash('a'.repeat(65)), false)
    assert.equal(isValidHash('g'.repeat(64)), false)
    assert.equal(isValidHash(''), false)
    assert.equal(isValidHash(null), false)
  })
})

describe('protocol/nextReqId', () => {
  it('单调可用且不重复（reqId 是单连接内的响应路由键）', () => {
    const seen = new Set()
    for (let i = 0; i < 500; i++) seen.add(nextReqId())
    assert.equal(seen.size, 500)
  })
})

describe('protocol/guessMime + baseName', () => {
  it('按扩展名猜 MIME，未知回退', () => {
    assert.equal(guessMime('a.PNG'), 'image/png')
    assert.equal(guessMime('dir/b.mp4'), 'video/mp4')
    assert.equal(guessMime('c.weird'), 'application/octet-stream')
    assert.equal(guessMime(''), 'application/octet-stream')
  })

  it('baseName 取路径末段（对端给的是相对路径，可能带目录）', () => {
    assert.equal(baseName('docs/a/b.txt'), 'b.txt')
    assert.equal(baseName('C:\\x\\y.bin'), 'y.bin')
    assert.equal(baseName('plain.txt'), 'plain.txt')
    assert.equal(baseName(''), '')
  })
})

describe('protocol/常量', () => {
  it('协议上限与 Go 侧 maxPeerFetchSize 一致（8GB）', () => {
    assert.equal(MAX_FILE_BYTES, 8 * 1024 * 1024 * 1024)
  })
})
