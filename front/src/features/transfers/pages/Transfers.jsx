// Module ⑤: Transfer tasks —— Top-level page orchestration (AGENTS.md §1 compliant).
//
// State & polling live in hooks/useTransfers.js; table & progress micro-components live
// in components/{TransferTable,TransferProgressCell}.jsx.
import React from 'react';
import { useTransfers } from '../hooks/useTransfers';
import TransferTable from '../components/TransferTable';

export default function Transfers() {
  const {
    mergedJobs,
    hubSnap,
    err,
    busy,
    aria2Status,
    rates,
    load,
    toggleAria2,
    cancel,
    clearCompleted,
  } = useTransfers();

  return (
    <div className="p-4 sm:p-8 overflow-y-auto overflow-x-hidden h-full max-w-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
          <div>
            <h1 className="text-2xl font-bold">Transfer Tasks</h1>
            <p className="text-sm text-gray-500 mt-0.5">
              PeerJS / WebSocket / HTTP queue & cross-node pull tasks
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {aria2Status && (
              <button
                type="button"
                onClick={toggleAria2}
                disabled={busy}
                className={`text-xs px-2.5 py-1.5 rounded-lg border flex items-center gap-1.5 transition-colors ${
                  aria2Status.enabled
                    ? 'border-brand-500/40 bg-brand-500/10 text-brand-300'
                    : 'border-white/10 bg-white/[0.04] text-gray-400 hover:text-gray-200'
                }`}
                title={
                  aria2Status.enabled
                    ? `aria2c connected (v${aria2Status.version || '?'})`
                    : 'aria2c integration disabled'
                }
              >
                <span
                  className={`w-2 h-2 rounded-full ${
                    aria2Status.enabled
                      ? aria2Status.connected
                        ? 'bg-green-400'
                        : 'bg-yellow-400 animate-pulse'
                      : 'bg-gray-500'
                  }`}
                />
                <span>
                  aria2c:{' '}
                  {aria2Status.enabled ? (aria2Status.connected ? 'Active' : 'Connecting') : 'Off'}
                </span>
              </button>
            )}
            {hubSnap.totalCompleted > 0 && (
              <button type="button" onClick={clearCompleted} className="btn-ghost text-xs">
                Clear Completed
              </button>
            )}
            <button onClick={load} disabled={busy} className="btn-ghost">
              Refresh
            </button>
          </div>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">
            {err}
            <span className="text-gray-500 ml-2">Showing last-known list; data may be stale.</span>
          </div>
        )}

        {mergedJobs === null ? (
          <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>
        ) : mergedJobs.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p>No transfer tasks</p>
            <p className="text-xs text-gray-600 mt-1">
              PeerJS / WS / HTTP file transfers and cross-node pulls appear here automatically.
            </p>
          </div>
        ) : (
          <TransferTable jobs={mergedJobs} rates={rates} busy={busy} onCancel={cancel} />
        )}
      </div>
    </div>
  );
}
