import React from 'react';
import CollectionDetailRow from './CollectionDetailRow';
import { VIS_LABEL, fmtTime } from '../utils/collectionUtils';

const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

export default function CollectionTableView({
  collections,
  openDetail,
  detail,
  navigate,
  unlockPasscode,
  setUnlockPasscode,
  unlockProtected,
  downloadEntry,
  entryHash,
  busy,
}) {
  return (
    <div className="card-surface overflow-x-auto max-w-full">
      <table className="w-full text-sm min-w-[560px]">
        <thead className="bg-white/[0.03]">
          <tr>
            <th className={th}>Name</th>
            <th className={th}>Tags</th>
            <th className={th}>Visibility</th>
            <th className={th}>Version</th>
            <th className={th}>Entries</th>
            <th className={th}>Created</th>
          </tr>
        </thead>
        <tbody>
          {collections.map((c, i) => {
            const v = VIS_LABEL[c.visibility] || VIS_LABEL.public;
            return (
              <React.Fragment key={c.hash || i}>
                <tr
                  onClick={() => openDetail(c)}
                  className="border-t border-white/[0.04] hover:bg-white/[0.02] cursor-pointer"
                >
                  <td className="px-3 py-2 text-gray-200">
                    <div className="flex items-center gap-2">
                      <span>📦</span>
                      <span className="font-medium truncate max-w-[200px]">{c.friendly_name || 'Unnamed'}</span>
                    </div>
                  </td>
                  <td className="px-3 py-2 text-gray-400 text-xs">
                    <div className="flex gap-1 flex-wrap">
                      {Array.isArray(c.tags) && c.tags.length > 0 ? (
                        c.tags.map((t, ti) => (
                          <span key={ti} className="text-[10px] px-1.5 py-0.5 rounded bg-brand-500/10 text-brand-300">
                            #{t}
                          </span>
                        ))
                      ) : (
                        <span className="text-gray-600">—</span>
                      )}
                    </div>
                  </td>
                  <td className="px-3 py-2 text-gray-500 whitespace-nowrap">
                    {v.icon} {v.label}
                  </td>
                  <td className="px-3 py-2 text-gray-500 whitespace-nowrap">v{c.version}</td>
                  <td className="px-3 py-2 text-gray-500 whitespace-nowrap">
                    {c.entry_count ?? c.entries?.length ?? '—'}
                  </td>
                  <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{fmtTime(c.created_at)}</td>
                </tr>
                {detail?.hash === (c.hash || c.current_hash) && (
                  <CollectionDetailRow
                    detail={detail}
                    navigate={navigate}
                    unlockPasscode={unlockPasscode}
                    setUnlockPasscode={setUnlockPasscode}
                    unlockProtected={unlockProtected}
                    downloadEntry={downloadEntry}
                    entryHash={entryHash}
                    busy={busy}
                  />
                )}
              </React.Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
