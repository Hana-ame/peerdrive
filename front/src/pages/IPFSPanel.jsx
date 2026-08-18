// IPFS 面板：CID 固定/取消、已固定列表、网关健康状态。
// 2026-08-19：原「节点状态/已连接/已发现/宣布/查找/手动连接」区段全部依赖已删除的
// libp2p 端点（/p2p/status、/p2p/peers、/p2p/announce 等），随 libp2p 栈清理移除；
// 本面板保留仍存活的 IPFS HTTP 能力（/ipfs/pin、/ipfs/pins、/ipfs/gateways）。
import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';

export default function IPFSPanel() {
  const [pins, setPins] = useState([]);
  const [cidInput, setCidInput] = useState('');
  const [pinningCid, setPinningCid] = useState(false);
  const [unpinningCids, setUnpinningCids] = useState({});
  const [gateways, setGateways] = useState([]);
  const [statusMsg, setStatusMsg] = useState('');
  const [lastRefresh, setLastRefresh] = useState(null);

  const refreshPins = useCallback(async () => {
    try {
      const data = await api.listPins();
      setPins(data?.pins || []);
    } catch { /* ignore */ }
  }, []);

  const refreshGateways = useCallback(async () => {
    try {
      const data = await api.getIPFSGatewayStatus();
      setGateways(data?.gateways || []);
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    refreshPins();
    refreshGateways();
    const t = setInterval(() => {
      refreshPins();
      refreshGateways();
    }, 5000);
    return () => clearInterval(t);
  }, [refreshPins, refreshGateways]);

  /* ---- pin / unpin ---- */
  const handlePinCID = async (e) => {
    e.preventDefault();
    if (!cidInput.trim()) return;
    setPinningCid(true);
    setStatusMsg('');
    try {
      const res = await api.pinCID(cidInput.trim());
      setStatusMsg('已固定 CID: ' + res.cid.substring(0, 24) + '... (' + (res.size || 0) + ' bytes)');
      setCidInput('');
      refreshPins();
    } catch (e) {
      setStatusMsg('固定失败: ' + e.message);
    } finally {
      setPinningCid(false);
    }
  };

  const handleUnpinCID = async (cid) => {
    setUnpinningCids(prev => ({ ...prev, [cid]: true }));
    setStatusMsg('');
    try {
      await api.unpinCID(cid);
      setStatusMsg('已取消固定: ' + cid.substring(0, 24) + '...');
      refreshPins();
    } catch (e) {
      setStatusMsg('取消固定失败: ' + e.message);
    } finally {
      setUnpinningCids(prev => ({ ...prev, [cid]: false }));
    }
  };

  const gwHealthyCount = gateways.filter(g => g.healthy).length;

  return (
    <div className="h-full overflow-y-auto bg-gray-950">
      <div className="max-w-6xl mx-auto p-4 md:p-6 space-y-4 md:space-y-6">

        {/* ===== HEADER ===== */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <h1 className="text-xl md:text-2xl font-bold text-gray-100">IPFS 面板</h1>
            <span className="flex items-center gap-1.5 text-xs text-emerald-400">
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              自动刷新
            </span>
          </div>
          <div className="flex items-center gap-3 text-xs text-gray-500">
            {lastRefresh && <span>更新于 {lastRefresh.toLocaleTimeString()}</span>}
            <button onClick={() => { refreshPins(); refreshGateways(); }}
              className="px-3 py-1.5 rounded-lg border border-gray-700 text-gray-400 hover:text-gray-200 hover:border-gray-500 transition-colors">
              刷新
            </button>
          </div>
        </div>

        {/* ===== IPFS GATEWAY STATUS ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">IPFS 网关状态</h2>
          <div className="flex flex-wrap items-center gap-4">
            {gateways.length === 0 ? (
              <span className="text-xs text-gray-500">未配置网关</span>
            ) : (
              gateways.map((gw, i) => (
                <div key={i} className="flex items-center gap-2 bg-gray-800/50 rounded-lg px-3 py-2">
                  <span className={`w-2.5 h-2.5 rounded-full ${gw.healthy ? 'bg-emerald-400' : 'bg-red-400'} ${gw.healthy ? '' : 'animate-pulse'}`} />
                  <div className="flex flex-col">
                    <span className="text-xs text-gray-300 font-mono">{gw.url.replace('https://', '')}</span>
                    <span className="text-[10px] text-gray-500">{gw.healthy ? (gw.latency || 'OK') : '不可用'}</span>
                  </div>
                </div>
              ))
            )}
            <span className="text-[10px] text-gray-600 ml-auto">
              {gwHealthyCount}/{gateways.length} 在线
            </span>
          </div>
        </div>

        {/* ===== CID PIN INPUT ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">固定 CID</h2>
          <form onSubmit={handlePinCID} className="flex gap-2">
            <input value={cidInput} onChange={e => setCidInput(e.target.value)}
              placeholder="输入 IPFS CID（如 Qm... 或 bafy...）"
              className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <button type="submit" disabled={pinningCid || !cidInput.trim()}
              className="px-4 py-2 rounded-lg text-xs font-medium bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors whitespace-nowrap">
              {pinningCid ? '下载中...' : '固定'}
            </button>
          </form>
        </div>

        {/* ===== PINNED CIDS ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            已固定的 CID
            <span className="text-gray-500 font-normal ml-1.5">({pins.length})</span>
          </h2>
          {pins.length === 0 ? (
            <p className="text-gray-600 text-xs py-6 text-center">暂无已固定的 CID</p>
          ) : (
            <div className="space-y-2 max-h-64 overflow-y-auto">
              {pins.map((pin) => (
                <div key={pin.cid} className="flex items-center justify-between bg-gray-800/50 rounded-lg px-3 py-2 gap-2">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="w-1.5 h-1.5 rounded-full bg-blue-400 shrink-0" />
                      <span className="text-xs font-mono text-blue-300 truncate">{pin.cid}</span>
                    </div>
                    <div className="flex items-center gap-3 mt-0.5 text-[10px] text-gray-500">
                      <span>{(pin.size / 1024).toFixed(1)} KB</span>
                      <span>固定于 {pin.pinned_at ? new Date(pin.pinned_at).toLocaleString() : '-'}</span>
                    </div>
                  </div>
                  <button
                    onClick={() => handleUnpinCID(pin.cid)}
                    disabled={unpinningCids[pin.cid]}
                    className="text-[10px] text-red-400 hover:text-red-300 px-2 py-1 rounded border border-red-800/40 hover:border-red-600 transition-colors shrink-0 disabled:opacity-40">
                    {unpinningCids[pin.cid] ? '取消中...' : '取消固定'}
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

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