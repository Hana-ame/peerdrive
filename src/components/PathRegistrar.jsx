import React, { useState } from 'react';
import * as api from '../api';

export default function PathRegistrar({ onFilesRegistered }) {
  const [path, setPath] = useState('');
  const [mode, setMode] = useState('folder'); // folder | file
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');

  const handleRegister = async (e) => {
    e.preventDefault();
    if (!path.trim()) return;
    setLoading(true);
    setError('');
    setResult(null);
    try {
      let res;
      if (mode === 'folder') {
        res = await api.registerFolder(path.trim());
        // returns { registered: [{path, hash, filename}, ...] }
        setResult(res.registered);
        if (onFilesRegistered && res.registered?.length > 0) {
          onFilesRegistered(res.registered);
        }
      } else {
        res = await api.registerLocalFile(path.trim());
        // returns { hash, filename }
        setResult([res]);
        if (onFilesRegistered) {
          onFilesRegistered([res]);
        }
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl mx-auto">
      <h2 className="text-xl font-semibold mb-2">注册本地路径</h2>
      <p className="text-zinc-400 text-sm mb-6">将服务器上已有的文件或目录纳入内容寻址系统。</p>

      <form onSubmit={handleRegister}>
        <div className="flex gap-2 mb-3">
          <input
            value={path}
            onChange={e => setPath(e.target.value)}
            placeholder={mode === 'folder' ? '服务器目录路径，如 /storage/mydata/' : '服务器文件绝对路径，如 /storage/test.txt'}
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
        </div>

        <div className="flex gap-3 text-sm">
          <label className={`flex items-center gap-1.5 cursor-pointer ${mode === 'folder' ? 'text-white' : 'text-zinc-500'}`}>
            <input type="radio" name="regmode" checked={mode === 'folder'} onChange={() => setMode('folder')} className="accent-white" />
            文件夹
          </label>
          <label className={`flex items-center gap-1.5 cursor-pointer ${mode === 'file' ? 'text-white' : 'text-zinc-500'}`}>
            <input type="radio" name="regmode" checked={mode === 'file'} onChange={() => setMode('file')} className="accent-white" />
            单个文件
          </label>
        </div>
      </form>

      {error && (
        <div className="mt-4 bg-red-900/20 border border-red-900/40 rounded-lg px-4 py-3 text-sm text-red-400">
          {error}
        </div>
      )}

      {result && result.length > 0 && (
        <div className="mt-4 bg-emerald-900/20 border border-emerald-900/40 rounded-lg p-5">
          <p className="text-sm text-emerald-300 mb-3">注册成功 &mdash; {result.length} 个文件</p>
          <div className="max-h-48 overflow-y-auto space-y-1 mb-4">
            {result.map((f, i) => (
              <div key={i} className="text-xs font-mono text-emerald-200/70 flex gap-4">
                <span className="truncate max-w-[12rem]">{f.filename || f.path}</span>
                <span className="text-emerald-400/50 truncate">{f.hash}</span>
              </div>
            ))}
          </div>
          {onFilesRegistered && (
            <button
              onClick={() => onFilesRegistered(result)}
              className="bg-emerald-500 text-black px-4 py-2 rounded-lg text-sm font-medium
                         hover:bg-emerald-400 transition-colors"
            >
              用这些文件创建匿名合集 &rarr;
            </button>
          )}
        </div>
      )}
    </div>
  );
}