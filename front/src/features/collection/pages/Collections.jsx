import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { entrySha } from '../../../platform/shared/collectionTree';
import DataState from '../../../components/netdisk/DataState';
import { getNodeSession, onNodeSession } from '../../../lib/nodeSession';
import CreateCollectionForm from '../components/CreateCollectionForm';
import CollectionDetailRow from '../components/CollectionDetailRow';

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
  const [peerSession, setPeerSession] = useState(() => getNodeSession());

  useEffect(() => {
    return onNodeSession((s) => {
      setPeerSession(s);
    });
  }, []);

  // Create-mode state
  const [files, setFiles] = useState([]);
  const [selected, setSelected] = useState([]);
  const [name, setName] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const [visibility, setVisibility] = useState('public');
  const [accessPolicy, setAccessPolicy] = useState('public');
  const [passcode, setPasscode] = useState('');

  // Search & filter state
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedTag, setSelectedTag] = useState('');
  const [viewMode, setViewMode] = useState('list'); // list | grid

  // Detail-mode state
  const [detail, setDetail] = useState(null); // {hash, data}
  const [unlockPasscode, setUnlockPasscode] = useState('');

  const loadList = useCallback(async () => {
    setErr('');
    if (peerSession?.client && ws.getStatus() !== 'open') {
      try {
        const snap = await peerSession.client.shares();
        const colls = (snap?.collections || []).map((c, i) => ({
          hash: c.hash || `c-${i}`,
          current_hash: c.hash || `c-${i}`,
          friendly_name: c.name || c.hash || 'Unnamed Collection',
          entry_count: c.size ?? (Array.isArray(c.entries) ? c.entries.length : 0),
          entries: (c.entries || []).map(e => ({
            path: e.path || e.name || 'unnamed',
            hash: e.hash || '',
            mime_type: e.mime || '',
            providers: [{ type: 'sha256', value: e.hash || '', mime_type: e.mime || '' }],
          })),
          tags: c.tags || [],
          visibility: c.access_policy || 'public',
          version: 1,
          is_protected: Boolean(c.is_protected),
          created_at: '',
          isRemotePeer: true,
        }));
        setCollections(colls);
        return;
      } catch (err) {
        setErr('无法获取远程节点共享合集: ' + (err?.message || String(err)));
        setCollections([]);
        return;
      }
    }

    try {
      const res = await ws.admin('GET', '/anon/collections');
      setCollections(Array.isArray(res) ? res : []);
    } catch (e) {
      setErr(e?.message || String(e));
      setCollections([]);
    }
  }, [peerSession]);
  useEffect(() => { loadList(); }, [loadList]);

  const openCreate = async () => {
    setErr('');
    setTab('create');
    setSelected([]);
    setName('');
    setTagsInput('');
    setVisibility('public');
    setAccessPolicy('public');
    setPasscode('');
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
    if (accessPolicy === 'protected' && !passcode.trim()) {
      setErr('Please enter a passcode for protected access policy');
      return;
    }
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
        access_policy: accessPolicy,
        passcode: accessPolicy === 'protected' ? passcode.trim() : '',
      });
      setTab('list');
      await loadList();
    } catch (e) { setErr(e?.message || String(e)); }
    finally { setBusy(false); }
  };

  const openDetail = async (c) => {
    const targetHash = c.hash || c.current_hash;
    if (detail?.hash === targetHash) { setDetail(null); return; }
    setErr('');
    setUnlockPasscode('');
    setDetail({ hash: targetHash, data: null });

    if (Array.isArray(c.entries) && c.entries.length > 0) {
      setDetail({ hash: targetHash, data: c });
      return;
    }

    try {
      const d = await ws.admin('GET', `/anon/collections/${targetHash}`);
      setDetail({ hash: targetHash, data: d });
    } catch (e) {
      if (peerSession?.client) {
        try {
          const snap = await peerSession.client.shares();
          const match = (snap?.collections || []).find(col => col.hash === targetHash);
          if (match) {
            setDetail({
              hash: targetHash,
              data: {
                hash: match.hash,
                friendly_name: match.name,
                entries: (match.entries || []).map(entry => ({
                  path: entry.path || entry.name || 'unnamed',
                  hash: entry.hash || '',
                  mime_type: entry.mime || '',
                  providers: [{ type: 'sha256', value: entry.hash || '', mime_type: entry.mime || '' }],
                })),
                tags: match.tags || [],
                is_protected: Boolean(match.is_protected),
                visibility: match.access_policy || 'public',
              },
            });
            return;
          }
        } catch { /* ignore */ }
      }
      setErr(e?.message || String(e));
      setDetail(null);
    }
  };

  const unlockProtected = async (hash) => {
    if (!unlockPasscode.trim()) return;
    setBusy(true);
    try {
      const d = await ws.admin('GET', `/anon/collections/${hash}?passcode=${encodeURIComponent(unlockPasscode.trim())}`);
      setDetail({ hash, data: d });
    } catch (e) {
      setErr(e?.message || String(e));
    } finally {
      setBusy(false);
    }
  };

  const downloadEntry = async (hash, path) => {
    try {
      const fname = (path || 'download').split('/').pop();
      if (peerSession?.client) {
        await peerSession.client.saveAs(hash, fname);
        return;
      }
      const query = unlockPasscode ? `?passcode=${encodeURIComponent(unlockPasscode.trim())}` : '';
      await ws.downloadToFile(hash, path || 'download', query);
    }
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
    <div className="p-4 sm:p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-bold">Collections</h1>
              <div className="flex items-center bg-white/[0.06] p-0.5 rounded-lg text-xs shrink-0 border border-white/[0.08]">
                <button type="button" onClick={() => navigate('/drive')} className="px-2.5 py-1 rounded text-gray-400 hover:text-white transition-colors">Files</button>
                <span className="px-2.5 py-1 rounded bg-brand-600 text-white font-medium">Collections</span>
              </div>
            </div>
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

            <DataState
              loading={collections === null}
              error={err}
              empty={filteredCollections.length === 0}
              onRetry={loadList}
              skeletonType={viewMode === 'grid' ? 'cards' : 'table'}
              skeletonRows={6}
              emptyProps={{
                icon: '📦',
                title: collections?.length === 0 ? 'No collections yet' : 'No matching collections found',
                description: collections?.length === 0
                  ? 'Click "New Collection" to create one from your netdisk files.'
                  : 'Try clearing your search query or tag filter.',
                actionLabel: collections?.length === 0 ? '+ New Collection' : null,
                onAction: openCreate,
              }}
            >
              {viewMode === 'grid' ? (
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
              <div className="card-surface overflow-x-auto">
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
            )}
            </DataState>
          </div>
        )}

        {tab === 'create' && (
          <CreateCollectionForm
            name={name}
            setName={setName}
            tagsInput={tagsInput}
            setTagsInput={setTagsInput}
            visibility={visibility}
            setVisibility={setVisibility}
            accessPolicy={accessPolicy}
            setAccessPolicy={setAccessPolicy}
            passcode={passcode}
            setPasscode={setPasscode}
            files={files}
            selected={selected}
            toggleFile={toggleFile}
            create={create}
            busy={busy}
            onCancel={() => setTab('list')}
            visLabels={VIS_LABEL}
          />
        )}
      </div>
    </div>
  );
}
