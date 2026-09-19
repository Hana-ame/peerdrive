// netdisk/FileTable.jsx：文件表格（网盘主区的通用列表）。
//
// 版式参考 Cloudreve / Alist 的文件列表：名称（带类型图标）· 大小 · 来源 ·
// 时间 · 操作。抽成一个组件是因为"我的网盘""对方节点详情"两处的列表结构
// 一致，只是操作按钮不同（本地是预览/下载/删除；对端是勾选/保存）。
import React from 'react';
import { formatSize, formatTime, shortHash } from './format';

// fileIcon 按扩展名/类型给图标（纯装饰，不参与逻辑）。
function fileIcon(name = '', mime = '') {
  const ext = String(name).split('.').pop()?.toLowerCase();
  if (mime.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'svg'].includes(ext)) return '🖼';
  if (mime.startsWith('video/') || ['mp4', 'webm', 'mkv', 'mov'].includes(ext)) return '🎬';
  if (mime.startsWith('audio/') || ['mp3', 'wav', 'flac', 'm4a'].includes(ext)) return '🎵';
  if (ext === 'pdf') return '📕';
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) return '🗜';
  if (['txt', 'md', 'json', 'js', 'ts', 'go', 'py'].includes(ext)) return '📄';
  return '📄';
}

/**
 * rows: [{ key, name, hash, size, mime, time, source, extra }]
 * columns: 需要显示的列（'size' | 'source' | 'time' | 'hash'）
 * selectable / selected / onToggleSelect：勾选（对方节点详情页"选中保存"）
 * actions：为该行渲染的操作节点函数 (row) => ReactNode
 */
export default function FileTable({
  rows = [],
  columns = ['size', 'time'],
  loading = false,
  empty = '这里还没有文件',
  selectable = false,
  selected = [],
  onToggleSelect,
  onOpen,
  actions,
}) {
  const selectedSet = new Set(selected);

  if (loading) {
    return <div className="px-4 py-10 text-center text-sm text-gray-500">加载中…</div>;
  }
  if (!rows.length) {
    return (
      <div className="px-4 py-12 text-center text-sm text-gray-500">
        <div className="text-3xl mb-2 opacity-60">📭</div>
        {empty}
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-[11px] uppercase tracking-wider text-gray-500 border-b border-gray-800">
            {selectable && <th className="w-8 px-3 py-2" />}
            <th className="px-3 py-2 font-normal">名称</th>
            {columns.includes('size') && <th className="w-24 px-3 py-2 font-normal">大小</th>}
            {columns.includes('source') && <th className="w-32 px-3 py-2 font-normal">来源</th>}
            {columns.includes('time') && <th className="w-40 px-3 py-2 font-normal">时间</th>}
            {columns.includes('hash') && <th className="w-36 px-3 py-2 font-normal">Hash</th>}
            <th className="w-40 px-3 py-2 font-normal text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key || r.hash || r.name} className="border-b border-gray-800/60 hover:bg-gray-800/40">
              {selectable && (
                <td className="px-3 py-2">
                  <input
                    type="checkbox"
                    aria-label={`选择 ${r.name}`}
                    checked={selectedSet.has(r.key || r.hash)}
                    onChange={() => onToggleSelect?.(r.key || r.hash)}
                  />
                </td>
              )}
              <td className="px-3 py-2 min-w-0">
                <button
                  type="button"
                  onClick={() => onOpen?.(r)}
                  className="flex items-center gap-2 min-w-0 text-left"
                  title={r.title || r.name}
                >
                  <span className="shrink-0">{r.icon || fileIcon(r.name, r.mime)}</span>
                  <span className="truncate text-gray-200">{r.name}</span>
                  {r.badge && (
                    <span className="shrink-0 text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-300">
                      {r.badge}
                    </span>
                  )}
                </button>
              </td>
              {columns.includes('size') && (
                <td className="px-3 py-2 text-gray-400 whitespace-nowrap">{formatSize(r.size)}</td>
              )}
              {columns.includes('source') && (
                <td className="px-3 py-2 text-gray-400 truncate">{r.source || '—'}</td>
              )}
              {columns.includes('time') && (
                <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{formatTime(r.time)}</td>
              )}
              {columns.includes('hash') && (
                <td className="px-3 py-2 text-gray-500 font-mono text-xs">{shortHash(r.hash)}</td>
              )}
              <td className="px-3 py-2 text-right">
                <div className="inline-flex gap-1.5">{actions?.(r)}</div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// Btn 表格内的小按钮（统一尺寸与配色，避免每处各写一份 class）。
export function Btn({ children, onClick, title, tone = 'default', disabled = false }) {
  const tones = {
    default: 'text-gray-300 hover:text-white hover:bg-gray-700 border-gray-700',
    primary: 'text-blue-300 hover:text-white hover:bg-blue-600/30 border-blue-700/50',
    danger: 'text-red-300 hover:text-white hover:bg-red-600/30 border-red-800/50',
  };
  return (
    <button
      type="button"
      title={title}
      disabled={disabled}
      onClick={onClick}
      className={`text-xs px-2 py-1 rounded border transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${tones[tone] || tones.default}`}
    >
      {children}
    </button>
  );
}
