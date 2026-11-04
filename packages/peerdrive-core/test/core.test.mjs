// core.test.mjs — Comprehensive modular & order-independence tests for peerdrive-core (Single Source of Truth).
import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  Sha256,
  sha256Hex,
  parseFrame,
  isBinaryFrame,
  toUint8Array,
  nextReqId,
  makeEnvelope,
  encodeBinaryHeader32,
  decodeBinaryHeader32,
  FrameDispatcher,
  buildFileUrl,
  buildCollectionUrl,
  parseDirectTarget,
  TransferProgressHub,
  TransportQueue,
  AsyncQueueLane,
} from '../src/index.js'

const SAMPLE_HASH = 'a'.repeat(64)
const COLL_HASH = 'b'.repeat(64)

describe('Module 1: peerdrive-core Protocol & 32B Self-Routing Binary Frames (#378 & #396)', () => {
  it('parses v1 JSON control frames and preserves exact structure', () => {
    const f = parseFrame('{"type":"meta","reqId":"123","total":100}')
    assert.deepEqual(f, { type: 'meta', reqId: '123', total: 100 })
    assert.equal(parseFrame('invalid json'), null)
    assert.equal(parseFrame('{"noType":1}'), null)
  })

  it('normalizes v2 unified envelopes {event, req_id, code, data} (#378 A4)', () => {
    const raw = makeEnvelope('file.meta', 'req-v2-1', { hash: SAMPLE_HASH, total: 4096 }, 0)
    const f = parseFrame(raw)
    assert.equal(f.type, 'meta')
    assert.equal(f.event, 'file.meta')
    assert.equal(f.reqId, 'req-v2-1')
    assert.equal(f.total, 4096)
    assert.equal(f.hash, SAMPLE_HASH)
  })

  it('encodes and decodes 32-byte binary frame headers (PD02) (#378 A7)', () => {
    const payload = new Uint8Array([10, 20, 30, 40, 50])
    const encoded = encodeBinaryHeader32('req-99', 65536, payload, 7)
    assert.equal(encoded.byteLength, 32 + 5)
    const decoded = decodeBinaryHeader32(encoded)
    assert.ok(decoded)
    assert.equal(decoded.reqId, 'req-99')
    assert.equal(decoded.offset, 65536)
    assert.equal(decoded.flags, 7)
    assert.deepEqual([...decoded.payload], [10, 20, 30, 40, 50])
  })

  it('detects binary frames and generates unique request IDs', () => {
    assert.equal(isBinaryFrame('text'), false)
    assert.equal(isBinaryFrame(new Uint8Array([1, 2, 3])), true)
    assert.equal(isBinaryFrame(new ArrayBuffer(4)), true)
    const id1 = nextReqId()
    const id2 = nextReqId()
    assert.notEqual(id1, id2)
  })

  it('routes contiguous v1 data header and binary payload via connection expect', () => {
    const d = new FrameDispatcher()
    let gotHeader = null
    let gotChunk = null

    d.register('r1', {
      onDataHeader: (f) => { gotHeader = f },
      onDataChunk: (c) => { gotChunk = c },
    })

    assert.equal(d.handleIncoming('{"type":"data","reqId":"r1","offset":0,"size":3}'), true)
    assert.equal(gotHeader.size, 3)

    assert.equal(d.handleIncoming(new Uint8Array([9, 8, 7])), true)
    assert.deepEqual(Array.from(gotChunk), [9, 8, 7])
  })

  it('ORDER-INDEPENDENCE: routes interleaved 32B binary chunks across concurrent requests without collision', () => {
    const d = new FrameDispatcher()
    const chunksA = []
    const chunksB = []

    d.register('reqA', {
      onDataChunk: (c, meta) => chunksA.push({ offset: meta.offset, data: [...c] }),
    })
    d.register('reqB', {
      onDataChunk: (c, meta) => chunksB.push({ offset: meta.offset, data: [...c] }),
    })

    // Feed chunks in completely interleaved, out-of-order sequence: B(off=2) -> A(off=0) -> B(off=0) -> A(off=3)
    d.handleIncoming(encodeBinaryHeader32('reqB', 2, new Uint8Array([22, 23])))
    d.handleIncoming(encodeBinaryHeader32('reqA', 0, new Uint8Array([1, 2, 3])))
    d.handleIncoming(encodeBinaryHeader32('reqB', 0, new Uint8Array([20, 21])))
    d.handleIncoming(encodeBinaryHeader32('reqA', 3, new Uint8Array([4, 5])))

    assert.deepEqual(chunksA, [
      { offset: 0, data: [1, 2, 3] },
      { offset: 3, data: [4, 5] },
    ])
    assert.deepEqual(chunksB, [
      { offset: 2, data: [22, 23] },
      { offset: 0, data: [20, 21] },
    ])
  })

  it('closes all pending requests on connection teardown', () => {
    const d = new FrameDispatcher()
    let err1 = null
    d.register('r1', { onError: (e) => { err1 = e } })
    d.closeAll(new Error('disconnected'))
    assert.equal(err1.message, 'disconnected')
    assert.equal(d.has('r1'), false)
  })
})

