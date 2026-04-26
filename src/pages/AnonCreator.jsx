import React, { useState, useEffect } from 'react';
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
  const [loading, setLoading] = useState(true);
  const [sortBy, setSortBy] = useState('time');
  const [search, setSearch] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [selectedHashes, setSelectedHashes] = useState(new Set());
  const [friendlyName, setFriendlyName] = useState('');
  const [creating, setCreating] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { loadFiles(); }, [sortBy]);

  const loadFiles = async () => {
    setLoading(true);
    try {
      const data = await api.listFiles(sortBy);
      setFiles(data || []);
    } catch (e) { console.error(e); setFiles([]); }
    setLoading(false);
  };

  const toggle = (hash) => {
    setSelectedHashes(prev => {
      const next = new Set(prev);
      next.has(hash) ? next.delete(hash) : next.add(hash);
      return next;
    });
  };

  const selectAll = () => {
    const s = new Set(selectedHashes);
    filteredFiles.forEach(f => s.add(f.hash));
    setSelectedHashes(s);
  };

  const deselectAll = () => {
    const s = new Set(selectedHashes);
    filteredFiles.forEach(f => s.delete(f.hash));
    setSelectedHashes(s);
  };

  const handleCreate = async () => {
    const selected = files.filter(f => selectedHashes.has(f.hash));
    if (selected.length === 0) return alert('请先选择文件');
    const entries = selected.map(f => ({ path: f.filename, hash: f.hash }));
    setCreating(true);
    try {
      const res = await api.createAnonCollection(entries, friendlyName);
      alert(`创建成功！Hash: ${res.hash}`);
      setSelectedHashes(new Set());
      setFriendlyName('');
      navigate('/anon');
    } catch (err) {
      alert(`创建失败: ${err.message}`);
    } finally {
      setCreating(false);
    }
  };

  const filteredFiles = files.filter(f => {
    if (search && !f.filename?.toLowerCase().includes(search.toLowerCase())) return false;
    if (typeFilter && !f.mime_type?.startsWith(typeFilter)) return false;
    return true;
  });

  const ext = (mime) => {
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
      <div className="flex-1 flex flex-col">
        <div className="h-14 bg-gray-900 border-b border-gray-700 flex items-center px-4 shrink-0 space-x-3">
          <Link to="/anon" className="text-gray-400 hover:text-white text-xs">← 匿名探索</Link>
          <h2 className="text-base font-bold">创建匿名合集</h2>
          <span className="text-[10px] text-gray-600">{files.length} 个文件</span>
        </div>

        <div className="h-10 flex items-center px-4 space-x-3 shrink-0 border-b border-gray-800 bg-gray-900/50">
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
          </select>
          <input value={search} onChange={e => setSearch(e.target.value)}
            placeholder="搜索文件名..."
            className="bg-gray-800 text-xs px-2 py-1 rounded border border-gray-700 w-44 focus:outline-none focus:border-blue-600" />
          <div className="flex-1" />
          <input value={friendlyName} onChange={e => setFriendlyName(e.target.value)}
            placeholder="合集名称 (可选)"
            className="bg-gray-800 text-xs px-2 py-1 rounded border border-gray-700 w-44 focus:outline-none focus:border-blue-600" />
          <button onClick={handleCreate} disabled={creating || selectedHashes.size === 0}
            className="bg-green-600 hover:bg-green-700 disabled:opacity-40 px-4 py-1 rounded text-xs font-medium whitespace-nowrap">
            {creating ? '创建中...' : `生成不可变合集 (${selectedHashes.size})`}
          </button>
        </div>

        <div className="flex-1 overflow-y-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-gray-500 sticky top-0 bg-gray-950">
              <tr>
                <th className="py-2 pl-4 w-8">
                  <input type="checkbox"
                    onChange={e => e.target.checked ? selectAll() : deselectAll()}
                    checked={filteredFiles.length > 0 && filteredFiles.every(f => selectedHashes.has(f.hash))}
                    className="rounded" />
                </th>
                <th className="py-2 pr-2 w-6"></th>
                <th className="py-2">文件名 (合集路径)</th>
                <th className="py-2 w-20">类型</th>
                <th className="py-2 w-24 text-right pr-4">大小</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-800/50">
              {loading ? (
                <tr><td colSpan="5" className="py-10 text-center text-gray-600">加载中...</td></tr>
              ) : filteredFiles.length === 0 ? (
                <tr><td colSpan="5" className="py-10 text-center text-gray-600">
                  {files.length === 0 ? '还没有注册文件，请先去文件管理注册。' : '无匹配文件'}
                </td></tr>
              ) : filteredFiles.map(f => (
                <tr key={f.hash}
                  className={`cursor-pointer hover:bg-gray-800/60 ${selectedHashes.has(f.hash) ? 'bg-blue-900/30' : ''}`}
                  onClick={() => toggle(f.hash)}>
                  <td className="py-1.5 pl-4" onClick={e => e.stopPropagation()}>
                    <input type="checkbox" checked={selectedHashes.has(f.hash)}
                      onChange={() => toggle(f.hash)} className="rounded" />
                  </td>
                  <td className="py-1.5 pr-2">{ext(f.mime_type)}</td>
                  <td className="py-1.5 font-mono text-blue-300 truncate max-w-[400px]" title={f.filename}>{f.filename}</td>
                  <td className="py-1.5 text-gray-600">{f.mime_type?.split('/').pop() || '-'}</td>
                  <td className="py-1.5 text-gray-500 text-right pr-4">{fmtSize(f.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
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
