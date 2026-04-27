import React, { useState, useEffect, useCallback } from 'react'
import * as api from '../api'

const SCANNERS = [
  { key: 'dht', label: 'DHT Scanner', showPeers: true },
  { key: 'reg_server', label: 'Reg Server Scanner', showPeers: true },
  { key: 'lan_mdns', label: 'LAN/mDNS Scanner', showPeers: true },
  { key: 'bootstrap_maintainer', label: 'Bootstrap Maintainer', showPeers: false },
]

function formatTimestamp(ts) {
  if (!ts) return '-'
  const d = new Date(ts)
  if (isNaN(d.getTime())) return '-'
  const now = Date.now()
  const diff = now - d.getTime()
  if (diff < 0) return d.toLocaleTimeString()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return d.toLocaleTimeString()
}

function ScannerCard({ scanner, config }) {
  const enabled = scanner?.enabled ?? scanner?.active ?? false
  const lastScan = scanner?.last_scan ?? scanner?.last_scan_time ?? null
  const peersFound = scanner?.peers_found ?? scanner?.peers ?? null

  return (
    <div className="bg-zinc-900 border border-zinc-800 rounded-lg p-3">
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs font-medium text-zinc-300">{config.label}</span>
        <span className={`flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full ${
          enabled
            ? 'bg-emerald-900/40 text-emerald-400'
            : 'bg-zinc-800 text-zinc-500'
        }`}>
          <span className={`w-1.5 h-1.5 rounded-full ${enabled ? 'bg-emerald-400 animate-pulse' : 'bg-zinc-600'}`} />
          {enabled ? 'ON' : 'OFF'}
        </span>
      </div>
      <div className="space-y-1 text-[10px]">
        <div className="flex justify-between">
          <span className="text-zinc-600">Last Scan</span>
          <span className="text-zinc-400 font-mono">{formatTimestamp(lastScan)}</span>
        </div>
        {config.showPeers && (
          <div className="flex justify-between">
            <span className="text-zinc-600">Peers Found</span>
            <span className="text-zinc-300 font-mono font-medium">{peersFound ?? '-'}</span>
          </div>
        )}
      </div>
    </div>
  )
}

