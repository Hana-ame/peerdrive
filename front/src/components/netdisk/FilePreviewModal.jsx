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

  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') {
        onClose?.();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex flex-col bg-black/95 backdrop-blur-xl text-gray-100 select-none overflow-hidden"
      onClick={onClose}
    >
      {/* Sleek Top Navigation Bar */}
      <div
        className="h-14 px-4 sm:px-6 flex items-center justify-between gap-4 border-b border-white/[0.08] bg-black/40 backdrop-blur-md shrink-0 z-10"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 min-w-0">
          <span className="text-xl">
            {kind === 'image' ? '🖼️' : kind === 'video' ? '🎬' : kind === 'audio' ? '🎵' : kind === 'pdf' ? '📑' : kind === 'text' ? '📝' : '📄'}
          </span>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-gray-100 truncate flex items-center gap-2">
              <span title={filename}>{filename}</span>
            </h2>
            <div className="text-[11px] text-gray-400 flex items-center gap-2 font-mono flex-wrap">
              {file.size != null && <span>{fmtBytes(file.size)}</span>}
              {effectiveMime && <span className="hidden sm:inline">• {effectiveMime}</span>}
              {isStream && (
                <span className="px-1.5 py-0.5 rounded text-[10px] bg-emerald-500/15 text-emerald-400 border border-emerald-500/20 font-sans font-medium flex items-center gap-1">
                  <span>⚡</span> 边下边播 (Range 流式)
                </span>
              )}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          {onDownload && (
            <button
              onClick={() => onDownload(file)}
              className="btn-brand text-xs px-3 py-1.5 flex items-center gap-1.5 shadow-md"
            >
              <span>⬇️</span>
              <span>Download file</span>
            </button>
          )}
          <button
            onClick={onClose}
            className="w-8 h-8 flex items-center justify-center text-gray-400 hover:text-white rounded-lg hover:bg-white/[0.1] transition-colors leading-none text-base"
            aria-label="Close"
          >
            ✕
          </button>
        </div>
      </div>

      {/* Main Full-Screen Display Area (No bounding frame box) */}
      <div
        className="flex-1 min-h-0 w-full h-full relative flex items-center justify-center overflow-hidden p-0 sm:p-2"
        onClick={(e) => e.stopPropagation()}
      >
        {loading && (
          <div className="flex flex-col items-center gap-3 py-16 text-gray-400">
            <span className="text-4xl animate-spin">⏳</span>
            <span className="text-sm font-medium text-gray-300">正在加载文件数据...</span>
            <span className="text-xs text-gray-500 font-mono">
              {filename} {file?.size != null ? `(${fmtBytes(file.size)})` : ''}
            </span>
          </div>
        )}

        {error && !loading && (
          <div className="flex flex-col items-center gap-3 py-12 text-center px-4 max-w-lg">
            <span className="text-4xl">⚠️</span>
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
              <div className="w-full h-full flex items-center justify-center p-2 sm:p-4">
                <img
                  src={blobUrl}
                  alt={filename}
                  className="max-w-full max-h-full object-contain select-none transition-transform shadow-2xl"
                  onError={() => {
                    setError('图片解码或显示失败，文件数据可能不完整。');
                  }}
                />
              </div>
            )}

            {kind === 'video' && (
              <div className="w-full h-full flex flex-col items-center justify-center bg-black">
                <video
                  src={blobUrl}
                  controls
                  autoPlay
                  playsInline
                  crossOrigin="anonymous"
                  className="w-full h-full max-h-full object-contain bg-black"
                  onError={async () => {
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
                    const isMov = (filename || '').toLowerCase().endsWith('.mov') || effectiveMime === 'video/quicktime';
                    if (isMov) {
                      setError('该视频为 MOV (QuickTime) 格式，当前浏览器可能不支持原生硬件解码。建议下载后使用本地播放器查看。');
                    } else {
                      setError('视频播放失败：浏览器无法解码该媒体或格式不受支持。');
                    }
                  }}
                />
              </div>
            )}

            {kind === 'audio' && (
              <div className="w-full h-full flex flex-col items-center justify-center gap-6 p-6">
                <div className="w-24 h-24 rounded-2xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-5xl shadow-2xl">
                  🎵
                </div>
                <div className="text-center">
                  <h3 className="text-base font-medium text-gray-200">{filename}</h3>
                  <p className="text-xs text-gray-400 mt-1 font-mono">{fmtBytes(file.size)}</p>
                </div>
                <audio
                  src={blobUrl}
                  controls
                  autoPlay
                  crossOrigin="anonymous"
                  className="w-full max-w-lg"
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
              <div className="w-full h-full">
                <iframe
                  src={blobUrl}
                  title={filename}
                  className="w-full h-full border-0 bg-white"
                />
              </div>
            )}

            {kind === 'text' && (
              <div className="w-full h-full flex flex-col p-3 sm:p-6 max-w-6xl mx-auto">
                {isTruncated && (
                  <div className="text-[11px] text-amber-300 bg-amber-500/10 border border-amber-500/20 px-3 py-1.5 rounded-lg mb-2 shrink-0">
                    ⚠️ File exceeds 1MB limit. Previewing the first 1 MB only.
                  </div>
                )}
                <pre className="flex-1 w-full p-4 font-mono text-xs sm:text-sm text-gray-200 bg-black/60 rounded-xl overflow-auto whitespace-pre-wrap break-all select-text border border-white/[0.06] shadow-inner">
                  {textContent}
                </pre>
              </div>
            )}

            {!kind && (
              <div className="flex flex-col items-center gap-3 py-16 text-center">
                <span className="text-5xl">📄</span>
                <p className="text-sm text-gray-300">Preview not supported for this file type</p>
                <p className="text-xs text-gray-500 font-mono">MIME: {effectiveMime || 'application/octet-stream'}</p>
                {onDownload && (
                  <button onClick={() => onDownload(file)} className="btn-brand text-xs px-4 py-2 mt-2">
                    Download file
                  </button>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
