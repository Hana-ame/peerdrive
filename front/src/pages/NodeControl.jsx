// 节点控制页：连接成功后跳转过来的对端节点控制屏幕。
// 从全局会话取已连接的对端：节点信息 / 共享（文件·合集）/ 保存 / 断开。
import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { getNodeSession, clearNodeSession } from '../lib/nodeSession';

function fmtBytes(n) {
  if (!n || n === 0) return '—';
  const u = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(v < 10 ? 1 : 0) + ' ' + u[i];
}

// ── 预览支持：按文件名后缀分类（图片 / 视频 / 文本）──
const IMG_EXT = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'avif'];
const VID_EXT = ['mp4', 'webm', 'mov', 'm4v', 'ogv'];
const TXT_EXT = ['txt', 'md', 'markdown', 'json', 'js', 'ts', 'jsx', 'tsx', 'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'hpp', 'xml', 'html', 'htm', 'css', 'scss', 'yaml', 'yml', 'csv', 'log', 'sql', 'sh', 'toml', 'ini', 'conf'];

function extOf(name) {
  const m = String(name || '').toLowerCase().match(/\.([a-z0-9]+)$/);
  return m ? m[1] : '';
}

function kindOf(name) {
  const ext = extOf(name);
  if (IMG_EXT.includes(ext)) return 'image';
  if (VID_EXT.includes(ext)) return 'video';
  if (TXT_EXT.includes(ext)) return 'text';
  return null;
}

function mimeOf(name) {
  const ext = extOf(name);
  const map = {
    jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
    webp: 'image/webp', bmp: 'image/bmp', svg: 'image/svg+xml', avif: 'image/avif',
    mp4: 'video/mp4', webm: 'video/webm', mov: 'video/quicktime', m4v: 'video/x-m4v', ogv: 'video/ogg',
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
  const [preview, setPreview] = useState(null); // {hash,name,kind,url?,text?,loading,error}

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
    try {
      await session.client.saveAs(item.hash, item.path || item.name || 'download');
    } catch (e) { setErr(e?.message || String(e)); }
  };

  // 预览：按类型拉取内容
  const PREVIEW_LIMIT = 20 * 1024 * 1024; // 图片/视频预览内存上限 20MB
  const TEXT_LIMIT = 1024 * 1024;         // 文本预览上限 1MB
  const openPreview = async (item) => {
    const name = item.path || item.name || '';
    const kind = kindOf(name);
    if (!kind) { setPreview({ hash: item.hash, name, kind: null, error: '该类型不支持预览，可下载查看' }); return; }
    if (item.size && item.size > (kind === 'text' ? TEXT_LIMIT : PREVIEW_LIMIT)) {
      setPreview({ hash: item.hash, name, kind, error: `文件过大（${fmtBytes(item.size)}），请下载后查看` });
      return;
    }
    setPreview({ hash: item.hash, name, kind, loading: true, error: '' });
    try {
      if (kind === 'text') {
        const text = await session.client.fetchText(item.hash);
        setPreview(p => ({ ...p, loading: false, text }));
      } else {
        const blob = await session.client.fetchBlob(item.hash, { name, mime: mimeOf(name) });
        const url = URL.createObjectURL(blob);
        setPreview(p => ({ ...p, loading: false, url }));
      }
    } catch (e) {
      setPreview(p => ({ ...p, loading: false, error: e?.message || String(e) }));
    }
  };

  const closePreview = () => {
    setPreview(p => {
      if (p?.url) URL.revokeObjectURL(p.url);
      return null;
    });
  };

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
          <div className="card-surface p-4 mb-4">
            <div className="flex items-center justify-between mb-3">
              <p className="text-sm text-gray-200 font-mono truncate max-w-[80%]">{preview.name || preview.hash}</p>
              <button onClick={closePreview} className="btn-ghost !px-2 !py-1 !text-xs">关闭</button>
            </div>
            {preview.loading && <p className="text-xs text-gray-500 py-8 text-center">加载中...</p>}
            {preview.error && <p className="text-xs text-red-400 py-4">{preview.error}</p>}
            {!preview.loading && !preview.error && preview.kind === 'image' && preview.url && (
              <div className="flex justify-center">
                <img src={preview.url} alt={preview.name} className="max-w-full max-h-[60vh] rounded object-contain" />
              </div>
            )}
            {!preview.loading && !preview.error && preview.kind === 'video' && preview.url && (
              <video src={preview.url} controls className="w-full max-h-[60vh] rounded bg-black" />
            )}
            {!preview.loading && !preview.error && preview.kind === 'text' && preview.text != null && (
              <pre className="text-xs text-gray-300 bg-black/40 rounded p-3 max-h-[55vh] overflow-auto whitespace-pre-wrap break-all">{preview.text}</pre>
            )}
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
                      <span className="flex-1 truncate text-gray-200">{f.path || f.name}</span>
                      <span className="text-gray-500 shrink-0">{fmtBytes(f.size)}</span>
                      <span className="font-mono text-[10px] text-gray-600 shrink-0">{String(f.hash).slice(0, 12)}…</span>
                      {kindOf(f.path || f.name) && (
                        <button onClick={() => openPreview(f)}
                          className="px-2.5 py-1 text-[11px] bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 rounded shrink-0 mr-1">预览</button>
                      )}
                      <button onClick={() => save(f)} className="btn-brand !px-3 !py-1 !text-xs shrink-0">保存</button>
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
                                <span className="flex-1 truncate text-gray-400">{e.path}</span>
                                <span className="text-gray-500 shrink-0">{fmtBytes(e.size)}</span>
                                <button onClick={() => save({ ...e, name: e.path || 'download' })}
                                  className="px-2 py-0.5 text-[11px] bg-white/[0.06] hover:bg-white/[0.1] text-gray-300 rounded shrink-0 mr-1">保存</button>
                                {kindOf(e.path) && (
                                  <button onClick={() => openPreview({ ...e, name: e.path || 'download' })}
                                    className="px-2 py-0.5 text-[11px] bg-white/[0.06] hover:bg-white/[0.1] text-gray-300 rounded shrink-0">预览</button>
                                )}
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
    </div>
  );
}