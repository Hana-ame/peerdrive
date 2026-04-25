import React, { useContext, useState, useEffect } from 'react';
import { AppContext } from '../App';
import { Link, useLocation } from 'react-router-dom';
import { getNodeInfo } from '../api';

export default function Navbar() {
  const { username, setUsername } = useContext(AppContext);
  const location = useLocation();
  const [nodeInfo, setNodeInfo] = useState(null);

  useEffect(() => { loadP2P(); }, []);

  const loadP2P = async () => {
    try { setNodeInfo(await getNodeInfo()); } catch(e) {}
  };

  const pathParts = location.pathname.split('/').filter(Boolean);

  return (
    <nav className="h-14 bg-gray-800 border-b border-gray-700 flex items-center px-6 justify-between shrink-0">
      <div className="flex items-center space-x-4">
        <Link to="/" className="text-xl font-bold text-blue-400 hover:text-blue-300">Peerdrive</Link>
        {pathParts.length === 2 && (
          <div className="flex items-center text-sm text-gray-400">
            <span className="mx-2">/</span>
            <span className="text-gray-300">{pathParts[0]}</span>
            <span className="mx-2">/</span>
            <span className="text-white font-medium">{pathParts[1]}</span>
          </div>
        )}
      </div>

      <div className="flex items-center space-x-4">
        <div className="flex items-center space-x-2 text-sm">
          <label className="text-gray-400">节点用户:</label>
          <input
            type="text" value={username} onChange={(e) => setUsername(e.target.value)}
            className="bg-gray-700 px-2 py-1 rounded w-24 text-center text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        {nodeInfo && (
          <div className="text-xs text-green-400 flex items-center space-x-1">
            <span className="w-2 h-2 bg-green-400 rounded-full animate-pulse"></span>
            <span>P2P Online</span>
          </div>
        )}
      </div>
    </nav>
  );
}
