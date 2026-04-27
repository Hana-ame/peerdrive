// SHA256 寻址查询页：通过哈希值查询文件元数据并下载
import React, { useState } from 'react';
import * as api from '../api';

export default function Sha256Manager() {
  const [hash, setHash] = useState('');
  const [meta, setMeta] = useState(null);
  const [loading, setLoading] = useState(false);

  const handleVerify = async (e) => {
    e.preventDefault();
    if (!hash.trim()) return;
    setLoading(true);
    try {
      const res = await api.verifyFile(hash.trim());
      setMeta(res);
    } catch {
      setMeta({ error: '未找到此哈希对应的文件' });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="max-w-2xl mx-auto">
      <h2 className="text-xl font-semibold mb-6">SHA256 寻址</h2>
      <p className="text-zinc-400 text-sm mb-6">
        输入文件的 SHA256 哈希值，查询元数据或直接下载。
      </p>

      <form onSubmit={handleVerify} className="flex gap-2 mb-6">
        <input
          value={hash}
          onChange={e => setHash(e.target.value)}
          placeholder="e3b0c44298fc1c149afbf4c8996fb924..."
          className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-4 py-2.5 text-sm
                     focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 font-mono"
        />
        <button
          type="submit"
          disabled={loading}
          className="bg-white text-black px-5 py-2.5 rounded-lg text-sm font-medium
                     hover:bg-zinc-200 disabled:opacity-40 transition-colors"
        >
          {loading ? '查询中' : '查询'}
        </button>
      </form>

      {meta && !meta.error && (
        <div className="bg-zinc-900 border border-zinc-800 rounded-lg p-5">
          <table className="w-full text-sm mb-4">
            <tbody className="divide-y divide-zinc-800">
              {Object.entries(meta).map(([k, v]) => (
                <tr key={k}>
                  <td className="py-2 text-zinc-500 w-24">{k}</td>
                  <td className="py-2 font-mono text-zinc-300 break-all">{String(v)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <a
            href={api.getDownloadUrl(hash)}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-2 bg-white text-black px-4 py-2 rounded-lg text-sm font-medium
                       hover:bg-zinc-200 transition-colors"
          >
            下载文件
          </a>
        </div>
      )}

      {meta?.error && (
        <div className="bg-red-900/20 border border-red-900/40 rounded-lg px-4 py-3 text-sm text-red-400">
          {meta.error}
        </div>
      )}
    </div>
  );
}