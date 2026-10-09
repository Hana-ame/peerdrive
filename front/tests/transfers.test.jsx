// Transfers.jsx render tests — progress column + polling lifecycle.
//
// Discovery context (2026-10, benchmark B15): the transfers table is the only
// place users watch a cross-node pull go (or fail). The old table had no
// progress at all, and this batch added bar/pct/rate/ETA + failure tooltip +
// auto-refresh. The risks pinned here are behavioral, not visual: polling must
// run while a job is running and must STOP when everything is terminal (an
// always-on timer would hammer the admin plane forever from a background tab),
// and a failed job must surface PullJob.Error (otherwise "Failed" is a dead end
// — you can't tell sha256 mismatch from peer went offline).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'

const { adminMock } = vi.hoisted(() => ({ adminMock: vi.fn() }))
vi.mock('../src/ws.js', () => ({
  admin: adminMock,
}))
vi.mock('../src/platform/transport-ws', () => ({
  admin: adminMock,
}))

import Transfers from '../src/pages/Transfers'

const runningJob = (over = {}) => ({
  id: 'job-1',
  peer: 'peer-abc',
  hash: 'f'.repeat(64),
  name: 'big.iso',
  path: 'isos/big.iso',
  collection: 'coll1',
  total: 1000,
  received: 250,
  status: 'running',
  started_at: '2026-10-07T10:00:00Z',
  ...over,
})

beforeEach(() => {
  vi.useFakeTimers()
  adminMock.mockReset()
  adminMock.mockResolvedValue({ jobs: [] })
})

afterEach(() => {
  vi.useRealTimers()
})

// Flush microtasks so a resolved promise triggers the React render update.
async function flush() {
  await act(async () => { await Promise.resolve() })
}

// Advance fake timers by ms, then flush microtasks.
async function tick(ms) {
  await act(async () => { vi.advanceTimersByTime(ms) })
}

describe('Transfers page', () => {
  it('empty list renders the dashed placeholder', async () => {
    adminMock.mockResolvedValueOnce({ jobs: [] })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('No transfer tasks')).toBeTruthy()
  })

  it('running job shows progress percent and byte totals', async () => {
    adminMock.mockResolvedValueOnce({ jobs: [runningJob()] })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('big.iso')).toBeTruthy()
    // 250/1000 → 25%
    expect(screen.getByText('25%')).toBeTruthy()
    expect(screen.getByText(/250 B \/ 1\.00 KB/)).toBeTruthy()
  })

  it('resuming job shows Resuming badge, progress percent and byte totals without reset', async () => {
    adminMock.mockResolvedValueOnce({ jobs: [runningJob({ status: 'resuming', received: 400, total: 1000 })] })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('big.iso')).toBeTruthy()
    expect(screen.getByText('Resuming')).toBeTruthy()
    // 400/1000 → 40%
    expect(screen.getByText('40%')).toBeTruthy()
    expect(screen.getByText(/400 B \/ 1\.00 KB/)).toBeTruthy()
    expect(screen.getByText('Cancel')).toBeTruthy()
  })

  it('unknown size (total=-1) renders indeterminate bar + received only, no fake percent', async () => {
    adminMock.mockResolvedValueOnce({ jobs: [runningJob({ total: -1, received: 4000 })] })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('big.iso')).toBeTruthy()
    expect(screen.queryByText(/\d+%/)).toBeNull()
    expect(screen.getByText('4.00 KB…')).toBeTruthy()
  })

  it('failed job surfaces PullJob.Error text (failure tooltip), not just the badge', async () => {
    adminMock.mockResolvedValueOnce({
      jobs: [runningJob({ status: 'failed', error: 'sha256 mismatch after 3 attempts' })],
    })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('Failed')).toBeTruthy()
    const cell = screen.getByText('sha256 mismatch after 3 attempts')
    expect(cell).toBeTruthy()
    // hover title carries the full reason too (the inline copy truncates)
    expect(screen.getByTitle('sha256 mismatch after 3 attempts')).toBeTruthy()
  })

  it('done job shows saved_to path (where the file landed)', async () => {
    adminMock.mockResolvedValueOnce({
      jobs: [runningJob({ status: 'done', received: 1000, saved_to: '/dl/isos/big.iso' })],
    })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('Done')).toBeTruthy()
    expect(screen.getByText(/\/dl\/isos\/big\.iso/)).toBeTruthy()
  })

  it('polls while a job is running', async () => {
    adminMock.mockResolvedValue({ jobs: [runningJob()] })
    render(<Transfers />)
    await flush()
    expect(adminMock).toHaveBeenCalledTimes(1)
    await tick(2000)
    expect(adminMock).toHaveBeenCalledTimes(2)
    await tick(2000)
    expect(adminMock).toHaveBeenCalledTimes(3)
  })

  it('stops polling once no job is running (idle page must not hammer the admin plane)', async () => {
    adminMock.mockResolvedValue({ jobs: [runningJob()] })
    render(<Transfers />)
    await flush()
    expect(adminMock).toHaveBeenCalledTimes(1)
    await tick(2000) // load 2 — still running
    expect(adminMock).toHaveBeenCalledTimes(2)

    // backend now reports everything terminal
    adminMock.mockResolvedValue({ jobs: [runningJob({ status: 'done', received: 1000 })] })
    await tick(2000) // load 3 sees done → hasRunning false → interval cleared
    expect(adminMock).toHaveBeenCalledTimes(3)

    const frozen = adminMock.mock.calls.length
    await tick(10000)
    expect(adminMock.mock.calls.length).toBe(frozen)
  })

  it('admin error keeps the last list visible and shows the reason', async () => {
    adminMock.mockResolvedValueOnce({ jobs: [runningJob()] })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('big.iso')).toBeTruthy()

    adminMock.mockRejectedValueOnce(Object.assign(new Error('ws: connection closed')))
    await tick(2000) // poll fires and fails
    await flush()
    expect(screen.getByText(/connection closed/)).toBeTruthy()
    // the row is still there (stale-but-visible beats blanked)
    expect(screen.getByText('big.iso')).toBeTruthy()
  })

  it('rate appears once a second poll shows movement', async () => {
    // 4000 B over the 2000ms fake-timer interval → 2000 B/s on the first measurable tick
    adminMock.mockResolvedValueOnce({ jobs: [runningJob({ received: 0 })] })
    render(<Transfers />)
    await flush()
    expect(screen.getByText('big.iso')).toBeTruthy()

    adminMock.mockResolvedValueOnce({ jobs: [runningJob({ received: 4000 })] })
    await tick(2000)
    await flush()
    expect(screen.getByText('2.00 KB/s')).toBeTruthy()
  })
})
