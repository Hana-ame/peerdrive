// Module ②: My Cloud Drive —— file management for this node (goes through WS admin frames to the backend)
// List / upload / download / delete / generate share links / categorize / tag files.
import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession, onNodeSession } from '../../../lib/nodeSession';
import { fmtBytes } from '../../../platform/shared/format';
import { kindOf, mimeOf } from '../../../platform/shared/mime';
import { STORAGE_KEY_API_BASE } from '../../../platform/shared/storageKeys';
import FilePreviewModal from '../../../components/netdisk/FilePreviewModal';
import DataState from '../../../components/netdisk/DataState';
import DriveGridView from '../components/DriveGridView';
import DriveListView from '../components/DriveListView';
import CastModal from '../components/CastModal';
import ContextMenu from '../../../components/netdisk/ContextMenu';

const CATEGORIES = [
  { id: 'all', label: 'All Files', icon: '📁' },
  { id: 'inbox', label: 'Inbox (待审)', icon: '📥' },
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
  const navigate = useNavigate();
  const [currentDir, setCurrentDir] = useState('');
  const [files, setFiles] = useState(null); // null = loading
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [shareLink, setShareLink] = useState('');
  const [copiedHash, setCopiedHash] = useState('');
  const [peerSession, setPeerSession] = useState(() => getNodeSession());
  const fileRef = useRef(null);

  useEffect(() => {
    return onNodeSession((s) => {
      setPeerSession(s);
    });
  }, []);

  // Netdisk Features: Search, Category, ViewMode, Tags, Sorting
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedCategory, setSelectedCategory] = useState('all');
  const [selectedTag, setSelectedTag] = useState('');
  const [viewMode, setViewMode] = useState('list'); // list | grid
  const [sortBy, setSortBy] = useState('time'); // 'time' | 'name' | 'size'
  const [sortOrder, setSortOrder] = useState('desc'); // 'asc' | 'desc'
  const [fileTags, setFileTags] = useState({}); // hash -> string[]
  const [globalTagCounts, setGlobalTagCounts] = useState([]);
  const [editingTagFile, setEditingTagFile] = useState(null); // file object
  const [tagInputText, setTagInputText] = useState('');
  const [previewFile, setPreviewFile] = useState(null); // file object to preview
  const [castFile, setCastFile] = useState(null);
  const [screens, setScreens] = useState([]);
  const [targetScreenId, setTargetScreenId] = useState('');
  const [targetChannel, setTargetChannel] = useState('default');
  const [castStatus, setCastStatus] = useState('');
  const [contextMenu, setContextMenu] = useState(null); // { x, y, file } | null

  const loadTagsSummary = useCallback(async () => {
    if (peerSession?.client) return; // PeerJS mode doesn't have local tags admin endpoint
    try {
      const res = await ws.admin('GET', '/tags/summary');
      const list = res?.data || res?.tags || [];
      if (Array.isArray(list)) {
        setGlobalTagCounts(list);
      }
    } catch {
      // offline / not available
    }
  }, [peerSession]);

  useEffect(() => {
    loadTagsSummary();
  }, [loadTagsSummary]);

  const handleContextMenu = (e, file) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({
      x: e.clientX,
      y: e.clientY,
      file,
    });
  };

  const handleContainerContextMenu = (e) => {
    // Only trigger if right-clicking outside of file cards/rows
    if (e.target.closest('[data-context-target]')) return;
    e.preventDefault();
    setContextMenu({
      x: e.clientX,
      y: e.clientY,
      file: null, // blank canvas context
    });
  };

  const load = useCallback(async () => {
    setErr('');
    // If WebRTC peerSession is active, strictly load files from remote peer via shares
    if (peerSession?.client) {
      try {
        const shareData = await peerSession.client.shares();
        const rawFiles = shareData?.files || [];
        const fileList = rawFiles.map(e => {
          const fname = e.name || e.path || 'unnamed';
          return {
            hash: e.hash || '',
            filename: fname,
            size: e.size || 0,
            mime_type: mimeOf(fname, e.mime),
            created_at: '',
            isDir: false,
            path: e.path || e.name || '',
            isRemotePeer: true,
          };
        });
        setFiles(fileList);
        return;
      } catch (e) {
        setErr('无法获取远程节点共享文件: ' + (e?.message || String(e)));
        setFiles([]);
        return;
      }
    }

    try {
      let fileList = [];
      if (selectedCategory === 'inbox') {
        const res = await ws.admin('GET', '/files/inbox');
        const raw = Array.isArray(res) ? res : [];
        fileList = raw.map(e => ({
          hash: e.hash || '',
          filename: e.filename || 'unnamed',
          size: e.size || 0,
          mime_type: mimeOf(e.filename, e.mime_type),
          created_at: e.created_at || '',
          isDir: false,
          path: e.path || '',
          isInbox: true,
          uploader: e.uploader_peer_id || '',
        }));
      } else if (!currentDir) {
        // Root directory: fetch registered cloud drive files (full hashes, mime types, sizes)
        const res = await ws.admin('GET', '/files');
        const raw = Array.isArray(res) ? res : [];
        fileList = raw.map(e => ({
          hash: e.hash || '',
          filename: e.filename || e.name || 'unnamed',
          size: e.size || 0,
          mime_type: mimeOf(e.filename || e.name, e.mime_type || e.mime),
          created_at: e.created_at || e.mod_time || '',
          isDir: false,
          path: e.provider_path || e.path || '',
        }));
      } else {
        // Subdirectory browsing: query /files/browse?path=...
        const query = `?path=${encodeURIComponent(currentDir)}`;
        const res = await ws.admin('GET', `/files/browse${query}`);
        const raw = Array.isArray(res) ? res : [];
        fileList = raw.map(e => ({
          hash: e.hash || '',
          filename: e.name || e.filename || 'unnamed',
          size: e.size || 0,
          mime_type: e.is_dir ? 'folder' : mimeOf(e.name || e.filename, e.mime),
          created_at: e.mod_time || '',
          isDir: Boolean(e.is_dir),
          path: e.path || '',
        }));
      }
      setFiles(fileList);

      // Fetch tags for files in a single batch request (eliminates N+1 network waterfall)
      const hashes = fileList.map(f => f.hash).filter(Boolean);
      if (hashes.length > 0) {
        try {
          const res = await ws.admin('POST', '/tags/batch', { shas: hashes });
          const batchTags = res?.tags || res?.data || {};
          setFileTags(prev => ({ ...prev, ...batchTags }));
        } catch {
          // tag endpoint might not be available
        }
      }
    } catch (e) {
      // If peerSession is also not present, display connection guide
      if (!peerSession?.client) {
        setErr('');
      } else {
        setErr(e?.message || String(e));
      }
      setFiles([]);
    }
  }, [currentDir, peerSession, selectedCategory]);

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
    try {
      if (peerSession?.client) {
        console.log(`[Drive Download] Starting download for ${f.filename} (${f.hash})`);
        const t0 = performance.now();
        await peerSession.client.saveAs(f.hash, f.filename, {
          onProgress: ({ received, total }) => {
            const sec = (performance.now() - t0) / 1000;
            const pct = total > 0 ? ((received / total) * 100).toFixed(1) : '?';
            const mbps = sec > 0 ? (received / sec / (1024 * 1024)).toFixed(2) : '0';
            console.log(`[Drive Download] ${f.filename}: ${received}/${total} (${pct}%) at ${mbps} MB/s`);
          }
        });
        const totalSec = ((performance.now() - t0) / 1000).toFixed(2);
        console.log(`[Drive Download] Finished ${f.filename} in ${totalSec}s`);
      } else {
        await ws.downloadToFile(f.hash, f.filename);
      }
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  const onDelete = async (f) => {
    if (f.isRemotePeer) {
      alert('Cannot delete files on a remote peer.');
      return;
    }
    if (!confirm(`Delete "${f.filename}"?`)) return;
    try {
      await ws.admin('DELETE', `/files/${f.hash}`);
      await load();
    } catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onApproveInbox = async (f) => {
    try {
      await ws.admin('POST', '/files/inbox/approve', { hash: f.hash });
      await load();
    } catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onRejectInbox = async (f) => {
    if (!confirm(`Reject and delete quarantined file "${f.filename}"?`)) return;
    try {
      await ws.admin('DELETE', `/files/inbox/${f.hash}`);
      await load();
    } catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onShare = async (f) => {
    try {
      const r = await ws.admin('POST', '/shares', { hash: f.hash, type: 'file', filename: f.filename });
      const token = r?.token;
      if (token) {
        const base = localStorage.getItem(STORAGE_KEY_API_BASE) || 'https://wsl-3000.moonchan.xyz';
        setShareLink(`${base}/s/${token}`);
      } else {
        setShareLink('');
      }
    } catch (ex) { setErr(ex?.message || String(ex)); }
  };

  const onCopyDeepLink = (f) => {
    const origin = window.location.origin;
    const base = import.meta.env.BASE_URL.replace(/\/$/, '');
    // Hash router: append #/drive/:hash (no server SPA fallback needed)
    const url = `${origin}${base}#/drive/${f.hash}`;
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
      loadTagsSummary();
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  // Remote Display Cast Management (Issue #243)
  const openCastModal = async (f) => {
    setCastFile(f);
    setCastStatus('');
    try {
      const res = await ws.admin('GET', '/display/screens');
      const list = Array.isArray(res) ? res : [];
      setScreens(list);
      if (list.length > 0) {
        setTargetScreenId(list[0].id);
        setTargetChannel(list[0].channel || 'default');
      } else {
        setTargetScreenId('');
        setTargetChannel('default');
      }
    } catch {
      setScreens([]);
    }
  };

  const handleCast = async () => {
    if (!castFile) return;
    setBusy(true);
    setCastStatus('Casting to screen...');
    try {
      const ext = (castFile.filename || '').split('.').pop().toLowerCase();
      let mediaType = 'image';
      if (['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(ext)) mediaType = 'video';
      else if (['mp3', 'wav', 'flac', 'ogg', 'aac'].includes(ext)) mediaType = 'audio';

      await ws.admin('POST', '/display/cast', {
        targetSessionId: targetScreenId,
        channel: targetChannel,
        mediaType,
        hash: castFile.hash,
        title: castFile.filename,
        autoplay: true,
      });
      setCastStatus('Cast successful!');
    } catch (ex) {
      setCastStatus('Cast failed: ' + (ex?.message || String(ex)));
    } finally {
      setBusy(false);
    }
  };

  const handleCastControl = async (action) => {
    try {
      if (action === 'clear') {
        await ws.admin('POST', '/display/clear', {
          targetSessionId: targetScreenId,
          channel: targetChannel,
        });
        setCastStatus('Screen cleared');
      } else {
        await ws.admin('POST', '/display/control', {
          targetSessionId: targetScreenId,
          channel: targetChannel,
          controlAction: action,
        });
        setCastStatus(`Control ${action} sent`);
      }
    } catch (ex) {
      setCastStatus('Control failed: ' + (ex?.message || String(ex)));
    }
  };

  // Distinct tags and counts (prefers global aggregate counts)
  const allTagsWithCounts = useMemo(() => {
    if (globalTagCounts && globalTagCounts.length > 0) {
      return globalTagCounts;
    }
    const map = {};
    Object.values(fileTags).forEach(tagList => {
      if (Array.isArray(tagList)) {
        tagList.forEach(t => {
          if (t) map[t] = (map[t] || 0) + 1;
        });
      }
    });
    return Object.entries(map).map(([tag, count]) => ({ tag, count }));
  }, [globalTagCounts, fileTags]);

  // Filtering & sorting files
  const filteredFiles = useMemo(() => {
    if (!files) return [];
    const list = files.filter(f => {
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

    return list.sort((a, b) => {
      let cmp = 0;
      if (sortBy === 'name') {
        cmp = (a.filename || '').localeCompare(b.filename || '');
      } else if (sortBy === 'size') {
        cmp = (a.size || 0) - (b.size || 0);
      } else {
        cmp = String(a.created_at || '').localeCompare(String(b.created_at || ''));
      }
      return sortOrder === 'asc' ? cmp : -cmp;
    });
  }, [files, searchQuery, selectedCategory, selectedTag, fileTags, sortBy, sortOrder]);

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';
  const td = 'px-3 py-2';

  return (
    <div className="p-4 sm:p-8 overflow-y-auto h-full" onContextMenu={handleContainerContextMenu}>
      <div className="max-w-5xl mx-auto">
        {/* Header toolbar */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5">
          <div className="min-w-0">
            <h1 className="text-xl sm:text-2xl font-bold flex items-center gap-2 truncate">
              <span>☁️ My Cloud Drive</span>
            </h1>
            <p className="text-xs sm:text-sm text-gray-500 mt-0.5 truncate">Files registered on this node (content-addressed storage)</p>
          </div>
          <div className="flex flex-wrap items-center gap-2 max-w-full">
            {/* Sorting controls */}
            <div className="flex items-center bg-white/[0.04] p-0.5 rounded-lg border border-white/[0.06] text-xs shrink-0">
              <span className="text-gray-500 pl-2 pr-1 text-[11px]">Sort:</span>
              <select
                value={sortBy}
                onChange={(e) => setSortBy(e.target.value)}
                className="bg-transparent text-gray-300 text-xs py-1 pr-1 outline-none cursor-pointer"
                title="Sort field"
              >
                <option value="time" className="bg-gray-900 text-white">Date</option>
                <option value="name" className="bg-gray-900 text-white">Name</option>
                <option value="size" className="bg-gray-900 text-white">Size</option>
              </select>
              <button
                onClick={() => setSortOrder(o => o === 'asc' ? 'desc' : 'asc')}
                className="px-2 py-1 text-gray-400 hover:text-white transition-colors text-xs font-mono"
                title={sortOrder === 'asc' ? 'Ascending (click for Descending)' : 'Descending (click for Ascending)'}
              >
                {sortOrder === 'asc' ? '▲' : '▼'}
              </button>
            </div>
            {/* View mode toggle */}
            <div className="flex items-center bg-white/[0.04] p-0.5 rounded-lg border border-white/[0.06] text-xs shrink-0">
              <button
                onClick={() => setViewMode('list')}
                className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'list' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
                title="Detailed Information List"
              >
                ☰ Details
              </button>
              <button
                onClick={() => setViewMode('grid')}
                className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'grid' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
                title="Thumbnail Cards View"
              >
                ▦ Thumbnails
              </button>
            </div>
            <button
              onClick={() => fileRef.current?.click()}
              disabled={busy}
              className="btn-brand shrink-0 text-xs sm:text-sm py-1.5 px-3"
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
        <DataState
          loading={files === null}
          error={err}
          empty={filteredFiles.length === 0}
          onRetry={load}
          skeletonType={viewMode === 'grid' ? 'cards' : 'table'}
          skeletonRows={8}
          emptyProps={{
            icon: '☁️',
            title: !peerSession?.client && files?.length === 0
              ? 'No connected node'
              : files?.length === 0 ? 'No files available' : 'No matching files found',
            description: !peerSession?.client && files?.length === 0
              ? 'Connect to an online node in Plaza to access files, or connect a local backend.'
              : files?.length === 0
                ? (peerSession?.client ? 'This remote node has not shared any public files yet.' : 'Click "Upload File" in the top right or register local paths on the backend.')
                : 'Try adjusting your search query, category, or tag filter.',
            actionLabel: !peerSession?.client && files?.length === 0
              ? 'Find & Connect Node'
              : (files?.length === 0 && !peerSession?.client ? '+ Upload File' : null),
            onAction: !peerSession?.client && files?.length === 0
              ? () => navigate('/')
              : () => fileRef.current?.click(),
          }}
        >
          {viewMode === 'grid' ? (
            <DriveGridView
              files={filteredFiles}
              deepHash={deepHash}
              fileTags={fileTags}
              getFileIcon={getFileIcon}
              openTagEditor={openTagEditor}
              setPreviewFile={setPreviewFile}
              onDownload={onDownload}
              openCastModal={openCastModal}
              onCopyDeepLink={onCopyDeepLink}
              copiedHash={copiedHash}
              onShare={onShare}
              onDelete={onDelete}
              onContextMenu={handleContextMenu}
            />
          ) : (
            <DriveListView
              files={filteredFiles}
              deepHash={deepHash}
              fileTags={fileTags}
              getFileIcon={getFileIcon}
              openTagEditor={openTagEditor}
              setPreviewFile={setPreviewFile}
              onDownload={onDownload}
              openCastModal={openCastModal}
              onCopyDeepLink={onCopyDeepLink}
              copiedHash={copiedHash}
              onShare={onShare}
              onDelete={onDelete}
              onContextMenu={handleContextMenu}
              onApproveInbox={onApproveInbox}
              onRejectInbox={onRejectInbox}
            />
          )}
        </DataState>

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

        {/* In-line Preview Modal */}
        {previewFile && (
          <FilePreviewModal
            file={previewFile}
            fetchBlob={async (hash) => {
              const effectiveType = mimeOf(previewFile.filename, previewFile.mime_type) || 'application/octet-stream';
              if (peerSession?.client) {
                const chunks = [];
                for await (const chunk of peerSession.client.stream(hash)) {
                  chunks.push(chunk);
                }
                return new Blob(chunks, { type: effectiveType });
              }
              const u8 = await ws.download(hash);
              return new Blob([u8], { type: effectiveType });
            }}
            onClose={() => setPreviewFile(null)}
            onDownload={(f) => onDownload(f)}
          />
        )}

        {/* Remote Screen Cast Modal (Issue #243) */}
        <CastModal
          castFile={castFile}
          screens={screens}
          targetScreenId={targetScreenId}
          setTargetScreenId={setTargetScreenId}
          targetChannel={targetChannel}
          setTargetChannel={setTargetChannel}
          castStatus={castStatus}
          busy={busy}
          handleCast={handleCast}
          handleCastControl={handleCastControl}
          onClose={() => setCastFile(null)}
        />

        {/* Custom Context Menu (Issue #252) */}
        {contextMenu && (
          <ContextMenu
            x={contextMenu.x}
            y={contextMenu.y}
            onClose={() => setContextMenu(null)}
            items={
              contextMenu.file
                ? [
                    { label: '预览文件 (Preview)', icon: '👁️', onClick: () => setPreviewFile(contextMenu.file) },
                    { label: '下载 (Download)', icon: '⬇️', onClick: () => onDownload(contextMenu.file) },
                    { label: '投屏到大屏 (Cast)', icon: '📺', onClick: () => openCastModal(contextMenu.file) },
                    { divider: true },
                    { label: '复制文件链接 (Copy Link)', icon: '🔗', onClick: () => onCopyDeepLink(contextMenu.file) },
                    { label: '对外共享 (Share)', icon: '📤', onClick: () => onShare(contextMenu.file) },
                    { label: '编辑标签 (Tags)', icon: '🏷️', onClick: () => openTagEditor(contextMenu.file) },
                    { divider: true },
                    { label: '删除 (Delete)', icon: '🗑️', danger: true, onClick: () => onDelete(contextMenu.file) },
                  ]
                : [
                    { label: '上传文件 (Upload)', icon: '⬆️', onClick: () => fileRef.current?.click() },
                    { label: '刷新列表 (Refresh)', icon: '🔄', onClick: load },
                  ]
            }
          />
        )}
      </div>
    </div>
  );
}

