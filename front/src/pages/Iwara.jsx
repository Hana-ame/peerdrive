// Iwara.jsx — iwara.tv video browser.
//
// Discovery context: the first cut of this page copied a BBS/forum post layout
// (185px author sidebar, floor numbers, "only show OP"/block dropdown, A-/A/A+
// font toggles, reply/quote actions, .tpc_content / .authorbox / .readbot CSS).
// That was the wrong reference — this is a content browser, not a forum, so the
// page now follows the same shape as the netdisk pages: Drive.jsx (header +
// top-right control + one card-surface table + row hover + 11px action buttons)
// and CollectionView.jsx (input bar → loading → error → ready phases, dashed
// empty state, "Sample"/reset control). What is unchanged: the data path.
//
// Data path:
//   ws.admin('GET', '/iwara/video/:id') → gin → internal/echproxy.GetVideoMeta
//   (/video/{id} → fileUrl → X-Version signed resolution list).
// The node only registers that route when PEERDRIVE_IWARA_ENABLE=true, so with
// the default config the request comes back as gin's plain "404 page not found"
// body. The page uses that body shape to tell "module off" apart from "video not
// found" (a JSON {"error": ...}) instead of guessing from the status code.
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import * as ws from '../ws';

// SAMPLE_ENTRY keeps the layout inspectable when the node has no iwara module.
// It is marked sample:true so the cards render a badge and never show a fake
// download link — an empty downloadUrl renders a disabled control instead.
const SAMPLE_ENTRY = {
  id: 'sample-0001',
  title: 'Sample video (placeholder)',
  author: 'SampleUploader',
  authorId: 'u-sample',
  cover: '',
  duration: 182,
  views: 12800,
  uploaded: 0,
  resolutions: [
    { name: '1080p', downloadUrl: '' },
    { name: '720p', downloadUrl: '' },
  ],
  sample: true,
};

// fmtDuration: seconds → m:ss / h:mm:ss. 0 and missing both render '—' rather
// than '0:00' — a real zero-length video is not the same as "not reported".
export function fmtDuration(sec) {
  if (!sec || sec <= 0) return '—';
  const s = Math.round(sec);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  const p2 = (n) => String(n).padStart(2, '0');
  return h ? `${h}:${p2(m)}:${p2(r)}` : `${m}:${p2(r)}`;
}

// fmtCount: view counts → 950 / 12.8k / 1.2M, so the column stays narrow.
export function fmtCount(n) {
  if (!n || n <= 0) return '—';
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e4) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
}

// fmtDate: unix seconds → 'YYYY-MM-DD HH:mm'. 0 renders '—'.
export function fmtDate(ts) {
  if (!ts || ts <= 0) return '—';
  const d = new Date(ts * 1000);
  if (Number.isNaN(d.getTime())) return '—';
  const p2 = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
}

export function iwaraUrl(entry) {
  return `https://www.iwara.tv/videos/${entry.id}`;
}

// parseIds: pasted text -> one id per request. Also splits on full-width commas
// (， and 、), which is how a Chinese keyboard enters the separator when someone
// pastes a list out of a note or chat message.
export function parseIds(text) {
  return String(text || '')
    .split(/[\s,，、]+/)
    .map((t) => t.trim())
    .filter(Boolean);
}

// entryQualities: the resolution rows worth offering as download choices, empty
// names dropped so the <select> never has a blank first option.
function entryQualities(entry) {
  return (Array.isArray(entry.resolutions) ? entry.resolutions : [])
    .filter((r) => r && r.name);
}

// pickedUrl: the download URL for the quality the user picked on this entry.
// The selection lives in the page (not the backend) — the backend returns every
// resolution, picking one is purely a display choice.
function pickedUrl(entry, quality) {
  const list = entryQualities(entry);
  const name = quality[entry.id] || (list[0] && list[0].name);
  return (list.find((r) => r.name === name) || {}).downloadUrl || '';
}

