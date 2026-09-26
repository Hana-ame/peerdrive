// PeerJSConnect — PeerJS 信令拨号连接器（消费端）
//
// 完整 UI 里连对端节点的第二种方式：经公共信令按 peer id 拨号目标节点，
// 看对方共享的单独文件 / 合集，并可直接保存。与 WebSocket/HTTP（管理面）并列。
// 抽成独立组件：Settings 节点连接分区与首页（Plaza）共用。
import React, { useState, useRef, useEffect } from 'react';
import Peer from 'peerjs';
import { connectToPeer, discoverNodes } from './pd-client';
import { setNodeSession, clearNodeSession } from './nodeSession';

const DEFAULT_SIG = {
  host: 'peersignal.moonchan.xyz',
  port: 443,
  path: '/',
  key: 'pd-signal-b9447b406828e500',
  secure: true,
};

function getStableMyId() {
  try {
    const data = JSON.parse(localStorage.getItem('peerdrive.panel.v1') || '{}');
    if (data.myId) return data.myId;
  } catch (e) { /* 忽略 */ }
  const id = 'pd-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8);
  try {
    const data = JSON.parse(localStorage.getItem('peerdrive.panel.v1') || '{}');
    data.myId = id;
    localStorage.setItem('peerdrive.panel.v1', JSON.stringify(data));
  } catch (e) { /* 忽略 */ }
  return id;
}

