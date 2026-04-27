import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';

/* ---- helpers ---- */

function formatBytes(bytes) {
  if (bytes == null || isNaN(bytes)) return '0 B';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const k = 1024;
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), units.length - 1);
  const val = bytes / Math.pow(k, i);
  return val.toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
}

function relativeTime(ts) {
  if (!ts) return '-';
  const now = Date.now();
  const t = typeof ts === 'number' ? ts : new Date(ts).getTime();
  if (isNaN(t)) return '-';
  const diff = now - t;
  const seconds = Math.floor(diff / 1000);
  if (seconds < 5) return 'just now';
  if (seconds < 60) return seconds + 's ago';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return minutes + 'm ago';
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return hours + 'h ago';
  const days = Math.floor(hours / 24);
  return days + 'd ago';
}

function formatDuration(startTs) {
  if (!startTs) return '-';
  const t = typeof startTs === 'number' ? startTs : new Date(startTs).getTime();
  if (isNaN(t)) return '-';
  const diff = Date.now() - t;
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return seconds + 's';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return minutes + 'm ' + (seconds % 60) + 's';
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return hours + 'h ' + (minutes % 60) + 'm';
  const days = Math.floor(hours / 24);
  return days + 'd ' + (hours % 24) + 'h';
}

function truncatePeerId(pid) {
  if (!pid) return '-';
  if (pid.length <= 12) return pid;
  return pid.substring(0, 4) + pid.substring(pid.length - 4);
}

const TRANSPORT_COLORS = {
  tcp:    'bg-blue-900/50 text-blue-300',
  quic:   'bg-emerald-900/50 text-emerald-300',
  ws:     'bg-purple-900/50 text-purple-300',
  wss:    'bg-purple-900/50 text-purple-300',
  webrtc: 'bg-amber-900/50 text-amber-300',
  webtransport: 'bg-cyan-900/50 text-cyan-300',
};

function transportBadge(t) {
  const key = t.toLowerCase();
  const cls = TRANSPORT_COLORS[key] || 'bg-zinc-800 text-zinc-400';
  return (
    <span key={t} className={`inline-block text-[10px] px-1.5 py-0.5 rounded font-mono ${cls}`}>
      {t.toUpperCase()}
    </span>
  );
}

/* ---- component ---- */

