// ws.test.js — ws.js 客户端单元测试：帧协议 reqId 路由、admin 响应、
// 二进制块归属（admin-bin / data 头+块）、错误语义（status>=400 → err.status/err.data）。
//
// 发现背景：ws.js 是「前端全面迁移到 ws/peerjs」的核心客户端（api.js 的
// request() 全部走它）。帧路由正确性直接决定页面能否工作——特别是
// 「最近二进制声明头」单槽（binaryExpect）必须与后端连接级 expect 语义一致。

import { describe, it, expect, beforeEach, vi } from 'vitest'
import * as ws from '../src/ws'

// 构造模拟 WebSocket：手动注入 onmessage，便于直接喂帧
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

// feedText 模拟服务端发来的文本帧
function feedText(sock, msg) {
  sock.onmessage({ data: JSON.stringify(msg) })
}

describe('ws.js client', () => {
  beforeEach(() => {
    // _reset 清掉残留 pending：上一测试未 resolve 的请求（如 token 测试）
    // 若遗留在 map 里，后续 onclose 会连带 reject 产生 unhandled rejection
    ws.__test._reset()
    localStorage.clear()
  })

  it('admin 请求按 reqId 路由响应', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p1 = ws.admin('GET', '/files')
    const p2 = ws.admin('POST', '/collections', { name: 'x' })
    // 两个请求已发出
    expect(sock.sent.length).toBe(2)
    const f1 = JSON.parse(sock.sent[0])
    const f2 = JSON.parse(sock.sent[1])
    expect(f1.type).toBe('admin')
    expect(f2.reqId).not.toBe(f1.reqId)
    // 乱序响应：先回第二个
    feedText(sock, { type: 'admin-resp', status: 200, body: { id: 'coll-x' }, reqId: f2.reqId })
    const r2 = await p2
    expect(r2).toEqual({ id: 'coll-x' })
    feedText(sock, { type: 'admin-resp', status: 200, body: { files: [] }, reqId: f1.reqId })
    const r1 = await p1
    expect(r1).toEqual({ files: [] })
  })

  it('admin 4xx 响应 → reject Error(err.status/err.data)（409 冲突清单语义）', async () => {
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

  it('admin 请求携带 token（Authorization 语义）', async () => {
    localStorage.setItem('peerdrive_auth_token', 'tok-123')
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    ws.admin('GET', '/files')
    const f = JSON.parse(sock.sent[0])
    expect(f.token).toBe('tok-123')
  })

  it('download：data 头+二进制块按 binaryExpect 收集，done 帧 resolve', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.download('a'.repeat(64))
    const req = JSON.parse(sock.sent[0])
    expect(req.type).toBe('req')
    // 服务端回：meta → data 头 + 块1 → data 头 + 块2 → done
    feedText(sock, { type: 'meta', total: 6, reqId: req.reqId })
    feedText(sock, { type: 'data', offset: 0, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([1, 2, 3]).buffer })
    feedText(sock, { type: 'data', offset: 3, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([4, 5, 6]).buffer })
    feedText(sock, { type: 'done', offset: 0, size: 6, reqId: req.reqId })
    const data = await p
    expect(Array.from(data)).toEqual([1, 2, 3, 4, 5, 6])
  })

  it('download：err 帧 reject', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.download('b'.repeat(64))
    const req = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'err', msg: 'file not found', reqId: req.reqId })
    await expect(p).rejects.toThrow('file not found')
  })

  it('stat：req size=0 探大小，meta 帧 total resolve', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.stat('c'.repeat(64))
    const req = JSON.parse(sock.sent[0])
    expect(req.type).toBe('req')
    expect(req.offset).toBe(0)
    expect(req.size).toBe(0)
    // 服务端 meta 帧带 total（不发数据直接 done）
    feedText(sock, { type: 'meta', total: 4096, reqId: req.reqId })
    await expect(p).resolves.toBe(4096)
    // 迟到的 done 帧不得影响（pending 已删）
    feedText(sock, { type: 'done', offset: 0, size: 0, reqId: req.reqId })
  })

  it('downloadStream：块边收边吐，done 帧 close 流', async () => {
    // 发现背景：代码审阅 2026-08-18——download 全量内存组装，大文件
    // 保存/预览 OOM；downloadStream 提供边收边吐路径（FS Access API 保存用）。
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const stream = ws.downloadStream('d'.repeat(64))
    const reader = stream.getReader()
    const req = JSON.parse(sock.sent[0])
    expect(req.type).toBe('req')
    // meta → data 头 + 块1 → data 头 + 块2 → done
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

  it('downloadStream：err 帧灌进流（read 抛错）', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const stream = ws.downloadStream('e'.repeat(64))
    const reader = stream.getReader()
    const req = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'err', msg: 'peer fetch failed', reqId: req.reqId })
    await expect(reader.read()).rejects.toThrow('peer fetch failed')
  })

  it('downloadStream：消费者 cancel 清理 pending 与 binaryExpect（防迟到帧污染）', async () => {
    // 发现背景：代码审阅 2026-08-18——abort/cancel 不清理会留下 pending
    // 泄漏，且迟到 data 帧继续写入已放弃的流。
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const stream = ws.downloadStream('f'.repeat(64))
    const reader = stream.getReader()
    const req = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'data', offset: 0, size: 3, reqId: req.reqId })
    sock.onmessage({ data: new Uint8Array([1, 2, 3]).buffer })
    // 消费者放弃
    await reader.cancel()
    expect(ws.__test.pending.has(req.reqId)).toBe(false)
    // 迟到帧到达：binaryExpect 已清，静默丢弃不抛错
    sock.onmessage({ data: new Uint8Array([4, 5, 6]).buffer })
    feedText(sock, { type: 'done', offset: 0, size: 3, reqId: req.reqId })
  })

  it('admin-bin size=0 清空 binaryExpect（防残留单槽污染后续二进制帧）', async () => {
    // 发现背景：再 review（2026-08）——空文件/空响应的 admin-bin 声明没有
    // 后续二进制帧，旧实现未清 binaryExpect；万一后面来一个无关二进制帧，
    // 会被误判为这个已完成请求的数据块（pending 已删，数据被静默丢弃）。
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('GET', '/empty-file')
    const f = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'admin-bin', status: 200, size: 0, reqId: f.reqId })
    const data = await p
    expect(data).toHaveLength(0)
    expect(ws.__test._binaryExpect()).toBeNull()
    // 随后到达的无主二进制帧不得被误归到已完成请求
    sock.onmessage({ data: new Uint8Array([1]).buffer })
    expect(ws.__test._binaryExpect()).toBeNull()
  })

  it('admin-bin：二进制文件流响应收集为 Uint8Array', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('GET', '/bt/download/abc/torrent')
    const f = JSON.parse(sock.sent[0])
    feedText(sock, { type: 'admin-bin', status: 200, size: 4, reqId: f.reqId })
    sock.onmessage({ data: new Uint8Array([9, 8, 7, 6]).buffer })
    const data = await p
    expect(Array.from(data)).toEqual([9, 8, 7, 6])
  })

  it('连接关闭 → 全部 pending reject', async () => {
    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.admin('GET', '/files')
    // 先挂 catch 再触发 onclose（避免 reject 先于 await 附着产生 unhandled rejection）
    let caught = null
    p.catch(err => { caught = err })
    sock.onclose()
    await vi.waitFor(() => { expect(caught).toBeInstanceOf(Error) })
    expect(caught.message).toBe('ws: connection closed')
  })

  it('upload：声明帧 + 二进制块按 BIN_CHUNK 切片上传（FileReader 回退路径）', async () => {
    // 发现背景（2026-08-18 代码审阅）：FileReader 回退分支引用未定义常量
    // BIN_CHUNK → ReferenceError 上传直接失败（现代浏览器走 Streams API 分支
    // 所以线上未触发）。本测试强制走回退分支（file 无 stream() 方法 + mock
    // FileReader），验证声明帧 + 按 64KB 切片发送 + admin-resp resolve。
    const CHUNK = 64 * 1024
    const total = CHUNK * 2 + 22 // 150KB → 3 块：64KB + 64KB + 22KB
    const file = {
      name: 'big.bin',
      size: total,
      slice: (a, b) => new Uint8Array(Math.min(b, total) - a), // 真实 File.slice 会截到文件末尾，mock 需同样行为
    }
    // FileReader mock：readAsArrayBuffer 同步触发 onload（真实为异步，
    // 同步触发对「切块数量/大小」断言无影响）
    const reads = []
    class FakeFileReader {
      readAsArrayBuffer(slice) {
        reads.push(slice.length)
        this.result = slice // 真实 FileReader 的 result 是 ArrayBuffer，这里直接给 slice
        this.onload({})
      }
    }
    vi.stubGlobal('FileReader', FakeFileReader)

    const sock = makeMockSock()
    ws.__test._setSock(sock)
    const p = ws.upload(file, 'big.bin')

    // 第 1 帧：声明帧（admin binary）
    const decl = JSON.parse(sock.sent[0])
    expect(decl.type).toBe('admin')
    expect(decl.binary).toBe(true)
    expect(decl.filename).toBe('big.bin')
    expect(decl.size).toBe(total)
    expect(decl.field).toBe('file')
    expect(decl.path).toBe('/files/upload')
    // 后续帧：二进制块（64KB × 2 + 22B）
    expect(sock.sent.length).toBe(4)
    expect(sock.sent[1].byteLength).toBe(CHUNK)
    expect(sock.sent[2].byteLength).toBe(CHUNK)
    expect(sock.sent[3].byteLength).toBe(22)
    // 服务端回 admin-resp → resolve
    feedText(sock, { type: 'admin-resp', status: 200, body: { hash: 'h' }, reqId: decl.reqId })
    await expect(p).resolves.toEqual({ hash: 'h' })

    vi.unstubAllGlobals()
  })
})
