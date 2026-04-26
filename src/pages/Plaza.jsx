import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { listAnonCollections, listPublicCollections, searchCollections } from '../api';

const TABS = [
  { key: 'anon', label: '我的合集' },
  { key: 'public', label: '公开合集' },
  { key: 'search', label: '搜索' },
];

export default function Plaza() {
  const [collections, setCollections] = useState([]);
  const [tab, setTab] = useState('anon');
  const [searchQuery, setSearchQuery] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { loadTab(); }, [tab]);

  const loadTab = async () => {
    setLoading(true);
    try {
      let data;
      if (tab === 'anon') {
        data = await listAnonCollections();
        setCollections(data || []);
      } else if (tab === 'public') {
        data = await listPublicCollections();
        setCollections(data.collections || data.data || data || []);
      } else {
        setCollections(searchQuery ? [] : []);
      }
    } catch (e) { console.error(e); setCollections([]); }
    setLoading(false);
  };

  const handleSearch = async (e) => {
    e.preventDefault();
    if (!searchQuery.trim()) return;
    setTab('search');
    setLoading(true);
    try {
      const data = await searchCollections(searchQuery.trim());
      setCollections(data.collections || data.data || []);
    } catch (e) { console.error(e); }
    setLoading(false);
  };

  const collName = (c) => c.collection_name || c.friendly_name || (c.hash ? c.hash.substring(0, 12) + '...' : '未命名');
  const collUser = (c) => c.username || '匿名';
  const collTime = (c) => {
    const t = c.created_at || c.timestamp;
    return t ? new Date(t).toLocaleDateString() : '';
  };
  const collLink = (c) => {
    if (c.username && c.collection_name) return `/${c.username}/${c.collection_name}`;
    if (c.hash) return `/anon/collections/${c.hash}`;
    return '';
  };
  const collId = (c) => c.id || c.hash || c.collection_name;

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center mb-8 gap-4">
          <h1 className="text-3xl font-bold">
            {tab === 'anon' ? '我的合集' : tab === 'search' ? '搜索结果' : '公开合集'}
          </h1>

          <div className="flex items-center gap-3">
            <div className="flex bg-gray-800 rounded-lg p-1">
              {TABS.map(t => (
                <button
                  key={t.key}
                  onClick={() => { setTab(t.key); setSearchQuery(''); }}
                  className={`px-4 py-1.5 rounded-md text-sm font-medium transition-colors ${tab === t.key ? 'bg-gray-600 text-white' : 'text-gray-400 hover:text-white'}`}
                >
                  {t.label}
                </button>
              ))}
            </div>

            {tab === 'search' && (
              <form onSubmit={handleSearch} className="flex space-x-2">
                <input
                  type="text" placeholder="搜索合集或用户名..."
                  value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)}
                  className="bg-gray-700 px-4 py-2 rounded text-sm w-48 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
                <button type="submit" className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm">搜索</button>
              </form>
            )}
          </div>
        </div>

        {loading ? (
          <div className="text-center py-20 text-gray-500">加载中...</div>
        ) : collections.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-gray-700 rounded-xl">
            {tab === 'anon' ? (
              <div>
                <p className="mb-2">还没有创建任何合集</p>
                <button onClick={() => navigate('/anon/create')} className="text-blue-400 hover:underline text-sm">
                  → 创建一个匿名合集
                </button>
              </div>
            ) : tab === 'search' ? (
              <div>
                <p className="mb-2">未找到匹配结果</p>
                <p className="text-xs">试试搜索用户名或合集名称</p>
              </div>
            ) : (
              <p>暂无公开合集</p>
            )}
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
            {collections.map(c => (
              <div
                key={collId(c)}
                onClick={() => navigate(collLink(c))}
                className="bg-gray-800 border border-gray-700 rounded-xl p-5 cursor-pointer hover:border-blue-500 hover:shadow-lg transition-all group"
              >
                <div className="flex items-center justify-between mb-4">
                  <div className="w-10 h-10 bg-gray-700 rounded-lg flex items-center justify-center text-blue-400 group-hover:bg-blue-600 group-hover:text-white transition-colors">
                    📦
                  </div>
                  <span className="text-xs text-gray-500">{collTime(c)}</span>
                </div>
                <h3 className="text-lg font-bold truncate text-gray-100">{collName(c)}</h3>
                <p className="text-sm text-gray-400 mt-1">{collUser(c)}</p>
                {c.tags && c.tags.length > 0 && (
                  <div className="flex flex-wrap gap-1 mt-3">
                    {c.tags.map((tag, i) => (
                      <span key={i} className="text-[10px] bg-gray-700 text-gray-300 px-2 py-0.5 rounded-full">
                        {tag}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