export default function PeerDetailPanel() {
  const [peers, setPeers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [sortKey, setSortKey] = useState('first_seen');
  const [sortDir, setSortDir] = useState('desc');

  const fetchPeers = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.getPeersDetail();
      setPeers(Array.isArray(data) ? data : []);
    } catch (err) {
      setError(err.message || 'Failed to load peer details');
      setPeers([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchPeers();
    const t = setInterval(fetchPeers, 5000);
    return () => clearInterval(t);
  }, [fetchPeers]);

  const handleSort = (key) => {
    if (sortKey === key) {
      setSortDir(prev => prev === 'asc' ? 'desc' : 'asc');
    } else {
      setSortKey(key);
      setSortDir('asc');
    }
  };

  const sortedPeers = [...peers].sort((a, b) => {
    let va, vb;
    switch (sortKey) {
      case 'peer_id':
        va = (a.peer_id || '').toLowerCase();
        vb = (b.peer_id || '').toLowerCase();
        break;
      case 'direction':
        va = a.direction || '';
        vb = b.direction || '';
        break;
      case 'first_seen':
        va = a.first_seen || 0;
        vb = b.first_seen || 0;
        break;
      case 'last_seen':
        va = a.last_seen || 0;
        vb = b.last_seen || 0;
        break;
      case 'bytes_sent':
        va = a.bytes_sent || 0;
        vb = b.bytes_sent || 0;
        break;
      case 'bytes_recv':
        va = a.bytes_recv || 0;
        vb = b.bytes_recv || 0;
        break;
      case 'latency':
        va = a.latency || a.rtt || 0;
        vb = b.latency || b.rtt || 0;
        break;
      default:
        va = a[sortKey] || 0;
        vb = b[sortKey] || 0;
    }
    if (typeof va === 'string') {
      return sortDir === 'asc' ? va.localeCompare(vb) : vb.localeCompare(va);
    }
    return sortDir === 'asc' ? va - vb : vb - va;
  });

  const SortIcon = ({ active, dir }) => (
    <span className="inline-block ml-1 text-[10px] text-zinc-500">
      {active ? (dir === 'asc' ? '▲' : '▼') : '▴'}
    </span>
  );

  const Th = ({ sort, children, className = '' }) => (
    <th
      className={`text-left text-[10px] font-medium text-zinc-500 px-2 py-2 cursor-pointer hover:text-zinc-300 select-none whitespace-nowrap ${className}`}
      onClick={() => handleSort(sort)}
    >
      {children}
      {sort && <SortIcon active={sortKey === sort} dir={sortDir} />}
    </th>
  );

  if (error) {
    return (
      <div className="bg-zinc-900/50 border border-zinc-800 rounded-lg p-4">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-medium text-zinc-300">Peers Detail</h3>
          <button onClick={fetchPeers}
            className="text-xs px-3 py-1.5 rounded border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors">
            Retry
          </button>
        </div>
        <div className="bg-red-900/20 border border-red-900/40 text-red-400 p-3 rounded-lg text-xs">
          {error}
        </div>
      </div>
    );
  }

  return (
    <div className="bg-zinc-900/50 border border-zinc-800 rounded-lg p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-medium text-zinc-300">
          Peers Detail
          <span className="text-zinc-500 font-normal ml-2">{peers.length} peers</span>
        </h3>
        <button onClick={fetchPeers} disabled={loading}
          className="text-xs px-3 py-1.5 rounded border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors disabled:opacity-40">
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>

      {loading && peers.length === 0 ? (
        <div className="flex items-center justify-center py-12">
          <div className="w-5 h-5 border-2 border-zinc-600 border-t-zinc-300 rounded-full animate-spin" />
        </div>
      ) : peers.length === 0 ? (
        <div className="py-8 text-center">
          <p className="text-zinc-600 text-xs">No connected peers</p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-zinc-800">
                <Th sort="peer_id">Peer ID</Th>
                <Th sort="direction">Dir</Th>
                <Th sort={null} className="cursor-default">Addresses</Th>
                <Th sort="first_seen">First Seen</Th>
                <Th sort="last_seen">Last Seen</Th>
                <Th sort="bytes_sent">Sent</Th>
                <Th sort="bytes_recv">Recv</Th>
                <Th sort={null} className="cursor-default">Transports</Th>
                <Th sort="latency">Latency</Th>
                <Th sort={null} className="cursor-default">Reg</Th>
                <Th sort={null} className="cursor-default">Duration</Th>
              </tr>
            </thead>
            <tbody>
              {sortedPeers.map((p, i) => {
                const pid = p.peer_id || p.id || '-';
                const dir = p.direction || 'inbound';
                const addrs = Array.isArray(p.addresses) ? p.addresses : Array.isArray(p.addrs) ? p.addrs : [];
                const transports = Array.isArray(p.transports) ? p.transports : [];
                const latency = p.latency || p.rtt;
                const regVerified = p.reg_verified || p.registration_verified || false;
                const regUsername = p.reg_username || p.registration_username || '';
                const durationStart = p.connection_start || p.connected_since || p.first_seen;

                return (
                  <tr key={p.peer_id || i}
                    className="border-b border-zinc-800/50 hover:bg-zinc-800/30 transition-colors">
                    {/* Peer ID */}
                    <td className="px-2 py-2.5">
                      <span className="font-mono text-zinc-300 text-[11px]" title={pid}>
                        {truncatePeerId(pid)}
                      </span>
                    </td>

                    {/* Direction */}
                    <td className="px-2 py-2.5">
                      <span className={`text-xs ${dir === 'outbound' ? 'text-blue-400' : 'text-amber-400'}`}>
                        {dir === 'outbound' ? '🔼' : '🔽'}
                      </span>
                    </td>

                    {/* Addresses */}
                    <td className="px-2 py-2.5 max-w-[160px]">
                      <span className="text-zinc-400 text-[10px] font-mono truncate block" title={addrs.join(', ')}>
                        {addrs.slice(0, 2).join(', ') || '-'}
                      </span>
                    </td>

                    {/* First Seen */}
                    <td className="px-2 py-2.5 text-zinc-400 text-[10px] whitespace-nowrap">
                      {relativeTime(p.first_seen)}
                    </td>

                    {/* Last Seen */}
                    <td className="px-2 py-2.5 text-zinc-400 text-[10px] whitespace-nowrap">
                      {relativeTime(p.last_seen)}
                    </td>

                    {/* Bytes Sent */}
                    <td className="px-2 py-2.5 text-zinc-300 text-[10px] font-mono whitespace-nowrap">
                      {formatBytes(p.bytes_sent)}
                    </td>

                    {/* Bytes Recv */}
                    <td className="px-2 py-2.5 text-zinc-300 text-[10px] font-mono whitespace-nowrap">
                      {formatBytes(p.bytes_recv)}
                    </td>

                    {/* Transports */}
                    <td className="px-2 py-2.5">
                      <div className="flex flex-wrap gap-1">
                        {transports.length > 0
                          ? transports.map(transportBadge)
                          : <span className="text-zinc-600 text-[10px]">-</span>
                        }
                      </div>
                    </td>

                    {/* Latency */}
                    <td className="px-2 py-2.5">
                      {latency != null ? (
                        <span className={`font-mono text-[11px] ${
                          latency < 50 ? 'text-emerald-400' :
                          latency < 150 ? 'text-yellow-400' :
                          'text-red-400'
                        }`}>
                          {latency}ms
                        </span>
                      ) : (
                        <span className="text-zinc-600">-</span>
                      )}
                    </td>

                    {/* Reg Verified + Username */}
                    <td className="px-2 py-2.5 whitespace-nowrap">
                      <span className="mr-1" title={regVerified ? 'Verified' : 'Not verified'}>
                        {regVerified
                          ? <span className="text-emerald-400 text-xs">✅</span>
                          : <span className="text-zinc-600 text-xs">❌</span>
                        }
                      </span>
                      {regVerified && regUsername && (
                        <span className="text-zinc-300 text-[10px] font-mono">{regUsername}</span>
                      )}
                    </td>

                    {/* Duration */}
                    <td className="px-2 py-2.5 text-zinc-400 text-[10px] font-mono whitespace-nowrap">
                      {formatDuration(durationStart)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* auto-refresh indicator */}
      <div className="mt-3 pt-3 border-t border-zinc-800 flex items-center justify-between">
        <div className="flex items-center gap-2 text-[10px] text-zinc-600">
          <span className={`w-1.5 h-1.5 rounded-full ${loading ? 'bg-emerald-400 animate-pulse' : 'bg-zinc-600'}`} />
          {loading ? 'Refreshing...' : `Last updated ${new Date().toLocaleTimeString()}`}
        </div>
        <span className="text-[10px] text-zinc-600">Auto-refresh every 5s</span>
      </div>
    </div>
  );
}
