import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';

function getRttColor(rtt) {
  if (rtt == null) return 'text-gray-500';
  if (rtt < 50) return 'text-emerald-400';
  if (rtt < 150) return 'text-yellow-400';
  return 'text-red-400';
}

function getRttBg(rtt) {
  if (rtt == null) return 'bg-gray-700';
  if (rtt < 50) return 'bg-emerald-500';
  if (rtt < 150) return 'bg-yellow-500';
  return 'bg-red-500';
}

export default function P2PDashboard() {
  const [status, setStatus] = useState(null);
  const [peers, setPeers] = useState([]);
  const [discovered, setDiscovered] = useState([]);
  const [signalPeers, setSignalPeers] = useState([]);
  const [node, setNode] = useState(null);
  const [pingResults, setPingResults] = useState({});
  const [pingLoading, setPingLoading] = useState({});
  const [statusMsg, setStatusMsg] = useState('');
  const [loading, setLoading] = useState(false);
  const [lastRefresh, setLastRefresh] = useState(null);

  /* ---- manual connect ---- */
  const [connPeerId, setConnPeerId] = useState('');
  const [connAddrs, setConnAddrs] = useState('');

  /* ---- file announce ---- */
  const [announceHash, setAnnounceHash] = useState('');

  /* ---- file find ---- */
  const [findHash, setFindHash] = useState('');
  const [findResults, setFindResults] = useState([]);
  const [findLoading, setFindLoading] = useState(false);

  /* ---- auto refresh ---- */
  const refresh = useCallback(async () => {
    try {
      const [s, p, d, sp] = await Promise.all([
        api.getP2PStatus(),
        api.getP2PPeers(),
        api.getP2PDiscovered(),
        api.getSignalPeers(),
      ]);
      setStatus(s);
      setPeers(p?.peers || p || []);
      setDiscovered(d || []);
      setSignalPeers(sp || []);
      setLastRefresh(new Date());
    } catch { /* ignore silent errors */ }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [refresh]);

  useEffect(() => {
    api.getP2PNode().then(setNode).catch(() => {});
  }, []);

  /* ---- ping ---- */
  const handlePing = async (peerId) => {
    setPingLoading(prev => ({ ...prev, [peerId]: true }));
    try {
      const res = await api.pingPeer(peerId);
      setPingResults(prev => ({ ...prev, [peerId]: res }));
    } catch (e) {
      setPingResults(prev => ({ ...prev, [peerId]: { error: e.message } }));
    } finally {
      setPingLoading(prev => ({ ...prev, [peerId]: false }));
    }
  };

  /* ---- connect ---- */
  const handleConnect = async (e) => {
    e.preventDefault();
    if (!connPeerId) return;
    setLoading(true);
    setStatusMsg('');
    try {
      const addrs = connAddrs ? connAddrs.split(',').map(a => a.trim()).filter(Boolean) : [];
      await api.connectPeer(connPeerId, addrs);
      setStatusMsg('连接请求已发送');
      setConnPeerId('');
      setConnAddrs('');
      refresh();
    } catch (e) {
      setStatusMsg('连接失败: ' + e.message);
    } finally {
      setLoading(false);
    }
  };

  /* ---- announce ---- */
  const handleAnnounce = async (e) => {
    e.preventDefault();
    if (!announceHash.trim()) return;
    setLoading(true);
    setStatusMsg('');
    try {
      await api.p2pAnnounce(announceHash.trim());
      setStatusMsg('已宣布: ' + announceHash.trim().substring(0, 16) + '...');
      setAnnounceHash('');
    } catch (e) {
      setStatusMsg('宣布失败: ' + e.message);
    } finally {
      setLoading(false);
    }
  };

  /* ---- find ---- */
  const handleFind = async (e) => {
    e.preventDefault();
    if (!findHash.trim()) return;
    setFindLoading(true);
    setStatusMsg('');
    setFindResults([]);
    try {
      const res = await api.p2pRequestFile(findHash.trim());
      setFindResults(prev => [...prev, { hash: findHash.trim(), result: res, time: new Date() }]);
      setStatusMsg('查找请求已广播: ' + findHash.trim().substring(0, 16) + '...');
    } catch (e) {
      setStatusMsg('查找失败: ' + e.message);
    } finally {
      setFindLoading(false);
    }
  };

  /* ---- helpers ---- */
  const avgRtt = () => {
    const vals = Object.values(pingResults).filter(r => r && !r.error && (r.latency || r.rtt));
    if (vals.length === 0) return null;
    const sum = vals.reduce((a, r) => a + (r.latency || r.rtt), 0);
    return (sum / vals.length).toFixed(0);
  };

  const peerDisplayName = (p) => {
    const pid = p.peer_id || p.id || p;
    if (typeof pid === 'string') {
      if (pid.length > 20) return pid.substring(0, 20) + '...';
      return pid;
    }
    return String(pid);
  };

  return (
    <div className="h-full overflow-y-auto bg-gray-950">
      <div className="max-w-6xl mx-auto p-4 md:p-6 space-y-4 md:space-y-6">

        {/* ===== HEADER ===== */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <h1 className="text-xl md:text-2xl font-bold text-gray-100">P2P 网络仪表盘</h1>
            <span className="flex items-center gap-1.5 text-xs text-emerald-400">
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              实时
            </span>
          </div>
          <div className="flex items-center gap-3 text-xs text-gray-500">
            {lastRefresh && <span>更新于 {lastRefresh.toLocaleTimeString()}</span>}
            <button onClick={refresh}
              className="px-3 py-1.5 rounded-lg border border-gray-700 text-gray-400 hover:text-gray-200 hover:border-gray-500 transition-colors">
              刷新
            </button>
          </div>
        </div>

        {/* ===== STATS CARDS ===== */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3 md:gap-4">
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-blue-400" />
              已连接节点
            </div>
            <div className="text-3xl font-bold text-blue-400">{peers.length}</div>
            <div className="text-gray-600 text-xs mt-1">
              已知 {status?.conn_stats?.known_peers ?? 0}
            </div>
          </div>

          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-purple-400" />
              信令节点
            </div>
            <div className="text-3xl font-bold text-purple-400">{signalPeers.length}</div>
            <div className="text-gray-600 text-xs mt-1">
              {status?.relay_mode || 'off'}
            </div>
          </div>

          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
              活跃传输
            </div>
            <div className="text-3xl font-bold text-amber-400">{status?.active_transfers?.length || 0}</div>
            <div className="text-gray-600 text-xs mt-1">
              进行中
            </div>
          </div>

          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
              连接质量
            </div>
            <div className="text-3xl font-bold text-emerald-400">{avgRtt() || '-'}</div>
            <div className="text-gray-600 text-xs mt-1">
              {avgRtt() ? '平均 RTT (ms)' : 'WS ' + (status?.ws_connections ?? 0) + ' 连接'}
            </div>
          </div>
        </div>

        {/* ===== NODE INFO ===== */}
        {status?.peer_id && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="flex items-center justify-between mb-2">
              <h2 className="text-sm font-medium text-gray-300">节点信息</h2>
              <span className={`text-[10px] px-2 py-0.5 rounded-full font-mono ${
                status.relay_mode === 'server' ? 'bg-blue-900/50 text-blue-300' :
                status.relay_mode === 'client' ? 'bg-purple-900/50 text-purple-300' :
                'bg-gray-800 text-gray-500'
              }`}>
                中继: {status.relay_mode || 'off'}
              </span>
            </div>
            <div className="text-xs font-mono text-gray-400 break-all bg-gray-950 rounded-lg px-3 py-2 border border-gray-800">
              {status.peer_id}
            </div>
            {status.addresses?.length > 0 && (
              <div className="mt-2 pt-2 border-t border-gray-800">
                <div className="text-gray-500 text-[10px] mb-1">监听地址</div>
                <div className="space-y-0.5 max-h-20 overflow-y-auto">
                  {status.addresses.map((a, i) => (
                    <div key={i} className="text-gray-500 text-[10px] font-mono truncate">{a}</div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {/* ===== CONNECTION STATS ===== */}
        {status?.conn_stats && (
          <div className="grid grid-cols-3 gap-3">
            <div className="bg-gray-900 border border-gray-800 rounded-xl p-3 text-center">
              <div className="text-lg font-bold text-gray-300">{status.conn_stats.successful_conns ?? 0}</div>
              <div className="text-[10px] text-emerald-500">成功</div>
            </div>
            <div className="bg-gray-900 border border-gray-800 rounded-xl p-3 text-center">
              <div className="text-lg font-bold text-gray-300">{status.conn_stats.failed_conns ?? 0}</div>
              <div className="text-[10px] text-red-500">失败</div>
            </div>
            <div className="bg-gray-900 border border-gray-800 rounded-xl p-3 text-center">
              <div className="text-lg font-bold text-gray-300">{status.conn_stats.known_peers ?? 0}</div>
              <div className="text-[10px] text-gray-500">已知节点</div>
            </div>
          </div>
        )}

        {/* ===== ACTIVE TRANSFERS ===== */}
        {status?.active_transfers?.length > 0 && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">
              活跃传输
              <span className="text-gray-500 font-normal ml-1.5">({status.active_transfers.length})</span>
            </h2>
            <div className="space-y-3">
              {status.active_transfers.map((t, i) => (
                <div key={i} className="bg-gray-800/50 rounded-lg p-3">
                  <div className="flex justify-between text-xs mb-2">
                    <span className="text-gray-400 font-mono truncate mr-2">
                      {(t.hash || t.name || '').substring(0, 20)}...
                    </span>
                    <span className="text-gray-500 shrink-0">{t.progress?.toFixed(1)}%</span>
                  </div>
                  <div className="w-full bg-gray-700 rounded-full h-2 overflow-hidden">
                    <div className="bg-gradient-to-r from-blue-500 to-blue-400 h-2 rounded-full transition-all duration-500 ease-out"
                      style={{ width: `${Math.min(100, t.progress || 0)}%` }} />
                  </div>
                  <div className="flex justify-between text-[10px] text-gray-600 mt-1.5">
                    <span>{t.total_mb ? t.total_mb.toFixed(1) + ' MB' : ''}</span>
                    <span>{t.speed || ''}</span>
                    <span>{t.peers ? t.peers + ' 节点' : ''}</span>
                    <span>{t.elapsed || ''}</span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* ===== CONNECTED PEERS ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            已连接节点
            <span className="text-gray-500 font-normal ml-1.5">({peers.length})</span>
          </h2>
          {peers.length === 0 ? (
            <p className="text-gray-600 text-xs py-6 text-center">暂无 P2P 连接</p>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              {peers.map((p, i) => {
                const pid = p.peer_id || p.id || p;
                const rtt = pingResults[pid];
                const isRelayed = p.connection_type === 'relayed' || p.relayed;
                return (
                  <div key={i} className="bg-gray-800/50 rounded-lg p-3 flex items-center justify-between gap-2">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className={`w-2 h-2 rounded-full ${isRelayed ? 'bg-yellow-400' : 'bg-emerald-400'} ${!isRelayed ? 'animate-pulse' : ''}`} />
                        <span className="text-xs font-mono text-gray-300 truncate">{peerDisplayName(p)}</span>
                      </div>
                      <div className="flex items-center gap-2 mt-1 ml-4">
                        <span className={`text-[10px] px-1.5 py-0.5 rounded ${
                          isRelayed
                            ? 'bg-yellow-900/30 text-yellow-400'
                            : 'bg-emerald-900/30 text-emerald-400'
                        }`}>
                          {isRelayed ? '中继' : '直连'}
                        </span>
                        {rtt && !rtt.error && (
                          <span className={`text-[10px] flex items-center gap-1 ${getRttColor(rtt.latency || rtt.rtt)}`}>
                            <span className={`w-1 h-1 rounded-full ${getRttBg(rtt.latency || rtt.rtt)}`} />
                            {(rtt.latency || rtt.rtt)}ms
                          </span>
                        )}
                      </div>
                    </div>
                    <button
                      onClick={() => handlePing(pid)}
                      disabled={pingLoading[pid]}
                      className="text-xs text-gray-400 hover:text-amber-400 px-2 py-1 rounded border border-gray-700 hover:border-amber-600 transition-colors disabled:opacity-40 shrink-0"
                    >
                      {pingLoading[pid] ? '测速...' :
                       rtt?.error ? '重试' :
                       rtt ? `${rtt.latency || rtt.rtt}ms` : '测速'}
                    </button>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* ===== DISCOVERED PEERS ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            发现节点
            <span className="text-gray-500 font-normal ml-1.5">({discovered.length})</span>
          </h2>
          {discovered.length === 0 ? (
            <p className="text-gray-600 text-xs py-6 text-center">未发现邻居节点</p>
          ) : (
            <div className="space-y-2 max-h-60 overflow-y-auto">
              {discovered.map((d, i) => {
                const did = d.peer_id || d.id || d;
                return (
                  <div key={i} className="flex items-center justify-between bg-gray-800/50 rounded-lg px-3 py-2 gap-2">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="w-1.5 h-1.5 rounded-full bg-gray-500" />
                        <p className="text-xs font-mono text-gray-300 truncate">{peerDisplayName(d)}</p>
                      </div>
                      {d.addrs && (
                        <p className="text-[10px] text-gray-600 mt-0.5 pl-3.5 truncate">
                          {Array.isArray(d.addrs) ? d.addrs.join(', ') : String(d.addrs)}
                        </p>
                      )}
                    </div>
                    <button
                      onClick={() => { setConnPeerId(did); setConnAddrs(typeof d.addrs === 'string' ? d.addrs : (d.addrs || []).join(',')); }}
                      className="text-xs text-amber-400 hover:text-amber-300 px-2 py-1 rounded border border-amber-800/40 hover:border-amber-600 transition-colors shrink-0"
                    >
                      连接
                    </button>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* ===== FILE AVAILABILITY ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">文件可用性</h2>

          <div className="mb-4 pb-4 border-b border-gray-800">
            <h3 className="text-xs text-gray-500 mb-2">宣布文件 (Announce)</h3>
            <form onSubmit={handleAnnounce} className="flex gap-2">
              <input value={announceHash} onChange={e => setAnnounceHash(e.target.value)}
                placeholder="SHA256 Hash"
                className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                           focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
              <button type="submit" disabled={loading || !announceHash.trim()}
                className="px-4 py-2 rounded-lg text-xs font-medium bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors whitespace-nowrap">
                宣布
              </button>
            </form>
          </div>

          <div>
            <h3 className="text-xs text-gray-500 mb-2">查找文件 (Find)</h3>
            <form onSubmit={handleFind} className="flex gap-2">
              <input value={findHash} onChange={e => setFindHash(e.target.value)}
                placeholder="SHA256 Hash"
                className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                           focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
              <button type="submit" disabled={findLoading || !findHash.trim()}
                className="px-4 py-2 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 transition-colors whitespace-nowrap">
                {findLoading ? '查找中...' : '查找'}
              </button>
            </form>
            {findResults.length > 0 && (
              <div className="mt-3 space-y-1.5">
                <div className="text-[10px] text-gray-500">查找记录 ({findResults.length})</div>
                {findResults.map((r, i) => (
                  <div key={i} className="bg-gray-800/50 rounded-lg px-3 py-2 text-xs">
                    <div className="flex items-center justify-between text-gray-400 font-mono">
                      <span className="truncate mr-2">{r.hash?.substring(0, 20)}...</span>
                      <span className="text-gray-600 shrink-0">{r.time?.toLocaleTimeString()}</span>
                    </div>
                    <div className="text-[10px] text-gray-500 mt-0.5">
                      {r.result?.status || r.result?.message || '请求已发送'}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* ===== MANUAL CONNECT ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">手动连接节点</h2>
          <form onSubmit={handleConnect} className="flex flex-wrap gap-2">
            <input value={connPeerId} onChange={e => setConnPeerId(e.target.value)}
              placeholder="Peer ID"
              className="flex-1 min-w-[200px] bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <input value={connAddrs} onChange={e => setConnAddrs(e.target.value)}
              placeholder="Multiaddr (可选, 逗号分隔)"
              className="flex-1 min-w-[240px] bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <button type="submit" disabled={loading || !connPeerId.trim()}
              className="px-4 py-2 rounded-lg text-xs font-medium bg-gray-700 text-gray-200 hover:bg-gray-600 disabled:opacity-40 transition-colors">
              {loading ? '连接中...' : '连接'}
            </button>
          </form>
        </div>

        {/* ===== SIGNAL PEERS ===== */}
        {signalPeers.length > 0 && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">
              信令节点
              <span className="text-gray-500 font-normal ml-1.5">({signalPeers.length})</span>
            </h2>
            <div className="space-y-2 max-h-48 overflow-y-auto">
              {signalPeers.map((sp, i) => (
                <div key={i} className="flex items-center gap-3 bg-gray-800/50 rounded-lg px-3 py-2">
                  <span className="w-2 h-2 rounded-full bg-purple-400 animate-pulse shrink-0" />
                  <span className="text-xs font-mono text-gray-300 truncate">
                    {sp.peer_id || sp.id || (typeof sp === 'string' ? sp : JSON.stringify(sp))}
                  </span>
                  {(sp.addr || sp.address) && (
                    <span className="text-[10px] text-gray-600 truncate">{sp.addr || sp.address}</span>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {/* ===== STATUS MESSAGE ===== */}
        {statusMsg && (
          <div className={`p-3 rounded-lg text-sm border transition-opacity ${
            statusMsg.includes('失败')
              ? 'bg-red-900/20 border-red-900/40 text-red-400'
              : 'bg-emerald-900/20 border-emerald-900/40 text-emerald-300'
          }`}>
            {statusMsg}
          </div>
        )}

      </div>
    </div>
  );
}
