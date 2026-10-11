import React from 'react';

export default function CollectionDetailRow({
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
    <tr className="bg-white/[0.02]">
      <td colSpan={6} className="px-4 py-3">
        {detail.data === null ? (
          <span className="text-xs text-gray-500">Loading...</span>
        ) : (
          <div>
            <div className="flex items-center justify-between gap-2 mb-2">
              <p className="text-xs text-gray-500">
                hash: <span className="font-mono text-gray-400 break-all">{detail.hash}</span>
              </p>
              <button
                type="button"
                onClick={() => navigate(`/collection/${detail.hash}`)}
                className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 shrink-0"
              >
                📁 Browse as folders
              </button>
            </div>
            {detail.data?.is_protected && (
              <div className="flex items-center gap-2 mb-3 p-2.5 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-200 text-xs">
                <span>🔒 Protected collection (entries hidden). Enter passcode:</span>
                <input
                  type="password"
                  placeholder="Passcode / 提取码"
                  value={unlockPasscode}
                  onChange={e => setUnlockPasscode(e.target.value)}
                  className="input-base text-xs py-1 px-2 w-36"
                  onKeyDown={e => {
                    if (e.key === 'Enter') unlockProtected(detail.hash);
                  }}
                />
                <button
                  type="button"
                  onClick={() => unlockProtected(detail.hash)}
                  disabled={busy || !unlockPasscode.trim()}
                  className="btn-brand text-xs py-1 px-2.5"
                >
                  Unlock
                </button>
              </div>
            )}
            {Array.isArray(detail.data.entries) && detail.data.entries.length > 0 ? (
              <ul className="space-y-1">
                {detail.data.entries.map((e, ei) => (
                  <li key={ei} className="flex items-center gap-3 text-xs">
                    <span className="flex-1 truncate text-gray-300">{e.path}</span>
                    <span className="font-mono text-xs text-gray-600">
                      {String(entryHash(e) || '').slice(0, 12)}…
                    </span>
                    <button
                      type="button"
                      onClick={() => entryHash(e) && downloadEntry(entryHash(e), e.path)}
                      disabled={!entryHash(e)}
                      className="px-2 py-0.5 text-xs bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 rounded disabled:opacity-40"
                    >
                      Download
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-xs text-gray-600">This collection has no entries.</p>
            )}
          </div>
        )}
      </td>
    </tr>
  );
}
