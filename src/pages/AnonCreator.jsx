// 匿名合集创建器：从文件源（时间线/本机/已有合集）拖拽文件，构建并保存不可变合集
import React, { useContext, useState, useEffect, useRef } from 'react';
import * as api from '../api';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../App';
import FileTree from '../components/FileTree';

const LLM_URL = api.getLlmEndpoint ? api.getLlmEndpoint() : 'https://siliconflow.moonchan.xyz';
const LLM_CHAT = `${LLM_URL}/v1/chat/completions`;

// 调用 LLM 根据文件名建议合集名称
async function llmSuggest(names) {
  const res = await fetch(LLM_CHAT, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'Qwen/Qwen3-8B', messages: [{ role: 'user', content: `请用3-5个中文字为以下文件集取一个简洁的合集名称,只输出名称: ${names}` }], max_tokens: 20, stream: false }),
  });
  const d = await res.json();
  return d.choices?.[0]?.message?.content?.trim()?.replace(/["""'']/g, '') || null;
}

const SORT_OPTS = [
  { v: 'time', l: '时间' }, { v: 'name', l: '名称' },
  { v: 'path', l: '目录' }, { v: 'type', l: '类型' }, { v: 'size', l: '大小' },
];
const TYPE_OPTS = [
  { v: '', l: '全部' }, { v: 'image/', l: '图片' }, { v: 'video/', l: '视频' },
  { v: 'audio/', l: '音频' }, { v: 'text/', l: '文本' },
];
const COLL_SORT_OPTS = [
  { v: 'time', l: '时间' }, { v: 'name', l: '名称' }, { v: 'count', l: '文件数' },
];

// 根据 MIME 类型返回文件图标 emoji
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
// 格式化文件大小为可读字符串
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
  const [srcTab, setSrcTab] = useState('timeline');
  const [localViewMode, setLocalViewMode] = useState('timeline');
  const [collections, setCollections] = useState([]);
  const [collSort, setCollSort] = useState('time');
  const [collSearch, setCollSearch] = useState('');
  const [collTagFilter, setCollTagFilter] = useState('');
  const [collSource, setCollSource] = useState(null);
  const [collViewPath, setCollViewPath] = useState('');
  const [fname, setFname] = useState('');
  const [tags, setTags] = useState('');
  const [localDirPath, setLocalDirPath] = useState('');
  const [entries, setEntries] = useState([]);
  const [saving, setSaving] = useState(false);
  const [showNamePrompt, setShowNamePrompt] = useState(false);
  const lastClick = useRef(0);

  // Raw filesystem browse state
  const [sysPath, setSysPath] = useState('/');
  const [sysEntries, setSysEntries] = useState([]);
  const [sysLoading, setSysLoading] = useState(false);

  useEffect(() => { loadFiles(); loadCollections(); }, [sort]);
  useEffect(() => {
    if (srcTab === 'system') {
      setSysLoading(true);
      api.browseDir(sysPath).then(res => {
        res.sort((a,b) => (a.is_dir === b.is_dir) ? a.name.localeCompare(b.name) : (a.is_dir ? -1 : 1));
        setSysEntries(res);
      }).catch(() => setSysEntries([])).finally(() => setSysLoading(false));
    }
  }, [srcTab, sysPath]);
  useEffect(() => {
    if (navState.forkFrom) {
      const c = navState.forkFrom;
      setEntries(c.entries || []);
      setFname((c.friendly_name || '') + ' (fork)');
      nav('/anon/create', { replace: true });
    } else if (navState.draftFrom) {
      setEntries(navState.draftFrom.entries || []);
      setFname(navState.draftFrom.friendlyName || '');
      nav('/anon/create', { replace: true });
    } else if (navState.editFrom) {
      const c = navState.editFrom;
      setEntries(c.entries || []);
      setFname(c.friendly_name || '');
      nav('/anon/create', { replace: true });
    }
  }, []);
  useEffect(() => {
    setPageContext({ type: 'anonCreator', fileCount: files.length, entryCount: entries.length, friendlyName: fname });
  }, [files, entries, fname]);

  // 从后端加载已注册文件列表
  const loadFiles = async () => {
    setFLoading(true);
    try { setFiles(await api.listFiles(sort) || []); } catch { setFiles([]); }
    setFLoading(false);
  };
  // 加载已有匿名合集列表（作为文件源）
  const loadCollections = async () => {
    try { setCollections(await api.listAnonCollections() || []); } catch { setCollections([]); }
  };

  // 添加一个文件条目到待创建合集中（带防抖）
  const addEntry = (hash, path, mime_type, size) => {
    const now = Date.now();
    if (now - lastClick.current < 500) return;
    lastClick.current = now;
    setEntries(prev => [...prev, { hash, path, mime_type, size }]);
  };
  // 从待创建合集中移除条目
  const removeEntry = (entry) => {
    setEntries(prev => prev.filter(e => !(e.path === entry.path && e.hash === entry.hash)));
  };
  const renameEntry = (oldPath, newPath) => {
    setEntries(prev => prev.map(e => e.path === oldPath ? { ...e, path: newPath } : e));
  };

  // 保存/创建匿名合集，支持 AI 推荐名称
  const handleSave = async (useAI = false) => {
    const valid = entries.filter(e => e.path?.trim() && e.hash);
    if (!valid.length) return alert('请先添加文件');
    if (useAI) {
      try {
        const names = valid.slice(0, 20).map(e => e.path).join(', ');
        const name = await llmSuggest(names);
        if (name) setFname(name);
      } catch {}
    }
    if (!fname.trim() && !useAI && !showNamePrompt) {
      setShowNamePrompt(true);
      return;
    }
    setShowNamePrompt(false);
    setSaving(true);
    try {
      const res = await api.createAnonCollection(valid, fname.trim(), tags.split(/[,;]/).map(t => t.trim()).filter(Boolean));
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
    } catch (err) { alert(`创建失败: ${err.message}`); }
    setSaving(false);
  };

  const loadCollAsSource = async (hash) => {
    try {
      const c = await api.getAnonCollection(hash);
      setCollSource(c);
      setCollViewPath('');
    } catch { alert('无法加载合集'); }
  };

  const filtered = files.filter(f => {
    if (search && !(f.filename || '').toLowerCase().includes(search.toLowerCase())) return false;
    if (typeF && !(f.mime_type || '').startsWith(typeF)) return false;
    return true;
  });

  // filter collection source entries by current view path (breadcrumb navigation)
  const collDirPrefix = collViewPath ? collViewPath + '/' : '';
  const collSubDirs = new Set();
  const collFiles = [];
  if (collSource?.entries) {
    for (const e of collSource.entries) {
      const rel = e.path.startsWith(collDirPrefix) ? e.path.slice(collDirPrefix.length) : null;
      if (rel === null) continue;
      const slash = rel.indexOf('/');
      if (slash === -1) collFiles.push(e);
      else if (rel.slice(0, slash)) collSubDirs.add(rel.slice(0, slash));
    }
  }

  // filtered collection list
  const filteredCollections = collections.filter(c => {
    if (collSearch && !(c.friendly_name || c.name_preview || '').toLowerCase().includes(collSearch.toLowerCase())) return false;
    if (collTagFilter && !(c.tags || []).some(t => t.toLowerCase().includes(collTagFilter.toLowerCase()))) return false;
    return true;
  }).sort((a, b) => {
    switch (collSort) {
      case 'name': return (a.friendly_name || a.name_preview || '').localeCompare(b.friendly_name || b.name_preview || '');
      case 'count': return (b.entry_count || 0) - (a.entry_count || 0);
      default: return (b.created_at || '').localeCompare(a.created_at || '');
    }
  });

  const allCollTags = [...new Set(collections.flatMap(c => c.tags || []))].sort();

  const onSplitMouseDown = (e) => {
    e.preventDefault(); setDragging(true);
    const sx = e.clientX, ss = split;
    const onMove = (ev) => setSplit(Math.max(20, Math.min(80, ss + (ev.clientX - sx) / window.innerWidth * 100)));
    const onUp = () => { setDragging(false); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  const onDragStartFile = (e, f) => {
    const payload = JSON.stringify({
      hash: f.hash || '',
      path: f.filename || f.name || '',
      name: f.filename || f.name || '',
      mime_type: f.mime_type || '',
      size: f.size || 0,
      sysPath: f.sysPath || f.path || '',
    });
    e.dataTransfer.setData('text/plain', payload);
    e.dataTransfer.setData('application/peerdrive-file', payload);
    e.dataTransfer.effectAllowed = 'copy';
  };

  const entryActions = {
    onRemove: removeEntry,
    onRename: renameEntry,
    onNewFolder: (name) => addEntry('', name + '/'),
    onDrop: async (data) => {
      const d = data.targetDir || '';
      const n = data.name || data.path || 'untitled';
      const filePath = d ? `${d}/${n}` : n;
      let h = data.hash;
      if (!h && data.sysPath) {
        try {
          const res = await api.registerLocalFile(data.sysPath, n);
          h = res.hash;
        } catch (e) { console.error('drop register fail:', e); return; }
      }
      if (h) addEntry(h, filePath, data.mime_type || '', data.size || 0);
    },
  };

  return (
    <div className={`flex flex-1 overflow-hidden h-full bg-gray-950 ${dragging ? 'select-none' : ''}`}>
      {/* LEFT PANEL — file sources */}
      <div style={{ width: `${split}%` }} className="h-full flex flex-col border-r border-gray-700">
        {/* 文件源顶部标签栏 */}
        <div className="flex bg-gray-800 rounded mx-2 mt-2 shrink-0">
          <button onClick={() => { setSrcTab('timeline'); setLocalDirPath(''); }} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'timeline' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>🕐 时间线</button>
          <button onClick={() => { setSrcTab('registered'); setLocalDirPath(''); }} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'registered' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>📁 已注册</button>
          <button onClick={() => { setSrcTab('system'); setSysPath('/'); }} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'system' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>🖥️ 本机</button>
          <button onClick={() => setSrcTab('collection')} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'collection' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>📦 合集</button>
        </div>

        {/* === TIMELINE TAB === */}
        {srcTab === 'timeline' && (
          <>
            <div className="p-2 border-b border-gray-800 shrink-0 space-y-1.5">
              <div className="flex gap-1">{SORT_OPTS.map(o => (<button key={o.v} onClick={() => setSort(o.v)} className={`flex-1 text-sm px-2 py-1.5 rounded ${sort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>))}</div>
              <div className="flex gap-1">{TYPE_OPTS.map(o => (<button key={o.v} onClick={() => setTypeF(o.v)} className={`flex-1 text-sm px-2 py-1.5 rounded ${typeF===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>))}</div>
            </div>
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索文件名..." className="w-full bg-gray-800 text-sm px-3 py-2 border-b border-gray-800 focus:outline-none focus:border-blue-600" />
            <span className="text-xs text-gray-600 px-3 py-1">{filtered.length} 个文件</span>
            <div className="flex-1 overflow-y-auto">
              {filtered.length === 0 ? <p className="p-4 text-gray-600 text-sm">无匹配文件</p> :
                (() => {
                  const sorted = [...filtered].sort((a,b) => (b.created_at||'').localeCompare(a.created_at||''));
                  let lastDate = '';
                  return sorted.map(f => {
                    const d = f.created_at ? f.created_at.split('T')[0] : '';
                    const showDate = d !== lastDate;
                    lastDate = d;
                    return <div key={f.hash}>
                      {showDate && <div className="px-4 py-2 text-xs text-gray-500 bg-gray-900/50 sticky top-0">{d}</div>}
                      <div draggable onDragStart={(e) => onDragStartFile(e, f)} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                        <a href={api.getDownloadUrl(f.hash)} target="_blank" rel="noreferrer" className="text-lg">{fileIcon(f.mime_type)}</a>
                        <span className="text-blue-300 truncate flex-1 font-mono text-xs">{f.filename}</span>
                        <span className="text-gray-500 text-xs shrink-0">{fmtSize(f.size)}</span>
                        <button onClick={() => addEntry(f.hash,f.filename,f.mime_type,f.size)} className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
                      </div>
                    </div>;
                  });
                })()
              }
            </div>
          </>
        )}

        {/* === REGISTERED DIR TAB === */}
        {srcTab === 'registered' && (
          <>
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索文件路径..." className="w-full bg-gray-800 text-sm px-3 py-2 border-b border-gray-800 focus:outline-none focus:border-blue-600" />
            <span className="text-xs text-gray-600 px-3 py-1">{filtered.length} 个文件</span>
            <div className="flex-1 overflow-y-auto">
              {( () => {
                const prefix = localDirPath ? localDirPath + '/' : '';
                const dirs = new Set();
                const localFiles = [];
                for (const f of filtered) {
                  const rel = (f.provider_path || f.filename || '').replace(/^\//, '');
                  const rest = rel.startsWith(prefix) ? rel.slice(prefix.length) : null;
                  if (rest === null) continue;
                  const slash = rest.indexOf('/');
                  if (slash === -1) localFiles.push(f);
                  else if (rest.slice(0, slash)) dirs.add(rest.slice(0, slash));
                }
                const sortedDirs = Array.from(dirs).sort();
                if (sortedDirs.length===0 && localFiles.length===0) return <p className="p-4 text-gray-600 text-sm">此目录为空</p>;
                return (
                  <div>
                    <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 text-xs">
                      {localDirPath ? (
                        <button onClick={() => { const p = localDirPath.split('/'); p.pop(); setLocalDirPath(p.join('/')); }} className="text-gray-400 hover:text-white">← 返回</button>
                      ) : <span className="text-gray-500">📂</span>}
                      <span className="text-gray-400 font-mono text-xs">{localDirPath || '/'}</span>
                      <span className="ml-auto text-gray-600">{sortedDirs.length + localFiles.length} 项</span>
                    </div>
                    {sortedDirs.map(dir => (
                      <div key={dir} onClick={() => setLocalDirPath(localDirPath ? `${localDirPath}/${dir}` : dir)}
                        className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                        <span className="text-lg">📁</span>
                        <span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
                        <span className="text-gray-600 text-xs">文件夹</span>
                      </div>
                    ))}
                    {localFiles.map(f => (
                      <div key={f.hash} draggable onDragStart={(e) => onDragStartFile(e, f)}
                        className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                        <a href={api.getDownloadUrl(f.hash)} target="_blank" rel="noreferrer" className="text-lg">{fileIcon(f.mime_type)}</a>
                        <span className="text-blue-300 truncate flex-1 font-mono text-xs">{f.filename}</span>
                        <span className="text-gray-500 text-xs">{fmtSize(f.size)}</span>
                        <button onClick={() => addEntry(f.hash, f.filename, f.mime_type, f.size)}
                          className="text-blue-400 hover:text-blue-200 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 transition-all shrink-0">+</button>
                      </div>
                    ))}
                  </div>
                );
              })()}
            </div>
          </>
        )}

        {/* === SYSTEM BROWSE TAB === */}
        {srcTab === 'system' && (
          <>
            <div className="flex-1 overflow-y-auto">
              <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 text-xs">
                <button onClick={() => { const p = sysPath.split('/'); p.pop(); setSysPath(p.join('/') || '/'); }} disabled={sysPath === '/'} className="text-gray-400 hover:text-white disabled:opacity-30">←</button>
                <span className="text-gray-300 font-mono text-xs truncate">{sysPath}</span>
              </div>
              {sysLoading ? <p className="p-4 text-gray-600 text-sm">加载中...</p> :
               sysEntries.length === 0 ? <p className="p-4 text-gray-600 text-sm">此目录为空</p> :
               sysEntries.map(e => (
                 <div key={e.path} draggable={!e.is_dir}
                   onDragStart={e.is_dir ? undefined : (ev) => onDragStartFile(ev, { name: e.name, path: e.path, sysPath: e.path, size: e.size })}
                   onClick={() => e.is_dir ? setSysPath(e.path) : null}
                   className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm ${e.is_dir ? 'cursor-pointer' : ''}`}>
                   <span className="text-lg">{e.is_dir ? '📁' : '📄'}</span>
                   <span className={`font-mono truncate flex-1 text-xs ${e.is_dir ? 'text-yellow-400' : 'text-blue-300'}`}>{e.name}</span>
                   {e.is_dir ? <span className="text-gray-600 text-xs">文件夹</span> :
                    <button onClick={(ev) => { ev.stopPropagation(); addEntry('', e.path, '', e.size || 0); }}
                      className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>}
                 </div>
               ))}
            </div>
          </>
        )}

        {/* === COLLECTIONS TAB === */}
        {srcTab === 'collection' && !collSource && (
          <div className="flex-1 flex flex-col overflow-hidden">
            {/* collection search & sort */}
            <div className="p-2 border-b border-gray-800 shrink-0 space-y-1.5">
              <input value={collSearch} onChange={e => setCollSearch(e.target.value)} placeholder="搜索合集..." className="w-full bg-gray-800 text-sm px-3 py-2 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
              <div className="flex gap-1 flex-wrap">
                {COLL_SORT_OPTS.map(o => (
                  <button key={o.v} onClick={() => setCollSort(o.v)} className={`text-xs px-2 py-1 rounded ${collSort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
                ))}
              </div>
              {allCollTags.length > 0 && (
                <div className="flex gap-1 flex-wrap">
                  <button onClick={() => setCollTagFilter('')} className={`text-[10px] px-1.5 py-0.5 rounded-full ${!collTagFilter ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400'}`}>全部</button>
                  {allCollTags.map(t => (
                    <button key={t} onClick={() => setCollTagFilter(t === collTagFilter ? '' : t)} className={`text-[10px] px-1.5 py-0.5 rounded-full ${collTagFilter===t?'bg-blue-600 text-white':'bg-gray-700 text-gray-400 hover:bg-gray-600'}`}>{t}</button>
                  ))}
                </div>
              )}
            </div>
            <div className="flex-1 overflow-y-auto">
              {filteredCollections.length === 0 ? <p className="p-4 text-gray-600 text-sm">暂无合集</p> :
                filteredCollections.map(c => (
                  <div key={c.hash} onClick={() => loadCollAsSource(c.hash)}
                    className="px-4 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50">
                    <div className="flex items-center gap-2">
                      <span className="text-lg">📦</span>
                      <span className="text-blue-300 truncate flex-1 text-sm font-medium">{c.friendly_name || c.name_preview || (c.entry_count ? `${c.entry_count} 个文件` : '空合集')}</span>
                    </div>
                    <div className="flex items-center gap-2 mt-1 ml-8">
                      {c.tags?.map(t => <span key={t} className="text-[10px] bg-blue-900/50 text-blue-300 px-1.5 py-0.5 rounded-full">{t}</span>)}
                      <span className="text-xs text-gray-600">{c.entry_count || 0} 文件</span>
                      <span className="text-xs text-gray-500">{c.created_at ? new Date(c.created_at).toLocaleDateString() : ''}</span>
                    </div>
                  </div>
                ))}
            </div>
          </div>
        )}

        {/* === COLLECTION SOURCE FILE VIEW === */}
        {srcTab === 'collection' && collSource && (
          <div className="flex-1 flex flex-col overflow-hidden">
            {/* breadcrumb nav */}
            <div className="flex flex-col px-3 py-2 border-b border-gray-800 shrink-0 gap-1">
              <div className="flex items-center gap-2">
                <button onClick={() => setCollSource(null)} className="text-gray-400 hover:text-white text-sm">← 合集列表</button>
                <span className="text-gray-300 text-sm font-bold truncate">📦 {collSource.friendly_name || collSource.name_preview || '合集'}</span>
              </div>
              {collViewPath && (
                <div className="flex items-center gap-1 text-xs">
                  <button onClick={() => setCollViewPath('')} className="text-gray-500 hover:text-white">📦</button>
                  {(() => {
                    const parts = collViewPath.split('/');
                    return parts.map((p, i) => (
                      <span key={i} className="flex items-center gap-1">
                        <span className="text-gray-600">/</span>
                        <button
                          onClick={() => setCollViewPath(parts.slice(0, i+1).join('/'))}
                          className="text-gray-400 hover:text-white">{p}</button>
                      </span>
                    ));
                  })()}
                </div>
              )}
              <div className="text-xs text-gray-600">{collSubDirs.size + collFiles.length} 项</div>
            </div>
            <div className="flex-1 overflow-y-auto">
              {collSubDirs.size === 0 && collFiles.length === 0 && !collViewPath ? (
                /* show all entries flat when at root with no subdirs */
                (collSource.entries || []).map(e => (
                  <div key={e.path} draggable
                    onDragStart={(ev) => onDragStartFile(ev, {hash:e.hash, path:e.path, name:e.path.split('/').pop()})}
                    className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                    <span className="text-lg">📄</span>
                    <span className="text-blue-300 truncate flex-1 font-mono text-xs">{e.path}</span>
                    <button onClick={() => addEntry(e.hash, e.path)} className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
                  </div>
                ))) : collSubDirs.size === 0 && collFiles.length === 0 ? (
                <p className="p-4 text-gray-600 text-sm">此目录为空</p>
              ) : (
                <>
                  {Array.from(collSubDirs).sort().map(dir => (
                    <div key={dir} onClick={() => setCollViewPath(collViewPath ? `${collViewPath}/${dir}` : dir)}
                      className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                      <span className="text-lg">📁</span>
                      <span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
                      <span className="text-gray-600 text-xs">文件夹</span>
                    </div>
                  ))}
                  {collFiles.map(e => (
                    <div key={e.path} draggable
                      onDragStart={(ev) => onDragStartFile(ev, {hash:e.hash, path:e.path, name:e.path.split('/').pop()})}
                      className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                      <span className="text-lg">📄</span>
                      <span className="text-blue-300 truncate flex-1 font-mono text-xs">{e.path.split('/').pop()}</span>
                      <button onClick={() => addEntry(e.hash, e.path)} className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
                    </div>
                  ))}
                </>
              )}
            </div>
          </div>
        )}
      </div>

      {/* SPLIT HANDLE */}
      <div className="w-1 bg-gray-700 hover:bg-blue-600 cursor-col-resize shrink-0 relative group" onMouseDown={onSplitMouseDown}>
        <div className="absolute inset-y-0 -left-1 -right-1" />
      </div>

      {/* RIGHT PANEL — collection editor draft */}
      <div style={{ width: `${100 - split}%` }} className="h-full flex flex-col">
        <div className="h-12 flex items-center px-4 space-x-3 border-b border-gray-800 shrink-0">
          <Link to="/" className="text-sm text-gray-500 hover:text-white shrink-0">←</Link>
          <input value={fname} onChange={e => setFname(e.target.value)} placeholder="合集名称 (可选)" className="bg-gray-800 text-sm px-3 py-2 rounded border border-gray-700 w-36 focus:outline-none focus:border-blue-500" />
          <input value={tags} onChange={e => setTags(e.target.value)} placeholder="标签: tag1, tag2" className="bg-gray-800 text-sm px-3 py-2 rounded border border-gray-700 w-32 focus:outline-none focus:border-blue-500" />
          <button onClick={async () => {
            if (!entries.length) return;
            try {
              const name = await llmSuggest(entries.slice(0, 20).map(e => e.path).join(', '));
              if (name) setFname(name);
            } catch(e) {}
          }} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1.5 rounded shrink-0 whitespace-nowrap" title="AI 推荐名称">🤖</button>
          <div className="flex-1" />
          <span className="text-sm text-gray-500">{entries.filter(e => e.path && e.hash).length} 个文件</span>
          {showNamePrompt && (
            <div className="flex items-center gap-1 bg-gray-800 rounded px-2 py-1">
              <span className="text-xs text-gray-400 whitespace-nowrap">名称:</span>
              <button onClick={() => handleSave(true)} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1 rounded whitespace-nowrap">🤖 AI 推荐</button>
              <button onClick={() => handleSave(false)} className="text-xs bg-gray-600 hover:bg-gray-500 px-2 py-1 rounded whitespace-nowrap">留空</button>
              <button onClick={() => setShowNamePrompt(false)} className="text-xs text-gray-500 hover:text-white px-1">✕</button>
            </div>
          )}
          <button onClick={() => handleSave(false)} disabled={saving || !entries.filter(e => e.path && e.hash).length}
            className="bg-green-600 hover:bg-green-700 disabled:opacity-40 text-sm px-4 py-2 rounded font-medium">保存</button>
        </div>
        <div className="flex-1 overflow-hidden">
           <FileTree entries={entries} entryActions={entryActions} />
        </div>
      </div>
    </div>
  );
}
