import React from 'react';
import { fmtBytes } from '../../../platform/shared/format';

export default function DriveGridView({
  files,
  deepHash,
  fileTags,
  getFileIcon,
  openTagEditor,
  setPreviewFile,
  onDownload,
  openCastModal,
  onCopyDeepLink,
  copiedHash,
  onShare,
  onDelete,
  onContextMenu,
}) {
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-4">
      {files.map((f, i) => {
        const isTarget = deepHash && f.hash === deepHash;
        const tags = fileTags[f.hash] || [];
        const icon = getFileIcon(f);
        const ext = (f.filename || '').split('.').pop().toLowerCase();
        const isImg = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp'].includes(ext) || f.mime_type?.startsWith('image/');
        const isVid = ['mp4', 'mov', 'webm', 'mkv', 'avi'].includes(ext) || f.mime_type?.startsWith('video/');

        return (
          <div
            key={f.hash || i}
            onContextMenu={(e) => onContextMenu?.(e, f)}
            className={`card-surface p-3 sm:p-4 rounded-xl border transition-all flex flex-col justify-between group cursor-pointer ${
              isTarget
                ? 'border-brand-500 bg-brand-500/10'
                : 'border-white/[0.06] hover:border-brand-500/40'
            }`}
          >
            <div>
              {/* Thumbnail / Icon banner */}
              <div
                className="relative w-full h-32 rounded-lg bg-black/30 border border-white/[0.04] mb-2.5 flex items-center justify-center overflow-hidden group/thumb"
                onClick={() => setPreviewFile(f)}
              >
                {isImg ? (
                  <div className="w-full h-full flex items-center justify-center bg-gray-900/60 relative">
                    <span className="text-3xl opacity-40">{icon}</span>
                    <div className="absolute inset-0 bg-brand-500/10 opacity-0 group-hover/thumb:opacity-100 flex items-center justify-center transition-opacity text-xs font-medium text-brand-300">
                      Click to Preview
                    </div>
                  </div>
                ) : isVid ? (
                  <div className="w-full h-full flex flex-col items-center justify-center bg-gray-900/60 relative">
                    <span className="text-3xl mb-1">🎬</span>
                    <span className="text-[10px] text-gray-400 font-mono uppercase tracking-wider">{ext}</span>
                    <div className="absolute inset-0 bg-brand-500/10 opacity-0 group-hover/thumb:opacity-100 flex items-center justify-center transition-opacity text-xs font-medium text-brand-300">
                      Play Video
                    </div>
                  </div>
                ) : (
                  <div className="w-full h-full flex flex-col items-center justify-center bg-white/[0.02]">
                    <span className="text-3xl mb-1">{icon}</span>
                    <span className="text-[10px] text-gray-400 font-mono uppercase tracking-wider">{ext || 'file'}</span>
                  </div>
                )}

                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    openTagEditor(f);
                  }}
                  className="absolute top-1.5 right-1.5 opacity-0 group-hover:opacity-100 text-[10px] px-1.5 py-0.5 rounded bg-black/60 hover:bg-black/90 text-gray-300 transition-opacity"
                  title="Manage Tags"
                >
                  🏷️
                </button>
              </div>

              <h3
                className="font-medium text-xs text-gray-200 truncate hover:text-brand-300 transition-colors"
                title={f.filename}
                onClick={() => setPreviewFile(f)}
              >
                {f.filename}
              </h3>
              <div className="flex items-center justify-between text-[11px] text-gray-500 mt-1">
                <span>{fmtBytes(f.size)}</span>
                <span className="font-mono text-[10px] text-gray-600">{f.hash ? f.hash.slice(0, 8) : ''}</span>
              </div>
              {tags.length > 0 && (
                <div className="flex gap-1 flex-wrap mt-2">
                  {tags.map((t, ti) => (
                    <span key={ti} className="text-[10px] px-1.5 py-0.2 rounded bg-brand-500/10 text-brand-300">
                      #{t}
                    </span>
                  ))}
                </div>
              )}
            </div>

            <div className="mt-3 pt-2 border-t border-white/[0.04] flex items-center justify-between">
              <div className="flex items-center gap-1.5">
                <button
                  onClick={() => setPreviewFile(f)}
                  className="text-[11px] text-brand-400 hover:text-brand-300 font-medium"
                >
                  Preview
                </button>
                <span className="text-gray-600 text-xs">•</span>
                <button onClick={() => onDownload(f)} className="text-[11px] text-gray-400 hover:text-gray-200">Download</button>
              </div>
              <div className="flex items-center gap-1">
                <button onClick={() => openCastModal(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-white/[0.1] text-brand-300" title="Cast to screen">Cast</button>
                <button onClick={() => onCopyDeepLink(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300">
                  {copiedHash === f.hash ? 'Copied' : 'Link'}
                </button>
                <button onClick={() => onShare(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300">Share</button>
                <button onClick={() => onDelete(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-red-500/20 text-red-400">✕</button>
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}
