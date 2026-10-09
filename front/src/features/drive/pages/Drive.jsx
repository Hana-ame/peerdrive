// Module ②: My Cloud Drive —— file management for this node (goes through WS admin frames to the backend)
// List / upload / download / delete / generate share links / categorize / tag files.
import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { fmtBytes } from '../../../platform/shared/format';

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  if (isNaN(d)) return ts;
  return d.toLocaleString('en-US', { hour12: false });
}

const CATEGORIES = [
  { id: 'all', label: 'All Files', icon: '📁' },
  { id: 'docs', label: 'Documents', icon: '📄', ext: ['pdf', 'doc', 'docx', 'txt', 'md', 'rtf', 'odt', 'csv', 'xls', 'xlsx', 'ppt', 'pptx'] },
  { id: 'images', label: 'Images', icon: '🖼️', ext: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico'] },
  { id: 'videos', label: 'Videos', icon: '🎬', ext: ['mp4', 'mkv', 'avi', 'mov', 'webm', 'flv', 'wmv'] },
  { id: 'audio', label: 'Audio', icon: '🎵', ext: ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'wma'] },
  { id: 'archives', label: 'Archives', icon: '📦', ext: ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'] },
  { id: 'code', label: 'Code', icon: '💻', ext: ['js', 'ts', 'jsx', 'tsx', 'go', 'py', 'rs', 'c', 'cpp', 'h', 'java', 'html', 'css', 'json', 'sh'] },
];

function getFileExt(filename) {
  const parts = String(filename || '').split('.');
  return parts.length > 1 ? parts.pop().toLowerCase() : '';
}

function getFileCategory(f) {
  if (f.isDir) return 'folder';
  const ext = getFileExt(f.filename);
  for (const cat of CATEGORIES) {
    if (cat.ext && cat.ext.includes(ext)) return cat.id;
  }
  const mime = String(f.mime_type || '').toLowerCase();
  if (mime.startsWith('image/')) return 'images';
  if (mime.startsWith('video/')) return 'videos';
  if (mime.startsWith('audio/')) return 'audio';
  if (mime.startsWith('text/')) return 'docs';
  return 'other';
}

function getFileIcon(f) {
  if (f.isDir) return '📁';
  const cat = getFileCategory(f);
  const found = CATEGORIES.find(c => c.id === cat);
  return found ? found.icon : '📄';
}

export default function Drive() {
  const { hash: deepHash } = useParams();
  const [currentDir, setCurrentDir] = useState('');
  const [files, setFiles] = useState(null); // null = loading
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [shareLink, setShareLink] = useState('');
  const [copiedHash, setCopiedHash] = useState('');
  const fileRef = useRef(null);

  // Netdisk Features: Search, Category, ViewMode, Tags
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedCategory, setSelectedCategory] = useState('all');
  const [selectedTag, setSelectedTag] = useState('');
  const [viewMode, setViewMode] = useState('list'); // list | grid
  const [fileTags, setFileTags] = useState({}); // hash -> string[]
  const [editingTagFile, setEditingTagFile] = useState(null); // file object
  const [tagInputText, setTagInputText] = useState('');

  const load = useCallback(async () => {
    setErr('');
    try {
      let res;
      try {
        const query = currentDir ? `?path=${encodeURIComponent(currentDir)}` : '';
        res = await ws.admin('GET', `/files/browse${query}`);
        if (Array.isArray(res)) {
          res = res.map(e => ({
            hash: e.hash || '',
            filename: e.name || '',
            size: e.size || 0,
            mime_type: e.is_dir ? 'folder' : (e.mime || ''),
            created_at: e.mod_time || '',
            isDir: Boolean(e.is_dir),
            path: e.path || '',
          }));
        }
      } catch {
        res = await ws.admin('GET', '/files');
      }
      const fileList = Array.isArray(res) ? res : [];
      setFiles(fileList);

      // Fetch tags for files that have hashes
      const hashes = fileList.map(f => f.hash).filter(Boolean);
      const tagsMap = {};
      await Promise.all(
        hashes.map(async (h) => {
          try {
            const data = await ws.admin('GET', `/tags/sha/${h}`);
            if (Array.isArray(data?.tags)) {
              tagsMap[h] = data.tags;
            }
          } catch {
            // tag endpoint might not have entry yet
          }
        })
      );
      setFileTags(prev => ({ ...prev, ...tagsMap }));
    } catch (e) {
      setErr(e?.message || String(e));
      setFiles([]);
    }
  }, [currentDir]);

  useEffect(() => { load(); }, [load]);

  const onPick = async (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    setBusy(true);
    try {
      await ws.upload(f, f.name, 'file', '/files/upload');
      await load();
    } catch (ex) {
      setErr(ex?.message || String(ex));
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  };

  const onDownload = async (f) => {
    try { await ws.downloadToFile(f.hash, f.filename); }
    catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onDelete = async (f) => {
    if (!confirm(`Delete "${f.filename}"?`)) return;
    try {
      await ws.admin('DELETE', `/files/${f.hash}`);
      await load();
    } catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onShare = async (f) => {
    try {
      const r = await ws.admin('POST', '/shares', { hash: f.hash, type: 'file', filename: f.filename });
      const token = r?.token;
      if (token) {
        const base = localStorage.getItem('peerdrive_api_base') || 'https://wsl-3000.moonchan.xyz';
        setShareLink(`${base}/s/${token}`);
      } else {
        setShareLink('');
      }
    } catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onCopyDeepLink = (f) => {
    const origin = window.location.origin;
    const base = import.meta.env.BASE_URL.replace(/\/$/, '');
    const url = `${origin}${base}/drive/${f.hash}`;
    navigator.clipboard?.writeText(url);
    setCopiedHash(f.hash);
    setTimeout(() => setCopiedHash(''), 2000);
  };

  // Tag Management
  const openTagEditor = (f) => {
    setEditingTagFile(f);
    const existing = fileTags[f.hash] || [];
    setTagInputText(existing.join(', '));
  };

  const saveTags = async () => {
    if (!editingTagFile) return;
    const tags = tagInputText
      .split(',')
      .map(t => t.trim())
      .filter(Boolean);
    try {
      await ws.admin('POST', `/tags/sha/${editingTagFile.hash}`, { tags });
      setFileTags(prev => ({ ...prev, [editingTagFile.hash]: tags }));
      setEditingTagFile(null);
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  // Distinct tags and counts
  const allTagsWithCounts = useMemo(() => {
    const map = {};
    Object.values(fileTags).forEach(tagList => {
      if (Array.isArray(tagList)) {
        tagList.forEach(t => {
          if (t) map[t] = (map[t] || 0) + 1;
        });
      }
    });
    return Object.entries(map).map(([tag, count]) => ({ tag, count }));
  }, [fileTags]);

  // Filtering files
  const filteredFiles = useMemo(() => {
    if (!files) return [];
    return files.filter(f => {
      // 1. Directory search / match
      const matchQuery = !searchQuery.trim() ||
        f.filename.toLowerCase().includes(searchQuery.toLowerCase()) ||
        (f.hash && f.hash.toLowerCase().includes(searchQuery.toLowerCase())) ||
        ((fileTags[f.hash] || []).some(t => t.toLowerCase().includes(searchQuery.toLowerCase())));

      // 2. Category match
      let matchCategory = true;
      if (selectedCategory !== 'all') {
        matchCategory = getFileCategory(f) === selectedCategory;
      }

      // 3. Tag match
      let matchTag = true;
      if (selectedTag) {
        matchTag = (fileTags[f.hash] || []).includes(selectedTag);
      }

      return matchQuery && matchCategory && matchTag;
    });
  }, [files, searchQuery, selectedCategory, selectedTag, fileTags]);

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';
  const td = 'px-3 py-2';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        {/* Header toolbar */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5">
          <div>
            <h1 className="text-2xl font-bold flex items-center gap-2">
              <span>☁️ My Cloud Drive</span>
            </h1>
            <p className="text-sm text-gray-500 mt-0.5">Files registered on this node (content-addressed storage)</p>
          </div>
          <div className="flex items-center gap-2">
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
            <button
              onClick={() => fileRef.current?.click()}
              disabled={busy}
              className="btn-brand shrink-0"
            >
              {busy ? 'Uploading...' : '+ Upload File'}
            </button>
            <input ref={fileRef} type="file" className="hidden" onChange={onPick} />
          </div>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">
            {err}
          </div>
        )}
        {shareLink && (
          <div className="mb-4 text-xs text-gray-300 bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 break-all">
            Share link: <span className="font-mono text-brand-300">{shareLink}</span>
            <button onClick={() => { navigator.clipboard?.writeText(shareLink); }} className="ml-2 text-gray-500 hover:text-white">Copy</button>
          </div>
        )}

        {/* Category Toolbar (All / Documents / Images / Videos / Audio / Archives / Code) */}
        <div className="flex items-center gap-1.5 overflow-x-auto pb-2 mb-3 text-xs border-b border-white/[0.06]">
          {CATEGORIES.map(cat => (
            <button
              key={cat.id}
              onClick={() => setSelectedCategory(cat.id)}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg whitespace-nowrap transition-colors ${
                selectedCategory === cat.id
                  ? 'bg-brand-600/30 text-brand-300 border border-brand-500/40 font-medium'
                  : 'text-gray-400 hover:text-white hover:bg-white/[0.04]'
              }`}
            >
              <span>{cat.icon}</span>
              <span>{cat.label}</span>
            </button>
          ))}
        </div>

        {/* Search & Tag Filter Bar */}
        <div className="mb-4 space-y-2">
          <div className="relative">
            <span className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500 text-xs">
              🔍
            </span>
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Search files by name, hash, or tags..."
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
                All
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

        {/* Content list or grid */}
        {files === null ? (
          <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>
        ) : filteredFiles.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            {files.length === 0 ? (
              <>
                <p className="mb-2">No files yet</p>
                <p className="text-xs text-gray-600">Click "Upload File" in the top right or register local paths on the backend.</p>
              </>
            ) : (
              <>
                <p className="mb-2">No matching files found</p>
                <p className="text-xs text-gray-600">Try adjusting your search query, category, or tag filter.</p>
              </>
            )}
          </div>
        ) : viewMode === 'grid' ? (
          /* Grid View Mode */
          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-4">
            {filteredFiles.map((f, i) => {
              const isTarget = deepHash && f.hash === deepHash;
              const tags = fileTags[f.hash] || [];
              const icon = getFileIcon(f);

              return (
                <div
                  key={f.hash || i}
                  className={`card-surface p-4 rounded-xl border transition-all flex flex-col justify-between group ${
                    isTarget
                      ? 'border-brand-500 bg-brand-500/10'
                      : 'border-white/[0.06] hover:border-brand-500/40'
                  }`}
                >
                  <div>
                    <div className="flex items-center justify-between text-2xl mb-2">
                      <span>{icon}</span>
                      <button
                        onClick={() => openTagEditor(f)}
                        className="opacity-0 group-hover:opacity-100 text-[10px] px-1.5 py-0.5 rounded bg-white/[0.08] hover:bg-white/[0.15] text-gray-300 transition-opacity"
                        title="Manage Tags"
                      >
                        🏷️
                      </button>
                    </div>
                    <h3 className="font-medium text-xs text-gray-200 truncate" title={f.filename}>
                      {f.filename}
                    </h3>
                    <p className="text-[11px] text-gray-500 mt-1">
                      {fmtBytes(f.size)}
                    </p>
                    {tags.length > 0 && (
                      <div className="flex gap-1 flex-wrap mt-2">
                        {tags.map((t, ti) => (
                          <span key={ti} className="text-[10px] px-1.5 py-0.2 rounded bg-brand-500/10 text-brand-300">
                            #{t}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>

                  <div className="mt-3 pt-2 border-t border-white/[0.04] flex items-center justify-between">
                    <button onClick={() => onDownload(f)} className="text-[11px] text-brand-400 hover:text-brand-300">Download</button>
                    <div className="flex items-center gap-1">
                      <button onClick={() => onCopyDeepLink(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300">
                        {copiedHash === f.hash ? 'Copied' : 'Link'}
                      </button>
                      <button onClick={() => onShare(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300">Share</button>
                      <button onClick={() => onDelete(f)} className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.05] hover:bg-red-500/20 text-red-400">✕</button>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          /* List View Mode */
          <div className="card-surface overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-white/[0.03]">
                <tr>
                  <th className={th}>Name</th>
                  <th className={th}>Tags</th>
                  <th className={th}>Size</th>
                  <th className={th}>Type</th>
                  <th className={th}>Registered</th>
                  <th className={th + ' text-right'}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredFiles.map((f, i) => {
                  const isTarget = deepHash && f.hash === deepHash;
                  const tags = fileTags[f.hash] || [];
                  const icon = getFileIcon(f);

                  return (
                    <tr
                      key={f.hash || i}
                      className={`border-t border-white/[0.04] transition-colors ${
                        isTarget
                          ? 'bg-brand-500/15 border-l-2 border-l-brand-400 hover:bg-brand-500/20'
                          : 'hover:bg-white/[0.02]'
                      }`}
                    >
                      <td className={td + ' text-gray-200 max-w-[240px] truncate'}>
                        <div className="flex items-center gap-2">
                          <span className="text-base">{icon}</span>
                          {isTarget && (
                            <span className="inline-block w-1.5 h-1.5 rounded-full bg-brand-400 shrink-0" title="Deep linked" />
                          )}
                          <span className="truncate">{f.filename}</span>
                        </div>
                      </td>
                      <td className={td + ' text-xs text-gray-400 max-w-[150px]'}>
                        <div className="flex items-center gap-1 flex-wrap">
                          {tags.length > 0 ? (
                            tags.map((t, ti) => (
                              <span key={ti} className="text-[10px] px-1.5 py-0.5 rounded bg-brand-500/10 text-brand-300">
                                #{t}
                              </span>
                            ))
                          ) : (
                            <span className="text-gray-600">—</span>
                          )}
                          <button
                            onClick={() => openTagEditor(f)}
                            className="text-[10px] text-gray-500 hover:text-white px-1 ml-0.5"
                            title="Edit tags"
                          >
                            +
                          </button>
                        </div>
                      </td>
                      <td className={td + ' text-gray-500 whitespace-nowrap'}>{fmtBytes(f.size)}</td>
                      <td className={td + ' text-gray-500 whitespace-nowrap'}>{f.mime_type || '—'}</td>
                      <td className={td + ' text-gray-500 whitespace-nowrap'}>{fmtTime(f.created_at)}</td>
                      <td className={td + ' text-right whitespace-nowrap'}>
                        <button onClick={() => onDownload(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">Download</button>
                        <button onClick={() => onCopyDeepLink(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">
                          {copiedHash === f.hash ? 'Copied' : 'Link'}
                        </button>
                        <button onClick={() => onShare(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 mr-1">Share</button>
                        <button onClick={() => onDelete(f)} className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-red-500/20 text-gray-300 hover:text-red-300">Delete</button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {/* Tag Editor Modal */}
        {editingTagFile && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" onClick={() => setEditingTagFile(null)}>
            <div className="card-surface max-w-md w-full p-5 rounded-xl border border-white/10" onClick={e => e.stopPropagation()}>
              <h3 className="text-base font-semibold text-gray-200 mb-2">Edit File Tags</h3>
              <p className="text-xs text-gray-400 mb-3 truncate">
                File: <span className="text-gray-200">{editingTagFile.filename}</span>
              </p>
              <div className="space-y-3">
                <div>
                  <label className="block text-xs text-gray-400 mb-1">Tags (comma-separated)</label>
                  <input
                    type="text"
                    value={tagInputText}
                    onChange={(e) => setTagInputText(e.target.value)}
                    placeholder="e.g. project, invoice, favorite"
                    className="input-base w-full text-xs"
                    autoFocus
                  />
                  <p className="text-[11px] text-gray-500 mt-1">Tags are associated with this file's content hash (SHA256).</p>
                </div>
                <div className="flex justify-end gap-2 pt-2">
                  <button onClick={() => setEditingTagFile(null)} className="btn-ghost text-xs">Cancel</button>
                  <button onClick={saveTags} className="btn-brand text-xs">Save Tags</button>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
