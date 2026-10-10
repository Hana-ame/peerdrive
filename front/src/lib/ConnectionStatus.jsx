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

  // 1. Signaling server state (Peersignal)
  // If local WS is open, signal host is from nodeInfo; if peerSession is active, from peerSession.signalHost
  const signalHost = nodeInfo?.signal_host
    ? `${nodeInfo.signal_host}${nodeInfo.signal_port ? `:${nodeInfo.signal_port}` : ''}`
    : (peerSession?.signalHost ? `${peerSession.signalHost}${peerSession.signalPort ? `:${peerSession.signalPort}` : ''}` : 'peersignal.moonchan.xyz');
  const isSignalLive = (status === 'open' && Boolean(nodeInfo?.id)) || Boolean(peerSession?.client);

  // 2. Node connection state (Local WS vs Remote WebRTC Peer)
  const isWsOpen = status === 'open';
  const isPeerOpen = Boolean(peerSession?.client && peerSession?.peerId);
  const isNodeLive = isWsOpen || isPeerOpen;

  // Authentication & Role Label
  // - WS: admin/user
  // - PeerJS: PSK验证 (if psk present and valid), or 游客 (Guest)
  let nodeRoleLabel = '未连接';
  let nodeBadgeColor = 'text-gray-400';
  let nodeDotColor = 'bg-gray-500';

  if (isWsOpen) {
    const user = authInfo?.username || (authInfo?.authenticated ? 'User' : 'WS 本地');
    nodeRoleLabel = `WS 本地 (${user})`;
    nodeBadgeColor = 'text-green-300';
    nodeDotColor = 'bg-green-400';
  } else if (isPeerOpen) {
    const peerShort = peerSession.peerId.slice(0, 8);
    const hasPsk = Boolean(peerSession.psk || peerSession.client?.psk);
    const authType = hasPsk ? 'PSK验证' : '游客';
    nodeRoleLabel = `Peer: ${peerShort} [${authType}]`;
    nodeBadgeColor = 'text-emerald-300';
    nodeDotColor = 'bg-emerald-400';
  } else if (status === 'connecting') {
    nodeRoleLabel = '连接中…';
    nodeBadgeColor = 'text-yellow-300';
    nodeDotColor = 'bg-yellow-400 animate-pulse';
  } else if (status === 'closed') {
    nodeRoleLabel = '重连中…';
    nodeBadgeColor = 'text-red-300';
    nodeDotColor = 'bg-red-400 animate-pulse';
  }

  const handleClick = (e) => {
    e.preventDefault();
    e.stopPropagation();
    if (isNodeLive || isSignalLive) {
      setShowDetails((prev) => !prev);
    } else {
      try {
        admin('GET', '/ping').catch(() => {});
      } catch {}
    }
  };

  return (
    <div className="relative ml-auto shrink-0 flex items-center gap-2" ref={containerRef}>
      {/* Pill ①: Peersignal 信令服务器状态 */}
      <button
        type="button"
        onClick={handleClick}
        title={`信令服务器: ${signalHost} (${isSignalLive ? '已连接' : '未连接'})`}
        className="hidden sm:flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full border border-white/[0.08] bg-white/[0.04] text-gray-300 hover:bg-white/[0.08] transition-colors focus:outline-none"
      >
        <span
          className={`w-2 h-2 rounded-full ${isSignalLive ? 'bg-sky-400' : 'bg-gray-500'}`}
          aria-hidden="true"
        />
        <span className="font-mono text-[11px]">
          信令: {signalHost.split(':')[0].split('.')[0]} {isSignalLive ? '✓' : '—'}
        </span>
      </button>

      {/* Pill ②: 节点连接状态 (WS 本地 / PeerJS PSK验证 / 游客) */}
      <button
        type="button"
        onClick={handleClick}
        title={isPeerOpen ? `WebRTC Node: ${peerSession.peerId}` : `Node session: ${status}`}
        className={`flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full border border-white/[0.08] bg-white/[0.04] ${nodeBadgeColor} hover:bg-white/[0.08] transition-colors focus:outline-none`}
        data-testid="connection-status"
        data-status={isNodeLive ? 'open' : status}
      >
        <span className={`w-2 h-2 rounded-full ${nodeDotColor}`} aria-hidden="true" />
        <span>{nodeRoleLabel}</span>
      </button>

      {/* 详细信息弹窗 */}
      {showDetails && (
        <div
          data-testid="connection-details"
          className="absolute right-0 top-full mt-2 w-80 p-3.5 rounded-lg shadow-xl border border-white/[0.12] bg-gray-900/95 backdrop-blur text-xs z-50 text-gray-200 space-y-3"
        >
          <div className="flex items-center justify-between border-b border-white/[0.08] pb-1.5 font-medium text-gray-300">
            <span>连接状态详情 (Dual-Channel)</span>
            <span className={`text-[10px] px-1.5 py-0.5 rounded font-mono ${isNodeLive ? 'bg-green-500/20 text-green-300' : 'bg-gray-700 text-gray-400'}`}>
              {isNodeLive ? 'Active' : 'Offline'}
            </span>
          </div>

          {/* 信令层信息 */}
          <div className="space-y-1">
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold flex items-center justify-between">
              <span>① 信令网络 (PeerSignal)</span>
              <span className={isSignalLive ? 'text-sky-300' : 'text-gray-500'}>
                {isSignalLive ? '● Connected' : '○ Standby'}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-gray-400">信令服务器:</span>
              <span
                data-testid="detail-signal-server"
                className="font-mono text-[11px] text-gray-200 truncate max-w-[170px]"
                title={signalHost}
              >
                {signalHost}
              </span>
            </div>
            {peerSession?.myId && (
              <div className="flex items-center justify-between">
                <span className="text-gray-400">本地客户端 ID:</span>
                <span className="font-mono text-[11px] text-gray-300 truncate max-w-[170px]" title={peerSession.myId}>
                  {peerSession.myId}
                </span>
              </div>
            )}
          </div>

          {/* 节点层信息 */}
          <div className="space-y-1 border-t border-white/[0.06] pt-2">
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold flex items-center justify-between">
              <span>② 存储节点 (Node Session)</span>
              <span className={isNodeLive ? 'text-emerald-300' : 'text-gray-500'}>
                {isWsOpen ? 'WS 本地' : isPeerOpen ? 'WebRTC 远端' : '未连接'}
              </span>
            </div>

            {isWsOpen && (
              <>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">模式:</span>
                  <span className="text-gray-200">WebSocket 管理面 (本地)</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">Node ID:</span>
                  <span
                    data-testid="detail-node-id"
                    className="font-mono text-[11px] text-gray-200 truncate max-w-[170px]"
                    title={nodeInfo?.id || 'Local'}
                  >
                    {nodeInfo?.id || 'Local'}
                  </span>
                </div>
                {nodeInfo?.peers && (
                  <div className="flex items-center justify-between">
                    <span className="text-gray-400">Peers Online:</span>
                    <span data-testid="detail-peer-count" className="font-mono text-gray-200">
                      {nodeInfo.peers.length}
                    </span>
                  </div>
                )}
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">身份认证:</span>
                  <span data-testid="detail-username" className="font-mono text-gray-200 truncate max-w-[170px]">
                    {authInfo?.username || (authInfo?.authenticated ? 'User' : '本地运营者')}
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
              </>
            )}

            {isPeerOpen && (
              <>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">模式:</span>
                  <span className="text-gray-200">WebRTC P2P DataChannel</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">对端 Peer ID:</span>
                  <span className="font-mono text-[11px] text-gray-200 truncate max-w-[170px]" title={peerSession.peerId}>
                    {peerSession.peerId}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-gray-400">准入身份:</span>
                  <span className={`px-1.5 py-0.5 rounded text-[11px] font-medium ${Boolean(peerSession.psk || peerSession.client?.psk) ? 'bg-emerald-500/20 text-emerald-300' : 'bg-amber-500/20 text-amber-300'}`}>
                    {Boolean(peerSession.psk || peerSession.client?.psk) ? 'PSK 预共享密钥已验证' : '游客访问 (Guest)'}
                  </span>
                </div>
              </>
            )}

            {!isNodeLive && (
              <p className="text-gray-500 text-[11px] py-1">
                未连接到任何本地或远程存储节点。可前往 Plaza 搜索并连接公网节点。
              </p>
            )}
          </div>

          {isPeerOpen && (
            <div className="border-t border-white/[0.06] pt-2 flex justify-end">
              <button
                type="button"
                onClick={() => {
                  clearNodeSession();
                  setShowDetails(false);
                }}
                className="px-2.5 py-1 text-[11px] bg-red-500/20 text-red-300 hover:bg-red-500/30 rounded border border-red-500/30 transition-colors"
              >
                断开节点连接 (Disconnect)
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
