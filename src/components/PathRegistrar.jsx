import React, { useState } from 'react';
import * as api from '../api';

export default function PathRegistrar({ onHashGenerated }) {
  const [path, setPath] = useState('');
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');

  const handleRegister = async (e) => {
    e.preventDefault();
    if (!path.trim()) return;
    setLoading(true);
    setError('');
    try {
      const res = await api.registerLocalFile(path.trim());
      setResult(res);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl mx-auto">
      <h2 className="text-xl font-semibold mb-2">注册本地路径</h2>
      <p className="text-zinc-400 text-sm mb-6">将服务器上已有的文件纳入内容寻址系统。</p>

      <form onSubmit={handleRegister} className="flex gap-2 mb-4">
        <input
          value={path}
          onChange={e => setPath(e.target.value)}
          placeholder="服务器绝对路径，如 /storage/test.txt"
          className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-4 py-2.5 text-sm
                     focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 font-mono"
        />
        <button
          type="submit"
          disabled={loading}
          className="bg-white text-black px-5 py-2.5 rounded-lg text-sm font-medium
                     hover:bg-zinc-200 disabled:opacity-40 transition-colors whitespace-nowrap"
        >
          {loading ? '注册中' : '注册'}
        </button>
      </form>

      {error && (
        <div className="bg-red-900/20 border border-red-900/40 rounded-lg px-4 py-3 text-sm text-red-400 mb-4">
          {error}
        </div>
      )}

      {result && (
        <div className="bg-emerald-900/20 border border-emerald-900/40 rounded-lg p-5">
          <p className="text-sm text-emerald-300 mb-3">
            注册成功 &mdash; Hash: <span className="font-mono text-emerald-200">{result.hash}</span>
          </p>
          <button
            onClick={() => onHashGenerated(result.hash)}
            className="bg-emerald-500 text-black px-4 py-2 rounded-lg text-sm font-medium
                       hover:bg-emerald-400 transition-colors"
          >
            用此 Hash 创建匿名合集 &rarr;
          </button>
        </div>
      )}
    </div>
  );
}