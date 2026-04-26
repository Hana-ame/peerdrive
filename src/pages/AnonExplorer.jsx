import React, { useContext, useState, useEffect, useMemo } from 'react';
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
  const navigate = useNavigate();

  useEffect(() => { if (paramHash) { setInputVal(paramHash); setSearchHash(paramHash); } }, [paramHash]);
  useEffect(() => { if (searchHash) fetchCollection(searchHash); }, [searchHash]);

  const fetchCollection = async (h) => {
    if (!h) return;
    setLoading(true); setError(''); setCollection(null);
    try { setCollection(await api.getAnonCollection(h)); }
    catch { setError('合集未找到'); }
    setLoading(false);
    setNavPath('');
  };

  const handleFork = () => navigate('/anon/create', { state: { forkFrom: collection, sourceHash: searchHash } });
  const handleCommit = () => navigate('/anon/create', { state: { editFrom: collection, savedHash: searchHash } });

  const entries = collection?.entries || [];
  const fname = collection?.friendly_name || '';

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
                  <h2 className="text-base font-bold truncate">📦 {fname || '合集'}</h2>
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
                if (!collection?.entries?.length) return;
                try {
                  const local = await api.listAnonCollections().catch(() => []);
                  const exists = Array.isArray(local) && local.some(c => c.hash === searchHash);
                  if (exists) { alert('此合集已在本机，无需保存'); return; }
                  const selected = confirm('保存全部文件到本机作为新合集？\n确定=保存全部 | 取消=不保存');
                  if (!selected) return;
                  const name = collection.friendly_name || collection.name_preview || '合集副本';
                  const res = await api.createAnonCollection(collection.entries, name + ' (副本)');
                  navigate(`/anon/collections/${res.hash}`);
                } catch(e) { alert('保存失败: ' + e.message); }
              }} className="bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded text-xs">💾 保存</button>
            </div>

            <div className="flex-1 overflow-y-auto">
              {currentItems.dirs.length === 0 && currentItems.files.length === 0 ? (
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
