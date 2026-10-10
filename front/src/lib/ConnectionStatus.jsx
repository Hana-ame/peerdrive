// ConnectionStatus.jsx — live WS-session pill in the app nav.
//
// Why this exists (audit P7 / Issue #218): ws.js has exported onStatus() since the migration.
// Issue #218: displays not just binary connection status, but also node identity (peer ID),
// signaling server (host:port), and authentication status (username / operator).
// Event-driven: onStatus() triggers metadata fetch once on 'open', without continuous polling.
import React, { useEffect, useState, useRef } from 'react';
import { getStatus, onStatus } from '../platform/transport-ws/status';
import { admin } from '../platform/transport-ws';
import { onNodeSession, clearNodeSession } from './nodeSession';

// Map status → visual + copy. Unknown future states fall back to the neutral one
// rather than crashing the nav.
const STATE = {
  idle: { dot: 'bg-gray-500', text: 'text-gray-400', label: 'Not connected' },
  connecting: { dot: 'bg-yellow-400 animate-pulse', text: 'text-yellow-300', label: 'Connecting…' },
  open: { dot: 'bg-green-400', text: 'text-green-300', label: 'Connected' },
  closed: { dot: 'bg-red-400 animate-pulse', text: 'text-red-300', label: 'Reconnecting…' },
};

