// api.test.js: getBlobUrl preview path tests (200MB threshold + in-flight dedup + LRU).
// Discovery context: 2026-08-18 optimization batch item 1/7 — OOM protection for large-file
// previews (TOO_LARGE), concurrent duplicate downloads for the same hash (blobUrlInflight),
// cache growing without bound causing leaks (LRU revoke).
// Note: tests/setup.js does a global vi.mock('../src/api.js') (to prevent component tests from
// sending real requests), this file needs to test the real implementation — doUnmock + dynamic
// import to un-mock (vitest per-file isolation, does not affect other test files).

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
    // happy-dom's URL.createObjectURL/revokeObjectURL has no stable implementation, so
    // replace with deterministic values (blob:size encodes content length for comparison).
    URL.createObjectURL = vi.fn((b) => `blob:${b.size}`)
    URL.revokeObjectURL = vi.fn()
  })

  it('stat exceeds 200MB threshold → throws err.code=TOO_LARGE, does not trigger download', async () => {
    ws.stat.mockResolvedValue(200 * 1024 * 1024 + 1)
    await expect(api.getBlobUrl('a'.repeat(64))).rejects.toMatchObject({
      code: 'TOO_LARGE',
    })
    expect(ws.download).not.toHaveBeenCalled()
  })

  it('concurrent same hash → stat/download each called only once (in-flight dedup)', async () => {
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

  it('inflight cleared after failure → next call retries', async () => {
    ws.stat
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(1024)
    ws.download.mockResolvedValue(new Uint8Array(5))
    await expect(api.getBlobUrl('c'.repeat(64))).rejects.toThrow('boom')
    const url = await api.getBlobUrl('c'.repeat(64))
    expect(url).toBe('blob:5')
  })

  it('cache hit does not download again (and refreshes LRU position)', async () => {
    ws.stat.mockResolvedValue(10)
    ws.download.mockResolvedValue(new Uint8Array(3))
    const u1 = await api.getBlobUrl('d'.repeat(64))
    const u2 = await api.getBlobUrl('d'.repeat(64))
    expect(u1).toBe(u2)
    expect(ws.download).toHaveBeenCalledTimes(1)
    expect(ws.stat).toHaveBeenCalledTimes(1)
  })

  it('LRU cap 50: evicts oldest and revokes on overflow, evicted item re-downloads', async () => {
    // Module-level blobUrlCache is shared across tests (no cleanup export), so assertions
    // use relative baselines — only verify increments within this test.
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
    // The first hash ('00...0') in the loop must have been evicted from cache given
    // 51 insertions in this test + any leftover from prior tests: re-fetch triggers a download
    await api.getBlobUrl('0'.repeat(64))
    expect(ws.download).toHaveBeenCalledTimes(1)
    expect(ws.stat).toHaveBeenCalledTimes(1)
  })
})
