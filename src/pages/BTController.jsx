// BT 下载控制器：完整的 BitTorrent 下载管理面板，类 qBittorrent/Transmission 风格
import React, { useState, useEffect, useCallback, useRef } from 'react';
import * as api from '../api';

/* ---- Helpers ---- */

function formatSize(bytes) {
  if (bytes == null || isNaN(bytes)) return '-';
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return (bytes / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
}

function formatSpeed(bytesPerSec) {
  if (bytesPerSec == null || isNaN(bytesPerSec)) return '-';
  if (bytesPerSec === 0) return '0 B/s';
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytesPerSec) / Math.log(1024)));
  return (bytesPerSec / Math.pow(1024, i)).toFixed(i <= 1 ? 0 : 1) + ' ' + units[i];
}

function formatETA(remainingBytes, speedBytesPerSec) {
  if (remainingBytes == null || speedBytesPerSec == null || speedBytesPerSec <= 0) return '∞';
  const secs = remainingBytes / speedBytesPerSec;
  if (secs < 60) return Math.round(secs) + 's';
  if (secs < 3600) return Math.floor(secs / 60) + 'm ' + Math.round(secs % 60) + 's';
  if (secs < 86400) return Math.floor(secs / 3600) + 'h ' + Math.floor((secs % 3600) / 60) + 'm';
  return Math.floor(secs / 86400) + 'd ' + Math.floor((secs % 86400) / 3600) + 'h';
}

function statusBadge(status) {
  const map = {
    downloading: { label: '下载中', cls: 'bg-blue-600/30 text-blue-300 border-blue-700/40' },
    seeding:     { label: '做种中', cls: 'bg-emerald-600/30 text-emerald-300 border-emerald-700/40' },
    completed:   { label: '已完成', cls: 'bg-emerald-600/30 text-emerald-300 border-emerald-700/40' },
    error:       { label: '错误',   cls: 'bg-red-600/30 text-red-300 border-red-700/40' },
    paused:      { label: '已暂停', cls: 'bg-yellow-600/30 text-yellow-300 border-yellow-700/40' },
    queued:      { label: '排队中', cls: 'bg-gray-600/30 text-gray-300 border-gray-700/40' },
    checking:    { label: '校验中', cls: 'bg-purple-600/30 text-purple-300 border-purple-700/40' },
  };
  const entry = map[status] || { label: status || '未知', cls: 'bg-gray-600/30 text-gray-300 border-gray-700/40' };
  return (
    <span className={`inline-block px-2 py-0.5 rounded text-[10px] font-medium border ${entry.cls}`}>
      {entry.label}
    </span>
  );
}

function progressFillCls(status) {
  if (status === 'seeding' || status === 'completed') return 'bg-emerald-500';
  if (status === 'paused') return 'bg-yellow-500';
  if (status === 'error') return 'bg-red-500';
  return 'bg-blue-500';
}

/* ============ Main Component ============ */

