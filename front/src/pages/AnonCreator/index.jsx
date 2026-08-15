import React, { useContext, useState, useEffect, useMemo } from 'react';
import * as api from '../../api';
import { useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../../App';
import { loadSearchHistory } from './utils';
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
  const [leftSourceTab, setLeftSourceTab] = useState('local');
  const [leftSortKey, setLeftSortKey] = useState('created_at');
  const [leftSortOrder, setLeftSortOrder] = useState('desc');
  const [leftTypeFilters, setLeftTypeFilters] = useState([]);
  const [search, setSearch] = useState('');

  // ===== 合集管理 =====
  const [collSort, setCollSort] = useState('time');
  const [collSearch, setCollSearch] = useState('');
  const [collTagFilter, setCollTagFilter] = useState([]);
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
      // 坑：forkFrom 来自 Plaza 的 AnonCollectionSummary（/anon/collections），
      // summary 只有 hash/friendly_name/entry_count，没有 entries 字段 → 直接 setEntries(c.entries||[]) 永远是空合集。
      // 必须按 hash 拉取完整合集。
      setFname((c.friendly_name || '') + ' (副本)');
      const loadFork = async () => {
        try {
          const full = await api.getAnonCollection(c.hash);
          setEntries(full?.entries || []);
        } catch (e) { console.error('fork load failed:', e); }
      };
      loadFork();
      nav('/create', { replace: true });
    } else if (navState.draftFrom) {
      setEntries(navState.draftFrom.entries || []);
      setFname(navState.draftFrom.friendlyName || '');
      nav('/create', { replace: true });
    } else if (navState.editFrom) {
      const c = navState.editFrom;
      setEntries(c.entries || []);
      setFname(c.friendly_name || '');
      nav('/create', { replace: true });
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
      if (collTagFilter.length > 0 && !collTagFilter.some(f => (c.tags || []).some(t => t.toLowerCase().includes(f.toLowerCase())))) return false;
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
    // 坑：旧实现有 500ms lastClick 防抖，handleSysAddFolder/拖拽等程序化批量 add 会被静默丢弃
    //（walkDir 不重试，加了 N 个但实际只进 1-2 个）。改为在 reducer 内按 path+hash 去重，去掉时间闸。
    const providers = hash ? [{ type: "sha256", value: hash, mime_type: mime_type || '' }] : [];
    setEntries(prev => {
      const dup = prev.some(e =>
        e.path === path && (e.hash || e.providers?.[0]?.value) === hash
      );
      if (dup) return prev;
      return [...prev, { hash, path, providers, mime_type, size }];
    });
  };
  const removeEntry = (entry) => {
    setEntries(prev => prev.filter(e => {
      const eh = e.hash || e.providers?.[0]?.value;
      const th = entry.hash || entry.providers?.[0]?.value;
      return !(e.path === entry.path && eh === th);
    }));
  };
  const renameEntry = (oldPath, newPath) => {
    // 坑：旧实现只改 path 精确相等的条目 → 重命名文件夹（dir/ → newdir/）时
    // dir/a.txt 等子条目路径不变，合集内容损坏（孤儿条目浮到根部）。
    // 目录重命名必须把子路径前缀一并改掉。
    const isDir = oldPath.endsWith('/');
    const oldPrefix = isDir ? oldPath : oldPath + '/';
    setEntries(prev => prev.map(e => {
      if (e.path === oldPath) return { ...e, path: newPath };
      if (isDir && e.path.startsWith(oldPrefix)) return { ...e, path: newPath + e.path.slice(oldPath.length) };
      return e;
    }));
  };

  // 移动条目到目标目录（含目录本身：连带子条目）。用于 FileTree 树内拖拽/移动弹窗。
  // 坑：树内拖拽是"移动"语义（去源），不是 onDrop 的"添加"语义（复制）——
  // 旧实现拖进文件夹后源条目残留 = 重复副本。
  const moveEntry = (oldPath, targetDir) => {
    setEntries(prev => {
      const isDir = oldPath.endsWith('/');
      const base = oldPath.replace(/\/$/, '');
      const name = base.split('/').pop();
      const newPath = (targetDir ? targetDir + '/' : '') + name;
      const oldPrefix = isDir ? oldPath : oldPath + '/';
      // 防呆：目标是自己或自己子目录 → 拒绝，避免把目录拖进自己怀里
      if (targetDir && (targetDir === base || targetDir.startsWith(base + '/'))) {
        showToast('不能移动到自身或其子目录', true);
        return prev;
      }
      const dst = newPath + (isDir ? '/' : '');
      if (dst === oldPath) return prev;
      return prev.flatMap(e => {
        if (e.path === oldPath) return [{ ...e, path: dst }];
        if (isDir && e.path.startsWith(oldPrefix)) return [{ ...e, path: dst + e.path.slice(oldPath.length) }];
        return [e];
      });
    });
  };

  const handleFileAdd = (hash, path, mime_type, size) => {
    addEntry(hash, path, mime_type, size);
    showToast(`已添加: ${(path || '').split('/').pop()}`);
  };

  // 添加 URL 条目：可选择是否注册（下载文件获取 sha256）
  const handleAddUrl = async (url, filename, shouldRegister) => {
    const name = filename || url.split('/').pop() || 'url-file';
    if (shouldRegister) {
      try {
        const res = await api.registerURL(url, name);
        if (res?.hash) {
          addEntry(res.hash, name, res.mime_type || '', res.size || 0);
          showToast(`已注册 URL: ${name}`);
        }
      } catch (e) {
        showToast(`URL 注册失败: ${e.message}`, true);
      }
    } else {
      const providers = [{ type: "url", value: url }];
      setEntries(prev => [...prev, { hash: '', path: name, providers, mime_type: '', size: 0 }]);
      showToast(`已添加 URL: ${name}`);
    }
  };

  // 从本地电脑添加文件：先注册再添加
  const handleSysAddFile = async (sysPath, name, size) => {
    try {
      const res = await api.registerLocalFile(sysPath, name);
      if (res?.hash) {
        addEntry(res.hash, name, res.mime_type || '', size);
        showToast(`已添加: ${name}`);
      }
    } catch (e) {
      showToast(`注册失败: ${e.message}`, true);
    }
  };

  // 从本地电脑添加文件夹：递归列出文件后逐一注册添加
  const handleSysAddFolder = async (dirPath, dirName) => {
    let added = 0;
    const walkDir = async (path, prefix) => {
      let entries;
      try {
        entries = await api.browseDir(path);
      } catch { return; }
      if (!entries || entries.length === 0) return;
      for (const e of entries) {
        const relPath = prefix ? prefix + '/' + e.name : e.name;
        if (e.is_dir) {
          await walkDir(e.path, relPath);
        } else {
          try {
            const res = await api.registerLocalFile(e.path, relPath);
            if (res?.hash) {
              addEntry(res.hash, dirName + '/' + relPath, res.mime_type || '', e.size || 0);
              added++;
            }
          } catch {}
        }
      }
    };
    await walkDir(dirPath, '');
    if (added > 0) showToast(`已添加 ${added} 个文件到 ${dirName}/`);
    else showToast(`空文件夹已忽略: ${dirName}`, true);
  };

  // ===== 保存合集 =====
  const handleSave = async () => {
    const valid = entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value));
    if (!valid.length) { showToast('请先添加文件', true); return; }
    if (!fname.trim() && !showNamePrompt) {
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
    onMove: moveEntry,
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

  const handleSourceTabChange = (tab) => {
    setLeftSourceTab(tab);
    if (tab === 'local') setSysPath('/');
  };

  // 移动端面板切换
  const [mobilePanel, setMobilePanel] = useState('files');

  const handleFileSelect = (file) => {
    setSelectedFile(file);
  };

  // 选择文件后自动切换到预览面板（移动端）
  useEffect(() => {
    if (selectedFile && window.innerWidth < 768) {
      setMobilePanel('preview');
    }
  }, [selectedFile]);

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      {/* ── 移动端面板切换标签 ── */}
      <div className="md:hidden sticky top-0 z-10 bg-gray-950/95 backdrop-blur border-b border-gray-800">
        <div className="flex p-1 gap-1">
          {[
            { id: 'files', label: '📁 文件', count: files.length },
            { id: 'preview', label: '👁 预览' },
            { id: 'editor', label: '✏️ 编辑器', count: entries.length },
          ].map(tab => (
            <button key={tab.id} onClick={() => setMobilePanel(tab.id)}
              className={`flex-1 text-xs py-2 rounded transition-colors ${
                mobilePanel === tab.id
                  ? 'bg-blue-600 text-white font-medium'
                  : 'text-gray-400 hover:text-white hover:bg-gray-800'
              }`}>
              {tab.label}{tab.count !== undefined ? ` (${tab.count})` : ''}
            </button>
          ))}
        </div>
      </div>

      {/* 左列：筛选 + 文件列表 */}
      <div className={`w-[380px] shrink-0 flex-col overflow-hidden border-r border-gray-800 ${
        mobilePanel === 'files' ? 'flex' : 'hidden'
      } md:flex`}>
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
          onSysNav={setSysPath} onSysAdd={addEntry} onSysAddFile={handleSysAddFile} onSysAddFolder={handleSysAddFolder}
          onSearchHistorySelect={(q) => { setCollSearch(q); }}
          onSearchHistoryUpdate={setSearchHistory}
        />
      </div>

      {/* 中列：文件预览 */}
      <div className={`flex-1 flex-col overflow-hidden bg-gray-950 ${
        mobilePanel === 'preview' ? 'flex' : 'hidden'
      } md:flex`}>
        <MiddlePanel selectedFile={selectedFile} />
      </div>

      {/* 右列：编辑器 */}
      <div className={`w-[420px] shrink-0 border-l border-gray-800 bg-gray-900 flex-col overflow-hidden ${
        mobilePanel === 'editor' ? 'flex' : 'hidden'
      } md:flex`}>
        <RightPanel
          fname={fname} tags={tags} entries={entries} saving={saving}
          showNamePrompt={showNamePrompt} toastMsg={toastMsg} toastErr={toastErr}
          entryActions={entryActions}
          onFname={setFname} onTags={setTags} onSave={handleSave}
          onAddUrl={handleAddUrl}
          onCloseNamePrompt={() => setShowNamePrompt(false)}
        />
      </div>
    </div>
  );
}
