// BitTorrent DHT 面板：BT DHT 状态监控、Infohash 宣布与查找
import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';

export default function BTPanel() {
  const [status, setStatus] = useState(null);
  const [announceHash, setAnnounceHash] = useState('');
  const [announceResult, setAnnounceResult] = useState(null);
  const [announceLoading, setAnnounceLoading] = useState(false);
  const [findHash, setFindHash] = useState('');
  const [findResult, setFindResult] = useState(null);
  const [findLoading, setFindLoading] = useState(false);
  const [statusMsg, setStatusMsg] = useState('');
  const [lastRefresh, setLastRefresh] = useState(null);

  const refresh = useCallback(async () => {
    try {
      const s = await api.getBTStatus();
      setStatus(s);
      setLastRefresh(new Date());
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 10000);
    return () => clearInterval(t);
  }, [refresh]);

  /* ---- announce ---- */
  const handleAnnounce = async (e) => {
    e.preventDefault();
    if (!announceHash.trim()) return;
    setAnnounceLoading(true);
    setAnnounceResult(null);
    setStatusMsg('');
    try {
      const res = await api.btAnnounce(announceHash.trim());
      setAnnounceResult(res);
      setStatusMsg('BT DHT 宣布成功');
    } catch (e) {
      setAnnounceResult({ error: e.message });
      setStatusMsg('BT 宣布失败: ' + e.message);
    } finally {
      setAnnounceLoading(false);
    }
  };

  /* ---- find ---- */
  const handleFind = async (e) => {
    e.preventDefault();
    if (!findHash.trim()) return;
    setFindLoading(true);
    setFindResult(null);
    setStatusMsg('');
    try {
      const res = await api.btFind(findHash.trim());
      setFindResult(res);
      setStatusMsg('BT DHT 查找完成');
    } catch (e) {
      setFindResult({ error: e.message });
      setStatusMsg('BT 查找失败: ' + e.message);
    } finally {
      setFindLoading(false);
    }
  };

  /* ---- helpers ---- */
  const peerList = () => {
    if (!findResult) return [];
    const raw = findResult.peers || findResult.results || findResult;
    if (Array.isArray(raw)) return raw;
    if (typeof raw === 'object' && !raw.error) {
      return Object.entries(raw).map(([k, v]) => ({ id: k, addr: v }));
    }
    return [];
  };

  const formatUptime = (seconds) => {
    if (seconds == null) return '-';
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = Math.floor(seconds % 60);
    if (h > 0) return `${h}h ${m}m`;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
  };

  return (
    <div className="h-full overflow-y-auto bg-gray-950">
      <div className="max-w-4xl mx-auto p-4 md:p-6 space-y-4 md:space-y-6">

        {/* ===== HEADER ===== */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <h1 className="text-xl md:text-2xl font-bold text-gray-100">BitTorrent DHT 面板</h1>
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

        {/* ===== STATUS CARD ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">BT DHT 状态</h2>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
            <div className="bg-gray-800/50 rounded-lg p-3 text-center">
              <div className="text-[10px] text-gray-500 mb-1">状态</div>
              <span className={`text-sm font-bold ${
                status?.enabled ? 'text-emerald-400' : 'text-red-400'
              }`}>
                {status?.enabled ? '已启用' : '未启用'}
              </span>
            </div>
            <div className="bg-gray-800/50 rounded-lg p-3 text-center">
              <div className="text-[10px] text-gray-500 mb-1">节点数</div>
              <div className="text-lg font-bold text-blue-400">{status?.node_count ?? '-'}</div>
            </div>
            <div className="bg-gray-800/50 rounded-lg p-3 text-center">
              <div className="text-[10px] text-gray-500 mb-1">运行时间</div>
              <div className="text-lg font-bold text-amber-400">{formatUptime(status?.uptime)}</div>
            </div>
            <div className="bg-gray-800/50 rounded-lg p-3 text-center">
              <div className="text-[10px] text-gray-500 mb-1">活跃查询</div>
              <div className="text-lg font-bold text-purple-400">{status?.active_queries ?? '-'}</div>
            </div>
          </div>
          {status?.listen_addr && (
            <div className="bg-gray-950 rounded-lg px-3 py-2 border border-gray-800">
              <div className="text-[10px] text-gray-500 mb-0.5">监听地址</div>
              <div className="text-xs font-mono text-gray-400 break-all">{status.listen_addr}</div>
            </div>
          )}
          {status?.routing_table && (
            <div className="mt-3 pt-3 border-t border-gray-800">
              <div className="text-[10px] text-gray-500 mb-1.5">路由表</div>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                {Object.entries(status.routing_table).map(([k, v]) => (
                  <div key={k} className="bg-gray-800/30 rounded px-2 py-1 text-center">
                    <div className="text-[10px] text-gray-500">{k}</div>
                    <div className="text-xs font-bold text-gray-300">{String(v)}</div>
                  </div>
                ))}
              </div>
            </div>
          )}
          {status?.buckets && (
            <div className="mt-3 pt-3 border-t border-gray-800">
              <div className="text-[10px] text-gray-500 mb-1.5">K-Buckets</div>
              <div className="text-xs text-gray-400">{JSON.stringify(status.buckets).substring(0, 200)}</div>
            </div>
          )}
        </div>

        {/* ===== ANNOUNCE ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">宣布到 BT DHT</h2>
          <form onSubmit={handleAnnounce} className="flex gap-2">
            <input value={announceHash} onChange={e => setAnnounceHash(e.target.value)}
              placeholder="Infohash (十六进制)"
              className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <button type="submit" disabled={announceLoading || !announceHash.trim()}
              className="px-4 py-2 rounded-lg text-xs font-medium bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors whitespace-nowrap">
              {announceLoading ? '宣布中...' : '宣布'}
            </button>
          </form>
          {announceResult && (
            <div className="mt-3 bg-gray-800/50 rounded-lg p-3 text-xs">
              <div className="text-gray-500 mb-1">结果</div>
              <pre className="text-gray-300 font-mono text-[10px] whitespace-pre-wrap">
                {JSON.stringify(announceResult, null, 2)}
              </pre>
            </div>
          )}
        </div>

        {/* ===== FIND ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">在 BT DHT 中查找</h2>
          <form onSubmit={handleFind} className="flex gap-2">
            <input value={findHash} onChange={e => setFindHash(e.target.value)}
              placeholder="Infohash (十六进制)"
              className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <button type="submit" disabled={findLoading || !findHash.trim()}
              className="px-4 py-2 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 transition-colors whitespace-nowrap">
              {findLoading ? '查找中...' : '查找'}
            </button>
          </form>

          {findLoading && (
            <div className="mt-3 text-xs text-gray-500 text-center py-4">正在查询 BT DHT 网络...</div>
          )}

          {findResult && !findLoading && (
            <div className="mt-3">
              {findResult.error ? (
                <div className="bg-red-900/20 border border-red-900/40 rounded-lg p-3 text-xs text-red-400">
                  查找失败: {findResult.error}
                </div>
              ) : (
                <>
                  <div className="text-[10px] text-gray-500 mb-2">
                    找到 {peerList().length} 个节点
                    {findResult.query_time && <span> (耗时 {findResult.query_time}ms)</span>}
                  </div>
                  {peerList().length === 0 ? (
                    <p className="text-gray-600 text-xs py-4 text-center">未找到提供该 Infohash 的节点</p>
                  ) : (
                    <div className="space-y-1.5 max-h-72 overflow-y-auto">
                      {peerList().map((peer, i) => (
                        <div key={i} className="flex items-center gap-3 bg-gray-800/50 rounded-lg px-3 py-2 text-xs">
                          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 shrink-0" />
                          <span className="font-mono text-gray-300 truncate">
                            {peer.id || peer.peer_id || peer.node_id || `节点 ${i + 1}`}
                          </span>
                          <span className="text-gray-500 shrink-0">
                            {(peer.addr || peer.address || peer.ip || '') +
                             (peer.port ? ':' + peer.port : '')}
                          </span>
                        </div>
                      ))}
                    </div>
                  )}
                </>
              )}
            </div>
          )}
        </div>

        {/* ===== ACTIVE DOWNLOADS ===== */}
        {status?.active_downloads?.length > 0 && (
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <h2 className="text-sm font-medium text-gray-300 mb-3">
              活跃下载
              <span className="text-gray-500 font-normal ml-1.5">({status.active_downloads.length})</span>
            </h2>
            <div className="space-y-2">
              {status.active_downloads.map((d, i) => (
                <div key={i} className="bg-gray-800/50 rounded-lg p-3">
                  <div className="flex justify-between items-center text-xs mb-1.5">
                    <span className="font-mono text-gray-400 truncate">
                      {d.hash || d.name || '#' + i}
                    </span>
                    <span className="text-gray-500 shrink-0 ml-2">
                      {d.progress != null ? d.progress.toFixed(1) + '%' : ''}
                    </span>
                  </div>
                  {d.progress != null && (
                    <div className="w-full bg-gray-700 rounded-full h-1.5 overflow-hidden">
                      <div className="bg-gradient-to-r from-blue-500 to-cyan-400 h-1.5 rounded-full transition-all"
                        style={{ width: `${Math.min(100, d.progress)}%` }} />
                    </div>
                  )}
                  <div className="flex gap-3 text-[10px] text-gray-600 mt-1">
                    {d.speed && <span>{d.speed}</span>}
                    {d.peers != null && <span>{d.peers} 节点</span>}
                    {d.total != null && <span>{d.total}</span>}
                  </div>
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
