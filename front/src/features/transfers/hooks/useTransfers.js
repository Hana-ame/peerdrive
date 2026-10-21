// useTransfers.js — State machine hook for polling cross-node pull jobs, subscribing to
// globalProgressHub, computing EWMA transfer rates, and controlling aria2c.
import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession } from '../../../lib/nodeSession';
import { RateTracker, globalProgressHub } from '../../../platform/shared';

const POLL_MS = 2000;

export function useTransfers() {
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

  return {
    mergedJobs,
    hubSnap,
    err,
    busy,
    aria2Status,
    rates,
    load,
    toggleAria2,
    cancel,
    clearCompleted: () => globalProgressHub.clearCompleted(),
  };
}
