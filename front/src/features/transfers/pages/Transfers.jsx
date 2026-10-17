// Module ⑤: Transfer tasks —— Unified PeerJS + WS + HTTP client transfer queue & cross-node pull tasks.
//
// Displays both:
//   1. Backend cross-node pull jobs (GET /p2p/pull via WS admin plane)
//   2. Live browser-side PeerJS / WebSocket / HTTP queue transfers (via globalProgressHub)
// so users have a complete, real-time progress bar, transfer rate, and ETA across all transports.
import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession } from '../../../lib/nodeSession';
import {
  fmtBytes,
  fmtRate,
  fmtEta,
  progressPct,
  estEtaSeconds,
  RateTracker,
  globalProgressHub,
} from '../../../platform/shared';

const POLL_MS = 2000;

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? String(ts) : d.toLocaleString('zh-CN', { hour12: false });
}

function statusMeta(j) {
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

function Spinner() {
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

function ProgressCell({ job, rateBps }) {
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

export default function Transfers() {
  const [jobs, setJobs] = useState(null);
  const [hubSnap, setHubSnap] = useState(() => globalProgressHub.getSnapshot());
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [aria2Status, setAria2Status] = useState(null);

  const trackerRef = useRef(null);
  if (!trackerRef.current) trackerRef.current = new RateTracker();
  const lastTickRef = useRef(0);
  const [rates, setRates] = useState(() => new Map());

  useEffect(() => {
    return globalProgressHub.subscribe((next) => {
      setHubSnap({ ...next });
    });
  }, []);

  const load = useCallback(async () => {
    if (getNodeSession?.()?.client) {
      setJobs((prev) => (prev === null ? [] : prev));
      return;
    }
    try {
      const [res, aria2Res] = await Promise.all([
        ws.admin('GET', '/p2p/pull'),
        ws.admin('GET', '/p2p/aria2/status').catch(() => null),
      ]);
      const list = Array.isArray(res) ? res : Array.isArray(res?.jobs) ? res.jobs : [];
      const now = Date.now();
      const elapsed = lastTickRef.current ? now - lastTickRef.current : 0;
      lastTickRef.current = now;
      const nextRates = trackerRef.current.update(list, elapsed);
      setRates(nextRates);
      setJobs(list);
      if (aria2Res) setAria2Status(aria2Res);
      setErr('');
    } catch (e) {
      setErr(e?.message || String(e));
      setJobs((prev) => (prev === null ? [] : prev));
    }
  }, []);

  const hasRunning = useMemo(
    () =>
      Array.isArray(jobs) &&
      jobs.some((j) => j.status === 'running' || j.status === 'resuming' || !j.status),
    [jobs]
  );

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (!hasRunning) return undefined;
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [hasRunning, load]);

  const toggleAria2 = async () => {
    if (!aria2Status) return;
    const next = !aria2Status.enabled;
    setBusy(true);
    try {
      await ws.admin('POST', '/p2p/aria2/toggle', { enabled: next });
      await load();
    } catch (e) {
      setErr('Failed to toggle aria2: ' + (e?.message || String(e)));
    } finally {
      setBusy(false);
    }
  };

  const cancel = async (job) => {
    if (job._isClientTask) {
      globalProgressHub.cancelTask(job.id);
      return;
    }
    setBusy(true);
    try {
      await ws.admin('POST', '/p2p/pull/cancel', { id: job.id });
      await load();
    } catch (e) {
      setErr(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  };

  // Merge backend pull jobs with client-side PeerJS / WS / HTTP transfer tasks
  const mergedJobs = useMemo(() => {
    if (jobs === null) return null;
    const clientTasks = (hubSnap.tasks || [])
      .filter((t) => t.transport !== 'node-pull')
      .map((t) => ({
        id: t.id,
        peer: t.peerId || t.transport.toUpperCase(),
        hash: t.hash,
        name: t.name,
        received: t.received,
        total: t.total,
        status: t.status,
        error: t.error,
        started_at: t.startedAt,
        ended_at: t.endedAt,
        rateBps: t.rateBps,
        transport: t.transport,
        _isClientTask: true,
      }));
    return [...clientTasks, ...(jobs || [])];
  }, [jobs, hubSnap.tasks]);

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

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
              <button
                type="button"
                onClick={() => globalProgressHub.clearCompleted()}
                className="btn-ghost text-xs"
              >
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
          <div className="card-surface overflow-x-auto max-w-full">
            <table className="w-full text-sm min-w-[580px]">
              <thead className="bg-white/[0.03]">
                <tr>
                  <th className={th}>Peer / Channel</th>
                  <th className={th}>Name / Path</th>
                  <th className={th}>Status</th>
                  <th className={th}>Progress</th>
                  <th className={th}>Created</th>
                  <th className={th + ' text-right'}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {mergedJobs.map((j, i) => {
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
                        <ProgressCell job={j} rateBps={rate} />
                      </td>
                      <td className="px-3 py-2 text-gray-500 whitespace-nowrap">
                        {fmtTime(j.started_at || j.created_at)}
                      </td>
                      <td className="px-3 py-2 text-right">
                        {running ? (
                          <button
                            onClick={() => cancel(j)}
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
        )}
      </div>
    </div>
  );
}
