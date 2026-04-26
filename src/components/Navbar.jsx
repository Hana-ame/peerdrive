import React, { useContext, useState, useEffect } from 'react';
import { AppContext } from '../App';
import { Link, useNavigate } from 'react-router-dom';
import { getNodeInfo } from '../api';

export default function Navbar() {
  const { username, setUsername, nodeInfo, setNodeInfo } = useContext(AppContext);
  const [nodeError, setNodeError] = useState(false);
  const navigate = useNavigate();

  useEffect(() => { loadP2P(); }, []);

  const loadP2P = async () => {
    try {
      setNodeInfo(await getNodeInfo());
      setNodeError(false);
    } catch(e) {
      setNodeError(true);
    }
  };

  return (
    <nav className="h-14 bg-gray-800 border-b border-gray-700 flex items-center px-6 justify-between shrink-0">
      <div className="flex items-center space-x-4">
        <Link to="/" className="text-xl font-bold text-blue-400 hover:text-blue-300">Peerdrive</Link>
        <div className="flex items-center space-x-1">
          <Link to="/files" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700">文件管理</Link>
          <Link to="/anon" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700">探索合集</Link>
          <Link to="/anon/create" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700">创建合集</Link>
        </div>
      </div>

      <div className="flex items-center space-x-3">
        {nodeError && (
          <Link to="/settings" className="text-xs text-red-400 hover:text-red-300 flex items-center space-x-1">
            <span className="w-1.5 h-1.5 bg-red-400 rounded-full"></span>
            <span>节点未连接</span>
          </Link>
        )}
        {nodeInfo && (
          <span className="text-xs text-green-500/50">已连接</span>
        )}
        <div className="flex items-center space-x-1 text-sm">
          <input
            type="text" value={username} onChange={(e) => setUsername(e.target.value)}
            className="bg-gray-700 px-2 py-1 rounded w-28 text-center text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            placeholder="用户名"
          />
        </div>
        <Link to="/settings" className="text-gray-500 hover:text-gray-300 text-sm" title="设置">⚙</Link>
      </div>
    </nav>
  );
}
