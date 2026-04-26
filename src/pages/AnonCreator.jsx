import React, { useContext, useState, useEffect, useRef } from 'react';
import * as api from '../api';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../App';
import FileTree from '../components/FileTree';

const SORT_OPTS = [
  { v: 'time', l: '时间' }, { v: 'name', l: '名称' },
  { v: 'path', l: '目录' }, { v: 'type', l: '类型' }, { v: 'size', l: '大小' },
];
const TYPE_OPTS = [
  { v: '', l: '全部' }, { v: 'image/', l: '图片' }, { v: 'video/', l: '视频' },
  { v: 'audio/', l: '音频' }, { v: 'text/', l: '文本' },
];

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
  if (!b) return '-';
  if (b < 1024) return b + ' B';
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
  return (b / 1073741824).toFixed(1) + ' GB';
}

export default function AnonCreator() {
  const nav = useNavigate();
  const { setPageContext } = useContext(PageContext);
  const navState = useLocation().state || {};

  const [split, setSplit] = useState(50);
  const [dragging, setDragging] = useState(false);
  const [files, setFiles] = useState([]);
  const [fLoading, setFLoading] = useState(true);
  const [sort, setSort] = useState('time');
  const [search, setSearch] = useState('');
  const [typeF, setTypeF] = useState('');
  const [srcTab, setSrcTab] = useState('local');
  const [collections, setCollections] = useState([]);
  const [collSource, setCollSource] = useState(null);
  const [fname, setFname] = useState('');
  const [entries, setEntries] = useState([]);
  const [openHash, setOpenHash] = useState('');
  const [savedHash, setSavedHash] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => { loadFiles(); loadCollections(); }, [sort]);
  useEffect(() => {
    if (navState.forkFrom) {
      const c = navState.forkFrom;
      setEntries(c.entries || []);
      setFname((c.friendly_name || '') + ' (fork)');
      setOpenHash(navState.sourceHash || '');
      nav('/anon/create', { replace: true });
    } else if (navState.draftFrom) {
      setEntries(navState.draftFrom.entries || []);
      setFname(navState.draftFrom.friendlyName || '');
      nav('/anon/create', { replace: true });
    } else if (navState.editFrom) {
      const c = navState.editFrom;
      setEntries(c.entries || []);
      setFname(c.friendly_name || '');
      setOpenHash(navState.savedHash || '');
      setSavedHash(navState.savedHash || '');
      nav('/anon/create', { replace: true });
    }
  }, []);
  useEffect(() => {
    setPageContext({ type: 'anonCreator', fileCount: files.length, entryCount: entries.length, friendlyName: fname, openHash: openHash ? openHash.substring(0, 16) : '' });
  }, [files, entries, fname, openHash]);

  const loadFiles = async () => {
    setFLoading(true);
    try { setFiles(await api.listFiles(sort) || []); } catch { setFiles([]); }
    setFLoading(false);
  };
  const loadCollections = async () => {
    try { setCollections(await api.listAnonCollections() || []); } catch { setCollections([]); }
  };

  const addEntry = (hash, path, mime_type, size) => {
    setEntries(prev => [...prev, { hash, path, mime_type, size }]);
  };
  const removeEntry = (entry) => {
    setEntries(prev => prev.filter(e => !(e.path === entry.path && e.hash === entry.hash)));
  };
  const renameEntry = (oldPath, newPath) => {
    setEntries(prev => prev.map(e => e.path === oldPath ? { ...e, path: newPath } : e));
  };

  const handleMint = async () => {
    const valid = entries.filter(e => e.path?.trim() && e.hash);
    if (!valid.length) return alert('请先添加文件');
    setSaving(true);
    const oldHash = savedHash;
    try {
      const res = await api.createAnonCollection(valid, fname.trim());
      setSavedHash(res.hash); setOpenHash(res.hash);
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
      if (oldHash) api.deleteFile(oldHash).catch(() => {});
    } catch (err) { alert(`创建失败: ${err.message}`); }
    setSaving(false);
  };
  const handleCommit = async () => {
    if (!savedHash) return handleMint();
    const valid = entries.filter(e => e.path?.trim() && e.hash);
    const oldHash = savedHash;
    try {
      const res = await api.commitAnonCollection(oldHash, valid.map(e => ({ path: e.path, hash: e.hash })));
      setSavedHash(res.hash); setOpenHash(res.hash);
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
      api.deleteFile(oldHash).catch(() => {});
    } catch (err) { alert(`提交失败: ${err.message}`); }
  };
  const loadCollAsSource = async (hash) => {
    try { setCollSource(await api.getAnonCollection(hash)); } catch { alert('无法加载合集'); }
  };

  const filtered = files.filter(f => {
    if (search && !(f.filename || '').toLowerCase().includes(search.toLowerCase())) return false;
    if (typeF && !(f.mime_type || '').startsWith(typeF)) return false;
    return true;
  });
  const collFiltered = collSource?.entries?.filter(e =>
    !search || (e.path || '').toLowerCase().includes(search.toLowerCase())
  ) || [];

  const onSplitMouseDown = (e) => {
    e.preventDefault(); setDragging(true);
    const sx = e.clientX, ss = split;
    const onMove = (ev) => setSplit(Math.max(20, Math.min(80, ss + (ev.clientX - sx) / window.innerWidth * 100)));
    const onUp = () => { setDragging(false); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };
  const inDraft = (path, hash) => entries.some(e => e.path === path && e.hash === hash);

  return (
    <div className={`flex flex-1 overflow-hidden h-full bg-gray-950 ${dragging ? 'select-none' : ''}`}>
      <div style={{ width: `${split}%` }} className="h-full flex flex-col border-r border-gray-700">
        <div className="h-12 flex items-center px-3 border-b border-gray-800 shrink-0 gap-2">
          <div className="flex bg-gray-800 rounded">
            <button onClick={() => setSrcTab('local')} className={`px-4 py-1.5 text-sm rounded ${srcTab === 'local' ? 'bg-gray-600 text-white' : 'text-gray-400'}`}>本地文件</button>
            <button onClick={() => setSrcTab('collection')} className={`px-4 py-1.5 text-sm rounded ${srcTab === 'collection' ? 'bg-gray-600 text-white' : 'text-gray-400'}`}>已有合集</button>
          </div>
          <div className="flex-1" />
          <span className="text-sm text-gray-600">{srcTab === 'local' ? filtered.length : collFiltered.length} 项</span>
        </div>

        {srcTab === 'collection' && (
          <div className="p-2 border-b border-gray-800 shrink-0 space-y-1 max-h-[200px] overflow-y-auto">
            {collections.length === 0 ? <p className="text-sm text-gray-600 p-2">暂无历史合集</p> :
              collections.map(c => (
                <div key={c.hash} onClick={() => loadCollAsSource(c.hash)}
                  className={`text-sm px-3 py-2 rounded cursor-pointer hover:bg-gray-800 flex items-center justify-between ${collSource && c.hash === openHash ? 'bg-blue-900/30' : ''}`}>
                  <span className="text-blue-300 truncate">{c.friendly_name || c.hash.substring(0, 12) + '...'}</span>
                  {c.friendly_name && <span className="text-gray-600 ml-2">v{c.version}</span>}
                </div>
              ))}
          </div>
        )}

        <div className="p-2 border-b border-gray-800 shrink-0 space-y-1.5">
          <div className="flex gap-1">
            {SORT_OPTS.map(o => (
              <button key={o.v} onClick={() => setSort(o.v)}
                className={`flex-1 text-sm px-2 py-1.5 rounded ${sort === o.v ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
            ))}
          </div>
          <div className="flex gap-1">
            {TYPE_OPTS.map(o => (
              <button key={o.v} onClick={() => setTypeF(o.v)}
                className={`flex-1 text-sm px-2 py-1.5 rounded ${typeF === o.v ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
            ))}
          </div>
        </div>
        <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索文件名或路径..." className="w-full bg-gray-800 text-sm px-3 py-2 border-b border-gray-800 focus:outline-none focus:border-blue-600" />

        <div className="flex-1 overflow-y-auto">
          {srcTab === 'local' ? (
            fLoading ? <p className="p-4 text-gray-600 text-sm">加载中...</p> :
            filtered.length === 0 ? <p className="p-4 text-gray-600 text-sm">无匹配文件</p> :
            filtered.map(f => (
              <div key={f.hash} draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData('application/peerdrive-file', JSON.stringify({ hash: f.hash, path: f.filename, name: f.filename, mime_type: f.mime_type, size: f.size }));
                  e.dataTransfer.effectAllowed = 'copy';
                }}
                className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group ${inDraft(f.filename, f.hash) ? 'opacity-40' : ''}`}>
                <span className="text-lg">{fileIcon(f.mime_type)}</span>
                <span className="text-blue-300 truncate flex-1 font-mono">{f.filename}</span>
                <span className="text-gray-500 text-xs">{fmtSize(f.size)}</span>
                {!inDraft(f.filename, f.hash) && (
                  <button onClick={() => addEntry(f.hash, f.filename, f.mime_type, f.size)}
                    className="text-blue-400 hover:text-blue-200 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 transition-all shrink-0">
                    + 添加
                  </button>
                )}
              </div>
            ))
          ) : (
            !collSource ? <p className="p-4 text-gray-600 text-sm">选择一个合集查看其文件</p> :
            collFiltered.length === 0 ? <p className="p-4 text-gray-600 text-sm">无匹配文件</p> :
            collFiltered.map(e => (
              <div key={e.path} draggable
                onDragStart={(ev) => {
                  ev.dataTransfer.setData('application/peerdrive-file', JSON.stringify({ hash: e.hash, path: e.path, name: e.path.split('/').pop(), mime_type: '', size: 0 }));
                  ev.dataTransfer.effectAllowed = 'copy';
                }}
                className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group ${inDraft(e.path, e.hash) ? 'opacity-40' : ''}`}>
                <span className="text-lg">📄</span>
                <span className="text-blue-300 truncate flex-1 font-mono">{e.path}</span>
                <span className="text-gray-500 text-xs font-mono">{(e.hash || '').substring(0, 10)}</span>
                {!inDraft(e.path, e.hash) && (
                  <button onClick={() => addEntry(e.hash, e.path)}
                    className="text-blue-400 hover:text-blue-200 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 transition-all shrink-0">
                    + 添加
                  </button>
                )}
              </div>
            ))
          )}
        </div>
      </div>

      <div className="w-1 bg-gray-700 hover:bg-blue-600 cursor-col-resize shrink-0 relative group" onMouseDown={onSplitMouseDown}>
        <div className="absolute inset-y-0 -left-1 -right-1" />
      </div>

      <div style={{ width: `${100 - split}%` }} className="h-full flex flex-col">
        <div className="h-12 flex items-center px-4 space-x-3 border-b border-gray-800 shrink-0">
          <Link to="/" className="text-sm text-gray-500 hover:text-white shrink-0">←</Link>
          <input value={fname} onChange={e => setFname(e.target.value)} placeholder="合集名称 (可选)" className="bg-gray-800 text-sm px-3 py-2 rounded border border-gray-700 w-48 focus:outline-none focus:border-blue-500" />
          <div className="flex-1" />
          {entries.length > 0 && !savedHash && (
            <span className="text-sm text-yellow-500 bg-yellow-500/10 px-3 py-1 rounded border border-yellow-600/30">未保存</span>
          )}
          {savedHash && entries.length > 0 && (
            <span className="text-sm text-yellow-500 bg-yellow-500/10 px-3 py-1 rounded border border-yellow-600/30">已修改</span>
          )}
          <button onClick={handleMint} disabled={saving || !entries.filter(e => e.path && e.hash).length}
            className="bg-green-600 hover:bg-green-700 disabled:opacity-40 text-sm px-4 py-2 rounded font-medium">保存</button>
          {savedHash && (
            <button onClick={handleCommit} disabled={saving}
              className="bg-blue-600 hover:bg-blue-700 disabled:opacity-40 text-sm px-4 py-2 rounded font-medium">Commit</button>
          )}
        </div>
        <div className="flex-1 overflow-hidden">
          <FileTree entries={entries} entryActions={{ onRemove: removeEntry, onRename: renameEntry, onNewFolder: (name) => addEntry('', name + '/'), onDrop: (data) => { const d = data.targetDir || ''; const n = data.name || data.path || 'untitled'; addEntry(data.hash || '', d ? `${d}/${n}` : n, data.mime_type || '', data.size || 0); } }} />
        </div>
      </div>
    </div>
  );
}
