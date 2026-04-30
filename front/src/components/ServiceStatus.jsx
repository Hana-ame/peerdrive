// 服务状态仪表盘：展示 Relay/注册服务器/存储/BT DHT/WebSocket 等各服务的健康度
import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';

/* ───────── helpers ───────── */

function formatBytes(bytes) {
  if (bytes == null || bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const k = 1024;
  const i = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(k)), units.length - 1);
  const val = bytes / Math.pow(k, i);
  return (i === 0 ? val.toFixed(0) : val.toFixed(1)) + ' ' + units[i];
}

function formatNumber(n) {
  if (n == null) return '-';
  return Number(n).toLocaleString();
}

/* ───────── StatusDot ───────── */

function StatusDot({ ok, partial, pulse }) {
  const color = ok
    ? partial
      ? 'bg-amber-400'
      : 'bg-emerald-400'
    : 'bg-red-400';
  const animate = pulse && ok && !partial;
  return (
    <span
      className={`w-2.5 h-2.5 rounded-full shrink-0 ${color} ${animate ? 'animate-pulse' : ''}`}
    />
  );
}

/* ───────── StatRow ───────── */

function StatRow({ label, value }) {
  return (
    <div className="flex items-center justify-between py-0.5">
      <span className="text-zinc-500 text-xs">{label}</span>
      <span className="text-zinc-300 text-xs font-mono font-medium truncate ml-2 max-w-[220px]" title={value}>
        {value ?? '-'}
      </span>
    </div>
  );
}

/* ───────── ServiceCard ───────── */

function Badge({ children, className = '' }) {
  return (
    <span className={`inline-block text-[10px] px-1.5 py-0.5 rounded font-mono ${className}`}>
      {children}
    </span>
  );
}

