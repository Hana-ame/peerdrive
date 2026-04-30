// 文件管理器：浏览/搜索/筛选注册文件，支持多选创建合集
// 支持三种浏览模式：时间线、本机目录、数据目录
import React, { useContext, useState, useEffect, useMemo, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageContext } from '../App';
import * as api from '../api';

const CATEGORIES = [
  { key: '', label: '全部', icon: '📋' },
  { key: 'image/', label: '图片', icon: '🖼️' },
  { key: 'video/', label: '视频', icon: '🎬' },
  { key: 'audio/', label: '音频', icon: '🎵' },
  { key: 'document', label: '文档', icon: '📝' },
  { key: 'archive', label: '压缩包', icon: '📦' },
];

const SORT_KEYS = [
  { key: 'time', label: '时间' },
  { key: 'name', label: '名称' },
  { key: 'size', label: '大小' },
  { key: 'type', label: '类型' },
];

const BROWSE_MODES = [
  { key: 'timeline', label: '时间线', icon: '🕐' },
  { key: 'localDir', label: '本机目录', icon: '💻' },
  { key: 'dbDir', label: '数据目录', icon: '🗄️' },
];

// Group files by date category: 今天 / 昨天 / 本周 / 更早
function groupByDate(files) {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const dayOfWeek = today.getDay(); // 0=Sun
  const monday = new Date(today);
  monday.setDate(monday.getDate() - (dayOfWeek === 0 ? 6 : dayOfWeek - 1));

  const groups = { '今天': [], '昨天': [], '本周': [], '更早': [] };
  const order = ['今天', '昨天', '本周', '更早'];

  for (const file of files) {
    if (!file.created_at) { groups['更早'].push(file); continue; }
    const d = new Date(file.created_at);
    const fileDay = new Date(d.getFullYear(), d.getMonth(), d.getDate());
    if (fileDay.getTime() === today.getTime()) groups['今天'].push(file);
    else if (fileDay.getTime() === yesterday.getTime()) groups['昨天'].push(file);
    else if (fileDay >= monday) groups['本周'].push(file);
    else groups['更早'].push(file);
  }

  // Return only non-empty groups in order
  return order.filter(k => groups[k].length > 0).map(k => ({ label: k, files: groups[k] }));
}

// Extract subdirectories and files at a given virtual path prefix from the DB file list
function getDbDirContents(files, prefix) {
  const subdirs = new Set();
  const filesHere = [];
  const normalizedPrefix = prefix ? (prefix.endsWith('/') ? prefix : prefix + '/') : '';

  for (const f of files) {
    const path = f.provider_path || f.filename || '';
    if (!path || path === '/') continue;
    const cleanPath = path.startsWith('/') ? path.slice(1) : path;

    // If we have a prefix, only include files under it
    if (normalizedPrefix && !cleanPath.startsWith(normalizedPrefix)) continue;

    const relative = normalizedPrefix ? cleanPath.slice(normalizedPrefix.length) : cleanPath;
    if (!relative) continue;

    const parts = relative.split('/');
    if (parts.length === 1) {
      // Direct file at this level
      filesHere.push(f);
    } else {
      // File is inside a subdirectory
      subdirs.add(parts[0]);
    }
  }

  return {
    subdirs: [...subdirs].sort(),
    files: filesHere,
  };
}