function RecentConnEntry({ entry, index }) {
  const ts = entry.timestamp ? new Date(entry.timestamp).toLocaleTimeString() : '-'
  const hasError = !!entry.error
  return (
    <div className={`flex items-center justify-between bg-zinc-800/50 rounded px-3 py-2 ${
      hasError ? 'border-l-2 border-red-500/50' : ''
    }`}>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${
            hasError ? 'bg-red-400' : 'bg-emerald-400'
          }`} />
          <span className="text-xs font-mono text-zinc-300 truncate">{entry.peer_id}</span>
        </div>
        <div className="flex items-center gap-2 mt-0.5 pl-3.5">
          <span className="text-[10px] text-zinc-600 font-mono truncate">{entry.multiaddr}</span>
        </div>
      </div>
      <div className="flex flex-col items-end shrink-0 ml-2">
        <span className="text-[10px] text-zinc-600">{ts}</span>
        {hasError && <span className="text-[10px] text-red-400/70 mt-0.5">failed</span>}
      </div>
    </div>
  )
}

export default function ActiveConnPanel() {
  const [connections, setConnections] = useState(null)
  const [peers, setPeers] = useState([])
  const [recentConns, setRecentConns] = useState([])
  const [peerId, setPeerId] = useState('')
  const [multiaddr, setMultiaddr] = useState('')
  const [loading, setLoading] = useState(false)
  const [statusMsg, setStatusMsg] = useState('')
  const [statusType, setStatusType] = useState('info')

  /* ---- fetch data ---- */
  const refresh = useCallback(async () => {
    try {
      const [conn, p] = await Promise.all([
        api.getConnections(),
        api.getP2PPeers(),
      ])
      setConnections(conn)
      setPeers(p || [])
    } catch {
      // silent
    }
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 5000)
    return () => clearInterval(t)
  }, [refresh])

  /* ---- compute stats ---- */
  const conn = connections || {}
  const inboundCount = conn.inbound ?? peers.filter(p => p.direction === 'inbound').length
  const outboundCount = conn.outbound ?? peers.filter(p => p.direction !== 'inbound').length
  const totalCount = conn.total ?? (inboundCount + outboundCount)

  /* ---- extract scanner data (flexible shape) ---- */
  const scannerData = {}
  for (const sc of SCANNERS) {
    scannerData[sc.key] =
      conn.scanners?.[sc.key] ??
      conn[sc.key] ??
      null
  }

  /* ---- manual connect ---- */
  const handleConnect = async (e) => {
    e.preventDefault()
    if (!peerId.trim()) return
    setLoading(true)
    setStatusMsg('')
    try {
      const addrs = multiaddr ? multiaddr.split(',').map(a => a.trim()).filter(Boolean) : []
      await api.connectPeer(peerId.trim(), addrs)
      setStatusMsg('Connection request sent')
      setStatusType('info')
      setRecentConns(prev => [
        { peer_id: peerId.trim(), multiaddr: multiaddr.trim() || '(auto)', timestamp: new Date().toISOString() },
        ...prev,
      ].slice(0, 10))
      setPeerId('')
      setMultiaddr('')
      refresh()
    } catch (err) {
      setStatusMsg('Connect failed: ' + err.message)
      setStatusType('error')
      setRecentConns(prev => [
        { peer_id: peerId.trim(), multiaddr: multiaddr.trim() || '(auto)', timestamp: new Date().toISOString(), error: err.message },
        ...prev,
      ].slice(0, 10))
    } finally {
      setLoading(false)
    }
  }

  /* ---- merge API recent connections with local ones ---- */
  const apiRecentConns = (conn.recent_connections || conn.recent_conns || []).slice(0, 10)
  const displayConns = recentConns.length > 0
    ? recentConns
    : apiRecentConns

  return (
    <section className="bg-zinc-900 border border-zinc-800 rounded-lg p-4 space-y-4">
      {/* ---- header ---- */}
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-zinc-300">Active Connections</h3>
        <button onClick={refresh}
          className="text-xs px-3 py-1.5 rounded border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors">
          Refresh
        </button>
      </div>

      {/* ---- stats bar ---- */}
      <div className="grid grid-cols-3 gap-3">
        <div className="bg-zinc-950 border border-zinc-800 rounded-lg p-3 text-center">
          <div className="text-xs text-zinc-500 mb-1">Inbound</div>
          <div className="text-xl font-bold text-blue-400 font-mono">{inboundCount}</div>
        </div>
        <div className="bg-zinc-950 border border-zinc-800 rounded-lg p-3 text-center">
          <div className="text-xs text-zinc-500 mb-1">Outbound</div>
          <div className="text-xl font-bold text-amber-400 font-mono">{outboundCount}</div>
        </div>
        <div className="bg-zinc-950 border border-zinc-800 rounded-lg p-3 text-center">
          <div className="text-xs text-zinc-500 mb-1">Total</div>
          <div className="text-xl font-bold text-emerald-400 font-mono">{totalCount}</div>
        </div>
      </div>

      {/* ---- scanner grid ---- */}
      <div>
        <h4 className="text-xs text-zinc-500 mb-2">Scanners</h4>
        <div className="grid grid-cols-2 gap-2">
          {SCANNERS.map(sc => (
            <ScannerCard key={sc.key} scanner={scannerData[sc.key]} config={sc} />
          ))}
        </div>
      </div>

      {/* ---- manual connect form ---- */}
      <div>
        <h4 className="text-xs text-zinc-500 mb-2">Manual Connect</h4>
        <form onSubmit={handleConnect} className="flex flex-wrap gap-2">
          <input
            value={peerId} onChange={e => setPeerId(e.target.value)}
            placeholder="Peer ID"
            className="flex-1 min-w-[200px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                       focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 transition-colors"
          />
          <input
            value={multiaddr} onChange={e => setMultiaddr(e.target.value)}
            placeholder="Multiaddr (optional, comma-separated)"
            className="flex-1 min-w-[240px] bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-xs font-mono
                       focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 transition-colors"
          />
          <button type="submit" disabled={loading || !peerId.trim()}
            className="px-4 py-2 rounded text-xs font-medium bg-zinc-700 text-zinc-200 hover:bg-zinc-600 disabled:opacity-40 transition-colors">
            {loading ? 'Connecting...' : 'Connect'}
          </button>
        </form>
      </div>

      {/* ---- recent connections log ---- */}
      <div>
        <h4 className="text-xs text-zinc-500 mb-2">
          Recent Connections
          <span className="text-zinc-600 font-normal ml-1.5">(last {displayConns.length})</span>
        </h4>
        {displayConns.length === 0 ? (
          <p className="text-zinc-600 text-xs py-4 text-center">No recent connections</p>
        ) : (
          <div className="space-y-1.5 max-h-60 overflow-y-auto">
            {displayConns.slice(0, 10).map((entry, i) => (
              <RecentConnEntry key={i} entry={entry} index={i} />
            ))}
          </div>
        )}
      </div>

      {/* ---- status message ---- */}
      {statusMsg && (
        <div className={`p-2.5 rounded text-xs border ${
          statusType === 'error'
            ? 'bg-red-900/20 border-red-900/40 text-red-400'
            : 'bg-emerald-900/20 border-emerald-900/40 text-emerald-300'
        }`}>
          {statusMsg}
        </div>
      )}
    </section>
  )
}
