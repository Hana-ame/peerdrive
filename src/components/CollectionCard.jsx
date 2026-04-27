import React from 'react';
import { useNavigate } from 'react-router-dom';

function relTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  const now = new Date();
  const diff = Math.floor((now - d) / 1000);
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 604800) return `${Math.floor(diff / 86400)} 天前`;
  return d.toLocaleDateString();
}

function collName(c) {
  return c.collection_name || c.friendly_name || c.name_preview ||
    (c.entries ? `${c.entries.length} 个文件` : (c.entry_count ? `${c.entry_count} 个文件` : '未命名合集'));
}

function collFileCount(c) {
  return c.entry_count || (c.entries ? c.entries.length : 0);
}

function collLink(c) {
  if (c.isDummy) return '';
  if (c.username && c.collection_name) return `/${c.username}/${c.collection_name}`;
  if (c.hash) return `/anon/collections/${c.hash}`;
  return '';
}

export default function CollectionCard({ collection, onFork, onShare, onDownload, viewMode = 'grid' }) {
  const navigate = useNavigate();
  const c = collection;
  const count = collFileCount(c);
  const name = collName(c);
  const time = relTime(c.created_at || c.timestamp);
  const isDummy = c.isDummy;
  const isP2PAvailable = c._type === 'public';
  const link = collLink(c);

  const handleClick = () => {
    if (link) navigate(link);
  };

  if (viewMode === 'list') {
    return (
      <div onClick={handleClick} className="flex items-center gap-4 px-4 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 group">
        <span className="text-xl shrink-0">{isDummy ? '🧪' : '📦'}</span>
        <span className="flex-1 truncate text-sm font-medium text-gray-200">{name}</span>
        <span className="text-xs text-gray-500 shrink-0">{count} 文件</span>
        <span className="text-xs text-gray-500 shrink-0">{time}</span>
        <span className={'text-xs px-2 py-0.5 rounded-full shrink-0 ' + (isP2PAvailable ? 'bg-blue-900/50 text-blue-400' : 'bg-gray-700 text-gray-500')}>
          {isP2PAvailable ? '🔵 P2P可用' : '⚪ 仅本地'}
        </span>
        <div className="flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
          {onFork && <button onClick={(e) => { e.stopPropagation(); onFork(c); }} className="text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2 py-1 rounded">📋 Fork</button>}
          {onShare && <button onClick={(e) => { e.stopPropagation(); onShare(c); }} className="text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2 py-1 rounded">🔗 分享</button>}
          {onDownload && <button onClick={(e) => { e.stopPropagation(); onDownload(c); }} className="text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2 py-1 rounded">⬇ 下载</button>}
        </div>
      </div>
    );
  }

  return (
    <div onClick={handleClick} className={'bg-gray-800 border border-gray-700 rounded-xl p-5 transition-all group ' + (link ? 'cursor-pointer hover:border-blue-500 hover:shadow-lg' : 'cursor-default opacity-80')}>
      <div className="flex items-center justify-between mb-4">
        <div className="w-10 h-10 bg-gray-700 rounded-lg flex items-center justify-center text-blue-400 group-hover:bg-blue-600 group-hover:text-white transition-colors text-lg">
          {isDummy ? '🧪' : '📦'}
        </div>
        <span className="text-xs text-gray-500">{time}</span>
      </div>
      <h3 className="text-lg font-bold truncate text-gray-100">{name}</h3>
      <div className="flex items-center gap-2 mt-2">
        <span className="text-xs text-gray-500">{count} 个文件</span>
        <span className={'text-xs px-2 py-0.5 rounded-full ' + (isP2PAvailable ? 'bg-blue-900/50 text-blue-400' : 'bg-gray-700 text-gray-500')}>
          {isP2PAvailable ? '🔵 P2P可用' : '⚪ 仅本地'}
        </span>
      </div>
      {isDummy && (
        <p className="text-[10px] text-blue-400/60 mt-2 flex items-center gap-1"><span>🔍</span> 来自 P2P 网络的示例合集 -- 连接注册中心获取更多</p>
      )}
      {c.tags && c.tags.length > 0 && (
        <div className="flex flex-wrap gap-1 mt-3">
          {c.tags.map((tag, i) => (<span key={i} className="text-[10px] bg-gray-700 text-gray-300 px-2 py-0.5 rounded-full">{tag}</span>))}
        </div>
      )}
      <div className="flex gap-2 mt-4 pt-3 border-t border-gray-700/50 opacity-0 group-hover:opacity-100 transition-opacity">
        {onFork && <button onClick={(e) => { e.stopPropagation(); onFork(c); }} className="flex-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2 py-1.5 rounded">📋 Fork</button>}
        {onShare && <button onClick={(e) => { e.stopPropagation(); onShare(c); }} className="flex-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2 py-1.5 rounded">🔗 分享</button>}
        {onDownload && <button onClick={(e) => { e.stopPropagation(); onDownload(c); }} className="flex-1 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-2 py-1.5 rounded">⬇ 下载</button>}
      </div>
    </div>
  );
}
