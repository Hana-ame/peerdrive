// 模块⑥：BT —— BT DHT / torrent 下载状态与下载管理（简化面板）。
import React, { useState, useEffect, useCallback } from 'react';
import * as ws from '../ws';

export default function BT() {
  const [status, setStatus] = useState(null);
  const [downloads, setDownloads] = useState([]);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setErr('');
    try {
      const [st, dl] = await Promise.all([
        ws.admin('GET', '/bt/status').catch(() => null),
        ws.admin('GET', '/bt/downloads').catch(() => []),
      ]);
      setStatus(st);
      setDownloads(Array.isArray(dl) ? dl : []);
    } catch (e) { setErr(e?.message || String(e)); }
  }, []);
  useEffect(() => { load(); }, [load]);

  const act = async (fn) => {
    setBusy(true);
    try { await fn(); await load(); } catch (e) { setErr(e?.message || String(e)); }
    finally { setBusy(false); }
  };

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">BT 下载</h1>
            <p className="text-sm text-gray-500 mt-0.5">BT DHT / torrent 下载管理</p>
          </div>
          <button onClick={load} disabled={busy} className="btn-ghost">刷新</button>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {status && (
          <div className="card-surface p-4 mb-4">
            <p className="text-xs text-gray-500 mb-1">BT DHT 状态</p>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-xs">
              {Object.entries(status).filter(([k]) => !/^[a-z_]+:\//.test(k)).map(([k, v]) => (
                <div key={k} className="bg-white/[0.03] rounded px-2 py-1.5">
                  <span className="block text-[10px] text-gray-500">{k}</span>
                  <span className="text-gray-300">{typeof v === 'boolean' ? (v ? '开' : '关') : String(v)}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {downloads.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p>没有 BT 下载任务</p>
            <p className="text-xs text-gray-600 mt-1">用种子 / magnet 发起下载后在这里管理。</p>
          </div>
        ) : (
          <div className="card-surface overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-white/[0.03]">
                <tr>
                  <th className="text-left text-[10px] uppercase tracking-wider text-gray-500 px-3 py-2">infohash</th>
                  <th className="text-left text-[10px] uppercase tracking-wider text-gray-500 px-3 py-2">状态</th>
                  <th className="text-right text-[10px] uppercase tracking-wider text-gray-500 px-3 py-2">操作</th>
                </tr>
              </thead>
              <tbody>
                {downloads.map((d, i) => (
                  <tr key={d.infohash || i} className="border-t border-white/[0.04] hover:bg-white/[0.02]">
                    <td className="px-3 py-2 font-mono text-xs text-gray-300">{d.infohash || '—'}</td>
                    <td className="px-3 py-2 text-gray-500 text-xs">{d.status || '—'}</td>
                    <td className="px-3 py-2 text-right whitespace-nowrap">
                      <button onClick={() => act(() => ws.admin('POST', `/bt/download/${d.infohash}/pause`))}
                        className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">暂停</button>
                      <button onClick={() => act(() => ws.admin('POST', `/bt/download/${d.infohash}/resume`))}
                        className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">继续</button>
                      <button onClick={() => act(() => ws.admin('DELETE', `/bt/download/${d.infohash}`))}
                        className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300">删除</button>
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