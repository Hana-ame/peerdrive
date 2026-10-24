// format.js unit tests — the display math behind the Transfers progress column.
//
// Discovery context (2026-10, benchmark B15): the transfers table gained
// progress/rate/ETA, and the RateTracker's delta logic (EMA blend, stall decay,
// negative-delta reset) is exactly the kind of thing that looks right by eyeball
// and is wrong at the boundaries — peerpull.go truncates .part with O_TRUNC on
// retry, so received can legitimately go backwards between polls; a tracker that
// averaged that in would show negative speeds until the row caught up.

import { describe, it, expect } from 'vitest'
import {
  fmtBytes, fmtRate, fmtEta, progressPct, estEtaSeconds, RateTracker,
} from '../src/lib/format'

describe('fmtBytes', () => {
  it('decimal units', () => {
    expect(fmtBytes(0)).toBe('0 B')
    expect(fmtBytes(999)).toBe('999 B')
    expect(fmtBytes(1000)).toBe('1.00 KB')
    expect(fmtBytes(1500)).toBe('1.50 KB')
    expect(fmtBytes(12_500)).toBe('12.5 KB')
    expect(fmtBytes(1_500_000)).toBe('1.50 MB')
    expect(fmtBytes(3_600_000_000)).toBe('3.60 GB')
  })
  it('invalid input → em dash (never a fake number)', () => {
    expect(fmtBytes(null)).toBe('—')
    expect(fmtBytes(undefined)).toBe('—')
    expect(fmtBytes(NaN)).toBe('—')
    expect(fmtBytes(-5)).toBe('—')
  })
})

describe('fmtRate', () => {
  it('renders per-second', () => {
    expect(fmtRate(1_250_000)).toBe('1.25 MB/s')
    expect(fmtRate(1000)).toBe('1.00 KB/s')
  })
  it('zero/unknown → em dash, not 0 B/s (0 means stalled, — means not measured)', () => {
    expect(fmtRate(0)).toBe('—')
    expect(fmtRate(null)).toBe('—')
    expect(fmtRate(-3)).toBe('—')
  })
})

describe('fmtEta', () => {
  it('human durations', () => {
    expect(fmtEta(0)).toBe('0s')
    expect(fmtEta(59)).toBe('59s')
    expect(fmtEta(60)).toBe('1m')
    expect(fmtEta(200)).toBe('3m 20s')
    expect(fmtEta(3600)).toBe('1h')
    expect(fmtEta(3661)).toBe('1h 1m')
  })
  it('unknown or absurd → em dash', () => {
    expect(fmtEta(null)).toBe('—')
    expect(fmtEta(-1)).toBe('—')
    expect(fmtEta(25 * 3600)).toBe('—') // >24h: not an ETA, noise
  })
})

describe('progressPct', () => {
  it('0-100 rounded', () => {
    expect(progressPct(0, 100)).toBe(0)
    expect(progressPct(50, 100)).toBe(50)
    expect(progressPct(99.6, 100)).toBe(100)
    expect(progressPct(300, 100)).toBe(100) // capped: overrun never shows >100
  })
  it('total<=0 (peer declared no size, PullJob.Total==-1) → null indeterminate', () => {
    expect(progressPct(123, -1)).toBeNull()
    expect(progressPct(123, 0)).toBeNull()
    expect(progressPct(123, null)).toBeNull()
  })
  it('negative/missing received reads as 0 (never negative width)', () => {
    expect(progressPct(-10, 100)).toBe(0)
    expect(progressPct(undefined, 100)).toBe(0)
  })
})

describe('estEtaSeconds', () => {
  it('remaining / rate', () => {
    expect(estEtaSeconds(1000, 500, 100)).toBe(5)
    expect(estEtaSeconds(1000, 1000, 100)).toBe(0)
  })
  it('unknown size or rate → null', () => {
    expect(estEtaSeconds(-1, 500, 100)).toBeNull()
    expect(estEtaSeconds(1000, 500, null)).toBeNull()
    expect(estEtaSeconds(1000, 500, 0)).toBeNull()
  })
})

describe('RateTracker', () => {
  const job = (id, received, status = 'running') => ({ id, received, status })

  it('first sight has no measurable rate (shows — until the second poll)', () => {
    const t = new RateTracker()
    const out = t.update([job('a', 100)], 1000)
    expect(out.get('a')).toBeNull()
  })

  it('second poll computes bytes/second from the delta', () => {
    const t = new RateTracker({ alpha: 1 }) // alpha=1 → pure instant rate
    t.update([job('a', 100)], 1000)
    const out = t.update([job('a', 300)], 1000) // +200B in 1s
    expect(out.get('a')).toBe(200)
  })

  it('EMA smooths spikes instead of showing raw instant rates', () => {
    const t = new RateTracker({ alpha: 0.5 })
    t.update([job('a', 0)], 1000)
    t.update([job('a', 100)], 1000) // inst 100, ema 100 (first)
    const out = t.update([job('a', 300)], 1000) // inst 200 → ema (100+200)/2
    expect(out.get('a')).toBe(150)
  })

  it('stall decays toward zero instead of freezing the old speed', () => {
    const t = new RateTracker({ alpha: 0.5 })
    t.update([job('a', 0)], 1000)
    t.update([job('a', 100)], 1000) // ema 100
    const out = t.update([job('a', 100)], 1000) // no movement
    expect(out.get('a')).toBe(50)
  })

  it('negative delta (.part truncated by a pull retry) resets the estimate', () => {
    const t = new RateTracker({ alpha: 1 })
    t.update([job('a', 0)], 1000)
    t.update([job('a', 500)], 1000) // 500 B/s
    const out = t.update([job('a', 50)], 1000) // retry truncated .part
    expect(out.get('a')).toBeNull()
    // and recovery: the next positive delta measures from the reset point
    const out2 = t.update([job('a', 250)], 1000)
    expect(out2.get('a')).toBe(200)
  })

  it('elapsed<=0 never divides by zero', () => {
    const t = new RateTracker()
    t.update([job('a', 10)], 1000)
    const out = t.update([job('a', 20)], 0)
    expect(out.get('a')).toBeNull()
  })

  it('ids that vanish from the list are forgotten (bounded map)', () => {
    const t = new RateTracker()
    t.update([job('a', 1), job('b', 1)], 1000)
    t.update([job('a', 2)], 1000)
    expect(t.prev.has('b')).toBe(false)
  })
})
