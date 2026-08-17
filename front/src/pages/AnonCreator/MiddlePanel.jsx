import { useState, useEffect } from 'react';
import * as api from '../../api';
import { fileIcon, fmtSize } from './utils';

function ext(filename) {
  return (filename || '').split('.').pop()?.toLowerCase() || '';
}

export default function MiddlePanel({ selectedFile }) {
  const [textContent, setTextContent] = useState(null);
  const [textLoading, setTextLoading] = useState(false);
  const [textError, setTextError] = useState('');
  // 预览 blob URL（WS 下载 → objectURL；替代旧 HTTP getDownloadUrl 直连）
  const [blobUrl, setBlobUrl] = useState(null);

  useEffect(() => {
    setTextContent(null);
    setTextError('');
    if (!selectedFile) return;
    const mime = selectedFile.mime_type || '';
    const isText = mime.startsWith('text/') ||
      ['json', 'js', 'jsx', 'ts', 'tsx', 'css', 'html', 'xml', 'md', 'yaml', 'yml',
       'toml', 'ini', 'cfg', 'conf', 'sh', 'bash', 'py', 'go', 'rs', 'java', 'c',
       'cpp', 'h', 'log', 'txt', 'sql', 'r', 'rb', 'php', 'swift', 'kt', 'scala',
       'lua', 'pl', 'vim', 'zsh', 'fish'].includes(ext(selectedFile.filename || selectedFile.path));
    if (isText && selectedFile.hash) {
      setTextLoading(true);
      // 文本预览改走 WS 拉取（fetch HTTP 是 legacy）
      api.downloadFile(selectedFile.hash)
        .then(buf => {
          setTextContent(new TextDecoder().decode(buf));
          setTextLoading(false);
        })
        .catch(e => { setTextError(e.message); setTextLoading(false); });
    }
    // 非文本可预览类型：拉 blob URL 给 <img>/<video>/<audio>/<iframe>
    const isPreview = mime.startsWith('image/') || mime.startsWith('video/') ||
      mime.startsWith('audio/') || mime === 'application/pdf';
    if (isPreview && selectedFile.hash) {
      let cancelled = false;
      api.getBlobUrl(selectedFile.hash, mime)
        .then(url => { if (!cancelled) setBlobUrl(url); })
        .catch(() => {});
      return () => { cancelled = true; };
    }
    setBlobUrl(null);
  }, [selectedFile?.hash, selectedFile?.filename, selectedFile?.path]);

  if (!selectedFile) {
    return (
      <div className="h-full flex items-center justify-center bg-gray-950">
        <div className="text-center text-gray-600">
          <div className="text-4xl mb-3">👆</div>
          <div className="text-sm">选择左侧文件查看预览</div>
        </div>
      </div>
    );
  }

  const { mime_type, filename, hash, size, path } = selectedFile;
  const fname = filename || (path || '').split('/').pop() || '未知文件';
  const mime = mime_type || '';
  const e = ext(fname);
  const inlineUrl = blobUrl;

  const isImage = mime.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico'].includes(e);
  const isVideo = mime.startsWith('video/') || ['mp4', 'avi', 'mkv', 'mov', 'webm'].includes(e);
  const isAudio = mime.startsWith('audio/') || ['mp3', 'wav', 'flac', 'ogg'].includes(e);
  const isPdf = mime === 'application/pdf' || e === 'pdf';
  const isText = mime.startsWith('text/') ||
    ['json', 'js', 'jsx', 'ts', 'tsx', 'css', 'html', 'xml', 'md', 'yaml', 'yml',
     'toml', 'ini', 'cfg', 'conf', 'sh', 'bash', 'py', 'go', 'rs', 'java', 'c',
     'cpp', 'h', 'log', 'txt', 'sql'].includes(e);

  return (
    <div className="h-full flex flex-col bg-gray-950">
      {/* 文件标题栏 */}
      <div className="flex items-center gap-2 px-4 py-2 border-b border-gray-800 bg-gray-900 shrink-0">
        <span className="text-lg">{fileIcon(mime, fname)}</span>
        <span className="text-sm text-gray-200 truncate flex-1 font-mono">{fname}</span>
        {size > 0 && <span className="text-[10px] text-gray-500 shrink-0">{fmtSize(size)}</span>}
      </div>

      {/* 预览内容 */}
      <div className="flex-1 overflow-auto">
        {isImage && inlineUrl && (
          <div className="p-4 flex flex-col items-center">
            <img src={inlineUrl} alt={fname}
              className="max-w-full max-h-[70vh] object-contain rounded border border-gray-800"
              onError={(e) => { e.target.style.display = 'none'; }}
            />
            <div className="mt-3 text-xs text-gray-500 text-center">
              <div>{fname}</div>
              <div className="font-mono text-[10px] mt-1">{hash?.substring(0, 16)}...</div>
            </div>
          </div>
        )}

        {isVideo && inlineUrl && (
          <div className="p-4 flex flex-col items-center">
            <video src={inlineUrl} controls className="max-w-full max-h-[70vh] rounded border border-gray-800" />
            <div className="mt-3 text-xs text-gray-500 text-center">
              <div>{fname}</div>
              <div>{fmtSize(size)}</div>
            </div>
          </div>
        )}

        {isAudio && inlineUrl && (
          <div className="p-8 flex flex-col items-center justify-center">
            <div className="text-5xl mb-6">🎵</div>
            <audio src={inlineUrl} controls className="w-full max-w-md" />
            <div className="mt-4 text-xs text-gray-500 text-center">
              <div>{fname}</div>
              <div>{fmtSize(size)}</div>
            </div>
          </div>
        )}

        {isPdf && inlineUrl && (
          <div className="p-4 h-full">
            <iframe src={inlineUrl + '#toolbar=0'} className="w-full h-full rounded border border-gray-800" title={fname} />
          </div>
        )}

        {isText && (
          <div className="p-4">
            <div className="text-xs text-gray-500 mb-2 flex justify-between">
              <span>{fname}</span>
              <span>{fmtSize(size)}</span>
            </div>
            {textLoading ? (
              <div className="text-gray-600 text-xs py-8 text-center">加载文本内容...</div>
            ) : textError ? (
              <div className="text-red-400 text-xs py-8 text-center">加载失败: {textError}</div>
            ) : (
              <pre className="bg-gray-900 p-4 rounded border border-gray-800 text-xs text-gray-300 overflow-auto max-h-[70vh] font-mono whitespace-pre-wrap break-all">
                {textContent || '(空文件)'}
              </pre>
            )}
          </div>
        )}

        {/* 默认：非可预览类型 */}
        {!isImage && !isVideo && !isAudio && !isPdf && !isText && (
          <div className="p-8 text-center">
            <div className="text-5xl mb-4">{fileIcon(mime, fname)}</div>
            <div className="text-sm font-medium text-gray-200">{fname}</div>
            <div className="text-xs text-gray-500 mt-3 space-y-1">
              <div>类型: {mime || '未知'}</div>
              <div>大小: {fmtSize(size)}</div>
              {hash && <div className="font-mono text-[10px]">Hash: {hash.substring(0, 32)}...</div>}
              <div className="text-gray-600 mt-2">此格式暂不支持预览</div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
