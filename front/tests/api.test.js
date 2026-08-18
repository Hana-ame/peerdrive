// api.test.js：getBlobUrl 预览路径测试（200MB 阈值 + in-flight 去重 + LRU）。
// 发现背景：2026-08-18 优化批次第 1/7 项——预览大文件 OOM 防护（TOO_LARGE）、
// 同 hash 并发双下载（blobUrlInflight）、缓存只增不减泄漏（LRU revoke）。
// 注意：tests/setup.js 全局 vi.mock('../src/api.js')（防组件测试发真请求），
// 本文件要测真实实现——doUnmock + 动态 import 解除（vitest per-file 隔离，
// 不影响其它测试文件）。

import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.doUnmock('../src/api.js')
vi.mock('../src/ws.js', () => ({
  stat: vi.fn(),
  download: vi.fn(),
  downloadToFile: vi.fn(),
}))

import * as ws from '../src/ws.js'
const api = await import('../src/api.js')

describe('getBlobUrl', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // happy-dom 的 URL.createObjectURL/revokeObjectURL 无稳定实现，替换为
    // 确定值（blob:size 编码内容长度，断言可比对）。
    URL.createObjectURL = vi.fn((b) => `blob:${b.size}`)
    URL.revokeObjectURL = vi.fn()
  })

  it('stat 超过 200MB 阈值 → 抛 err.code=TOO_LARGE，不触发 download', async () => {
    ws.stat.mockResolvedValue(200 * 1024 * 1024 + 1)
    await expect(api.getBlobUrl('a'.repeat(64))).rejects.toMatchObject({
      code: 'TOO_LARGE',
    })
    expect(ws.download).not.toHaveBeenCalled()
  })

  it('并发同 hash → stat/download 各只调一次（in-flight 去重）', async () => {
    let resolveStat
    ws.stat.mockImplementation(() => new Promise((r) => { resolveStat = r }))
    ws.download.mockResolvedValue(new Uint8Array(10))
    const p1 = api.getBlobUrl('b'.repeat(64))
    const p2 = api.getBlobUrl('b'.repeat(64))
    resolveStat(1024)
    const [u1, u2] = await Promise.all([p1, p2])
    expect(ws.stat).toHaveBeenCalledTimes(1)
    expect(ws.download).toHaveBeenCalledTimes(1)
    expect(u1).toBe(u2)
  })

  it('失败后 inflight 清除 → 下次调用重试', async () => {
    ws.stat
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(1024)
    ws.download.mockResolvedValue(new Uint8Array(5))
    await expect(api.getBlobUrl('c'.repeat(64))).rejects.toThrow('boom')
    const url = await api.getBlobUrl('c'.repeat(64))
    expect(url).toBe('blob:5')
  })

  it('缓存命中不再下载（且刷新 LRU 位置）', async () => {
    ws.stat.mockResolvedValue(10)
    ws.download.mockResolvedValue(new Uint8Array(3))
    const u1 = await api.getBlobUrl('d'.repeat(64))
    const u2 = await api.getBlobUrl('d'.repeat(64))
    expect(u1).toBe(u2)
    expect(ws.download).toHaveBeenCalledTimes(1)
    expect(ws.stat).toHaveBeenCalledTimes(1)
  })

  it('LRU 上限 50：超出淘汰最旧并 revoke，被淘汰项重新下载', async () => {
    // 模块级 blobUrlCache 跨测试共享（无清理导出），断言用相对基线——
    // 只验证本测试内的增量。
    const createdBase = URL.createObjectURL.mock.calls.length
    const revokedBase = URL.revokeObjectURL.mock.calls.length
    ws.stat.mockResolvedValue(10)
    ws.download.mockResolvedValue(new Uint8Array(1))
    for (let i = 0; i < 51; i++) {
      await api.getBlobUrl(String(i).padStart(64, '0'))
    }
    expect(URL.createObjectURL.mock.calls.length).toBe(createdBase + 51)
    expect(URL.revokeObjectURL.mock.calls.length).toBeGreaterThan(revokedBase)
    ws.download.mockClear()
    ws.stat.mockClear()
    // 循环第 1 个 hash（'00...0'）在本测试 51 次插入 + 历史遗留项下必然
    // 已被逐出缓存：再取触发重新下载
    await api.getBlobUrl('0'.repeat(64))
    expect(ws.download).toHaveBeenCalledTimes(1)
    expect(ws.stat).toHaveBeenCalledTimes(1)
  })
})