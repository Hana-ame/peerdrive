// ConnectionStatus.jsx — nav pill driven by ws.onStatus (audit P7).
//
// Discovery context: ws.js exported onStatus() but had zero subscribers, so the
// UI could not distinguish "connecting to the local node" from "backend down" —
// on disconnect users clicked dead buttons with no idea whether to wait or
// refresh. These tests pin the wiring: the pill must react to emitted status
// changes *without* polling, and must unsubscribe on unmount (statusListeners
// is a module-level Set — a leaked listener accumulates across route changes).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'

const { onStatusMock, adminMock } = vi.hoisted(() => ({
  onStatusMock: vi.fn(),
  adminMock: vi.fn(),
}))
vi.mock('../src/platform/transport-ws/status', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    getStatus: () => 'idle',
    onStatus: onStatusMock,
  }
})
vi.mock('../src/platform/transport-ws', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    getStatus: () => 'idle',
    onStatus: onStatusMock,
    admin: adminMock,
  }
})
vi.mock('../src/ws.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    getStatus: () => 'idle',
    onStatus: onStatusMock,
    admin: adminMock,
  }
})

import ConnectionStatus from '../src/lib/ConnectionStatus'

beforeEach(() => {
  onStatusMock.mockReset()
  adminMock.mockReset()
  adminMock.mockReturnValue(Promise.resolve({ pong: 'pong' }))
  // Fake the real contract: subscribe → callback fires immediately with current
  // state → returns an unsubscribe function.
  onStatusMock.mockImplementation((cb) => {
    cb('idle')
    return () => {}
  })
})

// Emit a status change through the captured listener, like ws.js setStatus() would.
// onStatusMock.mock.calls[0][0] is the setStatus callback passed by the component
function emit(s) {
  const setter = onStatusMock.mock.calls[0][0]
  act(() => { setter(s) })
}

describe('ConnectionStatus', () => {
  it('renders the initial status from the onStatus callback (no polling involved)', () => {
    render(<ConnectionStatus />)
    expect(onStatusMock).toHaveBeenCalledTimes(1)
    const pill = screen.getByTestId('connection-status')
    expect(pill.dataset.status).toBe('idle')
    expect(pill.textContent).toContain('Not connected')
  })

  it('reacts to every state transition: connecting/open/closed', () => {
    render(<ConnectionStatus />)
    emit('connecting')
    expect(screen.getByTestId('connection-status').textContent).toContain('Connecting…')
    emit('open')
    expect(screen.getByTestId('connection-status').textContent).toContain('Connected')
    emit('closed')
    // 'closed' must NOT say "Disconnected" — ws.js auto-reconnects with backoff,
    // and copy that implies permanence makes users refresh instead of wait.
    expect(screen.getByTestId('connection-status').textContent).toContain('Reconnecting…')
    expect(screen.getByTestId('connection-status').dataset.status).toBe('closed')
  })

  it('click pokes the connection via a /ping admin probe (immediate retry, no backoff wait)', () => {
    render(<ConnectionStatus />)
    fireEvent.click(screen.getByTestId('connection-status'))
    expect(adminMock).toHaveBeenCalledWith('GET', '/ping')
  })

  it('unsubscribes on unmount (module-level listener set must not accumulate)', () => {
    const unsub = vi.fn()
    onStatusMock.mockImplementation((cb) => { cb('idle'); return unsub })
    const { unmount } = render(<ConnectionStatus />)
    expect(unsub).not.toHaveBeenCalled()
    unmount()
    expect(unsub).toHaveBeenCalledTimes(1)
  })
})
