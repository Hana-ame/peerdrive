// format.js — pure display helpers for the transfers table (progress / rate / ETA).
//
// Why a separate module: the byte/rate/ETA math is the only genuinely testable
// logic in the Transfers page (the rest is DOM). Keeping it pure means vitest can
// pin the edge cases (total=-1 unknown size, NaN rate, negative delta after a
// retried pull truncates .part) without a render. Discovery context: the old
// table had no progress column at all; these label conventions follow
// rclone-web's TransfersTable (`{progress}%` + speed + ETA, benchmark B15).

// fmtBytes renders a byte count with decimal (1000-based) units — the convention
// download managers use, so "12.5 MB/s" reads like a network speed.
export function fmtBytes(n) {
  if (n == null || !isFinite(n) || n < 0) return '—'
  if (n < 1000) return `${Math.round(n)} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n
  let u = -1
  while (v >= 1000 && u < units.length - 1) { v /= 1000; u++ }
  // <10 keeps 2 decimals, above that 1 — roughly stabilizes column width
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[u]}`
}

// fmtRate renders bytes-per-second; null/NaN/≤0 (unknown) renders '—' rather than
// a fake "0 B/s" (0 implies stalled, — implies not-measured — different meanings
// the user can act on).
export function fmtRate(bps) {
  if (bps == null || !isFinite(bps) || bps <= 0) return '—'
  return `${fmtBytes(bps)}/s`
}

// fmtEta renders a remaining-time duration ("12s", "3m 20s", "2h 5m"). Absurdly
// far-out values (>24h: a stalled transfer with a tiny smoothed rate) collapse to
// '—' — an ETA nobody would wait for is noise, not information.
export function fmtEta(seconds) {
  if (seconds == null || !isFinite(seconds) || seconds < 0) return '—'
  const s = Math.round(seconds)
  if (s > 24 * 3600) return '—'
  if (s < 60) return `${s}s`
  if (s < 3600) {
    const m = Math.floor(s / 60)
    const r = s % 60
    return r ? `${m}m ${r}s` : `${m}m`
  }
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return m ? `${h}h ${m}m` : `${h}h`
}

// progressPct: 0-100 integer from received/total. total<=0 means the peer did not
// declare a size (backend PullJob.Total == -1, see peerpull.go) → null, and the
// caller renders an indeterminate (animated) bar instead of a percentage.
export function progressPct(received, total) {
  if (total == null || total <= 0) return null
  const r = received == null || !(received > 0) ? 0 : received
  return Math.min(100, Math.round((r / total) * 100))
}

// RateTracker smooths per-job transfer rates across successive poll snapshots.
//
// Why: a raw delta/interval is noisy (frame batching, heartbeat interleaving), and
// after a stall a naive "average since start" would keep showing a stale speed.
// EMA with decay: positive delta blends into the average, zero delta decays
// toward 0 (a frozen-looking line is worse than a falling one), negative delta
// resets — that happens when a pull retries and truncates .part (peerpull.go
// attempt loop uses O_TRUNC), so the old cumulative received is no longer
// comparable.
export class RateTracker {
  constructor({ alpha = 0.4 } = {}) {
    this.alpha = alpha
    this.prev = new Map() // id → { received, ema } — ema in bytes/tick-delta units
  }

  // update(jobs, elapsedMs) → Map id → smoothed bytes/second (or null when not
  // yet measurable). Call once per refresh with the actual time since the last
  // call; the caller measures elapsed because tests drive it with fake timers.
  update(jobs, elapsedMs) {
    const out = new Map()
    if (!Array.isArray(jobs)) return out
    const span = elapsedMs > 0 ? elapsedMs / 1000 : 0
    for (const j of jobs) {
      if (!j || !j.id) continue
      const received = Number(j.received) || 0
      const p = this.prev.get(j.id)
      let ema = p ? p.ema : null
      if (p) {
        const delta = received - p.received
        if (delta > 0 && span > 0) {
          const inst = delta / span
          ema = ema == null ? inst : ema * (1 - this.alpha) + inst * this.alpha
        } else if (delta === 0) {
          ema = ema == null ? null : Math.max(0, ema * (1 - this.alpha))
        } else {
          ema = null // negative delta: retry truncated .part, start over
        }
      }
      this.prev.set(j.id, { received, ema })
      out.set(j.id, ema)
    }
    // Forget vanished rows (job dropped from backend list) so the map stays bounded.
    for (const id of [...this.prev.keys()]) {
      if (!jobs.some((j) => j && j.id === id)) this.prev.delete(id)
    }
    return out
  }

  reset() { this.prev.clear() }
}

// estEtaSeconds: remaining seconds given total/received/current rate. Unknown
// inputs (no size declared, no measurable rate) → null.
export function estEtaSeconds(total, received, bps) {
  if (total == null || total <= 0) return null
  if (bps == null || !(bps > 0)) return null
  const rem = total - (received || 0)
  if (rem <= 0) return 0
  return rem / bps
}
