// PeerJSConnect — PeerJS signaling dialer connector (consumer)
//
// The second way to connect to a peer node in the full UI: dial the target
// node by peer id via public signaling, view the peer's shared standalone
// files / collections, and save them directly. Runs alongside WebSocket/HTTP
// (management plane).
// Extracted as an independent component: shared by the Settings node-connect
// section and the home page (Plaza).
import React, { useState, useRef, useEffect } from 'react';
import { connectToPeer, discoverNodes } from 'peerdrive-client';
import { setNodeSession, clearNodeSession } from './nodeSession';

import { fmtBytes } from '../platform/shared/format';
import { STORAGE_KEY_PANEL_PREFS } from '../platform/shared/storageKeys';

const DEFAULT_SIG = {
  host: 'peersignal.moonchan.xyz',
  port: 443,
  path: '/',
  key: 'pd-signal-1edf5e05e4a52b7351392574',
  secure: true,
};

function getStableMyId() {
  try {
    const data = JSON.parse(localStorage.getItem(STORAGE_KEY_PANEL_PREFS) || '{}');
    if (data.myId) return data.myId;
  } catch (e) { /* ignore */ }
  const id = 'pd-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8);
  try {
    const data = JSON.parse(localStorage.getItem(STORAGE_KEY_PANEL_PREFS) || '{}');
    data.myId = id;
    localStorage.setItem(STORAGE_KEY_PANEL_PREFS, JSON.stringify(data));
  } catch (e) { /* ignore */ }
  return id;
}

