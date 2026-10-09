import React from 'react';
import { fmtBytes } from '../../../platform/shared/format';

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  if (isNaN(d)) return ts;
  return d.toLocaleString('en-US', { hour12: false });
}

export default function DriveListView({
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
}) {
  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';
  const td = 'px-3 py-2';

  return (
    <div className="card-surface overflow-x-auto">
      <table className="w-full text-sm min-w-[560px]">
        <thead className="bg-white/[0.03]">
          <tr>
            <th className={th}>Name</th>
            <th className={th}>Tags</th>
            <th className={th}>Size</th>
            <th className={th}>Type</th>
            <th className={th}>Registered</th>
            <th className={th + ' text-right'}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {files.map((f, i) => {
            const isTarget = deepHash && f.hash === deepHash;
            const tags = fileTags[f.hash] || [];
            const icon = getFileIcon(f);

            return (
              <tr
                key={f.hash || i}
                className={`border-t border-white/[0.04] transition-colors ${
                  isTarget
                    ? 'bg-brand-500/15 border-l-2 border-l-brand-400 hover:bg-brand-500/20'
                    : 'hover:bg-white/[0.02]'
                }`}
              >
                <td className={td + ' text-gray-200 max-w-[240px] truncate'}>
                  <div className="flex items-center gap-2">
                    <span className="text-base">{icon}</span>
                    {isTarget && (
                      <span className="inline-block w-1.5 h-1.5 rounded-full bg-brand-400 shrink-0" title="Deep linked" />
                    )}
                    <button
                      onClick={() => setPreviewFile(f)}
                      className="truncate text-left hover:text-brand-300 transition-colors cursor-pointer"
                      title="Click to preview"
                    >
                      {f.filename}
                    </button>
                  </div>
                </td>
                <td className={td + ' text-xs text-gray-400 max-w-[150px]'}>
                  <div className="flex items-center gap-1 flex-wrap">
                    {tags.length > 0 ? (
                      tags.map((t, ti) => (
                        <span key={ti} className="text-[10px] px-1.5 py-0.5 rounded bg-brand-500/10 text-brand-300">
                          #{t}
                        </span>
                      ))
                    ) : (
                      <span className="text-gray-600">—</span>
                    )}
                    <button
                      onClick={() => openTagEditor(f)}
                      className="text-[10px] text-gray-500 hover:text-white px-1 ml-0.5"
                      title="Edit tags"
                    >
                      +
                    </button>
                  </div>
                </td>
                <td className={td + ' text-gray-500 whitespace-nowrap'}>{fmtBytes(f.size)}</td>
                <td className={td + ' text-gray-500 whitespace-nowrap'}>{f.mime_type || '—'}</td>
                <td className={td + ' text-gray-500 whitespace-nowrap'}>{fmtTime(f.created_at)}</td>
                <td className={td + ' text-right whitespace-nowrap'}>
                  <button onClick={() => openCastModal(f)} className="text-[11px] px-2 py-1 rounded bg-brand-500/15 text-brand-300 hover:bg-brand-500/25 mr-1 font-medium" title="Cast to screen">Cast</button>
                  <button onClick={() => setPreviewFile(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">Preview</button>
                  <button onClick={() => onDownload(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">Download</button>
                  <button onClick={() => onCopyDeepLink(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">
                    {copiedHash === f.hash ? 'Copied' : 'Link'}
                  </button>
                  <button onClick={() => onShare(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">Share</button>
                  <button onClick={() => onDelete(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300">Delete</button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
