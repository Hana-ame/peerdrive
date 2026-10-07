// ConnectionStatus.jsx — live WS-session pill in the app nav.
//
// Why this exists (audit P7): ws.js has exported onStatus() since the migration,
// but nothing subscribed — the UI could not tell "connecting to the local node"
// apart from "the backend is down", so on a disconnect users saw buttons that did
// nothing, with no idea whether to wait or refresh (the very problem documented
// above ws.js:97). Syncthing's event-driven status (benchmark B14) is the model:
// subscribe to the state machine, render it, never poll for it.
//
// The pill reflects ws.js's own status machine: idle / connecting / open / closed.
// "closed" is not fatal — ws.js auto-reconnects with backoff — so the label says
// "Reconnecting…" to match reality, and the click affordance forces an immediate
// retry for users who won't wait out the backoff.
import React, { useEffect, useState } from 'react';
import * as ws from '../ws';

// Map status → visual + copy. Unknown future states fall back to the neutral one
// rather than crashing the nav.
const STATE = {
  idle: { dot: 'bg-gray-500', text: 'text-gray-400', label: 'Not connected' },
  connecting: { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-300', label: 'Connecting…' },
  open: { dot: 'bg-green-400', text: 'text-green-300', label: 'Connected' },
  closed: { dot: 'bg-red-400 animate-pulse', text: 'text-red-300', label: 'Reconnecting…' },
};

export default function ConnectionStatus() {
  const [status, setStatus] = useState(() => ws.getStatus());

  useEffect(() => {
    // onStatus fires immediately with the current state, then on every change;
    // the returned function unsubscribes (module-level Set — leak-free for remounts).
    const off = ws.onStatus(setStatus);
    return off;
  }, []);

  const cur = STATE[status] || STATE.idle;

  const retry = (e) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      // admin() lazily connects (ensureConnected semantics inside ws.js), so this
      // /ping probe — the lightest endpoint (controller/ping.go returns pong) —
      // both forces an immediate retry now and drives the pill back to
      // "connecting → Connected" without waiting for the next user action.
      ws.admin('GET', '/ping').catch(() => {});
    } catch {}
  };

  return (
    <button
      type="button"
      onClick={retry}
      title={`Local node session: ${status}${status === 'closed' ? ' (auto-retry with backoff; click to retry now)' : ''}`}
      className={`ml-auto shrink-0 flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full border border-white/[0.08] bg-white/[0.04] ${cur.text} hover:bg-white/[0.08] transition-colors`}
      data-testid="connection-status"
      data-status={status}
    >
      <span className={`w-2 h-2 rounded-full ${cur.dot}`} aria-hidden="true" />
      {cur.label}
    </button>
  );
}
