import React, { useContext, useState, useEffect, useMemo, useRef } from 'react';
import * as api from '../api';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { PageContext } from '../App';

const SHA256_RE = /\b([a-f0-9]{64})\b/i;
function extractHash(text) { const m = (text || '').match(SHA256_RE); return m ? m[1].toLowerCase() : null; }

function fileIcon(m) {
  if (!m) return '📄';
  if (m.startsWith('image/')) return '🖼️';
  if (m.startsWith('video/')) return '🎬';
  if (m.startsWith('audio/')) return '🎵';
  if (m.startsWith('text/')) return '📝';
  if (m.includes('pdf')) return '📕';
  if (m.includes('zip') || m.includes('tar') || m.includes('gzip') || m.includes('rar')) return '📦';
  return '📄';
}
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
  const navigate = useNavigate();

  useEffect(() => { if (paramHash) { setInputVal(paramHash); setSearchHash(paramHash); } }, [paramHash]);
  useEffect(() => { if (searchHash) fetchCollection(searchHash); }, [searchHash]);
  useEffect(() => { api.listAnonCollections().then(l => { if (Array.isArray(l)) setAllCollHashes(new Set(l.map(c => c.hash))); }).catch(() => {}); }, [searchHash]);

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

  const handleFork = () => navigate('/anon/create', { state: { forkFrom: collection, sourceHash: searchHash } });
  const handleCommit = () => navigate('/anon/create', { state: { editFrom: collection, savedHash: searchHash } });

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
    if (h.length !== 64) return alert('请输入有效的 SHA256 Hash');
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
            <div className="flex items-center gap-2 px-4 py-3 border-b border-gray-800 shrink-0">
              {navPath ? (
                <button onClick={navBack} className="text-gray-400 hover:text-white text-sm">← 返回</button>
              ) : (
                <div className="flex items-center gap-2">
                  {entries.length === 1 ? (
                    <h2 className="text-base font-bold truncate">📄 {isSingleFile ? entries[0].path.split('/').pop() : (fname || '合集')}</h2>
                  ) : (
                    <h2 className="text-base font-bold truncate">📦 {fname || '合集'}</h2>
                  )}
                  {entries.length === 1 && <span className="text-[10px] bg-amber-900/40 text-amber-400 px-1.5 py-0.5 rounded-full">单文件</span>}
                  {collection?.tags?.length > 0 && (
                    <div className="flex gap-1">
                      {collection.tags.map((t, i) => <span key={i} className="text-[10px] bg-blue-900/50 text-blue-300 px-2 py-0.5 rounded-full">{t}</span>)}
                    </div>
                  )}
                </div>
              )}
              <div className="flex items-center gap-1 text-xs text-gray-500">
                {navPath && <span className="text-gray-300">{fname || '合集'}</span>}
                {navPath.split('/').map((p, i) => (
                  <span key={i} className="flex items-center gap-1">
                    <span>›</span>
                    <button onClick={() => { const parts = navPath.split('/'); setNavPath(parts.slice(0, i + 1).join('/')); }}
                      className="text-blue-400 hover:underline">{p}</button>
                  </span>
                ))}
              </div>
              <div className="flex-1" />
              <span className="text-xs text-gray-600">{currentItems.totalFiles} 项</span>
              <button onClick={async () => {
                try {
                  const share = await api.createShare(searchHash, 'collection', fname || '合集');
                  const url = api.getShareUrl(share.token);
                  await navigator.clipboard.writeText(url);
                  alert(`分享链接已复制: ${url}`);
                } catch(e) { alert('分享失败: ' + e.message); }
              }} className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs">🔗 分享</button>
              {isLocal ? (
                <span className="text-xs text-green-500/70 bg-green-500/10 px-3 py-1 rounded-full">✓ 已保存到本机</span>
              ) : (
                <button onClick={async () => {
                  if (!collection?.entries?.length) return;
                  const selected = confirm('将合集副本保存到本机？');
                  if (!selected) return;
                  try {
                    const name = collection.friendly_name || collection.name_preview || '合集副本';
                    const res = await api.createAnonCollection(collection.entries, name + ' (副本)');
                    navigate(`/anon/collections/${res.hash}`);
                  } catch(e) { alert('保存失败: ' + e.message); }
                }} className="bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded text-xs">💾 保存到本机</button>
              )}
            </div>

            <div className="flex-1 overflow-y-auto">
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
                        <span className="text-xl">{fileIcon(f.mime_type)}</span>
                        <span className="text-blue-300 font-mono truncate flex-1">{f.path.split('/').pop()}</span>
                        <span className="text-gray-600 text-xs">{relTime(collection.created_at)}</span>
                      </a>
                    );
                  })}
                </div>
              )}
            </div>
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
