// CollectionBrowser + CollectionView tests — the folder-style collection display.
//
// Discovery context (2026-10, collection frontend batch): the feature's contract
// is "browse a collection manifest as folders, land on files, fetch by sha".
// These tests pin the three behaviors that would otherwise rot silently: folder
// navigation must actually change the listing (not just look like it), preview
// thumbnails must fetch through the same by-sha channel as file download, and
// Download must call ws.downloadToFile with (sha, basename) — the one line the
// whole feature reduces to. ws is mocked entirely: preview fetching uses
// ws.download, so a happy-dom image element is enough (no real network).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'

const { downloadMock, adminMock, downloadToFileMock } = vi.hoisted(() => ({
  downloadMock: vi.fn(),
  adminMock: vi.fn(),
  downloadToFileMock: vi.fn(),
}))
vi.mock('../src/ws.js', () => ({
  download: downloadMock,
  admin: adminMock,
  downloadToFile: downloadToFileMock,
}))

import CollectionBrowser from '../src/components/CollectionBrowser'
import CollectionView from '../src/pages/CollectionView'

const SHA = (c) => c.repeat(64)
const bytes = (s) => new TextEncoder().encode(s)

// 一个覆盖"目录 + 带/不带 preview 文件"的样例 collection
const sample = {
  version: 3,
  friendly_name: 'demo pack',
  entries: [
    { path: 'photos/a.jpg', sha: SHA('a'), preview: SHA('p'), size: 2048, mime_type: 'image/jpeg' },
    { path: 'photos/b.jpg', sha: SHA('b'), preview: '', size: 1024 },
    { path: 'docs/readme.md', sha: SHA('r'), size: 512 },
    { path: 'root.txt', sha: SHA('z') },
  ],
}

beforeEach(() => {
  downloadMock.mockReset()
  adminMock.mockReset()
  downloadToFileMock.mockReset()
  // 默认：preview 取数成功（返回 3 字节假图）
  downloadMock.mockResolvedValue(new Uint8Array([1, 2, 3]))
})

async function flush() {
  await act(async () => { await Promise.resolve() })
}

describe('CollectionBrowser folder navigation', () => {
  it('renders root level: dirs first, then files', () => {
    render(<CollectionBrowser collection={sample} />)
    expect(screen.getByText('photos')).toBeTruthy()
    expect(screen.getByText('docs')).toBeTruthy()
    expect(screen.getByText('root.txt')).toBeTruthy()
    // 目录行显示条目计数
    expect(screen.getByText('2 items')).toBeTruthy()
  })

  it('entering a folder shows its children with sizes; breadcrumb navigates back', async () => {
    render(<CollectionBrowser collection={sample} />)
    fireEvent.click(screen.getByText('photos'))
    await flush()
    expect(screen.getByText('a.jpg')).toBeTruthy()
    expect(screen.getByText('b.jpg')).toBeTruthy()
    // 大小格式化（fmtBytes 十进制：2048→2.05 KB, 1024→1.02 KB）
    // 注：a.jpg 有 mime，size 与 mime 在同一文本节点里（"2.05 KB · image/jpeg"），用正则匹配
    expect(screen.getByText(/2\.05 KB/)).toBeTruthy()
    expect(screen.getByText(/1\.02 KB/)).toBeTruthy()
    // 根目录的文件不应出现在子目录里
    expect(screen.queryByText('root.txt')).toBeNull()
    // 面包屑：Collection / photos，点 Collection 回根
    fireEvent.click(screen.getByText('Collection'))
    await flush()
    expect(screen.getByText('root.txt')).toBeTruthy()
  })

  it('empty collection shows the empty-folder placeholder', () => {
    render(<CollectionBrowser collection={{ entries: [] }} />)
    expect(screen.getByText('This folder is empty')).toBeTruthy()
  })
})

