import React, { useState, useEffect } from 'react';

export default function TextPreview({ buf, onLoad, downloadUrl, filename, hash, created, onDownload }) {
  const [content, setContent] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    // 文本预览改走 WS 拉取（旧 fetch(url) HTTP 是 legacy；url 参数已移除）
    if (onLoad && typeof onLoad.then === 'function') {
      onLoad
        .then(b => { setContent(new TextDecoder().decode(b).substring(0, 50000)); setLoading(false); })
        .catch(() => { setError('加载失败'); setLoading(false); });
    } else {
      setLoading(false);
    }
  }, []);
  if (loading) return <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">加载预览中...</div>;
  if (error) return <div className="flex-1 flex items-center justify-center text-red-400 text-sm">{error}</div>;
  return (
    <div className="flex flex-col h-full overflow-hidden px-4 py-3">
      <div className="flex items-center gap-2 mb-2 text-xs text-gray-500">
        <span className="text-gray-300 font-mono truncate">{filename}</span>
        <span>{(hash || '').substring(0, 12)}</span>
        {content && content.length >= 50000 && <span className="text-amber-500">（截断至前 50KB）</span>}
      </div>
      <pre className="flex-1 overflow-auto bg-gray-900 border border-gray-800 rounded-lg p-4 text-xs font-mono text-gray-300 whitespace-pre-wrap break-all">{content}</pre>
      <a href="#" onClick={e => { e.preventDefault(); if (onDownload) onDownload(); }}
        className="text-xs text-blue-400 hover:underline mt-2 self-end">⬇ 下载</a>
    </div>
  );
}
