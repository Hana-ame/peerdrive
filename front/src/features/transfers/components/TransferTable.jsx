// TransferTable.jsx — Presentational table for unified client & cross-node transfer tasks.
// Extracted from Transfers.jsx per AGENTS.md modularization rules.
import React from 'react';
import TransferProgressCell, { fmtTime, statusMeta, Spinner } from './TransferProgressCell';

const TH_CLS = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

export default function TransferTable({ jobs, rates, busy, onCancel }) {
  return (
    <div className="card-surface overflow-x-auto max-w-full">
      <table className="w-full text-sm min-w-[580px]">
        <thead className="bg-white/[0.03]">
          <tr>
            <th className={TH_CLS}>Peer / Channel</th>
            <th className={TH_CLS}>Name / Path</th>
            <th className={TH_CLS}>Status</th>
            <th className={TH_CLS}>Progress</th>
            <th className={TH_CLS}>Created</th>
            <th className={TH_CLS + ' text-right'}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((j, i) => {
            const meta = statusMeta(j);
            const running =
              j.status === 'running' ||
              j.status === 'resuming' ||
              j.status === 'queued' ||
              !j.status;
            const rate = j._isClientTask ? j.rateBps : (rates.get(j.id) ?? null);
            const failedText = j.status === 'failed' && j.error ? j.error : '';
            return (
              <tr
                key={j.id || i}
                className="border-t border-white/[0.04] hover:bg-white/[0.02] align-top"
              >
                <td className="px-3 py-2 text-gray-300 font-mono text-xs">
                  <div>{j.peer || j.peer_id || '—'}</div>
                  {j.transport && (
                    <div className="text-[10px] text-brand-300 uppercase">{j.transport}</div>
                  )}
                </td>
                <td className="px-3 py-2 text-gray-300 max-w-[240px]">
                  <div className="truncate" title={j.path || j.name || j.hash}>
                    {j.name || j.path || j.hash || '—'}
                  </div>
                  {j.collection && (
                    <div className="text-[10px] text-gray-600 font-mono truncate">
                      {j.collection.slice(0, 16)}
                    </div>
                  )}
                </td>
                <td className="px-3 py-2">
                  <div className="flex items-center gap-1.5">
                    {running && <Spinner />}
                    <span
                      className={`badge-soft ${meta.cls}${failedText ? ' cursor-help' : ''}`}
                      title={failedText || undefined}
                    >
                      {meta.label}
                    </span>
                  </div>
                  {failedText && (
                    <div className="text-[10px] text-red-400/80 mt-1 max-w-[180px] truncate">
                      {failedText}
                    </div>
                  )}
                  {j.status === 'done' && j.saved_to && (
                    <div
                      className="text-[10px] text-gray-600 mt-1 max-w-[180px] truncate"
                      title={j.saved_to}
                    >
                      → {j.saved_to}
                    </div>
                  )}
                </td>
                <td className="px-3 py-2">
                  <TransferProgressCell job={j} rateBps={rate} />
                </td>
                <td className="px-3 py-2 text-gray-500 whitespace-nowrap">
                  {fmtTime(j.started_at || j.created_at)}
                </td>
                <td className="px-3 py-2 text-right">
                  {running ? (
                    <button
                      onClick={() => onCancel(j)}
                      disabled={busy}
                      className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300"
                    >
                      Cancel
                    </button>
                  ) : (
                    <span className="text-[11px] text-gray-600 px-2">
                      {fmtTime(j.ended_at)}
                    </span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
