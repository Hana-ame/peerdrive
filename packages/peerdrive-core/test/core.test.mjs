import { describe, it } from 'node:test'
import assert from 'node:assert'
import { FrameDispatcher, parseFrame, isBinaryFrame, nextReqId } from '../src/index.js'

describe('peerdrive-core', () => {
  it('parses valid JSON control frames', () => {
    assert.deepStrictEqual(parseFrame('{"type":"req","reqId":"123"}'), { type: 'req', reqId: '123' })
    assert.strictEqual(parseFrame('not json'), null)
    assert.strictEqual(parseFrame('{"noType":"ok"}'), null)
  })

  it('detects binary frames correctly', () => {
    assert.strictEqual(isBinaryFrame('text'), false)
    assert.strictEqual(isBinaryFrame(new Uint8Array(10)), true)
    assert.strictEqual(isBinaryFrame(new ArrayBuffer(10)), true)
  })

  it('generates unique request IDs', () => {
    const id1 = nextReqId()
    const id2 = nextReqId()
    assert.notStrictEqual(id1, id2)
    assert.ok(id1.length > 5)
  })

  it('routes contiguous data header and binary payload via connection expect', () => {
    const dispatcher = new FrameDispatcher()
    let receivedHeader = null
    let receivedChunk = null

    dispatcher.register('req-1', {
      onDataHeader: (h) => { receivedHeader = h },
      onDataChunk: (chunk) => { receivedChunk = chunk },
    })

    // 1. Data header arrives
    const headerResult = dispatcher.handleIncoming(JSON.stringify({
      type: 'data',
      reqId: 'req-1',
      offset: 0,
      size: 4,
    }))
    assert.strictEqual(headerResult, true)
    assert.strictEqual(receivedHeader.reqId, 'req-1')

    // 2. Binary chunk arrives immediately after
    const chunkData = new Uint8Array([1, 2, 3, 4])
    const chunkResult = dispatcher.handleIncoming(chunkData)
    assert.strictEqual(chunkResult, true)
    assert.deepStrictEqual(receivedChunk, chunkData)

    // 3. Second orphan binary chunk without expect is dropped
    assert.strictEqual(dispatcher.handleIncoming(chunkData), false)
  })

  it('closes all pending requests on connection teardown', () => {
    const dispatcher = new FrameDispatcher()
    let errorSeen = null

    dispatcher.register('req-2', {
      onError: (err) => { errorSeen = err },
    })

    dispatcher.closeAll(new Error('peer disconnected'))
    assert.ok(errorSeen)
    assert.strictEqual(errorSeen.message, 'peer disconnected')
    assert.strictEqual(dispatcher.has('req-2'), false)
  })
})