function ServiceCard({ icon, name, ok, partial, metrics, badge, children, expanded, onToggle }) {
  return (
    <div className="bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden transition-colors">
      {/* ── header ── */}
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between p-4 hover:bg-zinc-800/40 transition-colors text-left"
      >
        <div className="flex items-center gap-3 min-w-0">
          <span className="text-lg shrink-0">{icon}</span>
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <StatusDot ok={ok} partial={partial} pulse />
              <span className="text-sm font-medium text-zinc-200">{name}</span>
              {badge && <Badge className={badge.className}>{badge.label}</Badge>}
            </div>
          </div>
        </div>
        <svg
          className={`w-4 h-4 text-zinc-500 shrink-0 transition-transform duration-200 ${
            expanded ? 'rotate-180' : ''
          }`}
          viewBox="0 0 20 20"
          fill="currentColor"
        >
          <path
            fillRule="evenodd"
            d="M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z"
            clipRule="evenodd"
          />
        </svg>
      </button>

      {/* ── metrics ── */}
      {metrics && (
        <div className="px-4 pb-3">
          <div
            className={`grid gap-3 ${
              metrics.length <= 2 ? 'grid-cols-2' : metrics.length === 3 ? 'grid-cols-3' : 'grid-cols-2 md:grid-cols-4'
            }`}
          >
            {metrics.map((m, i) => (
              <div key={i} className="bg-zinc-800/40 rounded-lg p-2.5 text-center">
                <div className="text-lg font-bold text-zinc-100 leading-tight">{m.value}</div>
                <div className="text-[10px] text-zinc-500 mt-0.5 leading-tight">{m.label}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* ── no-data placeholder ── */}
      {!ok && !children && (
        <div className="px-4 pb-4">
          <p className="text-red-400/80 text-xs">Unreachable</p>
        </div>
      )}

      {/* ── expanded detail ── */}
      {expanded && children && (
        <div className="mx-4 mb-4 pt-3 border-t border-zinc-800">{children}</div>
      )}
    </div>
  );
}

/* ───────── ServiceStatus (main) ───────── */

export default function ServiceStatus() {
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState({});
  /* reg server: input buffer vs committed value to avoid fetching on every keystroke */
  const [regServerInput, setRegServerInput] = useState(() => api.getRegServerUrl());
  const [regServerUrl, setRegServerUrl] = useState(() => api.getRegServerUrl());
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [lastRefresh, setLastRefresh] = useState(null);
  const [showConfig, setShowConfig] = useState(false);

  /* ── data fetching ── */

  const fetchStats = useCallback(async () => {
    try {
      const result = await api.getServiceStats({
        regServerUrl: regServerUrl || undefined,
      });
      setStats(result);
      setLastRefresh(new Date());
    } catch {
      /* errors are reported per-service */
    } finally {
      setLoading(false);
    }
  }, [regServerUrl]);

  useEffect(() => {
    fetchStats();
  }, [fetchStats]);

  useEffect(() => {
    if (!autoRefresh) return;
    const t = setInterval(fetchStats, 5000);
    return () => clearInterval(t);
  }, [autoRefresh, fetchStats]);

  /* ── handlers ── */

  const commitRegServerUrl = (val) => {
    setRegServerUrl(val);
    api.setRegServerUrl(val);
  };

  const handleRegServerKeyDown = (e) => {
    if (e.key === 'Enter') {
      commitRegServerUrl(e.target.value);
    }
  };

  const handleRegServerBlur = (e) => {
    commitRegServerUrl(e.target.value);
  };

  const toggleExpand = (key) => {
    setExpanded((prev) => ({ ...prev, [key]: !prev[key] }));
  };

  /* ── overall status ── */

  const statsArr = stats ? Object.values(stats).filter((s) => s !== undefined) : [];
  const allOk = statsArr.length > 0 && statsArr.every((s) => s.ok);
  const anyOk = statsArr.some((s) => s.ok);

  /* ── render ── */

  return (
    <div className="space-y-4">
      {/* ── header ── */}
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-3">
          <h2 className="text-lg font-semibold text-zinc-100">Service Status</h2>
          {stats && (
            <div className="flex items-center gap-1.5 text-xs">
              <StatusDot ok={allOk} partial={!allOk && anyOk} pulse={!loading} />
              <span
                className={
                  allOk
                    ? 'text-emerald-400'
                    : anyOk
                      ? 'text-amber-400'
                      : 'text-red-400'
                }
              >
                {allOk ? 'All Online' : anyOk ? 'Degraded' : 'Offline'}
              </span>
            </div>
          )}
        </div>

        <div className="flex items-center gap-2">
          {lastRefresh && (
            <span className="text-[10px] text-zinc-600 hidden sm:inline">
              {lastRefresh.toLocaleTimeString()}
            </span>
          )}
          <button
            onClick={() => setShowConfig(!showConfig)}
            className="text-xs px-2.5 py-1.5 rounded-lg border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors"
          >
            {showConfig ? 'Close Config' : 'Config'}
          </button>
          <button
            onClick={fetchStats}
            disabled={loading}
            className="text-xs px-2.5 py-1.5 rounded-lg border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors disabled:opacity-40"
          >
            {loading ? '...' : 'Refresh'}
          </button>
        </div>
      </div>

      {/* ── config panel ── */}
      {showConfig && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-xl p-4 space-y-3">
          <div>
            <label className="text-xs text-zinc-500 block mb-1">
              Registration Server URL
            </label>
            <input
              value={regServerInput}
              onChange={(e) => setRegServerInput(e.target.value)}
              onBlur={handleRegServerBlur}
              onKeyDown={handleRegServerKeyDown}
              placeholder="https://reg.example.com"
              className="w-full bg-zinc-950 border border-zinc-700 rounded-lg px-3 py-2 text-xs font-mono
                         focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 transition-colors"
            />
            <p className="text-[10px] text-zinc-600 mt-1">
              Leave empty to skip registration server status.
            </p>
          </div>
          <label className="flex items-center gap-2 text-xs text-zinc-400 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={autoRefresh}
              onChange={(e) => setAutoRefresh(e.target.checked)}
              className="rounded border-zinc-600 bg-zinc-800 text-emerald-500 focus:ring-emerald-500/30 focus:ring-offset-0"
            />
            Auto-refresh every 5 seconds
          </label>
        </div>
      )}

      {/* ── loading state ── */}
      {loading && !stats && (
        <div className="text-center py-16">
          <div className="inline-block w-6 h-6 border-2 border-zinc-600 border-t-zinc-300 rounded-full animate-spin" />
          <p className="text-xs text-zinc-500 mt-3">Fetching service status...</p>
        </div>
      )}

      {/* ── cards grid ── */}
      {stats && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {/* ── Relay Node ── */}
          <ServiceCard
            icon={'\u{1F504}'}
            name="Relay Node"
            ok={stats.relay.ok}
            expanded={expanded.relay}
            onToggle={() => toggleExpand('relay')}
            badge={
              stats.relay.ok
                ? {
                    label: stats.relay.data.relay_mode || 'off',
                    className:
                      stats.relay.data.relay_mode === 'server'
                        ? 'bg-blue-900/50 text-blue-300'
                        : stats.relay.data.relay_mode === 'client'
                          ? 'bg-purple-900/50 text-purple-300'
                          : 'bg-zinc-800 text-zinc-500',
                  }
                : null
            }
            metrics={
              stats.relay.ok
                ? [
                    {
                      label: 'Peers',
                      value: formatNumber(stats.relay.data.conn_stats?.known_peers ?? 0),
                    },
                    {
                      label: 'Connected',
                      value: formatNumber((stats.relay.data.signal_peers || []).length),
                    },
                    {
                      label: 'WS Conn',
                      value: formatNumber(stats.relay.data.ws_connections ?? 0),
                    },
                  ]
                : null
            }
          >
            {stats.relay.ok ? (
              <div className="space-y-1.5">
                <StatRow
                  label="Peer ID"
                  value={stats.relay.data.peer_id?.substring(0, 28) + '...'}
                />
                <StatRow
                  label="Hole Punch"
                  value={stats.relay.data.hole_punch ? 'Enabled' : 'Disabled'}
                />
                <StatRow
                  label="NAT Type"
                  value={stats.relay.data.nat_type || '-'}
                />
                <div className="border-t border-zinc-800 pt-1.5 mt-1.5">
                  <span className="text-zinc-500 text-[10px] block mb-1">
                    Connection Stats
                  </span>
                  <div className="grid grid-cols-3 gap-2 text-center">
                    <div>
                      <div className="text-sm font-bold text-zinc-300 font-mono">
                        {formatNumber(stats.relay.data.conn_stats?.successful_conns ?? 0)}
                      </div>
                      <div className="text-[10px] text-emerald-500">OK</div>
                    </div>
                    <div>
                      <div className="text-sm font-bold text-zinc-300 font-mono">
                        {formatNumber(stats.relay.data.conn_stats?.failed_conns ?? 0)}
                      </div>
                      <div className="text-[10px] text-red-500">Fail</div>
                    </div>
                    <div>
                      <div className="text-sm font-bold text-zinc-300 font-mono">
                        {formatNumber(stats.relay.data.conn_stats?.known_peers ?? 0)}
                      </div>
                      <div className="text-[10px] text-zinc-500">Known</div>
                    </div>
                  </div>
                </div>
                {stats.relay.data.addresses?.length > 0 && (
                  <div className="border-t border-zinc-800 pt-1.5 mt-1.5">
                    <span className="text-zinc-500 text-[10px] block mb-1">
                      Addresses
                    </span>
                    <div className="space-y-0.5 max-h-20 overflow-y-auto">
                      {stats.relay.data.addresses.map((a, i) => (
                        <div key={i} className="text-zinc-600 text-[10px] font-mono truncate">
                          {a}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
                {stats.relay.data.active_transfers?.length > 0 && (
                  <div className="border-t border-zinc-800 pt-1.5 mt-1.5">
                    <span className="text-zinc-500 text-[10px] block mb-1">
                      Active Transfers ({stats.relay.data.active_transfers.length})
                    </span>
                    {stats.relay.data.active_transfers.slice(0, 5).map((t, i) => (
                      <div key={i} className="flex items-center justify-between text-[10px] py-0.5">
                        <span className="text-zinc-400 font-mono truncate mr-2">
                          {(t.hash || '').substring(0, 16)}...
                        </span>
                        <span className="text-zinc-500 shrink-0">
                          {t.progress?.toFixed(0)}%
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ) : (
              <div className="space-y-1">
                <p className="text-red-400/80 text-xs">
                  {stats.relay.error || 'Service unreachable'}
                </p>
              </div>
            )}
          </ServiceCard>

          {/* ── Registration Server ── */}
          <ServiceCard
            icon={'\u{1F4CB}'}
            name="Registration Server"
            ok={stats.reg.ok}
            expanded={expanded.reg}
            onToggle={() => toggleExpand('reg')}
            badge={
              stats.reg.ok && stats.reg.data?.jwt
                ? {
                    label: stats.reg.data.jwt.role || 'user',
                    className: 'bg-emerald-900/50 text-emerald-300',
                  }
                : stats.reg.ok
                  ? {
                      label: 'no auth',
                      className: 'bg-zinc-800 text-zinc-500',
                    }
                  : null
            }
            metrics={
              stats.reg.ok
                ? [
                    {
                      label: 'Status',
                      value:
                        typeof stats.reg.data?.ping?.status === 'string'
                          ? stats.reg.data.ping.status
                          : 'OK',
                    },
                    {
                      label: 'JWT',
                      value: stats.reg.data?.jwt ? stats.reg.data.jwt.username || 'Active' : 'N/A',
                    },
                  ]
                : !regServerUrl
                  ? [
                      { label: 'Status', value: 'No URL' },
                      { label: 'JWT', value: '-' },
                    ]
                  : null
            }
          >
            {stats.reg.ok ? (
              <div className="space-y-1.5">
                <StatRow
                  label="Service"
                  value={stats.reg.data.ping?.service || 'peerdrive-registration'}
                />
                <StatRow
                  label="Server URL"
                  value={regServerUrl}
                />
                <div className="border-t border-zinc-800 pt-1.5 mt-1.5">
                  <span className="text-zinc-500 text-[10px] block mb-1">
                    JWT Authentication
                  </span>
                  {stats.reg.data.jwt ? (
                    <>
                      <StatRow
                        label="Username"
                        value={stats.reg.data.jwt.username || '-'}
                      />
                      <StatRow
                        label="Role"
                        value={stats.reg.data.jwt.role || 'user'}
                      />
                    </>
                  ) : (
                    <p className="text-zinc-500 text-xs">
                      No JWT token configured or invalid. Set auth credentials in Settings.
                    </p>
                  )}
                </div>
              </div>
            ) : !regServerUrl ? (
              <div className="space-y-1">
                <p className="text-zinc-500 text-xs">
                  No registration server URL configured. Add one in the config panel above.
                </p>
              </div>
            ) : (
              <div className="space-y-1">
                <p className="text-red-400/80 text-xs">
                  {stats.reg.error || 'Service unreachable'}
                </p>
                <p className="text-zinc-600 text-[10px]">
                  URL: {regServerUrl}
                </p>
              </div>
            )}
          </ServiceCard>

          {/* ── Storage ── */}
          <ServiceCard
            icon={'\u{1F4BE}'}
            name="Storage"
            ok={stats.storage.ok}
            expanded={expanded.storage}
            onToggle={() => toggleExpand('storage')}
            metrics={
              stats.storage.ok
                ? [
                    {
                      label: 'Files',
                      value: formatNumber(stats.storage.data.totalFiles),
                    },
                    {
                      label: 'Total Size',
                      value: formatBytes(stats.storage.data.totalSize),
                    },
                  ]
                : null
            }
          >
            {stats.storage.ok ? (
              <div className="space-y-1.5">
                <StatRow
                  label="Total Files"
                  value={formatNumber(stats.storage.data.totalFiles)}
                />
                <StatRow
                  label="Total Size"
                  value={formatBytes(stats.storage.data.totalSize)}
                />
                <StatRow
                  label="Avg File Size"
                  value={
                    stats.storage.data.totalFiles > 0
                      ? formatBytes(
                          stats.storage.data.totalSize / stats.storage.data.totalFiles,
                        )
                      : '-'
                  }
                />
                <div className="border-t border-zinc-800 pt-1.5 mt-1.5">
                  <span className="text-zinc-500 text-[10px] block mb-1">
                    Recent Files
                  </span>
                  <div className="space-y-1 max-h-40 overflow-y-auto">
                    {stats.storage.data.files.length === 0 ? (
                      <p className="text-zinc-600 text-xs">No files stored</p>
                    ) : (
                      stats.storage.data.files
                        .slice(-10)
                        .reverse()
                        .map((f, i) => (
                          <div
                            key={f.hash || i}
                            className="flex items-center justify-between text-[10px] py-0.5"
                          >
                            <span className="text-zinc-400 truncate mr-2 max-w-[180px]">
                              {f.filename ||
                                f.name ||
                                (f.hash || '').substring(0, 16)}
                            </span>
                            <span className="text-zinc-600 shrink-0 font-mono">
                              {formatBytes(f.size)}
                            </span>
                          </div>
                        ))
                    )}
                  </div>
                </div>
              </div>
            ) : (
              <div className="space-y-1">
                <p className="text-red-400/80 text-xs">
                  {stats.storage.error || 'Service unreachable'}
                </p>
              </div>
            )}
          </ServiceCard>

          {/* ── BT DHT ── */}
          <ServiceCard
            icon={'\u{1F9F2}'}
            name="BT DHT"
            ok={stats.bt.ok}
            expanded={expanded.bt}
            onToggle={() => toggleExpand('bt')}
            badge={
              stats.bt.ok
                ? {
                    label: stats.bt.data?.enabled ? 'enabled' : 'disabled',
                    className: stats.bt.data?.enabled
                      ? 'bg-emerald-900/50 text-emerald-300'
                      : 'bg-zinc-800 text-zinc-500',
                  }
                : null
            }
            metrics={
              stats.bt.ok && stats.bt.data?.enabled
                ? [
                    {
                      label: 'Nodes',
                      value: formatNumber(stats.bt.data.num_nodes ?? 0),
                    },
                    {
                      label: 'Listen',
                      value: stats.bt.data.listen_addr
                        ? stats.bt.data.listen_addr.split(':').pop() || '-'
                        : '-',
                    },
                  ]
                : stats.bt.ok
                  ? [{ label: 'Status', value: 'Disabled' }]
                  : null
            }
          >
            {stats.bt.ok ? (
              <div className="space-y-1.5">
                {stats.bt.data?.enabled ? (
                  <>
                    <StatRow
                      label="Enabled"
                      value="Yes"
                    />
                    <StatRow
                      label="Listen Address"
                      value={stats.bt.data.listen_addr || '-'}
                    />
                    <StatRow
                      label="DHT Nodes"
                      value={formatNumber(stats.bt.data.num_nodes ?? 0)}
                    />
                  </>
                ) : (
                  <p className="text-zinc-500 text-xs">
                    BitTorrent DHT is not enabled on this node.
                  </p>
                )}
              </div>
            ) : (
              <div className="space-y-1">
                <p className="text-red-400/80 text-xs">
                  {stats.bt.error || 'Service unreachable'}
                </p>
              </div>
            )}
          </ServiceCard>

          {/* ── WebSocket ── */}
          <ServiceCard
            icon={'\u{1F517}'}
            name="WebSocket"
            ok={stats.ws.ok}
            expanded={expanded.ws}
            onToggle={() => toggleExpand('ws')}
            metrics={
              stats.ws.ok
                ? [
                    {
                      label: 'Connections',
                      value: formatNumber(stats.ws.data?.ws_connections ?? 0),
                    },
                    {
                      label: 'Endpoint',
                      value: stats.ws.data?.ws_endpoint
                        ? stats.ws.data.ws_endpoint.replace('/ws/', '')
                        : '-',
                    },
                    {
                      label: 'Types',
                      value: (stats.ws.data?.message_types || []).length || '-',
                    },
                  ]
                : null
            }
          >
            {stats.ws.ok ? (
              <div className="space-y-1.5">
                <StatRow
                  label="Active Connections"
                  value={formatNumber(stats.ws.data?.ws_connections ?? 0)}
                />
                <StatRow
                  label="WS Endpoint"
                  value={stats.ws.data?.ws_endpoint || '-'}
                />
                <StatRow
                  label="Message Types"
                  value={(stats.ws.data?.message_types || []).join(', ') || '-'}
                />
              </div>
            ) : (
              <div className="space-y-1">
                <p className="text-red-400/80 text-xs">
                  {stats.ws.error || 'Service unreachable'}
                </p>
              </div>
            )}
          </ServiceCard>
        </div>
      )}
    </div>
  );
}
