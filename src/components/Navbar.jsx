import React, { useContext } from 'react';
import { AppContext } from '../App';
import { Link } from 'react-router-dom';

export default function Navbar() {

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
        <Link to="/settings" className="text-gray-500 hover:text-gray-300 text-sm" title="设置">⚙</Link>
      </div>
    </nav>
  );
}