describe('Module 2: Canonical Self-Contained URLs for SHA256 Files & Collections', () => {
  it('builds self-contained SPA and HTTP URLs for SHA256 files', () => {
    const spaUrl = buildFileUrl(SAMPLE_HASH, {
      baseUrl: 'https://peerdrive.pages.dev/',
      peerId: 'node-alpha',
      filename: 'video.mp4',
      mode: 'spa',
    })
    assert.equal(
      spaUrl,
      `https://peerdrive.pages.dev#/f/${SAMPLE_HASH}?peer=node-alpha&name=video.mp4`
    )

    const httpUrl = buildFileUrl(SAMPLE_HASH, {
      baseUrl: 'http://127.0.0.1:3000',
      filename: 'video.mp4',
      mode: 'http',
    })
    assert.equal(httpUrl, `http://127.0.0.1:3000/f/${SAMPLE_HASH}/video.mp4`)
  })

  it('builds self-contained SPA and HTTP URLs for Collections', () => {
    const spaColl = buildCollectionUrl(COLL_HASH, {
      baseUrl: 'https://peerdrive.pages.dev',
      peerId: 'node-beta',
      name: 'Photos 2026',
      mode: 'spa',
    })
    assert.equal(
      spaColl,
      `https://peerdrive.pages.dev#/c/${COLL_HASH}?peer=node-beta&name=Photos+2026`
    )

    const httpColl = buildCollectionUrl(COLL_HASH, {
      baseUrl: 'http://127.0.0.1:3000',
      entryPath: 'sub/a.jpg',
      mode: 'http',
    })
    assert.equal(httpColl, `http://127.0.0.1:3000/c/${COLL_HASH}/sub/a.jpg`)
  })

  it('parses any direct URL, hash route, legacy route, panel link, or bare SHA256 accurately', () => {
    const t1 = parseDirectTarget(SAMPLE_HASH, 'file')
    assert.equal(t1.kind, 'file')
    assert.equal(t1.route, `/f/${SAMPLE_HASH}`)

    const t2 = parseDirectTarget(COLL_HASH, 'collection')
    assert.equal(t2.kind, 'collection')
    assert.equal(t2.route, `/c/${COLL_HASH}`)

    const t3 = parseDirectTarget(`https://peerdrive.pages.dev/#/f/${SAMPLE_HASH}?peer=node-1&name=doc.pdf`)
    assert.equal(t3.kind, 'file')
    assert.equal(t3.hash, SAMPLE_HASH)
    assert.equal(t3.peerId, 'node-1')
    assert.equal(t3.filename, 'doc.pdf')

    const t4 = parseDirectTarget(`http://127.0.0.1:3000/api/v1/c/${COLL_HASH}/folder/pic.png?peer=node-2`)
    assert.equal(t4.kind, 'collection')
    assert.equal(t4.hash, COLL_HASH)
    assert.equal(t4.peerId, 'node-2')
    assert.equal(t4.entryPath, 'folder/pic.png')

    const t5 = parseDirectTarget(`https://peerdrive.pages.dev/panel?node=peer-xyz&hash=${SAMPLE_HASH}&auto=1`)
    assert.equal(t5.kind, 'file')
    assert.equal(t5.hash, SAMPLE_HASH)
    assert.equal(t5.peerId, 'peer-xyz')

    // Malformed percent encoding and path traversal in entryPath must not throw or escape
    const t6 = parseDirectTarget(`http://127.0.0.1:3000/c/${COLL_HASH}/..%2F..%2Fsecret/%E0%A4%A`)
    assert.equal(t6.kind, 'collection')
    assert.equal(t6.entryPath, 'secret/%E0%A4%A')
  })
})