// Cover: preview image or a labelled placeholder. iwara returns only one cover
// URL per video, so a missing cover is expected rather than an error — hence the
// neutral box instead of a broken-image glyph.
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

// QualityPicker: per-row resolution <select>. A plain control (not a dropdown
// menu) because it is data selection, like choosing a download size.
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

const TH = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';
const TD = 'px-3 py-2';
const ACT = 'text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300';

export default function Iwara() {
  const { id } = useParams();
  const [input, setInput] = useState('');
  const [entries, setEntries] = useState([SAMPLE_ENTRY]);
  const [sampleMode, setSampleMode] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [notice, setNotice] = useState('');
  const [view, setView] = useState('grid');
  const [query, setQuery] = useState('');
  const [authorFilter, setAuthorFilter] = useState('');
  const [quality, setQuality] = useState({});
  const [copied, setCopied] = useState('');

  // resolve: turn pasted IDs/URLs into entries. Each id is one independent
  // request — a partial failure must not discard the ids that did work.
  const resolve = useCallback(async (rawIds) => {
    const ids = rawIds.map((s) => String(s).trim()).filter(Boolean);
    if (ids.length === 0) { setErr('Enter an iwara URL or video id'); return; }
    setBusy(true);
    setErr('');
    setNotice('');
    const ok = [];
    const fails = [];
    for (const raw of ids) {
      try {
        const meta = await ws.admin('GET', '/iwara/video/' + encodeURIComponent(raw));
        if (meta) ok.push(meta); // a null body would crash the renderers
      } catch (e) {
        fails.push(e);
      }
    }
    setBusy(false);
    if (ok.length > 0) {
      setEntries(ok);
      setSampleMode(false);
      setNotice(`Resolved ${ok.length} of ${ids.length}` + (fails.length ? `, ${fails.length} failed` : ''));
      return;
    }
    // Nothing resolved. Branch on the error body shape: a string is gin's
    // "404 page not found" for an unregistered route (module off), a JSON object
    // is the backend's own error, and no body at all means the ws session is
    // offline. Kept separate so the message tells the user what to do next.
    const first = fails[0];
    const data = first && first.data;
    setEntries([SAMPLE_ENTRY]);
    setSampleMode(true);
    if (typeof data === 'string') {
      setErr('The node does not have the iwara module enabled (PEERDRIVE_IWARA_ENABLE=false is the default). so the entry below is sample data — resolve a real video id to replace it.');
    } else if (data && typeof data === 'object') {
      setErr((first && first.message) || 'Could not resolve that video');
    } else {
      setErr('Not connected to a local node, so iwara ids cannot be resolved.');
    }
  }, []);

  // Deep link: /iwara/:id resolves that id on mount.
  useEffect(() => {
    if (id) resolve([id]);
  }, [id, resolve]);

  const onSubmit = (e) => {
    e.preventDefault();
    resolve(parseIds(input));
  };

  const loadSample = () => {
    setEntries([SAMPLE_ENTRY]);
    setSampleMode(true);
    setErr('');
    setNotice('Sample data, not a real video');
  };

  const authors = useMemo(
    () => [...new Set(entries.map((e) => e.author || 'Unknown').filter(Boolean))].sort(),
    [entries],
  );

  // visible: search box + author filter applied to the loaded entries. Both are
  // local only — the backend has no search endpoint for resolved videos.
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return entries.filter((e) => {
      if (authorFilter && (e.author || 'Unknown') !== authorFilter) return false;
      if (!q) return true;
      return [e.title, e.author, e.id, e.authorId]
        .some((f) => String(f || '').toLowerCase().includes(q));
    });
  }, [entries, query, authorFilter]);

  // copy: the same clipboard path Drive.jsx uses for share links, in one place
  // so the "Copied" feedback timer cannot be spawned twice per click.
  const copy = async (key, text) => {
    if (!text) return;
    try {
      await navigator.clipboard?.writeText(text);
      setCopied(key);
      setTimeout(() => setCopied(''), 1500);
    } catch { /* clipboard unavailable: the label just stays "Copy link" */ }
  };

  // rowActions: quality select + download + copy direct link + open on iwara.
  // Download is disabled when the picked quality has no URL (sample data, or the
  // backend returned a resolution without a downloadUrl) — an anchor with no
  // href would look clickable and do nothing, which is worse than a disabled one.
  const rowActions = (e) => {
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
          className={`${ACT} inline-block${url ? '' : ' opacity-40 cursor-not-allowed'}`}>
          Download
        </a>
        <button type="button" onClick={() => copy('link:' + e.id, url)} className={`${ACT} inline-block mr-1`}>
          {copied === 'link:' + e.id ? 'Copied' : 'Copy link'}
        </button>
        <a href={iwaraUrl(e)} target="_blank" rel="noreferrer" className={ACT + ' inline-block'}>Open</a>
      </>
    );
  };

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">Iwara</h1>
            <p className="text-sm text-gray-500 mt-0.5">Resolve iwara.tv videos to direct download links</p>
          </div>
          <div className="flex items-center gap-1">
            {['grid', 'list'].map((v) => (
              <button
                key={v}
                type="button"
                onClick={() => setView(v)}
                className={`text-[11px] px-2.5 py-1 rounded ${view === v ? 'bg-brand-500/20 text-brand-300 border border-brand-400/30' : 'bg-white/[0.05] text-gray-400 border border-white/[0.08]'}`}>
                {v === 'grid' ? 'Grid' : 'List'}
              </button>
            ))}
          </div>
        </div>

        <div className="card-surface p-4 mb-4">
          <form onSubmit={onSubmit} className="flex gap-2">
            <input
              className="input-base flex-1"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="https://www.iwara.tv/videos/xxxxxxxx or a video id"
              aria-label="iwara URL or video id"
            />
            <button type="submit" disabled={busy} className="btn-brand shrink-0">
              {busy ? 'Resolving...' : 'Resolve'}
            </button>
            <button type="button" onClick={loadSample} className="btn-ghost">Sample</button>
          </form>
          {err && (
            <div className="mt-3 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2" role="alert">
              {err}
            </div>
          )}
          {notice && !err && <p className="mt-3 text-[11px] text-gray-500">{notice}</p>}
        </div>

        <div className="flex items-center gap-3 mb-3 flex-wrap">
          <span className="text-xs text-gray-500">
            {visible.length} {visible.length === 1 ? 'entry' : 'entries'}
          </span>
          <input
            className="input-base flex-1 min-w-[160px] text-xs"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search title or author"
            aria-label="search entries"
          />
          <select
            className="input-base text-xs"
            value={authorFilter}
            onChange={(e) => setAuthorFilter(e.target.value)}
            aria-label="filter by author"
          >
            <option value="">All authors</option>
            {authors.map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </div>

        {visible.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p className="mb-2">No entries</p>
            <p className="text-xs text-gray-600">Clear the filters, or resolve an iwara url above.</p>
          </div>
        ) : view === 'grid' ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {visible.map((e) => (
              <div key={e.id} className="card-surface overflow-hidden flex flex-col">
                <div className="aspect-video bg-white/[0.03] flex items-center justify-center overflow-hidden">
                  {e.cover
                    ? <img src={e.cover} alt={`preview of ${e.title}`} className="w-full h-full object-cover" />
                    : <span className="text-xs text-gray-600">No preview</span>}
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
                      <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-gray-400 border border-white/[0.08]">Sample data</span>
                    )}
                  </div>
                  <div className="mt-auto">{rowActions(e)}</div>
                </div>
              </div>
            ))}
          </div>
        ) : (
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
                    <td className={TD + ' text-right whitespace-nowrap'}>{rowActions(e)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {sampleMode && (
          <p className="text-[11px] text-gray-600 mt-4 text-center">
            Showing sample data — the iwara module is not enabled on this node, so nothing here is a real video.
          </p>
        )}
      </div>
    </div>
  );
}
