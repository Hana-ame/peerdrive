import React, { useContext, useState, useEffect } from 'react';
import { AppContext } from '../App';
import { useNavigate } from 'react-router-dom';
import { listCollections, createCollection, searchCollections } from '../api';

export default function Plaza() {
  const { username } = useContext(AppContext);
  const [collections, setCollections] = useState([]);
  const [newCollName, setNewCollName] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [isSearching, setIsSearching] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { if (username) loadCollections(); }, [username]);

  const loadCollections = async () => {
    try {
      const res = await listCollections(username);
      setCollections(res.data || []);
    } catch (e) { console.error(e); }
  };

  const handleSearch = async (e) => {
    e.preventDefault();
    if (!searchQuery) {
      loadCollections();
      setIsSearching(false);
      return;
    }
    setIsSearching(true);
    try {
      // Using the search endpoint from api.js
      const data = await searchCollections(searchQuery);
      setCollections(data.data || []);
    } catch (e) {
      console.error(e);
      alert("搜索失败");
    } finally {
      setIsSearching(false);
    }
  };

  const handleCreate = async (e) => {
    e.preventDefault();
    if (!newCollName) return;
    try {
      await createCollection(username, newCollName);
      setNewCollName('');
      loadCollections();
    } catch (err) {
      alert(`创建失败: ${err.message}`);
    }
  };

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center mb-8 gap-4">
          <div>
            <h1 className="text-3xl font-bold">
              {isSearching ? '搜索结果' : 'My Collections'}
            </h1>
            <p className="text-gray-400 text-sm mt-1">
              {isSearching ? `正在搜索 "${searchQuery}"...` : '管理你的内容合集'}
            </p>
          </div>
          
          <div className="flex flex-col sm:flex-row gap-3 w-full md:w-auto">
            <form onSubmit={handleSearch} className="flex space-x-2">
              <input
                type="text" placeholder="搜索合集或用户..."
                value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)}
                className="bg-gray-700 px-4 py-2 rounded text-sm w-full sm:w-64 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button type="submit" className="bg-gray-600 hover:bg-gray-500 px-4 py-2 rounded text-sm font-medium">
                {isSearching ? '...' : '搜索'}
              </button>
            </form>
            
            <form onSubmit={handleCreate} className="flex space-x-2">
              <input
                type="text" placeholder="新合集名称..."
                value={newCollName} onChange={(e) => setNewCollName(e.target.value)}
                className="bg-gray-700 px-4 py-2 rounded text-sm w-full sm:w-48 focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button type="submit" className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium">
                + 新建
              </button>
            </form>
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
          {collections.map(coll => (
            <div
              key={coll.id}
              onClick={() => navigate(`/${coll.username}/${coll.collection_name}`)}
              className="bg-gray-800 border border-gray-700 rounded-xl p-5 cursor-pointer hover:border-blue-500 hover:shadow-lg transition-all group"
            >
              <div className="flex items-center justify-between mb-4">
                <div className="w-10 h-10 bg-gray-700 rounded-lg flex items-center justify-center text-blue-400 group-hover:bg-blue-600 group-hover:text-white transition-colors">
                  📦
                </div>
                <span className="text-xs text-gray-500">{new Date(coll.created_at).toLocaleDateString()}</span>
              </div>
              <h3 className="text-lg font-bold truncate text-gray-100">{coll.collection_name}</h3>
              <p className="text-sm text-gray-400 mt-1">{coll.username}</p>
            </div>
          ))}

          {collections.length === 0 && (
            <div className="col-span-full text-center py-20 text-gray-500 border-2 border-dashed border-gray-700 rounded-xl">
              {isSearching ? '未找到匹配的合集' : '还没有任何合集，点击右上角新建一个吧！'}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
