// Iwara.jsx — iwara.tv video browser.
// Data path:
//   ws.admin('GET', '/iwara/video/:id') → gin → internal/echproxy.GetVideoMeta
//   (/video/{id} → fileUrl → X-Version signed resolution list).
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import {
  SAMPLE_ENTRY,
  fmtDuration,
  fmtCount,
  fmtDate,
  iwaraUrl,
  parseIds,
} from '../utils/formatters';
import IwaraVideoList from '../components/IwaraVideoList';

// Re-export format utilities so tests and callers maintain 100% backward compatibility
export { fmtDuration, fmtCount, fmtDate, iwaraUrl, parseIds };

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

  // resolve: turn pasted IDs/URLs into entries.
  const resolve = useCallback(async (rawIds) => {
    const ids = rawIds.map((s) => String(s).trim()).filter(Boolean);
    if (ids.length === 0) {
      setErr('Enter an iwara URL or video id');
      return;
    }
    setBusy(true);
    setErr('');
    setNotice('');
    const ok = [];
    const fails = [];
    for (const raw of ids) {
      try {
        const meta = await ws.admin('GET', '/iwara/video/' + encodeURIComponent(raw));
        if (meta) ok.push(meta);
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

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return entries.filter((e) => {
      if (authorFilter && (e.author || 'Unknown') !== authorFilter) return false;
      if (!q) return true;
      return [e.title, e.author, e.id, e.authorId]
        .some((f) => String(f || '').toLowerCase().includes(q));
    });
  }, [entries, query, authorFilter]);

  const copy = async (key, text) => {
    if (!text) return;
    try {
      await navigator.clipboard?.writeText(text);
      setCopied(key);
      setTimeout(() => setCopied(''), 1500);
    } catch {
      // clipboard unavailable fallback
    }
  };

  return (
    <div className="p-4 sm:p-8 overflow-y-auto overflow-x-hidden h-full max-w-full">
      <div className="max-w-5xl mx-auto">
        {/* Header toolbar */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
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
                className={`text-[11px] px-2.5 py-1 rounded ${
                  view === v
                    ? 'bg-brand-500/20 text-brand-300 border border-brand-400/30'
                    : 'bg-white/[0.05] text-gray-400 border border-white/[0.08]'
                }`}
              >
                {v === 'grid' ? 'Grid' : 'List'}
              </button>
            ))}
          </div>
        </div>

        {/* Input box */}
        <div className="card-surface p-4 mb-4">
          <form onSubmit={onSubmit} className="flex flex-wrap sm:flex-nowrap gap-2">
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

        {/* Search & Author filter controls */}
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

        {/* Video entries list (grid / table) */}
        <IwaraVideoList
          visible={visible}
          view={view}
          quality={quality}
          setQuality={setQuality}
          copied={copied}
          onCopy={copy}
        />

        {sampleMode && (
          <p className="text-[11px] text-gray-600 mt-4 text-center">
            Showing sample data — the iwara module is not enabled on this node, so nothing here is a real video.
          </p>
        )}
      </div>
    </div>
  );
}
