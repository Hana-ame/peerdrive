// thumbnail.test.jsx — FileThumbnail image and video cover tests (Issue #340).
//
// Discovery context (Issue #340): Video files in drive views were hardcoded to show
// static 🎬 icons without covers. FileThumbnail now supports both image and video
// media types, rendering rich covers and play indicators.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import FileThumbnail from '../src/components/netdisk/FileThumbnail'
import * as ws from '../src/platform/transport-ws'
import { setNodeSession, clearNodeSession } from '../src/lib/nodeSession'
import { previewBlobCache } from '../src/lib/cache'

vi.mock('../src/platform/transport-ws', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...actual,
    getStatus: vi.fn(() => 'open'),
    download: vi.fn(),
  }
})

describe('FileThumbnail (Issue #340)', () => {
  let originalCreateObjectURL
  let originalRevokeObjectURL

  beforeEach(() => {
    clearNodeSession()
    previewBlobCache.clear()
    vi.clearAllMocks()

    originalCreateObjectURL = URL.createObjectURL
    originalRevokeObjectURL = URL.revokeObjectURL
    URL.createObjectURL = vi.fn((blob) => `blob:mock-url-${Math.random()}`)
    URL.revokeObjectURL = vi.fn()
  })

  afterEach(() => {
    URL.createObjectURL = originalCreateObjectURL
    URL.revokeObjectURL = originalRevokeObjectURL
  })

  it('renders image thumbnail with <img> when given image file', async () => {
    const dummyBytes = new Uint8Array([1, 2, 3, 4])
    ws.download.mockResolvedValue(dummyBytes)

    render(
      <FileThumbnail
        sha="img-sha-123456"
        filename="scenery.jpg"
        mimeType="image/jpeg"
      />
    )

    await waitFor(() => {
      const img = screen.getByRole('img')
      expect(img).toBeDefined()
      expect(img.getAttribute('src')).toContain('blob:mock-url-')
      expect(img.getAttribute('alt')).toBe('scenery.jpg')
    })
  })

  it('renders video thumbnail with <video> and play badge when given video file', async () => {
    const dummyBytes = new Uint8Array([5, 6, 7, 8])
    ws.download.mockResolvedValue(dummyBytes)

    const { container } = render(
      <FileThumbnail
        sha="vid-sha-654321"
        filename="trailer.mp4"
        mimeType="video/mp4"
      />
    )

    await waitFor(() => {
      const video = container.querySelector('video')
      expect(video).toBeDefined()
      expect(video.getAttribute('src')).toBe('/sha256sum/vid-sha-654321/trailer.mp4')
      expect(video.hasAttribute('muted')).toBe(true)
      expect(video.hasAttribute('playsinline')).toBe(true)
      expect(screen.getByText('▶')).toBeDefined()
    })
  })

  it('renders fallback icon immediately for non-media files', () => {
    const { container } = render(
      <FileThumbnail
        sha="doc-sha-789"
        filename="notes.pdf"
        mimeType="application/pdf"
        fallbackIcon="📄"
      />
    )

    expect(screen.getByText('📄')).toBeDefined()
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('video')).toBeNull()
    expect(ws.download).not.toHaveBeenCalled()
  })

  it('supports remote peer session client streaming for media covers', async () => {
    async function* mockStream() {
      yield new Uint8Array([1, 2, 3])
      yield new Uint8Array([4, 5, 6])
    }
    const mockClient = {
      stream: vi.fn(() => mockStream()),
    }
    setNodeSession({ peerId: 'remote-peer-1', client: mockClient })

    render(
      <FileThumbnail
        sha="remote-vid-sha"
        filename="clip.webm"
        mimeType="video/webm"
      />
    )

    await waitFor(() => {
      expect(mockClient.stream).toHaveBeenCalledWith('remote-vid-sha')
    })
  })
})