export default function ConnectionStatus() {
  const [status, setStatus] = useState(() => getStatus());
  const [nodeInfo, setNodeInfo] = useState(null);
  const [authInfo, setAuthInfo] = useState(null);
  const [peerSession, setPeerSession] = useState(null);
  const [showDetails, setShowDetails] = useState(false);
  const containerRef = useRef(null);

  useEffect(() => {
    const unsubSession = onNodeSession((s) => {
      setPeerSession(s);
    });
    return unsubSession;
  }, []);

  useEffect(() => {
    // onStatus fires immediately with the current state, then on every change;
    // the returned function unsubscribes (module-level Set — leak-free for remounts).
    const off = onStatus((s) => {
      setStatus(s);
      if (s === 'open') {
        admin('GET', '/peerjs/node')
          .then((data) => setNodeInfo(data))
          .catch(() => {});
        admin('GET', '/p2p/auth/status')
          .then((data) => setAuthInfo(data))
          .catch(() => {});
      } else {
        setNodeInfo(null);
        setAuthInfo(null);
        setShowDetails(false);
      }
    });
    return off;
  }, []);

  useEffect(() => {
    if (!showDetails) return;
    const handleClickOutside = (e) => {
      if (containerRef.current && !containerRef.current.contains(e.target)) {
        setShowDetails(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [showDetails]);

  // Connection state resolution: WS has highest fidelity if open, otherwise peerSession if active
  const isWsOpen = status === 'open';
  const isPeerOpen = Boolean(peerSession?.client && peerSession?.peerId);
  const isLive = isWsOpen || isPeerOpen;

  const cur = isWsOpen
    ? (STATE[status] || STATE.idle)
    : isPeerOpen
      ? { dot: 'bg-green-400', text: 'text-green-300', label: `Peer: ${peerSession.peerId.slice(0, 8)}` }
      : (STATE[status] || STATE.idle);

  const handleClick = (e) => {
    e.preventDefault();
    e.stopPropagation();
    if (isLive) {
      setShowDetails((prev) => !prev);
    } else {
      try {
        admin('GET', '/ping').catch(() => {});
      } catch {}
    }
  };

  const nodeLabel = isWsOpen && nodeInfo?.id
    ? `Connected (${nodeInfo.id.slice(0, 8)})`
    : isPeerOpen
      ? `Connected (Peer: ${peerSession.peerId.slice(0, 8)})`
      : cur.label;

  return (
    <div className="relative ml-auto shrink-0" ref={containerRef}>
      <button
        type="button"
        onClick={handleClick}
        title={isPeerOpen ? `Connected to remote peer: ${peerSession.peerId}` : `Local node session: ${status}`}
        className={`flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full border border-white/[0.08] bg-white/[0.04] ${cur.text} hover:bg-white/[0.08] transition-colors focus:outline-none`}
        data-testid="connection-status"
        data-status={isLive ? 'open' : status}
      >
        <span className={`w-2 h-2 rounded-full ${cur.dot}`} aria-hidden="true" />
        <span>{nodeLabel}</span>
      </button>

      {showDetails && isLive && (
        <div
          data-testid="connection-details"
          className="absolute right-0 top-full mt-2 w-72 p-3 rounded-lg shadow-xl border border-white/[0.12] bg-gray-900/95 backdrop-blur text-xs z-50 text-gray-200 space-y-2.5"
        >
          <div className="flex items-center justify-between border-b border-white/[0.08] pb-1.5 font-medium text-gray-300">
            <span>{isPeerOpen && !isWsOpen ? 'Remote Peer Details' : 'Connection Details'}</span>
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-500/20 text-green-300 font-mono">
              Live
            </span>
          </div>

          <div className="space-y-1">
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
              {isPeerOpen && !isWsOpen ? 'Remote Node' : 'Node Identity'}
            </div>
            <div className="flex items-center justify-between">
              <span className="text-gray-400">Peer ID:</span>
              <span
                data-testid="detail-node-id"
                className="font-mono text-[11px] text-gray-200 truncate max-w-[170px]"
                title={nodeInfo?.id || peerSession?.peerId || 'Unknown'}
              >
                {nodeInfo?.id || peerSession?.peerId || 'Unknown'}
              </span>
            </div>
            {isPeerOpen && peerSession?.myId && (
              <div className="flex items-center justify-between">
                <span className="text-gray-400">My Client ID:</span>
                <span className="font-mono text-[11px] text-gray-300 truncate max-w-[170px]" title={peerSession.myId}>
                  {peerSession.myId}
                </span>
              </div>
            )}
            {nodeInfo?.peers && (
              <div className="flex items-center justify-between">
                <span className="text-gray-400">Peers Online:</span>
                <span data-testid="detail-peer-count" className="font-mono text-gray-200">
                  {nodeInfo.peers.length}
                </span>
              </div>
            )}
          </div>

          {isWsOpen && (
            <>
              <div className="space-y-1 border-t border-white/[0.06] pt-1.5">
                <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">Signaling Server</div>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">Server:</span>
                  <span data-testid="detail-signal-server" className="font-mono text-[11px] text-gray-200 truncate max-w-[170px]">
                    {nodeInfo?.signal_host ? `${nodeInfo.signal_host}:${nodeInfo.signal_port || 443}` : 'Default cloud'}
                  </span>
                </div>
              </div>

              <div className="space-y-1 border-t border-white/[0.06] pt-1.5">
                <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">Account / Auth</div>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">User:</span>
                  <span data-testid="detail-username" className="font-mono text-gray-200 truncate max-w-[170px]">
                    {authInfo?.username || (authInfo?.authenticated ? 'Authenticated' : 'Guest')}
                  </span>
                </div>
                {authInfo?.operator && (
                  <div className="flex items-center justify-between">
                    <span className="text-gray-400">Operator:</span>
                    <span data-testid="detail-operator" className="font-mono text-gray-200 truncate max-w-[170px]">
                      {authInfo.operator}
                    </span>
                  </div>
                )}
              </div>
            </>
          )}

          {isPeerOpen && (
            <div className="border-t border-white/[0.06] pt-2 flex justify-end">
              <button
                type="button"
                onClick={() => {
                  clearNodeSession();
                  setShowDetails(false);
                }}
                className="px-2 py-1 text-[11px] bg-red-500/20 text-red-300 hover:bg-red-500/30 rounded border border-red-500/30 transition-colors"
              >
                Disconnect Peer
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
