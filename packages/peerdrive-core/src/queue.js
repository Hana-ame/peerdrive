// queue.js — real-time transfer progress hub + order-independent transport queue
// (invariant #3 SSOT). Transfers.jsx / client.js / the panel all consume the two
// singleton instances exported at the bottom.
//
// Why order-independent: PeerJS, WS and HTTP downloads/upload on the same file
// hash can race; the queue guarantees per-lane serialization with priority, and
// a cancelled task never blocks the next one (the 2026-09 queueing-abort bug
// class this module exists to prevent — see REFACTOR §3.12).

let __id = 0
const nextId = () => `t${++__id}`

function nowMs() {
  return typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now()
}

const ACTIVE = new Set(['queued', 'running'])

/**
 * TransferProgressHub keeps a live task list with pct / EWMA rate and notifies
 * subscribers on every mutation (getSnapshot() feeds React state in Transfers).
 */
export class TransferProgressHub {
  constructor() {
    this._tasks = new Map() // id -> task
    this._subs = new Set()
    this._totalCompleted = 0
  }

  _notify() {
    for (const cb of this._subs) {
      try {
        cb(this.getSnapshot())
      } catch {
        // a broken subscriber must not corrupt hub state
      }
    }
  }

  _emit(task) {
    this._tasks.set(task.id, task)
    this._notify()
  }

  createTask(meta = {}) {
    const task = {
      id: nextId(),
      kind: meta.kind || 'download',
      transport: meta.transport || '',
      hash: meta.hash || '',
      name: meta.name || '',
      peerId: meta.peerId || '',
      total: typeof meta.total === 'number' ? meta.total : -1,
      received: 0,
      status: 'queued',
      pct: 0,
      error: '',
      rateBps: 0,
      startedAt: 0,
      endedAt: 0,
    }
    this._emit(task)
    return task
  }

  startTask(id, total) {
    const t = this._tasks.get(id)
    if (!t) return
    if (typeof total === 'number') t.total = total
    t.status = 'running'
    t.startedAt = nowMs()
    this._emit(t)
  }

  updateProgress(id, received, total) {
    const t = this._tasks.get(id)
    if (!t) return
    if (typeof total === 'number') t.total = total
    t.received = Math.max(0, received)
    if (t.status === 'queued') {
      t.status = 'running'
      if (!t.startedAt) t.startedAt = nowMs()
    }
    t.pct = t.total > 0 ? Math.min(100, Math.round((t.received / t.total) * 100)) : 0
    // EWMA transfer rate: instantaneous rate smoothed 70/30 so the progress bar
    // does not jitter on chunk-boundary bursts (format.js RateTracker semantics).
    const elapsed = nowMs() - (t.startedAt || nowMs())
    if (elapsed > 20 && t.received > 0) {
      const inst = (t.received / elapsed) * 1000
      t.rateBps = t.rateBps ? t.rateBps * 0.7 + inst * 0.3 : inst
    }
    this._emit(t)
  }

  completeTask(id, extra = {}) {
    const t = this._tasks.get(id)
    if (!t) return
    if (typeof extra.received === 'number') t.received = extra.received
    if (typeof extra.total === 'number') t.total = extra.total
    t.status = 'done'
    t.pct = 100
    t.endedAt = nowMs()
    this._totalCompleted += 1
    this._emit(t)
  }

  failTask(id, err) {
    const t = this._tasks.get(id)
    if (!t) return
    t.status = 'error'
    t.error = err ? String(err.message || err) : 'failed'
    t.endedAt = nowMs()
    this._emit(t)
  }

  cancelTask(id) {
    const t = this._tasks.get(id)
    if (!t || t.status === 'done') return
    t.status = 'cancelled'
    t.endedAt = nowMs()
    this._totalCompleted += 1
    this._emit(t)
  }

  clearCompleted() {
    for (const t of this._tasks.values()) {
      if (t.status === 'done' || t.status === 'error' || t.status === 'cancelled') {
        this._tasks.delete(t.id)
      }
    }
    this._totalCompleted = 0
    this._notify()
  }

  getTasks() {
    return [...this._tasks.values()]
  }

  getSnapshot() {
    const tasks = [...this._tasks.values()]
    return {
      totalActive: tasks.filter((t) => ACTIVE.has(t.status)).length,
      totalCompleted: this._totalCompleted,
      tasks,
    }
  }

  subscribe(cb) {
    this._subs.add(cb)
    cb(this.getSnapshot())
    return () => this._subs.delete(cb)
  }
}

/**
 * AsyncQueueLane serializes async work with a concurrency cap and priority
 * ordering: the highest-priority *waiting* task always runs next, while the
 * active task is never interrupted (test pins t1 -> t3-high -> t2-low).
 */
export class AsyncQueueLane {
  constructor(concurrency = 1) {
    this.limit = Math.max(1, concurrency)
    this.active = 0
    this._waiting = [] // { fn, priority, resolve, reject, signal, started }
  }

  enqueue(fn, { priority = 0, signal = null } = {}) {
    return new Promise((resolve, reject) => {
      const item = { fn, priority, resolve, reject, signal, started: false }
      if (signal && signal.aborted) {
        reject(signal.reason || new Error('Aborted'))
        return
      }
      if (signal) {
        // Abort while still queued: settle immediately and skip execution so a
        // cancelled task can never block the lane (order-independence).
        item.onAbort = () => {
          if (!item.started) {
            item.aborted = true
            reject(signal.reason || new Error('Aborted'))
            this._tick()
          }
        }
        signal.addEventListener('abort', item.onAbort, { once: true })
      }
      this._waiting.push(item)
      this._tick()
    })
  }