describe('Module 3: TransferProgressHub & Order-Independent TransportQueue', () => {
  it('tracks live percentage, EWMA transfer rate, completion, and cancellation in TransferProgressHub', () => {
    const hub = new TransferProgressHub()
    const snapshots = []
    const unsub = hub.subscribe((s) => snapshots.push(s.totalActive))

    const task = hub.createTask({
      kind: 'download',
      transport: 'peerjs',
      hash: SAMPLE_HASH,
      name: 'archive.zip',
      total: 1000,
      peerId: 'peer-1',
    })
    assert.equal(task.status, 'queued')
    assert.equal(task.pct, 0)

    hub.updateProgress(task.id, 250, 1000)
    assert.equal(hub.getTasks()[0].status, 'running')
    assert.equal(hub.getTasks()[0].pct, 25)

    hub.completeTask(task.id)
    assert.equal(hub.getTasks()[0].status, 'done')
    assert.equal(hub.getTasks()[0].pct, 100)
    assert.equal(hub.getSnapshot().totalActive, 0)
    assert.equal(hub.getSnapshot().totalCompleted, 1)

    unsub()
    assert.ok(snapshots.length >= 3)
  })

  it('ORDER-INDEPENDENCE: TransportQueue serializes single-lane tasks and isolates cancellations', async () => {
    const hub = new TransferProgressHub()
    const q = new TransportQueue({ progressHub: hub, wsConcurrency: 1, peerjsConcurrency: 1, httpConcurrency: 2 })
    const order = []

    const p1 = q.runTransfer({ kind: 'upload', transport: 'ws', laneKey: 'ws-Upload', name: 'f1.bin', total: 100 }, async ({ reportProgress }) => {
      order.push('start-1')
      reportProgress(50, 100)
      await new Promise((r) => setTimeout(r, 15))
      reportProgress(100, 100)
      order.push('end-1')
      return 'ok-1'
    })

    const ac2 = new AbortController()
    const p2 = q.runTransfer({ kind: 'upload', transport: 'ws', laneKey: 'ws-Upload', name: 'f2.bin', total: 100, signal: ac2.signal }, async () => {
      order.push('start-2')
      return 'ok-2'
    })

    const p3 = q.runTransfer({ kind: 'upload', transport: 'ws', laneKey: 'ws-Upload', name: 'f3.bin', total: 100 }, async ({ reportProgress }) => {
      order.push('start-3')
      reportProgress(100, 100)
      order.push('end-3')
      return 'ok-3'
    })

    // Cancel task 2 while task 1 is still in flight — task 3 must execute cleanly right after task 1!
    const p2Rejects = assert.rejects(p2, /cancel-2|Aborted/)
    ac2.abort(new Error('cancel-2'))

    const [r1, , r3] = await Promise.all([p1, p2Rejects, p3])

    assert.equal(r1, 'ok-1')
    assert.equal(r3, 'ok-3')
    assert.deepEqual(order, ['start-1', 'end-1', 'start-3', 'end-3'])
    const tasks = hub.getTasks()
    assert.equal(tasks[1].status, 'cancelled')
  })

  it('respects priority ordering in AsyncQueueLane without corrupting active execution', async () => {
    const lane = new AsyncQueueLane(1)
    const seq = []

    const t1 = lane.enqueue(async () => {
      await new Promise((r) => setTimeout(r, 10))
      seq.push('t1')
    }, { priority: 1 })

    const t2 = lane.enqueue(async () => {
      seq.push('t2-low')
    }, { priority: 1 })

    const t3 = lane.enqueue(async () => {
      seq.push('t3-high')
    }, { priority: 10 })

    await Promise.all([t1, t2, t3])
    assert.deepEqual(seq, ['t1', 't3-high', 't2-low'])
  })

  it('Sha256 incremental and one-shot sha256Hex match across arbitrary chunk boundaries', async () => {
    const data = new TextEncoder().encode('Peerdrive v1.0 single source of truth test payload')
    const oneShot = await sha256Hex(data)
    const inc = new Sha256()
    for (let i = 0; i < data.length; i += 3) {
      inc.update(data.subarray(i, i + 3))
    }
    assert.equal(inc.digestHex(), oneShot)
  })
})
