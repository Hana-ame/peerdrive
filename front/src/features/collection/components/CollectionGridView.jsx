import React from 'react';
import { VIS_LABEL } from '../utils/collectionUtils';

export default function CollectionGridView({ collections, openDetail }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-4">
      {collections.map((c, i) => {
        const v = VIS_LABEL[c.visibility] || VIS_LABEL.public;
        return (
          <div
            key={c.hash || i}
            onClick={() => openDetail(c)}
            className="card-surface p-4 rounded-xl border border-white/[0.06] hover:border-brand-500/40 cursor-pointer transition-all flex flex-col justify-between"
          >
            <div>
              <div className="flex items-start justify-between gap-2 mb-2">
                <span className="text-2xl">📦</span>
                <span className="text-[11px] px-2 py-0.5 rounded bg-white/[0.05] text-gray-400">
                  {v.icon} {v.label}
                </span>
              </div>
              <h3 className="font-semibold text-gray-200 text-sm truncate" title={c.friendly_name}>
                {c.friendly_name || 'Unnamed'}
              </h3>
              <p className="font-mono text-[11px] text-gray-500 truncate mt-0.5">
                {c.hash || c.current_hash}
              </p>
            </div>

            <div className="mt-4 pt-3 border-t border-white/[0.04] text-xs text-gray-400 flex items-center justify-between">
              <span>{c.entry_count ?? c.entries?.length ?? 0} files</span>
              <div className="flex gap-1 flex-wrap">
                {Array.isArray(c.tags) &&
                  c.tags.slice(0, 2).map((t, ti) => (
                    <span key={ti} className="text-[10px] px-1.5 py-0.5 rounded bg-brand-500/10 text-brand-300">
                      #{t}
                    </span>
                  ))}
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}
