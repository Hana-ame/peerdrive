import React, { useState, useEffect } from 'react';
import * as api from '../api';

export default function AnonCollectionManager({ prefillEntries }) {
  const [viewHash, setViewHash] = useState('');
  const [collection, setCollection] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  /* --- create form --- */
  const [newEntries, setNewEntries] = useState(() => {
    if (prefillEntries && prefillEntries.length > 0) return prefillEntries;
    return [{ path: '', hash: '' }];
  });
  const [creating, setCreating] = useState(false);

  /* --- fork section --- */
  const [forkAddPath, setForkAddPath] = useState('');

  // When prefillEntries changes from outside, update form
  useEffect(() => {
    if (prefillEntries && prefillEntries.length > 0) {
      setNewEntries(prefillEntries);
    }
  }, [prefillEntries]);

  const fetchCollection = async (hash) => {
    if (!hash) return;
    setLoading(true);
    setError('');
    try {
      const res = await api.getAnonCollection(hash);
      setCollection(res);
      setViewHash(hash);
    } catch {
      setError('合集未找到');
      setCollection(null);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async (e) => {
    e.preventDefault();
    const filtered = newEntries.filter(e => e.path && e.hash);
    if (filtered.length === 0) return;
    setCreating(true);
    try {
      const res = await api.createAnonCollection(filtered);
      setNewEntries([{ path: '', hash: '' }]);
      fetchCollection(res.hash);
    } catch (err) {
      setError(err.message);
    } finally {
      setCreating(false);
    }
  };

  const handleFork = async (e) => {
    e.preventDefault();
    const add_entries = forkAddPath && forkAddHash ? [{ path: forkAddPath, hash: forkAddHash }] : [];
    const remove_paths = forkRemovePath ? [forkRemovePath] : [];
    try {
      const res = await api.forkAnonCollection(viewHash, add_entries, remove_paths);
      setForkAddPath(''); setForkAddHash(''); setForkRemovePath('');
      fetchCollection(res.hash);
    } catch (err) {
      alert('Fork 失败: ' + err.message);
    }
  };

  return (
    <div className="max-w-3xl mx-auto space-y-10">
      {/* ---- Browse ---- */}
      <section>
        <h2 className="text-xl font-semibold mb-2">匿名合集</h2>
        <p className="text-zinc-400 text-sm mb-5">通过哈希值查看不可变合集的内容，或创建新的合集。</p>

        <form onSubmit={e => { e.preventDefault(); fetchCollection(viewHash); }} className="flex gap-2">
          <input
            value={viewHash}
            onChange={e => setViewHash(e.target.value)}
            placeholder="输入 Collection Hash"
            className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-4 py-2.5 text-sm
                       focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 font-mono"
          />
          <button
            type="submit"
            disabled={loading}
            className="bg-white text-black px-5 py-2.5 rounded-lg text-sm font-medium
                       hover:bg-zinc-200 disabled:opacity-40 transition-colors"
          >
            {loading ? '加载中' : '查看'}
          </button>
        </form>

        {error && (
          <div className="mt-3 bg-red-900/20 border border-red-900/40 rounded-lg px-4 py-3 text-sm text-red-400">
            {error}
          </div>
        )}
      </section>

      {/* ---- Collection content ---- */}
      {collection && (
        <section className="bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden">
          <div className="flex items-center justify-between px-5 py-3 border-b border-zinc-800 bg-zinc-900/50">
            <div className="flex items-center gap-4 text-xs text-zinc-400">
              <span>Version <span className="text-zinc-200 font-mono">{collection.version}</span></span>
              <span>{new Date(collection.created_at).toLocaleString()}</span>
              {collection.name && <span className="text-zinc-500">{collection.name}</span>}
            </div>
            <span className="text-xs text-zinc-600 font-mono truncate max-w-xs">{viewHash}</span>
          </div>

          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-zinc-800 text-zinc-500 text-xs">
                <th className="text-left py-2.5 px-5 font-medium">Path</th>
                <th className="text-left py-2.5 px-5 font-medium w-48">Hash</th>
                <th className="text-right py-2.5 px-5 font-medium w-20">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800">
              {collection.entries.map((entry, idx) => (
                <tr key={idx} className="hover:bg-zinc-800/50 transition-colors">
                  <td className="py-2.5 px-5 font-mono text-zinc-300">{entry.path}</td>
                  <td className="py-2.5 px-5 font-mono text-xs text-zinc-500 truncate max-w-[12rem]">{entry.hash}</td>
                  <td className="py-2.5 px-5 text-right">
                    <a
                      href={api.getAnonFileDownloadUrl(viewHash, entry.path)}
                      target="_blank" rel="noreferrer"
                      className="text-blue-400 hover:text-blue-300 text-xs transition-colors"
                    >下载</a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {/* ---- Fork ---- */}
      {collection && (
        <section>
          <details className="group">
            <summary className="cursor-pointer text-sm text-zinc-400 hover:text-zinc-200 transition-colors select-none">
              Fork 此合集
            </summary>
            <form onSubmit={handleFork} className="mt-3 bg-zinc-900 border border-zinc-800 rounded-lg p-4 space-y-3">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-xs text-zinc-500 mb-1">新增文件 Path</label>
                  <input value={forkAddPath} onChange={e => setForkAddPath(e.target.value)} placeholder="new.txt"
                    className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                               focus:outline-none focus:border-zinc-500" />
                </div>
                <div>
                  <label className="block text-xs text-zinc-500 mb-1">新增文件 Hash</label>
                  <input value={forkAddHash} onChange={e => setForkAddHash(e.target.value)} placeholder="sha256..."
                    className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                               focus:outline-none focus:border-zinc-500" />
                </div>
              </div>
              <div>
                <label className="block text-xs text-zinc-500 mb-1">移除文件 Path</label>
                <input value={forkRemovePath} onChange={e => setForkRemovePath(e.target.value)} placeholder="old.txt"
                  className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                             focus:outline-none focus:border-zinc-500" />
              </div>
              <button type="submit"
                className="bg-purple-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-purple-500 transition-colors">
                执行 Fork
              </button>
            </form>
          </details>
        </section>
      )}

      {/* ---- Create ---- */}
      <section>
        <h3 className="text-lg font-semibold mb-4">创建新匿名合集</h3>
        <form onSubmit={handleCreate} className="space-y-3">
          {newEntries.map((entry, idx) => (
            <div key={idx} className="flex gap-2">
              <input
                value={entry.path}
                onChange={e => { const n = [...newEntries]; n[idx].path = e.target.value; setNewEntries(n); }}
                placeholder="相对路径 (如 docs/readme.txt)"
                className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm
                           focus:outline-none focus:border-zinc-500 font-mono placeholder:text-zinc-600"
                required
              />
              <input
                value={entry.hash}
                onChange={e => { const n = [...newEntries]; n[idx].hash = e.target.value; setNewEntries(n); }}
                placeholder="SHA256 Hash"
                className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-3 py-2 text-sm
                           focus:outline-none focus:border-zinc-500 font-mono placeholder:text-zinc-600"
                required
              />
              <button type="button" onClick={() => {
                if (newEntries.length === 1) setNewEntries([{ path: '', hash: '' }]);
                else setNewEntries(newEntries.filter((_, i) => i !== idx));
              }}
                className="text-zinc-600 hover:text-red-400 px-1 text-lg transition-colors">&times;</button>
            </div>
          ))}
          <div className="flex gap-3 pt-1">
            <button type="button" onClick={() => setNewEntries([...newEntries, { path: '', hash: '' }])}
              className="text-sm text-zinc-400 hover:text-zinc-200 px-3 py-1.5 rounded-lg border border-zinc-700 hover:border-zinc-500 transition-colors">
              + 添加条目
            </button>
            <button type="submit" disabled={creating}
              className="bg-white text-black px-4 py-2 rounded-lg text-sm font-medium hover:bg-zinc-200 disabled:opacity-40 transition-colors">
              {creating ? '创建中' : '生成不可变合集'}
            </button>
          </div>
        </form>
      </section>
    </div>
  );
}