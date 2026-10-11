import React from 'react';
import { fmtBytes } from '../../../platform/shared/format';
import { kindOf } from '../../../platform/shared/mime';

export default function NodeShareList({
  share,
  collOpen,
  setCollOpen,
  onOpenFile,
}) {
  if (share === null) {
    return <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>;
  }

  if ((share.total ?? 0) === 0) {
    return (
      <div className="text-center py-16 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
        <p>This node has no shared content</p>
        <p className="text-xs text-gray-600 mt-1">
          The peer has not enabled external sharing, or no shared directories/files are declared.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {share.files?.length > 0 && (
        <div className="card-surface overflow-hidden">
          <div className="px-4 py-2 bg-white/[0.03] text-[10px] uppercase tracking-wider text-gray-500">
            Standalone Files
          </div>
          <ul className="divide-y divide-white/[0.04]">
            {share.files.map((f, i) => (
              <li key={i} className="flex items-center gap-3 px-4 py-2.5 text-sm hover:bg-white/[0.02]">
                <button
                  onClick={() => onOpenFile(f)}
                  title={kindOf(f.path || f.name) ? 'Click to preview' : 'Click to download'}
                  className="flex-1 truncate text-left text-gray-200 hover:text-brand-300 cursor-pointer transition-colors"
                >
                  {f.path || f.name}
                </button>
                <span className="text-gray-500 shrink-0 whitespace-nowrap">{fmtBytes(f.size)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {share.collections?.length > 0 && (
        <div className="card-surface overflow-hidden">
          <div className="px-4 py-2 bg-white/[0.03] text-[10px] uppercase tracking-wider text-gray-500">
            Collections
          </div>
          <ul className="divide-y divide-white/[0.04]">
            {share.collections.map((c, i) => {
              const entries = Array.isArray(c.entries) ? c.entries : [];
              const open = collOpen === i;
              return (
                <li key={i}>
                  <button
                    onClick={() => setCollOpen(open ? null : i)}
                    className="w-full flex items-center gap-3 px-4 py-2.5 text-sm hover:bg-white/[0.02] text-left"
                  >
                    <span className={open ? 'rotate-90 transition-transform' : 'transition-transform'}>▶</span>
                    <span className="flex-1 truncate text-gray-200">{c.name || String(c.hash).slice(0, 12) + '…'}</span>
                    <span className="text-gray-500 shrink-0">{entries.length} entries</span>
                  </button>
                  {open && (
                    <ul className="bg-white/[0.02] px-4 pb-2">
                      {entries.map((e, ei) => (
                        <li key={ei} className="flex items-center gap-3 py-1.5 text-sm">
                          <button
                            onClick={() => onOpenFile({ ...e, name: e.path || 'download' })}
                            title={kindOf(e.path) ? 'Click to preview' : 'Click to download'}
                            className="flex-1 truncate text-left text-gray-400 hover:text-brand-300 cursor-pointer transition-colors"
                          >
                            {e.path}
                          </button>
                          <span className="text-gray-500 shrink-0 whitespace-nowrap">{fmtBytes(e.size)}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </div>
  );
}
