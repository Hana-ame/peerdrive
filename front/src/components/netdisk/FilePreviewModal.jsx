// FilePreviewModal.jsx — In-line file preview modal dispatching by MIME / Kind.
//
// 交互设计（issue #150，网盘式预览）：
// 1. image/*: 大图展示，支持自适应居中。
// 2. video/*: 内联 HTML5 video 播放器，原生播放与控制条。
// 3. audio/*: 内联 HTML5 audio 播放器，带音频波形条/控制栏。
// 4. application/pdf: 内嵌 iframe / object，原生浏览 PDF。
// 5. text/* / code / json: 代码块高亮/等宽字体预览，带 1MB 截断保护（防止内存溢出）。
// 6. 其他 / 未知 / 加载失败: 友好占位提示 + 明确的下载按钮。

import React, { useState, useEffect } from 'react';
import { fmtBytes } from '../../platform/shared/format';
import { kindOf, mimeOf, MAX_TEXT_PREVIEW_BYTES } from '../../platform/shared/mime';
import { swControlled } from '../../platform/shared/swBridge';

export default function FilePreviewModal({
  file, // { hash, filename, size, mime_type, ... }
  fetchBlob, // async (hash) => Uint8Array or Blob
  onClose,
  onDownload,
}) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [blobUrl, setBlobUrl] = useState('');
  const [textContent, setTextContent] = useState('');
  const [isTruncated, setIsTruncated] = useState(false);

  const filename = file?.filename || file?.name || 'file';
  const explicitMime = file?.mime_type || file?.mime || '';
  const effectiveMime = mimeOf(filename, explicitMime);
  const kind = kindOf(filename, explicitMime);

  useEffect(() => {
    let active = true;
    let createdUrl = '';

    async function loadData() {
      if (!file?.hash && !file?.url) {
        setError('Missing file hash or URL');
        setLoading(false);
        return;
      }

      setLoading(true);
      setError('');

      try {
        if (file.url) {
          if (active) {
            setBlobUrl(file.url);
            setLoading(false);
          }
          return;
        }

        // SW 边下边播与 206 Range 支持：
        // 若当前处于 Service Worker 控制下且为音视频媒体类型，直接构造 /swdrive/ URL
        // 浏览器 video/audio 播放器将直接发起带有 Range 头的 HTTP 206 请求，
        // 由 service-worker.js 拦截并通过 MessageChannel 流式索取分片，实现秒开播放和任意 seek。
        const isMediaStreamable = (kind === 'video' || kind === 'audio') && swControlled();
        if (isMediaStreamable) {
          const base = import.meta.env.BASE_URL || '/';
          const swDriveUrl = `${base}swdrive/${encodeURIComponent(file.hash)}?name=${encodeURIComponent(filename)}${file.size ? '&size=' + file.size : ''}`;
          if (active) {
            setBlobUrl(swDriveUrl);
            setLoading(false);
          }
          return;
        }

        if (!fetchBlob) {
          throw new Error('No blob loader provided');
        }

        const data = await fetchBlob(file.hash);
        if (!active) return;

        let blob;
        let bytes;
        if (data instanceof Blob) {
          blob = data;
        } else {
          bytes = data;
          blob = new Blob([bytes], { type: effectiveMime || 'application/octet-stream' });
        }

        createdUrl = URL.createObjectURL(blob);
        setBlobUrl(createdUrl);

        if (kind === 'text') {
          // Read up to MAX_TEXT_PREVIEW_BYTES
          let rawBytes = bytes;
          if (!rawBytes) {
            const ab = await blob.arrayBuffer();
            rawBytes = new Uint8Array(ab);
          }

          if (rawBytes.byteLength > MAX_TEXT_PREVIEW_BYTES) {
            setIsTruncated(true);
            const slice = rawBytes.slice(0, MAX_TEXT_PREVIEW_BYTES);
            const dec = new TextDecoder('utf-8', { fatal: false });
            setTextContent(dec.decode(slice));
          } else {
            setIsTruncated(false);
            const dec = new TextDecoder('utf-8', { fatal: false });
            setTextContent(dec.decode(rawBytes));
          }
        }
        setLoading(false);
      } catch (err) {
        if (!active) return;
        setError(err?.message || 'Failed to load preview');
        setLoading(false);
      }
    }

    loadData();

    return () => {
      active = false;
      if (createdUrl) {
        URL.revokeObjectURL(createdUrl);
      }
    };
  }, [file, fetchBlob, effectiveMime, kind]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-sm p-2 sm:p-6"
      onClick={onClose}
    >
      <div
        className="card-surface max-w-4xl w-full p-3 sm:p-5 rounded-2xl max-h-[92vh] flex flex-col shadow-2xl border border-white/[0.08]"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-start justify-between gap-4 mb-4 pb-3 border-b border-white/[0.06]">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-gray-100 truncate flex items-center gap-2">
              <span>{filename}</span>
            </h2>
            <div className="text-xs text-gray-400 mt-1 flex items-center gap-2 font-mono">
              {file.size != null && <span>{fmtBytes(file.size)}</span>}
              {effectiveMime && <span>• {effectiveMime}</span>}
              {file.hash && <span className="text-gray-500 truncate max-w-[200px]">SHA: {file.hash.slice(0, 16)}…</span>}
            </div>
          </div>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-white p-1 rounded-lg hover:bg-white/[0.05] transition-colors leading-none"
            aria-label="Close"
          >
            ✕
          </button>
        </div>

        {/* Content body */}
        <div className="flex-1 min-h-[300px] max-h-[68vh] overflow-y-auto flex flex-col items-center justify-center bg-black/40 rounded-xl p-3 border border-white/[0.04]">
          {loading && (
            <div className="flex flex-col items-center gap-3 py-16 text-gray-400">
              <span className="text-2xl animate-spin">⏳</span>
              <span className="text-xs">Loading preview...</span>
            </div>
          )}

          {error && !loading && (
            <div className="flex flex-col items-center gap-3 py-16 text-center px-4">
              <span className="text-3xl">⚠️</span>
              <p className="text-sm text-red-400">{error}</p>
              <p className="text-xs text-gray-500 max-w-md">
                Unable to render preview directly in browser. You can still download the file to inspect it locally.
              </p>
            </div>
          )}

          {!loading && !error && (
            <>
              {kind === 'image' && (
                <div className="flex items-center justify-center w-full h-full p-2">
                  <img
                    src={blobUrl}
                    alt={filename}
                    className="max-w-full max-h-[62vh] object-contain rounded shadow"
                  />
                </div>
              )}

              {kind === 'video' && (
                <div className="w-full h-full flex items-center justify-center">
                  <video
                    src={blobUrl}
                    controls
                    autoPlay
                    className="max-w-full max-h-[62vh] rounded bg-black"
                    onError={async () => {
                      if (fetchBlob && !blobUrl.startsWith('blob:')) {
                        try {
                          const data = await fetchBlob(file.hash);
                          const b = data instanceof Blob ? data : new Blob([data], { type: effectiveMime || 'video/mp4' });
                          setBlobUrl(URL.createObjectURL(b));
                        } catch (err) {
                          setError('Video playback failed: ' + (err?.message || String(err)));
                        }
                      }
                    }}
                  />
                </div>
              )}

              {kind === 'audio' && (
                <div className="w-full py-16 flex flex-col items-center justify-center gap-4">
                  <div className="text-4xl">🎵</div>
                  <audio
                    src={blobUrl}
                    controls
                    autoPlay
                    className="w-full max-w-md"
                    onError={async () => {
                      if (fetchBlob && !blobUrl.startsWith('blob:')) {
                        try {
                          const data = await fetchBlob(file.hash);
                          const b = data instanceof Blob ? data : new Blob([data], { type: effectiveMime || 'audio/mpeg' });
                          setBlobUrl(URL.createObjectURL(b));
                        } catch (err) {
                          setError('Audio playback failed: ' + (err?.message || String(err)));
                        }
                      }
                    }}
                  />
                </div>
              )}

              {kind === 'pdf' && (
                <div className="w-full h-[62vh]">
                  <iframe
                    src={blobUrl}
                    title={filename}
                    className="w-full h-full rounded border-0 bg-white"
                  />
                </div>
              )}

              {kind === 'text' && (
                <div className="w-full h-full flex flex-col">
                  {isTruncated && (
                    <div className="text-[11px] text-amber-300 bg-amber-500/10 border border-amber-500/20 px-3 py-1.5 rounded-lg mb-2">
                      ⚠️ File exceeds 1MB limit. Previewing the first 1 MB only.
                    </div>
                  )}
                  <pre className="flex-1 w-full p-3 font-mono text-xs text-gray-200 bg-black/50 rounded-lg overflow-auto whitespace-pre-wrap break-all select-text border border-white/[0.04]">
                    {textContent}
                  </pre>
                </div>
              )}

              {!kind && (
                <div className="flex flex-col items-center gap-3 py-16 text-center">
                  <span className="text-4xl">📄</span>
                  <p className="text-sm text-gray-300">Preview not supported for this file type</p>
                  <p className="text-xs text-gray-500">MIME: {effectiveMime || 'application/octet-stream'}</p>
                </div>
              )}
            </>
          )}
        </div>

        {/* Footer actions */}
        <div className="flex items-center justify-between gap-3 mt-4 pt-3 border-t border-white/[0.06]">
          <div className="text-xs text-gray-500">
            {kind ? `Preview: ${kind.toUpperCase()}` : 'Binary file'}
          </div>
          <div className="flex items-center gap-2">
            {onDownload && (
              <button
                onClick={() => onDownload(file)}
                className="btn-brand text-xs px-3 py-1.5"
              >
                Download file
              </button>
            )}
            <button
              onClick={onClose}
              className="btn-ghost text-xs px-3 py-1.5"
            >
              Close
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
