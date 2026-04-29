// 匿名合集创建器：左侧合集列表（支持搜索/标签/历史）+ 右侧文件源与编辑区
// Android式导航：点击合集进入浏览 → 回退列表 → 点击文件选中
import React, { useContext, useState, useEffect, useRef, useMemo } from 'react';
import * as api from '../api';
import { Link, useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../App';
import FileTree from '../components/FileTree';

const LLM_URL = api.getLlmEndpoint ? api.getLlmEndpoint() : 'https://siliconflow.moonchan.xyz';
const LLM_CHAT = `${LLM_URL}/v1/chat/completions`;

const SEARCH_HISTORY_KEY = 'peerdrive_search_history';
const MAX_HISTORY = 20;

function loadSearchHistory() {
  try { return JSON.parse(localStorage.getItem(SEARCH_HISTORY_KEY) || '[]'); } catch { return []; }
}
function saveSearchHistory(items) {
  try { localStorage.setItem(SEARCH_HISTORY_KEY, JSON.stringify(items.slice(0, MAX_HISTORY))); } catch {}
}
function addSearchHistory(q) {
  if (!q?.trim()) return;
  const h = loadSearchHistory().filter(i => i !== q);
  h.unshift(q);
  saveSearchHistory(h);
}

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

  const [split, setSplit] = useState(35);
  const [dragging, setDragging] = useState(false);
  const [files, setFiles] = useState([]);
  const [fLoading, setFLoading] = useState(true);
  const [sort, setSort] = useState('time');
  const [search, setSearch] = useState('');
  const [typeF, setTypeF] = useState('');
  const [srcTab, setSrcTab] = useState('timeline');
  const [collections, setCollections] = useState([]);
  const [collSort, setCollSort] = useState('time');
  const [collSearch, setCollSearch] = useState('');
  const [collTagFilter, setCollTagFilter] = useState('');
  // Android式导航：进入的合集
  const [enteredColl, setEnteredColl] = useState(null);
  const [enteredCollFiles, setEnteredCollFiles] = useState(null);
  const [collViewPath, setCollViewPath] = useState('');
  const [fname, setFname] = useState('');
  const [tags, setTags] = useState('');
  const [entries, setEntries] = useState([]);
  const [saving, setSaving] = useState(false);
  const [showNamePrompt, setShowNamePrompt] = useState(false);
  const [toastMsg, setToastMsg] = useState('');
  const [toastErr, setToastErr] = useState(false);
  const lastClick = useRef(0);

  // 选择模式：批量选中合集/文件
  const [selectMode, setSelectMode] = useState(false);
  const [selectedColls, setSelectedColls] = useState(new Set());
  const [selectedFiles, setSelectedFiles] = useState(new Set());

  // 搜索历史
  const [searchHistory, setSearchHistory] = useState(loadSearchHistory());
  const [showHistory, setShowHistory] = useState(false);

  // Raw filesystem browse
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
      setFname((c.friendly_name || '') + ' (副本)');
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

  const loadFiles = async () => {
    setFLoading(true);
    try { setFiles(await api.listFiles(sort) || []); } catch { setFiles([]); }
    setFLoading(false);
  };
  const loadCollections = async () => {
    try { setCollections(await api.listAnonCollections() || []); } catch { setCollections([]); }
  };

  const showToast = (msg, err = false) => {
    setToastMsg(msg); setToastErr(err); setTimeout(() => setToastMsg(''), 3000);
  };

  // Android式：进入合集浏览
  const enterCollection = async (hash) => {
    try {
      const c = await api.getAnonCollection(hash);
      if (!c?.entries?.length) { showToast('空合集', true); return; }
      setEnteredColl(c);
      setEnteredCollFiles(c.entries || []);
      setCollViewPath('');
    } catch { showToast('无法加载合集', true); }
  };
  // 退出合集浏览，返回列表
  const leaveCollection = () => {
    setEnteredColl(null);
    setEnteredCollFiles(null);
    setCollViewPath('');
    setSelectMode(false);
    setSelectedColls(new Set());
    setSelectedFiles(new Set());
  };

  // 在合集浏览中导航子目录
  const navIntoDir = (dir) => setCollViewPath(prev => prev ? `${prev}/${dir}` : dir);
  const navBackDir = () => {
    const p = collViewPath.split('/'); p.pop(); setCollViewPath(p.join('/'));
  };

  // 计算当前合集视图中的目录和文件
  const currentCollView = useMemo(() => {
    if (!enteredCollFiles) return { dirs: [], files: [], total: 0 };
    const dirs = new Set();
    const files = [];
    const prefix = collViewPath ? collViewPath + '/' : '';
    for (const e of enteredCollFiles) {
      if (!e.path.startsWith(prefix)) continue;
      const rel = e.path.slice(prefix.length);
      const slash = rel.indexOf('/');
      if (slash === -1) files.push(e);
      else if (rel.slice(0, slash)) dirs.add(rel.slice(0, slash));
    }
    const total = enteredCollFiles.filter(e => e.path.startsWith(prefix)).length;
    return { dirs: Array.from(dirs).sort(), files, total };
  }, [enteredCollFiles, collViewPath]);

  const addEntry = (hash, path, mime_type, size) => {
    const now = Date.now();
    if (now - lastClick.current < 500) return;
    lastClick.current = now;
    const providers = hash ? [{ type: "sha256", value: hash, mime_type: mime_type || '' }] : [];
    setEntries(prev => [...prev, { hash, path, providers, mime_type, size }]);
  };
  const removeEntry = (entry) => {
    setEntries(prev => prev.filter(e => {
      const eh = e.hash || e.providers?.[0]?.value;
      const th = entry.hash || entry.providers?.[0]?.value;
      return !(e.path === entry.path && eh === th);
    }));
  };
  const renameEntry = (oldPath, newPath) => {
    setEntries(prev => prev.map(e => e.path === oldPath ? { ...e, path: newPath } : e));
  };

  // 保存合集
  const handleSave = async (useAI = false) => {
    const valid = entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value));
    if (!valid.length) { showToast('请先添加文件', true); return; }
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
      const tagList = tags.split(/[,;]/).map(t => t.trim()).filter(Boolean);
      const res = await api.createAnonCollection(valid, fname.trim(), tagList);
      loadCollections();
      showToast('合集创建成功');
      nav(`/anon/collections/${res.hash}`);
    } catch (err) { showToast(`创建失败: ${err.message}`, true); }
    setSaving(false);
  };

  // 保存合集到我的node（从进入的合集）
  const saveCollToNode = async (coll) => {
    if (!coll?.entries?.length) return;
    try {
      const name = (coll.friendly_name || '合集') + ' (副本)';
      const res = await api.createAnonCollection(coll.entries, name);
      showToast('已保存到本机');
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
    } catch(e) { showToast('保存失败: ' + e.message, true); }
  };

  // 批量保存选中的文件到新合集
  const batchSaveFiles = async () => {
    const selectedEntries = enteredCollFiles?.filter(f => selectedFiles.has(f.path)) || [];
    if (!selectedEntries.length) { showToast('请选择文件', true); return; }
    try {
      const name = (enteredColl?.friendly_name || '选集') + ' (选集)';
      const res = await api.createAnonCollection(selectedEntries.map(e => ({ path: e.path, hash: e.hash, providers: e.providers })), name);
      showToast(`已保存 ${selectedEntries.length} 个文件`);
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
    } catch(e) { showToast('保存失败: ' + e.message, true); }
  };

  // 批量保存选中的合集
  const batchSaveColls = async () => {
    const sel = collections.filter(c => selectedColls.has(c.hash));
    if (!sel.length) { showToast('请选择合集', true); return; }
    try {
      for (const c of sel) {
        const full = await api.getAnonCollection(c.hash);
        if (full?.entries?.length) {
          await api.createAnonCollection(full.entries, (full.friendly_name || c.friendly_name || '合集') + ' (副本)');
        }
      }
      showToast(`已保存 ${sel.length} 个合集`);
      loadCollections();
    } catch(e) { showToast('批量保存失败: ' + e.message, true); }
  };

  // 切换合集选择
  const toggleCollSelect = (hash) => {
    setSelectedColls(prev => { const n = new Set(prev); n.has(hash) ? n.delete(hash) : n.add(hash); return n; });
  };
  const toggleFileSelect = (path) => {
    setSelectedFiles(prev => { const n = new Set(prev); n.has(path) ? n.delete(path) : n.add(path); return n; });
  };

  // 文件源筛选
  const filtered = files.filter(f => {
    if (search && !(f.filename || '').toLowerCase().includes(search.toLowerCase())) return false;
    if (typeF && !(f.mime_type || '').startsWith(typeF)) return false;
    return true;
  });

  // 合集筛选
  const filteredCollections = useMemo(() => {
    return collections.filter(c => {
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
  }, [collections, collSearch, collTagFilter, collSort]);

  const allCollTags = useMemo(() =>
    [...new Set(collections.flatMap(c => c.tags || []))].sort()
  , [collections]);

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

  // 合集名显示逻辑（不显示SHA256，用友好名称或"文件a, 文件b 等N个文件"）
  const collDisplayName = (c) => {
    if (c.friendly_name) return c.friendly_name;
    if (c.name_preview && c.name_preview !== '未命名') return c.name_preview;
    if (c.entry_count > 0) {
      // 尝试获取前几个文件名
      return `${c.entry_count} 个文件`;
    }
    return '空合集';
  };

  return (
    <div className={`flex flex-1 overflow-hidden h-full bg-gray-950 ${dragging ? 'select-none' : ''}`}>
      {/* ===== LEFT PANEL — 合集列表 ===== */}
      <div style={{ width: `${split}%` }} className="h-full flex flex-col border-r border-gray-700">
        {/* 合集列表头部 */}
        <div className="shrink-0 bg-gray-900">
          <div className="flex items-center gap-2 px-3 pt-2 pb-1">
            <h3 className="text-sm font-bold text-gray-300 flex items-center gap-1">📦 合集</h3>
            <span className="text-[10px] text-gray-600">{collections.length}</span>
            <div className="flex-1" />
            <button onClick={() => { setSelectMode(!selectMode); setSelectedColls(new Set()); setSelectedFiles(new Set()); }}
              className={`text-[10px] px-2 py-0.5 rounded ${selectMode ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'}`}>
              {selectMode ? '退出选择' : '选择'}
            </button>
            <Link to="/" className="text-[10px] bg-gray-800 text-gray-400 hover:text-white px-2 py-0.5 rounded">广场</Link>
          </div>

          {/* 搜索栏 + 历史 */}
          <div className="px-2 pb-1 relative">
            <input value={collSearch} onChange={e => { setCollSearch(e.target.value); setShowHistory(true); }}
              onFocus={() => setShowHistory(true)}
              onBlur={() => setTimeout(() => setShowHistory(false), 200)}
              onKeyDown={e => { if (e.key === 'Enter') { addSearchHistory(collSearch); setSearchHistory(loadSearchHistory()); setShowHistory(false); } }}
              placeholder="搜索合集名称 / tag..." className="w-full bg-gray-800 text-xs px-3 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
            {showHistory && searchHistory.length > 0 && !collSearch && (
              <div className="absolute top-full left-2 right-2 bg-gray-800 border border-gray-700 rounded shadow-lg z-20 max-h-32 overflow-y-auto">
                <div className="text-[10px] text-gray-500 px-2 py-0.5">最近搜索</div>
                {searchHistory.map((h, i) => (
                  <div key={i} className="px-3 py-1 text-xs text-gray-400 hover:bg-gray-700 cursor-pointer"
                    onMouseDown={() => { setCollSearch(h); addSearchHistory(h); setSearchHistory(loadSearchHistory()); }}>
                    🕐 {h}
                  </div>
                ))}
                <div className="text-[10px] text-gray-600 px-2 py-0.5 cursor-pointer hover:text-red-400 border-t border-gray-700"
                  onMouseDown={() => { localStorage.removeItem(SEARCH_HISTORY_KEY); setSearchHistory([]); }}>清除历史</div>
              </div>
            )}
          </div>

          {/* 排序 + 标签过滤 */}
          <div className="px-2 pb-1.5 space-y-1">
            <div className="flex gap-0.5">
              {COLL_SORT_OPTS.map(o => (
                <button key={o.v} onClick={() => setCollSort(o.v)}
                  className={`text-[10px] px-2 py-0.5 rounded ${collSort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
              ))}
            </div>
            {allCollTags.length > 0 && (
              <div className="flex gap-0.5 flex-wrap">
                <button onClick={() => setCollTagFilter('')}
                  className={`text-[10px] px-1.5 py-0.5 rounded-full ${!collTagFilter ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400'}`}>全部</button>
                {allCollTags.map(t => (
                  <button key={t} onClick={() => setCollTagFilter(t === collTagFilter ? '' : t)}
                    className={`text-[10px] px-1.5 py-0.5 rounded-full ${collTagFilter===t?'bg-blue-600 text-white':'bg-gray-700 text-gray-300 hover:bg-gray-600'}`}>{t}</button>
                ))}
              </div>
            )}
          </div>

          {/* 选择模式操作栏 */}
          {selectMode && selectedColls.size > 0 && (
            <div className="px-2 pb-1.5">
              <button onClick={batchSaveColls}
                className="w-full text-xs bg-blue-600 hover:bg-blue-700 px-2 py-1 rounded">
                💾 保存选中的 {selectedColls.size} 个合集到本机
              </button>
            </div>
          )}
        </div>

        {/* 合集列表 — Android式导航 */}
        <div className="flex-1 overflow-y-auto">
          {/* 进入合集后的文件浏览视图 */}
          {enteredColl ? (
            <div className="flex flex-col h-full">
              {/* 合集内导航栏 */}
              <div className="flex flex-col px-3 py-2 border-b border-gray-800 shrink-0 gap-1 bg-gray-900 sticky top-0 z-10">
                <div className="flex items-center gap-2">
                  <button onClick={leaveCollection} className="text-gray-400 hover:text-white text-sm">← 合集列表</button>
                  <span className="text-gray-300 text-sm font-bold truncate">📦 {enteredColl.friendly_name || collDisplayName(enteredColl)}</span>
                </div>
                {collViewPath && (
                  <div className="flex items-center gap-1 text-[10px] ml-6">
                    <button onClick={() => setCollViewPath('')} className="text-gray-500 hover:text-white">📦</button>
                    {collViewPath.split('/').map((p, i) => (
                      <span key={i} className="flex items-center gap-1">
                        <span className="text-gray-600">/</span>
                        <button onClick={() => setCollViewPath(collViewPath.split('/').slice(0, i+1).join('/'))}
                          className="text-gray-400 hover:text-white">{p}</button>
                      </span>
                    ))}
                  </div>
                )}
                <div className="flex items-center gap-2 text-[10px]">
                  <span className="text-gray-600">{currentCollView.total} 项</span>
                  <div className="flex-1" />
                  {/* 保存到本机按钮 */}
                  {!collViewPath && (
                    <button onClick={() => saveCollToNode(enteredColl)}
                      className="text-[10px] bg-blue-600 hover:bg-blue-700 px-2 py-0.5 rounded">💾 保存到本机</button>
                  )}
                  {/* 选择模式切换 */}
                  <button onClick={() => { setSelectMode(!selectMode); setSelectedFiles(new Set()); }}
                    className={`text-[10px] px-2 py-0.5 rounded ${selectMode ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400'}`}>
                    {selectMode ? '取消' : '选择文件'}
                  </button>
                </div>
                {/* 选择模式批量操作 */}
                {selectMode && selectedFiles.size > 0 && (
                  <button onClick={batchSaveFiles}
                    className="text-[10px] bg-blue-600 hover:bg-blue-700 px-2 py-1 rounded">
                    💾 保存选中的 {selectedFiles.size} 个文件为新合集
                  </button>
                )}
              </div>

              {/* 文件列表 */}
              <div className="flex-1 overflow-y-auto">
                {currentCollView.dirs.length === 0 && currentCollView.files.length === 0 ? (
                  <div className="p-4 text-gray-600 text-xs text-center">
                    {collViewPath ? '此目录为空' : (
                      enteredColl.entries?.map(e => (
                        <div key={e.path}
                          className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm ${selectMode ? 'cursor-pointer' : ''}`}
                          onClick={() => {
                            if (selectMode) { toggleFileSelect(e.path); return; }
                            // Android式：点击文件 = 添加到编辑区
                            addEntry(e.hash, e.path, e.mime_type, e.size);
                            showToast(`已添加: ${e.path.split('/').pop()}`);
                          }}>
                          {selectMode && <input type="checkbox" checked={selectedFiles.has(e.path)} readOnly className="shrink-0" />}
                          <span className="text-lg">{fileIcon(e.mime_type)}</span>
                          <span className="text-blue-300 truncate flex-1 font-mono text-xs">{e.path}</span>
                          {!selectMode && <button className="text-blue-400 text-xs px-2 py-0.5 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>}
                        </div>
                      ))
                    )}
                  </div>
                ) : (
                  <>
                    {Array.from(currentCollView.dirs).map(dir => (
                      <div key={dir} onClick={() => navIntoDir(dir)}
                        className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                        <span className="text-lg">📁</span>
                        <span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
                        <span className="text-gray-600 text-xs">文件夹</span>
                      </div>
                    ))}
                    {currentCollView.files.map(e => (
                      <div key={e.path}
                        className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm ${selectMode ? 'cursor-pointer' : ''}`}
                        onClick={() => {
                          if (selectMode) { toggleFileSelect(e.path); return; }
                          addEntry(e.hash, e.path, e.mime_type, e.size);
                          showToast(`已添加: ${e.path.split('/').pop()}`);
                        }}>
                        {selectMode && <input type="checkbox" checked={selectedFiles.has(e.path)} readOnly className="shrink-0" />}
                        <span className="text-lg">{fileIcon(e.mime_type)}</span>
                        <span className="text-blue-300 truncate flex-1 font-mono text-xs">{e.path.split('/').pop()}</span>
                        {!selectMode && <button className="text-blue-400 text-xs px-2 py-0.5 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>}
                      </div>
                    ))}
                  </>
                )}
              </div>
            </div>
          ) : (
            /* 合集列表（未进入合集时） */
            <>
              {filteredCollections.length === 0 ? (
                <p className="p-4 text-gray-600 text-xs text-center">
                  {collSearch || collTagFilter ? '无匹配合集' : '暂无合集，创建第一个吧 →'}
                </p>
              ) : (
                filteredCollections.map(c => (
                  <div key={c.hash}
                    className={`px-4 py-3 border-b border-gray-800/50 ${selectMode ? 'cursor-pointer' : 'cursor-pointer'} hover:bg-gray-800 ${selectedColls.has(c.hash) ? 'bg-blue-900/30' : ''}`}
                    onClick={() => {
                      if (selectMode) { toggleCollSelect(c.hash); return; }
                      // Android式：点击合集 → 进入浏览
                      enterCollection(c.hash);
                    }}>
                    <div className="flex items-center gap-2">
                      {selectMode && <input type="checkbox" checked={selectedColls.has(c.hash)} readOnly className="shrink-0" />}
                      <span className="text-lg">📦</span>
                      <span className="text-blue-300 truncate flex-1 text-sm font-medium">{collDisplayName(c)}</span>
                    </div>
                    <div className="flex items-center gap-2 mt-1 ml-8">
                      {c.tags?.map(t => <span key={t} className="text-[10px] bg-blue-900/50 text-blue-300 px-1.5 py-0.5 rounded-full">{t}</span>)}
                      <span className="text-[10px] text-gray-600">{c.entry_count || 0} 文件</span>
                      <span className="text-[10px] text-gray-500">{c.created_at ? new Date(c.created_at).toLocaleDateString() : ''}</span>
                    </div>
                  </div>
                ))
              )}
            </>
          )}
        </div>
      </div>

      {/* SPLIT HANDLE */}
      <div className="w-1 bg-gray-700 hover:bg-blue-600 cursor-col-resize shrink-0 relative group" onMouseDown={onSplitMouseDown}>
        <div className="absolute inset-y-0 -left-1 -right-1" />
      </div>

      {/* ===== RIGHT PANEL — 文件源 + 编辑区 ===== */}
      <div style={{ width: `${100 - split}%` }} className="h-full flex flex-col">
        {/* 文件源标签栏 */}
        <div className="flex bg-gray-800 rounded mx-2 mt-2 shrink-0">
          <button onClick={() => setSrcTab('timeline')} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'timeline' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>🕐 时间线</button>
          <button onClick={() => setSrcTab('registered')} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'registered' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>📁 已注册</button>
          <button onClick={() => { setSrcTab('system'); setSysPath('/'); }} className={`flex-1 px-3 py-2 text-sm rounded ${srcTab === 'system' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>🖥️ 本机</button>
        </div>

        {/* 文件源过滤栏 */}
        {srcTab === 'timeline' && (
          <div className="p-2 border-b border-gray-800 shrink-0 space-y-1.5">
            <div className="flex gap-1">{SORT_OPTS.map(o => (<button key={o.v} onClick={() => setSort(o.v)} className={`flex-1 text-xs px-2 py-1 rounded ${sort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>))}</div>
            <div className="flex gap-1">{TYPE_OPTS.map(o => (<button key={o.v} onClick={() => setTypeF(o.v)} className={`flex-1 text-xs px-2 py-1 rounded ${typeF===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>))}</div>
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索文件名..." className="w-full bg-gray-800 text-xs px-3 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
            <span className="text-[10px] text-gray-600">{filtered.length} 个文件</span>
          </div>
        )}

        {srcTab === 'registered' && (
          <>
            <input value={search} onChange={e => setSearch(e.target.value)} placeholder="搜索文件路径..." className="w-full bg-gray-800 text-xs px-3 py-1.5 border-b border-gray-800 focus:outline-none focus:border-blue-600" />
            <span className="text-[10px] text-gray-600 px-3 py-1">{filtered.length} 个文件</span>
          </>
        )}

        {/* 文件源内容区（可滚动） */}
        <div className={`flex-1 overflow-y-auto ${srcTab === 'system' ? '' : 'max-h-[45%]'}`}>
          {/* TIMELINE */}
          {srcTab === 'timeline' && (
            filtered.length === 0 ? <p className="p-4 text-gray-600 text-xs">无匹配文件</p> :
            (() => {
              const sorted = [...filtered].sort((a,b) => (b.created_at||'').localeCompare(a.created_at||''));
              let lastDate = '';
              return sorted.map(f => {
                const d = f.created_at ? f.created_at.split('T')[0] : '';
                const showDate = d !== lastDate; lastDate = d;
                return <div key={f.hash}>
                  {showDate && <div className="px-4 py-2 text-[10px] text-gray-500 bg-gray-900/50 sticky top-0">{d}</div>}
                  <div draggable onDragStart={(e) => onDragStartFile(e, f)}
                    className="flex items-center gap-2 px-3 py-2 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                    <a href={api.getDownloadUrl(f.hash)} target="_blank" rel="noreferrer" className="text-lg">{fileIcon(f.mime_type)}</a>
                    <span className="text-blue-300 truncate flex-1 font-mono text-[11px]">{f.filename}</span>
                    <span className="text-gray-500 text-[10px] shrink-0">{fmtSize(f.size)}</span>
                    <button onClick={() => addEntry(f.hash,f.filename,f.mime_type,f.size)}
                      className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
                  </div>
                </div>;
              });
            })()
          )}

          {/* REGISTERED DIR */}
          {srcTab === 'registered' && (
            (() => {
              const prefix = (files.length > 0 && files[0].provider_path) ? '' : '';
              const dirs = new Set(); const localFiles = [];
              for (const f of filtered) {
                const rel = (f.provider_path || f.filename || '').replace(/^\//, '');
                const slash = rel.indexOf('/');
                if (slash === -1) localFiles.push(f);
                else if (rel.slice(0, slash)) dirs.add(rel.slice(0, slash));
              }
              if (dirs.size===0 && localFiles.length===0) return <p className="p-4 text-gray-600 text-xs">此目录为空</p>;
              return (
                <div>
                  {Array.from(dirs).sort().map(dir => (
                    <div key={dir} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                      <span className="text-lg">📁</span><span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
                    </div>
                  ))}
                  {localFiles.map(f => (
                    <div key={f.hash} draggable onDragStart={(e) => onDragStartFile(e, f)}
                      className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group">
                      <a href={api.getDownloadUrl(f.hash)} target="_blank" rel="noreferrer" className="text-lg">{fileIcon(f.mime_type)}</a>
                      <span className="text-blue-300 truncate flex-1 font-mono text-xs">{f.filename}</span>
                      <span className="text-gray-500 text-xs">{fmtSize(f.size)}</span>
                      <button onClick={() => addEntry(f.hash, f.filename, f.mime_type, f.size)}
                        className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
                    </div>
                  ))}
                </div>
              );
            })()
          )}

          {/* SYSTEM BROWSE */}
          {srcTab === 'system' && (
            <>
              <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 text-xs">
                <button onClick={() => { const p = sysPath.split('/'); p.pop(); setSysPath(p.join('/') || '/'); }} disabled={sysPath === '/'} className="text-gray-400 hover:text-white disabled:opacity-30">←</button>
                <span className="text-gray-300 font-mono text-xs truncate">{sysPath}</span>
              </div>
              {sysLoading ? <p className="p-4 text-gray-600 text-xs">加载中...</p> :
               sysEntries.length === 0 ? <p className="p-4 text-gray-600 text-xs">此目录为空</p> :
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
            </>
          )}
        </div>

        {/* 编辑区 — 合集草稿 */}
        <div className="border-t border-gray-700 flex-1 flex flex-col min-h-[200px]">
          {/* Toast 消息 */}
          {toastMsg && (
            <div className={`px-3 py-1 text-xs shrink-0 ${toastErr ? 'text-red-400 bg-red-500/10' : 'text-green-400 bg-green-500/10'}`}>{toastMsg}</div>
          )}

          {/* 顶部操作栏 */}
          <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-800 shrink-0 flex-wrap">
            <input value={fname} onChange={e => setFname(e.target.value)} placeholder="合集名称" className="bg-gray-800 text-xs px-2 py-1.5 rounded border border-gray-700 w-28 focus:outline-none focus:border-blue-500" />
            <input value={tags} onChange={e => setTags(e.target.value)} placeholder="标签: a, b" className="bg-gray-800 text-xs px-2 py-1.5 rounded border border-gray-700 w-24 focus:outline-none focus:border-blue-500" />
            <button onClick={async () => {
              if (!entries.length) return;
              try {
                const name = await llmSuggest(entries.slice(0, 20).map(e => e.path).join(', '));
                if (name) setFname(name);
              } catch(e) {}
            }} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1 rounded shrink-0" title="AI 推荐名称">🤖</button>
            <div className="flex-1" />
            <span className="text-[10px] text-gray-500">{entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value)).length} 个文件</span>
            <button onClick={() => handleSave(false)} disabled={saving || !entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value)).length}
              className="bg-green-600 hover:bg-green-700 disabled:opacity-40 text-sm px-3 py-1.5 rounded font-medium">💾 保存</button>
          </div>

          {/* 名称提示弹窗 */}
          {showNamePrompt && (
            <div className="flex items-center gap-2 px-3 py-1.5 bg-gray-800 border-b border-gray-700 shrink-0">
              <span className="text-xs text-gray-400">合集名称:</span>
              <button onClick={() => handleSave(true)} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1 rounded">🤖 AI 推荐</button>
              <button onClick={() => handleSave(false)} className="text-xs bg-gray-600 hover:bg-gray-500 px-2 py-1 rounded">留空</button>
              <button onClick={() => setShowNamePrompt(false)} className="text-xs text-gray-500 hover:text-white">✕</button>
            </div>
          )}

          {/* FileTree 编辑区 */}
          <div className="flex-1 overflow-hidden">
            <FileTree entries={entries} entryActions={entryActions} />
          </div>
        </div>
      </div>
    </div>
  );
}
