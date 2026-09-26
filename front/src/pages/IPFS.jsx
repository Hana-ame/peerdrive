// 模块⑥：IPFS —— pin 列表与 gateway 状态（简化面板）。
import React, { useState, useEffect, useCallback } from 'react';
import * as ws from '../ws';

export default function IPFS() {
  const [pins, setPins] = useState([]);
  const [gateways, setGateways] = useState([]);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setErr('');
    try {
      const [p, g] = await Promise.all([
        ws.admin('GET', '/ipfs/pins').catch(() => []),
        ws.admin('GET', '/ipfs/gateways').catch(() => []),
      ]);
      setPins(Array.isArray(p) ? p : Array.isArray(p?.pins) ? p.pins : []);
      setGateways(Array.isArray(g) ? g : Array.isArray(g?.gateways) ? g.gateways : []);
    } catch (e) { setErr(e?.message || String(e)); }
  }, []);
  useEffect(() => { load(); }, [load]);

  const unpin = async (cid) => {
    setBusy(true);
    try {
      await ws.admin('DELETE', `/ipfs/pin/${cid}`);
      await load();
    } catch (e) { setErr(e?.message || String(e)); }
    finally { setBusy(false); }
  };

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">IPFS</h1>
            <p className="text-sm text-gray-500 mt-0.5">内容寻址下载 / pin 管理</p>
          </div>
          <button onClick={load} disabled={busy} className="btn-ghost">刷新</button>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {gateways.length > 0 && (
          <div className="card-surface p-4 mb-4">
            <p className="text-xs text-gray-500 mb-1.5">可用 gateway</p>
            <ul className="space-y-1">
              {gateways.map((g, i) => (
                <li key={i} className="text-xs text-gray-300 font-mono">{typeof g === 'string' ? g : String(g.url || g)}</li>
              ))}
            </ul>
          </div>
        )}

        {pins.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p>没有 pin 的内容</p>
            <p className="text-xs text-gray-600 mt-1">pin 过的 CID 会在这里列出。</p>
          </div>
        ) : (
          <div className="card-surface overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-white/[0.03]">
                <tr>
                  <th className="text-left text-[10px] uppercase tracking-wider text-gray-500 px-3 py-2">CID</th>
                  <th className="text-right text-[10px] uppercase tracking-wider text-gray-500 px-3 py-2">操作</th>
                </tr>
              </thead>
              <tbody>
                {pins.map((p, i) => (
                  <tr key={i} className="border-t border-white/[0.04] hover:bg-white/[0.02]">
                    <td className="px-3 py-2 font-mono text-xs text-gray-300 break-all">{typeof p === 'string' ? p : p.cid || p.hash || JSON.stringify(p)}</td>
                    <td className="px-3 py-2 text-right">
                      <button onClick={() => unpin(typeof p === 'string' ? p : p.cid || p.hash)}
                        className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300">取消 pin</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}