// Module 3: Collections — browse local anonymous collections / create new from netdisk files / view entries and download.
import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import * as ws from '../ws';
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
  const [visibility, setVisibility] = useState('public');

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
    setBusy(true);
    try {
      await ws.admin('POST', '/anon/collections', {
        friendly_name: name.trim(),
        entries,
        tags: [],
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

  // 取数键兼容新旧两种条目格式（sha/hash/providers，见 lib/collectionTree.js）：
  // 后端落地新格式后 entries[].sha 才是主字段，旧代码只认 e.hash 会让详情页的
  // 下载按钮在新格式下全部失效。
  const entryHash = (e) => entrySha(e);

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">Collections</h1>
            <p className="text-sm text-gray-500 mt-0.5">Content-addressed versioned file collections (anonymous / hash-addressed)</p>
          </div>
          <button onClick={openCreate} className="btn-brand">+ New Collection</button>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {tab === 'list' && (
          collections === null ? (
            <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>
          ) : collections.length === 0 ? (
            <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
              <p>No collections yet</p>
              <p className="text-xs text-gray-600 mt-1">Click "New Collection" to create from netdisk files.</p>
            </div>
          ) : (
            <div className="card-surface overflow-hidden">
              <table className="w-full text-sm">
                <thead className="bg-white/[0.03]">
                  <tr>
                    <th className={th}>Name</th>
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
                        <tr onClick={() => openDetail(c)}
                          className="border-t border-white/[0.04] hover:bg-white/[0.02] cursor-pointer">
                          <td className="px-3 py-2 text-gray-200">{c.friendly_name || 'Unnamed'}</td>
                          <td className="px-3 py-2 text-gray-500">{v.icon} {v.label}</td>
                          <td className="px-3 py-2 text-gray-500">v{c.version}</td>
                          <td className="px-3 py-2 text-gray-500">{c.entry_count ?? c.entries?.length ?? '—'}</td>
                          <td className="px-3 py-2 text-gray-500">{fmtTime(c.created_at)}</td>
                        </tr>
                        {detail?.hash === (c.hash || c.current_hash) && (
                          <tr className="bg-white/[0.02]">
                            <td colSpan={5} className="px-4 py-3">
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
          )
        )}

        {tab === 'create' && (
          <div className="card-surface p-5 space-y-4">
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">Collection Name</label>
              <input value={name} onChange={e => setName(e.target.value)} placeholder="Collection name"
                className="input-base max-w-md" />
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
