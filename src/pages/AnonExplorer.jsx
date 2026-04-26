import React, { useState } from 'react';
import * as api from '../api';
import { Link } from 'react-router-dom';

export default function AnonExplorer() {
  const [hash, setHash] = useState('');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const [showFork, setShowFork] = useState(false);
  const [forkData, setForkData] = useState({ addPath: '', addHash: '', removePath: '', friendlyName: '' });

  const [showCommit, setShowCommit] = useState(false);
  const [commitData, setCommitData] = useState({ message: '', addPath: '', addHash: '', removePath: '' });

  const fetchCollection = async (targetHash) => {
    if (!targetHash) return;
    setLoading(true);
    setError('');
    try {
      const coll = await api.getAnonCollection(targetHash);
      setCollection(coll);
      setHash(targetHash);
    } catch (err) {
      setError('合集未找到或网络错误');
      setCollection(null);
    } finally {
      setLoading(false);
    }
  };

  const handleFork = async (e) => {
    e.preventDefault();
    const add_entries = forkData.addPath && forkData.addHash 
      ? [{ path: forkData.addPath, hash: forkData.addHash }] 
      : [];
    const remove_paths = forkData.removePath ? [forkData.removePath] : [];
    
    try {
      const res = await api.forkAnonCollection(hash, add_entries, remove_paths, forkData.friendlyName);
      const newHash = res.hash;
      alert(`Fork 成功！新 Hash: ${newHash}`);
      fetchCollection(newHash);
      setShowFork(false);
      setForkData({ addPath: '', addHash: '', removePath: '', friendlyName: '' });
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
      const res = await api.commitAnonCollection(hash, newEntries, commitData.message || 'update');
      alert(`Commit 成功！新 Hash: ${res.hash}`);
      fetchCollection(res.hash);
      setShowCommit(false);
      setCommitData({ message: '', addPath: '', addHash: '', removePath: '' });
    } catch (err) {
      alert(`Commit 失败: ${err.message}`);
    }
  };

  return (
    <div className="p-6 max-w-4xl mx-auto h-full overflow-y-auto">
      <div className="flex items-center gap-3 mb-6">
        <Link to="/anon/create" className="text-sm bg-blue-600 hover:bg-blue-700 px-3 py-1.5 rounded">
          + 创建合集
        </Link>
        <h2 className="text-2xl font-bold">匿名合集浏览器</h2>
      </div>
      
      <div className="flex gap-2 mb-4">
        <input
          type="text"
          value={hash}
          onChange={(e) => setHash(e.target.value)}
          placeholder="输入 Collection Hash 探索"
          className="flex-1 bg-gray-800 border border-gray-600 px-4 py-2 rounded text-sm focus:outline-none focus:border-blue-500"
        />
        <button 
          onClick={() => fetchCollection(hash)} 
          disabled={loading}
          className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50"
        >
          {loading ? '加载中...' : '查看'}
        </button>
      </div>

      {error && <p className="text-red-400 mb-4">{error}</p>}

      {collection && (
        <div>
          <div className="bg-gray-800 p-4 rounded-lg mb-4 border border-gray-700">
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
              <code className="text-xs bg-gray-900 px-2 py-1 rounded text-gray-400 truncate">
                Hash: {hash.substring(0, 16)}...
              </code>
              <button 
                onClick={() => setShowCommit(!showCommit)} 
                className="bg-green-600 hover:bg-green-700 px-3 py-1 rounded text-xs"
              >
                {showCommit ? '取消' : 'Commit'}
              </button>
              <button 
                onClick={() => setShowFork(!showFork)} 
                className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs"
              >
                {showFork ? '取消 Fork' : 'Fork'}
              </button>
            </div>
          </div>

          {showCommit && (
            <form onSubmit={handleCommit} className="bg-green-900/30 p-4 rounded-lg mb-4 border border-green-700">
              <h4 className="font-bold mb-3">提交新版本</h4>
              <div className="mb-3">
                <label className="block text-xs text-gray-400 mb-1">Commit Message</label>
                <input 
                  value={commitData.message} 
                  onChange={(e) => setCommitData({...commitData, message: e.target.value})}
                  placeholder="描述本次变更..."
                  className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                />
              </div>
              <div className="grid grid-cols-2 gap-3 mb-3">
                <div>
                  <label className="block text-xs text-gray-400 mb-1">新增/修改文件路径</label>
                  <input 
                    value={commitData.addPath} 
                    onChange={(e) => setCommitData({...commitData, addPath: e.target.value})}
                    placeholder="如 CHANGELOG.md"
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                  />
                </div>
                <div>
                  <label className="block text-xs text-gray-400 mb-1">文件 Hash</label>
                  <input 
                    value={commitData.addHash} 
                    onChange={(e) => setCommitData({...commitData, addHash: e.target.value})}
                    placeholder="sha256..."
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                  />
                </div>
              </div>
              <div className="mb-3">
                <label className="block text-xs text-gray-400 mb-1">移除文件路径 (留空不删除)</label>
                <input 
                  value={commitData.removePath} 
                  onChange={(e) => setCommitData({...commitData, removePath: e.target.value})}
                  placeholder="如 old.txt"
                  className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                />
              </div>
              <button type="submit" className="bg-green-600 hover:bg-green-700 px-4 py-2 rounded text-sm">
                确认提交
              </button>
            </form>
          )}

          {showFork && (
            <form onSubmit={handleFork} className="bg-purple-900/30 p-4 rounded-lg mb-4 border border-purple-700">
              <h4 className="font-bold mb-3">Fork 变体</h4>
              <div className="mb-3">
                <label className="block text-xs text-gray-400 mb-1">Fork 名称</label>
                <input 
                  value={forkData.friendlyName} 
                  onChange={(e) => setForkData({...forkData, friendlyName: e.target.value})}
                  placeholder="可选"
                  className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                />
              </div>
              <div className="grid grid-cols-2 gap-3 mb-3">
                <div>
                  <label className="block text-xs text-gray-400 mb-1">新增文件路径</label>
                  <input 
                    value={forkData.addPath} 
                    onChange={(e) => setForkData({...forkData, addPath: e.target.value})}
                    placeholder="如 new.txt"
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                  />
                </div>
                <div>
                  <label className="block text-xs text-gray-400 mb-1">文件 Hash</label>
                  <input 
                    value={forkData.addHash} 
                    onChange={(e) => setForkData({...forkData, addHash: e.target.value})}
                    placeholder="sha256..."
                    className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                  />
                </div>
              </div>
              <div className="mb-3">
                <label className="block text-xs text-gray-400 mb-1">移除文件路径</label>
                <input 
                  value={forkData.removePath} 
                  onChange={(e) => setForkData({...forkData, removePath: e.target.value})}
                  placeholder="如 old.txt"
                  className="w-full bg-gray-700 px-3 py-2 rounded text-sm"
                />
              </div>
              <button type="submit" className="bg-purple-600 hover:bg-purple-700 px-4 py-2 rounded text-sm">
                确认 Fork
              </button>
            </form>
          )}

          <table className="w-full text-left border-collapse">
            <thead>
              <tr className="border-b-2 border-gray-700 text-gray-400 text-xs">
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
                    <a 
                      href={api.getAnonFileDownloadUrl(hash, entry.path)} 
                      target="_blank" 
                      rel="noreferrer"
                      className="text-blue-400 hover:underline text-xs"
                    >
                      下载
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
