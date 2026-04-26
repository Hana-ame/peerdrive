import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { listAnonCollections, listPublicCollections } from '../api';

const TABS = [
  { key: 'anon', label: '我的合集' },
  { key: 'public', label: '公开合集' },
];

const DUMMY_COLLECTIONS = [
  { hash: 'demo-1', friendly_name: '🧪 示例图片集', version: 1, isDummy: true, entries: [{ path: 'cat.jpg' }, { path: 'dog.png' }] },
  { hash: 'demo-2', friendly_name: '🧪 示例文档集', version: 2, isDummy: true, entries: [{ path: 'readme.md' }, { path: 'notes.txt' }] },
];

function SkeletonCard() {
  return (
    <div className="bg-gray-800 border border-gray-700 rounded-xl p-5 animate-pulse">
      <div className="flex items-center justify-between mb-4">
        <div className="w-10 h-10 bg-gray-700 rounded-lg" />
        <div className="w-12 h-3 bg-gray-700 rounded" />
      </div>
      <div className="w-3/4 h-5 bg-gray-700 rounded mb-2" />
      <div className="w-1/2 h-3 bg-gray-700 rounded" />
    </div>
  );
}

export default function Plaza() {
  const [collections, setCollections] = useState([]);
  const [tab, setTab] = useState('anon');
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
      } else {
        data = await listPublicCollections();
        setCollections(data.collections || data.data || data || []);
      }
    } catch { setCollections([]); }
    setLoading(false);
  };

  const collName = (c) => c.collection_name || c.friendly_name || (c.hash ? c.hash.substring(0, 12) + '...' : '未命名');
  const collUser = (c) => c.username || (!c.isDummy ? '匿名' : 'Peerdrive');
  const collTime = (c) => {
    if (c.isDummy) return '';
    const t = c.created_at || c.timestamp;
    return t ? new Date(t).toLocaleDateString() : '';
  };
  const collLink = (c) => {
    if (c.isDummy) return '';
    if (c.username && c.collection_name) return `/${c.username}/${c.collection_name}`;
    if (c.hash) return `/anon/collections/${c.hash}`;
    return '';
  };
  const collId = (c) => c.id || c.hash || c.collection_name;

  const showDummies = tab === 'anon' && collections.length === 0 && !loading;
  const display = showDummies ? DUMMY_COLLECTIONS : collections;

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center mb-8 gap-4">
          <h1 className="text-3xl font-bold">
            {tab === 'anon' ? '我的合集' : '公开合集'}
          </h1>
          <div className="flex bg-gray-800 rounded-lg p-1">
            {TABS.map(t => (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                className={`px-4 py-1.5 rounded-md text-sm font-medium transition-colors ${tab === t.key ? 'bg-gray-600 text-white' : 'text-gray-400 hover:text-white'}`}
              >
                {t.label}
              </button>
            ))}
          </div>
        </div>

        {loading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
            {Array.from({ length: 8 }).map((_, i) => <SkeletonCard key={i} />)}
          </div>
        ) : display.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-gray-700 rounded-xl">
            {tab === 'anon' ? (
              <div>
                <p className="mb-3">还没有创建任何合集</p>
                <p className="text-xs text-gray-600 mb-4">从文件管理器注册文件并自动创建匿名合集，或手动创建</p>
                <div className="flex justify-center gap-3">
                  <button onClick={() => navigate('/anon/create')} className="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded text-sm font-medium">
                    + 创建合集
                  </button>
                  <button onClick={() => navigate('/files')} className="bg-gray-700 hover:bg-gray-600 text-gray-300 px-4 py-2 rounded text-sm">
                    浏览文件管理器
                  </button>
                </div>
              </div>
            ) : (
              <div>
                <p className="mb-2">暂无公开合集</p>
                <p className="text-xs text-gray-600 mb-4">公开合集由 Peerdrive 注册中心或 P2P 网络提供</p>
                <div className="flex justify-center gap-3">
                  <button onClick={() => navigate('/anon/create')} className="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded text-sm">
                    创建合集并设为公开
                  </button>
                  <button className="bg-gray-700 hover:bg-gray-600 text-gray-300 px-4 py-2 rounded text-sm" disabled>
                    🔗 连接到注册中心
                  </button>
                </div>
              </div>
            )}
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
            {display.map(c => (
              <div
                key={collId(c)}
                onClick={() => {
                  const link = collLink(c);
                  if (link) navigate(link);
                }}
                className={`bg-gray-800 border border-gray-700 rounded-xl p-5 transition-all group ${collLink(c) ? 'cursor-pointer hover:border-blue-500 hover:shadow-lg' : 'cursor-default opacity-80'}`}
              >
                <div className="flex items-center justify-between mb-4">
                  <div className="w-10 h-10 bg-gray-700 rounded-lg flex items-center justify-center text-blue-400 group-hover:bg-blue-600 group-hover:text-white transition-colors text-lg">
                    {c.isDummy ? '🧪' : '📦'}
                  </div>
                  <span className="text-xs text-gray-500">{collTime(c)}</span>
                </div>
                <h3 className="text-lg font-bold truncate text-gray-100">{collName(c)}</h3>
                <p className="text-sm text-gray-400 mt-1">{collUser(c)}</p>
                {c.isDummy && (
                  <p className="text-[10px] text-blue-400/60 mt-2 flex items-center gap-1">
                    <span>🔍</span> 来自 P2P 网络的示例合集 — 连接注册中心获取更多
                  </p>
                )}
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
