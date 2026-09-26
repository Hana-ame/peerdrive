// 节点控制页：连接成功后跳转过来的对端节点控制屏幕。
// 从全局会话取已连接的对端：节点信息 / 共享（文件·合集）/ 保存 / 断开。
import React, { useState, useEffect, useCallback, useRef } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { getNodeSession, clearNodeSession } from '../lib/nodeSession';

function fmtBytes(n) {
  if (!n || n === 0) return '—';
  const u = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(v < 10 ? 1 : 0) + ' ' + u[i];
}

// ── 预览支持：按文件名后缀分类（图片 / 视频 / 音频 / 文本）──
const IMG_EXT = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'avif'];
const VID_EXT = ['mp4', 'webm', 'mov', 'm4v', 'ogv'];
const AUD_EXT = ['mp3', 'wav', 'flac', 'ogg', 'oga', 'aac', 'm4a', 'opus'];
const TXT_EXT = ['txt', 'md', 'markdown', 'json', 'js', 'ts', 'jsx', 'tsx', 'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'hpp', 'xml', 'html', 'htm', 'css', 'scss', 'yaml', 'yml', 'csv', 'log', 'sql', 'sh', 'toml', 'ini', 'conf'];

function extOf(name) {
  const m = String(name || '').toLowerCase().match(/\.([a-z0-9]+)$/);
  return m ? m[1] : '';
}

function kindOf(name) {
  const ext = extOf(name);
  if (IMG_EXT.includes(ext)) return 'image';
  if (VID_EXT.includes(ext)) return 'video';
  if (AUD_EXT.includes(ext)) return 'audio';
  if (TXT_EXT.includes(ext)) return 'text';
  return null;
}

function mimeOf(name) {
  const ext = extOf(name);
  const map = {
    jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
    webp: 'image/webp', bmp: 'image/bmp', svg: 'image/svg+xml', avif: 'image/avif',
    mp4: 'video/mp4', webm: 'video/webm', mov: 'video/quicktime', m4v: 'video/x-m4v', ogv: 'video/ogg',
    mp3: 'audio/mpeg', wav: 'audio/wav', flac: 'audio/flac', ogg: 'audio/ogg', oga: 'audio/ogg',
    aac: 'audio/aac', m4a: 'audio/mp4', opus: 'audio/opus',
    txt: 'text/plain', md: 'text/markdown', json: 'application/json', csv: 'text/csv',
    html: 'text/html', htm: 'text/html', xml: 'text/xml', css: 'text/css', js: 'text/javascript',
  };
  return map[ext] || '';
}

export default function NodeControl() {
  const navigate = useNavigate();
  const session = getNodeSession();
  const [share, setShare] = useState(null); // null=加载中
  const [err, setErr] = useState('');
  const [collOpen, setCollOpen] = useState(null);
  const [preview, setPreview] = useState(null); // {hash,name,kind,url?,text?,loading,error,imgIndex?}
  // 图片查看器：缩放 + 拖拽平移
  const [viewer, setViewer] = useState({ scale: 1, x: 0, y: 0 });
  const dragRef = useRef(null);
  const [downloading, setDownloading] = useState(null); // {hash,name,loaded,total}

  const loadShares = useCallback(async () => {
    if (!session?.client) return;
    setErr('');
    try {
      const s = await session.client.shares();
      setShare(s);
    } catch (e) { setErr(e?.message || String(e)); setShare({ total: 0, files: [], collections: [] }); }
  }, [session]);

  useEffect(() => { loadShares(); }, [loadShares]);

  const save = async (item) => {
    if (downloading) return;
    const name = item.path || item.name || 'download';
    const total = item.size || 0;
    setDownloading({ hash: item.hash, name, loaded: 0, total });
    try {
      // stream 流式拉取 + 进度；收齐后触发浏览器下载
      const chunks = [];
      let loaded = 0;
      for await (const chunk of session.client.stream(item.hash)) {
        chunks.push(chunk);
        loaded += chunk.byteLength;
        setDownloading(d => (d && d.hash === item.hash ? { ...d, loaded } : d));
        await new Promise(r => setTimeout(r, 0));
      }
      const blob = new Blob(chunks, { type: mimeOf(name) });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = String(name).split(/[\\/]/).pop() || 'download';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 15000);
    } catch (e) {
      setErr(e?.message || String(e));
    } finally {
      setDownloading(null);
    }
  };

  // 点击文件名：可预览 → 预览；否则直接下载
  const openFile = (item) => {
    const name = item.path || item.name || '';
    if (kindOf(name)) openPreview(item);
    else save(item);
  };

  // 预览：媒体（图片/视频/音频）走 sw.js 伪造 fetch（/swdrive/<hash> 带 Range 断点续传），
  // 原生 img/video/audio 直接渐进加载；文本走流的 fetchText 逐块渲染。不设大小限制。
  const openPreview = async (item) => {
    // 打开预览压一条历史（同 URL）：浏览器 back 会先触发 popstate → 关预览回列表，而不是直接离开页面
    window.history.pushState({ pdPreview: true }, '');
    const name = item.path || item.name || '';
    const kind = kindOf(name);
    if (!kind) { setPreview({ hash: item.hash, name, kind: null, error: '该类型不支持预览，可下载查看' }); return; }
    const imgList = (share?.files || []).filter(f => kindOf(f.path || f.name) === 'image');
    const imgIdx = kind === 'image' ? imgList.findIndex(f => f.hash === item.hash) : null;
    if (kind === 'text') {
      setPreview({ hash: item.hash, name, kind, loading: true, error: '', text: '', url: '', imgIndex: imgIdx });
      const dec = new TextDecoder('utf-8');
      let acc = '';
      try {
        for await (const chunk of session.client.stream(item.hash)) {
          acc += dec.decode(chunk, { stream: true });
          setPreview(p => (p && p.hash === item.hash ? { ...p, text: acc } : p));
          await new Promise(r => setTimeout(r, 0));
        }
        acc += dec.decode();
        setPreview(p => (p && p.hash === item.hash ? { ...p, text: acc, loading: false } : p));
      } catch (e) {
        setPreview(p => (p && p.hash === item.hash ? { ...p, loading: false, error: e?.message || String(e) } : p));
      }
      return;
    }
    // 图片：增量 blob 边下载边刷新显示（进度条 + 收一块刷一版；progressive/interlaced 会提前出图）
    if (kind === 'image') {
      setViewer({ scale: 1, x: 0, y: 0 });
      const total = item.size || 0;
      setPreview({ hash: item.hash, name, kind, loading: true, error: '', text: '', url: '', progress: { loaded: 0, total }, imgIndex: imgIdx });
      const chunks = [];
      let loaded = 0;
      try {
        for await (const chunk of session.client.stream(item.hash)) {
          chunks.push(chunk);
          loaded += chunk.byteLength;
          // 每约 1MB 重建 objectURL 刷新 img（边下边显；progressive/interlaced 格式会提前出图）
          if (loaded % (1024 * 1024) < chunk.byteLength || loaded >= total) {
            setPreview(p => {
              if (!p || p.hash !== item.hash) return p;
              if (p.url) URL.revokeObjectURL(p.url);
              return {
                ...p,
                url: URL.createObjectURL(new Blob(chunks, { type: mimeOf(name) })),
                progress: { loaded, total },
              };
            });
            await new Promise(r => setTimeout(r, 0));
          }
        }
        setPreview(p => (p && p.hash === item.hash ? { ...p, loading: false, progress: { loaded: loaded || total, total } } : p));
      } catch (e) {
        setPreview(p => (p && p.hash === item.hash ? { ...p, loading: false, error: e?.message || String(e) } : p));
      }
      return;
    }
    // 视频/音频：SW 伪造 fetch 断点续传（原生控件 + 进度条）
    const url = `${import.meta.env.BASE_URL}swdrive/${encodeURIComponent(item.hash)}?name=${encodeURIComponent(name)}${item.size ? '&size=' + item.size : ''}`;
    setPreview({ hash: item.hash, name, kind, loading: false, error: '', text: '', url, imgIndex: imgIdx });
  };

  const closePreview = () => {
    setPreview(p => {
      if (p?.url) URL.revokeObjectURL(p.url);
      return null;
    });
  };

  // 媒体加载失败兜底：SW 未接管/流异常时改用内存 blob（objectURL）渲染
  const fallbackBlob = async () => {
    const cur = preview;
    if (!cur || !cur.hash) return;
    try {
      const blob = await session.client.fetchBlob(cur.hash, { name: cur.name || '', mime: mimeOf(cur.name || '') });
      const u = URL.createObjectURL(blob);
      setPreview(p => (p && p.hash === cur.hash ? { ...p, url: u, error: '', _blob: true } : p));
    } catch (e) {
      setPreview(p => (p && p.hash === cur.hash ? { ...p, error: '预览失败：' + (e?.message || String(e)) } : p));
    }
  };
  const onMediaError = () => {
    if (preview?._blob) {
      setPreview(p => (p ? { ...p, error: '媒体加载失败（可返回列表或下载查看）' } : p));
      return;
    }
    fallbackBlob();
  };

  // 拦截 back：预览开着时按后退 → 关闭预览回到文件列表（不离开页面）
  useEffect(() => {
    const onPop = () => {
      if (preview) {
        setPreview(p => {
          if (p?.url) URL.revokeObjectURL(p.url);
          return null;
        });
        setViewer({ scale: 1, x: 0, y: 0 });
      }
      // 无预览时不拦截，让浏览器正常后退
    };
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, [preview]);

  const pct = (loaded, total) => (total ? Math.min(100, Math.round((loaded / total) * 100)) : 0);

  // ── 图片查看器：缩放 / 翻页 / 拖拽平移 ──
  const zoomBy = (factor) => setViewer(v => ({ ...v, scale: Math.min(5, Math.max(0.1, +(v.scale * factor).toFixed(2))) }));
  const resetZoom = () => setViewer({ scale: 1, x: 0, y: 0 });
  const goImg = (delta) => {
    const imgList = (share?.files || []).filter(f => kindOf(f.path || f.name) === 'image');
    if (imgList.length < 2 || preview?.imgIndex == null) return;
    const next = (preview.imgIndex + delta + imgList.length) % imgList.length;
    openPreview(imgList[next]);
  };
  const onDragStart = (e) => {
    if (preview?.kind !== 'image' || viewer.scale <= 1) return;
    dragRef.current = { sx: e.clientX, sy: e.clientY, ox: viewer.x, oy: viewer.y };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };
  const onDragMove = (e) => {
    if (!dragRef.current) return;
    setViewer(v => ({ ...v, x: dragRef.current.ox + (e.clientX - dragRef.current.sx), y: dragRef.current.oy + (e.clientY - dragRef.current.sy) }));
  };
  const onDragEnd = () => { dragRef.current = null; };

  const disconnect = () => {
    clearNodeSession();
    navigate('/');
  };

  if (!session?.client || !session.peerId) {
    return (
      <div className="p-8 h-full overflow-y-auto flex items-center justify-center">
        <div className="text-center">
          <p className="text-gray-300 mb-3">还没有连接任何节点</p>
          <Link to="/" className="btn-brand">去连接节点</Link>
        </div>
      </div>
    );
  }

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-4xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="text-2xl font-bold">节点控制</h1>
            <p className="text-sm text-gray-500 mt-0.5">已连接对端节点，浏览共享内容并保存</p>
          </div>
          <button onClick={disconnect} className="btn-ghost">断开</button>
        </div>

        {/* 节点信息 */}
        <div className="card-surface p-4 mb-4">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-xs">
            <div>
              <span className="block text-[10px] text-gray-500 mb-1">对端节点</span>
              <span className="text-gray-200 font-mono text-sm break-all">{session.peerId}</span>
            </div>
            <div>
              <span className="block text-[10px] text-gray-500 mb-1">本机身份</span>
              <span className="text-gray-400 font-mono text-sm break-all">{session.myId || session.client.id || '—'}</span>
            </div>
            <div>
              <span className="block text-[10px] text-gray-500 mb-1">状态</span>
              <span className="text-green-400">已连接 ✓</span>
            </div>
          </div>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {/* 共享清单 */}
        <h2 className="text-sm font-semibold text-gray-200 mb-2">对端共享{share ? `（${share.total ?? 0} 项）` : ''}</h2>

        {preview && (
          <div className="fixed inset-0 z-50 flex flex-col bg-black" onClick={closePreview}>
            <div className="relative flex-1 min-h-0 flex flex-col" onClick={e => e.stopPropagation()}>
              <div className="flex items-center justify-between px-4 py-3 border-b border-white/[0.06] shrink-0">
                <p className="text-sm text-gray-200 font-mono truncate pr-4">{preview.name || preview.hash}</p>
                <button onClick={closePreview} className="btn-ghost !px-2 !py-1 !text-xs shrink-0">关闭 ✕</button>
              </div>
              <div className="flex-1 min-h-0 overflow-auto p-3">
                {preview.loading && (
                  <div className="flex flex-col items-center gap-2 py-8">
                    <p className="text-xs text-gray-400">
                      加载中… {preview.progress?.loaded ? `${fmtBytes(preview.progress.loaded)}${preview.progress.total ? ' / ' + fmtBytes(preview.progress.total) : ''} (${pct(preview.progress.loaded, preview.progress.total)}%)` : ''}
                    </p>
                    <div className="w-64 h-1.5 bg-white/[0.08] rounded overflow-hidden">
                      <div className="h-full bg-brand-500 transition-all" style={{ width: pct(preview.progress?.loaded, preview.progress?.total) + '%' }} />
                    </div>
                  </div>
                )}
                {preview.error && (
                  <div className="text-center py-10">
                    <p className="text-xs text-red-400 mb-4">{preview.error}</p>
                    <button onClick={closePreview} className="btn-brand !px-4 !py-2 !text-sm">← 返回列表</button>
                  </div>
                )}
                {!preview.error && preview.kind === 'image' && preview.url && (
                  <div>
                    {/* 查看器工具条 */}
                    <div className="flex items-center justify-between gap-2 mb-2 text-xs">
                      <div className="flex items-center gap-1.5">
                        <button onClick={() => zoomBy(0.8)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">−</button>
                        <span className="w-14 text-center text-gray-400">{Math.round(viewer.scale * 100)}%</span>
                        <button onClick={() => zoomBy(1.25)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">+</button>
                        <button onClick={resetZoom} className="px-2 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">1:1</button>
                      </div>
                      {(share?.files || []).filter(f => kindOf(f.path || f.name) === 'image').length > 1 && (
                        <div className="flex items-center gap-1.5">
                          <button onClick={() => goImg(-1)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">←</button>
                          <span className="text-gray-400">{(preview.imgIndex ?? 0) + 1}/{(share?.files || []).filter(f => kindOf(f.path || f.name) === 'image').length}</span>
                          <button onClick={() => goImg(1)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">→</button>
                        </div>
                      )}
                    </div>
                    {/* 图片区：放大后可拖拽平移 */}
                    <div
                      className="relative bg-black/30 rounded overflow-hidden select-none"
                      style={{ height: 'calc(100vh - 150px)' }}
                      onPointerDown={onDragStart}
                      onPointerMove={onDragMove}
                      onPointerUp={onDragEnd}
                      onPointerLeave={onDragEnd}
                      onWheel={e => { e.preventDefault(); zoomBy(e.deltaY < 0 ? 1.1 : 0.9); }}
                    >
                      <img
                        src={preview.url}
                        alt={preview.name}
                        draggable={false}
                        onError={() => setPreview(p => (p ? { ...p, error: '图片加载失败：Service Worker 未接管 /swdrive 或连接已断开（返回列表重试）' } : p))}
                        style={{
                          transform: `translate(${viewer.x}px, ${viewer.y}px) scale(${viewer.scale})`,
                          cursor: viewer.scale > 1 ? 'grab' : 'default',
                        }}
                        className="absolute inset-0 m-auto max-w-full max-h-full object-contain"
                      />
                    </div>
                  </div>
                )}
                {!preview.loading && !preview.error && preview.kind === 'video' && preview.url && (
                  <video
                    src={preview.url}
                    controls autoPlay
                    onError={onMediaError}
                    className="w-full h-[calc(100vh-150px)] object-contain rounded bg-black"
                  />
                )}
                {!preview.loading && !preview.error && preview.kind === 'audio' && preview.url && (
                  <div className="flex items-center justify-center h-[calc(100vh-150px)]">
                    <audio
                      src={preview.url}
                      controls autoPlay
                      onError={() => setPreview(p => (p ? { ...p, error: '媒体加载失败：Service Worker 未接管 /swdrive 或连接已断开（返回列表重试）' } : p))}
                      className="w-full max-w-xl"
                    />
                  </div>
                )}
                {preview.kind === 'text' && preview.text != null && (
                  <pre className="text-xs text-gray-300 bg-black/40 rounded p-3 h-[calc(100vh-150px)] overflow-auto whitespace-pre-wrap break-all">{preview.text}</pre>
                )}
              </div>
            </div>
          </div>
        )}
        {share === null ? (
          <div className="text-center py-16 text-gray-500 text-sm">读取中...</div>
        ) : (share.total ?? 0) === 0 ? (
          <div className="text-center py-16 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p>该节点没有共享内容</p>
            <p className="text-xs text-gray-600 mt-1">对方未开启对外共享，或未声明共享目录/文件。</p>
          </div>
        ) : (
          <div className="space-y-4">
            {share.files?.length > 0 && (
              <div className="card-surface overflow-hidden">
                <div className="px-4 py-2 bg-white/[0.03] text-[10px] uppercase tracking-wider text-gray-500">单独文件</div>
                <ul className="divide-y divide-white/[0.04]">
                  {share.files.map((f, i) => (
                      <li key={i} className="flex items-center gap-3 px-4 py-2.5 text-sm hover:bg-white/[0.02]">
                          <button
                            onClick={() => openFile(f)}
                            title={kindOf(f.path || f.name) ? '点击预览' : '点击下载'}
                            className="flex-1 truncate text-left text-gray-200 hover:text-brand-300 cursor-pointer transition-colors">
                            {f.path || f.name}
                          </button>
                          <span className="text-gray-500 shrink-0 whitespace-nowrap">{fmtBytes(f.size)}</span>
                        </li>
                    ))}
                </ul>
              </div>
            )}
            {share.collections?.length > 0 && (
              <div className="card-surface overflow-hidden">
                <div className="px-4 py-2 bg-white/[0.03] text-[10px] uppercase tracking-wider text-gray-500">合集</div>
                <ul className="divide-y divide-white/[0.04]">
                  {share.collections.map((c, i) => {
                    const entries = Array.isArray(c.entries) ? c.entries : [];
                    const open = collOpen === i;
                    return (
                      <li key={i}>
                        <button
                          onClick={() => setCollOpen(open ? null : i)}
                          className="w-full flex items-center gap-3 px-4 py-2.5 text-sm hover:bg-white/[0.02] text-left">
                          <span className={open ? 'rotate-90 transition-transform' : 'transition-transform'}>▶</span>
                          <span className="flex-1 truncate text-gray-200">{c.name || String(c.hash).slice(0, 12) + '…'}</span>
                          <span className="text-gray-500 shrink-0">{entries.length} 条目</span>
                        </button>
                        {open && (
                          <ul className="bg-white/[0.02] px-4 pb-2">
                            {entries.map((e, ei) => (
                              <li key={ei} className="flex items-center gap-3 py-1.5 text-sm">
                                <button
                                  onClick={() => openFile({ ...e, name: e.path || 'download' })}
                                  title={kindOf(e.path) ? '点击预览' : '点击下载'}
                                  className="flex-1 truncate text-left text-gray-400 hover:text-brand-300 cursor-pointer transition-colors">
                                  {e.path}
                                </button>
                                <span className="text-gray-500 shrink-0 whitespace-nowrap">{fmtBytes(e.size)}</span>
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
          </div>
        )}
      </div>

        {/* 下载进度条（页面底部浮动） */}
        {downloading && (
          <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-50 card-surface px-5 py-3 flex items-center gap-3">
            <p className="text-xs text-gray-300 max-w-[240px] truncate">下载中：{downloading.name}</p>
            <div className="w-48 h-1.5 bg-white/[0.08] rounded overflow-hidden">
              <div className="h-full bg-brand-500 transition-all" style={{ width: pct(downloading.loaded, downloading.total) + '%' }} />
            </div>
            <span className="text-xs text-gray-400 w-12 text-right">{pct(downloading.loaded, downloading.total)}%</span>
          </div>
        )}
      </div>
  );
}