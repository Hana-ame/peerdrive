import React, { useContext, useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { PageContext } from '../App';
import * as api from '../api';

export default function FileManager() {
  const { setPageContext } = useContext(PageContext);
  const [files, setFiles] = useState([]);
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState({});
  const [selected, setSelected] = useState({});
  const [showFileBrowser, setShowFileBrowser] = useState(false);
  const [currentDir, setCurrentDir] = useState('/');
  const [dirEntries, setDirEntries] = useState([]);
  const [selectedFiles, setSelectedFiles] = useState({});
  const [browseLoading, setBrowseLoading] = useState(false);
  const [regTarget, setRegTarget] = useState('dir');
  const [regName, setRegName] = useState('');
  const navigate = useNavigate();

  useEffect(() => { loadFiles(); }, []);

  useEffect(() => {
    setPageContext({ type: 'fileManager', fileCount: files.length });
  }, [files]);

  const loadFiles = async () => {
    setLoading(true);
    try {
      const data = await api.listFiles('path');
      setFiles(data || []);
    } catch (e) { console.error(e); setFiles([]); }
    setLoading(false);
  };

  const toggleExpand = (path) => {
    setExpanded(prev => ({ ...prev, [path]: !prev[path] }));
  };

  const toggleFile = (hash) => {
    setSelected(prev => ({ ...prev, [hash]: !prev[hash] }));
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
        if (!node.dirs[part]) {
          node.dirs[part] = { name: part, dirs: {}, files: [] };
        }
        node = node.dirs[part];
      }
      node.files.push(f);
    }
    return root;
  };

  const handleCreateFromDir = async (dirName, dirFiles) => {
    const entries = dirFiles.map(f => ({ path: f.filename, hash: f.hash }));
    const friendly = dirName || 'collection';
    try {
      const res = await api.createAnonCollection(entries, friendly);
      navigate(`/anon/collections/${res.hash}`);
    } catch (e) { alert(`创建失败: ${e.message}`); }
  };

  const handleCreateFromFile = async (file) => {
    try {
      const res = await api.createAnonCollection([{ path: file.filename, hash: file.hash }], file.filename);
      navigate(`/anon/collections/${res.hash}`);
    } catch (e) { alert(`创建失败: ${e.message}`); }
  };

  const selectedHashes = Object.keys(selected).filter(k => selected[k]);

  const handleCreateFromSelected = async () => {
    if (selectedHashes.length === 0) return alert('请先选择文件');
    const entries = files.filter(f => selected[f.hash]).map(f => ({ path: f.filename, hash: f.hash }));
    const name = entries[0].path.split('/').pop()?.replace(/\.[^.]+$/, '') || 'collection';
    try {
      const res = await api.createAnonCollection(entries, name);
      navigate(`/anon/collections/${res.hash}`);
    } catch (e) { alert(`创建失败: ${e.message}`); }
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
    } catch (e) {
      alert(`目录浏览失败: ${e.message}`);
    }
    setBrowseLoading(false);
  };

  const handleOpenFileBrowser = () => {
    setShowFileBrowser(true);
    setSelectedFiles({});
    setRegTarget('dir');
    setRegName('');
    browseDir('/');
  };

  const handleRegEnter = (entry) => {
    if (entry.is_dir) {
      browseDir(entry.path);
    } else {
      setSelectedFiles(prev => {
        const next = { ...prev };
        next[entry.path] = !next[entry.path];
        return next;
      });
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
      } catch (e) {
        alert(`注册失败: ${path}: ${e.message}`);
        return;
      }
    }
    setShowFileBrowser(false);
    setSelectedFiles({});
    if (regTarget === 'collection' && entries.length > 0) {
      const name = regName.trim() || entries[0].path || 'collection';
      try {
        const res = await api.createAnonCollection(entries, name);
        navigate(`/anon/collections/${res.hash}`);
        return;
      } catch (e) { alert(`创建合集失败: ${e.message}`); }
    }
    loadFiles();
  };

  const handleRegisterCurrentFolder = async () => {
    setRegName(currentDir.split('/').pop() || 'collection');
    try {
      const res = await api.registerFolder(currentDir);
      const registered = res.registered || [];
      if (regTarget === 'collection' && registered.length > 0) {
        const entries = registered.map(r => ({ path: r.filename, hash: r.hash }));
        const name = regName.trim() || currentDir.split('/').pop() || 'collection';
        const col = await api.createAnonCollection(entries, name);
        setShowFileBrowser(false);
        navigate(`/anon/collections/${col.hash}`);
        return;
      }
      alert(`注册完成: ${registered.length} 个文件`);
      setShowFileBrowser(false);
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

  const tree = buildTree(files);

  const selCount = selectedHashes.length;

  const renderTree = (node, depth) => {
    const sortedDirs = Object.keys(node.dirs).sort();
    return (
      <div>
        {sortedDirs.map(name => {
          const dir = node.dirs[name];
          const isExpanded = expanded[dir.name];
          const allHashes = dir.files.map(f => f.hash);
          const dirSelected = allHashes.length > 0 && allHashes.every(h => selected[h]);
          return (
            <div key={name}>
              <div
                className={`flex items-center py-1.5 px-2 ml-${Math.min(depth * 4, 16)} rounded cursor-pointer hover:bg-gray-800/50 group`}
                style={{ marginLeft: `${depth * 16}px` }}
              >
                <span onClick={() => toggleExpand(dir.name)} className="mr-1 w-5 text-center text-gray-500 shrink-0">
                  {isExpanded ? '▼' : '▶'}
                </span>
                <input
                  type="checkbox"
                  checked={dirSelected}
                  onChange={() => {
                    const next = { ...selected };
                    for (const h of allHashes) {
                      next[h] = !dirSelected;
                    }
                    setSelected(next);
                  }}
                  className="rounded mr-2 shrink-0"
                />
                <span className="mr-2 text-lg shrink-0">📁</span>
                <span className="text-yellow-400 text-sm font-mono truncate flex-1">{name}</span>
                <span className="text-xs text-gray-500 mr-2">{dir.files.length} 文件</span>
                <button
                  onClick={() => handleCreateFromDir(name, dir.files)}
                  className="hidden group-hover:inline-block bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded text-[10px] shrink-0"
                >
                  创建为合集
                </button>
              </div>
              {isExpanded && (
                <div>
                  {dir.files.map(f => (
                    <div
                      key={f.hash}
                      className={`flex items-center py-1.5 px-2 cursor-pointer hover:bg-gray-800/50 group`}
                      style={{ marginLeft: `${(depth + 1) * 16 + 22}px` }}
                    >
                      <input
                        type="checkbox"
                        checked={!!selected[f.hash]}
                        onChange={() => toggleFile(f.hash)}
                        className="rounded mr-2 shrink-0"
                      />
                      <span className="mr-2 text-lg shrink-0">{extIcon(f.mime_type)}</span>
                      <span className="font-mono text-sm text-blue-300 truncate flex-1">{f.filename}</span>
                      <span className="text-xs text-gray-500 mr-2 w-16 text-right shrink-0">{formatSize(f.size)}</span>
                      <span className="text-xs text-gray-500 mr-2 w-28 shrink-0">{f.created_at?.replace('T', ' ').substring(0, 16) || '-'}</span>
                      <button
                        onClick={() => handleCreateFromFile(f)}
                        className="hidden group-hover:inline-block bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded text-[10px] shrink-0"
                      >
                        创建合集
                      </button>
                    </div>
                  ))}
                  {renderTree(dir, depth + 1)}
                </div>
              )}
            </div>
          );
        })}
        {depth === 0 && sortedDirs.length === 0 && node.files.length > 0 && (
          <div className="text-gray-500 text-xs px-2 py-4">
            {node.files.length} 个文件位于根目录
            {node.files.map(f => (
              <div key={f.hash} className="flex items-center py-1.5 group">
                <input type="checkbox" checked={!!selected[f.hash]} onChange={() => toggleFile(f.hash)} className="rounded mr-2" />
                <span className="mr-2">{extIcon(f.mime_type)}</span>
                <span className="font-mono text-sm text-blue-300 truncate flex-1">{f.filename}</span>
                <span className="text-xs text-gray-500 mr-2">{formatSize(f.size)}</span>
                <button onClick={() => handleCreateFromFile(f)} className="hidden group-hover:inline-block bg-teal-600 hover:bg-teal-500 px-2 py-0.5 rounded text-[10px]">创建合集</button>
              </div>
            ))}
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="flex flex-1 overflow-hidden h-full">
      <div className="flex-1 flex flex-col bg-gray-900">
        <div className="h-16 bg-gray-800 border-b border-gray-700 flex items-center px-6 justify-between shrink-0">
          <div className="flex items-center space-x-4">
            <button onClick={() => navigate('/')} className="text-gray-400 hover:text-white">← 广场</button>
            <h2 className="text-lg font-bold">文件管理器</h2>
            <span className="text-xs text-gray-500">{files.length} 个文件</span>
          </div>
          <div className="flex items-center space-x-3">
            {selCount > 0 && (
              <button onClick={handleCreateFromSelected} className="bg-teal-600 hover:bg-teal-500 px-4 py-1.5 rounded text-sm font-medium">
                从选中创建合集 ({selCount})
              </button>
            )}
            <button onClick={handleOpenFileBrowser} className="bg-indigo-600 hover:bg-indigo-500 px-4 py-1.5 rounded text-sm">
              + 浏览文件系统
            </button>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-4">
          {loading ? (
            <div className="text-center text-gray-500 py-10">加载中...</div>
          ) : files.length === 0 ? (
            <div className="text-center text-gray-500 py-20 border-2 border-dashed border-gray-700 rounded-xl">
              还没有注册任何文件。点击"浏览文件系统"浏览节点目录，
              <br />
              选择文件夹或文件注册后自动创建为合集。
            </div>
          ) : (
            <div className="text-xs text-gray-400 mb-2 flex items-center space-x-4 px-2">
              <span className="w-5"></span>
              <span className="w-4"></span>
              <span></span>
              <span className="flex-1 ml-20">文件名</span>
              <span className="w-16 text-right">大小</span>
              <span className="w-32 ml-2">注册时间</span>
            </div>
          )}
          {renderTree(tree, 0)}
        </div>
      </div>

      {showFileBrowser && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-[640px] border border-gray-600 shadow-2xl flex flex-col max-h-[85vh]">
            <h3 className="text-lg font-bold mb-2">浏览节点文件系统</h3>
            <p className="text-xs text-gray-400 mb-4">选择文件夹或文件，注册后自动创建为合集</p>

            <div className="flex space-x-4 mb-4">
              <button
                onClick={() => setRegTarget('dir')}
                className={`flex-1 p-2 rounded text-sm border ${regTarget === 'dir' ? 'border-indigo-500 bg-indigo-900/30 text-indigo-300' : 'border-gray-600 text-gray-400'}`}
              >
                注册目录
              </button>
              <button
                onClick={() => setRegTarget('collection')}
                className={`flex-1 p-2 rounded text-sm border ${regTarget === 'collection' ? 'border-teal-500 bg-teal-900/30 text-teal-300' : 'border-gray-600 text-gray-400'}`}
              >
                注册并创建匿名合集
              </button>
            </div>

            {regTarget === 'collection' && (
              <div className="mb-3">
                <input
                  type="text"
                  placeholder="合集友好名称..."
                  value={regName}
                  onChange={(e) => setRegName(e.target.value)}
                  className="w-full bg-gray-700 p-2 rounded text-sm focus:outline-none focus:ring-1 focus:ring-teal-500"
                />
              </div>
            )}

            <div className="flex items-center space-x-2 mb-3 text-sm">
              <button onClick={handleRegParent} disabled={currentDir === '/'} className="px-2 py-1 bg-gray-700 rounded hover:bg-gray-600 disabled:opacity-30 text-xs">← 上一级</button>
              <span className="text-gray-300 font-mono text-xs truncate flex-1">{currentDir}</span>
              <button onClick={() => browseDir('/')} className="px-2 py-1 bg-gray-700 rounded hover:bg-gray-600 text-xs">/</button>
            </div>

            <div className="flex-1 overflow-y-auto bg-gray-900 rounded mb-4 min-h-[200px]">
              {browseLoading ? (
                <div className="p-4 text-center text-gray-500">加载中...</div>
              ) : dirEntries.length === 0 ? (
                <div className="p-4 text-center text-gray-500">空目录</div>
              ) : (
                <div className="divide-y divide-gray-800">
                  {dirEntries.map(e => (
                    <div
                      key={e.path}
                      onClick={() => handleRegEnter(e)}
                      className={`flex items-center px-3 py-2 cursor-pointer hover:bg-gray-800 ${e.is_dir ? 'text-yellow-400' : ''} ${selectedFiles[e.path] ? 'bg-blue-900/30 border-l-2 border-blue-500' : ''}`}
                    >
                      <span className="mr-2 text-lg">{e.is_dir ? '📁' : '📄'}</span>
                      <span className="flex-1 text-sm font-mono truncate">{e.name}</span>
                      {!e.is_dir && <span className="text-xs text-gray-500 ml-2">{formatSize(e.size)}</span>}
                      {!e.is_dir && <span className="text-xs text-gray-500 ml-2">{e.mod_time?.substring(0, 10)}</span>}
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="flex justify-between items-center">
              <span className="text-xs text-gray-500">
                {Object.values(selectedFiles).filter(Boolean).length} 个文件已选
              </span>
              <div className="flex space-x-3">
                <button onClick={handleRegisterCurrentFolder} className="px-3 py-1.5 bg-indigo-700 hover:bg-indigo-600 rounded text-xs">
                  注册当前目录
                </button>
                <button onClick={handleRegisterSelected} className="px-3 py-1.5 bg-green-600 hover:bg-green-700 rounded text-xs font-medium">
                  注册选中文件
                </button>
                <button onClick={() => setShowFileBrowser(false)} className="px-3 py-1.5 bg-gray-600 rounded text-xs">取消</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
