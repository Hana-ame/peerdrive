import React from 'react';
import {
  fmtDuration,
  fmtCount,
  fmtDate,
  iwaraUrl,
  entryQualities,
  pickedUrl,
} from '../utils/formatters';

const TH = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';
const TD = 'px-3 py-2';
const ACT = 'text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300';

function Cover({ entry, size = 40 }) {
  if (!entry.cover) {
    return (
      <div
        className="rounded-lg bg-white/[0.04] border border-white/[0.06] flex items-center justify-center text-[10px] text-gray-600 shrink-0"
        style={{ width: size, height: size }}
      >
        No preview
      </div>
    );
  }
  return (
    <img
      src={entry.cover}
      alt={`preview of ${entry.title}`}
      className="rounded-lg bg-white/[0.04] object-cover shrink-0"
      style={{ width: size, height: size }}
    />
  );
}

function QualityPicker({ entry, value, onChange, className }) {
  const list = entryQualities(entry);
  if (list.length <= 1) return null;
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      aria-label={`quality for ${entry.title}`}
      className={className || 'text-[11px] px-1.5 py-1 rounded bg-white/[0.05] border border-white/[0.08] text-gray-300'}
    >
      {list.map((r) => (
        <option key={r.name} value={r.name}>{r.name}</option>
      ))}
    </select>
  );
}

export default function IwaraVideoList({
  visible,
  view,
  quality,
  setQuality,
  copied,
  onCopy,
}) {
  const renderRowActions = (e) => {
    const url = pickedUrl(e, quality);
    return (
      <>
        <QualityPicker
          entry={e}
          value={quality[e.id] || (entryQualities(e)[0] || {}).name}
          onChange={(v) => setQuality((q) => ({ ...q, [e.id]: v }))}
        />
        <a
          {...(url
            ? { href: url, download: true, target: '_blank', rel: 'noreferrer' }
            : { 'aria-disabled': true, onClick: (ev) => ev.preventDefault() })}
          className={`${ACT} inline-block${url ? '' : ' opacity-40 cursor-not-allowed'}`}
        >
          Download
        </a>
        <button
          type="button"
          onClick={() => onCopy('link:' + e.id, url)}
          className={`${ACT} inline-block mr-1`}
        >
          {copied === 'link:' + e.id ? 'Copied' : 'Copy link'}
        </button>
        <a href={iwaraUrl(e)} target="_blank" rel="noreferrer" className={`${ACT} inline-block`}>
          Open
        </a>
      </>
    );
  };

  if (visible.length === 0) {
    return (
      <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
        <p className="mb-2">No entries</p>
        <p className="text-xs text-gray-600">Clear the filters, or resolve an iwara url above.</p>
      </div>
    );
  }

  if (view === 'grid') {
    return (
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        {visible.map((e) => (
          <div key={e.id} className="card-surface overflow-hidden flex flex-col">
            <div className="aspect-video bg-white/[0.03] flex items-center justify-center overflow-hidden">
              {e.cover ? (
                <img src={e.cover} alt={`preview of ${e.title}`} className="w-full h-full object-cover" />
              ) : (
                <span className="text-xs text-gray-600">No preview</span>
              )}
            </div>
            <div className="p-3 flex flex-col gap-2 flex-1">
              <div className="text-sm text-gray-200 truncate" title={e.title}>{e.title}</div>
              <div className="text-xs text-gray-500 flex items-center gap-1.5 flex-wrap">
                <span>{e.author || 'Unknown'}</span>
                <span>·</span>
                <span>{fmtDuration(e.duration)}</span>
                <span>·</span>
                <span>{fmtCount(e.views)} views</span>
                {e.sample && (
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-gray-400 border border-white/[0.08]">
                    Sample data
                  </span>
                )}
              </div>
              <div className="mt-auto">{renderRowActions(e)}</div>
            </div>
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="card-surface overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-white/[0.03]">
          <tr>
            <th className={TH}>Preview</th>
            <th className={TH}>Title</th>
            <th className={TH}>Author</th>
            <th className={TH}>Duration</th>
            <th className={TH}>Views</th>
            <th className={TH}>Uploaded</th>
            <th className={TH + ' text-right'}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {visible.map((e) => (
            <tr key={e.id} className="border-t border-white/[0.04] hover:bg-white/[0.02]">
              <td className={TD}><Cover entry={e} /></td>
              <td className={TD + ' text-gray-200 max-w-[280px]'}>
                <span className="block truncate" title={e.title}>{e.title}</span>
                {e.sample && <span className="block text-[10px] text-gray-600">sample data</span>}
              </td>
              <td className={TD + ' text-gray-500 whitespace-nowrap'}>{e.author || 'Unknown'}</td>
              <td className={TD + ' text-gray-500 whitespace-nowrap'}>{fmtDuration(e.duration)}</td>
              <td className={TD + ' text-gray-500 whitespace-nowrap'}>{fmtCount(e.views)}</td>
              <td className={TD + ' text-gray-500 whitespace-nowrap'}>{fmtDate(e.uploaded)}</td>
              <td className={TD + ' text-right whitespace-nowrap'}>{renderRowActions(e)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
