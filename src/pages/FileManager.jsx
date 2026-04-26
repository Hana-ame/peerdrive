import React, { useContext, useState, useEffect } from 'react';
import { AppContext, PageContext } from '../App';
import { useNavigate } from 'react-router-dom';
import * as api from '../api';

const SORT_OPTIONS = [
  { value: 'time', label: '时间' },
  { value: 'name', label: '文件名' },
  { value: 'path', label: '目录' },
  { value: 'type', label: '文件类型' },
  { value: 'size', label: '大小' },
];

export default function FileManager() {
  const { username, setUsername: ctxSetUsername } = useContext(AppContext);
  const { setPageContext } = useContext(PageContext);
  const [files, setFiles] = useState([]);
  const [loading, setLoading] = useState(true);
  const [sortBy, setSortBy] = useState('time');
  const [selected, setSelected] = useState({});
  const [selectAll, setSelectAll] = useState(false);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [createTarget, setCreateTarget] = useState('new');
  const [newCollName, setNewCollName] = useState('');
  const [existingColl, setExistingColl] = useState('');
  const [collections, setCollections] = useState([]);
  const [localUser, setLocalUser] = useState(localStorage.getItem('peerdrive_reg_user') || '');
  const [showFileBrowser, setShowFileBrowser] = useState(false);
  const [currentDir, setCurrentDir] = useState('/');
  const [dirEntries, setDirEntries] = useState([]);
  const [selectedFiles, setSelectedFiles] = useState({});
  const [browseLoading, setBrowseLoading] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    loadFiles();
    if (!username) {
      const saved = localStorage.getItem('peerdrive_username');
      if (saved) ctxSetUsername(saved);
    }
  }, [username, sortBy]);

  useEffect(() => {
    if (showCreateModal && username) {
      api.listCollections(username).then(d => setCollections(d.collections || d.data || [])).catch(() => {});
    }
  }, [showCreateModal, username]);

  const loadFiles = async () => {
    setLoading(true);
    try {
      const data = await api.listFiles(sortBy);
      setFiles(data || []);
    } catch (e) {
      console.error(e);
      setFiles([]);
    }
    setLoading(false);
  };

  const toggleFile = (hash) => {
    setSelected(prev => {
      const next = { ...prev };
      next[hash] = !next[hash];
      return next;
    });
    setSelectAll(false);
  };

  const toggleSelectAll = () => {
    if (selectAll) {
      setSelected({});
      setSelectAll(false);
    } else {
      const sel = {};
      files.forEach(f => { sel[f.hash] = true; });
      setSelected(sel);
      setSelectAll(true);
    }
  };

  const selectedHashes = Object.keys(selected).filter(k => selected[k]);
  const selectedEntries = files.filter(f => selected[f.hash]).map(f => ({ path: f.filename, hash: f.hash }));

  useEffect(() => {
    setPageContext({ type: 'fileManager', fileCount: files.length, selectedCount: selectedEntries.length, sortBy });
  }, [files, selectedEntries, sortBy]);

  const handleCreateCollection = async () => {
    if (selectedEntries.length === 0) return alert('请先选择文件');

    let targetUser = username;
    if (!targetUser) {
      if (!localUser) return alert('请先在导航栏输入用户名');
      targetUser = localUser;
      localStorage.setItem('peerdrive_username', localUser);
      ctxSetUsername(localUser);
    }

    let collName;
    try {
      if (createTarget === 'new') {
        if (!newCollName.trim()) return alert('请输入合集名称');
        collName = newCollName.trim();
        try { await api.createCollection(targetUser, collName); } catch (e) {
          if (!e.message?.includes('already exists') && !e.message?.includes('UNIQUE')) throw e;
        }
      } else {
        if (!existingColl) return alert('请选择合集');
        collName = existingColl;
      }
    } catch (e) { return alert(`创建/选择合集失败: ${e.message}`); }

    try {
      for (const entry of selectedEntries) {
        await api.addEntry(targetUser, collName, entry.path, entry.hash);
      }
      alert(`已将 ${selectedEntries.length} 个文件添加到合集 "${collName}"`);
      setShowCreateModal(false);
      setNewCollName('');
      setExistingColl('');
      setSelected({});
      setSelectAll(false);
      navigate(`/${targetUser}/${collName}`);
    } catch (e) {
      alert(`添加文件失败: ${e.message}`);
    }
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
    const files = Object.keys(selectedFiles).filter(k => selectedFiles[k]);
    if (files.length === 0) return alert('请先选择文件');
    let ok = 0, fail = 0;
    for (const path of files) {
      try {
        const filename = path.split('/').pop();
        await api.registerLocalFile(path, filename);
        ok++;
      } catch (e) { fail++; }
    }
    alert(`注册完成: ${ok} 成功, ${fail} 失败`);
    setShowFileBrowser(false);
    setSelectedFiles({});
    loadFiles();
  };

  const handleRegisterCurrentFolder = async () => {
    try {
      const res = await api.registerFolder(currentDir);
      alert(`注册完成: ${res.registered?.length || 0} 个文件`);
      setShowFileBrowser(false);
      loadFiles();
    } catch (e) { alert(`注册失败: ${e.message}`); }
  };

  const relPath = (p) => {
    const idx = p.indexOf('/storage/');
    return idx >= 0 ? p.slice(idx) : p;
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

  const sortedCollections = [...collections].sort((a, b) => {
    if (a.username === username && b.username !== username) return -1;
    if (b.username === username && a.username !== username) return 1;
    return 0;
  });

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
            <select
              value={sortBy}
              onChange={(e) => setSortBy(e.target.value)}
              className="bg-gray-700 text-sm px-3 py-1.5 rounded border border-gray-600 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              {SORT_OPTIONS.map(opt => (
                <option key={opt.value} value={opt.value}>{opt.label} ↓</option>
              ))}
            </select>
            {selectedEntries.length > 0 && (
              <button
                onClick={() => setShowCreateModal(true)}
                className="bg-green-600 hover:bg-green-700 px-4 py-1.5 rounded text-sm font-medium"
              >
                创建合集 ({selectedEntries.length})
              </button>
            )}
          </div>
        </div>

        <div className="h-12 bg-gray-850 border-b border-gray-700 flex items-center px-6 space-x-4 shrink-0">
          <button onClick={handleOpenFileBrowser} className="bg-indigo-600 hover:bg-indigo-500 px-3 py-1 rounded text-xs">浏览文件系统</button>
          <span className="text-xs text-gray-500">浏览节点文件系统，选择文件注册</span>
        </div>

        <div className="flex-1 overflow-y-auto p-6">
          <table className="w-full text-left">
            <thead className="text-gray-400 text-xs border-b border-gray-700 sticky top-0 bg-gray-900">
              <tr>
                <th className="pb-3 font-medium w-10">
                  <input type="checkbox" checked={selectAll} onChange={toggleSelectAll} className="rounded" />
                </th>
                <th className="pb-3 font-medium w-10"></th>
                <th className="pb-3 font-medium">文件名</th>
                <th className="pb-3 font-medium w-36">类型</th>
                <th className="pb-3 font-medium w-24 text-right">大小</th>
                <th className="pb-3 font-medium w-36">注册时间</th>
                <th className="pb-3 font-medium w-40">源路径</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-800">
              {loading ? (
                <tr><td colSpan="7" className="py-10 text-center text-gray-500">加载中...</td></tr>
              ) : files.length === 0 ? (
                <tr><td colSpan="7" className="py-10 text-center text-gray-500">
                  还没有注册任何文件。请先用"注册文件夹"或"注册文件"按钮添加本地文件。
                </td></tr>
              ) : files.map(f => (
                <tr key={f.hash} className="group hover:bg-gray-800/50">
                  <td className="py-2.5">
                    <input type="checkbox" checked={!!selected[f.hash]} onChange={() => toggleFile(f.hash)} className="rounded" />
                  </td>
                  <td className="py-2.5 text-lg">{extIcon(f.mime_type)}</td>
                  <td className="py-2.5 font-mono text-sm text-blue-300">{f.filename}</td>
                  <td className="py-2.5 text-xs text-gray-500">{f.mime_type?.split('/').pop() || '-'}</td>
                  <td className="py-2.5 text-xs text-gray-400 text-right">{formatSize(f.size)}</td>
                  <td className="py-2.5 text-xs text-gray-500">{f.created_at?.replace('T', ' ').substring(0, 16) || '-'}</td>
                  <td className="py-2.5 text-[10px] text-gray-600 font-mono truncate max-w-[160px]" title={relPath(f.provider_path)}>
                    {relPath(f.provider_path || f.hash?.substring(0, 16) + '...')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {showCreateModal && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-[480px] border border-gray-600 shadow-2xl">
            <h3 className="text-lg font-bold mb-2">从选中文件创建合集</h3>
            <p className="text-sm text-gray-400 mb-4">已选中 {selectedEntries.length} 个文件</p>

            {!username && (
              <div className="mb-4">
                <label className="block text-xs text-gray-400 mb-1">归属用户 (必填)</label>
                <input
                  type="text" value={localUser} onChange={(e) => setLocalUser(e.target.value)}
                  placeholder="输入用户名"
                  className="w-full bg-gray-700 p-2 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            )}

            <div className="flex space-x-4 mb-4">
              <label className={`flex-1 p-3 rounded border cursor-pointer text-center text-sm ${createTarget === 'new' ? 'border-blue-500 bg-blue-900/30' : 'border-gray-600'}`}>
                <input type="radio" name="target" value="new" checked={createTarget === 'new'} onChange={() => setCreateTarget('new')} className="hidden" />
                新建合集
              </label>
              <label className={`flex-1 p-3 rounded border cursor-pointer text-center text-sm ${createTarget === 'existing' ? 'border-blue-500 bg-blue-900/30' : 'border-gray-600'}`}>
                <input type="radio" name="target" value="existing" checked={createTarget === 'existing'} onChange={() => setCreateTarget('existing')} className="hidden" />
                加入已有合集
              </label>
            </div>

            {createTarget === 'new' ? (
              <input
                type="text" value={newCollName} onChange={(e) => setNewCollName(e.target.value)}
                placeholder="输入新合集名称..."
                className="w-full bg-gray-700 p-2 rounded text-sm mb-6 focus:outline-none focus:ring-1 focus:ring-blue-500"
                autoFocus
              />
            ) : (
              <select
                value={existingColl} onChange={(e) => setExistingColl(e.target.value)}
                className="w-full bg-gray-700 p-2 rounded text-sm mb-6 focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="">-- 选择已有合集 --</option>
                {sortedCollections.map(c => (
                  <option key={c.id} value={c.collection_name}>{c.collection_name} ({c.username})</option>
                ))}
              </select>
            )}

            <div className="max-h-32 overflow-y-auto mb-4 bg-gray-900 rounded p-2 text-[10px] font-mono text-gray-500">
              {selectedEntries.map(e => (
                <div key={e.path + e.hash} className="truncate">{e.path} — {e.hash?.substring(0,16)}...</div>
              ))}
            </div>

            <div className="flex justify-end space-x-3">
              <button onClick={() => setShowCreateModal(false)} className="px-4 py-2 bg-gray-600 rounded text-sm">取消</button>
              <button onClick={handleCreateCollection} className="px-4 py-2 bg-green-600 hover:bg-green-700 rounded text-sm font-medium">
                添加到合集
              </button>
            </div>
          </div>
        </div>
      )}

      {showFileBrowser && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-[640px] border border-gray-600 shadow-2xl flex flex-col max-h-[80vh]">
            <h3 className="text-lg font-bold mb-4">浏览节点文件系统</h3>

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
                      {!e.is_dir && <span className="text-xs text-gray-500 ml-2">{e.mod_time?.substring(0,10)}</span>}
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

function formatSize(bytes) {
  if (!bytes) return '-';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
  if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB';
  return (bytes / 1073741824).toFixed(1) + ' GB';
}
