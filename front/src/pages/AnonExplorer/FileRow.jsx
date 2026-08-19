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
  const mime = file.mime_type || file.providers?.[0]?.mime_type || '';
  const displayName = (file.path || '').split('/').pop() || file.hash || 'file';
  // URL-only entry：没有 sha256 provider 时后端 WS 拉取会得到 302 跳转 HTML，
  // 前端不应尝试下载，改为直接打开外部链接（发现背景：再 review 2026-08）。
  const urlProvider = file.providers?.find(p => p.type === 'url');
  const shaHash = file.hash || file.providers?.find(p => p.type === 'sha256')?.value;
  if (!shaHash && urlProvider?.value) {
    return (
      <a key={file.path} href={urlProvider.value} target="_blank" rel="noreferrer"
        className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm block">
        <span className="text-xl">{fileIcon(mime, file.path)}</span>
        <span className="text-blue-300 font-mono truncate flex-1">{displayName}</span>
        <span className="text-purple-400 text-xs">外部链接 ↗</span>
      </a>
    );
  }
  return (
    <a key={file.path} href="#" onClick={(e) => {
      e.preventDefault();
      // 迁移后集合文件下载走 WS（旧 getAnonFileDownloadUrl HTTP 是 legacy）
      api.downloadAnonFile(searchHash, file.path).then(buf => {
        const blob = new Blob([buf], mime ? { type: mime } : undefined);
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = displayName;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        setTimeout(() => URL.revokeObjectURL(url), 5000);
      }).catch(err => alert('下载失败: ' + err.message));
    }}
      className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm block">
      <span className="text-xl">{fileIcon(mime, file.path)}</span>
      <span className="text-blue-300 font-mono truncate flex-1">{displayName}</span>
      {/* 坑：旧实现显示 relTime(collection.created_at) —— 匿名条目（path+providers）
          无逐文件时间字段，每行都渲染成合集创建时间，纯误导，已移除 */}
    </a>
  );
}