  _pick() {
    let best = -1
    let bestPriority = -Infinity
    for (let i = 0; i < this._waiting.length; i++) {
      const it = this._waiting[i]
      if (it.aborted) continue
      if (it.priority > bestPriority) {
        bestPriority = it.priority
        best = i
      }
    }
    return best >= 0 ? this._waiting.splice(best, 1)[0] : null
  }

  _tick() {
    while (this.active < this.limit) {
      const item = this._pick()
      if (!item) return
      if (item.aborted) continue // already rejected; keep draining
      this.active += 1
      item.started = true
      if (item.signal) item.signal.removeEventListener('abort', item.onAbort)
      // 槽位空闲时**同步**启动 fn：client.js 的 upload 走
      // uploadSerialLane.enqueue(() => sendOne())，测试在 upload() 返回后立即读
      // sock.sent[0]——若 fn 被压进微任务，第一帧还没发出去（ws.test.js 上传用例
      // 的同步断言正是靠这个时序）。异常与结果仍异步 settle。
      let settled = false
      const settle = (fn) => {
        if (settled) return
        settled = true
        this.active -= 1
        this._tick()
        fn()
      }
      let r
      try {
        r = item.fn()
      } catch (e) {
        settle(() => item.reject(e))
        continue
      }
      Promise.resolve(r).then(
        (v) => settle(() => item.resolve(v)),
        (e) => settle(() => item.reject(e))
      )
    }
  }
}

/**
 * TransportQueue runs transfers per-lane (serialized by laneKey) with a
 * per-transport concurrency cap, wiring progress into a TransferProgressHub and
 * isolating cancellations (abort never leaks into the next queued transfer).
 */
export class TransportQueue {
  constructor({ progressHub = null, wsConcurrency = 1, peerjsConcurrency = 1, httpConcurrency = 2 } = {}) {
    this.hub = progressHub || new TransferProgressHub()
    this._lanes = new Map() // laneKey -> AsyncQueueLane
    this._slots = { ws: wsConcurrency, peerjs: peerjsConcurrency, http: httpConcurrency }
    this._active = { ws: 0, peerjs: 0, http: 0 }
    this._waiting = [] // { transport, resolve, release } parkers for full transports
  }

  _lane(key) {
    let lane = this._lanes.get(key)
    if (!lane) {
      lane = new AsyncQueueLane(1) // per-lane serial: same lane never overlaps
      this._lanes.set(key, lane)
    }
    return lane
  }

  _drain(transport) {
    while (this._active[transport] < this._slots[transport] && this._waiting.length > 0) {
      const i = this._waiting.findIndex((w) => w.transport === transport)
      if (i < 0) return
      const w = this._waiting.splice(i, 1)[0]
      this._active[transport] += 1
      w.release()
    }
  }

  /**
   * acquire a per-transport slot. The returned release() decrements the slot
   * and wakes the next parker; waiters parking here never hold a lane.
   */
  _acquire(transport, signal) {
    return new Promise((resolve, reject) => {
      const release = () => {
        this._active[transport] -= 1
        this._drain(transport)
      }
      if (this._active[transport] < this._slots[transport]) {
        this._active[transport] += 1
        resolve(release)
        return
      }
      const waiter = {
        transport,
        release: () => resolve(release),
      }
      this._waiting.push(waiter)
      if (signal) {
        signal.addEventListener(
          'abort',
          () => {
            const i = this._waiting.indexOf(waiter)
            if (i >= 0) {
              this._waiting.splice(i, 1)
              reject(signal.reason || new Error('Aborted'))
            }
          },
          { once: true }
        )
      }
    })
  }

  runTransfer(taskMeta, workFn) {
    const task = this.hub.createTask(taskMeta)
    const laneKey = taskMeta.laneKey || `${task.transport || 'peerjs'}-${task.kind || 'download'}`
    const transport = this._slots[task.transport] !== undefined ? task.transport : 'peerjs'

    return new Promise((resolve, reject) => {
      const inLane = async () => {
        const doRelease = await this._acquire(transport, taskMeta.signal)
        try {
          const reportProgress = (received, total) => this.hub.updateProgress(task.id, received, total)
          const value = await workFn({ reportProgress })
          this.hub.completeTask(task.id)
          resolve(value)
        } catch (err) {
          this.hub.failTask(task.id, err)
          reject(err)
        } finally {
          doRelease()
        }
      }

      this._lane(laneKey)
        .enqueue(inLane, { priority: taskMeta.priority || 0, signal: taskMeta.signal })
        .catch((err) => {
          // Lane rejected: aborted while still queued — mark the hub task
          // cancelled (never blocking the next lane item) and settle the
          // transfer promise with the abort reason.
          if (task.status === 'queued') {
            this.hub.cancelTask(task.id)
          }
          reject(err)
        })
    })
  }
}

/** Global singletons consumed by the frontend (client.js / Transfers.jsx) via shared/index. */
export const globalProgressHub = new TransferProgressHub()
export const globalTransportQueue = new TransportQueue({
  wsConcurrency: 1,
  peerjsConcurrency: 2,
  httpConcurrency: 2,
})