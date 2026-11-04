// TransferProgressCell.jsx — Micro-components for transfer progress bar, spinner, and status badges.
// Extracted from Transfers.jsx per AGENTS.md modularization rules.
import React from 'react';
import {
  fmtBytes,
  fmtRate,
  fmtEta,
  progressPct,
  estEtaSeconds,
} from '../../../platform/shared';

export function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? String(ts) : d.toLocaleString('zh-CN', { hour12: false });
}

export function statusMeta(j) {
  switch (j.status) {
    case 'done':
      return {
        label: j.skipped ? 'Skipped (already local)' : 'Done',
        cls: 'text-green-400 border-green-400/20 bg-green-400/10',
      };
    case 'failed':
      return { label: 'Failed', cls: 'text-red-400 border-red-400/20 bg-red-400/10' };
    case 'cancelled':
      return { label: 'Cancelled', cls: 'text-gray-400 border-gray-400/20 bg-gray-400/10' };
    case 'resuming':
      return { label: 'Resuming', cls: 'text-blue-400 border-blue-400/20 bg-blue-400/10' };
    case 'queued':
      return { label: 'Queued', cls: 'text-purple-400 border-purple-400/20 bg-purple-400/10' };
    case 'running':
    default:
      return { label: 'Running', cls: 'text-yellow-400 border-yellow-400/20 bg-yellow-400/10' };
  }
}

export function Spinner() {
  return (
    <svg
      className="animate-spin h-3.5 w-3.5 text-yellow-400 shrink-0"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v3a5 5 0 00-5 5H4z" />
    </svg>
  );
}

export default function TransferProgressCell({ job, rateBps }) {
  const running =
    job.status === 'running' || job.status === 'resuming' || job.status === 'queued' || !job.status;
  const pct = progressPct(job.received, job.total);
  const eta = running ? estEtaSeconds(job.total, job.received, rateBps) : null;

  let bar;
  if (!running) {
    const tone =
      job.status === 'done'
        ? 'bg-green-400/60'
        : job.status === 'failed'
        ? 'bg-red-400/50'
        : 'bg-gray-500/40';
    bar = <div className={`h-1.5 w-full rounded-full ${tone}`} />;
  } else if (pct === null) {
    bar = <div className="h-1.5 w-full rounded-full bg-yellow-400/30 animate-pulse" />;
  } else {
    bar = (
      <div className="h-1.5 w-full rounded-full bg-white/[0.07] overflow-hidden">
        <div
          className="h-full rounded-full bg-brand-400 transition-all duration-500"
          style={{ width: `${pct}%` }}
        />
      </div>
    );
  }

  return (
    <div className="min-w-[160px]">
      {bar}
      <div className="flex items-center gap-2 mt-1 text-[11px] text-gray-400 tabular-nums">
        <span className="w-10 shrink-0">{running ? (pct === null ? '' : `${pct}%`) : ''}</span>
        <span>
          {running && pct === null
            ? `${fmtBytes(job.received)}…`
            : job.total > 0
            ? `${fmtBytes(job.received)} / ${fmtBytes(job.total)}`
            : fmtBytes(job.received)}
        </span>
        {running && (
          <>
            <span className="text-gray-500">{fmtRate(rateBps)}</span>
            <span className="text-gray-500" title="estimated time remaining">
              {fmtEta(eta) !== '—' ? `ETA ${fmtEta(eta)}` : '—'}
            </span>
          </>
        )}
      </div>
    </div>
  );
}
