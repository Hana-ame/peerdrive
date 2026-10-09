// Module ⑤: Transfer tasks —— cross-node pull task list with live progress.
//
// 2026-10 (benchmark B15, audit P7-adjacent): the table used to show only
// peer/name/status-badge/time — a "running" badge told you nothing about *how*
// a pull was going. rclone-web's TransfersTable is the reference shape:
// progress bar + percentage + speed + ETA + failure reason on hover. Backend
// already ships the data (PullJob.total/received/error/started_at/ended_at in
// peerpull.go); it was purely a UI gap.
//
// Refresh model: doc/NETDISK.md §6 (transfer list needs polling or WS push) —
// there is no job-push verb on the WS protocol, so we poll while any job is
// running (2s) and stop polling once everything reaches a terminal state,
// instead of hammering the admin plane from an idle page.
import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import * as ws from '../../../platform/transport-ws';
import {
  fmtBytes, fmtRate, fmtEta, progressPct, estEtaSeconds, RateTracker,
} from '../../../platform/shared/format';

const POLL_MS = 2000;

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? ts : d.toLocaleString('zh-CN', { hour12: false });
}

// statusMeta: badge copy + color per PullStatus (running/done/failed/cancelled —
// back/internal/service/peerpull.go). Kept as data so the legend and the cell
// render agree.
function statusMeta(j) {
  switch (j.status) {
    case 'done':
      return { label: j.skipped ? 'Skipped (already local)' : 'Done', cls: 'text-green-400 border-green-400/20 bg-green-400/10' };
    case 'failed':
      return { label: 'Failed', cls: 'text-red-400 border-red-400/20 bg-red-400/10' };
    case 'cancelled':
      return { label: 'Cancelled', cls: 'text-gray-400 border-gray-400/20 bg-gray-400/10' };
    case 'resuming':
      return { label: 'Resuming', cls: 'text-blue-400 border-blue-400/20 bg-blue-400/10' };
    case 'running':
    default:
      return { label: 'Running', cls: 'text-yellow-400 border-yellow-400/20 bg-yellow-400/10' };
  }
}

// Spinner: rotating icon for running rows (rclone-web rotates its transfer icon;
// the Tailwind animate-spin is already available).
function Spinner() {
  return (
    <svg className="animate-spin h-3.5 w-3.5 text-yellow-400 shrink-0" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v3a5 5 0 00-5 5H4z" />
    </svg>
  );
}

// ProgressCell: bar + pct + rate + ETA; indeterminate (peer declared no size,
// total=-1) renders a pulsing full-width muted bar with the byte count only.
function ProgressCell({ job, rateBps }) {
  const running = job.status === 'running' || job.status === 'resuming' || !job.status;
  const pct = progressPct(job.received, job.total);
  const eta = running ? estEtaSeconds(job.total, job.received, rateBps) : null;

  let bar;
  if (!running) {
    // Terminal states: full bar tinted by outcome reads as "finished" at a glance.
    const tone = job.status === 'done' ? 'bg-green-400/60'
      : job.status === 'failed' ? 'bg-red-400/50' : 'bg-gray-500/40';
    bar = <div className={`h-1.5 w-full rounded-full ${tone}`} />;
  } else if (pct === null) {
    bar = <div className="h-1.5 w-full rounded-full bg-yellow-400/30 animate-pulse" />;
  } else {
    bar = (
      <div className="h-1.5 w-full rounded-full bg-white/[0.07] overflow-hidden">
        <div className="h-full rounded-full bg-brand-400 transition-all duration-500" style={{ width: `${pct}%` }} />
      </div>
    );
  }

  return (
    <div className="min-w-[160px]">
      {bar}
      <div className="flex items-center gap-2 mt-1 text-[11px] text-gray-400 tabular-nums">
        <span className="w-10 shrink-0">
          {running ? (pct === null ? '' : `${pct}%`) : ''}
        </span>
        <span>{running && pct === null ? `${fmtBytes(job.received)}…` : (job.total > 0 ? `${fmtBytes(job.received)} / ${fmtBytes(job.total)}` : fmtBytes(job.received))}</span>
        {running && (
          <>
            <span className="text-gray-500">{fmtRate(rateBps)}</span>
            <span className="text-gray-500" title="estimated time remaining">{fmtEta(eta) !== '—' ? `ETA ${fmtEta(eta)}` : '—'}</span>
          </>
        )}
      </div>
    </div>
  );
}

