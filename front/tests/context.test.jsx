import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import React from 'react'

const { onStatusMock, adminMock, getStatusMock } = vi.hoisted(() => ({
  onStatusMock: vi.fn(),
  adminMock: vi.fn(),
  getStatusMock: vi.fn(() => 'idle'),
}))

vi.mock('../src/platform/transport-ws/status', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    getStatus: getStatusMock,
    onStatus: onStatusMock,
  }
})

vi.mock('../src/platform/transport-ws', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    getStatus: getStatusMock,
    onStatus: onStatusMock,
    admin: adminMock,
  }
})

vi.mock('../src/ws.js', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...Object.fromEntries(Object.keys(actual).map((k) => [k, typeof actual[k] === 'function' ? vi.fn() : actual[k]])),
    getStatus: getStatusMock,
    onStatus: onStatusMock,
    admin: adminMock,
  }
})

import { AppProvider, useNodeState, useTransferState, useAppContext } from '../src/context/AppContext'

beforeEach(() => {
  getStatusMock.mockReturnValue('idle')
  onStatusMock.mockReset()
  adminMock.mockReset()
  onStatusMock.mockImplementation((cb) => {
    cb('idle')
    return () => {}
  })
})

function emit(s) {
  getStatusMock.mockReturnValue(s)
  const setter = onStatusMock.mock.calls[0][0]
  act(() => { setter(s) })
}

function ConsumerComponent() {
  const { status, nodeInfo, authInfo } = useNodeState()
  const { jobs, activeJobs } = useTransferState()
  return (
    <div>
      <div data-testid="status">{status}</div>
      <div data-testid="node-id">{nodeInfo?.id || 'none'}</div>
      <div data-testid="auth-user">{authInfo?.username || 'none'}</div>
      <div data-testid="job-count">{jobs ? jobs.length : 'none'}</div>
      <div data-testid="active-job-count">{activeJobs.length}</div>
    </div>
  )
}

describe('AppContext (Issue #217)', () => {
  it('provides unified node and transfer state through AppProvider', () => {
    render(
      <AppProvider>
        <ConsumerComponent />
      </AppProvider>
    )

    expect(screen.getByTestId('status').textContent).toBe('idle')
    expect(screen.getByTestId('node-id').textContent).toBe('none')
    expect(screen.getByTestId('auth-user').textContent).toBe('none')
  })

  it('updates node state on open event and clears on closed event', async () => {
    adminMock.mockImplementation((method, path) => {
      if (path === '/peerjs/node') {
        return Promise.resolve({ id: 'test-node-99' })
      }
      if (path === '/p2p/auth/status') {
        return Promise.resolve({ username: 'bob', authenticated: true })
      }
      return Promise.resolve({})
    })

    render(
      <AppProvider>
        <ConsumerComponent />
      </AppProvider>
    )

    await act(async () => {
      emit('open')
    })

    expect(screen.getByTestId('status').textContent).toBe('open')
    expect(screen.getByTestId('node-id').textContent).toBe('test-node-99')
    expect(screen.getByTestId('auth-user').textContent).toBe('bob')

    // Disconnect event clears node metadata
    act(() => {
      emit('closed')
    })

    expect(screen.getByTestId('status').textContent).toBe('closed')
    expect(screen.getByTestId('node-id').textContent).toBe('none')
  })

  it('filters activeJobs correctly from transfer state', async () => {
    adminMock.mockImplementation((method, path) => {
      if (path === '/p2p/pull') {
        return Promise.resolve({
          jobs: [
            { id: '1', status: 'running' },
            { id: '2', status: 'done' },
            { id: '3', status: 'resuming' },
            { id: '4', status: 'failed' },
          ],
        })
      }
      return Promise.resolve({})
    })

    function TransferConsumer() {
      const { jobs, activeJobs, refreshTransfers } = useTransferState()
      return (
        <div>
          <button data-testid="refresh" onClick={refreshTransfers}>Refresh</button>
          <div data-testid="jobs-len">{jobs?.length ?? 0}</div>
          <div data-testid="active-len">{activeJobs.length}</div>
        </div>
      )
    }

    onStatusMock.mockImplementation((cb) => {
      getStatusMock.mockReturnValue('open')
      cb('open')
      return () => {}
    })

    render(
      <AppProvider>
        <TransferConsumer />
      </AppProvider>
    )

    await act(async () => {
      screen.getByTestId('refresh').click()
    })

    expect(screen.getByTestId('jobs-len').textContent).toBe('4')
    expect(screen.getByTestId('active-len').textContent).toBe('2')
  })
})
