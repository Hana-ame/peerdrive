// directfile.test.jsx — DirectFileView (#/f/:sha256) component tests (invariant #2, Issue #410).
//
// 发现背景：/f/ 后端 handler 与 #/f/ 前端页面在 v1.0-rc 修复时被切除（d2d2830，
// zip 源快照没写完），补回后钉住页面行为：取数按优先级 peerjs 会话 → WS downloadStream
// → 规范 HTTP /f/ 兜底（httpDownload）；文本按扩展名内联预览、非可预览类型给下载按钮、
// 全部传输失败给错误态。

import { describe, it, expect, vi, beforeEach } from 'vitest'
import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import DirectFileView from '../src/features/direct/pages/DirectFileView'
import { clearNodeSession } from '../src/lib/nodeSession'

vi.mock('../src/platform/transport-ws', async (importOriginal) => {
  const actual = await importOriginal()
  let status = 'open'
  const listeners = new Set()
  return {
    ...actual,
    getStatus: vi.fn(() => status),
    onStatus: vi.fn((cb) => {
      listeners.add(cb)
      cb(status)
      return () => listeners.delete(cb)
    }),
    downloadStream: vi.fn(),
    httpDownload: vi.fn(),
    admin: vi.fn(),
    downloadToFile: vi.fn(),
    __setStatus: (s) => {
      status = s
      listeners.forEach((l) => l(s))
    },
  }
})

const HASH = 'a'.repeat(64)
const enc = new TextEncoder()

function renderDirect(hash = HASH, query = '?name=note.txt') {
  return render(
    <MemoryRouter initialEntries={[`/f/${hash}${query}`]}>
      <Routes>
        <Route path="/f/:sha256" element={<DirectFileView />} />
      </Routes>
    </MemoryRouter>
  )
}

describe('DirectFileView (#/f/:sha256)', () => {
  beforeEach(() => {
    clearNodeSession()
    vi.clearAllMocks()
  })

  it('renders a text file pulled over the local WS channel', async () => {
    const ws = await import('../src/platform/transport-ws')
    const bytes = enc.encode('hello direct file')
    ws.downloadStream.mockReturnValue((async function* () {
      yield bytes
    })())

    renderDirect()

    await waitFor(() => {
      expect(screen.getByText('hello direct file')).toBeDefined()
    })
  })

  it('falls back to the canonical HTTP /f/ endpoint when WS is closed', async () => {
    const ws = await import('../src/platform/transport-ws')
    ws.__setStatus('closed')
    const bytes = enc.encode('fallback bytes')
    ws.httpDownload.mockResolvedValue(bytes)

    renderDirect()

    await waitFor(() => {
      expect(ws.httpDownload).toHaveBeenCalledWith(HASH, expect.any(Object))
      expect(screen.getByText('fallback bytes')).toBeDefined()
    })
  })

  it('shows a download affordance for non-previewable kinds', async () => {
    const ws = await import('../src/platform/transport-ws')
    const bytes = enc.encode('binary payload')
    ws.downloadStream.mockReturnValue((async function* () {
      yield bytes
    })())

    renderDirect(HASH, '?name=archive.zip')

    await waitFor(() => {
      expect(screen.getAllByText(/下载文件/).length).toBeGreaterThan(0)
    })
  })

  it('shows an error state when every transport fails', async () => {
    const ws = await import('../src/platform/transport-ws')
    ws.__setStatus('closed')
    ws.httpDownload.mockRejectedValue(new Error('HTTP 404 fetching /f/aaaa'))

    renderDirect()

    await waitFor(() => {
      expect(screen.getByText(/加载失败/)).toBeDefined()
    })
  })
})