export default function Transfers() {
  const [jobs, setJobs] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  // RateTracker lives in a ref: it must survive re-renders but not trigger them.
  const trackerRef = useRef(null);
  if (!trackerRef.current) trackerRef.current = new RateTracker();
  const lastTickRef = useRef(0);
  const [rates, setRates] = useState(() => new Map());

  const load = useCallback(async () => {
    try {
      const res = await ws.admin('GET', '/p2p/pull');
      const list = Array.isArray(res) ? res : Array.isArray(res?.jobs) ? res.jobs : [];
      const now = Date.now();
      const elapsed = lastTickRef.current ? now - lastTickRef.current : 0;
      lastTickRef.current = now;
      setRates(trackerRef.current.update(list, elapsed));
      setJobs(list);
      setErr('');
    } catch (e) {
      // Keep the last list on screen (a blip shouldn't blank the table); surface
      // the reason in the banner so the user knows data may be stale.
      setErr(e?.message || String(e));
      setJobs((prev) => (prev === null ? [] : prev));
    }
  }, []);

  const hasRunning = useMemo(
    () => Array.isArray(jobs) && jobs.some((j) => j.status === 'running' || j.status === 'resuming' || !j.status),
    [jobs]);

  useEffect(() => { load(); }, [load]);

  // Poll only while something is running — an idle list doesn't change on its own.
  useEffect(() => {
    if (!hasRunning) return undefined;
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [hasRunning, load]);

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
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">
            {err}
            <span className="text-gray-500 ml-2">Showing last-known list; data may be stale.</span>
          </div>
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
                  <th className={th}>Progress</th>
                  <th className={th}>Created</th>
                  <th className={th + ' text-right'}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((j, i) => {
                  const meta = statusMeta(j);
                  const running = j.status === 'running' || j.status === 'resuming' || !j.status;
                  const rate = rates.get(j.id) ?? null;
                  // Failure tooltip (B15): PullJob.Error holds the backend reason
                  // (sha256 mismatch / read failed / "after N attempts" — see
                  // peerpull.go finish()). Truncated in the cell, full text on hover.
                  const failedText = j.status === 'failed' && j.error ? j.error : '';
                  return (
                    <tr key={j.id || i} className="border-t border-white/[0.04] hover:bg-white/[0.02] align-top">
                      <td className="px-3 py-2 text-gray-300 font-mono text-xs">{j.peer || j.peer_id || '—'}</td>
                      <td className="px-3 py-2 text-gray-300 max-w-[240px]">
                        <div className="truncate" title={j.path || j.name || j.hash}>{j.name || j.path || j.hash || '—'}</div>
                        {j.collection && <div className="text-[10px] text-gray-600 font-mono truncate">{j.collection.slice(0, 16)}</div>}
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
                          <div className="text-[10px] text-gray-600 mt-1 max-w-[180px] truncate" title={j.saved_to}>
                            → {j.saved_to}
                          </div>
                        )}
                      </td>
                      <td className="px-3 py-2">
                        <ProgressCell job={j} rateBps={rate} />
                      </td>
                      <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{fmtTime(j.started_at || j.created_at)}</td>
                      <td className="px-3 py-2 text-right">
                        {running ? (
                          <button onClick={() => cancel(j)} disabled={busy}
                            className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300">Cancel</button>
                        ) : (
                          <span className="text-[11px] text-gray-600 px-2">{fmtTime(j.ended_at)}</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
