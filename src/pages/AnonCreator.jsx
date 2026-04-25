import React, { useState } from 'react';
import * as api from '../api';

export default function AnonCreator({ onCreated }) {
  const [entries, setEntries] = useState([{ path: '', hash: '' }]);
  const [loading, setLoading] = useState(false);

  const handleCreate = async (e) => {
    e.preventDefault();
    const filtered = entries.filter(entry => entry.path && entry.hash);
    if (filtered.length === 0) return alert("条目不能为空");

    setLoading(true);
    try {
      const res = await api.createAnonCollection(filtered);
      alert(`创建成功！Hash: ${res.hash}`);
      setEntries([{ path: '', hash: '' }]);
      if (onCreated) onCreated(res.hash);
    } catch (err) {
      alert(`创建失败: ${err.response?.data?.error || err.message}`);
    } finally {
      setLoading(false);
    }
  };

  const addEntryRow = () => {
    setEntries([...entries, { path: '', hash: '' }]);
  };

  const updateEntry = (idx, field, value) => {
    const newEntries = [...entries];
    newEntries[idx][field] = value;
    setEntries(newEntries);
  };

  const removeEntry = (idx) => {
    if (entries.length === 1) {
      setEntries([{ path: '', hash: '' }]);
    } else {
      setEntries(entries.filter((_, i) => i !== idx));
    }
  };

  return (
    <div className="p-6 max-w-4xl mx-auto">
      <h2 className="text-2xl font-bold mb-4 flex items-center">
        ➕ 创建匿名合集
      </h2>
      <p className="text-gray-400 text-sm mb-4">
        创建不可变的匿名合集。创建后无法修改，只能通过 Fork 生成新版本。
      </p>

      <form onSubmit={handleCreate} className="space-y-3">
        {entries.map((entry, idx) => (
          <div key={idx} className="flex gap-2 items-center">
            <input
              value={entry.path}
              onChange={(e) => updateEntry(idx, 'path', e.target.value)}
              placeholder="相对路径 (如 docs/readme.txt)"
              className="flex-1 bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm focus:outline-none focus:border-blue-500"
              required
            />
            <input
              value={entry.hash}
              onChange={(e) => updateEntry(idx, 'hash', e.target.value)}
              placeholder="文件 SHA256 Hash"
              className="flex-1 bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500"
              required
            />
            <button
              type="button"
              onClick={() => removeEntry(idx)}
              className="text-gray-500 hover:text-red-400 text-xl px-2"
              title="删除此行"
            >
              ×
            </button>
          </div>
        ))}

        <div className="flex gap-3 pt-2">
          <button
            type="button"
            onClick={addEntryRow}
            className="bg-gray-700 hover:bg-gray-600 px-4 py-2 rounded text-sm"
          >
            + 添加条目
          </button>
          <button
            type="submit"
            disabled={loading}
            className="bg-green-600 hover:bg-green-700 px-6 py-2 rounded text-sm font-medium disabled:opacity-50"
          >
            {loading ? '创建中...' : '生成不可变合集'}
          </button>
        </div>
      </form>
    </div>
  );
}