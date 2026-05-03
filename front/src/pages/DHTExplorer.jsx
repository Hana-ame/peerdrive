import React, { useState } from 'react';
import * as api from '../api';

function peerBadge(p, i) {
  const addr = typeof p === 'string' ? p : (p.addr || p.address || p.id || JSON.stringify(p));
  return (
    <span key={i} className="bg-zinc-800 border border-zinc-700 rounded px-2 py-1 font-mono text-[11px] text-zinc-300">
      {addr}
    </span>
  );
}

export default function DHTExplorer() {
  const [hash, setHash] = useState('');
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');
  const [bep51, setBep51] = useState(null);
  const [bep51Loading, setBep51Loading] = useState(false);

  const doDualFind = async () => {
    if (!hash.trim()) return;
    setLoading(true);
    setError('');
    setResult(null);
    try {
      const r = await api.dualFind(hash.trim());
      setResult(r);
    } catch (e) {
      setError(e.message);
    }
    setLoading(false);
  };

  const doBep51 = async () => {
    setBep51Loading(true);
    try {
      const r = await api.getBEP51Sample();
      setBep51(r);
    } catch (e) {
      setError(e.message);
    }
    setBep51Loading(false);
  };

  return (
    <div className="max-w-6xl mx-auto px-4 py-6">
      <h1 className="text-xl font-bold text-gray-200 mb-6">DHT 查询</h1>

      {/* Query Input */}
      <div className="flex flex-wrap gap-3 mb-6">
        <input
          value={hash}
          onChange={e => setHash(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') doDualFind(); }}
          placeholder="SHA256 / InfoHash / CID..."
          className="flex-1 min-w-0 md:min-w-[300px] bg-gray-900 border border-gray-600 px-4 py-2.5 rounded text-sm text-gray-200 font-mono focus:outline-none focus:border-blue-500"
        />
        <button
          onClick={doDualFind}
          disabled={loading || !hash.trim()}
          className="bg-blue-600 hover:bg-blue-700 disabled:opacity-40 px-6 py-2.5 rounded text-sm font-medium transition-colors"
        >
          {loading ? '查询中...' : '双栈查询'}
        </button>
        <button
          onClick={doBep51}
          disabled={bep51Loading}
          className="bg-purple-600 hover:bg-purple-700 disabled:opacity-40 px-6 py-2.5 rounded text-sm font-medium transition-colors"
        >
          {bep51Loading ? '采样中...' : 'BEP51 采样'}
        </button>
      </div>

      {error && (
        <div className="bg-red-900/30 border border-red-700/50 rounded-lg px-4 py-3 text-sm text-red-300 mb-4">
          {error}
        </div>
      )}

      {/* Dual Find Results */}
      {result && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
          {/* IPFS DHT */}
          <div className="bg-gray-900 border border-gray-700 rounded-lg p-4">
            <h2 className="text-sm font-semibold text-gray-300 mb-3 flex items-center gap-2">
              <span>🌐</span> IPFS DHT
              <span className="text-xs text-gray-500 font-normal ml-auto">
                {result.ipfs_peers?.length ?? 0} peers
              </span>
            </h2>
            <div className="flex flex-wrap gap-2 max-h-64 overflow-y-auto">
              {result.ipfs_peers?.length > 0
                ? result.ipfs_peers.map(peerBadge)
                : <p className="text-xs text-gray-600">未找到 IPFS peers</p>
              }
            </div>
          </div>

          {/* BT DHT */}
          <div className="bg-gray-900 border border-gray-700 rounded-lg p-4">
            <h2 className="text-sm font-semibold text-gray-300 mb-3 flex items-center gap-2">
              <span>📡</span> BT DHT
              <span className="text-xs text-gray-500 font-normal ml-auto">
                {result.bt_peers?.length ?? 0} peers
              </span>
            </h2>
            <div className="flex flex-wrap gap-2 max-h-64 overflow-y-auto">
              {result.bt_peers?.length > 0
                ? result.bt_peers.map(peerBadge)
                : <p className="text-xs text-gray-600">未找到 BT peers</p>
              }
            </div>
          </div>
        </div>
      )}

      {/* BEP51 Sample Results */}
      {bep51 && (
        <div className="bg-gray-900 border border-gray-700 rounded-lg p-4">
          <h2 className="text-sm font-semibold text-gray-300 mb-3 flex items-center gap-2">
            <span>🔍</span> BEP51 Infohash 样本
            <span className="text-xs text-gray-500 font-normal">
              {bep51.infohashes?.length ?? bep51.samples?.length ?? Object.keys(bep51).length} 个 infohash
            </span>
          </h2>
          <div className="flex flex-wrap gap-2 max-h-80 overflow-y-auto">
            {(bep51.infohashes || bep51.samples || []).length > 0
              ? (bep51.infohashes || bep51.samples || []).map((h, i) => (
                  <span key={i} className="bg-zinc-800 border border-zinc-700 rounded px-2 py-1 font-mono text-[11px] text-zinc-400">
                    {typeof h === 'string' ? h.slice(0, 40) : JSON.stringify(h)}
                  </span>
                ))
              : <p className="text-xs text-gray-600">DHT 未返回样本（DHT 服务可能未启用或无可用节点）</p>
            }
          </div>
        </div>
      )}

      {/* Empty state */}
      {!result && !bep51 && !loading && !error && (
        <div className="text-center py-16 text-gray-600">
          <p className="text-4xl mb-3">🔍</p>
          <p className="text-sm">输入 SHA256 / InfoHash / CID 查询 DHT 网络中的对等节点</p>
          <p className="text-xs mt-2">同时查询 IPFS DHT 和 BitTorrent Mainline DHT</p>
        </div>
      )}
    </div>
  );
}
