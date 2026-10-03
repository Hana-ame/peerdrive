// ws.test.js — ws.js client unit tests: frame protocol reqId routing, admin responses,
// binary chunk attribution (admin-bin / data header+chunk), error semantics (status>=400 → err.status/err.data).
//
// Discovery context: ws.js is the core client for "migrating the frontend entirely to ws/peerjs"
// (api.js's request() all go through it). Frame routing correctness directly determines whether
// the page works — especially the "last binary declaration header" single-slot (binaryExpect) must
// be consistent with the backend's connection-level expect semantics.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import * as ws from '../src/ws'

// Construct a mock WebSocket: manually inject onmessage for direct frame feeding
function makeMockSock() {
  const sock = {
    readyState: WebSocket.OPEN,
    sent: [],
    _wsHandlers: false,
    send(data) { this.sent.push(data) },
    close() {},
  }
  return sock
}

// feedText simulates text frames sent from the server
function feedText(sock, msg) {
  sock.onmessage({ data: JSON.stringify(msg) })
}

describe('ws.js client', () => {
  beforeEach(() => {
    // _reset clears leftover pending: requests from the previous test that didn't resolve
    // (e.g. token tests) — if they linger in the map, a subsequent onclose will reject them
    // causing unhandled rejection
    ws.__test._reset()
    localStorage.clear()
  })

  it('admin requests route responses by reqId', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p1 = ws.admin('GET', '/files')
    const p2 = ws.admin('POST', '/collections', { name: 'x' })
    // Two requests have been sent
    expect(sock.sent.length).toBe(2)
    const f1 = JSON.parse(sock.sent[0])
    const f2 = JSON.parse(sock.sent[1])
    expect(f1.type).toBe('admin')
    expect(f2.reqId).not.toBe(f1.reqId)
    // Out-of-order response: reply to the second one first
    feedText(sock, { type: 'admin-resp', status: 200, body: { id: 'coll-x' }, reqId: f2.reqId })
    const r2 = await p2
    expect(r2).toEqual({ id: 'coll-x' })
    feedText(sock, { type: 'admin-resp', status: 200, body: { files: [] }, reqId: f1.reqId })
    const r1 = await p1
    expect(r1).toEqual({ files: [] })
  })

  it('admin 4xx response → reject Error(err.status/err.data) (409 conflict-list semantics)', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('POST', '/actions/merge', {})
    const f = JSON.parse(sock.sent[0])
    feedText(sock, {
      type: 'admin-resp', status: 409,
      body: { error: 'merge conflict', conflicts: [{ path: 'a.txt' }] },
      reqId: f.reqId,
    })
    await expect(p).rejects.toMatchObject({
      status: 409,
      data: { error: 'merge conflict', conflicts: [{ path: 'a.txt' }] },
    })
  })

  it('admin requests carry token (Authorization semantics)', async () => {
    localStorage.setItem('peerdrive_auth_token', 'tok-123')
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    ws.admin('GET', '/files')
    const f = JSON.parse(sock.sent[0])
    expect(f.token).toBe('tok-123')
  })

  it('download: data header + binary chunks collected per binaryExpect, done frame resolves', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.download('a'.repeat(64))
    const req = JSON.parse(sock.sent[0])
    expect(req.type).toBe('req')
    // Server replies: meta → data header + chunk1 → data header + chunk2 → done
    feedText(sock, { type: 'meta', total: 6, reqId: req.reqId })
    feedText(sock, { type: 'data', offset: 0, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([1, 2, 3]).buffer })
    feedText(sock, { type: 'data', offset: 3, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([4, 5, 6]).buffer })
    feedText(sock, { type: 'done', offset: 0, size: 6, reqId: req.reqId })
    const data = await p
    expect(Array.from(data)).toEqual([1, 2, 3, 4, 5, 6])
  })

  it('download: err frame rejects', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.download('b'.repeat(64))
    const req = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'err', msg: 'file not found', reqId: req.reqId })
    await expect(p).rejects.toThrow('file not found')
  })

  it('stat: req size=0 probes size, meta frame total resolves', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.stat('c'.repeat(64))
    const req = JSON.parse(sock.sent[0])
    expect(req.type).toBe('req')
    expect(req.offset).toBe(0)
    expect(req.size).toBe(0)
    // Server meta frame carries total (no data sent, directly done)
    feedText(sock, { type: 'meta', total: 4096, reqId: req.reqId })
    await expect(p).resolves.toBe(4096)
    // A late-arriving done frame must not have any effect (pending already deleted)
    feedText(sock, { type: 'done', offset: 0, size: 0, reqId: req.reqId })
  })

  it('downloadStream: chunks streamed as received, done frame closes stream', async () => {
    // Discovery context: code review 2026-08-18 — download assembles everything in memory,
    // OOM for large-file save/preview; downloadStream provides a stream-through path (for
    // FS Access API saving).
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const stream = ws.downloadStream('d'.repeat(64))
    const reader = stream.getReader()
    const req = JSON.parse(sock.sent[0])
    expect(req.type).toBe('req')
    // meta → data header + chunk1 → data header + chunk2 → done
    feedText(sock, { type: 'meta', total: 6, reqId: req.reqId })
    feedText(sock, { type: 'data', offset: 0, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([1, 2, 3]).buffer })
    const r1 = await reader.read()
    expect(Array.from(r1.value)).toEqual([1, 2, 3])
    expect(r1.done).toBe(false)
    feedText(sock, { type: 'data', offset: 3, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([4, 5, 6]).buffer })
    const r2 = await reader.read()
    expect(Array.from(r2.value)).toEqual([4, 5, 6])
    feedText(sock, { type: 'done', offset: 0, size: 6, reqId: req.reqId })
    const r3 = await reader.read()
    expect(r3.done).toBe(true)
  })

  it('downloadStream: err frame piped into stream (read throws)', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const stream = ws.downloadStream('e'.repeat(64))
    const reader = stream.getReader()
    const req = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'err', msg: 'peer fetch failed', reqId: req.reqId })
    await expect(reader.read()).rejects.toThrow('peer fetch failed')
  })

  it('downloadStream: consumer cancel clears pending and binaryExpect (prevents late-frame pollution)', async () => {
    // Discovery context: code review 2026-08-18 — abort/cancel without cleanup leaves pending
    // leaking, and late-arriving data frames continue writing into an abandoned stream.
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const stream = ws.downloadStream('f'.repeat(64))
    const reader = stream.getReader()
    const req = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'data', offset: 0, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([1, 2, 3]).buffer })
    // Consumer abandons
    await reader.cancel()
    expect(ws.__test.pending.has(req.reqId)).toBe(false)
    // Late frame arrives: binaryExpect already cleared, silently dropped without throwing
    sock.onmessage({ data: new Uint8Array([4, 5, 6]).buffer })
    feedText(sock, { type: 'done', offset: 0, size: 3, reqId: req.reqId })
  })

  it('admin-bin size=0 clears binaryExpect (prevents leftover single-slot from polluting subsequent binary frames)', async () => {
    // Discovery context: re-review (2026-08) — empty file/empty response admin-bin declarations
    // have no subsequent binary frames, old implementation didn't clear binaryExpect; if an
    // unrelated binary frame arrives later, it could be misattributed as data for this
    // already-completed request (pending already deleted, data silently discarded).
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('GET', '/empty-file')
    const f = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'admin-bin', status: 200, size: 0, reqId: f.reqId })
    const data = await p
    expect(data).toHaveLength(0)
    expect(ws.__test._binaryExpect()).toBeNull()
    // A subsequently arriving orphan binary frame must not be misattributed to a completed request
    sock.onmessage({ data: new Uint8Array([1]).buffer })
    expect(ws.__test._binaryExpect()).toBeNull()
  })

  it('admin-bin: binary file stream response collected as Uint8Array', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('GET', '/bt/download/abc/torrent')
    const f = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'admin-bin', status: 200, size: 4, reqId: f.reqId })
    sock.onmessage({ data: new Uint8Array([9, 8, 7, 6]).buffer })
    const data = await p
    expect(Array.from(data)).toEqual([9, 8, 7, 6])
  })

  it('connection closed → all pending reject', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('GET', '/files')
    // Attach catch before triggering onclose (to avoid reject firing before await attachment,
    // which would produce unhandled rejection)
    let caught = null
    p.catch(err => { caught = err })
    sock.onclose()
    await vi.waitFor(() => { expect(caught).toBeInstanceOf(Error) })
    expect(caught.message).toBe('ws: connection closed')
  })

  it('upload: declaration frame + binary chunks sliced per BIN_CHUNK for upload (FileReader fallback path)', async () => {
    // Discovery context (2026-08-18 code review): FileReader fallback branch referenced an
    // undefined constant BIN_CHUNK → ReferenceError causing upload failure (modern browsers
    // use the Streams API branch, so this didn't trigger in production). This test forces
    // the fallback branch (file has no stream() method + mock FileReader), verifying
    // declaration frame + 64KB chunked sending + admin-resp resolve.
    const CHUNK = 64 * 1024
    const total = CHUNK * 2 + 22 // 150KB → 3 chunks: 64KB + 64KB + 22KB
    const file = {
      name: 'big.bin',
      size: total,
      slice: (a, b) => new Uint8Array(Math.min(b, total) - a), // Real File.slice truncates to end of file; mock needs same behavior
    }
    // FileReader mock: readAsArrayBuffer triggers onload synchronously (real is async,
    // synchronous trigger has no effect on "chunk count/size" assertions)
    const reads = []
    class FakeFileReader {
      readAsArrayBuffer(slice) {
        reads.push(slice.length)
        this.result = slice // Real FileReader result is an ArrayBuffer; here we use slice directly
        this.onload({})
      }
    }
    vi.stubGlobal('FileReader', FakeFileReader)

    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.upload(file, 'big.bin')

    // Frame 1: declaration frame (admin binary)
    const decl = JSON.parse(sock.sent[0])
    expect(decl.type).toBe('admin')
    expect(decl.binary).toBe(true)
    expect(decl.filename).toBe('big.bin')
    expect(decl.size).toBe(total)
    expect(decl.field).toBe('file')
    expect(decl.path).toBe('/files/upload')
    // Subsequent frames: binary chunks (64KB × 2 + 22B)
    expect(sock.sent.length).toBe(4)
    expect(sock.sent[1].byteLength).toBe(CHUNK)
    expect(sock.sent[2].byteLength).toBe(CHUNK)
    expect(sock.sent[3].byteLength).toBe(22)
    // Server replies admin-resp → resolve
    feedText(sock, { type: 'admin-resp', status: 200, body: { hash: 'h' }, reqId: decl.reqId })
    await expect(p).resolves.toEqual({ hash: 'h' })

    vi.unstubAllGlobals()
  })
})

// Discovery context: code review 2026-08-19 — downloadToFile should fall back to the
// <a download> path when showSaveFilePicker throws SecurityError, rather than letting the
// finally block access an undefined writable causing ReferenceError.
describe('downloadToFile error handling', () => {
  it('should fallback to <a download> when showSaveFilePicker is not available', async () => {
    // Use mock socket to avoid real WS connection
    const sock = makeMockSock()
    ws.__test._setSock(sock)

    // Simulate showSaveFilePicker not existing, triggering the fallback path
    const orig = window.showSaveFilePicker
    delete window.showSaveFilePicker

    // downloadToFile will try showSaveFilePicker (not available),
    // falling back to download(hash) -> send req frame -> wait for response
    // We simulate the server returning an err frame to make download reject quickly
    const promise = ws.downloadToFile('testhash', 'testfile')

    // Extract reqId from sock.sent
    const sent = sock.sent[0]
    const req = JSON.parse(sent)
    // Send err frame to make download reject quickly
    feedText(sock, { type: 'err', msg: 'not found', reqId: req.reqId })

    await expect(promise).rejects.toThrow('not found')

    // Cleanup
    window.showSaveFilePicker = orig
    ws.__test._reset()
  })
})
