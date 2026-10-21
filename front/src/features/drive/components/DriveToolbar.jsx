import React from 'react';
import { Link } from 'react-router-dom';

export default function DriveToolbar({
  sortBy,
  setSortBy,
  sortOrder,
  setSortOrder,
  viewMode,
  setViewMode,
  busy,
  onPick,
  fileRef,
  fileCount = 0,
  totalBytes = '0 B',
  isRemotePeer = false,
  peerId = '',
}) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2 sm:gap-3">
          <h1 className="text-xl sm:text-2xl font-bold flex items-center gap-2 truncate">
            <span>☁️ My Cloud Drive</span>
          </h1>
          <div className="flex items-center bg-white/[0.06] p-0.5 rounded-lg text-xs shrink-0 border border-white/[0.08]">
            <span className="px-2.5 py-1 rounded bg-brand-600 text-white font-medium">Files</span>
            <Link to="/collections" className="px-2.5 py-1 rounded text-gray-400 hover:text-white transition-colors">Collections</Link>
          </div>
          <span className="px-2.5 py-0.5 rounded-full text-[11px] bg-white/[0.04] border border-white/[0.08] text-gray-300 font-mono">
            {fileCount} 项 · {totalBytes}
          </span>
        </div>
        <p className="text-xs sm:text-sm text-gray-500 mt-0.5 truncate">
          {isRemotePeer
            ? `⚡ 正在浏览远程节点 (${peerId.slice(0, 12)}) 共享的公开网盘 · 支持在线预览与一键转存`
            : 'Files registered on this node (content-addressed storage · 支持直接拖拽文件到此页面上传)'}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2 max-w-full">
        {/* Sorting controls */}
        <div className="flex items-center bg-white/[0.04] p-0.5 rounded-lg border border-white/[0.06] text-xs shrink-0">
          <span className="text-gray-500 pl-2 pr-1 text-[11px]">Sort:</span>
          <select
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value)}
            className="bg-transparent text-gray-300 text-xs py-1 pr-1 outline-none cursor-pointer"
            title="Sort field"
          >
            <option value="time" className="bg-gray-900 text-white">Date</option>
            <option value="name" className="bg-gray-900 text-white">Name</option>
            <option value="size" className="bg-gray-900 text-white">Size</option>
          </select>
          <button
            onClick={() => setSortOrder(o => o === 'asc' ? 'desc' : 'asc')}
            className="px-2 py-1 text-gray-400 hover:text-white transition-colors text-xs font-mono"
            title={sortOrder === 'asc' ? 'Ascending (click for Descending)' : 'Descending (click for Ascending)'}
          >
            {sortOrder === 'asc' ? '▲' : '▼'}
          </button>
        </div>
        {/* View mode toggle */}
        <div className="flex items-center bg-white/[0.04] p-0.5 rounded-lg border border-white/[0.06] text-xs shrink-0">
          <button
            onClick={() => setViewMode('list')}
            className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'list' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
            title="Detailed Information List"
          >
            ☰ Details
          </button>
          <button
            onClick={() => setViewMode('grid')}
            className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'grid' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
            title="Thumbnail Cards View"
          >
            ▦ Thumbnails
          </button>
        </div>
        {!isRemotePeer && (
          <button
            onClick={() => fileRef.current?.click()}
            disabled={busy}
            className="btn-brand shrink-0 text-xs sm:text-sm py-1.5 px-3"
          >
            {busy ? 'Uploading...' : '+ Upload File'}
          </button>
        )}
        <input ref={fileRef} type="file" className="hidden" onChange={onPick} />
      </div>
    </div>
  );
}
