import React, { useContext, useState, useEffect, useRef } from 'react';
import * as api from '../api';
import { Link, useNavigate } from 'react-router-dom';
import { PageContext } from '../App';

const SORT_OPTS = [
  { v: 'time', l: '时间' }, { v: 'name', l: '文件名' },
  { v: 'path', l: '目录' }, { v: 'type', l: '类型' }, { v: 'size', l: '大小' },
];

export default function AnonCreator() {
  const nav = useNavigate();
  const { setPageContext } = useContext(PageContext);

  // DB files
  const [files, setFiles] = useState([]);
  const [fLoading, setFLoading] = useState(true);
  const [sort, setSort] = useState('time');
  const [search, setSearch] = useState('');
  const [typeF, setTypeF] = useState('');

  // Collection history
  const [history, setHistory] = useState([]);

  // Workspace
  const [fname, setFname] = useState('');
  const [entries, setEntries] = useState([]);
  const [openHash, setOpenHash] = useState('');
  const [savedHash, setSavedHash] = useState('');
  const [saving, setSaving] = useState(false);
  const [openInput, setOpenInput] = useState('');

  // Drag indicator
  const [dragOver, setDragOver] = useState(false);
  const [dragOverIndex, setDragOverIndex] = useState(-1);
  const dropZone = useRef(null);

  // scroll to bottom after drop
  const entryEnd = useRef(null);

  useEffect(() => { loadFiles(); loadHistory(); }, [sort]);

  useEffect(() => {
    setPageContext({ type: 'anonCreator', fileCount: files.length, entryCount: entries.length, friendlyName: fname, openHash: openHash ? openHash.substring(0, 16) : '' });
  }, [files, entries, fname, openHash]);

  const loadFiles = async () => {
    setFLoading(true);
    try { const d = await api.listFiles(sort); setFiles(d || []); }
    catch (e) { setFiles([]); }
    setFLoading(false);
  };

  const loadHistory = async () => {
    try { const d = await api.listAnonCollections(); setHistory(d || []); }
    catch (e) { setHistory([]); }
  };

  const openCollection = async (hash) => {
    try {
      const coll = await api.getAnonCollection(hash);
      setEntries(coll.entries || []);
      setFname(coll.friendly_name || '');
      setOpenHash(hash);
      setSavedHash(hash);
    } catch (e) {
      alert('合集未找到');
    }
  };

  const handleOpenHash = () => {
    const h = (openInput.match(/[a-f0-9]{64}/i) || [])[0] || openInput.trim().toLowerCase();
    if (h.length !== 64) return alert('请输入有效的 SHA256');
    openCollection(h);
    setOpenInput('');
  };

  const dirty = () => {
    if (!savedHash) return entries.length > 0;
    // simple check: if entries length differs or something changed
    return true; // always considered dirty until minted/saved
  };

  // Drag from DB files
  const onDragStart = (e, f) => {
    e.dataTransfer.effectAllowed = 'copy';
    e.dataTransfer.setData('application/peerdrive-file', JSON.stringify({ hash: f.hash, path: f.filename }));
    e.dataTransfer.setData('text/plain', f.filename);
  };

  // Drop into collection area
  const onDragOverZone = (e) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
    setDragOver(true);
  };

  const onDragLeaveZone = () => { setDragOver(false); setDragOverIndex(-1); };

  const onDropZone = (e) => {
    e.preventDefault();
    setDragOver(false);
    setDragOverIndex(-1);
    try {
      const data = JSON.parse(e.dataTransfer.getData('application/peerdrive-file'));
      addEntry(data.hash, data.path);
    } catch { /* ignore invalid drops */ }
  };

  // Individual entry row drag over (for reordering / target position)
  const onDragOverRow = (e, idx) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
    setDragOverIndex(idx);
  };

  const addEntry = (hash, path) => {
    setEntries(prev => {
      // update existing, or append
      const existing = prev.findIndex(e => e.path === path);
      if (existing >= 0) {
        const next = [...prev];
        next[existing] = { hash, path };
        return next;
      }
      return [...prev, { hash, path }];
    });
    scrollToEnd();
  };

  const removeEntry = (idx) => setEntries(prev => prev.filter((_, i) => i !== idx));

  const updateEntryPath = (idx, path) => {
    setEntries(prev => {
      const next = [...prev];
      next[idx] = { ...next[idx], path };
      return next;
    });
  };

  const scrollToEnd = () => {
    setTimeout(() => entryEnd.current?.scrollIntoView({ behavior: 'smooth' }), 50);
  };

  const handleMint = async () => {
    const valid = entries.filter(e => e.path?.trim() && e.hash);
    if (valid.length === 0) return alert('请先拖入文件');
    setSaving(true);
    try {
      const res = await api.createAnonCollection(valid, fname.trim());
      setSavedHash(res.hash);
      setOpenHash(res.hash);
      loadHistory();
      nav(`/anon/collections/${res.hash}`);
    } catch (err) { alert(`创建失败: ${err.message}`); }
    setSaving(false);
  };

  const handleCommit = async () => {
    if (!savedHash) return handleMint();
    const valid = entries.filter(e => e.path?.trim() && e.hash);
    try {
      const res = await api.commitAnonCollection(savedHash, valid.map(e => ({ path: e.path, hash: e.hash })));
      setSavedHash(res.hash);
      setOpenHash(res.hash);
      loadHistory();
      nav(`/anon/collections/${res.hash}`);
    } catch (err) { alert(`提交失败: ${err.message}`); }
  };

  const filtered = files.filter(f => {
    if (search && !f.filename?.toLowerCase().includes(search.toLowerCase())) return false;
    if (typeF && !f.mime_type?.startsWith(typeF)) return false;
    return true;
  });

  const ico = m => {
    if (!m) return '📄';
    if (m.startsWith('image/')) return '🖼️';
    if (m.startsWith('video/')) return '🎬';
    if (m.startsWith('audio/')) return '🎵';
    if (m.startsWith('text/')) return '📝';
    if (m.includes('pdf')) return '📕';
    if (m.includes('zip') || m.includes('tar') || m.includes('gzip')) return '📦';
    return '📄';
  };

  const fmt = b => {
    if (!b) return '-';
    if (b < 1024) return b + ' B';
    if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
    if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
    return (b / 1073741824).toFixed(1) + ' GB';
  };

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      {/* === LEFT SIDEBAR === */}
      <div className="w-[320px] shrink-0 border-r border-gray-700 flex flex-col">
        <div className="h-12 flex items-center px-3 space-x-2 border-b border-gray-800 shrink-0">
          <Link to="/anon" className="text-xs text-gray-400 hover:text-white">← 合集</Link>
          <span className="text-sm font-bold">文件浏览器</span>
        </div>

        <div className="p-2 space-y-1 border-b border-gray-800 shrink-0">
          <div className="flex gap-1">
            <select value={sort} onChange={e => setSort(e.target.value)}
              className="bg-gray-800 text-[10px] px-1 py-0.5 rounded border border-gray-700 flex-1">
              {SORT_OPTS.map(o => <option key={o.v} value={o.v}>{o.l} ↓</option>)}
            </select>
            <select value={typeF} onChange={e => setTypeF(e.target.value)}
              className="bg-gray-800 text-[10px] px-1 py-0.5 rounded border border-gray-700 flex-1">
              <option value="">全部</option>
              <option value="image/">图片</option>
              <option value="video/">视频</option>
              <option value="audio/">音频</option>
              <option value="text/">文本</option>
            </select>
          </div>
          <input value={search} onChange={e => setSearch(e.target.value)}
            placeholder="搜索..."
            className="w-full bg-gray-800 text-[10px] px-2 py-1 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
        </div>

        <div className="flex-1 overflow-y-auto text-[10px]">
          {fLoading ? <p className="p-3 text-gray-600">加载中...</p> :
            filtered.length === 0 ? <p className="p-3 text-gray-600">无文件</p> :
            filtered.map(f => (
              <div key={f.hash}
                draggable
                onDragStart={e => onDragStart(e, f)}
                onClick={() => addEntry(f.hash, f.filename)}
                className="flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-gray-800 border-b border-gray-800/50 select-none"
                title={f.filename}>
                <span>{ico(f.mime_type)}</span>
                <span className="text-blue-300 truncate flex-1 font-mono">{f.filename}</span>
                <span className="text-gray-600">{fmt(f.size)}</span>
              </div>
            ))
          }
        </div>

        {/* Collection history */}
        <div className="border-t border-gray-700 shrink-0">
          <div className="flex items-center justify-between px-3 py-2 border-b border-gray-800 text-[10px] text-gray-500">
            <span>历史合集</span>
            <button onClick={loadHistory} className="hover:text-white">↻</button>
          </div>
          <div className="max-h-[160px] overflow-y-auto text-[10px]">
            {history.length === 0 ? (
              <p className="px-3 py-2 text-gray-600">无记录</p>
            ) : history.map(c => (
              <div key={c.hash}
                onClick={() => openCollection(c.hash)}
                className={`px-3 py-1.5 cursor-pointer hover:bg-gray-800 truncate ${openHash === c.hash ? 'bg-blue-900/30' : ''}`}>
                <span className="text-blue-300">{c.friendly_name || c.hash.substring(0, 12) + '...'}</span>
                {c.friendly_name && <span className="text-gray-600 ml-1">v{c.version}</span>}
                <span className="block text-gray-600 truncate">{c.hash.substring(0, 16)}...</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* === RIGHT: WORKSPACE === */}
      <div className="flex-1 flex flex-col">
        {/* Toolbar */}
        <div className="h-12 flex items-center px-4 space-x-3 border-b border-gray-800 shrink-0">
          <input value={fname} onChange={e => setFname(e.target.value)}
            placeholder="合集友好名称..."
            className="bg-gray-800 text-sm px-3 py-1.5 rounded border border-gray-700 w-48 focus:outline-none focus:border-blue-500" />

          {openHash && (
            <code className="text-[10px] text-gray-500 font-mono bg-gray-800 px-2 py-1 rounded truncate max-w-[200px]"
              title={openHash}>{openHash.substring(0, 16)}...</code>
          )}

          <div className="flex-1" />

          {!savedHash && entries.length > 0 && (
            <span className="text-[10px] text-yellow-500 bg-yellow-500/10 px-2 py-0.5 rounded border border-yellow-600/30">
              未保存
            </span>
          )}
          {(savedHash && entries.length > 0) && (
            <span className="text-[10px] text-yellow-500 bg-yellow-500/10 px-2 py-0.5 rounded border border-yellow-600/30">
              已修改
            </span>
          )}

          <div className="flex items-center gap-1">
            <input value={openInput} onChange={e => setOpenInput(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleOpenHash()}
              onPaste={e => {
                const t = e.clipboardData.getData('text');
                const m = (t.match(/[a-f0-9]{64}/i) || [])[0];
                if (m) { e.preventDefault(); openCollection(m); setOpenInput(''); }
              }}
              placeholder="按 hash 打开..."
              className="bg-gray-800 text-[10px] px-2 py-1 rounded border border-gray-700 w-36 font-mono focus:outline-none focus:border-blue-500" />
            <button onClick={handleOpenHash}
              className="bg-gray-700 hover:bg-gray-600 text-[10px] px-2 py-1 rounded whitespace-nowrap">打开</button>
          </div>

          <button onClick={handleMint}
            disabled={saving || entries.filter(e => e.path && e.hash).length === 0}
            className="bg-green-600 hover:bg-green-700 disabled:opacity-40 text-sm px-4 py-1.5 rounded font-medium">
            Mint
          </button>
          {savedHash && (
            <button onClick={handleCommit} disabled={saving}
              className="bg-blue-600 hover:bg-blue-700 disabled:opacity-40 text-sm px-4 py-1.5 rounded font-medium">
              Commit
            </button>
          )}
        </div>

        {/* New entry quick add row */}
        <div className="h-8 flex items-center px-4 border-b border-gray-800 text-[10px] text-gray-500 shrink-0">
          拖拽左侧文件至此区域放入合集，或直接点击文件添加
          <span className="ml-auto">{entries.length} 个条目</span>
        </div>

        {/* Droppable entries area */}
        <div
          ref={dropZone}
          className={`flex-1 overflow-y-auto p-2 transition-colors ${dragOver ? 'bg-blue-900/20 border-2 border-dashed border-blue-500/50' : ''}`}
          onDragOver={onDragOverZone}
          onDragLeave={onDragLeaveZone}
          onDrop={onDropZone}
        >
          {dragOver && entries.length === 0 && (
            <div className="flex items-center justify-center h-full text-sm text-blue-400">释放以添加文件</div>
          )}

          {entries.length === 0 && !dragOver ? (
            <div className="flex items-center justify-center h-full text-gray-600 text-xs">
              ← 从左侧文件列表中拖拽或点击文件来添加到合集
            </div>
          ) : entries.map((e, idx) => (
            <div key={`${e.path}-${idx}`}
              onDragOver={e => onDragOverRow(e, idx)}
              className={`flex items-center gap-2 py-1.5 px-2 rounded text-sm group ${dragOverIndex === idx ? 'border-t-2 border-blue-500' : ''}`}>
              <span className="text-gray-500 text-[10px] w-6 text-right font-mono">{idx + 1}</span>
              <span className="font-mono text-blue-300 text-xs truncate max-w-[300px]" title={e.hash}>{e.hash.substring(0, 16)}...</span>
              <span className="text-gray-600 text-[10px]">→</span>
              <input value={e.path} onChange={ev => updateEntryPath(idx, ev.target.value)}
                className="flex-1 bg-gray-800 text-xs px-2 py-1 rounded border border-gray-700 focus:outline-none focus:border-blue-500 font-mono" />
              <button onClick={() => removeEntry(idx)}
                className="text-gray-600 hover:text-red-400 invisible group-hover:visible text-lg px-1">×</button>
            </div>
          ))}
          <div ref={entryEnd} />
        </div>
      </div>
    </div>
  );
}
