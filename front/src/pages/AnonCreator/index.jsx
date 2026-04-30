import React, { useContext, useState, useEffect, useRef, useMemo } from 'react';
import * as api from '../../api';
import { useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../../App';
import { llmSuggest, loadSearchHistory } from './utils';
import LeftPanel from './LeftPanel';
import MiddlePanel from './MiddlePanel';
import RightPanel from './RightPanel';

export default function AnonCreator() {
  const nav = useNavigate();
  const { setPageContext } = useContext(PageContext);
  const navState = useLocation().state || {};

  // ===== 数据 =====
  const [files, setFiles] = useState([]);
  const [fLoading, setFLoading] = useState(true);
  const [collections, setCollections] = useState([]);

  // ===== 左侧筛选 =====
  const [leftSourceTab, setLeftSourceTab] = useState('all');
  const [leftSortKey, setLeftSortKey] = useState('created_at');
  const [leftSortOrder, setLeftSortOrder] = useState('desc');
  const [leftTypeFilters, setLeftTypeFilters] = useState([]);
  const [search, setSearch] = useState('');

  // ===== 合集管理 =====
  const [collSort, setCollSort] = useState('time');
  const [collSearch, setCollSearch] = useState('');
  const [collTagFilter, setCollTagFilter] = useState('');
  const [enteredColl, setEnteredColl] = useState(null);
  const [enteredCollFiles, setEnteredCollFiles] = useState(null);
  const [collViewPath, setCollViewPath] = useState('');

  // ===== 编辑器 =====
  const [fname, setFname] = useState('');
  const [tags, setTags] = useState('');
  const [entries, setEntries] = useState([]);
  const [saving, setSaving] = useState(false);
  const [showNamePrompt, setShowNamePrompt] = useState(false);
  const [toastMsg, setToastMsg] = useState('');
  const [toastErr, setToastErr] = useState(false);
  const lastClick = useRef(0);

  // ===== 选择模式 =====
  const [selectMode, setSelectMode] = useState(false);
  const [selectedColls, setSelectedColls] = useState(new Set());
  const [selectedFiles, setSelectedFiles] = useState(new Set());

  // ===== 搜索历史 =====
  const [searchHistory, setSearchHistory] = useState(loadSearchHistory());

  // ===== 系统浏览 =====
  const [sysPath, setSysPath] = useState('/');
  const [sysEntries, setSysEntries] = useState([]);
  const [sysLoading, setSysLoading] = useState(false);

  // ===== 预览选中 =====
  const [selectedFile, setSelectedFile] = useState(null);

  // ===== 副作用 =====
  useEffect(() => { loadFiles(); loadCollections(); }, []);
  useEffect(() => {
    if (leftSourceTab === 'local') {
      setSysLoading(true);
      api.browseDir(sysPath).then(res => {
        res.sort((a, b) => (a.is_dir === b.is_dir) ? a.name.localeCompare(b.name) : (a.is_dir ? -1 : 1));
        setSysEntries(res);
      }).catch(() => setSysEntries([])).finally(() => setSysLoading(false));
    }
  }, [leftSourceTab, sysPath]);
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

  // ===== 数据加载 =====
  const loadFiles = async () => {
    setFLoading(true);
    try { setFiles(await api.listFiles('time') || []); } catch { setFiles([]); }
    setFLoading(false);
  };
  const loadCollections = async () => {
    try { setCollections(await api.listAnonCollections() || []); } catch { setCollections([]); }
  };

  // ===== 过滤 & 排序 =====
  const filteredFiles = useMemo(() => {
    let result = [...files];

    // 来源过滤
    if (leftSourceTab === 'registered') {
      result = result.filter(f => f.providers?.length > 0);
    }

    // 文本搜索（仅 all / registered 模式）
    if (search && (leftSourceTab === 'all' || leftSourceTab === 'registered')) {
      const q = search.toLowerCase();
      result = result.filter(f => (f.filename || '').toLowerCase().includes(q));
    }

    // 类型筛选（多选）
    if (leftTypeFilters.length > 0 && (leftSourceTab === 'all' || leftSourceTab === 'registered')) {
      result = result.filter(f => {
        const mime = f.mime_type || '';
        const ext = (f.filename || '').split('.').pop()?.toLowerCase() || '';
        if (leftTypeFilters.includes('document')) {
          if (mime === 'application/pdf' || ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx'].includes(ext)) return true;
        }
        return leftTypeFilters.some(t => mime.startsWith(t));
      });
    }

    // 排序
    result.sort((a, b) => {
      let cmp = 0;
      switch (leftSortKey) {
        case 'name': cmp = (a.filename || '').localeCompare(b.filename || ''); break;
        case 'size': cmp = (a.size || 0) - (b.size || 0); break;
        case 'modified_at': cmp = (a.modified_at || '').localeCompare(b.modified_at || ''); break;
        default: cmp = (a.created_at || '').localeCompare(b.created_at || ''); break;
      }
      return leftSortOrder === 'asc' ? cmp : -cmp;
    });

    return result;
  }, [files, leftSourceTab, search, leftTypeFilters, leftSortKey, leftSortOrder]);

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

  // ===== 提示 =====
  const showToast = (msg, err = false) => {
    setToastMsg(msg); setToastErr(err); setTimeout(() => setToastMsg(''), 3000);
  };

  // ===== 合集浏览 =====
  const enterCollection = async (hash) => {
    try {
      const c = await api.getAnonCollection(hash);
      if (!c?.entries?.length) { showToast('空合集', true); return; }
      setEnteredColl(c);
      setEnteredCollFiles(c.entries || []);
      setCollViewPath('');
    } catch { showToast('无法加载合集', true); }
  };

  const leaveCollection = () => {
    setEnteredColl(null);
    setEnteredCollFiles(null);
    setCollViewPath('');
    setSelectMode(false);
    setSelectedColls(new Set());
    setSelectedFiles(new Set());
  };

  const navIntoDir = (dir) => setCollViewPath(prev => prev ? `${prev}/${dir}` : dir);

  // ===== 条目操作 =====
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

  const handleFileAdd = (hash, path, mime_type, size) => {
    addEntry(hash, path, mime_type, size);
    showToast(`已添加: ${(path || '').split('/').pop()}`);
  };

  // ===== 保存合集 =====
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

  // ===== 选择切换 =====
  const toggleCollSelect = (hash) => {
    setSelectedColls(prev => { const n = new Set(prev); n.has(hash) ? n.delete(hash) : n.add(hash); return n; });
  };
  const toggleFileSelect = (path) => {
    setSelectedFiles(prev => { const n = new Set(prev); n.has(path) ? n.delete(path) : n.add(path); return n; });
  };
  const toggleSelectMode = () => {
    setSelectMode(!selectMode);
    setSelectedColls(new Set());
    setSelectedFiles(new Set());
  };

  // ===== 拖拽 =====
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

  // ===== 条目操作集合 =====
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

  const handleAiName = async () => {
    try {
      const names = entries.slice(0, 20).map(e => e.path).join(', ');
      const name = await llmSuggest(names);
      if (name) setFname(name);
    } catch {}
  };

  const handleSourceTabChange = (tab) => {
    setLeftSourceTab(tab);
    if (tab === 'local') setSysPath('/');
  };

  const handleFileSelect = (file) => {
    setSelectedFile(file);
  };

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      {/* 左列：筛选 + 文件列表 */}
      <div className="w-[380px] shrink-0 flex flex-col overflow-hidden border-r border-gray-800">
        <LeftPanel
          sourceTab={leftSourceTab} sortKey={leftSortKey} sortOrder={leftSortOrder}
          typeFilters={leftTypeFilters} search={search}
          files={files} filteredFiles={filteredFiles}
          collections={collections} filteredCollections={filteredCollections}
          collSearch={collSearch} collTagFilter={collTagFilter} collSort={collSort}
          allCollTags={allCollTags}
          enteredColl={enteredColl} collViewPath={collViewPath}
          enteredCollFiles={enteredCollFiles}
          selectMode={selectMode} selectedColls={selectedColls}
          selectedFiles={selectedFiles}
          sysPath={sysPath} sysEntries={sysEntries} sysLoading={sysLoading}
          searchHistory={searchHistory}
          onSourceTab={handleSourceTabChange}
          onSortKey={setLeftSortKey}
          onSortOrder={() => setLeftSortOrder(prev => prev === 'asc' ? 'desc' : 'asc')}
          onTypeFilter={setLeftTypeFilters}
          onSearch={setSearch}
          onFileSelect={handleFileSelect}
          onFileAdd={handleFileAdd}
          onDragStart={onDragStartFile}
          onCollSearch={setCollSearch} onCollTag={setCollTagFilter}
          onCollSort={setCollSort}
          onEnterColl={enterCollection} onLeaveColl={leaveCollection}
          onPathNav={setCollViewPath} onNavIntoDir={navIntoDir}
          onSaveToNode={saveCollToNode}
          onSelectToggle={toggleSelectMode}
          onToggleCollSelect={toggleCollSelect}
          onToggleFileSelect={toggleFileSelect}
          onBatchSaveColls={batchSaveColls}
          onBatchSaveFiles={batchSaveFiles}
          onSysNav={setSysPath} onSysAdd={addEntry}
          onSearchHistorySelect={(q) => { setCollSearch(q); }}
          onSearchHistoryUpdate={setSearchHistory}
        />
      </div>

      {/* 中列：文件预览 */}
      <div className="flex-1 flex flex-col overflow-hidden bg-gray-950">
        <MiddlePanel selectedFile={selectedFile} />
      </div>

      {/* 右列：编辑器 */}
      <div className="w-[420px] shrink-0 border-l border-gray-800 bg-gray-900 flex flex-col overflow-hidden">
        <RightPanel
          fname={fname} tags={tags} entries={entries} saving={saving}
          showNamePrompt={showNamePrompt} toastMsg={toastMsg} toastErr={toastErr}
          entryActions={entryActions}
          onFname={setFname} onTags={setTags} onSave={handleSave}
          onAiName={handleAiName}
          onCloseNamePrompt={() => setShowNamePrompt(false)}
        />
      </div>
    </div>
  );
}
