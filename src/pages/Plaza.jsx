import React, { useContext, useState, useEffect } from 'react';
import { AppContext } from '../App';
import { useNavigate } from 'react-router-dom';
import { listCollections, createCollection } from '../api';

export default function Plaza() {
  const { username } = useContext(AppContext);
  const [collections, setCollections] = useState([]);
  const [newCollName, setNewCollName] = useState('');
  const navigate = useNavigate();

  useEffect(() => {
    if (username) loadCollections();
  }, [username]);

  const loadCollections = async () => {
    try {
      const res = await listCollections(username);
      setCollections(res.data || []);
    } catch (e) { console.error(e); }
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
        <div className="flex justify-between items-center mb-8">
          <h1 className="text-3xl font-bold">My Collections</h1>
          <form onSubmit={handleCreate} className="flex space-x-2">
            <input
              type="text" placeholder="新合集名称..."
              value={newCollName} onChange={(e) => setNewCollName(e.target.value)}
              className="bg-gray-700 px-4 py-2 rounded text-sm w-48 focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <button type="submit" className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium">
              + 新建
            </button>
          </form>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
          {collections.map(coll => (
            <div
              key={coll.id}
              onClick={() => navigate(`/${username}/${coll.collection_name}`)}
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
              还没有任何合集，点击右上角新建一个吧！
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
