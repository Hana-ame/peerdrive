import * as api from '../../api';
import { fileIcon } from './utils';

export default function FileRow({ file, isNestedColl, searchHash, onNestedCollClick }) {
  if (isNestedColl) {
    return (
      <div onClick={() => onNestedCollClick(file.hash)}
        className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
        <span className="text-xl">📦</span>
        <span className="text-purple-300 font-mono truncate flex-1">{(file.path || '').split('/').pop() || file.hash || 'file'}</span>
        <span className="text-purple-500 text-xs">合集 →</span>
      </div>
    );
  }
  const url = api.getAnonFileDownloadUrl(searchHash, file.path);
  const mime = file.mime_type || file.providers?.[0]?.mime_type || '';
  const displayName = (file.path || '').split('/').pop() || file.hash || 'file';
  return (
    <a key={file.path} href={url} target="_blank" rel="noreferrer"
      className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm block">
      <span className="text-xl">{fileIcon(mime, file.path)}</span>
      <span className="text-blue-300 font-mono truncate flex-1">{displayName}</span>
      {/* 坑：旧实现显示 relTime(collection.created_at) —— 匿名条目（path+providers）
          无逐文件时间字段，每行都渲染成合集创建时间，纯误导，已移除 */}
    </a>
  );
}
