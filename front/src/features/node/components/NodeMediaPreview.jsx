import React from 'react';
import { fmtBytes } from '../../../platform/shared/format';
import { kindOf } from '../../../platform/shared/mime';

export default function NodeMediaPreview({
  preview,
  share,
  viewer,
  zoomBy,
  resetZoom,
  goImg,
  onDragStart,
  onDragMove,
  onDragEnd,
  onMediaError,
  closePreview,
  setPreview,
}) {
  if (!preview) return null;

  const pct = (loaded, total) => (total ? Math.min(100, Math.round((loaded / total) * 100)) : 0);
  const imageFiles = (share?.files || []).filter(f => kindOf(f.path || f.name) === 'image');

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-black" onClick={closePreview}>
      <div className="relative flex-1 min-h-0 flex flex-col" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/[0.06] shrink-0">
          <p className="text-sm text-gray-200 font-mono truncate pr-4">{preview.name || preview.hash}</p>
          <button onClick={closePreview} className="btn-ghost !px-2 !py-1 !text-xs shrink-0">Close ✕</button>
        </div>
        <div className="flex-1 min-h-0 overflow-auto p-3">
          {preview.loading && (
            <div className="flex flex-col items-center gap-2 py-8">
              <p className="text-xs text-gray-400">
                Loading... {preview.progress?.loaded ? `${fmtBytes(preview.progress.loaded)}${preview.progress.total ? ' / ' + fmtBytes(preview.progress.total) : ''} (${pct(preview.progress.loaded, preview.progress.total)}%)` : ''}
              </p>
              <div className="w-64 h-1.5 bg-white/[0.08] rounded overflow-hidden">
                <div className="h-full bg-brand-500 transition-all" style={{ width: `${pct(preview.progress?.loaded, preview.progress?.total)}%` }} />
              </div>
            </div>
          )}
          {preview.error && (
            <div className="text-center py-10">
              <p className="text-xs text-red-400 mb-4">{preview.error}</p>
              <button onClick={closePreview} className="btn-brand !px-4 !py-2 !text-sm">← Back to List</button>
            </div>
          )}
          {!preview.error && preview.kind === 'image' && preview.url && (
            <div>
              {/* Viewer toolbar */}
              <div className="flex items-center justify-between gap-2 mb-2 text-xs">
                <div className="flex items-center gap-1.5">
                  <button onClick={() => zoomBy(0.8)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">−</button>
                  <span className="w-14 text-center text-gray-400">{Math.round(viewer.scale * 100)}%</span>
                  <button onClick={() => zoomBy(1.25)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">+</button>
                  <button onClick={resetZoom} className="px-2 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">1:1</button>
                </div>
                {imageFiles.length > 1 && (
                  <div className="flex items-center gap-1.5">
                    <button onClick={() => goImg(-1)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">←</button>
                    <span className="text-gray-400">{(preview.imgIndex ?? 0) + 1}/{imageFiles.length}</span>
                    <button onClick={() => goImg(1)} className="w-7 h-7 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">→</button>
                  </div>
                )}
              </div>
              {/* Image area: drag-to-pan after zooming */}
              <div
                className="relative bg-black/30 rounded overflow-hidden select-none"
                style={{ height: 'calc(100vh - 150px)' }}
                onPointerDown={onDragStart}
                onPointerMove={onDragMove}
                onPointerUp={onDragEnd}
                onPointerLeave={onDragEnd}
                onWheel={e => { e.preventDefault(); zoomBy(e.deltaY < 0 ? 1.1 : 0.9); }}
              >
                <img
                  src={preview.url}
                  alt={preview.name}
                  draggable={false}
                  onError={() => setPreview(p => (p ? { ...p, error: 'Image loading failed: Service Worker not taking over /swdrive or connection lost (return to list and retry)' } : p))}
                  style={{
                    transform: `translate(${viewer.x}px, ${viewer.y}px) scale(${viewer.scale})`,
                    cursor: viewer.scale > 1 ? 'grab' : 'default',
                  }}
                  className="absolute inset-0 m-auto max-w-full max-h-full object-contain"
                />
              </div>
            </div>
          )}
          {!preview.loading && !preview.error && preview.kind === 'video' && preview.url && (
            <video
              src={preview.url}
              controls autoPlay
              onError={onMediaError}
              className="w-full h-[calc(100vh-150px)] object-contain rounded bg-black"
            />
          )}
          {!preview.loading && !preview.error && preview.kind === 'audio' && preview.url && (
            <div className="flex items-center justify-center h-[calc(100vh-150px)]">
              <audio
                src={preview.url}
                controls autoPlay
                onError={() => setPreview(p => (p ? { ...p, error: 'Media loading failed: Service Worker not taking over /swdrive or connection lost (return to list and retry)' } : p))}
                className="w-full max-w-xl"
              />
            </div>
          )}
          {preview.kind === 'text' && preview.text != null && (
            <pre className="text-xs text-gray-300 bg-black/40 rounded p-3 h-[calc(100vh-150px)] overflow-auto whitespace-pre-wrap break-all">{preview.text}</pre>
          )}
        </div>
      </div>
    </div>
  );
}
