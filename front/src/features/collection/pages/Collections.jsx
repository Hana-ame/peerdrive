// Module 3: Collections — browse local anonymous collections / create new from netdisk files / view entries and download.
import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import * as ws from '../../../ws';
import { entrySha } from '../lib/collectionTree';

const VIS_LABEL = {
  public: { icon: '🌐', label: 'Public' },
  restricted: { icon: '👥', label: 'Authorized Only' },
  private: { icon: '🔒', label: 'Self Only' },
};

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? ts : d.toLocaleString('en-US', { hour12: false });
}

export default function Collections() {
  const navigate = useNavigate();
  const [tab, setTab] = useState('list'); // list | create
  const [collections, setCollections] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  // Create-mode state
  const [files, setFiles] = useState([]);
  const [selected, setSelected] = useState([]);
  const [name, setName] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const [visibility, setVisibility] = useState('public');

  // Search & filter state
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedTag, setSelectedTag] = useState('');
  const [viewMode, setViewMode] = useState('list'); // list | grid

  // Detail-mode state
  const [detail, setDetail] = useState(null); // {hash, data}

  const loadList = useCallback(async () => {
    setErr('');
    try {
      const res = await ws.admin('GET', '/anon/collections');
      setCollections(Array.isArray(res) ? res : []);
    } catch (e) { setErr(e?.message || String(e)); setCollections([]); }
  }, []);
  useEffect(() => { loadList(); }, [loadList]);

  const openCreate = async () => {
    setErr('');
    setTab('create');
    setSelected([]);
    setName('');
    setTagsInput('');
    try {
      const res = await ws.admin('GET', '/files');
      setFiles(Array.isArray(res) ? res : []);
    } catch (e) { setErr(e?.message || String(e)); setFiles([]); }
  };

  const toggleFile = (hash) => {
    setSelected(prev => prev.includes(hash) ? prev.filter(h => h !== hash) : [...prev, hash]);
  };

  const create = async () => {
    if (!name.trim()) { setErr('Please enter a collection name'); return; }
    const chosen = files.filter(f => selected.includes(f.hash));
    if (chosen.length === 0) { setErr('Please select at least one file'); return; }
    const entries = chosen.map(f => ({
      path: f.filename,
      providers: [{ type: 'sha256', value: f.hash, mime_type: f.mime_type }],
    }));
    const tags = tagsInput
      .split(',')
      .map(t => t.trim())
      .filter(Boolean);

    setBusy(true);
    try {
      await ws.admin('POST', '/anon/collections', {
        friendly_name: name.trim(),
        entries,
        tags,
        visibility,
      });
      setTab('list');
      await loadList();
    } catch (e) { setErr(e?.message || String(e)); }
    finally { setBusy(false); }
  };

  const openDetail = async (c) => {
    if (detail?.hash === (c.hash || c.current_hash)) { setDetail(null); return; }
    setErr('');
    setDetail({ hash: c.hash || c.current_hash, data: null });
    try {
      const d = await ws.admin('GET', `/anon/collections/${c.hash || c.current_hash}`);
      setDetail({ hash: c.hash || c.current_hash, data: d });
    } catch (e) {
      setErr(e?.message || String(e));
      setDetail(null);
    }
  };

  const downloadEntry = async (hash, path) => {
    try { await ws.downloadToFile(hash, path || 'download'); }
    catch (e) { setErr(e?.message || String(e)); }
  };

  const entryHash = (e) => entrySha(e);

  // Compute all available tags across collections with occurrence count
  const allTagsWithCounts = React.useMemo(() => {
    if (!collections) return [];
    const map = {};
    collections.forEach(c => {
      if (Array.isArray(c.tags)) {
        c.tags.forEach(t => {
          if (t) map[t] = (map[t] || 0) + 1;
        });
      }
    });
    return Object.entries(map).map(([tag, count]) => ({ tag, count }));
  }, [collections]);

  // Filter collections by search query and selected tag
  const filteredCollections = React.useMemo(() => {
    if (!collections) return [];
    return collections.filter(c => {
      const matchQuery = !searchQuery.trim() ||
        (c.friendly_name && c.friendly_name.toLowerCase().includes(searchQuery.toLowerCase())) ||
        (c.hash && c.hash.toLowerCase().includes(searchQuery.toLowerCase())) ||
        (Array.isArray(c.tags) && c.tags.some(t => t.toLowerCase().includes(searchQuery.toLowerCase())));

      const matchTag = !selectedTag || (Array.isArray(c.tags) && c.tags.includes(selectedTag));

      return matchQuery && matchTag;
    });
  }, [collections, searchQuery, selectedTag]);

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">Collections</h1>
            <p className="text-sm text-gray-500 mt-0.5">Content-addressed versioned file collections (anonymous / hash-addressed)</p>
          </div>
          <div className="flex items-center gap-2">
            {tab === 'list' && (
              <div className="flex items-center bg-white/[0.04] p-0.5 rounded-lg border border-white/[0.06] text-xs">
                <button
                  onClick={() => setViewMode('list')}
                  className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'list' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
                  title="List View"
                >
                  ☰ List
                </button>
                <button
                  onClick={() => setViewMode('grid')}
                  className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'grid' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
                  title="Grid View"
                >
                  ▦ Grid
                </button>
              </div>
            )}
            <button onClick={openCreate} className="btn-brand">+ New Collection</button>
          </div>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {tab === 'list' && (
          <div>
            {/* Search and Tag Filtering Toolbar */}
            <div className="mb-4 space-y-3">
              <div className="flex items-center gap-3">
                <div className="relative flex-1">
                  <span className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500">
                    🔍
                  </span>
                  <input
                    type="text"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder="Search collections by name, hash, or tags..."
                    className="input-base pl-9 w-full text-xs"
                  />
                  {searchQuery && (
                    <button
                      onClick={() => setSearchQuery('')}
                      className="absolute inset-y-0 right-0 pr-3 flex items-center text-gray-500 hover:text-white text-xs"
                    >
                      ✕
                    </button>
                  )}
                </div>
              </div>

              {allTagsWithCounts.length > 0 && (
                <div className="flex items-center gap-1.5 flex-wrap text-xs">
                  <span className="text-gray-500 text-[11px] mr-1">Tags:</span>
                  <button
                    onClick={() => setSelectedTag('')}
                    className={`px-2 py-0.5 rounded-full border transition-colors text-[11px] ${
                      selectedTag === ''
                        ? 'bg-brand-600 text-white border-brand-500'
                        : 'bg-white/[0.04] text-gray-400 border-white/[0.08] hover:text-white'
                    }`}
                  >
                    All ({collections?.length || 0})
                  </button>
                  {allTagsWithCounts.map(({ tag, count }) => (
                    <button
                      key={tag}
                      onClick={() => setSelectedTag(selectedTag === tag ? '' : tag)}
                      className={`px-2 py-0.5 rounded-full border transition-colors text-[11px] ${
                        selectedTag === tag
                          ? 'bg-brand-600 text-white border-brand-500'
                          : 'bg-white/[0.04] text-gray-400 border-white/[0.08] hover:text-white'
                      }`}
                    >
                      #{tag} <span className="opacity-60 text-[10px]">({count})</span>
                    </button>
                  ))}
                </div>
              )}
            </div>

            {collections === null ? (
              <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>
            ) : filteredCollections.length === 0 ? (
              <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
                {collections.length === 0 ? (
                  <>
                    <p>No collections yet</p>
                    <p className="text-xs text-gray-600 mt-1">Click "New Collection" to create from netdisk files.</p>
                  </>
                ) : (
                  <>
                    <p>No matching collections found</p>
                    <p className="text-xs text-gray-600 mt-1">Try clearing your search query or tag filter.</p>
                  </>
                )}
              </div>
            ) : viewMode === 'grid' ? (
              <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-4">
                {filteredCollections.map((c, i) => {
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
                          {Array.isArray(c.tags) && c.tags.slice(0, 2).map((t, ti) => (
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
            ) : (
              <div className="card-surface overflow-hidden">
                <table className="w-full text-sm">
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
                    {filteredCollections.map((c, i) => {
                      const v = VIS_LABEL[c.visibility] || VIS_LABEL.public;
                      return (
                        <React.Fragment key={c.hash || i}>
                          <tr onClick={() => openDetail(c)}
                            className="border-t border-white/[0.04] hover:bg-white/[0.02] cursor-pointer">
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
                            <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{v.icon} {v.label}</td>
                            <td className="px-3 py-2 text-gray-500 whitespace-nowrap">v{c.version}</td>
                            <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{c.entry_count ?? c.entries?.length ?? '—'}</td>
                            <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{fmtTime(c.created_at)}</td>
                          </tr>
                          {detail?.hash === (c.hash || c.current_hash) && (
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
                                        onClick={() => navigate(`/collection/${detail.hash}`)}
                                        className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 shrink-0">
                                        📁 Browse as folders
                                      </button>
                                    </div>
                                    {Array.isArray(detail.data.entries) && detail.data.entries.length > 0 ? (
                                      <ul className="space-y-1">
                                        {detail.data.entries.map((e, ei) => (
                                          <li key={ei} className="flex items-center gap-3 text-xs">
                                            <span className="flex-1 truncate text-gray-300">{e.path}</span>
                                            <span className="font-mono text-xs text-gray-600">{String(entryHash(e) || '').slice(0, 12)}…</span>
                                            <button onClick={() => entryHash(e) && downloadEntry(entryHash(e), e.path)}
                                              disabled={!entryHash(e)}
                                              className="px-2 py-0.5 text-xs bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 rounded disabled:opacity-40">Download</button>
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
                          )}
                        </React.Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}

        {tab === 'create' && (
          <div className="card-surface p-5 space-y-4">
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">Collection Name</label>
              <input value={name} onChange={e => setName(e.target.value)} placeholder="Collection name"
                className="input-base max-w-md" />
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">Tags (comma-separated)</label>
              <input
                value={tagsInput}
                onChange={e => setTagsInput(e.target.value)}
                placeholder="e.g. photos, project-alpha, backup"
                className="input-base max-w-md text-xs"
              />
              <p className="text-[11px] text-gray-500 mt-1">Optional tags to organize and search collections.</p>
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">Visibility</label>
              <div className="flex gap-1">
                {Object.entries(VIS_LABEL).map(([k, v]) => (
                  <button key={k} onClick={() => setVisibility(k)}
                    className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                      visibility === k ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
                    }`}>
                    {v.icon} {v.label}
                  </button>
                ))}
              </div>
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">Select Netdisk Files ({selected.length} selected)</label>
              {files.length === 0 ? (
                <p className="text-xs text-gray-600">No files in netdisk yet. Go to "My Cloud Drive" to upload first.</p>
              ) : (
                <ul className="border border-white/[0.06] rounded-lg divide-y divide-white/[0.04] max-h-72 overflow-y-auto">
                  {files.map((f, i) => (
                    <li key={f.hash || i}
                      onClick={() => toggleFile(f.hash)}
                      className={`flex items-center gap-3 px-3 py-2 text-xs cursor-pointer hover:bg-white/[0.03] ${
                        selected.includes(f.hash) ? 'bg-brand-500/10' : ''
                      }`}>
                      <input type="checkbox" readOnly checked={selected.includes(f.hash)} className="accent-brand-500 pointer-events-none" />
                      <span className="flex-1 truncate text-gray-300">{f.filename}</span>
                      <span className="text-gray-500 shrink-0">{f.size} B</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div className="flex gap-2">
              <button onClick={create} disabled={busy || selected.length === 0}
                className="btn-brand">Create Collection</button>
              <button onClick={() => setTab('list')} className="btn-ghost">Cancel</button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
