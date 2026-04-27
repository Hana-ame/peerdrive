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

export default function IPFSPanel() {
  const [status, setStatus] = useState(null);
  const [node, setNode] = useState(null);
  const [peers, setPeers] = useState([]);
  const [discovered, setDiscovered] = useState([]);
  const [wsInfo, setWsInfo] = useState(null);
  const [pingResults, setPingResults] = useState({});
  const [pingLoading, setPingLoading] = useState({});
  const [loading, setLoading] = useState(false);
  const [statusMsg, setStatusMsg] = useState('');
  const [lastRefresh, setLastRefresh] = useState(null);

  /* ---- connect form ---- */
  const [connPeerId, setConnPeerId] = useState('');
  const [connAddrs, setConnAddrs] = useState('');

  /* ---- announce ---- */
  const [announceHash, setAnnounceHash] = useState('');

  /* ---- find ---- */
  const [findHash, setFindHash] = useState('');
  const [findResults, setFindResults] = useState([]);
  const [findLoading, setFindLoading] = useState(false);

  /* ---- refresh ---- */
  const refresh = useCallback(async () => {
    try {
      const [s, p, d] = await Promise.all([
        api.getP2PStatus(),
        api.getP2PPeers(),
        api.getP2PDiscovered(),
      ]);
      setStatus(s);
      setPeers(Array.isArray(p) ? p : p?.peers || []);
      setDiscovered(Array.isArray(d) ? d : d?.peers || []);
      setLastRefresh(new Date());
    } catch { /* ignore silent errors */ }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [refresh]);

  useEffect(() => {
    api.getP2PNode().then(setNode).catch(() => {});
    api.getWSInfo().then(setWsInfo).catch(() => {});
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
      setStatusMsg('已宣布到 DHT: ' + announceHash.trim().substring(0, 20) + '...');
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
    try {
      const res = await api.p2pRequestFile(findHash.trim());
      setFindResults(prev => [{ hash: findHash.trim(), result: res, time: new Date() }, ...prev].slice(0, 20));
      setStatusMsg('查找请求已广播');
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

  const displayPeerId = (id) => {
    if (!id) return '-';
    const s = typeof id === 'string' ? id : String(id);
    if (s.length <= 24) return s;
    return s.substring(0, 24) + '...';
  };

  return (
    <div className="h-full overflow-y-auto bg-gray-950">
      <div className="max-w-6xl mx-auto p-4 md:p-6 space-y-4 md:space-y-6">

        {/* ===== HEADER ===== */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <h1 className="text-xl md:text-2xl font-bold text-gray-100">IPFS / libp2p 面板</h1>
            <span className="flex items-center gap-1.5 text-xs text-emerald-400">
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              自动刷新
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

        {/* ===== NODE STATUS ===== */}
        {status?.peer_id && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <h2 className="text-sm font-medium text-gray-300">节点状态</h2>
              <span className={`text-[10px] px-2 py-0.5 rounded-full font-mono ${
                status.relay_mode === 'server' ? 'bg-blue-900/50 text-blue-300' :
                status.relay_mode === 'client' ? 'bg-purple-900/50 text-purple-300' :
                'bg-gray-800 text-gray-500'
              }`}>
                {status.relay_mode === 'server' ? '中继服务器' :
                 status.relay_mode === 'client' ? '中继客户端' :
                 '中继: ' + (status.relay_mode || 'off')}
              </span>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <div className="text-[10px] text-gray-500 mb-1">Peer ID</div>
                <div className="text-xs font-mono text-indigo-300 break-all bg-gray-950 rounded-lg px-3 py-2 border border-gray-800">
                  {status.peer_id}
                </div>
              </div>
              <div>
                <div className="text-[10px] text-gray-500 mb-1">协议版本</div>
                <div className="text-xs font-mono text-gray-400 break-all bg-gray-950 rounded-lg px-3 py-2 border border-gray-800">
                  {node?.agent_version || status.agent_version || '-'}
                </div>
              </div>
            </div>
            {status.addresses?.length > 0 && (
              <div className="mt-3">
                <div className="text-[10px] text-gray-500 mb-1">监听地址 ({status.addresses.length})</div>
                <div className="space-y-0.5 max-h-24 overflow-y-auto">
                  {status.addresses.map((a, i) => (
                    <div key={i} className="text-[10px] text-gray-600 font-mono truncate">{a}</div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {/* ===== STATS ===== */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3 md:gap-4">
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-blue-400" />
              已连接节点
            </div>
            <div className="text-3xl font-bold text-blue-400">{peers.length}</div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
              已发现节点
            </div>
            <div className="text-3xl font-bold text-amber-400">{discovered.length}</div>
            <div className="text-gray-600 text-xs mt-1">{status?.conn_stats?.known_peers ?? 0} 已知</div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
              连接质量
            </div>
            <div className="text-3xl font-bold text-emerald-400">{avgRtt() || '-'}</div>
            <div className="text-gray-600 text-xs mt-1">{avgRtt() ? '平均 RTT (ms)' : '暂无数据'}</div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1 flex items-center gap-1.5">
              <span className="w-1.5 h-1.5 rounded-full bg-purple-400" />
              WebSocket
            </div>
            <div className="text-3xl font-bold text-purple-400">{wsInfo?.connections ?? status?.ws_connections ?? 0}</div>
            <div className="text-gray-600 text-xs mt-1">{wsInfo?.listener ? '监听中' : status?.hole_punch_status || '-'}</div>
          </div>
        </div>

        {/* ===== CONNECTED PEERS TABLE ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            已连接节点
            <span className="text-gray-500 font-normal ml-1.5">({peers.length})</span>
          </h2>
          {peers.length === 0 ? (
            <p className="text-gray-600 text-xs py-6 text-center">暂无 P2P 连接</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-gray-500 border-b border-gray-800">
                    <th className="text-left py-2 pr-2 font-medium">Peer ID</th>
                    <th className="text-left py-2 px-2 font-medium hidden md:table-cell">地址</th>
                    <th className="text-center py-2 px-2 font-medium">类型</th>
                    <th className="text-center py-2 px-2 font-medium">延迟</th>
                    <th className="text-right py-2 pl-2 font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {peers.map((p, i) => {
                    const pid = p.peer_id || p.id || p;
                    const rtt = pingResults[pid];
                    const isRelayed = p.connection_type === 'relayed' || p.relayed;
                    return (
                      <tr key={i} className="border-b border-gray-800/50 hover:bg-gray-800/30">
                        <td className="py-2.5 pr-2">
                          <div className="flex items-center gap-2">
                            <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                              isRelayed ? 'bg-yellow-400' : 'bg-emerald-400'
                            }`} />
                            <span className="font-mono text-gray-300">{displayPeerId(pid)}</span>
                          </div>
                        </td>
                        <td className="py-2.5 px-2 hidden md:table-cell">
                          <span className="text-gray-600 font-mono text-[10px] truncate block max-w-[200px] xl:max-w-[300px]">
                            {p.addrs ? (Array.isArray(p.addrs) ? p.addrs[0] : String(p.addrs)) : '-'}
                          </span>
                        </td>
                        <td className="py-2.5 px-2 text-center">
                          <span className={`text-[10px] px-1.5 py-0.5 rounded ${
                            isRelayed
                              ? 'bg-yellow-900/30 text-yellow-400'
                              : 'bg-emerald-900/30 text-emerald-400'
                          }`}>
                            {isRelayed ? '中继' : '直连'}
                          </span>
                        </td>
                        <td className="py-2.5 px-2 text-center">
                          {rtt && !rtt.error ? (
                            <span className={`flex items-center justify-center gap-1 ${getRttColor(rtt.latency || rtt.rtt)}`}>
                              <span className={`w-1 h-1 rounded-full ${getRttBg(rtt.latency || rtt.rtt)}`} />
                              {rtt.latency || rtt.rtt}ms
                            </span>
                          ) : rtt?.error ? (
                            <span className="text-red-400" title={rtt.error}>超时</span>
                          ) : (
                            <span className="text-gray-600">-</span>
                          )}
                        </td>
                        <td className="py-2.5 pl-2 text-right">
                          <button
                            onClick={() => handlePing(pid)}
                            disabled={pingLoading[pid]}
                            className="text-[10px] text-gray-400 hover:text-amber-400 px-2 py-1 rounded border border-gray-700 hover:border-amber-600 transition-colors disabled:opacity-40">
                            {pingLoading[pid] ? '...' : rtt ? '重测' : '测速'}
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* ===== DISCOVERED PEERS ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            已发现节点
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
                        <span className="w-1.5 h-1.5 rounded-full bg-gray-500 shrink-0" />
                        <span className="text-xs font-mono text-gray-300 truncate">{displayPeerId(did)}</span>
                      </div>
                      {d.addrs && (
                        <p className="text-[10px] text-gray-600 mt-0.5 pl-3.5 truncate">
                          {Array.isArray(d.addrs) ? d.addrs.join(', ') : String(d.addrs)}
                        </p>
                      )}
                    </div>
                    <button
                      onClick={() => { setConnPeerId(did); setConnAddrs(typeof d.addrs === 'string' ? d.addrs : (d.addrs || []).join(',')); }}
                      className="text-[10px] text-amber-400 hover:text-amber-300 px-2 py-1 rounded border border-amber-800/40 hover:border-amber-600 transition-colors shrink-0">
                      连接
                    </button>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* ===== ACTIONS ===== */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {/* === Announce === */}
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">宣布到 DHT</h2>
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

          {/* === Find === */}
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">查找提供者</h2>
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
              <div className="mt-3 space-y-1.5 max-h-48 overflow-y-auto">
                {findResults.map((r, i) => (
                  <div key={i} className="bg-gray-800/50 rounded px-2.5 py-1.5 text-xs">
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono text-gray-400 truncate">{r.hash?.substring(0, 24)}...</span>
                      <span className="text-gray-600 shrink-0 text-[10px]">{r.time?.toLocaleTimeString()}</span>
                    </div>
                    <div className="text-[10px] text-gray-500 mt-0.5">
                      {r.result?.status || r.result?.message || JSON.stringify(r.result).substring(0, 80)}
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

        {/* ===== HOLE PUNCH / WS INFO ===== */}
        {status?.hole_punch_status && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-2">NAT 打洞状态</h2>
            <div className="flex items-center gap-3 text-xs">
              <span className={`px-2 py-1 rounded-full ${
                status.hole_punch_status === 'completed' || status.hole_punch_status === 'success'
                  ? 'bg-emerald-900/30 text-emerald-400'
                  : status.hole_punch_status === 'failed'
                  ? 'bg-red-900/30 text-red-400'
                  : 'bg-gray-800 text-gray-400'
              }`}>
                {status.hole_punch_status}
              </span>
              <span className="text-gray-500">
                WS 连接: {wsInfo?.connections ?? status?.ws_connections ?? 0}
              </span>
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
