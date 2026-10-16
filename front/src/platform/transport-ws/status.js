// status.js — Connection state machine for transport-ws.
//
// Why this exists: Separated from the frame routing core so UI components like
// ConnectionStatus can subscribe directly without dragging in the entire frame/data
// plane protocol.
//
// States:
//   idle       - initial state before any connection attempt
//   connecting - WebSocket connection in progress
//   open       - connection established and ready
//   closed     - connection dropped (auto-reconnect with backoff active)

let status = 'idle'
const statusListeners = new Set()

export function setStatus(s) {
  if (status === s) return
  status = s
  for (const cb of statusListeners) {
    try { cb(s) } catch {}
  }
}

export function getStatus() {
  return status
}

// onStatus subscribes to connection state changes; it immediately calls back once
// with the current state and returns an unsubscribe function.
export function onStatus(cb) {
  statusListeners.add(cb)
  try { cb(status) } catch {}
  return () => statusListeners.delete(cb)
}

export function resetStatus() {
  status = 'idle'
  statusListeners.clear()
}