export default function BTController() {
  const [downloads, setDownloads] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [input, setInput] = useState('');
  const [adding, setAdding] = useState(false);
  const [expanded, setExpanded] = useState({});
  const [statusMsg, setStatusMsg] = useState(null);
  const fileRef = useRef(null);
  const pollRef = useRef(null);

  /* ---- Poll downloads every 2s ---- */
  const fetchDownloads = useCallback(async (showLoading) => {
    if (showLoading) setLoading(true);
    try {
      const data = await api.btGetDownloads();
      setDownloads(Array.isArray(data) ? data : (data.downloads || []));
      setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchDownloads(true);
    pollRef.current = setInterval(() => fetchDownloads(false), 2000);
    return () => clearInterval(pollRef.current);
  }, [fetchDownloads]);

  /* ---- Add torrent ---- */
  const handleAdd = async () => {
    const v = input.trim();
    if (!v) return;
    setAdding(true);
    setStatusMsg(null);
    try {
      if (v.startsWith('magnet:')) {
        await api.btMagnetResolve(v);
        setStatusMsg({ type: 'success', text: 'magnet 链接已添加' });
      } else if (v.startsWith('http://') || v.startsWith('https://')) {
        // Fetch .torrent URL and upload
        const resp = await fetch(v);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const blob = await resp.blob();
        const file = new File([blob], 'torrent.torrent', { type: 'application/x-bittorrent' });
        await api.btTorrentUpload(file);
        setStatusMsg({ type: 'success', text: '种子 URL 已添加' });
      } else if (/^[0-9a-fA-F]{40}$/.test(v)) {
        // Plain infohash — wrap as magnet
        await api.btMagnetResolve(`magnet:?xt=urn:btih:${v}`);
        setStatusMsg({ type: 'success', text: 'Infohash 已添加' });
      } else {
        setStatusMsg({ type: 'error', text: '无法识别的输入。请输入 magnet 链接、种子 URL 或 40 位 infohash' });
      }
      setInput('');
      // Immediately refresh after adding
      setTimeout(() => fetchDownloads(false), 500);
    } catch (e) {
      setStatusMsg({ type: 'error', text: '添加失败: ' + e.message });
    } finally {
      setAdding(false);
    }
  };

  /* ---- Upload .torrent file ---- */
  const handleFileUpload = async (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    setAdding(true);
    setStatusMsg(null);
    try {
      await api.btTorrentUpload(f);
      setStatusMsg({ type: 'success', text: `种子文件 "${f.name}" 已添加` });
      setTimeout(() => fetchDownloads(false), 500);
    } catch (e) {
      setStatusMsg({ type: 'error', text: '上传失败: ' + e.message });
    } finally {
      setAdding(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  };

  /* ---- Actions ---- */
  const handlePause = async (ih) => {
    try { await api.btPauseDownload(ih); fetchDownloads(false); }
    catch (e) { setStatusMsg({ type: 'error', text: e.message }); }
  };
  const handleResume = async (ih) => {
    try { await api.btResumeDownload(ih); fetchDownloads(false); }
    catch (e) { setStatusMsg({ type: 'error', text: e.message }); }
  };
  const handleRemove = async (ih) => {
    if (!confirm('确定要移除这个下载任务吗？')) return;
    try { await api.btRemoveDownload(ih); fetchDownloads(false); }
    catch (e) { setStatusMsg({ type: 'error', text: e.message }); }
  };

  /* ---- Expand / Collapse ---- */
  const toggleExpand = (ih) => {
    setExpanded(prev => ({ ...prev, [ih]: !prev[ih] }));
  };

  /* ---- Footer stats ---- */
  const stats = (() => {
    let dling = 0, seeding = 0, completed = 0, queued = 0, errored = 0, paused = 0;
    let downSpeed = 0, upSpeed = 0;
    for (const d of downloads) {
      if (d.status === 'downloading') dling++;
      else if (d.status === 'seeding') seeding++;
      else if (d.status === 'completed') completed++;
      else if (d.status === 'queued') queued++;
      else if (d.status === 'error') errored++;
      else if (d.status === 'paused') paused++;
      downSpeed += d.download_speed || d.speed_bytes_per_sec || 0;
      upSpeed += d.upload_speed || 0;
    }
    return { dling, seeding, completed, queued, errored, paused, downSpeed, upSpeed, total: downloads.length };
  })();

  /* ---- Key handler: Enter to add ---- */
  const handleKeyDown = (e) => {
    if (e.key === 'Enter') handleAdd();
  };

  /* ---- Paste detection for infohash ---- */
  const handlePaste = (e) => {
    const text = (e.clipboardData || window.clipboardData).getData('text');
    // Auto-trim if pasted content is a raw infohash or magnet
    const trimmed = text.trim();
    if (trimmed.startsWith('magnet:') || /^[0-9a-fA-F]{40}$/.test(trimmed)) {
      // Don't hijack the paste, just let it flow to the input; the button handler will process it
    }
  };

  return (
    <div className="h-full flex flex-col bg-gray-950 text-gray-200 overflow-hidden">
      {/* ===== Top: Add Torrent Section ===== */}
      <div className="shrink-0 border-b border-gray-800 bg-gray-900/60 backdrop-blur-sm">
        <div className="max-w-7xl mx-auto px-4 md:px-6 py-4">
          <div className="flex items-center justify-between mb-3">
            <h1 className="text-lg md:text-xl font-bold text-gray-100 flex items-center gap-2">
              <span>BT 下载控制器</span>
              <span className="text-xs text-gray-500 font-normal">qBittorrent-style</span>
            </h1>
            <button
              onClick={() => fetchDownloads(true)}
              className="text-xs text-gray-500 hover:text-gray-300 px-3 py-1.5 rounded-lg border border-gray-700 hover:border-gray-500 transition-colors"
            >
              刷新
            </button>
          </div>

          {/* Input row */}
          <div className="flex flex-wrap gap-2 items-center">
            <div className="flex-1 min-w-[200px] relative">
              <input
                value={input}
                onChange={e => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                onPaste={handlePaste}
                placeholder="magnet:?xt=urn:btih:... 或 .torrent URL 或 Infohash"
                className="w-full bg-gray-950 border border-gray-700 rounded-lg px-3 py-2 text-xs font-mono
                           focus:outline-none focus:border-blue-500 placeholder:text-gray-600 transition-colors"
              />
              {input && (
                <button
                  onClick={() => setInput('')}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-600 hover:text-gray-400 text-sm"
                >
                  &#x2715;
                </button>
              )}
            </div>
            <button
              onClick={handleAdd}
              disabled={adding || !input.trim()}
              className="px-5 py-2 rounded-lg text-xs font-medium bg-blue-600 text-white hover:bg-blue-500
                         disabled:opacity-40 disabled:cursor-not-allowed transition-colors whitespace-nowrap"
            >
              {adding ? '添加中...' : '添加'}
            </button>
            <label className="px-4 py-2 rounded-lg text-xs font-medium bg-gray-800 text-gray-300 hover:bg-gray-700
                              border border-gray-700 cursor-pointer transition-colors whitespace-nowrap">
              选择种子文件
              <input ref={fileRef} type="file" accept=".torrent,application/x-bittorrent" onChange={handleFileUpload} className="hidden" />
            </label>
          </div>
          {statusMsg && (
            <div className={`mt-2 px-3 py-1.5 rounded-lg text-xs border transition-opacity ${
              statusMsg.type === 'error'
                ? 'bg-red-900/20 border-red-900/40 text-red-400'
                : 'bg-emerald-900/20 border-emerald-900/40 text-emerald-300'
            }`}>
              {statusMsg.text}
              <button onClick={() => setStatusMsg(null)} className="ml-2 opacity-60 hover:opacity-100">&#x2715;</button>
            </div>
          )}
        </div>
      </div>

      {/* ===== Middle: Download List ===== */}
      <div className="flex-1 overflow-auto">
        <div className="max-w-7xl mx-auto">
          {loading && downloads.length === 0 ? (
            <div className="flex items-center justify-center h-64 text-gray-500 text-sm">
              <div className="flex items-center gap-3">
                <svg className="animate-spin h-5 w-5 text-gray-500" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                加载中...
              </div>
            </div>
          ) : error && downloads.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-64 text-gray-500 gap-3">
              <p className="text-sm text-red-400">无法获取下载列表: {error}</p>
              <button onClick={() => fetchDownloads(true)} className="text-xs text-blue-400 hover:text-blue-300 underline">重试</button>
            </div>
          ) : downloads.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-64 text-gray-500 gap-2">
              <span className="text-3xl">&#x1F4E5;</span>
              <p className="text-sm">没有活动的下载任务</p>
              <p className="text-xs text-gray-600">输入 magnet 链接、种子 URL 或上传 .torrent 文件开始下载</p>
            </div>
          ) : (
            <div className="p-4 md:p-6">
              {/* Table header - hidden on very small screens */}
              <div className="hidden md:flex items-center text-[10px] text-gray-500 uppercase tracking-wider px-4 py-2 border-b border-gray-800">
                <div className="w-[6px] shrink-0" />
                <div className="flex-1 min-w-0 pl-2">名称</div>
                <div className="w-20 text-right">大小</div>
                <div className="w-40">进度</div>
                <div className="w-20 text-right">下载</div>
                <div className="w-20 text-right">上传</div>
                <div className="w-24 text-right">剩余时间</div>
                <div className="w-24 text-center">种子/对等</div>
                <div className="w-20 text-center">状态</div>
                <div className="w-20 text-right">操作</div>
              </div>

              {/* Table rows */}
              <div className="divide-y divide-gray-800/50">
                {downloads.map((d) => {
                  const pct = d.total_size > 0 ? Math.min(100, ((d.downloaded || 0) / d.total_size) * 100) : 0;
                  const remaining = (d.total_size || 0) - (d.downloaded || 0);
                  const downSpeed = d.download_speed || d.speed_bytes_per_sec || 0;
                  const upSpeed = d.upload_speed || 0;
                  const peers = d.peers ?? d.connected_peers ?? 0;
                  const seeds = d.seeds ?? d.connected_seeds ?? 0;
                  const isExpanded = expanded[d.infohash];
                  const hasFiles = d.files && d.files.length > 0;

                  return (
                    <div key={d.infohash || d.name} className="group">
                      {/* Row */}
                      <div className="flex flex-wrap md:flex-nowrap items-center gap-y-1.5 gap-x-2 px-4 py-3 hover:bg-gray-900/60 transition-colors">
                        {/* Expand toggle */}
                        <div className="w-[6px] shrink-0 flex justify-center">
                          {hasFiles && (
                            <button
                              onClick={() => toggleExpand(d.infohash)}
                              className="text-gray-600 hover:text-gray-400 text-[10px] leading-none transition-colors"
                            >
                              {isExpanded ? '▼' : '▶'}
                            </button>
                          )}
                        </div>

                        {/* Name */}
                        <div className="flex-1 min-w-0 md:pl-2">
                          <div className="text-sm font-medium text-gray-200 truncate" title={d.name}>
                            {d.name || d.infohash?.substring(0, 16) + '...' || '未命名'}
                          </div>
                          {/* Mobile-only info row */}
                          <div className="flex md:hidden flex-wrap items-center gap-x-3 gap-y-1 mt-1 text-[10px] text-gray-500">
                            <span>{formatSize(d.total_size)}</span>
                            <span>{statusBadge(d.status)}</span>
                            <span>DL: {formatSpeed(downSpeed)}</span>
                            <span>UL: {formatSpeed(upSpeed)}</span>
                          </div>
                        </div>

                        {/* Size - hidden on mobile (shown in inline above) */}
                        <div className="hidden md:block w-20 text-right text-xs text-gray-400">
                          {formatSize(d.total_size)}
                        </div>

                        {/* Progress */}
                        <div className="w-full md:w-40 order-last md:order-none mt-1 md:mt-0">
                          <div className="flex items-center gap-2">
                            <div className="flex-1 bg-gray-700 rounded-full h-2 overflow-hidden">
                              <div
                                className={`h-full rounded-full transition-all duration-500 ${progressFillCls(d.status)}`}
                                style={{ width: `${pct}%` }}
                              />
                            </div>
                            <span className="text-[10px] text-gray-400 whitespace-nowrap w-20 text-right">
                              {pct.toFixed(1)}%
                            </span>
                          </div>
                          <div className="text-[9px] text-gray-600 mt-0.5">
                            {(d.pieces_done ?? 0)}/{d.pieces_total ?? '?'} pieces
                          </div>
                        </div>

                        {/* Download speed */}
                        <div className="hidden md:block w-20 text-right text-xs text-blue-400">
                          {formatSpeed(downSpeed)}
                        </div>

                        {/* Upload speed */}
                        <div className="hidden md:block w-20 text-right text-xs text-emerald-400">
                          {formatSpeed(upSpeed)}
                        </div>

                        {/* ETA */}
                        <div className="hidden md:block w-24 text-right text-xs text-gray-400">
                          {d.status === 'completed' || d.status === 'seeding'
                            ? '∞'
                            : d.status === 'paused'
                              ? '-'
                              : formatETA(remaining, downSpeed)}
                        </div>

                        {/* Seeds / Peers */}
                        <div className="hidden md:block w-24 text-center text-xs text-gray-400">
                          <span className="text-emerald-400">{seeds}</span>
                          <span className="text-gray-600"> / </span>
                          <span className="text-blue-400">{peers}</span>
                        </div>

                        {/* Status badge */}
                        <div className="hidden md:block w-20 text-center">
                          {statusBadge(d.status)}
                        </div>

                        {/* Actions */}
                        <div className="hidden md:flex w-20 items-center justify-end gap-1.5">
                          {(d.status === 'downloading' || d.status === 'seeding') ? (
                            <button
                              onClick={() => handlePause(d.infohash)}
                              title="暂停"
                              className="p-1.5 rounded text-gray-500 hover:text-yellow-400 hover:bg-gray-800 transition-colors"
                            >
                              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" /><rect x="14" y="4" width="4" height="16" /></svg>
                            </button>
                          ) : (
                            <button
                              onClick={() => handleResume(d.infohash)}
                              title="继续"
                              className="p-1.5 rounded text-gray-500 hover:text-emerald-400 hover:bg-gray-800 transition-colors"
                            >
                              <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor"><polygon points="8,5 19,12 8,19" /></svg>
                            </button>
                          )}
                          <button
                            onClick={() => handleRemove(d.infohash)}
                            title="移除"
                            className="p-1.5 rounded text-gray-500 hover:text-red-400 hover:bg-gray-800 transition-colors"
                          >
                            <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M3 6h18M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" /></svg>
                          </button>
                        </div>

                        {/* Mobile actions */}
                        <div className="flex md:hidden items-center gap-2 ml-auto">
                          {(d.status === 'downloading' || d.status === 'seeding') ? (
                            <button onClick={() => handlePause(d.infohash)} className="text-yellow-400 text-xs px-2 py-1 rounded bg-gray-800">暂停</button>
                          ) : (
                            <button onClick={() => handleResume(d.infohash)} className="text-emerald-400 text-xs px-2 py-1 rounded bg-gray-800">继续</button>
                          )}
                          <button onClick={() => handleRemove(d.infohash)} className="text-red-400 text-xs px-2 py-1 rounded bg-gray-800">移除</button>
                        </div>
                      </div>

                      {/* Expanded file list */}
                      {isExpanded && hasFiles && (
                        <div className="bg-gray-900/40 border-t border-gray-800/30 px-4 py-2">
                          <div className="text-[10px] text-gray-500 mb-1.5 pl-1">
                            文件列表 ({d.files.length})
                          </div>
                          <div className="max-h-48 overflow-y-auto space-y-0.5">
                            {d.files.map((f, i) => (
                              <div key={i} className="flex items-center gap-3 px-3 py-1.5 rounded hover:bg-gray-800/50 text-xs">
                                <span className="text-gray-600 w-5 text-right shrink-0">{i + 1}</span>
                                <span className="text-gray-400 truncate flex-1 font-mono text-[11px]" title={f.path}>
                                  {f.path}
                                </span>
                                <span className="text-gray-500 shrink-0">{formatSize(f.size)}</span>
                              </div>
                            ))}
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* ===== Bottom: Stats Footer ===== */}
      <div className="shrink-0 border-t border-gray-800 bg-gray-900/80 backdrop-blur-sm">
        <div className="max-w-7xl mx-auto px-4 md:px-6 py-3">
          <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1.5 text-xs">
            <div className="flex items-center gap-4 text-gray-400">
              <span>
                总计: <span className="text-gray-200 font-medium">{stats.total}</span> 个任务
              </span>
              {stats.dling > 0 && <span className="text-blue-400">{stats.dling} 下载中</span>}
              {(stats.seeding + stats.completed) > 0 && (
                <span className="text-emerald-400">{stats.seeding + stats.completed} 已完成</span>
              )}
              {stats.queued > 0 && <span className="text-gray-500">{stats.queued} 排队中</span>}
              {stats.paused > 0 && <span className="text-yellow-400">{stats.paused} 已暂停</span>}
              {stats.errored > 0 && <span className="text-red-400">{stats.errored} 错误</span>}
            </div>
            <div className="flex items-center gap-4 text-gray-400">
              <span>
                <span className="text-gray-500">全局: </span>
                <span className="text-blue-400">&#x2193; {formatSpeed(stats.downSpeed)}</span>
                <span className="mx-1.5 text-gray-600">/</span>
                <span className="text-emerald-400">&#x2191; {formatSpeed(stats.upSpeed)}</span>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
