// BT DHT 采样查询页：BEP51 采样（后端 /bt/bep51/sample）。
// 2026-08-19：原「双栈查询」（IPFS DHT + BT DHT 网络查找）依赖已删除的
// /p2p/dual/find 端点，随 libp2p 栈清理移除；本页保留仍存活的 BEP51 采样功能。
import React, { useState } from 'react';
import * as api from '../api';

export default function DHTExplorer() {
  const [bep51, setBep51] = useState(null);
  const [bep51Loading, setBep51Loading] = useState(false);
  const [error, setError] = useState('');

  const doBep51 = async () => {
    setBep51Loading(true);
    setError('');
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
      <h1 className="text-xl font-bold text-gray-200 mb-6">BT DHT 采样查询</h1>

      <div className="flex flex-wrap gap-3 mb-6">
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

      {!bep51 && !bep51Loading && !error && (
        <div className="text-center py-16 text-gray-600">
          <p className="text-4xl mb-3">🔍</p>
          <p className="text-sm">从 BitTorrent Mainline DHT 采集 infohash 样本（BEP51）</p>
        </div>
      )}
    </div>
  );
}