describe('CollectionBrowser previews and file actions', () => {
  it('file with preview fetches by sha and renders an image; without preview shows placeholder', async () => {
    render(<CollectionBrowser collection={sample} />)
    fireEvent.click(screen.getByText('photos'))
    await flush()
    await flush()
    // a.jpg 的 preview 按 sha 走与文件取数相同的通道
    expect(downloadMock).toHaveBeenCalledWith(SHA('p'))
    expect(document.querySelectorAll('img').length).toBeGreaterThanOrEqual(1)
    // b.jpg 无 preview → 占位
    expect(screen.getAllByLabelText('no preview').length).toBeGreaterThanOrEqual(1)
  })

  it('preview fetch failure falls back to placeholder instead of crashing', async () => {
    downloadMock.mockRejectedValueOnce(new Error('sha not local'))
    render(<CollectionBrowser collection={sample} />)
    fireEvent.click(screen.getByText('photos'))
    await flush()
    await flush()
    expect(screen.getAllByLabelText('no preview').length).toBeGreaterThanOrEqual(1)
  })

  it('Download calls ws.downloadToFile(sha, basename)', async () => {
    render(<CollectionBrowser collection={sample} />)
    // 根层只有一个文件行（root.txt）→ 只有一个 Download 按钮
    fireEvent.click(screen.getByText('Download'))
    await flush()
    expect(downloadToFileMock).toHaveBeenCalledWith(SHA('z'), 'root.txt')
  })

  it('Open on a preview-less file degrades to download', async () => {
    render(<CollectionBrowser collection={sample} />)
    fireEvent.click(screen.getByText('photos'))
    await flush()
    fireEvent.click(screen.getByText('Open'))
    await flush()
    expect(downloadToFileMock).toHaveBeenCalledWith(SHA('b'), 'b.jpg')
  })

  it('thumbnail click opens the preview modal; modal Download uses real-file sha', async () => {
    render(<CollectionBrowser collection={sample} />)
    fireEvent.click(screen.getByText('photos'))
    await flush()
    await flush()
    const img = document.querySelector('img')
    fireEvent.click(img)
    await flush()
    // 弹窗标题 = 文件名（文件行里也有同名文本，所以用 getAllByText 断言存在）
    expect(screen.getAllByText('a.jpg').length).toBeGreaterThanOrEqual(1)
    fireEvent.click(screen.getByText('Download file'))
    await flush()
    expect(downloadToFileMock).toHaveBeenCalledWith(SHA('a'), 'a.jpg')
    // 关闭弹窗
    fireEvent.click(screen.getByText('✕'))
    await flush()
    expect(screen.queryByText('Download file')).toBeNull()
  })
})

describe('CollectionView page', () => {
  const renderAt = (path) => render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/collection" element={<CollectionView />} />
        <Route path="/collection/:hash" element={<CollectionView />} />
      </Routes>
    </MemoryRouter>,
  )

  it('loads a collection by sha (content-addressed JSON) and browses it', async () => {
    downloadMock.mockResolvedValueOnce(bytes(JSON.stringify(sample)))
    renderAt('/collection/' + SHA('c'))
    await flush()
    expect(screen.getByText('demo pack')).toBeTruthy()
    expect(screen.getByText('4 entries')).toBeTruthy()
    expect(screen.getByText('photos')).toBeTruthy()
  })

  it('falls back to the admin endpoint when the sha does not decode as JSON', async () => {
    downloadMock.mockRejectedValueOnce(new Error('not local'))
    adminMock.mockResolvedValueOnce(sample)
    renderAt('/collection/' + SHA('c'))
    await flush()
    expect(adminMock).toHaveBeenCalledWith('GET', `/anon/collections/${SHA('c')}`)
    expect(screen.getByText('photos')).toBeTruthy()
  })

  it('pasted JSON enters browsing directly (no network)', async () => {
    renderAt('/collection')
    fireEvent.change(screen.getByPlaceholderText(/"friendly_name"/), { target: { value: JSON.stringify(sample) } })
    fireEvent.click(screen.getByText('Browse JSON'))
    await flush()
    expect(screen.getByText('demo pack')).toBeTruthy()
    expect(downloadMock).not.toHaveBeenCalled()
    expect(adminMock).not.toHaveBeenCalled()
  })

  it('invalid pasted JSON shows an error and stays on the input panel', () => {
    renderAt('/collection')
    fireEvent.change(screen.getByPlaceholderText(/"friendly_name"/), { target: { value: '{not json' } })
    fireEvent.click(screen.getByText('Browse JSON'))
    // Node 的 JSON.parse 对 "{not json" 报 "Expected property name..."（见 loadJson 的 catch）
    expect(screen.getByText(/Expected/i)).toBeTruthy()
  })
})