export default function PeerJSConnect({ compact = false, onConnected = null }) {
  const [sigHost, setSigHost] = useState(DEFAULT_SIG.host);
  const [sigPort, setSigPort] = useState(String(DEFAULT_SIG.port));
  const [sigKey, setSigKey] = useState(DEFAULT_SIG.key);
  const [targetPeerId, setTargetPeerId] = useState('');
  const [psk, setPsk] = useState('');
  const myIdRef = useRef(getStableMyId());
  const [pdClient, setPdClient] = useState(null);
  const [pdStatus, setPdStatus] = useState('idle'); // idle|connecting|online|error
  const [pdError, setPdError] = useState('');
  const [pdShare, setPdShare] = useState(null);
  const [collOpen, setCollOpen] = useState(null); // expanded collection index
  // Search online nodes (signaling discover)
  const [foundNodes, setFoundNodes] = useState(null); // null=not searched | [] = empty
  const [searchStatus, setSearchStatus] = useState('idle'); // idle|searching|error
  const [searchErr, setSearchErr] = useState('');


  // Dial core: connect directly by the passed peerId (doesn't depend on state;
  // the search-list click can call it immediately)
  const connectTo = async (peerId) => {
    const target = (peerId || '').trim();
    if (!target) return;
    setPdStatus('connecting');
    setPdError('');
    setPdShare(null);
    try {
      const Peer = await import('peerjs').then(m => m.default || m);
      const client = await connectToPeer(Peer, target, {
        peerOptions: {
          host: sigHost.trim() || DEFAULT_SIG.host,
          port: Number(sigPort.trim()) || DEFAULT_SIG.port,
          path: DEFAULT_SIG.path,
          key: sigKey.trim() || DEFAULT_SIG.key,
          secure: DEFAULT_SIG.secure, // signaling always goes over wss (HTTPS-encrypted), no toggle
          id: myIdRef.current,
        },
        connOptions: { serialization: 'raw', reliable: true },
        psk: psk.trim(),
        timeoutMs: 15000,
      });
      setPdClient(client);
      const snap = await client.shares();
      setPdShare(snap);
      setPdStatus('online');
      // Store in global session (for the node control page), and trigger the
      // "jump on successful connect" callback
      setNodeSession({
        client,
        peerId: target,
        myId: myIdRef.current,
        psk: psk.trim(),
        pskAuthenticated: Boolean(psk.trim() && client.pskState === 'ok'),
        signalHost: sigHost.trim() || DEFAULT_SIG.host,
        signalPort: Number(sigPort.trim()) || DEFAULT_SIG.port,
      });
      onConnected?.(client, target);
    } catch (e) {
      setPdStatus('error');
      setPdError(e?.message || String(e));
    }
  };

  const handleConnect = () => connectTo(targetPeerId);

  const handleDisconnect = () => {
    clearNodeSession();
    try { pdClient?.close?.(); } catch (e) { /* ignore */ }
    setPdClient(null);
    setPdShare(null);
    setPdStatus('idle');
    setCollOpen(null);
  };

  // Search currently online nodes via public signaling
  const handleSearch = async () => {
    setSearchStatus('searching');
    setSearchErr('');
    try {
      const nodes = await discoverNodes(
        { host: sigHost.trim() || DEFAULT_SIG.host, port: Number(sigPort.trim()) || DEFAULT_SIG.port, secure: DEFAULT_SIG.secure },
        { timeoutMs: 8000 },
      );
      setFoundNodes(nodes);
      setSearchStatus('done');
    } catch (e) {
      setSearchStatus('error');
      setSearchErr(e?.message || String(e));
      setFoundNodes(null);
    }
  };

  // Auto-search online nodes on mount (no need to manually click "search
  // online nodes"; the button stays for manual refresh)
  useEffect(() => { handleSearch(); }, []);

  // Dial directly from search results (click = connect; no need to refill the
  // input field)
  const handleJoinFound = (peerId) => {
    setTargetPeerId(peerId);
    setFoundNodes(null);
    setSearchStatus('idle');
    connectTo(peerId);
  };

  const handleSave = async (item) => {
    if (!pdClient) return;
    try {
      await pdClient.saveAs(item.hash, item.path || item.name || 'download');
    } catch (e) {
      alert('Save failed: ' + (e?.message || String(e)));
    }
  };

  const inputCls = 'w-full bg-white/[0.06] px-2 py-1 text-xs font-mono rounded border border-white/[0.1] focus:outline-none focus:border-brand-500';

  return (
    <div className={compact ? 'space-y-2' : 'space-y-3'}>
      {!compact && (
        <div className="grid grid-cols-2 gap-2">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Signaling host</label>
            <input value={sigHost} onChange={e => setSigHost(e.target.value)} className={inputCls} />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Signaling port</label>
            <input value={sigPort} onChange={e => setSigPort(e.target.value)} className={inputCls} />
          </div>
          <div className="col-span-2">
            <label className="block text-xs text-gray-500 mb-1">Signaling key</label>
            <input value={sigKey} onChange={e => setSigKey(e.target.value)} className={inputCls} />
          </div>
        </div>
      )}


      {/* Search online nodes */}
      <div>
        <div className="flex gap-2">
          <button onClick={handleSearch} disabled={searchStatus === 'searching'}
            className="px-3 py-1 text-xs bg-white/[0.06] text-gray-300 rounded-lg hover:bg-white/[0.12] disabled:opacity-40 whitespace-nowrap">
            {searchStatus === 'searching' ? 'Searching...' : 'Search Online Nodes'}
          </button>
          <span className="text-xs self-center">
            {searchStatus === 'error' && <span className="text-red-400">{searchErr}</span>}
            {searchStatus === 'done' && <span className="text-gray-400">{foundNodes?.length ?? 0} online nodes</span>}
          </span>
        </div>
        {searchStatus === 'done' && foundNodes && foundNodes.length > 0 && (
          <ul className="mt-2 space-y-1.5">
            {foundNodes.map((n, i) => {
              const auth = n.loadInfo?.auth || (n.loadInfo?.shares?.countsHidden ? 'open' : 'open');
              const isPsk = auth === 'psk' || Boolean(n.psk);
              const sharesCount = typeof n.loadInfo?.shares_count === 'number'
                ? n.loadInfo.shares_count
                : (n.loadInfo?.shares?.collections ?? (Array.isArray(n.collections) ? n.collections.length : 0));
              const caps = Array.isArray(n.loadInfo?.caps) ? n.loadInfo.caps : [];

              return (
                <li key={i} className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs bg-white/[0.04] p-2.5 rounded-lg border border-white/[0.06] hover:bg-white/[0.06] transition-colors">
                  <div className="flex-1 min-w-0">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-mono text-gray-200 font-medium truncate max-w-[200px] sm:max-w-[280px]" title={n.peerId}>{n.peerId}</span>
                      <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${isPsk ? 'bg-amber-500/20 text-amber-300 border border-amber-500/30' : 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30'}`}>
                        {isPsk ? '🔒 需密码' : '🔓 公开免密'}
                      </span>
                      <span className="px-1.5 py-0.5 rounded text-[10px] bg-white/[0.06] text-gray-400 font-mono">
                        {sharesCount > 0 ? `📦 ${sharesCount}个公开合集` : '📭 暂无公开内容'}
                      </span>
                      {n.nodeType && (
                        <span className="text-[10px] text-gray-500 font-mono">[{n.nodeType}]</span>
                      )}
                    </div>
                    {caps.length > 0 && (
                      <div className="flex flex-wrap items-center gap-1 mt-1 text-[10px] text-gray-500">
                        <span className="text-gray-400">能力:</span>
                        {caps.map((c) => (
                          <span key={c} className="px-1 py-0.2 bg-white/[0.04] rounded text-gray-400 font-mono">{c}</span>
                        ))}
                      </div>
                    )}
                  </div>
                  <button onClick={() => handleJoinFound(n.peerId)}
                    className="self-end sm:self-center px-3 py-1 text-xs bg-brand-600 text-white rounded-lg hover:bg-brand-500 shrink-0 transition-colors font-medium">
                    连接 (Connect)
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {searchStatus === 'done' && foundNodes && foundNodes.length === 0 && (
          <p className="text-xs text-gray-600 mt-1.5">No online nodes currently (nodes will be announced via signaling when online).</p>
        )}
      </div>

      <div>
        {!compact && <label className="block text-xs text-gray-500 mb-1">Target peer id (dial target)</label>}
        <div className="flex gap-2">
          <input value={targetPeerId} onChange={e => setTargetPeerId(e.target.value)}
            placeholder="peerdrive-xxxxxxxx"
            className={inputCls + ' flex-1'}
            onKeyDown={e => { if (e.key === 'Enter') handleConnect(); }} />
          <input value={psk} onChange={e => setPsk(e.target.value)}
            type="password"
            placeholder="PSK key (optional / 留空为游客)"
            className={inputCls + ' w-44'}
            title="Pre-shared key (if required by node)"
            onKeyDown={e => { if (e.key === 'Enter') handleConnect(); }} />
          <button onClick={handleConnect} disabled={!targetPeerId.trim() || pdStatus === 'connecting'}
            className="px-3 py-1 text-xs bg-brand-600 text-white rounded-lg hover:bg-brand-500 disabled:opacity-40 whitespace-nowrap">Connect</button>
          {pdStatus === 'online' && (
            <button onClick={handleDisconnect}
              className="px-3 py-1 text-xs bg-white/[0.06] text-gray-300 rounded-lg hover:bg-white/[0.12] whitespace-nowrap">Disconnect</button>
          )}
        </div>
      </div>

      <div className="text-xs">
        {pdStatus === 'connecting' && <span className="text-yellow-400">Connecting...</span>}
        {pdStatus === 'online' && <span className="text-green-400">Connected ✓ (local id: {myIdRef.current})</span>}
        {pdStatus === 'error' && <span className="text-red-400">Failed: {pdError}</span>}
      </div>

      {pdStatus === 'online' && (
        <div className={compact ? '' : 'border-t border-white/[0.06] pt-2'}>
          <p className="text-xs text-gray-500 mb-1.5">Peer shares ({pdShare?.total ?? 0} items)</p>
          {pdShare && pdShare.files?.length > 0 && (
            <div className="mb-2">
              {!compact && <p className="text-xs text-gray-500 mb-1">Standalone Files</p>}
              <ul className="space-y-1">
                {pdShare.files.map((f, i) => (
                  <li key={i} className="flex items-center gap-2 text-xs">
                    <span className="flex-1 truncate text-gray-300">{f.path || f.name}</span>
                    <span className="text-gray-500 shrink-0">{fmtBytes(f.size)}</span>
                    <button onClick={() => handleSave(f)}
                      className="px-2 py-0.5 text-xs bg-white/[0.06] text-gray-300 rounded hover:bg-white/[0.12] shrink-0">Save</button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {pdShare && pdShare.collections?.length > 0 && (
            <div>
              {!compact && <p className="text-xs text-gray-500 mb-1">Collections</p>}
              <ul className="space-y-1">
                {pdShare.collections.map((c, i) => {
                  const entries = Array.isArray(c.entries) ? c.entries : [];
                  const open = collOpen === i;
                  return (
                    <li key={i} className="text-xs">
                      <button
                        onClick={() => setCollOpen(open ? null : i)}
                        className="flex items-center gap-1 text-gray-300 hover:text-white transition-colors"
                      >
                        <span className={open ? 'rotate-90 transition-transform' : 'transition-transform'}>▶</span>
                        <span className="truncate">{c.name || String(c.hash).slice(0, 12) + '…'}</span>
                        <span className="text-gray-500">· {entries.length} entries</span>
                      </button>
                      {open && (
                        <ul className="mt-1 ml-4 space-y-1">
                          {entries.map((e, ei) => (
                            <li key={ei} className="flex items-center gap-2">
                              <span className="flex-1 truncate text-gray-400">{e.path}</span>
                              <span className="text-gray-500 shrink-0">{fmtBytes(e.size)}</span>
                              <button onClick={() => handleSave({ ...e, name: e.path || 'download' })}
                                className="px-2 py-0.5 text-xs bg-white/[0.06] text-gray-300 rounded hover:bg-white/[0.12] shrink-0">Save</button>
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
          {pdShare && (pdShare.total ?? 0) === 0 && (
            <p className="text-xs text-gray-600">This node has no shared content (external sharing not enabled, or no shared directories declared).</p>
          )}
        </div>
      )}
    </div>
  );
}