export default function FileManager() {
  const { setPageContext } = useContext(PageContext);
  const navigate = useNavigate();

  // ---- core state ----
  const [files, setFiles] = useState([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState({});
  const [search, setSearch] = useState('');
  const [sortBy, setSortBy] = useState('time');
  const [category, setCategory] = useState('');
  const [viewMode, setViewMode] = useState('list');
  const [browseMode, setBrowseMode] = useState('timeline');

  // ---- local directory browsing (main view) ----
  const [localDirPath, setLocalDirPath] = useState('/');
  const [localDirEntries, setLocalDirEntries] = useState([]);
  const [localDirLoading, setLocalDirLoading] = useState(false);
  const [localSelected, setLocalSelected] = useState({});

  // ---- DB directory browsing (main view) ----
  const [dbDirPath, setDbDirPath] = useState('');

  // ---- add-file modal state ----
  const [expanded, setExpanded] = useState({});
  const [showFileBrowser, setShowFileBrowser] = useState(false);
  const [currentDir, setCurrentDir] = useState('/');
  const [dirEntries, setDirEntries] = useState([]);
  const [selectedFiles, setSelectedFiles] = useState({});
  const [browseLoading, setBrowseLoading] = useState(false);

  // URL registration
  const [addFileTab, setAddFileTab] = useState('local');
  const [urlInput, setUrlInput] = useState('');
  const [urlFilename, setUrlFilename] = useState('');
  const [urlResult, setUrlResult] = useState(null);
  const [urlLoading, setUrlLoading] = useState(false);
  const [urlError, setUrlError] = useState('');

  const [swarmData, setSwarmData] = useState({});
  const [notification, setNotification] = useState('');

  // ---- effects ----
  useEffect(() => { loadFiles(); }, []);

  // Load local dir when entering localDir mode
  useEffect(() => {
    if (browseMode === 'localDir') {
      loadLocalDir(localDirPath === '/' && localDirEntries.length === 0 ? '/' : localDirPath);
    }
  }, [browseMode]);

  // Force sort to 'time' in timeline mode; clear selections on mode switch
  useEffect(() => {
    if (browseMode === 'timeline' && sortBy !== 'time') {
      setSortBy('time');
    }
    setSelected({});
    setLocalSelected({});
  }, [browseMode]);

  useEffect(() => {
    setPageContext({ type: 'fileManager', fileCount: files.length, sortBy, category });
  }, [files, sortBy, category]);

  useEffect(() => {
    if (notification) {
      const t = setTimeout(() => setNotification(''), 3000);
      return () => clearTimeout(t);
    }
  }, [notification]);

  // Load swarm data (peer counts per file hash)
  useEffect(() => {
    (async () => {
      try {
        const discovered = await api.getP2PDiscovered();
        const counts = {};
        if (Array.isArray(discovered)) {
          for (const peer of discovered) {
            const peerHashes = peer.hashes || peer.files || [];
            if (Array.isArray(peerHashes)) {
              for (const h of peerHashes) {
                const hash = typeof h === 'string' ? h : (h.hash || h);
                counts[hash] = (counts[hash] || 0) + 1;
              }
            }
          }
        }
        setSwarmData(counts);
      } catch (e) {
        console.debug('Swarm data not available');
      }
    })();
  }, []);

  const [loadingProgress, setLoadingProgress] = useState('');

  // Load file list from the DB
  const loadFiles = async () => {
    setLoading(true);
    setLoadingProgress('加载中...');
    const start = Date.now();
    try {
      const data = await api.listFiles('path');
      const elapsed = ((Date.now() - start) / 1000).toFixed(1);
      setFiles(data || []);
      setLoadingProgress(`已加载 ${data?.length || 0} 个文件 (${elapsed}s)`);
    } catch (e) { console.error(e); setFiles([]); setLoadingProgress(''); }
    setLoading(false);
  };

  // Load local filesystem directory via backend API
  const loadLocalDir = async (path) => {
    setLocalDirLoading(true);
    try {
      const entries = await api.browseDir(path);
      entries.sort((a, b) => {
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });
      setLocalDirEntries(entries);
      setLocalDirPath(path);
    } catch (e) {
      console.error('Browse failed:', e);
      setLocalDirEntries([]);
    }
    setLocalDirLoading(false);
  };

  const toggleFile = (hash) => {
    setSelected(prev => {
      const next = { ...prev };
      next[hash] = !next[hash];
      return next;
    });
  };

  const selectAllFiltered = () => {
    const next = { ...selected };
    for (const f of filtered) {
      next[f.hash] = selectedHashes.length === filtered.length ? false : true;
    }
    setSelected(next);
  };

  // ---- filtering and sorting ----
  const filtered = useMemo(() => {
    let result = [...files];
    if (search) {
      const q = search.toLowerCase();
      result = result.filter(f => f.filename?.toLowerCase().includes(q));
    }
    if (category) {
      if (category === 'document') {
        result = result.filter(f => {
          const m = f.mime_type || '';
          return m.startsWith('text/') || m.includes('pdf') || m.includes('document') || m.includes('spreadsheet') || m.includes('presentation');
        });
      } else if (category === 'archive') {
        result = result.filter(f => {
          const m = f.mime_type || '';
          return m.includes('zip') || m.includes('tar') || m.includes('gzip') || m.includes('rar') || m.includes('7z') || m.includes('compress');
        });
      } else {
        result = result.filter(f => (f.mime_type || '').startsWith(category));
      }
    }
    result.sort((a, b) => {
      switch (sortBy) {
        case 'name': return (a.filename || '').localeCompare(b.filename || '');
        case 'size': return (b.size || 0) - (a.size || 0);
        case 'type': return (a.mime_type || '').localeCompare(b.mime_type || '');
        default: return (b.created_at || '').localeCompare(a.created_at || '');
      }
    });
    return result;
  }, [files, search, category, sortBy]);

  const selectedHashes = Object.keys(selected).filter(k => selected[k]);
  const selCount = selectedHashes.length;
  const selectedEntries = files.filter(f => selected[f.hash]).map(f => ({ path: f.filename, hash: f.hash }));

  const totalSize = files.reduce((s, f) => s + (f.size || 0), 0);

  const handleCreateCollection = () => {
    if (selectedEntries.length === 0) return alert('请先选择文件');
    const name = selectedEntries[0].path || 'collection';
    navigate('/create', { state: { draftFrom: { entries: selectedEntries, friendlyName: name } } });
    setSelected({});
  };

  const handleCreateFromFile = (file) => {
    navigate('/create', { state: { draftFrom: { entries: [{ path: file.filename, hash: file.hash }], friendlyName: file.filename } } });
  };

  const handleDeleteSelected = async () => {
    if (!confirm(`确定永久删除 ${selCount} 个文件？`)) return;
    for (const hash of selectedHashes) {
      try {
        await api.deleteFile(hash);
      } catch (e) {
        console.error(`删除失败: ${hash}`, e);
      }
    }
    setSelected({});
    loadFiles();
  };

  // ---- add-file modal functions (kept as-is) ----
  const browseDir = async (dir) => {
    setBrowseLoading(true);
    try {
      const entries = await api.browseDir(dir);
      entries.sort((a, b) => {
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });
      setDirEntries(entries);
      setCurrentDir(dir);
    } catch (e) { alert(`目录浏览失败: ${e.message}`); }
    setBrowseLoading(false);
  };

  const handleOpenFileBrowser = () => {
    setShowFileBrowser(true);
    setSelectedFiles({});
    browseDir('/');
  };

  const handleRegEnter = (entry) => {
    if (entry.is_dir) {
      browseDir(entry.path);
    } else {
      setSelectedFiles(prev => ({ ...prev, [entry.path]: !prev[entry.path] }));
    }
  };

  const handleRegParent = () => {
    const parent = currentDir.split('/').slice(0, -1).join('/') || '/';
    browseDir(parent);
  };

  const handleRegisterSelected = async () => {
    const paths = Object.keys(selectedFiles).filter(k => selectedFiles[k]);
    if (paths.length === 0) return alert('请先选择文件');
    const entries = [];
    for (const path of paths) {
      try {
        const filename = path.split('/').pop();
        const res = await api.registerLocalFile(path, filename);
        entries.push({ path: filename, hash: res.hash });
      } catch (e) { alert(`注册失败: ${path}: ${e.message}`); return; }
    }
    setShowFileBrowser(false);
    setSelectedFiles({});
    loadFiles();
  };

  const handleRegisterCurrentFolder = async () => {
    try {
      const res = await api.registerFolder(currentDir);
      const registered = res.registered || [];
      setShowFileBrowser(false);
      if (registered.length > 0) {
        const entries = registered.map(r => ({ path: r.filename, hash: r.hash }));
        const name = currentDir.split('/').pop() || 'collection';
        const coll = await api.createAnonCollection(entries, name);
        navigate(`/anon/collections/${coll.hash}`);
        loadFiles();
        return;
      }
      alert(`注册完成: ${registered.length} 个文件`);
      loadFiles();
    } catch (e) { alert(`注册失败: ${e.message}`); }
  };

  const handleRegisterUrl = async () => {
    const url = urlInput.trim();
    if (!url) return;
    setUrlLoading(true);
    setUrlResult(null);
    setUrlError('');
    try {
      const result = await api.registerURL(url, urlFilename.trim());
      setUrlResult(result);
      setUrlInput('');
      setUrlFilename('');
      loadFiles();
    } catch (e) {
      setUrlError(e.message);
    }
    setUrlLoading(false);
  };

  // ---- local dir mode navigation ----
  const handleLocalDirEnter = (entry) => {
    if (entry.is_dir) {
      loadLocalDir(entry.path);
    } else {
      setLocalSelected(prev => ({ ...prev, [entry.path]: !prev[entry.path] }));
    }
  };

  const handleLocalDirUp = () => {
    if (localDirPath === '/') return;
    const parent = localDirPath.split('/').slice(0, -1).join('/') || '/';
    loadLocalDir(parent);
  };

  const handleBreadcrumbNav = (path) => {
    loadLocalDir(path);
  };

  // Register selected files from local dir mode, then optionally create a collection
  const handleLocalRegisterAndCreate = async () => {
    const paths = Object.keys(localSelected).filter(k => localSelected[k]);
    if (paths.length === 0) return alert('请先选择文件');
    const entries = [];
    for (const path of paths) {
      try {
        const filename = path.split('/').pop();
        const res = await api.registerLocalFile(path, filename);
        entries.push({ path: filename, hash: res.hash });
      } catch (e) { alert(`注册失败: ${path}: ${e.message}`); return; }
    }
    setLocalSelected({});
    setNotification(`已注册 ${entries.length} 个文件`);
    loadFiles();
    // Navigate to collection creation
    navigate('/create', { state: { draftFrom: { entries, friendlyName: entries[0]?.path || 'collection' } } });
  };

  const handleLocalRegisterOnly = async () => {
    const paths = Object.keys(localSelected).filter(k => localSelected[k]);
    if (paths.length === 0) return alert('请先选择文件');
    let count = 0;
    for (const path of paths) {
      try {
        const filename = path.split('/').pop();
        await api.registerLocalFile(path, filename);
        count++;
      } catch (e) { alert(`注册失败: ${path}: ${e.message}`); return; }
    }
    setLocalSelected({});
    setNotification(`已注册 ${count} 个文件`);
    loadFiles();
  };

  // Select/deselect all at the current local directory level
  const handleLocalSelectAll = () => {
    const filesOnly = localDirEntries.filter(e => !e.is_dir);
    if (filesOnly.length === 0) return;
    const allSelected = filesOnly.every(e => localSelected[e.path]);
    const next = { ...localSelected };
    for (const e of filesOnly) {
      if (allSelected) delete next[e.path];
      else next[e.path] = true;
    }
    setLocalSelected(next);
  };

  // ---- DB dir mode navigation ----
  const handleDbDirEnter = (subdir) => {
    const newPath = dbDirPath ? `${dbDirPath}/${subdir}` : subdir;
    setDbDirPath(newPath);
    setSelected({});
  };

  const handleDbDirUp = () => {
    if (!dbDirPath) return;
    const parts = dbDirPath.split('/').filter(Boolean);
    parts.pop();
    setDbDirPath(parts.join('/'));
    setSelected({});
  };

  const handleDbBreadcrumbNav = (path) => {
    setDbDirPath(path);
    setSelected({});
  };

  const localSelCount = Object.keys(localSelected).filter(k => localSelected[k]).length;

  // ---- formatting helpers ----
  const formatSize = (bytes) => {
    if (!bytes) return '-';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
    if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB';
    return (bytes / 1073741824).toFixed(1) + ' GB';
  };

  const extIcon = (mime) => {
    if (!mime) return '📄';
    if (mime.startsWith('image/')) return '🖼️';
    if (mime.startsWith('video/')) return '🎬';
    if (mime.startsWith('audio/')) return '🎵';
    if (mime.startsWith('text/')) return '📝';
    if (mime.includes('pdf')) return '📕';
    if (mime.includes('zip') || mime.includes('tar') || mime.includes('gzip')) return '📦';
    return '📄';
  };

  const getProviderBadge = (file) => {
    const pt = file.provider_type || '';
    if (pt === 'local' || (!pt && file.provider_path?.includes('/storage/'))) {
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-green-900/50 text-green-300 border border-green-800/50 mr-2 shrink-0">
          🟢 本地
        </span>
      );
    }
    if (pt === 'p2p') {
      return (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-blue-900/50 text-blue-300 border border-blue-800/50 mr-2 shrink-0">
          🔵 P2P
        </span>
      );
    }
    return null;
  };

  // ---- existing tree building / rendering ----
  const buildTree = (files) => {
    const root = { name: '/', dirs: {}, files: [] };
    for (const f of files) {
      const pp = f.provider_path || f.filename || '';
      const idx = pp.indexOf('/storage/');
      const rel = idx >= 0 ? pp.slice(idx) : pp;
      const parts = rel.replace(/^\//, '').split('/');
      const filename = parts.pop();
      let node = root;
      for (const part of parts) {
        if (!node.dirs[part]) node.dirs[part] = { name: part, dirs: {}, files: [] };
        node = node.dirs[part];
      }
      node.files.push(f);
    }
    return root;
  };

  const collectAllHashes = (node) => {
    const hashes = node.files.map(f => f.hash);
    for (const name of Object.keys(node.dirs)) {
      hashes.push(...collectAllHashes(node.dirs[name]));
    }
    return hashes;
  };

  const renderTree = (node, depth) => {
    const sortedDirs = Object.keys(node.dirs).sort();
    return sortedDirs.map(name => {
      const dir = node.dirs[name];
      const isExpanded = expanded[name];
      const allHashes = collectAllHashes(dir);
      const dirSelected = allHashes.length > 0 && allHashes.every(h => selected[h]);
      return (
        <div key={name}>
          <div className="flex items-center py-2 px-2 rounded hover:bg-gray-800/50 group" style={{ marginLeft: `${depth * 20}px` }}>
            <span onClick={() => setExpanded(prev => ({ ...prev, [name]: !prev[name] }))} className="mr-1.5 w-5 text-center text-gray-400 shrink-0 cursor-pointer select-none">
              {isExpanded ? '▾' : '▸'}
            </span>
            <input
              type="checkbox" checked={dirSelected}
              onChange={() => {
                const next = { ...selected };
                for (const h of allHashes) next[h] = !dirSelected;
                setSelected(next);
              }}
              className="rounded mr-2 shrink-0 accent-cyan-500 w-4 h-4"
            />
            <span className="mr-2 text-base shrink-0">📁</span>
            <span className="text-yellow-400 text-sm truncate flex-1">{name}</span>
            <span className="text-xs text-gray-600 mr-3">{allHashes.length} 个</span>
            <button
              onClick={() => handleCreateFromDir(name, dir.files)}
              className="hidden group-hover:inline-block bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded text-[10px] shrink-0"
            >创建合集</button>
          </div>
          {isExpanded && (
            <div>
              {dir.files.length > 0 && dir.files.map(f => (
                <div key={f.hash} className="flex items-center py-2 px-2 rounded hover:bg-gray-800/50 group" style={{ marginLeft: `${(depth + 1) * 20 + 22}px` }}>
                  <input type="checkbox" checked={!!selected[f.hash]} onChange={() => toggleFile(f.hash)} className="rounded mr-2 shrink-0 accent-cyan-500 w-4 h-4" />
                  <span className="mr-2 text-base shrink-0">{extIcon(f.mime_type)}</span>
                  {getProviderBadge(f)}
                  <span className="text-sm text-blue-300 truncate flex-1">{f.filename}</span>
                  <span className="text-xs text-gray-500 w-16 text-right shrink-0 mr-3">{formatSize(f.size)}</span>
                  <button
                    onClick={() => handleCreateFromFile(f)}
                    className="hidden group-hover:inline-block bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded text-[10px] shrink-0"
                  >创建合集</button>
                </div>
              ))}
              {renderTree(dir, depth + 1)}
            </div>
          )}
        </div>
      );
    });
  };

  const handleCreateFromDir = (dirName, dirFiles) => {
    const entries = dirFiles.map(f => ({ path: f.filename, hash: f.hash }));
    navigate('/create', { state: { draftFrom: { entries, friendlyName: dirName } } });
  };

  const tree = buildTree(files);

  const categoryCounts = useMemo(() => {
    const counts = {};
    for (const f of files) {
      const m = f.mime_type || '';
      if (m.startsWith('image/')) counts['image/'] = (counts['image/'] || 0) + 1;
      else if (m.startsWith('video/')) counts['video/'] = (counts['video/'] || 0) + 1;
      else if (m.startsWith('audio/')) counts['audio/'] = (counts['audio/'] || 0) + 1;
      else if (m.startsWith('text/') || m.includes('pdf') || m.includes('document')) counts['document'] = (counts['document'] || 0) + 1;
      else if (m.includes('zip') || m.includes('tar') || m.includes('gzip')) counts['archive'] = (counts['archive'] || 0) + 1;
      else counts['other'] = (counts['other'] || 0) + 1;
    }
    return counts;
  }, [files]);

  // ---- breadcrumb component ----
  const renderBreadcrumb = (path, onNavigate) => {
    const parts = path.split('/').filter(Boolean);
    const crumbs = [{ name: '根目录', path: '/' }];
    let accumulated = '';
    for (const part of parts) {
      accumulated += '/' + part;
      crumbs.push({ name: part, path: accumulated });
    }
    return (
      <div className="flex items-center space-x-1.5 text-sm flex-wrap">
        {crumbs.map((crumb, i) => (
          <React.Fragment key={crumb.path}>
            {i > 0 && <span className="text-gray-600 select-none">/</span>}
            <button
              onClick={() => onNavigate(crumb.path)}
              className={`hover:underline transition-colors ${
                i === crumbs.length - 1
                  ? 'text-blue-300 font-medium'
                  : 'text-gray-400 hover:text-blue-300'
              }`}
            >
              {crumb.name}
            </button>
          </React.Fragment>
        ))}
      </div>
    );
  };

  // ---- timeline view renderer ----
  const renderTimelineView = () => {
    if (filtered.length === 0) {
      return files.length === 0 ? (
        <div className="mx-4 md:mx-6 my-6">
          <div className="flex flex-col items-center justify-center py-12 md:py-20 px-4 border-2 border-dashed border-gray-700 rounded-xl bg-gray-800/20">
            <div className="text-5xl md:text-6xl mb-4 opacity-30">📂</div>
            <p className="text-gray-400 text-base md:text-lg mb-2 text-center">拖拽文件到这里或点击添加</p>
            <p className="text-gray-600 text-xs md:text-sm mb-6 text-center">支持从本地文件系统浏览和注册文件</p>
            <button onClick={handleOpenFileBrowser} className="bg-indigo-600 hover:bg-indigo-500 px-5 md:px-6 py-2.5 rounded-lg text-sm font-medium transition-colors">
              + 添加文件
            </button>
          </div>
        </div>
      ) : (
        <div className="mx-4 md:mx-6 my-6">
          <div className="text-center text-gray-500 py-16 md:py-20 px-4 border-2 border-dashed border-gray-700 rounded-xl">
            <p className="text-gray-400 text-base md:text-lg mb-1">没有匹配的文件</p>
            <p className="text-gray-600 text-xs md:text-sm">尝试调整搜索条件或分类筛选</p>
          </div>
        </div>
      );
    }

    const dateGroups = groupByDate(filtered);
    return (
      <div>
        {dateGroups.map(group => (
          <div key={group.label}>
            {/* date group header */}
            <div className="sticky top-0 z-10 bg-gray-900/95 backdrop-blur-sm px-6 py-2 border-b border-gray-800 flex items-center">
              <span className={`text-sm font-bold mr-2 ${
                group.label === '今天' ? 'text-yellow-400' :
                group.label === '昨天' ? 'text-blue-400' :
                'text-gray-400'
              }`}>
                {group.label === '今天' ? '🟡' :
                 group.label === '昨天' ? '🔵' : '⚪'}
              </span>
              <span className={`text-sm font-bold ${
                group.label === '今天' ? 'text-yellow-400' :
                group.label === '昨天' ? 'text-blue-400' :
                'text-gray-400'
              }`}>
                {group.label}
              </span>
              <span className="text-xs text-gray-600 ml-2">({group.files.length} 个文件)</span>
            </div>
            {group.files.map(f => (
              <div key={f.hash} onClick={() => toggleFile(f.hash)} className={`flex flex-col md:flex-row md:items-center px-4 md:px-6 py-3 border-b border-gray-800/50 hover:bg-gray-800/50 transition-colors group cursor-pointer ${selected[f.hash] ? 'bg-blue-900/20 border-l-2 border-l-blue-500' : ''}`}>
                {/* Row 1: checkbox + icon + filename + actions */}
                <div className="flex items-center flex-1 min-w-0">
                  <label className="flex items-center gap-2 cursor-pointer mr-2 md:mr-3 shrink-0" onClick={e => e.stopPropagation()}>
                    <input type="checkbox" checked={!!selected[f.hash]} onChange={() => toggleFile(f.hash)} className="rounded accent-cyan-500 w-5 h-5 cursor-pointer" />
                  </label>
                  <span className="mr-2 md:mr-3 text-xl shrink-0">{extIcon(f.mime_type)}</span>
                  <div className="flex items-center gap-1 flex-1 min-w-0">
                    {getProviderBadge(f)}
                    <a href={api.getDownloadUrl(f.hash)} onClick={e => e.stopPropagation()} className="text-sm text-blue-300 truncate min-w-0 hover:text-blue-100 hover:underline cursor-pointer" title={`下载 ${f.filename}`}>{f.filename}</a>
                  </div>
                  <div className="flex items-center space-x-1 shrink-0 ml-1">
                    {swarmData[f.hash] > 0 && (
                      <span className="text-[10px] text-emerald-400 bg-emerald-900/30 px-1.5 py-0.5 rounded-full shrink-0" title={`${swarmData[f.hash]} 个节点有此文件`}>
                        🌱 {swarmData[f.hash]}
                      </span>
                    )}
                    <button onClick={(e) => { e.stopPropagation(); handleCreateFromFile(f); }} className="text-[10px] bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded whitespace-nowrap transition-colors">创建合集</button>
                  </div>
                </div>
                {/* Row 2: meta info (mobile only) + desktop meta */}
                <div className="flex md:hidden items-center gap-2 text-[11px] text-gray-500 mt-0.5 ml-9 pl-1">
                  <span>{formatSize(f.size)}</span>
                  <span>·</span>
                  <span className="truncate">{(f.mime_type || '').split(';')[0].split('/').pop() || '-'}</span>
                  <span>·</span>
                  <span className="whitespace-nowrap">{(f.created_at || '').replace('T', ' ').substring(0, 16)}</span>
                </div>
                {/* Desktop meta columns */}
                <span className="hidden md:block text-sm text-gray-400 w-24 text-right shrink-0 mr-8">{formatSize(f.size)}</span>
                <span className="hidden md:block text-xs text-gray-500 w-24 text-right shrink-0 mr-8 overflow-hidden text-ellipsis whitespace-nowrap">{(f.mime_type || '').split(';')[0].split('/').pop() || '-'}</span>
                <span className="hidden md:block text-xs text-gray-500 w-40 text-right shrink-0 mr-6">{(f.created_at || '').replace('T', ' ').substring(0, 16)}</span>
                {/* Desktop actions */}
                <div className="hidden md:flex items-center space-x-1 shrink-0">
                  {swarmData[f.hash] > 0 && (
                    <span className="text-[10px] text-emerald-400 bg-emerald-900/30 px-1.5 py-0.5 rounded-full mr-1 shrink-0" title={`${swarmData[f.hash]} 个节点有此文件`}>
                      🌱 {swarmData[f.hash]}
                    </span>
                  )}
                  <button onClick={(e) => { e.stopPropagation(); handleCreateFromFile(f); }} className="text-[10px] bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded whitespace-nowrap transition-colors">创建合集</button>
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
    );
  };

  // ---- local directory browser renderer ----
  const renderLocalDirBrowser = () => {
    if (localDirLoading) {
      return <div className="text-center text-gray-500 py-20">加载中...</div>;
    }
    if (localDirEntries.length === 0) {
      return <div className="mx-4 md:mx-6 my-6"><div className="text-center text-gray-500 py-16 md:py-20 px-4 border-2 border-dashed border-gray-700 rounded-xl"><p className="text-gray-400 text-base md:text-lg">空目录</p></div></div>;
    }
    return (
      <div>
        {/* column header */}
        <div className="hidden md:flex items-center text-xs text-gray-500 px-6 py-3 border-b border-gray-800 sticky top-0 bg-gray-900 z-10">
          <label className="flex items-center gap-2 cursor-pointer mr-3 shrink-0" onClick={e => e.stopPropagation()}>
            <input type="checkbox" checked={localDirEntries.filter(e => !e.is_dir).length > 0 && localDirEntries.filter(e => !e.is_dir).every(e => localSelected[e.path])}
              onChange={handleLocalSelectAll}
              className="rounded accent-cyan-500 w-5 h-5 cursor-pointer" />
          </label>
          <span className="flex-1 ml-1">名称</span>
          <span className="w-24 text-right mr-8">大小</span>
          <span className="w-24 text-right mr-8">类型</span>
          <span className="w-16 shrink-0" />
        </div>
        {localDirEntries.map(entry => (
          <div
            key={entry.path}
            onClick={() => handleLocalDirEnter(entry)}
            className={`flex flex-col md:flex-row md:items-center px-4 md:px-6 py-3 border-b border-gray-800/50 hover:bg-gray-800/50 transition-colors group cursor-pointer ${
              entry.is_dir
                ? ''
                : localSelected[entry.path]
                  ? 'bg-blue-900/20 border-l-2 border-l-blue-500'
                  : ''
            }`}
          >
            {entry.is_dir ? (
              <>
                <div className="flex items-center flex-1 min-w-0">
                  <span className="mr-2 md:mr-3 text-xl shrink-0">📁</span>
                  <span className="text-sm text-yellow-400 truncate flex-1">{entry.name}</span>
                  <span className="text-[10px] text-gray-500 bg-gray-800 px-1.5 py-0.5 rounded shrink-0">点击进入</span>
                </div>
                <div className="flex md:hidden items-center gap-2 text-[11px] text-gray-500 mt-0.5 ml-8">
                  <span>目录</span>
                </div>
                <span className="hidden md:block text-sm text-gray-500 w-24 text-right shrink-0 mr-8">-</span>
                <span className="hidden md:block text-xs text-gray-600 w-24 text-right shrink-0 mr-8">目录</span>
                <div className="hidden md:flex items-center space-x-1 shrink-0">
                  <span className="text-[10px] text-gray-500 bg-gray-800 px-1.5 py-0.5 rounded">点击进入</span>
                </div>
              </>
            ) : (
              <>
                <div className="flex items-center flex-1 min-w-0">
                  <label className="flex items-center gap-2 cursor-pointer mr-2 md:mr-3 shrink-0" onClick={e => e.stopPropagation()}>
                    <input type="checkbox" checked={!!localSelected[entry.path]} onChange={() => handleLocalDirEnter(entry)} className="rounded accent-cyan-500 w-5 h-5 cursor-pointer" />
                  </label>
                  <span className="mr-2 md:mr-3 text-xl shrink-0">{extIconFromName(entry.name)}</span>
                  <span className="text-sm text-gray-200 truncate flex-1">{entry.name}</span>
                  <div className="md:hidden shrink-0 ml-1">
                    {localSelected[entry.path] && <span className="text-[10px] text-blue-400">已选</span>}
                  </div>
                </div>
                <div className="flex md:hidden items-center gap-2 text-[11px] text-gray-500 mt-0.5 ml-9 pl-1">
                  <span>{formatSize(entry.size)}</span>
                  <span>·</span>
                  <span>{getExtFromName(entry.name)}</span>
                </div>
                <span className="hidden md:block text-sm text-gray-400 w-24 text-right shrink-0 mr-8">{formatSize(entry.size)}</span>
                <span className="hidden md:block text-xs text-gray-500 w-24 text-right shrink-0 mr-8 overflow-hidden text-ellipsis whitespace-nowrap">{getExtFromName(entry.name)}</span>
                <div className="hidden md:flex items-center space-x-1 shrink-0 w-16 justify-end">
                  {localSelected[entry.path] && <span className="text-[10px] text-blue-400">已选</span>}
                </div>
              </>
            )}
          </div>
        ))}
      </div>
    );
  };

  // Simple extension-based icon for local files (no mime type available)
  const extIconFromName = (name) => {
    if (!name) return '📄';
    const ext = name.split('.').pop()?.toLowerCase();
    if (['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico'].includes(ext)) return '🖼️';
    if (['mp4', 'avi', 'mkv', 'mov', 'webm', 'flv'].includes(ext)) return '🎬';
    if (['mp3', 'wav', 'flac', 'ogg', 'aac', 'wma'].includes(ext)) return '🎵';
    if (['txt', 'md', 'json', 'xml', 'csv', 'log', 'js', 'ts', 'py'].includes(ext)) return '📝';
    if (['pdf'].includes(ext)) return '📕';
    if (['zip', 'tar', 'gz', 'rar', '7z'].includes(ext)) return '📦';
    return '📄';
  };

  const getExtFromName = (name) => {
    if (!name) return '-';
    const parts = name.split('.');
    return parts.length > 1 ? parts.pop().toUpperCase() : '-';
  };

  // ---- DB directory browser renderer ----
  const renderDbDirBrowser = () => {
    const { subdirs, files: dbFiles } = useMemo(() => getDbDirContents(files, dbDirPath), [files, dbDirPath]);

    // Apply search filter at current level
    const filteredDbFiles = search
      ? dbFiles.filter(f => (f.filename || '').toLowerCase().includes(search.toLowerCase()))
      : dbFiles;

    if (!dbDirPath && subdirs.length === 0 && dbFiles.length === 0) {
      return <div className="mx-4 md:mx-6 my-6"><div className="text-center text-gray-500 py-16 md:py-20 px-4 border-2 border-dashed border-gray-700 rounded-xl"><p className="text-gray-400 text-base md:text-lg">暂无注册文件</p></div></div>;
    }

    return (
      <div>
        <div className="hidden md:flex items-center text-xs text-gray-500 px-6 py-3 border-b border-gray-800 sticky top-0 bg-gray-900 z-10">
          <label className="flex items-center gap-2 cursor-pointer mr-3 shrink-0">
            <input type="checkbox"
              checked={filteredDbFiles.length > 0 && filteredDbFiles.every(f => selected[f.hash])}
              onChange={() => {
                const next = { ...selected };
                const allSelected = filteredDbFiles.every(f => selected[f.hash]);
                for (const f of filteredDbFiles) {
                  if (allSelected) delete next[f.hash];
                  else next[f.hash] = true;
                }
                setSelected(next);
              }}
              className="rounded accent-cyan-500 w-5 h-5 cursor-pointer" />
          </label>
          <span className="flex-1 ml-1">名称</span>
          <span className="w-24 text-right mr-8">大小</span>
          <span className="w-24 text-right mr-8">类型</span>
          <span className="w-40 text-right mr-10">时间</span>
          <span className="w-16 shrink-0" />
        </div>
        {subdirs.map(dir => (
          <div
            key={dir}
            onClick={() => handleDbDirEnter(dir)}
            className="flex flex-col md:flex-row md:items-center px-4 md:px-6 py-3 border-b border-gray-800/50 hover:bg-gray-800/50 transition-colors group cursor-pointer"
          >
            <div className="flex items-center flex-1 min-w-0">
              <span className="mr-2 md:mr-3 text-xl shrink-0">📁</span>
              <span className="text-sm text-yellow-400 truncate flex-1">{dir}</span>
              <span className="md:hidden text-[10px] text-gray-500 bg-gray-800 px-1.5 py-0.5 rounded shrink-0">进入</span>
            </div>
            <div className="flex md:hidden items-center gap-2 text-[11px] text-gray-500 mt-0.5 ml-8">
              <span>目录</span>
            </div>
            <span className="hidden md:block text-sm text-gray-500 w-24 text-right shrink-0 mr-8">-</span>
            <span className="hidden md:block text-xs text-gray-600 w-24 text-right shrink-0 mr-8">目录</span>
            <span className="hidden md:block text-xs text-gray-500 w-40 text-right shrink-0 mr-6">-</span>
            <div className="hidden md:flex items-center space-x-1 shrink-0">
              <span className="text-[10px] text-gray-500 bg-gray-800 px-1.5 py-0.5 rounded">点击进入</span>
            </div>
          </div>
        ))}
        {filteredDbFiles.map(f => (
          <div key={f.hash} onClick={() => toggleFile(f.hash)} className={`flex flex-col md:flex-row md:items-center px-4 md:px-6 py-3 border-b border-gray-800/50 hover:bg-gray-800/50 transition-colors group cursor-pointer ${selected[f.hash] ? 'bg-blue-900/20 border-l-2 border-l-blue-500' : ''}`}>
            {/* Row 1: checkbox + icon + filename + actions (mobile) */}
            <div className="flex items-center flex-1 min-w-0">
              <label className="flex items-center gap-2 cursor-pointer mr-2 md:mr-3 shrink-0" onClick={e => e.stopPropagation()}>
                <input type="checkbox" checked={!!selected[f.hash]} onChange={() => toggleFile(f.hash)} className="rounded accent-cyan-500 w-5 h-5 cursor-pointer" />
              </label>
              <span className="mr-2 md:mr-3 text-xl shrink-0">{extIcon(f.mime_type)}</span>
              <div className="flex items-center gap-1 flex-1 min-w-0">
                {getProviderBadge(f)}
                <span className="text-sm text-blue-300 truncate min-w-0">{f.filename}</span>
              </div>
              <div className="flex md:hidden items-center space-x-1 shrink-0 ml-1">
                <button onClick={(e) => handleShare(f, e)} className="text-xs px-1.5 py-0.5 rounded hover:bg-gray-700 text-gray-400 hover:text-yellow-400 transition-colors" title="分享此文件">
                  📤
                </button>
                <button onClick={(e) => { e.stopPropagation(); handleCreateFromFile(f); }} className="text-[10px] bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded whitespace-nowrap transition-colors">创建合集</button>
              </div>
            </div>
            {/* Row 2: meta info (mobile) */}
            <div className="flex md:hidden items-center gap-2 text-[11px] text-gray-500 mt-0.5 ml-9 pl-1">
              <span>{formatSize(f.size)}</span>
              <span>·</span>
              <span className="truncate">{(f.mime_type || '').split(';')[0].split('/').pop() || '-'}</span>
              <span>·</span>
              <span className="whitespace-nowrap">{(f.created_at || '').replace('T', ' ').substring(0, 16)}</span>
            </div>
            {/* Desktop meta columns */}
            <span className="hidden md:block text-sm text-gray-400 w-24 text-right shrink-0 mr-8">{formatSize(f.size)}</span>
            <span className="hidden md:block text-xs text-gray-500 w-24 text-right shrink-0 mr-8 overflow-hidden text-ellipsis whitespace-nowrap">{(f.mime_type || '').split(';')[0].split('/').pop() || '-'}</span>
            <span className="hidden md:block text-xs text-gray-500 w-40 text-right shrink-0 mr-6">{(f.created_at || '').replace('T', ' ').substring(0, 16)}</span>
            {/* Desktop actions */}
            <div className="hidden md:flex items-center space-x-1 shrink-0">
              <button onClick={(e) => handleShare(f, e)} className="text-xs px-1.5 py-0.5 rounded hover:bg-gray-700 text-gray-400 hover:text-yellow-400 transition-colors" title="分享此文件">
                📤
              </button>
              <button onClick={(e) => { e.stopPropagation(); handleCreateFromFile(f); }} className="text-[10px] bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded whitespace-nowrap transition-colors">创建合集</button>
            </div>
          </div>
        ))}
      </div>
    );
  };

  // =============================================
  // MAIN RENDER
  // =============================================
  return (
    <div className="flex flex-1 overflow-hidden h-full">
      <div className="flex-1 flex flex-col bg-gray-900 has-mobile-nav">
        {/* header */}
        <div className="bg-gray-800 border-b border-gray-700 shrink-0">
          <div className="flex items-center px-4 md:px-6 py-3 justify-between gap-2">
            <div className="flex items-center space-x-2 md:space-x-4 min-w-0">
              <button onClick={() => navigate('/')} className="text-gray-400 hover:text-white text-sm shrink-0">← 广场</button>
              <h2 className="text-base md:text-lg font-bold truncate">文件管理</h2>
              <span className="text-xs text-gray-500 hidden sm:inline">{files.length} 个文件 · {formatSize(totalSize)}</span>
            </div>
            <div className="flex items-center space-x-2 shrink-0">
              <button onClick={handleOpenFileBrowser} className="bg-indigo-600 hover:bg-indigo-500 px-2.5 md:px-3 py-1.5 rounded text-xs md:text-sm whitespace-nowrap">
                + 添加文件
              </button>
            </div>
          </div>

          {/* mode switcher */}
          <div className="flex px-4 md:px-6 pb-3 space-x-1 overflow-x-auto scrollbar-hide">
            {BROWSE_MODES.map(m => (
              <button
                key={m.key}
                onClick={() => setBrowseMode(m.key)}
                className={`flex items-center px-3 md:px-4 py-1.5 rounded-lg text-xs md:text-sm font-medium transition-colors whitespace-nowrap flex-shrink-0 ${
                  browseMode === m.key
                    ? 'bg-blue-600 text-white shadow-lg shadow-blue-600/20'
                    : 'bg-gray-700 text-gray-400 hover:bg-gray-600 hover:text-white'
                }`}
              >
                <span className="mr-1.5">{m.icon}</span>
                {m.label}
              </button>
            ))}
          </div>

          {/* timeline mode controls */}
          {browseMode === 'timeline' && (
            <>
              {/* search + sort + view toggle */}
              <div className="flex flex-col md:flex-row items-stretch md:items-center px-4 md:px-6 pb-2 gap-2 md:space-x-3">
                <div className="relative flex-1 md:max-w-md">
                  <span className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500 text-sm">🔍</span>
                  <input
                    type="text" placeholder="搜索文件..."
                    value={search} onChange={e => setSearch(e.target.value)}
                    className="w-full bg-gray-700 pl-9 pr-3 py-1.5 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                </div>
                <div className="flex items-center gap-2">
                  <div className="flex bg-gray-800 rounded overflow-hidden border border-gray-600 shrink-0">
                    {SORT_KEYS.map(s => (
                      <button
                        key={s.key}
                        onClick={() => setSortBy(s.key)}
                        className={`px-2.5 md:px-3 py-1.5 text-xs ${sortBy === s.key ? 'bg-blue-600 text-white' : 'text-gray-400 hover:text-white'}`}
                      >{s.label}</button>
                    ))}
                  </div>
                  <div className="flex bg-gray-800 rounded overflow-hidden border border-gray-600 shrink-0">
                    <button
                      onClick={() => setViewMode('list')}
                      className={`px-2.5 md:px-3 py-1.5 text-xs ${viewMode === 'list' ? 'bg-gray-600 text-white' : 'text-gray-400'}`}
                    >≡ 列表</button>
                    <button
                      onClick={() => setViewMode('tree')}
                      className={`px-2.5 md:px-3 py-1.5 text-xs ${viewMode === 'tree' ? 'bg-gray-600 text-white' : 'text-gray-400'}`}
                    >📁 目录树</button>
                  </div>
                </div>
              </div>

              {/* category chips */}
              <div className="flex px-4 md:px-6 pb-3 space-x-2 overflow-x-auto scrollbar-hide">
                {CATEGORIES.map(c => {
                  const count = c.key === '' ? files.length : (categoryCounts[c.key] || 0);
                  return (
                    <button
                      key={c.key}
                      onClick={() => setCategory(c.key)}
                      className={`flex items-center px-3 py-1.5 rounded-full text-xs whitespace-nowrap shrink-0 transition-colors ${category === c.key ? 'bg-blue-600 text-white' : 'bg-gray-700 text-gray-400 hover:bg-gray-600 hover:text-white'}`}
                    >
                      <span className="mr-1">{c.icon}</span>
                      {c.label}
                      <span className={`ml-1.5 ${category === c.key ? 'text-blue-200' : 'text-gray-500'}`}>{count}</span>
                    </button>
                  );
                })}
              </div>

              {/* virtual scroll hint */}
              {files.length > 500 && (
                <div className="px-6 pb-2 text-xs text-gray-500">
                  {files.length} 个文件 · 滚动加载
                </div>
              )}
            </>
          )}

          {/* local directory mode controls */}
          {browseMode === 'localDir' && (
            <div className="px-4 md:px-6 pb-3 space-y-2">
              <div className="flex items-center space-x-2">
                <button
                  onClick={handleLocalDirUp}
                  disabled={localDirPath === '/'}
                  className="px-2.5 py-1.5 bg-gray-700 rounded hover:bg-gray-600 disabled:opacity-30 disabled:cursor-not-allowed text-xs font-medium"
                  title="上级目录"
                >
                  ← 返回
                </button>
                <div className="flex-1 min-w-0">
                  {renderBreadcrumb(localDirPath, handleBreadcrumbNav)}
                </div>
                <span className="text-xs text-gray-500 shrink-0">
                  {localDirEntries.filter(e => !e.is_dir).length} 个文件
                </span>
              </div>
              {localSelCount > 0 && (
                <div className="flex items-center space-x-2 text-xs">
                  <span className="text-cyan-400 bg-cyan-400/10 px-2 py-1 rounded-full font-medium">
                    已选 {localSelCount} 个文件
                  </span>
                  <button
                    onClick={handleLocalRegisterOnly}
                    className="bg-green-700 hover:bg-green-600 px-3 py-1 rounded"
                  >
                    注册选中
                  </button>
                  <button
                    onClick={handleLocalRegisterAndCreate}
                    className="bg-teal-600 hover:bg-teal-500 px-3 py-1 rounded font-medium"
                  >
                    注册并创建合集
                  </button>
                  <button
                    onClick={() => setLocalSelected({})}
                    className="text-gray-400 hover:text-white px-2 py-1"
                  >
                    取消选择
                  </button>
                </div>
              )}
            </div>
          )}

          {/* DB directory mode controls */}
          {browseMode === 'dbDir' && (
            <div className="px-4 md:px-6 pb-3 space-y-2">
              <div className="flex items-center space-x-2">
                <button
                  onClick={handleDbDirUp}
                  disabled={!dbDirPath}
                  className="px-2.5 py-1.5 bg-gray-700 rounded hover:bg-gray-600 disabled:opacity-30 disabled:cursor-not-allowed text-xs font-medium"
                  title="上级目录"
                >
                  ← 返回
                </button>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center space-x-1.5 text-sm flex-wrap">
                    <button
                      onClick={() => handleDbBreadcrumbNav('')}
                      className={`hover:underline transition-colors ${!dbDirPath ? 'text-blue-300 font-medium' : 'text-gray-400 hover:text-blue-300'}`}
                    >根目录</button>
                    {dbDirPath && dbDirPath.split('/').filter(Boolean).map((part, i, parts) => {
                      const path = parts.slice(0, i + 1).join('/');
                      return (
                        <React.Fragment key={path}>
                          <span className="text-gray-600 select-none">/</span>
                          <button
                            onClick={() => handleDbBreadcrumbNav(path)}
                            className={`hover:underline transition-colors ${i === parts.length - 1 ? 'text-blue-300 font-medium' : 'text-gray-400 hover:text-blue-300'}`}
                          >{part}</button>
                        </React.Fragment>
                      );
                    })}
                  </div>
                </div>
                <div className="relative max-w-[200px]">
                  <span className="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-500 text-xs">🔍</span>
                  <input
                    type="text" placeholder="筛选..."
                    value={search} onChange={e => setSearch(e.target.value)}
                    className="w-full bg-gray-700 pl-7 pr-2 py-1.5 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                </div>
              </div>
            </div>
          )}
        </div>

        {/* content area */}
        <div className={`flex-1 overflow-y-auto ${selCount > 0 && browseMode !== 'localDir' ? 'pb-16' : ''}`}>
          {browseMode === 'timeline' && (
            loading ? (
              <div className="text-center text-gray-500 py-20">{loadingProgress}</div>
            ) : viewMode === 'list' ? (
              renderTimelineView()
            ) : (
              <div className="p-4">
                <div className="text-xs text-gray-500 mb-2 px-2 flex">
                  <span className="flex-1">目录</span>
                </div>
                {renderTree(tree, 0)}
              </div>
            )
          )}
          {browseMode === 'localDir' && renderLocalDirBrowser()}
          {browseMode === 'dbDir' && (
            loading ? (
              <div className="text-center text-gray-500 py-20">{loadingProgress}</div>
            ) : (
              renderDbDirBrowser()
            )
          )}
        </div>

        {/* multi-select action bar (timeline + dbDir modes) */}
        {selCount > 0 && browseMode !== 'localDir' && (
          <div className="fixed bottom-0 left-0 right-0 md:bottom-0 bg-gray-900/80 backdrop-blur-md border-t border-gray-700 z-50 px-4 md:px-6 py-3 flex items-center justify-between shadow-2xl mb-0 md:mb-0" style={{ paddingBottom: 'max(12px, env(safe-area-inset-bottom, 0px))' }}>
            <span className="text-sm text-cyan-400 bg-cyan-400/10 px-3 py-1 rounded-full font-medium">
              已选择 {selCount} 个文件
            </span>
            <div className="flex items-center space-x-3">
              <button onClick={handleCreateCollection} className="bg-teal-600 hover:bg-teal-500 px-4 py-2 rounded-lg text-sm font-medium transition-colors">
                创建合集 ({selCount})
              </button>
              <button onClick={handleDeleteSelected} className="bg-red-700 hover:bg-red-600 px-4 py-2 rounded-lg text-sm font-medium transition-colors">
                删除选中 ({selCount})
              </button>
            </div>
          </div>
        )}

        {/* notification toast */}
        {notification && (
          <div className="fixed top-4 right-4 z-50 bg-gray-800 border border-gray-600 rounded-lg px-4 py-2.5 shadow-xl text-sm max-w-xs">
            <span className="text-green-400 mr-1.5">✓</span>
            {notification}
          </div>
        )}
      </div>

      {/* add-file modal (unchanged) */}
      {showFileBrowser && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50" onClick={() => setShowFileBrowser(false)}>
          <div className="bg-gray-800 p-4 md:p-6 rounded-xl w-full max-w-[680px] mx-4 border border-gray-600 shadow-2xl flex flex-col max-h-[85vh]" onClick={e => e.stopPropagation()}>
            <h3 className="text-lg font-bold mb-1">添加文件</h3>
            <p className="text-xs text-gray-400 mb-4">浏览节点文件系统或从 URL 注册文件。</p>

            {/* Tabs */}
            <div className="flex space-x-1 mb-4 border-b border-gray-700">
              <button
                onClick={() => setAddFileTab('local')}
                className={`px-4 py-2 text-sm rounded-t transition-colors ${addFileTab === 'local' ? 'bg-gray-700 text-white border-b-2 border-blue-500' : 'text-gray-400 hover:text-white hover:bg-gray-700/50'}`}
              >
                本地文件
              </button>
              <button
                onClick={() => setAddFileTab('url')}
                className={`px-4 py-2 text-sm rounded-t transition-colors ${addFileTab === 'url' ? 'bg-gray-700 text-white border-b-2 border-blue-500' : 'text-gray-400 hover:text-white hover:bg-gray-700/50'}`}
              >
                URL 注册
              </button>
            </div>

            {addFileTab === 'local' ? (
              <>
                <div className="flex items-center space-x-2 mb-3">
                  <button onClick={handleRegParent} disabled={currentDir === '/'} className="px-2 py-1 bg-gray-700 rounded hover:bg-gray-600 disabled:opacity-30 text-xs">←</button>
                  <code className="text-gray-300 text-xs truncate flex-1">{currentDir}</code>
                  <button onClick={() => browseDir('/')} className="px-2 py-1 bg-gray-700 rounded hover:bg-gray-600 text-xs">/</button>
                </div>

                <div className="flex-1 overflow-y-auto bg-gray-900 rounded mb-4 min-h-[200px]">
                  {browseLoading ? (
                    <div className="p-4 text-center text-gray-500">加载中...</div>
                  ) : dirEntries.length === 0 ? (
                    <div className="p-4 text-center text-gray-500">空目录</div>
                  ) : dirEntries.map(e => (
                    <div
                      key={e.path}
                      onClick={() => handleRegEnter(e)}
                      className={`flex items-center px-3 py-2 cursor-pointer hover:bg-gray-800 ${e.is_dir ? 'text-yellow-400' : ''} ${selectedFiles[e.path] ? 'bg-blue-900/30 border-l-2 border-blue-500' : ''}`}
                    >
                      <span className="mr-2 text-base">{e.is_dir ? '📁' : '📄'}</span>
                      <span className="flex-1 text-sm truncate">{e.name}</span>
                      {!e.is_dir && <span className="text-xs text-gray-500 ml-2">{formatSize(e.size)}</span>}
                    </div>
                  ))}
                </div>

                <div className="flex justify-between items-center">
                  <span className="text-xs text-gray-500">{Object.values(selectedFiles).filter(Boolean).length} 个已选</span>
                  <div className="flex space-x-3">
                    <button onClick={handleRegisterCurrentFolder} className="px-3 py-1.5 bg-indigo-700 hover:bg-indigo-600 rounded text-xs">注册当前目录</button>
                    <button onClick={handleRegisterSelected} className="px-3 py-1.5 bg-green-600 hover:bg-green-700 rounded text-xs font-medium">注册选中文件</button>
                    <button onClick={() => setShowFileBrowser(false)} className="px-3 py-1.5 bg-gray-600 rounded text-xs">取消</button>
                  </div>
                </div>
              </>
            ) : (
              <>
                <div className="flex-1 flex flex-col bg-gray-900 rounded mb-4 p-4 min-h-[200px]">
                  <div className="mb-3">
                    <label className="block text-xs text-gray-400 mb-1">URL</label>
                    <input
                      type="url"
                      value={urlInput}
                      onChange={(e) => setUrlInput(e.target.value)}
                      placeholder="https://example.com/file.zip"
                      onKeyDown={(e) => e.key === 'Enter' && handleRegisterUrl()}
                      className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
                    />
                  </div>
                  <div className="mb-3">
                    <label className="block text-xs text-gray-400 mb-1">
                      文件名
                      <span className="text-gray-600 ml-1">（可选，留空自动从 URL 提取）</span>
                    </label>
                    <input
                      type="text"
                      value={urlFilename}
                      onChange={(e) => setUrlFilename(e.target.value)}
                      placeholder="optional-filename.ext"
                      onKeyDown={(e) => e.key === 'Enter' && handleRegisterUrl()}
                      className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
                    />
                  </div>

                  {urlError && (
                    <div className="text-xs text-red-400 mb-3 bg-red-900/20 px-3 py-2 rounded">
                      {urlError}
                    </div>
                  )}

                  {urlResult && (
                    <div className="text-xs bg-green-900/20 border border-green-800/30 rounded px-3 py-2 mb-3 space-y-1">
                      <p className="text-green-400 font-medium">注册成功</p>
                      <p className="text-gray-300">哈希: <span className="font-mono text-gray-400">{urlResult.hash}</span></p>
                      <p className="text-gray-300">大小: <span className="text-gray-400">{formatSize(urlResult.size)}</span></p>
                      {urlResult.filename && <p className="text-gray-300">文件名: <span className="text-gray-400">{urlResult.filename}</span></p>}
                    </div>
                  )}

                  <div className="flex-1" />
                </div>

                <div className="flex justify-end items-center space-x-3">
                  <button
                    onClick={handleRegisterUrl}
                    disabled={urlLoading || !urlInput.trim()}
                    className="px-4 py-1.5 bg-green-600 hover:bg-green-700 disabled:opacity-40 disabled:cursor-not-allowed rounded text-xs font-medium"
                  >
                    {urlLoading ? '注册中...' : '注册 URL'}
                  </button>
                  <button onClick={() => { setShowFileBrowser(false); setUrlResult(null); setUrlError(''); setUrlInput(''); }} className="px-3 py-1.5 bg-gray-600 rounded text-xs">取消</button>
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
