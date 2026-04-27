import React, { useState, useEffect, useRef, useCallback } from 'react'
import * as api from '../api'
import PeerDetailPanel from './PeerDetailPanel'
import ActiveConnPanel from './ActiveConnPanel'

const TABS = ['IPFS', 'Active Conn', 'BT', '📋 Peers Detail']

export default function P2PStatus() {
  const [activeTab, setActiveTab] = useState('IPFS')
  const [status, setStatus] = useState(null)
  const [peers, setPeers] = useState([])
  const [discovered, setDiscovered] = useState([])
  const [wsInfo, setWsInfo] = useState(null)
  const [statusMsg, setStatusMsg] = useState('')
  const [loading, setLoading] = useState(false)
  const [pingResults, setPingResults] = useState({})
  const [pingLoading, setPingLoading] = useState({})

  /* --- BT --- */
  const [btStatus, setBtStatus] = useState(null)

  /* --- manual connect --- */
  const [connPeerId, setConnPeerId] = useState('')
  const [connAddrs, setConnAddrs] = useState('')

  /* --- exchange --- */
  const [fetchPeerId, setFetchPeerId] = useState('')
  const [fetchHash, setFetchHash] = useState('')
  const [syncPeerId, setSyncPeerId] = useState('')
  const [syncCollName, setSyncCollName] = useState('')
  const [pushPeerId, setPushPeerId] = useState('')
  const [pushCollName, setPushCollName] = useState('')

  /* --- file request --- */
  const [reqHash, setReqHash] = useState('')

  /* --- WS --- */
  const [wsConnected, setWsConnected] = useState(false)
  const [wsMessages, setWsMessages] = useState([])
  const wsRef = useRef(null)

  const appendLog = useCallback((msg) => {
    const time = new Date().toLocaleTimeString()
    setWsMessages(prev => [...prev.slice(-49), `[${time}] ${msg}`])
  }, [])

  /* ---- auto-refresh ---- */
  const refresh = useCallback(async () => {
    try {
      const [s, p, d] = await Promise.all([
        api.getP2PStatus(),
        api.getP2PPeers(),
        api.getP2PDiscovered(),
      ])
      setStatus(s)
      setPeers(p || [])
      setDiscovered(d || [])
    } catch {}
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 5000)
    return () => clearInterval(t)
  }, [refresh])

  const loadWS = async () => {
    try { setWsInfo(await api.getWSInfo()) } catch {}
  }

  const handlePing = async (peerId) => {
    setPingLoading(prev => ({ ...prev, [peerId]: true }))
    try {
      const res = await api.pingPeer(peerId)
      setPingResults(prev => ({ ...prev, [peerId]: res }))
    } catch (e) {
      setPingResults(prev => ({ ...prev, [peerId]: { error: e.message } }))
    } finally {
      setPingLoading(prev => ({ ...prev, [peerId]: false }))
    }
  }

  const handleConnect = async (e) => {
    e.preventDefault()
    if (!connPeerId) return
    setLoading(true)
    setStatusMsg('')
    try {
      const addrs = connAddrs ? connAddrs.split(',').map(a => a.trim()).filter(Boolean) : []
      await api.connectPeer(connPeerId, addrs)
      setStatusMsg('连接请求已发送')
      refresh()
    } catch (e) {
      setStatusMsg('连接失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  const handleFetch = async (e) => {
    e.preventDefault()
    if (!fetchPeerId || !fetchHash) return
    setLoading(true)
    setStatusMsg('')
    try {
      const res = await api.p2pFetch(fetchPeerId, fetchHash)
      setStatusMsg('拉取成功 → hash: ' + (res.hash || res.collection_hash || JSON.stringify(res)))
    } catch (e) {
      setStatusMsg('拉取失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  const handleSync = async (e) => {
    e.preventDefault()
    if (!syncPeerId || !syncCollName) return
    setLoading(true)
    setStatusMsg('')
    try {
      await api.p2pSync(syncPeerId, syncCollName)
      setStatusMsg('同步完成')
    } catch (e) {
      setStatusMsg('同步失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  const handlePush = async (e) => {
    e.preventDefault()
    if (!pushPeerId || !pushCollName) return
    setLoading(true)
    setStatusMsg('')
    try {
      await api.p2pPush(pushPeerId, pushCollName)
      setStatusMsg('推送完成')
    } catch (e) {
      setStatusMsg('推送失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  const handleRequestFile = async (e) => {
    e.preventDefault()
    if (!reqHash) return
    setLoading(true)
    setStatusMsg('')
    try {
      await api.p2pRequestFile(reqHash)
      setStatusMsg('文件请求已广播到 P2P 网络')
    } catch (e) {
      setStatusMsg('请求失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  /* ---- WebSocket ---- */
  const connectWS = () => {
    if (wsRef.current?.readyState === WebSocket.OPEN) return
    try {
      const ws = new WebSocket(api.WS_TRANSFER_URL)
      wsRef.current = ws
      ws.onopen = () => {
        setWsConnected(true)
        appendLog('WS 已连接')
      }
      ws.onclose = () => {
        setWsConnected(false)
        appendLog('WS 已断开')
      }
      ws.onerror = () => {
        appendLog('WS 连接错误')
      }
      ws.onmessage = (ev) => {
        if (typeof ev.data === 'string') {
          try {
            const msg = JSON.parse(ev.data)
            appendLog(`← ${msg.type || 'msg'}: ${msg.status || JSON.stringify(msg).slice(0, 60)}`)
          } catch {
            appendLog(`← ${ev.data.slice(0, 80)}`)
          }
        } else {
          appendLog(`← [binary ${ev.data.size || ev.data.byteLength} bytes]`)
        }
      }
    } catch (e) {
      appendLog('WS 创建失败: ' + e.message)
    }
  }

  const disconnectWS = () => {
    wsRef.current?.close()
    wsRef.current = null
  }

  /* --- BT handlers --- */
  const loadBTStatus = useCallback(async () => {
    try {
      setBtStatus(await api.getBTStatus());
    } catch {}
  }, []);

  useEffect(() => {
    if (activeTab === 'BT') loadBTStatus();
  }, [activeTab, loadBTStatus]);

  const [btAnnounceHash, setBtAnnounceHash] = useState('');
  const [btFindHash, setBtFindHash] = useState('');
  const [btFindResults, setBtFindResults] = useState([]);

  const handleBtAnnounce = async (e) => {
    e.preventDefault();
    if (!btAnnounceHash.trim()) return;
    try {
      await api.btAnnounce(btAnnounceHash.trim());
      setStatusMsg('BT 已宣布: ' + btAnnounceHash.trim().substring(0, 16) + '...');
      setBtAnnounceHash('');
    } catch (e) {
      setStatusMsg('BT 宣布失败: ' + e.message);
    }
  };

  const handleBtFind = async (e) => {
    e.preventDefault();
    if (!btFindHash.trim()) return;
    try {
      const res = await api.btFind(btFindHash.trim());
      setBtFindResults(prev => [...prev.slice(-19), { hash: btFindHash.trim(), result: res, time: new Date() }]);
      setStatusMsg('BT 查找已发送: ' + btFindHash.trim().substring(0, 16) + '...');
      setBtFindHash('');
    } catch (e) {
      setStatusMsg('BT 查找失败: ' + e.message);
    }
  };

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      {/* ============ Header + Tabs ============ */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-xl font-semibold">P2P 网络</h2>
          <span className={`flex items-center gap-1.5 text-xs ${wsConnected ? 'text-emerald-400' : 'text-zinc-500'}`}>
            <span className={`w-2 h-2 rounded-full ${wsConnected ? 'bg-emerald-400 animate-pulse' : 'bg-zinc-600'}`} />
            WS{wsConnected ? '已连接' : '未连接'}
          </span>
        </div>
        <div className="flex gap-1 border-b border-zinc-800">
          {TABS.map(tab => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={`px-4 py-2 text-xs font-medium rounded-t-lg transition-colors ${
                activeTab === tab
                  ? 'bg-zinc-900 text-zinc-200 border border-zinc-800 border-b-zinc-900 -mb-px'
                  : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-800/30'
              }`}
            >
              {tab}
            </button>
          ))}
        </div>
      </div>

      {/* ============ IPFS Tab ============ */}
      {activeTab === 'IPFS' && (
        <div className="space-y-6">
          {/* ============ Status ============ */}
          {status && (
            <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
                <div>
                  <span className="text-zinc-500 text-xs">Peer ID</span>
                  <p className="font-mono text-zinc-300 text-xs mt-1 break-all">{status.peer_id?.slice(0, 24)}...</p>
                </div>
                <div>
                  <span className="text-zinc-500 text-xs">Relay 模式</span>
                  <p className="text-zinc-300 mt-1">
                    <span className={`px-1.5 py-0.5 rounded text-xs font-mono ${
                      status.relay_mode === 'server' ? 'bg-blue-900/50 text-blue-300' :
                      status.relay_mode === 'client' ? 'bg-purple-900/50 text-purple-300' :
                      'bg-zinc-800 text-zinc-400'
                    }`}>{status.relay_mode || 'off'}</span>
                  </p>
                </div>
                <div>
                  <span className="text-zinc-500 text-xs">NAT 打洞</span>
                  <p className="text-zinc-300 mt-1">
                    <span className={`px-1.5 py-0.5 rounded text-xs font-mono ${
                      status.hole_punch ? 'bg-emerald-900/50 text-emerald-300' : 'bg-zinc-800 text-zinc-500'
                    }`}>{status.hole_punch ? '已启用' : '关闭'}</span>
                  </p>
                </div>
                <div>
                  <span className="text-zinc-500 text-xs">WS 连接数</span>
                  <p className="text-zinc-300 text-lg font-mono mt-1">{status.ws_connections ?? 0}</p>
                </div>
              </div>
              {status.addresses?.length > 0 && (
                <div className="mt-3 pt-3 border-t border-zinc-800">
                  <span className="text-zinc-500 text-xs">地址</span>
                  <div className="mt-1 space-y-0.5 max-h-24 overflow-y-auto">
                    {status.addresses.map((a, i) => (
                      <p key={i} className="text-zinc-400 text-xs font-mono">{a}</p>
                    ))}
                  </div>
                </div>
              )}
              {status.conn_stats && (
                <div className="mt-3 pt-3 border-t border-zinc-800">
                  <span className="text-zinc-500 text-xs">连接管理</span>
                  <div className="grid grid-cols-3 gap-2 mt-2">
                    <div className="text-center">
                      <p className="text-zinc-300 text-base font-mono">{status.conn_stats.known_peers ?? 0}</p>
                      <span className="text-zinc-600 text-[10px]">已知节点</span>
                    </div>
                    <div className="text-center">
                      <p className="text-emerald-400 text-base font-mono">{status.conn_stats.successful_conns ?? 0}</p>
                      <span className="text-zinc-600 text-[10px]">成功连接</span>
                    </div>
                    <div className="text-center">
                      <p className="text-amber-400 text-base font-mono">{status.conn_stats.failed_conns ?? 0}</p>
                      <span className="text-zinc-600 text-[10px]">失败</span>
                    </div>
                  </div>
                </div>
              )}
              {status.active_transfers?.length > 0 && (
                <div className="mt-3 pt-3 border-t border-zinc-800">
                  <span className="text-zinc-500 text-xs">活跃传输</span>
                  <div className="mt-2 space-y-1.5">
                    {status.active_transfers.map((t, i) => (
                      <div key={i} className="bg-zinc-800/50 rounded px-3 py-2">
                        <div className="flex justify-between text-xs mb-1">
                          <span className="text-zinc-400 font-mono truncate">{(t.hash || '').substring(0, 12)}...</span>
                          <span className="text-zinc-500">{t.progress?.toFixed(1)}%</span>
                        </div>
                        <div className="w-full bg-zinc-700 rounded-full h-1.5">
                          <div className="bg-blue-500 h-1.5 rounded-full transition-all" style={{width: `${Math.min(100, t.progress || 0)}%`}} />
                        </div>
                        <div className="flex justify-between text-[10px] text-zinc-600 mt-1">
                          <span>{t.total_mb?.toFixed(1)} MB</span>
                          <span>{t.peers} 节点</span>
                          <span>{t.elapsed}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </section>
          )}

          {/* ============ Active Connections ============ */}
          <ActiveConnPanel />

          {/* ============ Connected Peers ============ */}
          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <h3 className="text-sm font-medium text-zinc-300 mb-3">
              已连接节点
              <span className="text-zinc-500 font-normal ml-2">{peers.length} 个</span>
            </h3>
            {peers.length === 0 ? (
              <p className="text-zinc-600 text-xs">暂无连接</p>
            ) : (
              <div className="space-y-2 max-h-64 overflow-y-auto">
                {peers.map((p, i) => (
                  <div key={i} className="flex items-center justify-between bg-zinc-800/50 rounded px-3 py-2">
                    <div className="flex-1 min-w-0">
                      <p className="text-zinc-300 text-xs font-mono truncate">{p.peer_id || p.id || p}</p>
                    </div>
                    <button
                      onClick={() => handlePing(p.peer_id || p.id || p)}
                      disabled={pingLoading[p.peer_id || p.id || p]}
                      className="text-xs text-zinc-400 hover:text-amber-400 px-2 py-1 rounded border border-zinc-700 hover:border-amber-600 transition-colors disabled:opacity-40 ml-2 shrink-0"
                    >
                      {pingLoading[p.peer_id || p.id || p] ? '...' :
                       pingResults[p.peer_id || p.id || p]?.error ? '重试' :
                       pingResults[p.peer_id || p.id || p] ? `${pingResults[p.peer_id || p.id || p].latency || pingResults[p.peer_id || p.id || p].rtt || 'ok'}ms` : 'Ping'}
                    </button>
                  </div>
                ))}
              </div>
            )}
          </section>

          {/* ============ Discovered + Manual Connect ============ */}
          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <h3 className="text-sm font-medium text-zinc-300 mb-3">
              发现的节点
              <span className="text-zinc-500 font-normal ml-2">{discovered.length} 个</span>
            </h3>
            {discovered.length > 0 ? (
              <div className="space-y-2 max-h-48 overflow-y-auto mb-4">
                {discovered.map((d, i) => (
                  <div key={i} className="flex items-center justify-between bg-zinc-800/50 rounded px-3 py-2">
                    <div className="flex-1 min-w-0">
                      <p className="text-zinc-300 text-xs font-mono truncate">{d.peer_id || d.id || d}</p>
                      {d.addrs && <p className="text-zinc-600 text-xs mt-0.5 truncate">{Array.isArray(d.addrs) ? d.addrs.join(', ') : d.addrs}</p>}
                    </div>
                    <button
                      onClick={() => { setConnPeerId(d.peer_id || d.id || d); setConnAddrs(typeof d.addrs === 'string' ? d.addrs : (d.addrs || []).join(',')); }}
                      className="text-xs text-amber-400 hover:text-amber-300 px-2 py-1 rounded border border-zinc-700 hover:border-amber-600 transition-colors ml-2 shrink-0"
                    >
                      连接
                    </button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-zinc-600 text-xs mb-4">未发现邻居节点</p>
            )}

            <div className="border-t border-zinc-800 pt-3">
              <h4 className="text-xs text-zinc-500 mb-2">手动连接</h4>
              <form onSubmit={handleConnect} className="flex flex-wrap gap-2">
                <input
                  value={connPeerId} onChange={e => setConnPeerId(e.target.value)}
                  placeholder="Peer ID"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600"
                />
                <input
                  value={connAddrs} onChange={e => setConnAddrs(e.target.value)}
                  placeholder="Multiaddr (逗号分隔)"
                  className="flex-1 min-w-[240px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600"
                />
                <button type="submit" disabled={loading}
                  className="px-4 py-2 rounded text-xs font-medium bg-zinc-700 text-zinc-200 hover:bg-zinc-600 disabled:opacity-40 transition-colors">
                  连接
                </button>
              </form>
            </div>
          </section>

          {/* ============ Collection Exchange ============ */}
          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <h3 className="text-sm font-medium text-zinc-300 mb-3">合集交换</h3>
            <div className="mb-4 pb-4 border-b border-zinc-800">
              <h4 className="text-xs text-zinc-500 mb-2">拉取匿名合集 (Fetch)</h4>
              <form onSubmit={handleFetch} className="flex flex-wrap gap-2">
                <input value={fetchPeerId} onChange={e => setFetchPeerId(e.target.value)}
                  placeholder="Peer ID"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <input value={fetchHash} onChange={e => setFetchHash(e.target.value)}
                  placeholder="Collection Hash"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <button type="submit" disabled={loading}
                  className="px-4 py-2 rounded text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 transition-colors">
                  拉取
                </button>
              </form>
            </div>
            <div className="mb-4 pb-4 border-b border-zinc-800">
              <h4 className="text-xs text-zinc-500 mb-2">全量同步 (Sync)</h4>
              <form onSubmit={handleSync} className="flex flex-wrap gap-2">
                <input value={syncPeerId} onChange={e => setSyncPeerId(e.target.value)}
                  placeholder="Peer ID"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <input value={syncCollName} onChange={e => setSyncCollName(e.target.value)}
                  placeholder="合集名称"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <button type="submit" disabled={loading}
                  className="px-4 py-2 rounded text-xs font-medium bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors">
                  同步
                </button>
              </form>
            </div>
            <div>
              <h4 className="text-xs text-zinc-500 mb-2">推送合集 (Push)</h4>
              <form onSubmit={handlePush} className="flex flex-wrap gap-2">
                <input value={pushPeerId} onChange={e => setPushPeerId(e.target.value)}
                  placeholder="Peer ID"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <input value={pushCollName} onChange={e => setPushCollName(e.target.value)}
                  placeholder="合集名称"
                  className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <button type="submit" disabled={loading}
                  className="px-4 py-2 rounded text-xs font-medium bg-purple-600 text-white hover:bg-purple-500 disabled:opacity-40 transition-colors">
                  推送
                </button>
              </form>
            </div>
          </section>

          {/* ============ File Request ============ */}
          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <h3 className="text-sm font-medium text-zinc-300 mb-3">文件请求广播</h3>
            <p className="text-xs text-zinc-500 mb-3">在 P2P 网络中广播请求指定 hash 的文件，拥有该文件的节点会通过 WebSocket 推送给你。</p>
            <form onSubmit={handleRequestFile} className="flex gap-2">
              <input value={reqHash} onChange={e => setReqHash(e.target.value)}
                placeholder="文件 SHA256 Hash"
                className="flex-1 bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                           focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
              <button type="submit" disabled={loading}
                className="px-4 py-2 rounded text-xs font-medium bg-amber-600 text-white hover:bg-amber-500 disabled:opacity-40 transition-colors whitespace-nowrap">
                  广播请求
                </button>
            </form>
          </section>

          {/* ============ WebSocket ============ */}
          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-medium text-zinc-300">WebSocket 传输</h3>
              <div className="flex gap-2">
                {!wsConnected ? (
                  <button onClick={connectWS}
                    className="text-xs px-3 py-1.5 rounded bg-emerald-600 text-white hover:bg-emerald-500 transition-colors">
                    连接 WS
                  </button>
                ) : (
                  <button onClick={disconnectWS}
                    className="text-xs px-3 py-1.5 rounded bg-red-600 text-white hover:bg-red-500 transition-colors">
                    断开
                  </button>
                )}
                <button onClick={loadWS}
                  className="text-xs px-3 py-1.5 rounded border border-zinc-700 text-zinc-400 hover:text-zinc-200 transition-colors">
                  刷新连接信息
                </button>
              </div>
            </div>

            {wsInfo?.connections && (
              <div className="mb-3">
                <span className="text-xs text-zinc-500">活跃连接: {wsInfo.connections.length}</span>
                <div className="mt-1 space-y-1 max-h-32 overflow-y-auto">
                  {wsInfo.connections.map((c, i) => (
                    <div key={i} className="bg-zinc-800/50 rounded px-3 py-1.5 text-xs text-zinc-400 font-mono">
                      <span className="text-zinc-300">{c.peer_id?.slice(0, 20)}...</span>
                      <span className="text-zinc-600 ml-2">{c.remote_addr}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <div className="bg-zinc-950 border border-zinc-800 rounded p-3 max-h-48 overflow-y-auto">
              {wsMessages.length === 0 ? (
                <p className="text-zinc-600 text-xs">连接 WebSocket 后此处将显示传输日志</p>
              ) : (
                wsMessages.map((m, i) => (
                  <p key={i} className="text-xs font-mono text-zinc-400 leading-relaxed">{m}</p>
                ))
              )}
            </div>
          </section>
        </div>
      )}

      {/* ============ BT Tab ============ */}
      {activeTab === 'BT' && (
        <div className="space-y-6">
          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-medium text-zinc-300">BT 状态</h3>
              <button onClick={loadBTStatus}
                className="text-xs px-3 py-1.5 rounded border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors">
                刷新
              </button>
            </div>
            {btStatus ? (
              <div className="grid grid-cols-2 md:grid-cols-3 gap-4 text-sm">
                {Object.entries(btStatus).map(([k, v]) => (
                  <div key={k}>
                    <span className="text-zinc-500 text-[10px]">{k.replace(/_/g, ' ')}</span>
                    <p className="text-zinc-300 text-xs font-mono mt-0.5">
                      {typeof v === 'object' ? JSON.stringify(v) : String(v)}
                    </p>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-zinc-600 text-xs py-4 text-center">暂无 BT 状态信息</p>
            )}
          </section>

          <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4">
            <h3 className="text-sm font-medium text-zinc-300 mb-3">BT 操作</h3>
            <div className="mb-4 pb-4 border-b border-zinc-800">
              <h4 className="text-xs text-zinc-500 mb-2">宣布到 BT (Announce)</h4>
              <form onSubmit={handleBtAnnounce} className="flex gap-2">
                <input value={btAnnounceHash} onChange={e => setBtAnnounceHash(e.target.value)}
                  placeholder="文件 Hash"
                  className="flex-1 bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <button type="submit" disabled={!btAnnounceHash.trim()}
                  className="px-4 py-2 rounded text-xs font-medium bg-emerald-600 text-white hover:bg-emerald-500 disabled:opacity-40 transition-colors whitespace-nowrap">
                  宣布
                </button>
              </form>
            </div>
            <div>
              <h4 className="text-xs text-zinc-500 mb-2">BT 查找 (Find)</h4>
              <form onSubmit={handleBtFind} className="flex gap-2">
                <input value={btFindHash} onChange={e => setBtFindHash(e.target.value)}
                  placeholder="文件 Hash"
                  className="flex-1 bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                             focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600" />
                <button type="submit" disabled={!btFindHash.trim()}
                  className="px-4 py-2 rounded text-xs font-medium bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 transition-colors whitespace-nowrap">
                  查找
                </button>
              </form>
              {btFindResults.length > 0 && (
                <div className="mt-3 space-y-1">
                  <div className="text-[10px] text-zinc-500">查找记录 ({btFindResults.length})</div>
                  {btFindResults.map((r, i) => (
                    <div key={i} className="bg-zinc-800/50 rounded px-3 py-1.5 text-xs">
                      <div className="flex items-center justify-between text-zinc-400 font-mono">
                        <span className="truncate mr-2">{r.hash?.substring(0, 20)}...</span>
                        <span className="text-zinc-600 shrink-0">{r.time?.toLocaleTimeString()}</span>
                      </div>
                      <div className="text-[10px] text-zinc-500 mt-0.5">
                        {r.result?.status || r.result?.message || JSON.stringify(r.result).substring(0, 60)}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </section>
        </div>
      )}

      {/* ============ Active Conn Tab ============ */}
      {activeTab === 'Active Conn' && (
        <ActiveConnPanel />
      )}

      {/* ============ Peers Detail Tab ============ */}
      {activeTab === '📋 Peers Detail' && (
        <PeerDetailPanel />
      )}

      {/* ============ Status Msg ============ */}
      {statusMsg && (
        <div className={`p-3 rounded-lg text-sm border ${
          statusMsg.includes('失败') ? 'bg-red-900/20 border-red-900/40 text-red-400' :
          'bg-emerald-900/20 border-emerald-900/40 text-emerald-300'
        }`}>
          {statusMsg}
        </div>
      )}
    </div>
  )
}
