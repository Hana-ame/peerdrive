import { useState, useEffect, useCallback } from 'react';
import * as ws from '../../../platform/transport-ws';
import { mimeOf } from '../../../platform/shared/mime';
import { STORAGE_KEY_API_BASE } from '../../../platform/shared/storageKeys';

/**
 * useDriveFiles hook manages Drive file querying (root, browsing, inbox, WebRTC shares),
 * uploading, downloading, deleting, inbox quarantine approvals, share link generation,
 * and tag metadata (Issue #334).
 */
export function useDriveFiles({ peerSession, selectedCategory = 'all', fileRef } = {}) {
  const [currentDir, setCurrentDir] = useState('');
  const [files, setFiles] = useState(null); // null = loading
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [shareLink, setShareLink] = useState('');
  const [copiedHash, setCopiedHash] = useState('');
  const [fileTags, setFileTags] = useState({}); // hash -> string[]
  const [globalTagCounts, setGlobalTagCounts] = useState([]);

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
      if (!peerSession?.client) {
        setErr('');
      } else {
        setErr(e?.message || String(e));
      }
      setFiles([]);
    }
  }, [currentDir, peerSession, selectedCategory]);

  useEffect(() => {
    load();
  }, [load]);

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
      if (fileRef?.current) fileRef.current.value = '';
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
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  const onApproveInbox = async (f) => {
    try {
      await ws.admin('POST', '/files/inbox/approve', { hash: f.hash });
      await load();
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  const onRejectInbox = async (f) => {
    if (!confirm(`Reject and delete quarantined file "${f.filename}"?`)) return;
    try {
      await ws.admin('DELETE', `/files/inbox/${f.hash}`);
      await load();
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
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
    } catch (ex) {
      setErr(ex?.message || String(ex));
    }
  };

  const onCopyDeepLink = (f) => {
    const origin = window.location.origin;
    const base = import.meta.env.BASE_URL.replace(/\/$/, '');
    const url = `${origin}${base}#/drive/${f.hash}`;
    navigator.clipboard?.writeText(url);
    setCopiedHash(f.hash);
    setTimeout(() => setCopiedHash(''), 2000);
  };

  const saveFileTags = async (hash, tags) => {
    await ws.admin('POST', `/tags/sha/${hash}`, { tags });
    setFileTags(prev => ({ ...prev, [hash]: tags }));
    await loadTagsSummary();
  };

  return {
    currentDir,
    setCurrentDir,
    files,
    setFiles,
    err,
    setErr,
    busy,
    setBusy,
    shareLink,
    setShareLink,
    copiedHash,
    fileTags,
    setFileTags,
    globalTagCounts,
    loadTagsSummary,
    load,
    onPick,
    onDownload,
    onDelete,
    onApproveInbox,
    onRejectInbox,
    onShare,
    onCopyDeepLink,
    saveFileTags,
  };
}
