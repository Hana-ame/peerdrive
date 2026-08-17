import * as api from '../../api';
import { fileIcon, fmtSize } from './utils';

export default function FileSourceRow({ file, onAdd, onDragStart, onSelect }) {
  const handleClick = (e) => {
    // 不拦截 + 按钮和链接的点击
    if (e.target.closest('button') || e.target.closest('a')) return;
    if (onSelect) onSelect();
  };

  return (
    <div draggable onDragStart={onDragStart ? (e) => onDragStart(e, file) : undefined}
      onClick={handleClick}
      className="flex items-center gap-2 px-3 py-2 hover:bg-gray-800 border-b border-gray-800/50 text-sm group cursor-pointer">
      <a href="#" onClick={e => {
        e.preventDefault();
        e.stopPropagation();
        // 迁移后下载走 WS（旧 getDownloadUrl HTTP 是 legacy）
        api.downloadFileToDisk(file.hash, file.filename);
      }} className="text-lg shrink-0">{fileIcon(file.mime_type, file.filename)}</a>
      <span className="text-blue-300 truncate flex-1 font-mono text-[11px]">{file.filename}</span>
      <span className="text-gray-500 text-[10px] shrink-0">{fmtSize(file.size)}</span>
      <button onClick={(e) => { e.stopPropagation(); onAdd(file.hash, file.filename, file.mime_type, file.size); }}
        className="text-blue-400 opacity-0 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
    </div>
  );
}
