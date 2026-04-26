import React, { useContext, useState, useEffect, useRef } from 'react';
import * as api from '../api';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../App';
import FileTree from '../components/FileTree';

const LLM_URL = api.getLlmEndpoint ? api.getLlmEndpoint() : 'https://siliconflow.moonchan.xyz';
const LLM_CHAT = `${LLM_URL}/v1/chat/completions`;

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
  const [tags, setTags] = useState('');
  const [localDirPath, setLocalDirPath] = useState('');
  const [entries, setEntries] = useState([]);
  const [openHash, setOpenHash] = useState('');
  const [savedHash, setSavedHash] = useState('');
  const [saving, setSaving] = useState(false);
  const lastClick = useRef(0);

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
    const now = Date.now();
    if (now - lastClick.current < 500) return;
    lastClick.current = now;
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
    if (!fname.trim()) {
      const choice = confirm('合集名称未设置。\n\n点"确定"使用 AI 推荐名称\n点"取消"留空保存');
      if (choice) {
        try {
          const names = valid.slice(0, 20).map(e => e.path).join(', ');
          const name = await llmSuggest(names);
          if (name) setFname(name);
        } catch {}
      }
    }
    setSaving(true);
    const oldHash = savedHash;
    try {
      const res = await api.createAnonCollection(valid, fname.trim(), tags.split(/[,;]/).map(t => t.trim()).filter(Boolean));
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

  const entryActions = {
    onRemove: removeEntry,
    onRename: renameEntry,
    onNewFolder: (name) => addEntry('', name + '/'),
    onDrop: (data) => {
      const d = data.targetDir || '';
      const n = data.name || data.path || 'untitled';
      addEntry(data.hash || '', d ? `${d}/${n}` : n, data.mime_type || '', data.size || 0);
    },
  };

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

        {srcTab === 'collection' && !collSource && (
          <div className="flex-1 flex flex-col">
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索合集..." className="w-full bg-gray-800 text-sm px-3 py-2 border-b border-gray-800 focus:outline-none focus:border-blue-600" />
            <div className="flex-1 overflow-y-auto">
              {collections.length === 0 ? <p className="p-4 text-gray-600 text-sm">暂无历史合集</p> :
                collections.filter(c => !search || (c.friendly_name||c.name_preview||'').toLowerCase().includes(search.toLowerCase()) || (c.hash||'').toLowerCase().includes(search.toLowerCase()))
                .map(c => (
                  <div key={c.hash} onClick={() => loadCollAsSource(c.hash)}
                    className="px-4 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50">
                    <div className="flex items-center gap-2"><span className="text-lg">📦</span><span className="text-blue-300 truncate flex-1 text-sm font-medium">{c.friendly_name || c.name_preview || c.hash?.substring(0,16)+'...'}</span></div>
                    <div className="flex items-center gap-2 mt-1 ml-8">{c.tags?.map(t => <span key={t} className="text-[10px] bg-blue-900/50 text-blue-300 px-1.5 py-0.5 rounded-full">{t}</span>)}<span className="text-xs text-gray-600">{c.entry_count || 0} 文件</span><span className="text-xs text-gray-500">{c.created_at ? new Date(c.created_at).toLocaleDateString() : ''}</span></div>
                  </div>
                ))}
            </div>
          </div>
        )}
        {srcTab === 'collection' && collSource && (
          <div className="flex-1 flex flex-col">
            <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 text-sm"><button onClick={() => setCollSource(null)} className="text-gray-400 hover:text-white">← 返回</button><span className="text-gray-300 truncate">{collSource.friendly_name || '合集'}</span><span className="text-gray-600 text-xs ml-auto">{collSource.entries?.length || 0} 项</span></div>
            <div className="flex-1 overflow-y-auto">
              {(() => {
                const dirs = new Set(); const cfiles = [];
                for (const e of collFiltered) { const slash = e.path.indexOf('/'); if (slash === -1) cfiles.push(e); else dirs.add(e.path.slice(0, slash)); }
                return <div>
                  {Array.from(dirs).sort().map(dir => <div key={dir} onClick={() => { setCollSource(prev => prev ? {...prev, entries: prev.entries.filter(e => e.path.startsWith(dir+'/'))} : prev); }} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm"><span className="text-lg">📁</span><span className="text-yellow-400 font-mono truncate flex-1">{dir}</span></div>)}
                  {cfiles.map(e => <div key={e.path} draggable onDragStart={(ev) => { ev.dataTransfer.setData('text/plain', e.path); ev.dataTransfer.setData('application/peerdrive-file', JSON.stringify({hash:e.hash,path:e.path,name:e.path.split('/').pop()})); ev.dataTransfer.effectAllowed = 'copy'; }} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group"><span className="text-lg">📄</span><span className="text-blue-300 truncate flex-1 font-mono">{e.path.split('/').pop()}</span><button onClick={() => addEntry(e.hash, e.path.split('/').pop())} className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button></div>)}
                  {dirs.size===0&&cfiles.length===0&&<p className="p-4 text-gray-600 text-sm">空合集</p>}
                </div>;
              })()}
            </div>
          </div>
        )}

        {srcTab === 'local' && (
          <div className="p-2 border-b border-gray-800 shrink-0 space-y-1.5">
            <div className="flex gap-1">{SORT_OPTS.map(o => (<button key={o.v} onClick={() => setSort(o.v)} className={`flex-1 text-sm px-2 py-1.5 rounded ${sort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>))}</div>
            <div className="flex gap-1">{TYPE_OPTS.map(o => (<button key={o.v} onClick={() => setTypeF(o.v)} className={`flex-1 text-sm px-2 py-1.5 rounded ${typeF===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>))}</div>
          </div>
        )}
        {srcTab === 'local' && <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索文件名或路径..." className="w-full bg-gray-800 text-sm px-3 py-2 border-b border-gray-800 focus:outline-none focus:border-blue-600" />}

        <div className="flex-1 overflow-y-auto">
          {srcTab === 'local' ? (() => {
            const prefix = localDirPath ? localDirPath + '/' : '';
            const dirs = new Set();
            const localFiles = [];
            for (const f of filtered) {
              const rel = (f.provider_path || f.filename || '');
              const rest = rel.startsWith(prefix) ? rel.slice(prefix.length) : null;
              if (rest === null) continue;
              const slash = rest.indexOf('/');
              if (slash === -1) localFiles.push(f);
              else if (rest.slice(0, slash)) dirs.add(rest.slice(0, slash));
            }
            const sortedDirs = Array.from(dirs).sort();
            if (fLoading) return <p className="p-4 text-gray-600 text-sm">加载中...</p>;
            return (
              <div>
                {(localDirPath || sortedDirs.length > 0 || localFiles.length > 0) && (
                  <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 text-xs">
                    {localDirPath ? (
                      <button onClick={() => { const p = localDirPath.split('/'); p.pop(); setLocalDirPath(p.join('/')); }} className="text-gray-400 hover:text-white">← 返回</button>
                    ) : (
                      <span className="text-gray-500">📂</span>
                    )}
                    <span className="text-gray-400">{localDirPath || '/'}</span>
                    <span className="ml-auto text-gray-600">{sortedDirs.length + localFiles.length} 项</span>
                  </div>
                )}
                {sortedDirs.map(dir => (
                  <div key={dir} onClick={() => setLocalDirPath(localDirPath ? `${localDirPath}/${dir}` : dir)}
                    className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                    <span className="text-lg">📁</span>
                    <span className="text-yellow-400 font-mono truncate flex-1">{dir}</span>
                    <span className="text-gray-600 text-xs">文件夹</span>
                  </div>
                ))}
                {localFiles.map(f => (
                  <div key={f.hash} draggable
                    onDragStart={(e) => {
                      e.dataTransfer.setData('text/plain', f.filename);
                      e.dataTransfer.setData('application/peerdrive-file', JSON.stringify({ hash: f.hash, path: f.filename, name: f.filename, mime_type: f.mime_type, size: f.size }));
                      e.dataTransfer.effectAllowed = 'copy';
                    }}
                    className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                    <a href={api.getDownloadUrl(f.hash)} target="_blank" rel="noreferrer" className="text-lg">{fileIcon(f.mime_type)}</a>
                    <span className="text-blue-300 truncate flex-1 font-mono">{f.filename}</span>
                    <span className="text-gray-500 text-xs">{fmtSize(f.size)}</span>
                    <button onClick={() => addEntry(f.hash, f.filename, f.mime_type, f.size)}
                      className="text-blue-400 hover:text-blue-200 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 transition-all shrink-0">+</button>
                  </div>
                ))}
                {sortedDirs.length === 0 && localFiles.length === 0 && (
                  <p className="p-4 text-gray-600 text-sm">{localDirPath ? '此目录为空' : '无匹配文件'}</p>
                )}
              </div>
            );
          })() : (
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
          <input value={fname} onChange={e => setFname(e.target.value)} placeholder="合集名称 (可选)" className="bg-gray-800 text-sm px-3 py-2 rounded border border-gray-700 w-36 focus:outline-none focus:border-blue-500" />
          <input value={tags} onChange={e => setTags(e.target.value)} placeholder="标签: tag1, tag2" className="bg-gray-800 text-sm px-3 py-2 rounded border border-gray-700 w-28 focus:outline-none focus:border-blue-500" />
          <button onClick={async () => {
            if (!entries.length) return;
            try {
              const name = await llmSuggest(entries.slice(0, 20).map(e => e.path).join(', '));
              if (name) setFname(name);
            } catch(e) {}
          }} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1.5 rounded shrink-0 whitespace-nowrap" title="AI 推荐名称">🤖</button>
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
           <FileTree entries={entries} entryActions={entryActions} />
        </div>
      </div>
    </div>
  );
}
