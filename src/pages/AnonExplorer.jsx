// 匿名合集查看器：通过 SHA256 Hash 浏览不可变合集内容，支持预览/下载/Fork
import React, { useContext, useState, useEffect, useMemo, useRef } from 'react';
import * as api from '../api';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { PageContext } from '../App';
import CommentSection from '../components/CommentSection';

const SHA256_RE = /\b([a-f0-9]{64})\b/i;
function extractHash(text) { const m = (text || '').match(SHA256_RE); return m ? m[1].toLowerCase() : null; }

// 根据 MIME 和扩展名返回文件图标
function fileIcon(m, p) {
  if (!m && !p) return '📄';
  if (m) {
    if (m.startsWith('image/')) return '🖼️';
    if (m.startsWith('video/')) return '🎬';
    if (m.startsWith('audio/')) return '🎵';
    if (m.startsWith('text/')) return '📝';
    if (m.includes('pdf')) return '📕';
    if (m.includes('zip') || m.includes('tar') || m.includes('gzip') || m.includes('rar')) return '📦';
  }
  if (p) {
    const ext = p.split('.').pop()?.toLowerCase();
    if (['png','jpg','jpeg','gif','webp','svg','bmp','ico'].includes(ext)) return '🖼️';
    if (['mp4','avi','mkv','mov','webm'].includes(ext)) return '🎬';
    if (['mp3','wav','flac','ogg'].includes(ext)) return '🎵';
    if (['pdf'].includes(ext)) return '📕';
    if (['zip','rar','7z','tar','gz','gzip'].includes(ext)) return '📦';
    if (['txt','md','json','js','ts','jsx','tsx','css','html','xml','yaml','yml','py','go','rs','java','c','cpp','h','log','cfg','ini','conf','sh','bash'].includes(ext)) return '📝';
  }
  return '📄';
}
// 格式化文件大小
function fmtSize(b) {
  if (!b) return '';
  if (b < 1024) return b + ' B';
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
  return (b / 1073741824).toFixed(1) + ' GB';
}

