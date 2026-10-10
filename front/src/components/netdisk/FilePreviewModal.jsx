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
import { swControlled, ensureSWReady } from '../../platform/shared/swBridge';
import { getNodeSession } from '../../lib/nodeSession';
import { STORAGE_KEY_API_BASE } from '../../platform/shared/storageKeys';

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
  const [isStream, setIsStream] = useState(false);

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
      setIsStream(false);

      try {
        if (file.url) {
          if (active) {
            setBlobUrl(file.url);
            if (kind === 'text') {
              try {
                const res = await fetch(file.url);
                const txt = await res.text();
                if (txt.length > MAX_TEXT_PREVIEW_BYTES) {
                  setIsTruncated(true);
                  setTextContent(txt.slice(0, MAX_TEXT_PREVIEW_BYTES));
                } else {
                  setIsTruncated(false);
                  setTextContent(txt);
                }
              } catch { /* ignore */ }
            }
            setLoading(false);
          }
          return;
        }

        // 边下边播与 206 Range 支持：
        // 1. 若为远程 PeerJS 模式 (hasPeerClient)：
        //    使用 Service Worker 拦截 /swdrive/ 发起 206 Range 分片流式传输。
        //    若 SW 尚未 claim，异步等待 ensureSWReady()。
        // 2. 若在本地 WS/HTTP 模式且有 apiBase / 相对地址可达：
        //    直接构造 /sha256sum/:hash/:name 进行原生 HTTP 206 Range 流式点播。
        // 3. 仅当上述流式路径不可行时，才回退全量 fetchBlob。
        const isMedia = kind === 'video' || kind === 'audio';
        const hasPeerClient = Boolean(getNodeSession()?.client);

        if (isMedia) {
          if (hasPeerClient) {
            let ready = swControlled();
            if (!ready) {
              ready = await ensureSWReady();
            }
            if (ready && active) {
              const base = import.meta.env.BASE_URL || '/';
              const swDriveUrl = `${base}swdrive/${encodeURIComponent(file.hash)}?name=${encodeURIComponent(filename)}${file.size ? '&size=' + file.size : ''}`;
              setIsStream(true);
              setBlobUrl(swDriveUrl);
              setLoading(false);
              return;
            }
          } else {
            // 本地 WS/HTTP 模式：直接走原生 206 Range 点播（即时起播，零内存占用）
            const rawBase = typeof window !== 'undefined' ? localStorage.getItem(STORAGE_KEY_API_BASE) || '' : '';
            const apiBase = rawBase.replace(/\/+$/, '');
            const localStreamUrl = `${apiBase}/sha256sum/${encodeURIComponent(file.hash)}/${encodeURIComponent(filename)}`;
            setIsStream(true);
            setBlobUrl(localStreamUrl);
            setLoading(false);
            return;
          }
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
            <div className="text-xs text-gray-400 mt-1 flex items-center gap-2 font-mono flex-wrap">
              {file.size != null && <span>{fmtBytes(file.size)}</span>}
              {effectiveMime && <span>• {effectiveMime}</span>}
              {isStream && (
                <span className="px-1.5 py-0.5 rounded text-[10px] bg-emerald-500/15 text-emerald-400 border border-emerald-500/20 font-sans font-medium flex items-center gap-1">
                  <span>⚡</span> 边下边播 (Range 流式)
                </span>
              )}
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
              <span className="text-3xl animate-spin">⏳</span>
              <span className="text-sm font-medium text-gray-300">正在加载文件数据...</span>
              <span className="text-xs text-gray-500 font-mono">
                {filename} {file?.size != null ? `(${fmtBytes(file.size)})` : ''}
              </span>
            </div>
          )}

          {error && !loading && (
            <div className="flex flex-col items-center gap-3 py-12 text-center px-4 max-w-lg">
              <span className="text-3xl">⚠️</span>
              <p className="text-sm font-medium text-red-400">{error}</p>
              <p className="text-xs text-gray-400">
                无法在浏览器中直接内联预览该文件。您可以将其保存到本地设备进行查看。
              </p>
              {onDownload && (
                <button
                  onClick={() => onDownload(file)}
                  className="btn-brand text-xs px-4 py-2 mt-2 flex items-center gap-2 shadow-lg"
                >
                  <span>⬇️ 立即下载文件</span>
                  {file?.size != null && <span className="font-mono text-[11px]">({fmtBytes(file.size)})</span>}
                </button>
              )}
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
                    onError={() => {
                      setError('图片解码或显示失败，文件数据可能不完整。');
                    }}
                  />
                </div>
              )}

              {kind === 'video' && (
                <div className="w-full h-full flex flex-col items-center justify-center">
                  <video
                    src={blobUrl}
                    controls
                    autoPlay
                    playsInline
                    crossOrigin="anonymous"
                    className="max-w-full max-h-[60vh] rounded bg-black"
                    onError={async () => {
                      // 1. 若使用的是流式地址且播放失败，尝试回退到 fetchBlob 整体加载
                      if (fetchBlob && !blobUrl.startsWith('blob:')) {
                        try {
                          setLoading(true);
                          setIsStream(false);
                          const data = await fetchBlob(file.hash);
                          const b = data instanceof Blob ? data : new Blob([data], { type: effectiveMime || 'video/mp4' });
                          const nextUrl = URL.createObjectURL(b);
                          setBlobUrl(nextUrl);
                          setLoading(false);
                          return;
                        } catch {
                          setLoading(false);
                        }
                      }
                      // 2. 格式不兼容提示（特别是 QuickTime MOV 格式）
                      const isMov = (filename || '').toLowerCase().endsWith('.mov') || effectiveMime === 'video/quicktime';
                      if (isMov) {
                        setError('该视频为 MOV (QuickTime) 格式，当前浏览器可能不支持原生硬件解码。建议下载后使用本地播放器查看。');
                      } else {
                        setError('视频播放失败：浏览器无法解码该媒体或格式不受支持。');
                      }
                    }}
                  />
                  {(filename.toLowerCase().endsWith('.mov') || effectiveMime === 'video/quicktime') && (
                    <div className="mt-2 text-[11px] text-gray-400 flex items-center gap-1.5">
                      <span>💡 提示：若当前浏览器黑屏或无声音，请</span>
                      {onDownload && (
                        <button
                          onClick={() => onDownload(file)}
                          className="text-brand-400 hover:text-brand-300 underline font-medium"
                        >
                          下载到本地播放
                        </button>
                      )}
                    </div>
                  )}
                </div>
              )}

              {kind === 'audio' && (
                <div className="w-full py-16 flex flex-col items-center justify-center gap-4">
                  <div className="text-4xl">🎵</div>
                  <audio
                    src={blobUrl}
                    controls
                    autoPlay
                    crossOrigin="anonymous"
                    className="w-full max-w-md"
                    onError={async () => {
                      if (fetchBlob && !blobUrl.startsWith('blob:')) {
                        try {
                          setLoading(true);
                          setIsStream(false);
                          const data = await fetchBlob(file.hash);
                          const b = data instanceof Blob ? data : new Blob([data], { type: effectiveMime || 'audio/mpeg' });
                          setBlobUrl(URL.createObjectURL(b));
                          setLoading(false);
                        } catch (err) {
                          setLoading(false);
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