export default function PeerJSConnect({ compact = false, onConnected = null }) {
  const [sigHost, setSigHost] = useState(DEFAULT_SIG.host);
  const [sigPort, setSigPort] = useState(String(DEFAULT_SIG.port));
  const [sigKey, setSigKey] = useState(DEFAULT_SIG.key);
  const [targetPeerId, setTargetPeerId] = useState('');
  const myIdRef = useRef(getStableMyId());
  const [pdClient, setPdClient] = useState(null);
  const [pdStatus, setPdStatus] = useState('idle'); // idle|connecting|online|error
  const [pdError, setPdError] = useState('');
  const [pdShare, setPdShare] = useState(null);
  const [collOpen, setCollOpen] = useState(null); // 展开的合集下标
  // 搜索在线节点（信令 discover）
  const [foundNodes, setFoundNodes] = useState(null); // null=未搜 | [] = 空
  const [searchStatus, setSearchStatus] = useState('idle'); // idle|searching|error
  const [searchErr, setSearchErr] = useState('');

  const fmtBytes = (n) => {
    if (!n || n === 0) return '—';
    const u = ['B', 'KB', 'MB', 'GB'];
    let v = n, i = 0;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v.toFixed(v < 10 ? 1 : 0) + ' ' + u[i];
  };

  // 拨号核心：直接按传入的 peerId 连接（不依赖 state，搜索列表点击可立即调用）
  const connectTo = async (peerId) => {
    const target = (peerId || '').trim();
    if (!target) return;
    setPdStatus('connecting');
    setPdError('');
    setPdShare(null);
    try {
      const client = await connectToPeer(Peer, target, {
        peerOptions: {
          host: sigHost.trim() || DEFAULT_SIG.host,
          port: Number(sigPort.trim()) || DEFAULT_SIG.port,
          path: DEFAULT_SIG.path,
          key: sigKey.trim() || DEFAULT_SIG.key,
          secure: DEFAULT_SIG.secure, // 信令恒走 wss（HTTPS 加密），不提供开关
          id: myIdRef.current,
        },
        connOptions: { serialization: 'raw', reliable: true },
        timeoutMs: 15000,
      });
      setPdClient(client);
      const snap = await client.shares();
      setPdShare(snap);
      setPdStatus('online');
      // 存入全局会话（供节点控制页使用），并触发「连接成功跳转」回调
      setNodeSession({ client, peerId: target, myId: myIdRef.current });
      onConnected?.(client, target);
    } catch (e) {
      setPdStatus('error');
      setPdError(e?.message || String(e));
    }
  };

  const handleConnect = () => connectTo(targetPeerId);

  const handleDisconnect = () => {
    clearNodeSession();
    try { pdClient?.close?.(); } catch (e) { /* 忽略 */ }
    setPdClient(null);
    setPdShare(null);
    setPdStatus('idle');
    setCollOpen(null);
  };

  // 经公共信令搜当前在线节点
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

  // 挂载即自动搜索在线节点（不用手动点「搜索在线节点」；按钮留着手动刷新）
  useEffect(() => { handleSearch(); }, []);

  // 从搜索结果直接拨号（点击即连，不再回填输入框）
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
      alert('保存失败：' + (e?.message || String(e)));
    }
  };

  const inputCls = 'w-full bg-white/[0.06] px-2 py-1 text-xs font-mono rounded border border-white/[0.1] focus:outline-none focus:border-brand-500';

  return (
    <div className={compact ? 'space-y-2' : 'space-y-3'}>
      {!compact && (
        <div className="grid grid-cols-2 gap-2">
          <div>
            <label className="block text-xs text-gray-500 mb-1">信令 host</label>
            <input value={sigHost} onChange={e => setSigHost(e.target.value)} className={inputCls} />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">信令 port</label>
            <input value={sigPort} onChange={e => setSigPort(e.target.value)} className={inputCls} />
          </div>
          <div className="col-span-2">
            <label className="block text-xs text-gray-500 mb-1">信令 key</label>
            <input value={sigKey} onChange={e => setSigKey(e.target.value)} className={inputCls} />
          </div>
        </div>
      )}


      {/* 搜索在线节点 */}
      <div>
        <div className="flex gap-2">
          <button onClick={handleSearch} disabled={searchStatus === 'searching'}
            className="px-3 py-1 text-xs bg-white/[0.06] text-gray-300 rounded-lg hover:bg-white/[0.12] disabled:opacity-40 whitespace-nowrap">
            {searchStatus === 'searching' ? '搜索中...' : '搜索在线节点'}
          </button>
          <span className="text-xs self-center">
            {searchStatus === 'error' && <span className="text-red-400">{searchErr}</span>}
            {searchStatus === 'done' && <span className="text-gray-400">{foundNodes?.length ?? 0} 个在线节点</span>}
          </span>
        </div>
        {searchStatus === 'done' && foundNodes && foundNodes.length > 0 && (
          <ul className="mt-2 space-y-1">
            {foundNodes.map((n, i) => (
              <li key={i} className="flex items-center gap-2 text-xs bg-white/[0.04] px-2 py-1.5 rounded border border-white/[0.05]">
                <span className="flex-1 truncate text-gray-300 font-mono">{n.peerId}</span>
                <span className="text-gray-500 shrink-0">{n.nodeType || ''}</span>
                <span className="text-gray-600 shrink-0">{Array.isArray(n.collections) ? n.collections.length + ' 合集' : ''}</span>
                <button onClick={() => handleJoinFound(n.peerId)}
                  className="px-2 py-0.5 text-xs bg-brand-600 text-white rounded hover:bg-brand-500 shrink-0">连接</button>
              </li>
            ))}
          </ul>
        )}
        {searchStatus === 'done' && foundNodes && foundNodes.length === 0 && (
          <p className="text-xs text-gray-600 mt-1.5">当前没有在线节点（节点在线后会经信令 announce）。</p>
        )}
      </div>

      <div>
        {!compact && <label className="block text-xs text-gray-500 mb-1">目标节点 peer id（拨号对象）</label>}
        <div className="flex gap-2">
          <input value={targetPeerId} onChange={e => setTargetPeerId(e.target.value)}
            placeholder="peerdrive-xxxxxxxx"
            className={inputCls + ' flex-1'}
            onKeyDown={e => { if (e.key === 'Enter') handleConnect(); }} />
          <button onClick={handleConnect} disabled={!targetPeerId.trim() || pdStatus === 'connecting'}
            className="px-3 py-1 text-xs bg-brand-600 text-white rounded-lg hover:bg-brand-500 disabled:opacity-40 whitespace-nowrap">连接</button>
          {pdStatus === 'online' && (
            <button onClick={handleDisconnect}
              className="px-3 py-1 text-xs bg-white/[0.06] text-gray-300 rounded-lg hover:bg-white/[0.12] whitespace-nowrap">断开</button>
          )}
        </div>
      </div>

      <div className="text-xs">
        {pdStatus === 'connecting' && <span className="text-yellow-400">连接中...</span>}
        {pdStatus === 'online' && <span className="text-green-400">已连接 ✓（本机 id：{myIdRef.current}）</span>}
        {pdStatus === 'error' && <span className="text-red-400">失败：{pdError}</span>}
      </div>

      {pdStatus === 'online' && (
        <div className={compact ? '' : 'border-t border-white/[0.06] pt-2'}>
          <p className="text-xs text-gray-500 mb-1.5">对端共享（{pdShare?.total ?? 0} 项）</p>
          {pdShare && pdShare.files?.length > 0 && (
            <div className="mb-2">
              {!compact && <p className="text-xs text-gray-500 mb-1">单独文件</p>}
              <ul className="space-y-1">
                {pdShare.files.map((f, i) => (
                  <li key={i} className="flex items-center gap-2 text-xs">
                    <span className="flex-1 truncate text-gray-300">{f.path || f.name}</span>
                    <span className="text-gray-500 shrink-0">{fmtBytes(f.size)}</span>
                    <button onClick={() => handleSave(f)}
                      className="px-2 py-0.5 text-xs bg-white/[0.06] text-gray-300 rounded hover:bg-white/[0.12] shrink-0">保存</button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {pdShare && pdShare.collections?.length > 0 && (
            <div>
              {!compact && <p className="text-xs text-gray-500 mb-1">合集</p>}
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
                        <span className="text-gray-500">· {entries.length} 条目</span>
                      </button>
                      {open && (
                        <ul className="mt-1 ml-4 space-y-1">
                          {entries.map((e, ei) => (
                            <li key={ei} className="flex items-center gap-2">
                              <span className="flex-1 truncate text-gray-400">{e.path}</span>
                              <span className="text-gray-500 shrink-0">{fmtBytes(e.size)}</span>
                              <button onClick={() => handleSave({ ...e, name: e.path || 'download' })}
                                className="px-2 py-0.5 text-xs bg-white/[0.06] text-gray-300 rounded hover:bg-white/[0.12] shrink-0">保存</button>
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
            <p className="text-xs text-gray-600">该节点没有共享内容（未开启对外共享，或未声明共享目录）。</p>
          )}
        </div>
      )}
    </div>
  );
}