export default function AnonExplorer() {
  const { hash: paramHash } = useParams();
  const { setPageContext } = useContext(PageContext);
  const [inputVal, setInputVal] = useState(paramHash || '');
  const [searchHash, setSearchHash] = useState(paramHash || '');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [navPath, setNavPath] = useState('');
  const [isLocal, setIsLocal] = useState(false);
  const [allCollHashes, setAllCollHashes] = useState(new Set());
  const [toastMsg, setToastMsg] = useState('');
  const [toastErr, setToastErr] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { if (paramHash) { setInputVal(paramHash); setSearchHash(paramHash); } }, [paramHash]);
  useEffect(() => { if (searchHash) fetchCollection(searchHash); }, [searchHash]);
  useEffect(() => { api.listAnonCollections().then(l => { if (Array.isArray(l)) setAllCollHashes(new Set(l.map(c => c.hash))); }).catch(() => {}); }, [searchHash]);

  // 根据 Hash 获取合集数据，空合集自动删除
  const fetchCollection = async (h) => {
    if (!h) return;
    setLoading(true); setError(''); setCollection(null);
    try {
      const coll = await api.getAnonCollection(h);
      if (!coll.entries || coll.entries.length === 0) {
        await api.deleteFile(h).catch(() => {});
        setError('空合集，已自动删除');
        setLoading(false);
        return;
      }
      setCollection(coll);
    } catch { setError('合集未找到'); }
    setLoading(false); setNavPath('');
    api.listAnonCollections().then(l => setIsLocal(Array.isArray(l) && l.some(c => c.hash === h))).catch(() => {});
  };

  // 保存合集副本到本机后进行编辑
  const handleSaveAndEdit = async () => {
    if (!collection?.entries?.length) return;
    try {
      const name = (collection.friendly_name || '合集') + ' (副本)';
      const res = await api.createAnonCollection(collection.entries, name);
      setToastMsg('已保存到本机，可前往创建页编辑');
      setToastErr(false);
      setTimeout(() => setToastMsg(''), 3000);
      setIsLocal(true);
      navigate(`/anon/collections/${res.hash}`);
    } catch(e) { setToastMsg('保存失败: ' + e.message); setToastErr(true); }
  };

  const entries = collection?.entries || [];
  const fname = collection?.friendly_name || '';
  const isSingleFile = entries.length === 1 && !entries[0].path.includes('/');

  const navIn = (dir) => setNavPath(prev => prev ? `${prev}/${dir}` : dir);
  const navBack = () => {
    const p = navPath.split('/');
    p.pop();
    setNavPath(p.join('/'));
  };

  const currentItems = useMemo(() => {
    const dirs = new Set();
    const files = [];
    const prefix = navPath ? navPath + '/' : '';
    for (const e of entries) {
      if (e.path.startsWith(prefix)) {
        const rest = e.path.slice(prefix.length);
        const slash = rest.indexOf('/');
        if (slash === -1) files.push(e);
        else dirs.add(rest.slice(0, slash));
      }
    }
    const totalFiles = entries.filter(e => e.path.startsWith(prefix)).length;
    return { dirs: Array.from(dirs).sort(), files, totalFiles };
  }, [entries, navPath]);

  const handleInputChange = (val) => {
    setInputVal(val);
    const h = extractHash(val);
    if (h) { setSearchHash(h); setNavPath(''); navigate(`/anon/collections/${h}`); }
  };
  const handleSearch = () => {
    const h = extractHash(inputVal) || inputVal.trim().toLowerCase();
    if (h.length !== 64) { setToastMsg('请输入有效的 SHA256 Hash'); setToastErr(true); return; }
    setSearchHash(h); setNavPath(''); navigate(`/anon/collections/${h}`);
  };

  const relTime = (ts) => {
    if (!ts) return '';
    const d = new Date(ts), now = new Date();
    const diff = Math.floor((now - d) / 1000);
    if (diff < 60) return '刚刚';
    if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
    if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
    if (diff < 604800) return `${Math.floor(diff / 86400)} 天前`;
    return d.toLocaleDateString();
  };

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      <div className="flex-1 flex flex-col max-w-3xl mx-auto w-full">
        {/* 顶部搜索栏：输入 Hash 或粘贴链接 */}
        <div className="h-14 flex items-center px-4 shrink-0 gap-3 border-b border-gray-800">
          <button onClick={() => { setCollection(null); navigate(-1); }} className="text-gray-400 hover:text-white text-lg">←</button>
          <input type="text" value={inputVal}
            onChange={e => handleInputChange(e.target.value)}
            onPaste={e => { const h = extractHash(e.clipboardData.getData('text')); if (h) { e.preventDefault(); setInputVal(h); setSearchHash(h); setNavPath(''); navigate(`/anon/collections/${h}`); } }}
            onKeyDown={e => { if (e.key === 'Enter') handleSearch(); }}
            placeholder="输入 SHA256 Hash 粘贴合集链接"
            className="flex-1 bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500" />
          <button onClick={handleSearch} disabled={loading}
            className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50">{loading ? '...' : '查看'}</button>
        </div>

        {error && <div className="px-4 py-3"><p className="text-red-400 text-sm">{error}</p></div>}
        {loading && !collection && <div className="flex-1 flex items-center justify-center text-gray-600">加载中...</div>}

        {collection && (
          <div className="flex-1 flex flex-col overflow-hidden">
            <div className="flex items-center gap-2 px-4 py-2.5 border-b border-gray-800 shrink-0 flex-wrap">
              {navPath ? (
                <button onClick={navBack} className="text-gray-400 hover:text-white text-sm shrink-0">← 返回</button>
              ) : (
                <div className="flex items-center gap-2 min-w-0">
                  {entries.length === 1 ? (
                    <h2 className="text-base font-bold truncate">📄 {isSingleFile ? entries[0].path.split('/').pop() : (fname || '合集')}</h2>
                  ) : (
                    <h2 className="text-base font-bold truncate">📦 {fname || '合集'}</h2>
                  )}
                  {entries.length === 1 && <span className="text-[10px] bg-amber-900/40 text-amber-400 px-1.5 py-0.5 rounded-full shrink-0">单文件</span>}
                  {collection?.tags?.length > 0 && (
                    <div className="flex gap-1">
                      {collection.tags.map((t, i) => <span key={i} className="text-[10px] bg-blue-900/50 text-blue-300 px-2 py-0.5 rounded-full">{t}</span>)}
                    </div>
                  )}
                </div>
              )}
              <div className="flex-1 min-w-0" />
              <span className="text-xs text-gray-600 shrink-0">{currentItems.totalFiles} 项</span>
              <div className="flex items-center gap-1 shrink-0">
                <button onClick={async () => {
                  try {
                    const share = await api.createShare(searchHash, 'collection', fname || '合集');
                    const url = api.getShareUrl(share.token);
                    await navigator.clipboard.writeText(url);
                    setToastMsg('分享链接已复制: ' + url);
                    setToastErr(false);
                    setTimeout(() => setToastMsg(''), 4000);
                  } catch(e) { setToastMsg('分享失败: ' + e.message); setToastErr(true); }
                }} className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs">🔗 分享</button>
                <button onClick={async () => {
                  if (searchHash) {
                    try {
                      await api.p2pAnnounce(searchHash);
                      setToastMsg('广播成功');
                      setToastErr(false);
                      setTimeout(() => setToastMsg(''), 2500);
                    } catch(e) { setToastMsg('广播失败: ' + e.message); setToastErr(true); }
                  }
                }} className="bg-emerald-700 hover:bg-emerald-600 text-white px-3 py-1 rounded text-xs">📡 广播</button>
                <button onClick={handleSaveAndEdit}
                  className={`px-3 py-1 rounded text-xs ${isLocal ? 'bg-green-500/20 text-green-400' : 'bg-blue-600 hover:bg-blue-700 text-white'}`}>
                  {isLocal ? '✓ 已保存' : '💾 保存到本机'}
                </button>
              </div>
            </div>

            {toastMsg && (
              <div className={`px-4 py-1.5 text-xs shrink-0 ${toastErr ? 'text-red-400 bg-red-500/10' : 'text-green-400 bg-green-500/10'}`}>{toastMsg}</div>
            )}

            {navPath && (
              <div className="flex items-center gap-1 px-4 py-2 bg-gray-800/80 border-b border-gray-700/50 text-xs shrink-0">
                <button onClick={() => setNavPath('')} className="text-blue-400 hover:text-blue-300 font-medium flex items-center gap-1">
                  📦 {fname || '合集'}
                </button>
                {navPath.split('/').map((p, i) => (
                    <span key={i} className="flex items-center gap-1">
                      <span className="text-gray-600">›</span>
                      <button onClick={() => { const parts = navPath.split('/'); setNavPath(parts.slice(0, i + 1).join('/')); }}
                        className="text-blue-400 hover:text-blue-300 hover:underline font-medium">{p}</button>
                    </span>
                  ))}
              </div>
            )}

            <div className="flex-1 overflow-y-auto">
              {/* Comment section at top when not viewing single file */}
              {!isSingleFile && !loading && collection && !navPath && (
                <div className="px-4 pt-2">
                  <CommentSection hash={searchHash} />
                </div>
              )}
              {isSingleFile && !navPath ? (
                allCollHashes.has(entries[0].hash) ? (
                  <div className="flex flex-col items-center justify-center py-16 px-8 cursor-pointer"
                    onClick={() => navigate(`/anon/collections/${entries[0].hash}`)}>
                    <span className="text-5xl mb-4">📦</span>
                    <h3 className="text-xl font-bold text-purple-300 mb-1">{entries[0].path.split('/').pop()}</h3>
                    <p className="text-xs text-purple-500 font-mono mb-6">嵌套合集 — 点击打开</p>
                  </div>
                ) : (() => {
                  const mime = entries[0].mime_type || '';
                  const ext = (entries[0].path || '').split('.').pop()?.toLowerCase();
                  const url = api.getAnonFileDownloadUrl(searchHash, entries[0].path) + '?inline=1';
                  const dlUrl = api.getAnonFileDownloadUrl(searchHash, entries[0].path);
                  const filename = entries[0].path.split('/').pop() || 'file';
                  const isImage = mime.startsWith('image/') || ['png','jpg','jpeg','gif','webp','svg','bmp','ico'].includes(ext);
                  const isText = mime.startsWith('text/') || ['json','js','jsx','ts','tsx','css','html','xml','md','yaml','yml','toml','ini','cfg','conf','sh','bash','py','go','rs','java','c','cpp','h','log','txt'].includes(ext);
                  const isPdf = mime === 'application/pdf' || ext === 'pdf';

                  if (isImage) return (
                    <div className="flex flex-col items-center py-8 px-4">
                      <h3 className="text-sm font-bold text-gray-200 mb-2">{filename}</h3>
                      <img src={url} alt={filename} className="max-w-full max-h-[70vh] object-contain rounded-lg border border-gray-700" />
                      <a href={dlUrl} target="_blank" rel="noreferrer" className="text-xs text-blue-400 hover:underline mt-3">⬇ 下载原图</a>
                    </div>
                  );

                  if (isText) return <TextPreview url={url} downloadUrl={dlUrl} filename={filename} hash={entries[0].hash} created={collection.created_at} />;

                  if (isPdf) return (
                    <div className="flex flex-col py-4 px-2 h-full">
                      <div className="flex items-center justify-between mb-2 px-2">
                        <h3 className="text-sm font-bold text-gray-200 truncate">{filename}</h3>
                        <a href={dlUrl} target="_blank" rel="noreferrer" className="text-xs text-blue-400 hover:underline ml-3 shrink-0">⬇ 下载 PDF</a>
                      </div>
                      <iframe src={url + '#view=FitH'} className="flex-1 w-full rounded-lg border border-gray-700" title="PDF Preview" />
                    </div>
                  );

                  return (
                    <div className="flex flex-col items-center justify-center py-16 px-8">
                      <span className="text-5xl mb-4">{fileIcon(mime)}</span>
                      <h3 className="text-xl font-bold text-gray-200 mb-1">{entries[0].path.split('/').pop()}</h3>
                      <p className="text-xs text-gray-500 font-mono mb-2">{fmtSize(collection.entries?.[0]?.size || 0)}</p>
                      <a href={dlUrl} target="_blank" rel="noreferrer"
                        className="bg-blue-600 hover:bg-blue-700 text-white px-6 py-2.5 rounded-lg text-sm font-medium mb-3">
                        ⬇ 下载文件
                      </a>
                      <p className="text-xs text-gray-600">{relTime(collection.created_at)}</p>
                    </div>
                  );
                })()
              ) : currentItems.dirs.length === 0 && currentItems.files.length === 0 ? (
                <div className="text-center text-gray-600 py-10 text-sm">此目录为空</div>
              ) : (
                <div>
                  {currentItems.dirs.map(dir => (
                    <div key={dir} onClick={() => navIn(dir)}
                      className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                      <span className="text-xl">📁</span>
                      <span className="text-gray-200 font-mono truncate flex-1">{dir}</span>
                      <span className="text-gray-600 text-xs">文件夹</span>
                    </div>
                  ))}
                  {currentItems.files.map(f => {
                    const isNestedColl = allCollHashes.has(f.hash);
                    if (isNestedColl) {
                      return (
                        <div key={f.path} onClick={() => navigate(`/anon/collections/${f.hash}`)}
                          className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                          <span className="text-xl">📦</span>
                          <span className="text-purple-300 font-mono truncate flex-1">{f.path.split('/').pop()}</span>
                          <span className="text-purple-500 text-xs">合集 →</span>
                        </div>
                      );
                    }
                    const url = api.getAnonFileDownloadUrl(searchHash, f.path);
                    return (
                      <a key={f.path} href={url} target="_blank" rel="noreferrer"
                        className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm block">
                        <span className="text-xl">{fileIcon(f.mime_type, f.path)}</span>
                        <span className="text-blue-300 font-mono truncate flex-1">{f.path.split('/').pop()}</span>
                        <span className="text-gray-600 text-xs">{relTime(collection.created_at)}</span>
                      </a>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Comment section at bottom of all collection views */}
            {collection && (
              <div className="px-4 pb-4 border-t border-gray-800/50">
                <CommentSection hash={searchHash} />
              </div>
            )}
          </div>
        )}

        {!loading && !collection && !error && !paramHash && (
          <div className="flex-1 flex flex-col items-center justify-center text-gray-600 text-sm gap-4">
            <p>输入合集 Hash 查看内容</p>
            <div className="flex gap-3"><Link to="/anon/create" className="text-blue-400 hover:underline">创建合集</Link><Link to="/" className="text-blue-400 hover:underline">浏览合集</Link></div>
          </div>
        )}
      </div>
    </div>
  );
}

// 文本文件预览组件：加载并显示前 50KB 内容
function TextPreview({ url, downloadUrl, filename, hash, created }) {
  const [content, setContent] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    fetch(url).then(r => r.text()).then(t => { setContent(t.substring(0, 50000)); setLoading(false); }).catch(() => { setError('加载失败'); setLoading(false); });
  }, [url]);
  if (loading) return <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">加载预览中...</div>;
  if (error) return <div className="flex-1 flex items-center justify-center text-red-400 text-sm">{error}</div>;
  return (
    <div className="flex flex-col h-full overflow-hidden px-4 py-3">
      <div className="flex items-center gap-2 mb-2 text-xs text-gray-500">
        <span className="text-gray-300 font-mono truncate">{filename}</span>
        <span>{(hash || '').substring(0, 12)}</span>
        {content && content.length >= 50000 && <span className="text-amber-500">（截断至前 50KB）</span>}
      </div>
      <pre className="flex-1 overflow-auto bg-gray-900 border border-gray-800 rounded-lg p-4 text-xs font-mono text-gray-300 whitespace-pre-wrap break-all">{content}</pre>
      <a href={downloadUrl || url} target="_blank" rel="noreferrer" className="text-xs text-blue-400 hover:underline mt-2 self-end">⬇ 下载</a>
    </div>
  );
}
