import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';
import { Link, useNavigate, useParams } from 'react-router-dom';

const SHA256_RE = /\b([a-f0-9]{64})\b/i;

function extractHash(text) {
  const m = (text || '').match(SHA256_RE);
  return m ? m[1].toLowerCase() : null;
}

export default function AnonExplorer() {
  const { hash: paramHash } = useParams();
  const [inputVal, setInputVal] = useState(paramHash || '');
  const [searchHash, setSearchHash] = useState(paramHash || '');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const navigate = useNavigate();

  // fork / commit panels
  const [showFork, setShowFork] = useState(false);
  const [forkData, setForkData] = useState({ addPath: '', addHash: '', removePath: '', friendlyName: '' });

  const [showCommit, setShowCommit] = useState(false);
  const [commitData, setCommitData] = useState({ message: '', addPath: '', addHash: '', removePath: '' });

  // file list for hash picking in fork/commit
  const [dbFiles, setDbFiles] = useState([]);
  const [showFilePicker, setShowFilePicker] = useState(false);
  const [pickerTarget, setPickerTarget] = useState(''); // 'fork' or 'commit'

  // sync URL param <-> input
  useEffect(() => {
    if (paramHash) {
      setInputVal(paramHash);
      setSearchHash(paramHash);
    }
  }, [paramHash]);

  // load collection when hash is available
  useEffect(() => {
    if (searchHash) {
      fetchCollection(searchHash);
    }
  }, [searchHash]);

  // document.title
  useEffect(() => {
    if (collection) {
      const name = collection.friendly_name || searchHash.substring(0, 12);
      document.title = `${name} — 匿名合集`;
    } else if (searchHash) {
      document.title = `${searchHash.substring(0, 12)}... — 加载中`;
    } else {
      document.title = '匿名合集浏览器';
    }
    return () => { document.title = 'Peerdrive'; };
  }, [collection, searchHash]);

  const fetchCollection = async (h) => {
    if (!h) return;
    setLoading(true);
    setError('');
    setCollection(null);
    try {
      const coll = await api.getAnonCollection(h);
      setCollection(coll);
    } catch (err) {
      setError('合集未找到');
    } finally {
      setLoading(false);
    }
  };

  const loadDbFiles = async () => {
    try {
      const data = await api.listFiles('name');
      setDbFiles(data || []);
    } catch (e) { setDbFiles([]); }
  };

  const handleInputChange = (val) => {
    setInputVal(val);
    const extracted = extractHash(val);
    if (extracted) {
      setSearchHash(extracted);
      navigate(`/anon/collections/${extracted}`);
    }
  };

  const handleSearch = () => {
    const extracted = extractHash(inputVal);
    const h = extracted || inputVal.trim().toLowerCase();
    if (h.length !== 64) return alert('请输入有效的 SHA256 Hash (64位十六进制)');
    setSearchHash(h);
    navigate(`/anon/collections/${h}`);
  };

  const handleFork = async (e) => {
    e.preventDefault();
    const add_entries = forkData.addPath && forkData.addHash
      ? [{ path: forkData.addPath, hash: forkData.addHash }]
      : [];
    const remove_paths = forkData.removePath ? [forkData.removePath] : [];
    try {
      const res = await api.forkAnonCollection(searchHash, add_entries, remove_paths, forkData.friendlyName);
      setShowFork(false);
      setForkData({ addPath: '', addHash: '', removePath: '', friendlyName: '' });
      navigate(`/anon/collections/${res.hash}`);
    } catch (err) {
      alert(`Fork 失败: ${err.message}`);
    }
  };

  const handleCommit = async (e) => {
    e.preventDefault();
    const newEntries = [];
    if (commitData.addPath && commitData.addHash) {
      newEntries.push({ path: commitData.addPath, hash: commitData.addHash });
    }
    if (commitData.removePath) {
      newEntries.push({ path: commitData.removePath, hash: '' });
    }
    try {
      const res = await api.commitAnonCollection(searchHash, newEntries, commitData.message || 'update');
      setShowCommit(false);
      setCommitData({ message: '', addPath: '', addHash: '', removePath: '' });
      navigate(`/anon/collections/${res.hash}`);
    } catch (err) {
      alert(`Commit 失败: ${err.message}`);
    }
  };

  const pickFile = (f) => {
    if (pickerTarget === 'fork') {
      setForkData(prev => ({ ...prev, addHash: f.hash }));
    } else if (pickerTarget === 'commit') {
      setCommitData(prev => ({ ...prev, addHash: f.hash }));
    }
    setShowFilePicker(false);
  };

  const openFilePicker = (target) => {
    setPickerTarget(target);
    loadDbFiles();
    setShowFilePicker(true);
  };

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      <div className="flex-1 flex flex-col max-w-4xl mx-auto w-full">
        <div className="h-14 flex items-center px-4 shrink-0 space-x-3 border-b border-gray-800">
          <Link to="/anon/create" className="text-sm bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded">+ 创建合集</Link>
          <h2 className="text-base font-bold">匿名合集</h2>
          {collection?.friendly_name && (
            <span className="text-gray-400 text-sm">{collection.friendly_name}</span>
          )}
        </div>

        <div className="flex items-center gap-2 px-4 py-3 shrink-0">
          <div className="relative flex-1">
            <input
              type="text"
              value={inputVal}
              onChange={(e) => handleInputChange(e.target.value)}
              onPaste={(e) => {
                const pasted = e.clipboardData.getData('text');
                const extracted = extractHash(pasted);
                if (extracted) {
                  e.preventDefault();
                  setInputVal(extracted);
                  setSearchHash(extracted);
                  navigate(`/anon/collections/${extracted}`);
                }
              }}
              onKeyDown={(e) => { if (e.key === 'Enter') handleSearch(); }}
              placeholder="输入 SHA256 Hash (或粘贴含 hash 的链接，自动提取)"
              className="w-full bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500"
            />
            {inputVal && extractHash(inputVal) && (
              <span className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-green-500 font-mono">
                ✓ {extractHash(inputVal).substring(0, 12)}...
              </span>
            )}
          </div>
          <button onClick={handleSearch}
            disabled={loading}
            className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50">
            {loading ? '加载中...' : '查看'}
          </button>
        </div>

        {error && (
          <div className="px-4 py-3">
            <p className="text-red-400 text-sm">{error}</p>
          </div>
        )}

        {loading && !collection && (
          <div className="flex-1 flex items-center justify-center text-gray-600">加载中...</div>
        )}

        {collection && (
          <div className="flex-1 overflow-y-auto px-4">
            <div className="bg-gray-800 p-4 rounded-lg mb-4 border border-gray-700 mt-2">
              <div className="flex justify-between items-center mb-2">
                <div>
                  <span className="text-gray-400 text-sm">Version: </span>
                  <span className="font-bold text-blue-400">{collection.version}</span>
                </div>
                <div className="text-gray-500 text-xs">
                  {collection.created_at ? new Date(collection.created_at).toLocaleString() : ''}
                </div>
              </div>
              {collection.friendly_name && (
                <p className="text-gray-300 text-sm">{collection.friendly_name}</p>
              )}
              <div className="mt-3 flex gap-2 flex-wrap">
                <code className="text-xs bg-gray-900 px-2 py-1 rounded text-gray-400 truncate max-w-[300px]">
                  {searchHash}
                </code>
                <button onClick={() => setShowCommit(!showCommit)}
                  className="bg-green-600 hover:bg-green-700 px-3 py-1 rounded text-xs">
                  {showCommit ? '取消' : 'Commit'}
                </button>
                <button onClick={() => setShowFork(!showFork)}
                  className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs">
                  {showFork ? '取消 Fork' : 'Fork'}
                </button>
              </div>
            </div>

            {showCommit && (
              <form onSubmit={handleCommit} className="bg-green-900/30 p-4 rounded-lg mb-4 border border-green-700">
                <h4 className="font-bold mb-3 text-sm">提交新版本</h4>
                <div className="mb-3">
                  <label className="block text-xs text-gray-400 mb-1">Commit Message</label>
                  <input value={commitData.message}
                    onChange={(e) => setCommitData({...commitData, message: e.target.value})}
                    placeholder="描述本次变更..."
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm" />
                </div>
                <div className="grid grid-cols-2 gap-3 mb-3">
                  <div>
                    <label className="block text-xs text-gray-400 mb-1">新增/修改路径</label>
                    <input value={commitData.addPath}
                      onChange={(e) => setCommitData({...commitData, addPath: e.target.value})}
                      placeholder="如 CHANGELOG.md"
                      className="w-full bg-gray-700 px-3 py-2 rounded text-sm" />
                  </div>
                  <div className="flex gap-1 items-end">
                    <div className="flex-1">
                      <label className="block text-xs text-gray-400 mb-1">文件 Hash</label>
                      <input value={commitData.addHash}
                        onChange={(e) => setCommitData({...commitData, addHash: e.target.value})}
                        placeholder="sha256..."
                        className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono" />
                    </div>
                    <button type="button" onClick={() => openFilePicker('commit')}
                      className="bg-gray-600 hover:bg-gray-500 px-2 py-2 rounded text-xs whitespace-nowrap">📂</button>
                  </div>
                </div>
                <div className="mb-3">
                  <label className="block text-xs text-gray-400 mb-1">移除文件路径 (留空不删除)</label>
                  <input value={commitData.removePath}
                    onChange={(e) => setCommitData({...commitData, removePath: e.target.value})}
                    placeholder="如 old.txt"
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm" />
                </div>
                <button type="submit" className="bg-green-600 hover:bg-green-700 px-4 py-2 rounded text-sm">
                  确认提交
                </button>
              </form>
            )}

            {showFork && (
              <form onSubmit={handleFork} className="bg-purple-900/30 p-4 rounded-lg mb-4 border border-purple-700">
                <h4 className="font-bold mb-3 text-sm">Fork 变体</h4>
                <div className="mb-3">
                  <label className="block text-xs text-gray-400 mb-1">Fork 名称</label>
                  <input value={forkData.friendlyName}
                    onChange={(e) => setForkData({...forkData, friendlyName: e.target.value})}
                    placeholder="可选"
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm" />
                </div>
                <div className="grid grid-cols-2 gap-3 mb-3">
                  <div>
                    <label className="block text-xs text-gray-400 mb-1">新增文件路径</label>
                    <input value={forkData.addPath}
                      onChange={(e) => setForkData({...forkData, addPath: e.target.value})}
                      placeholder="如 new.txt"
                      className="w-full bg-gray-700 px-3 py-2 rounded text-sm" />
                  </div>
                  <div className="flex gap-1 items-end">
                    <div className="flex-1">
                      <label className="block text-xs text-gray-400 mb-1">文件 Hash</label>
                      <input value={forkData.addHash}
                        onChange={(e) => setForkData({...forkData, addHash: e.target.value})}
                        placeholder="sha256..."
                        className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono" />
                    </div>
                    <button type="button" onClick={() => openFilePicker('fork')}
                      className="bg-gray-600 hover:bg-gray-500 px-2 py-2 rounded text-xs whitespace-nowrap">📂</button>
                  </div>
                </div>
                <div className="mb-3">
                  <label className="block text-xs text-gray-400 mb-1">移除文件路径</label>
                  <input value={forkData.removePath}
                    onChange={(e) => setForkData({...forkData, removePath: e.target.value})}
                    placeholder="如 old.txt"
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm" />
                </div>
                <button type="submit" className="bg-purple-600 hover:bg-purple-700 px-4 py-2 rounded text-sm">
                  确认 Fork
                </button>
              </form>
            )}

            {showFilePicker && (
              <div className="bg-gray-800 p-4 rounded-lg mb-4 border border-gray-600">
                <div className="flex justify-between items-center mb-3">
                  <h4 className="font-bold text-sm">从已注册文件选择</h4>
                  <button onClick={() => setShowFilePicker(false)} className="text-gray-400 hover:text-white text-sm">× 关闭</button>
                </div>
                <div className="max-h-60 overflow-y-auto space-y-1">
                  {dbFiles.length === 0 ? (
                    <p className="text-gray-500 text-xs">还没有注册文件</p>
                  ) : dbFiles.map(f => (
                    <div key={f.hash}
                      onClick={() => pickFile(f)}
                      className="flex items-center gap-2 py-1.5 px-2 rounded cursor-pointer hover:bg-gray-700 text-xs">
                      <span className="text-blue-300 font-mono truncate flex-1">{f.filename}</span>
                      <span className="text-gray-500 font-mono">{f.hash.substring(0, 12)}...</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <table className="w-full text-left border-collapse mb-6">
              <thead>
                <tr className="border-b border-gray-700 text-gray-400 text-xs">
                  <th className="pb-2 font-medium">路径</th>
                  <th className="pb-2 font-medium w-48">文件 Hash</th>
                  <th className="pb-2 font-medium w-24 text-right">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-800">
                {collection.entries?.map((entry, idx) => (
                  <tr key={idx} className="hover:bg-gray-800/50">
                    <td className="py-2 font-mono text-sm text-blue-300">{entry.path}</td>
                    <td className="py-2 font-mono text-xs text-gray-500 truncate">{(entry.hash || '').substring(0, 24)}...</td>
                    <td className="py-2 text-right">
                      <a href={api.getAnonFileDownloadUrl(searchHash, entry.path)}
                        target="_blank" rel="noreferrer"
                        className="text-blue-400 hover:underline text-xs">下载</a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {!loading && !collection && !error && !paramHash && (
          <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
            输入合集 Hash 查看内容，或粘贴含 hash 的链接自动提取
          </div>
        )}
      </div>
    </div>
  );
}
