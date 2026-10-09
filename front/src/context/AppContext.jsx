import React, { createContext, useContext, useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { getStatus, onStatus } from '../platform/transport-ws/status';
import { admin } from '../platform/transport-ws';
import { RateTracker } from '../platform/shared/format';

// NodeContext: Global node session state, node identity, and authentication (Issue #217).
export const NodeContext = createContext(null);

// TransferContext: Global active transfer tasks, progress, and transfer rates (Issue #217).
export const TransferContext = createContext(null);

export function NodeProvider({ children }) {
  const [status, setStatus] = useState(() => getStatus());
  const [nodeInfo, setNodeInfo] = useState(null);
  const [authInfo, setAuthInfo] = useState(null);
  const [loading, setLoading] = useState(false);

  const fetchNodeDetails = useCallback(async () => {
    if (getStatus() !== 'open') return;
    setLoading(true);
    try {
      const [nodeRes, authRes] = await Promise.allSettled([
        admin('GET', '/peerjs/node'),
        admin('GET', '/p2p/auth/status'),
      ]);
      if (nodeRes.status === 'fulfilled') {
        setNodeInfo(nodeRes.value);
      }
      if (authRes.status === 'fulfilled') {
        setAuthInfo(authRes.value);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const unsub = onStatus((s) => {
      setStatus(s);
      if (s === 'open') {
        fetchNodeDetails();
      } else {
        setNodeInfo(null);
        setAuthInfo(null);
      }
    });
    return unsub;
  }, [fetchNodeDetails]);

  const value = useMemo(() => ({
    status,
    nodeInfo,
    authInfo,
    loading,
    refreshNode: fetchNodeDetails,
  }), [status, nodeInfo, authInfo, loading, fetchNodeDetails]);

  return (
    <NodeContext.Provider value={value}>
      {children}
    </NodeContext.Provider>
  );
}

export function TransferProvider({ children }) {
  const [jobs, setJobs] = useState(null);
  const [err, setErr] = useState('');
  const [loading, setLoading] = useState(false);
  const [rates, setRates] = useState(() => new Map());

  const trackerRef = useRef(null);
  if (!trackerRef.current) trackerRef.current = new RateTracker();
  const lastTickRef = useRef(0);

  const fetchTransfers = useCallback(async () => {
    if (getStatus() !== 'open') return;
    setLoading(true);
    try {
      const res = await admin('GET', '/p2p/pull');
      const list = Array.isArray(res) ? res : Array.isArray(res?.jobs) ? res.jobs : [];
      const now = Date.now();
      const elapsed = lastTickRef.current ? now - lastTickRef.current : 0;
      lastTickRef.current = now;
      setRates(trackerRef.current.update(list, elapsed));
      setJobs(list);
      setErr('');
    } catch (e) {
      setErr(e?.message || String(e));
      setJobs((prev) => (prev === null ? [] : prev));
    } finally {
      setLoading(false);
    }
  }, []);

  const activeJobs = useMemo(() => {
    if (!Array.isArray(jobs)) return [];
    return jobs.filter((j) => j.status === 'running' || j.status === 'resuming' || !j.status);
  }, [jobs]);

  const value = useMemo(() => ({
    jobs,
    activeJobs,
    rates,
    err,
    loading,
    refreshTransfers: fetchTransfers,
  }), [jobs, activeJobs, rates, err, loading, fetchTransfers]);

  return (
    <TransferContext.Provider value={value}>
      {children}
    </TransferContext.Provider>
  );
}

export function AppProvider({ children }) {
  return (
    <NodeProvider>
      <TransferProvider>
        {children}
      </TransferProvider>
    </NodeProvider>
  );
}

export function useNodeState() {
  const ctx = useContext(NodeContext);
  if (!ctx) {
    throw new Error('useNodeState must be used within a NodeProvider/AppProvider');
  }
  return ctx;
}

export function useTransferState() {
  const ctx = useContext(TransferContext);
  if (!ctx) {
    throw new Error('useTransferState must be used within a TransferProvider/AppProvider');
  }
  return ctx;
}

export function useAppContext() {
  const node = useNodeState();
  const transfer = useTransferState();
  return { ...node, ...transfer };
}
