import React, { useContext, useState, useEffect, useMemo } from 'react';
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

export default function FileManager() {
  const { setPageContext } = useContext(PageContext);
  const navigate = useNavigate();

  const [files, setFiles] = useState([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState({});
  const [search, setSearch] = useState('');
  const [sortBy, setSortBy] = useState('time');
  const [category, setCategory] = useState('');
  const [viewMode, setViewMode] = useState('list');

  const [expanded, setExpanded] = useState({});
  const [showFileBrowser, setShowFileBrowser] = useState(false);
  const [currentDir, setCurrentDir] = useState('/');
  const [dirEntries, setDirEntries] = useState([]);
  const [selectedFiles, setSelectedFiles] = useState({});
  const [browseLoading, setBrowseLoading] = useState(false);
  const [regTarget, setRegTarget] = useState('collection');

  useEffect(() => { loadFiles(); }, []);

  useEffect(() => {
    setPageContext({ type: 'fileManager', fileCount: files.length, sortBy, category });
  }, [files, sortBy, category]);

  const loadFiles = async () => {
    setLoading(true);
    try {
      const data = await api.listFiles('path');
      setFiles(data || []);
    } catch (e) { console.error(e); setFiles([]); }
    setLoading(false);
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
    navigate('/anon/create', { state: { draftFrom: { entries: selectedEntries, friendlyName: name } } });
    setSelected({});
  };

  const handleCreateFromFile = (file) => {
    navigate('/anon/create', { state: { draftFrom: { entries: [{ path: file.filename, hash: file.hash }], friendlyName: file.filename } } });
  };

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
        entries.push({ path: filename, hash: res.hash || path });
      } catch (e) { alert(`注册失败: ${path}: ${e.message}`); return; }
    }
    setShowFileBrowser(false);
    setSelectedFiles({});
    loadFiles();
    if (regTarget === 'collection' && entries.length > 0) {
      navigate('/anon/create', { state: { draftFrom: { entries, friendlyName: entries[0].path || 'collection' } } });
    }
  };

  const handleRegisterCurrentFolder = async () => {
    try {
      const res = await api.registerFolder(currentDir);
      const registered = res.registered || [];
      setShowFileBrowser(false);
      if (regTarget === 'collection' && registered.length > 0) {
        const entries = registered.map(r => ({ path: r.filename, hash: r.hash }));
        const name = currentDir.split('/').pop() || 'collection';
        navigate('/anon/create', { state: { draftFrom: { entries, friendlyName: name } } });
        return;
      }
      alert(`注册完成: ${registered.length} 个文件`);
      loadFiles();
    } catch (e) { alert(`注册失败: ${e.message}`); }
  };

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
    navigate('/anon/create', { state: { draftFrom: { entries, friendlyName: dirName } } });
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

  return (
    <div className="flex flex-1 overflow-hidden h-full">
      <div className="flex-1 flex flex-col bg-gray-900">
        {/* header */}
        <div className="bg-gray-800 border-b border-gray-700">
          <div className="flex items-center px-6 py-3 justify-between">
            <div className="flex items-center space-x-4">
              <button onClick={() => navigate('/')} className="text-gray-400 hover:text-white text-sm">← 广场</button>
              <h2 className="text-lg font-bold">文件管理</h2>
              <span className="text-xs text-gray-500">{files.length} 个文件 · {formatSize(totalSize)}</span>
            </div>
            <div className="flex items-center space-x-2">
              {selCount > 0 && (
                <button onClick={handleCreateCollection} className="bg-teal-600 hover:bg-teal-500 px-3 py-1.5 rounded text-sm font-medium">
                  创建合集 ({selCount})
                </button>
              )}
              <button onClick={handleOpenFileBrowser} className="bg-indigo-600 hover:bg-indigo-500 px-3 py-1.5 rounded text-sm">
                + 添加文件
              </button>
            </div>
          </div>

          {/* search + sort + view toggle */}
          <div className="flex items-center px-6 pb-2 space-x-3">
            <div className="relative flex-1 max-w-md">
              <span className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500 text-sm">🔍</span>
              <input
                type="text" placeholder="搜索文件..."
                value={search} onChange={e => setSearch(e.target.value)}
                className="w-full bg-gray-700 pl-9 pr-3 py-1.5 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div className="flex bg-gray-800 rounded overflow-hidden border border-gray-600 shrink-0">
              {SORT_KEYS.map(s => (
                <button
                  key={s.key}
                  onClick={() => setSortBy(s.key)}
                  className={`px-3 py-1.5 text-xs ${sortBy === s.key ? 'bg-blue-600 text-white' : 'text-gray-400 hover:text-white'}`}
                >{s.label}</button>
              ))}
            </div>
            <div className="flex bg-gray-800 rounded overflow-hidden border border-gray-600 shrink-0">
              <button
                onClick={() => setViewMode('list')}
                className={`px-3 py-1.5 text-xs ${viewMode === 'list' ? 'bg-gray-600 text-white' : 'text-gray-400'}`}
              >≡ 列表</button>
              <button
                onClick={() => setViewMode('tree')}
                className={`px-3 py-1.5 text-xs ${viewMode === 'tree' ? 'bg-gray-600 text-white' : 'text-gray-400'}`}
              >📁 目录树</button>
            </div>
          </div>

          {/* category chips */}
          <div className="flex px-6 pb-3 space-x-2 overflow-x-auto scrollbar-thin">
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
        </div>

        {/* content */}
        <div className="flex-1 overflow-y-auto">
          {loading ? (
            <div className="text-center text-gray-500 py-20">加载中...</div>
          ) : filtered.length === 0 ? (
            <div className="text-center text-gray-500 py-20 border-2 border-dashed border-gray-700 rounded-xl m-6">
              {files.length === 0 ? (
                <>还没有任何文件。点击"添加文件"浏览节点目录。</>
              ) : (
                <>没有匹配的文件</>
              )}
            </div>
          ) : viewMode === 'list' ? (
            <div>
              <div className="flex items-center text-xs text-gray-500 px-6 py-2 border-b border-gray-800">
                <input type="checkbox" checked={filtered.length > 0 && filtered.every(f => selected[f.hash])}
                  onChange={selectAllFiltered}
                  className="rounded mr-3 shrink-0" />
                <span className="flex-1">文件名</span>
                <span className="w-20 text-right mr-6">大小</span>
                <span className="w-20 text-right mr-6">类型</span>
                <span className="w-36 text-right mr-8">时间</span>
              </div>
              {filtered.map(f => (
                <div key={f.hash} className="flex items-center px-6 py-2.5 border-b border-gray-800/50 hover:bg-gray-800/30 transition-colors group">
                  <input type="checkbox" checked={!!selected[f.hash]} onChange={() => toggleFile(f.hash)} className="rounded mr-3 shrink-0 accent-cyan-500 w-4 h-4" />
                  <span className="mr-3 text-lg shrink-0">{extIcon(f.mime_type)}</span>
                  <a href={api.getDownloadUrl(f.hash)} className="text-sm text-blue-300 truncate flex-1 min-w-0 hover:text-blue-100 hover:underline cursor-pointer" title={`下载 ${f.filename}`}>{f.filename}</a>
                  <span className="text-xs text-gray-400 w-20 text-right shrink-0 mr-6">{formatSize(f.size)}</span>
                  <span className="text-xs text-gray-500 w-20 text-right shrink-0 mr-6 overflow-hidden text-ellipsis whitespace-nowrap">{(f.mime_type || '').split(';')[0].split('/').pop() || '-'}</span>
                  <span className="text-xs text-gray-500 w-36 text-right shrink-0 mr-4">{(f.created_at || '').replace('T', ' ').substring(0, 16)}</span>
                  <div className="flex space-x-2 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                    <button onClick={() => handleCreateFromFile(f)} className="text-[10px] bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded whitespace-nowrap">合集</button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="p-4">
              <div className="text-xs text-gray-500 mb-2 px-2 flex">
                <span className="flex-1">目录</span>
              </div>
              {renderTree(tree, 0)}
            </div>
          )}
        </div>
      </div>

      {showFileBrowser && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-[680px] border border-gray-600 shadow-2xl flex flex-col max-h-[85vh]">
            <h3 className="text-lg font-bold mb-1">添加文件</h3>
            <p className="text-xs text-gray-400 mb-4">浏览节点文件系统。选中文件夹注册后自动创建为匿名合集。</p>

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
          </div>
        </div>
      )}
    </div>
  );
}
