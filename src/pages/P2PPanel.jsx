// P2P 双栈总控：同时管理 IPFS DHT 和 BT DHT，支持双网宣布/查找/文件共享生命周期
import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';

// 脉冲动画圆点组件
function PulsingDot({ color = 'emerald' }) {
  return (
    <span className={`inline-block w-2 h-2 rounded-full bg-${color}-400 animate-pulse mr-1.5`} />
  );
}

export default function P2PPanel() {
  /* ---- IPFS state ---- */
  const [ipfsStatus, setIpfsStatus] = useState(null);
  const [ipfsPeers, setIpfsPeers] = useState([]);

  /* ---- BT state ---- */
  const [btStatus, setBtStatus] = useState(null);

  /* ---- dual actions ---- */
  const [announceHash, setAnnounceHash] = useState('');
  const [announceResult, setAnnounceResult] = useState(null);
  const [announceLoading, setAnnounceLoading] = useState(false);

  const [findHash, setFindHash] = useState('');
  const [findIpfs, setFindIpfs] = useState(null);
  const [findBt, setFindBt] = useState(null);
  const [findLoading, setFindLoading] = useState(false);

  /* ---- file share lifecycle ---- */
  const [lifecycle, setLifecycle] = useState({ step: 'idle', hash: null, file: null, msg: '' });

  const [statusMsg, setStatusMsg] = useState('');
  const [lastRefresh, setLastRefresh] = useState(null);

  /* ---- refresh ---- */
  const refresh = useCallback(async () => {
    try {
      const [ipfs, peers, bt] = await Promise.all([
        api.getP2PStatus(),
        api.getP2PPeers(),
        api.getBTStatus(),
      ]);
      setIpfsStatus(ipfs);
      setIpfsPeers(Array.isArray(peers) ? peers : peers?.peers || []);
      setBtStatus(bt);
      setLastRefresh(new Date());
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [refresh]);

  /* ---- dual announce ---- */
  const handleDualAnnounce = async (e) => {
    e.preventDefault();
    if (!announceHash.trim()) return;
    setAnnounceLoading(true);
    setAnnounceResult(null);
    setStatusMsg('');
    try {
      const res = await api.dualAnnounce(announceHash.trim());
      setAnnounceResult(res);
      setStatusMsg('已在两个网络宣布');
    } catch (err) {
      setAnnounceResult({ error: err.message });
      setStatusMsg('宣布失败: ' + err.message);
    } finally {
      setAnnounceLoading(false);
    }
  };

  /* ---- dual find ---- */
  const handleDualFind = async (e) => {
    e.preventDefault();
    if (!findHash.trim()) return;
    setFindLoading(true);
    setFindIpfs(null);
    setFindBt(null);
    setStatusMsg('');
    try {
      const res = await api.dualFind(findHash.trim());
      setFindIpfs(res?.ipfs || res?.ipfs_peers || null);
      setFindBt(res?.bt || res?.bt_peers || null);
      if (!res?.ipfs && !res?.bt) {
        setStatusMsg('两个网络均未找到该 Hash');
      } else {
        setStatusMsg('双网查找完成');
      }
    } catch (err) {
      setStatusMsg('查找失败: ' + err.message);
    } finally {
      setFindLoading(false);
    }
  };

  /* ---- file share lifecycle ---- */
  const handleUploadAndShare = async () => {
    setLifecycle({ step: 'selecting', hash: null, file: null, msg: '请选择文件...' });
    const input = document.createElement('input');
    input.type = 'file';
    input.onchange = async () => {
      const file = input.files?.[0];
      if (!file) { setLifecycle({ step: 'idle', hash: null, file: null, msg: '' }); return; }
      setLifecycle({ step: 'uploading', hash: null, file, msg: '正在上传...' });
      try {
        const uploadRes = await api.uploadFile(file);
        const hash = uploadRes.hash || uploadRes.sha256;
        if (!hash) throw new Error('上传响应缺少 hash');
        setLifecycle({ step: 'announcing', hash, file, msg: `Hash: ${hash.substring(0, 20)}... 正在双网宣布...` });
        await api.dualAnnounce(hash);
        setLifecycle({ step: 'finding', hash, file, msg: '已宣布，正在查找验证...' });
        const findRes = await api.dualFind(hash);
        const ipfsFound = findRes?.ipfs?.length || (findRes?.ipfs_peers?.length) || 0;
        const btFound = findRes?.bt?.length || (findRes?.bt_peers?.length) || 0;
        setLifecycle({
          step: 'done', hash, file,
          msg: `完成! IPFS: ${ipfsFound} 提供者, BT: ${btFound} 节点`
        });
      } catch (err) {
        setLifecycle({ step: 'error', hash: null, file, msg: '失败: ' + err.message });
      }
    };
    input.click();
  };

  /* ---- helpers ---- */
  const btPeerList = () => {
    if (!findBt) return [];
    const raw = findBt.peers || findBt.results || findBt;
    if (Array.isArray(raw)) return raw;
    if (typeof raw === 'object' && !raw.error) {
      return Object.entries(raw).map(([k, v]) => ({ id: k, addr: v }));
    }
    return [];
  };

  const ipfsPeerList = () => {
    if (!findIpfs) return [];
    if (Array.isArray(findIpfs)) return findIpfs;
    if (Array.isArray(findIpfs.peers)) return findIpfs.peers;
    if (Array.isArray(findIpfs.providers)) return findIpfs.providers;
    return [];
  };

  const totalPeers = () => {
    let ipfs = ipfsPeers.length;
    let bt = btStatus?.node_count ?? 0;
    return ipfs + bt;
  };

  return (
    <div className="h-full overflow-y-auto bg-gray-950">
      <div className="max-w-6xl mx-auto p-4 md:p-6 space-y-4 md:space-y-6">

        {/* ===== HEADER ===== */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <h1 className="text-xl md:text-2xl font-bold text-gray-100">P2P 双栈总控</h1>
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

        {/* ===== QUICK STATS ===== */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3 md:gap-4">
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1">IPFS 节点</div>
            <div className="text-3xl font-bold text-blue-400">{ipfsPeers.length}</div>
            <div className="text-gray-600 text-xs mt-1">已连接</div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1">BT DHT 节点</div>
            <div className="text-3xl font-bold text-amber-400">{btStatus?.node_count ?? '-'}</div>
            <div className="text-gray-600 text-xs mt-1">
              {btStatus?.enabled ? '已启用' : '未启用'}
            </div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1">网络覆盖</div>
            <div className="text-3xl font-bold text-emerald-400">{totalPeers()}</div>
            <div className="text-gray-600 text-xs mt-1">总计节点</div>
          </div>
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="text-gray-500 text-xs mb-1">中继模式</div>
            <div className="text-3xl font-bold text-purple-400">
              {ipfsStatus?.relay_mode === 'server' ? 'S' :
               ipfsStatus?.relay_mode === 'client' ? 'C' : '-'}
            </div>
            <div className="text-gray-600 text-xs mt-1">{ipfsStatus?.relay_mode || 'off'}</div>
          </div>
        </div>

        {/* ===== SIDE-BY-SIDE STATUS ===== */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {/* --- IPFS Status --- */}
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <h2 className="text-sm font-medium text-gray-300 flex items-center gap-1.5">
                <PulsingDot color="blue" />
                IPFS / libp2p
              </h2>
              <span className="text-[10px] text-gray-500">{ipfsPeers.length} 节点</span>
            </div>
            {ipfsStatus?.peer_id ? (
              <div className="space-y-2">
                <div className="bg-gray-950 rounded-lg px-3 py-2 border border-gray-800">
                  <div className="text-[10px] text-gray-500 mb-0.5">Peer ID</div>
                  <div className="text-xs font-mono text-indigo-300 break-all">
                    {ipfsStatus.peer_id.substring(0, 40)}...
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div className="bg-gray-800/50 rounded px-2.5 py-1.5 text-center">
                    <div className="text-gray-500 text-[10px]">已发现</div>
                    <div className="text-blue-400 font-bold">{ipfsStatus.conn_stats?.known_peers ?? 0}</div>
                  </div>
                  <div className="bg-gray-800/50 rounded px-2.5 py-1.5 text-center">
                    <div className="text-gray-500 text-[10px]">WS 连接</div>
                    <div className="text-purple-400 font-bold">{ipfsStatus.ws_connections ?? 0}</div>
                  </div>
                </div>
                {ipfsStatus.addresses?.slice(0, 2).map((a, i) => (
                  <div key={i} className="text-[10px] text-gray-600 font-mono truncate">{a}</div>
                ))}
                {ipfsStatus.hole_punch_status && (
                  <div className="text-[10px] text-gray-500">
                    打洞: {ipfsStatus.hole_punch_status}
                  </div>
                )}
              </div>
            ) : (
              <p className="text-gray-600 text-xs py-4 text-center">正在获取 IPFS 状态...</p>
            )}
          </div>

          {/* --- BT Status --- */}
          <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <h2 className="text-sm font-medium text-gray-300 flex items-center gap-1.5">
                <PulsingDot color={btStatus?.enabled ? 'emerald' : 'gray'} />
                BitTorrent DHT
              </h2>
              <span className="text-[10px] text-gray-500">{btStatus?.node_count ?? '-'} 节点</span>
            </div>
            {btStatus ? (
              <div className="space-y-2">
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div className="bg-gray-800/50 rounded px-2.5 py-1.5 text-center">
                    <div className="text-gray-500 text-[10px]">状态</div>
                    <span className={`font-bold ${btStatus.enabled ? 'text-emerald-400' : 'text-red-400'}`}>
                      {btStatus.enabled ? '在线' : '离线'}
                    </span>
                  </div>
                  <div className="bg-gray-800/50 rounded px-2.5 py-1.5 text-center">
                    <div className="text-gray-500 text-[10px]">运行时间</div>
                    <div className="text-amber-400 font-bold">
                      {btStatus.uptime ? Math.floor(btStatus.uptime / 60) + 'm' : '-'}
                    </div>
                  </div>
                </div>
                {btStatus.listen_addr && (
                  <div className="bg-gray-950 rounded-lg px-3 py-2 border border-gray-800">
                    <div className="text-[10px] text-gray-500 mb-0.5">监听地址</div>
                    <div className="text-xs font-mono text-gray-400 break-all">{btStatus.listen_addr}</div>
                  </div>
                )}
                {btStatus.routing_table && (
                  <div className="text-[10px] text-gray-500">
                    {Object.entries(btStatus.routing_table).map(([k, v]) => (
                      <span key={k} className="mr-3">{k}: {String(v)}</span>
                    ))}
                  </div>
                )}
              </div>
            ) : (
              <p className="text-gray-600 text-xs py-4 text-center">正在获取 BT DHT 状态...</p>
            )}
          </div>
        </div>

        {/* ===== DUAL ANNOUNCE ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            双网宣布
            <span className="text-gray-500 font-normal ml-1.5 text-[10px]">同时宣布到 IPFS DHT 和 BT DHT</span>
          </h2>
          <form onSubmit={handleDualAnnounce} className="flex gap-2">
            <input value={announceHash} onChange={e => setAnnounceHash(e.target.value)}
              placeholder="SHA256 / Infohash"
              className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <button type="submit" disabled={announceLoading || !announceHash.trim()}
              className="px-4 py-2 rounded-lg text-xs font-medium bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors whitespace-nowrap">
              {announceLoading ? '宣布中...' : '宣布到双网'}
            </button>
          </form>
          {announceResult && (
            <div className="mt-3 bg-gray-800/50 rounded-lg p-3 text-xs">
              <pre className="text-gray-300 font-mono text-[10px] whitespace-pre-wrap">
                {JSON.stringify(announceResult, null, 2)}
              </pre>
            </div>
          )}
        </div>

        {/* ===== DUAL FIND ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <h2 className="text-sm font-medium text-gray-300 mb-3">
            双网查找
            <span className="text-gray-500 font-normal ml-1.5 text-[10px]">同时在两个 DHT 网络搜索</span>
          </h2>
          <form onSubmit={handleDualFind} className="flex gap-2">
            <input value={findHash} onChange={e => setFindHash(e.target.value)}
              placeholder="SHA256 / Infohash"
              className="flex-1 bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-gray-500 placeholder:text-gray-600 transition-colors" />
            <button type="submit" disabled={findLoading || !findHash.trim()}
              className="px-4 py-2 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 transition-colors whitespace-nowrap">
              {findLoading ? '查找中...' : '搜索双网'}
            </button>
          </form>

          {(findIpfs || findBt) && !findLoading && (
            <div className="mt-3 grid grid-cols-1 md:grid-cols-2 gap-3">
              {/* IPFS results */}
              <div className="bg-gray-800/50 rounded-lg p-3">
                <h3 className="text-xs text-blue-400 mb-2 flex items-center gap-1.5">
                  <span className="w-1.5 h-1.5 rounded-full bg-blue-400" />
                  IPFS 结果 ({ipfsPeerList().length})
                </h3>
                {ipfsPeerList().length === 0 ? (
                  <p className="text-gray-600 text-[10px]">未找到</p>
                ) : (
                  <div className="space-y-1 max-h-48 overflow-y-auto">
                    {ipfsPeerList().map((p, i) => (
                      <div key={i} className="text-[10px] font-mono text-gray-400 truncate">
                        {p.peer_id || p.id || String(p).substring(0, 30)}
                      </div>
                    ))}
                  </div>
                )}
              </div>
              {/* BT results */}
              <div className="bg-gray-800/50 rounded-lg p-3">
                <h3 className="text-xs text-amber-400 mb-2 flex items-center gap-1.5">
                  <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
                  BT DHT 结果 ({btPeerList().length})
                </h3>
                {btPeerList().length === 0 ? (
                  <p className="text-gray-600 text-[10px]">未找到</p>
                ) : (
                  <div className="space-y-1 max-h-48 overflow-y-auto">
                    {btPeerList().map((peer, i) => (
                      <div key={i} className="text-[10px] font-mono text-gray-400 truncate">
                        {peer.id || peer.peer_id || `node_${i}`}
                        {(peer.addr || peer.address) && <span className="text-gray-600"> @ {peer.addr || peer.address}</span>}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        {/* ===== FILE SHARE LIFECYCLE ===== */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-4">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-medium text-gray-300">文件共享生命周期</h2>
            <button onClick={handleUploadAndShare}
              disabled={lifecycle.step === 'uploading' || lifecycle.step === 'announcing' || lifecycle.step === 'finding'}
              className="px-3 py-1.5 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 transition-colors">
              上传 & 共享
            </button>
          </div>

          <div className="flex items-center gap-2 mb-3 text-xs">
            {['selecting', 'uploading', 'announcing', 'finding', 'done', 'error'].map((step) => {
              const isActive = lifecycle.step === step;
              const isPast = ['done', 'error'].includes(lifecycle.step) &&
                ['selecting', 'uploading', 'announcing', 'finding'].indexOf(step) <
                ['selecting', 'uploading', 'announcing', 'finding', 'done', 'error'].indexOf(lifecycle.step);
              const stepNames = { selecting: '选择', uploading: '上传', announcing: '宣布', finding: '查找', done: '完成', error: '错误' };
              return (
                <div key={step} className="flex items-center gap-1.5">
                  <span className={`w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold
                    ${isActive ? 'bg-blue-500 text-white' :
                      isPast ? 'bg-emerald-600/50 text-emerald-300' :
                      'bg-gray-700 text-gray-500'}`}>
                    {isPast ? '✓' : step === 'done' ? '✓' : step === 'error' ? '!' : String(['selecting', 'uploading', 'announcing', 'finding', 'done', 'error'].indexOf(step) + 1)}
                  </span>
                  <span className={`text-[10px] ${isActive ? 'text-blue-300' : isPast ? 'text-emerald-400' : 'text-gray-600'}`}>
                    {stepNames[step]}
                  </span>
                  {step !== 'error' && <span className="text-gray-700 mx-0.5">-</span>}
                </div>
              );
            })}
          </div>

          {lifecycle.file && (
            <div className="bg-gray-800/50 rounded-lg p-3 text-xs space-y-1">
              <div className="text-gray-400">
                文件: <span className="text-gray-300">{lifecycle.file.name}</span>
                <span className="text-gray-600 ml-2">{(lifecycle.file.size / 1024).toFixed(1)} KB</span>
              </div>
              {lifecycle.hash && (
                <div className="text-gray-400">
                  Hash: <span className="font-mono text-indigo-400">{lifecycle.hash.substring(0, 32)}...</span>
                </div>
              )}
              {lifecycle.msg && (
                <div className={`mt-1.5 text-[10px] ${
                  lifecycle.step === 'error' ? 'text-red-400' :
                  lifecycle.step === 'done' ? 'text-emerald-400' :
                  'text-gray-500'
                }`}>
                  {lifecycle.msg}
                </div>
              )}
              {lifecycle.step === 'done' && (
                <div className="flex gap-2 mt-2">
                  <a href={api.getDownloadUrl(lifecycle.hash)}
                    target="_blank" rel="noopener noreferrer"
                    className="text-[10px] text-blue-400 hover:text-blue-300 underline">
                    下载链接
                  </a>
                  <button onClick={() => api.p2pRequestFile(lifecycle.hash).catch(() => {})}
                    className="text-[10px] text-gray-400 hover:text-gray-300 underline">
                    请求 P2P 传输
                  </button>
                </div>
              )}
            </div>
          )}

          {lifecycle.step === 'idle' && (
            <p className="text-gray-600 text-xs py-3 text-center">点击「上传 & 共享」测试完整文件共享流程</p>
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
