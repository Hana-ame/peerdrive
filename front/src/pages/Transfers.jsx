// Module ⑤: Transfer tasks —— cross-node pull task list and cancel.
import React, { useState, useEffect, useCallback } from 'react';
import * as ws from '../ws';

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? ts : d.toLocaleString('zh-CN', { hour12: false });
}

export default function Transfers() {
  const [jobs, setJobs] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setErr('');
    try {
      const res = await ws.admin('GET', '/p2p/pull');
      const list = Array.isArray(res) ? res : Array.isArray(res?.jobs) ? res.jobs : [];
      setJobs(list);
    } catch (e) { setErr(e?.message || String(e)); setJobs([]); }
  }, []);
  useEffect(() => { load(); }, [load]);

  const cancel = async (job) => {
    setBusy(true);
    try {
      await ws.admin('POST', '/p2p/pull/cancel', { id: job.id });
      await load();
    } catch (e) { setErr(e?.message || String(e)); }
    finally { setBusy(false); }
  };

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">Transfer Tasks</h1>
            <p className="text-sm text-gray-500 mt-0.5">Cross-node pull tasks</p>
          </div>
          <button onClick={load} disabled={busy} className="btn-ghost">Refresh</button>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {jobs === null ? (
          <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>
        ) : jobs.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p>No transfer tasks</p>
            <p className="text-xs text-gray-600 mt-1">Cross-node pulls are initiated from "Node Market / Peer Sharing".</p>
          </div>
        ) : (
          <div className="card-surface overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-white/[0.03]">
                <tr>
                  <th className={th}>Peer</th>
                  <th className={th}>Name / Path</th>
                  <th className={th}>Status</th>
                  <th className={th}>Created</th>
                  <th className={th + ' text-right'}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((j, i) => (
                  <tr key={j.id || i} className="border-t border-white/[0.04] hover:bg-white/[0.02]">
                    <td className="px-3 py-2 text-gray-300 font-mono text-xs">{j.peer || j.peer_id || '—'}</td>
                    <td className="px-3 py-2 text-gray-300 max-w-[280px] truncate">{j.name || j.path || j.hash || '—'}</td>
                    <td className="px-3 py-2">
                      <span className={`text-[11px] px-2 py-0.5 rounded-full border ${
                        j.status === 'done' || j.status === 'completed'
                          ? 'text-green-400 border-green-400/20 bg-green-400/10'
                          : j.status === 'error' || j.status === 'failed'
                            ? 'text-red-400 border-red-400/20 bg-red-400/10'
                            : 'text-yellow-400 border-yellow-400/20 bg-yellow-400/10'
                      }`}>{j.status || 'running'}</span>
                    </td>
                    <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{fmtTime(j.created_at)}</td>
                    <td className="px-3 py-2 text-right">
                      <button onClick={() => cancel(j)} disabled={busy}
                        className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300">Cancel</button>
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
