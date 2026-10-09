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

        return (
          <div
            key={f.hash || i}
            onContextMenu={(e) => onContextMenu?.(e, f)}
            className={`card-surface p-4 rounded-xl border transition-all flex flex-col justify-between group cursor-pointer ${
              isTarget
                ? 'border-brand-500 bg-brand-500/10'
                : 'border-white/[0.06] hover:border-brand-500/40'
            }`}
          >
            <div>
              <div className="flex items-center justify-between text-2xl mb-2">
                <span>{icon}</span>
                <button
                  onClick={() => openTagEditor(f)}
                  className="opacity-0 group-hover:opacity-100 text-[10px] px-1.5 py-0.5 rounded bg-white/[0.08] hover:bg-white/[0.15] text-gray-300 transition-opacity"
                  title="Manage Tags"
                >
                  🏷️
                </button>
              </div>
              <h3 className="font-medium text-xs text-gray-200 truncate" title={f.filename}>
                {f.filename}
              </h3>
              <p className="text-[11px] text-gray-500 mt-1">
                {fmtBytes(f.size)}
              </p>
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
