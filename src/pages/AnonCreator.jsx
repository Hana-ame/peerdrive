import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';
import { Link, useNavigate } from 'react-router-dom';

const SORT_OPTIONS = [
  { value: 'time', label: '时间' },
  { value: 'name', label: '文件名' },
  { value: 'path', label: '目录' },
  { value: 'type', label: '类型' },
  { value: 'size', label: '大小' },
];

export default function AnonCreator() {
  const [files, setFiles] = useState([]);
  const [fileLoading, setFileLoading] = useState(true);
  const [sortBy, setSortBy] = useState('time');
  const [search, setSearch] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [selectedHashes, setSelectedHashes] = useState(new Set());
  const [entries, setEntries] = useState([]);
  const [friendlyName, setFriendlyName] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { loadFiles(); }, [sortBy]);

  const loadFiles = async () => {
    setFileLoading(true);
    try {
      const data = await api.listFiles(sortBy);
      setFiles(data || []);
    } catch (e) {
      console.error(e);
      setFiles([]);
    }
    setFileLoading(false);
  };

  const toggleSelectFile = (hash) => {
    setSelectedHashes(prev => {
      const next = new Set(prev);
      next.has(hash) ? next.delete(hash) : next.add(hash);
      return next;
    });
  };

  const selectAllMatching = () => {
    const newSet = new Set(selectedHashes);
    filteredFiles.forEach(f => newSet.add(f.hash));
    setSelectedHashes(newSet);
  };

  const deselectAllMatching = () => {
    const newSet = new Set(selectedHashes);
    filteredFiles.forEach(f => newSet.delete(f.hash));
    setSelectedHashes(newSet);
  };

  const addToRight = () => {
    const selected = files.filter(f => selectedHashes.has(f.hash));
    const existingHashes = new Set(entries.map(e => e.hash));
    const newEntries = selected
      .filter(f => !existingHashes.has(f.hash))
      .map(f => ({ path: f.filename || '', hash: f.hash }));
    setEntries([...entries, ...newEntries]);
    setSelectedHashes(new Set());
  };

  const removeEntry = (idx) => setEntries(entries.filter((_, i) => i !== idx));

  const updateEntryPath = (idx, value) => {
    const next = [...entries];
    next[idx] = { ...next[idx], path: value };
    setEntries(next);
  };

  const handleCreate = async (e) => {
    e.preventDefault();
    const valid = entries.filter(e => e.path.trim() && e.hash);
    if (valid.length === 0) return alert('条目不能为空，请先从左侧选择文件并设置路径');
    setLoading(true);
    try {
      const res = await api.createAnonCollection(valid, friendlyName);
      alert(`创建成功！Hash: ${res.hash}`);
      setEntries([]);
      setSelectedHashes(new Set());
      navigate('/anon');
    } catch (err) {
      alert(`创建失败: ${err.message}`);
    } finally {
      setLoading(false);
    }
  };

  const filteredFiles = files.filter(f => {
    const matchSearch = !search || f.filename?.toLowerCase().includes(search.toLowerCase());
    const matchType = !typeFilter || f.mime_type?.startsWith(typeFilter);
    return matchSearch && matchType;
  });

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

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      {/* === LEFT: file browser === */}
      <div className="w-[55%] flex flex-col border-r border-gray-700">
        <div className="h-14 bg-gray-900 border-b border-gray-700 flex items-center px-4 shrink-0 space-x-3">
          <Link to="/anon" className="text-gray-400 hover:text-white text-xs">← 匿名探索</Link>
          <h2 className="text-base font-bold">创建匿名合集</h2>
          <span className="text-[10px] text-gray-600">{files.length} 个文件</span>
        </div>

        <div className="h-10 bg-gray-850 border-b border-gray-800 flex items-center px-4 space-x-3 shrink-0">
          <select value={sortBy} onChange={e => setSortBy(e.target.value)}
            className="bg-gray-800 text-xs px-2 py-1 rounded border border-gray-700">
            {SORT_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label} ↓</option>)}
          </select>
          <select value={typeFilter} onChange={e => setTypeFilter(e.target.value)}
            className="bg-gray-800 text-xs px-2 py-1 rounded border border-gray-700">
            <option value="">全部类型</option>
            <option value="image/">图片</option>
            <option value="video/">视频</option>
            <option value="audio/">音频</option>
            <option value="text/">文本</option>
            <option value="application/pdf">PDF</option>
          </select>
          <input value={search} onChange={e => setSearch(e.target.value)}
            placeholder="搜索文件名..."
            className="bg-gray-800 text-xs px-2 py-1 rounded border border-gray-700 w-40 focus:outline-none focus:border-blue-600" />
        </div>

        <div className="flex-1 overflow-y-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-gray-500 sticky top-0 bg-gray-900">
              <tr>
                <th className="py-2 pl-4 w-8">
                  <input type="checkbox" onChange={e => e.target.checked ? selectAllMatching() : deselectAllMatching()}
                    checked={filteredFiles.length > 0 && filteredFiles.every(f => selectedHashes.has(f.hash))}
                    className="rounded" />
                </th>
                <th className="py-2 pr-2 w-6"></th>
                <th className="py-2">文件名</th>
                <th className="py-2 w-20">类型</th>
                <th className="py-2 w-20 text-right">大小</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-800/50">
              {fileLoading ? (
                <tr><td colSpan="5" className="py-8 text-center text-gray-600">加载中...</td></tr>
              ) : filteredFiles.length === 0 ? (
                <tr><td colSpan="5" className="py-8 text-center text-gray-600">
                  {files.length === 0 ? '还没有注册文件，请先去文件管理注册。' : '无匹配文件'}
                </td></tr>
              ) : filteredFiles.map(f => (
                <tr key={f.hash}
                  className={`cursor-pointer hover:bg-gray-800/60 ${selectedHashes.has(f.hash) ? 'bg-blue-900/30' : ''}`}
                  onClick={() => toggleSelectFile(f.hash)}>
                  <td className="py-1.5 pl-4" onClick={e => e.stopPropagation()}>
                    <input type="checkbox" checked={selectedHashes.has(f.hash)}
                      onChange={() => toggleSelectFile(f.hash)} className="rounded" />
                  </td>
                  <td className="py-1.5 pr-2">{extIcon(f.mime_type)}</td>
                  <td className="py-1.5 font-mono text-blue-300 truncate max-w-[260px]" title={f.filename}>{f.filename}</td>
                  <td className="py-1.5 text-gray-600">{f.mime_type?.split('/').pop() || '-'}</td>
                  <td className="py-1.5 text-gray-500 text-right pr-2">{fmtSize(f.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="h-12 bg-gray-900 border-t border-gray-700 flex items-center px-4 shrink-0">
          <button onClick={addToRight}
            disabled={selectedHashes.size === 0}
            className="bg-blue-600 hover:bg-blue-500 disabled:opacity-40 px-4 py-1.5 rounded text-xs font-medium">
              添加到右侧 → ({selectedHashes.size})
          </button>
        </div>
      </div>

      {/* === RIGHT: entry list === */}
      <div className="w-[45%] flex flex-col">
        <div className="h-14 bg-gray-900 border-b border-gray-700 flex items-center px-4 shrink-0 space-x-4">
          <span className="text-sm font-bold">已选条目</span>
          <span className="text-[10px] text-gray-500">{entries.length} 条</span>
        </div>

        <div className="px-4 pt-4 shrink-0">
          <input value={friendlyName} onChange={e => setFriendlyName(e.target.value)}
            placeholder="合集名称 (可选)"
            className="w-full bg-gray-800 border border-gray-700 px-3 py-2 rounded text-sm focus:outline-none focus:border-blue-500" />
        </div>

        <div className="flex-1 overflow-y-auto px-4 pt-3 pb-4">
          {entries.length === 0 ? (
            <div className="text-center text-gray-600 text-xs mt-20">
              ← 从左侧勾选文件，点击"添加到右侧"
            </div>
          ) : entries.map((entry, idx) => (
            <div key={`${entry.hash}-${idx}`} className="flex items-center gap-2 mb-2 bg-gray-800/50 rounded p-2">
              <div className="flex-1 flex items-center gap-2">
                <span className="text-[10px] font-mono text-blue-400 truncate w-16"
                  title={entry.hash}>{entry.hash.substring(0, 8)}</span>
                <input value={entry.path} onChange={e => updateEntryPath(idx, e.target.value)}
                  placeholder="相对路径 (如 docs/readme.txt)"
                  className="flex-1 bg-gray-700 text-xs px-2 py-1 rounded border border-gray-600 focus:outline-none focus:border-blue-500" />
              </div>
              <button onClick={() => removeEntry(idx)}
                className="text-gray-500 hover:text-red-400 text-sm px-1 shrink-0">×</button>
            </div>
          ))}
        </div>

        <div className="h-14 bg-gray-900 border-t border-gray-700 flex items-center px-4 shrink-0 justify-end">
          <button onClick={handleCreate} disabled={loading || entries.length === 0}
            className="bg-green-600 hover:bg-green-700 disabled:opacity-40 px-5 py-2 rounded text-sm font-medium">
              {loading ? '创建中...' : '生成不可变合集'}
          </button>
        </div>
      </div>
    </div>
  );
}

function fmtSize(bytes) {
  if (!bytes) return '-';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
  if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB';
  return (bytes / 1073741824).toFixed(1) + ' GB';
}
