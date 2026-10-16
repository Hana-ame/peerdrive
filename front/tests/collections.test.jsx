// collections.test.jsx — Collections.jsx permission separation unit tests (Issue #336).
//
// Discovery context (Issue #336): Visitors (connected via remote PeerJS WebRTC) must have
// strictly read-only access and cannot create collections. Local node owners (connected via WS)
// have permission to create collections. This test validates the permission separation.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import Collections from '../src/features/collection/pages/Collections'
import * as ws from '../src/platform/transport-ws'
import { setNodeSession, clearNodeSession } from '../src/lib/nodeSession'

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
    admin: vi.fn(),
    downloadToFile: vi.fn(),
    __setStatus: (s) => {
      status = s
      listeners.forEach((l) => l(s))
    },
  }
})

describe('Collections.jsx guest vs owner permission separation (Issue #336)', () => {
  beforeEach(() => {
    clearNodeSession()
    ws.__setStatus('open')
    vi.clearAllMocks()
  })

  it('Owner mode: shows "+ New Collection" in header and empty state action button', async () => {
    ws.admin.mockResolvedValue([])

    render(
      <MemoryRouter>
        <Collections />
      </MemoryRouter>
    )

    // Owner header shows enabled "New Collection" button
    await waitFor(() => {
      const btn = screen.getByRole('button', { name: /New Collection/i })
      expect(btn).toBeDefined()
      expect(btn.hasAttribute('disabled')).toBe(false)
    })

    // Visitor badge must not be visible
    expect(screen.queryByText(/Visitor \(Read Only\)/i)).toBeNull()

    // Empty state for owner invites creating a new collection
    expect(screen.getByText('No collections yet')).toBeDefined()
    expect(screen.getByText(/Click "New Collection" to create one/i)).toBeDefined()

    // Clicking New Collection switches tab to create mode
    const newBtn = screen.getAllByRole('button', { name: /New Collection/i })[0]
    fireEvent.click(newBtn)

    await waitFor(() => {
      expect(screen.getByText('Collection Name')).toBeDefined()
      expect(screen.getByRole('button', { name: /Back to List/i })).toBeDefined()
    })
  })

  it('Visitor mode: shows "Visitor (Read Only)" badge, disables "+ New Collection" (grayed out), read-only empty state', async () => {
    ws.__setStatus('closed')
    const mockClient = {
      shares: vi.fn().mockResolvedValue({ collections: [], files: [] }),
    }
    setNodeSession({ peerId: 'remote-node-1', client: mockClient })

    render(
      <MemoryRouter>
        <Collections />
      </MemoryRouter>
    )

    // Visitor badge is displayed
    await waitFor(() => {
      expect(screen.getByText(/Visitor \(Read Only\)/i)).toBeDefined()
    })

    // Header "+ New Collection" button MUST be present but disabled (grayed out)
    const newBtn = screen.getByRole('button', { name: /New Collection/i })
    expect(newBtn).toBeDefined()
    expect(newBtn.hasAttribute('disabled')).toBe(true)
    expect(newBtn.className).toContain('cursor-not-allowed')
    expect(newBtn.getAttribute('title')).toContain('Visitors cannot create collections')

    // Empty state shows read-only message without action button
    expect(screen.getByText('No shared collections')).toBeDefined()
    expect(screen.getByText(/This remote node has not shared any public collections/i)).toBeDefined()
    expect(screen.queryByRole('button', { name: /\+ New Collection/i })).toBeNull()
  })

  it('Visitor mode with local WS open: when browsing remote peer, button is disabled and grayed out', async () => {
    ws.__setStatus('open')
    const mockClient = {
      shares: vi.fn().mockResolvedValue({ collections: [], files: [] }),
    }
    setNodeSession({ peerId: 'remote-node-1', client: mockClient })

    render(
      <MemoryRouter>
        <Collections />
      </MemoryRouter>
    )

    // Visitor badge is displayed despite local WS being open
    await waitFor(() => {
      expect(screen.getByText(/Visitor \(Read Only\)/i)).toBeDefined()
    })

    const newBtn = screen.getByRole('button', { name: /New Collection/i })
    expect(newBtn).toBeDefined()
    expect(newBtn.hasAttribute('disabled')).toBe(true)
    expect(newBtn.className).toContain('cursor-not-allowed')
  })

  it('Visitor mode: loads and displays shared collections from remote client', async () => {
    ws.__setStatus('closed')
    const mockClient = {
      shares: vi.fn().mockResolvedValue({
        collections: [
          {
            name: 'Shared Album',
            hash: 'hash-col-1',
            entries: [{ path: 'pic.png', hash: 'h-1', mime: 'image/png' }],
            tags: ['photos'],
            access_policy: 'public',
          },
        ],
        files: [],
      }),
    }
    setNodeSession({ peerId: 'remote-node-1', client: mockClient })

    render(
      <MemoryRouter>
        <Collections />
      </MemoryRouter>
    )

    await waitFor(() => {
      expect(screen.getByText('Shared Album')).toBeDefined()
      expect(screen.getByText(/Visitor \(Read Only\)/i)).toBeDefined()
    })

    // Header "+ New Collection" is disabled
    const newBtn = screen.getByRole('button', { name: /New Collection/i })
    expect(newBtn.hasAttribute('disabled')).toBe(true)
  })
})
