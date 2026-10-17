import { useState, useEffect, useCallback } from 'react';
import * as ws from '../../../platform/transport-ws';
import { mimeOf } from '../../../platform/shared/mime';
import { STORAGE_KEY_API_BASE } from '../../../platform/shared/storageKeys';
import { buildFileUrl } from '../../../platform/shared';
import { getNodeSession } from '../../../lib/nodeSession';

/**
 * useDriveFiles hook manages Drive file querying (root, browsing, inbox, WebRTC shares),
 * uploading, downloading (via unified PeerJS/WS/HTTP queue with live progress bar),
 * deleting, inbox quarantine approvals, share link generation, and canonical SHA256 URLs.
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
    if (peerSession?.client || getNodeSession?.()?.client) return;
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
    const client = peerSession?.client || getNodeSession?.()?.client;
    if (client) {
      try {
        const shareData = await client.shares();
        const rawFiles = shareData?.files || [];
        const fileList = rawFiles.map((e) => {
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
        fileList = raw.map((e) => ({
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
        const res = await ws.admin('GET', '/files');
        const raw = Array.isArray(res) ? res : [];
        fileList = raw.map((e) => ({
          hash: e.hash || '',
          filename: e.filename || e.name || 'unnamed',
          size: e.size || 0,
          mime_type: mimeOf(e.filename || e.name, e.mime_type || e.mime),
          created_at: e.created_at || e.mod_time || '',
          isDir: false,
          path: e.provider_path || e.path || '',
        }));
      } else {
        const query = `?path=${encodeURIComponent(currentDir)}`;
        const res = await ws.admin('GET', `/files/browse${query}`);
        const raw = Array.isArray(res) ? res : [];
        fileList = raw.map((e) => ({
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

      const hashes = fileList.map((f) => f.hash).filter(Boolean);
      if (!client && hashes.length > 0) {
        try {
          const res = await ws.admin('POST', '/tags/batch', { shas: hashes });
          const batchTags = res?.tags || res?.data || {};
          setFileTags((prev) => ({ ...prev, ...batchTags }));
        } catch {
          // tag endpoint might not be available
        }
      }
    } catch {
      setErr('');
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

  // Unified download through ws.downloadToFile (routes PeerJS -> WS -> HTTP and updates globalProgressHub)
  const onDownload = async (f) => {
    try {
      await ws.downloadToFile(f.hash, f.filename, { size: f.size });
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

  // Generates a canonical self-contained SHA256 file URL (#/f/<sha256>[?peer=<peerId>&name=<filename>])
  const onCopyDeepLink = (f) => {
    const origin = window.location.origin;
    const base = (import.meta.env?.BASE_URL || '/').replace(/\/$/, '');
    const sess = peerSession || getNodeSession?.();
    let url = `${origin}${base}#/f/${f.hash}`;
    try {
      url = buildFileUrl(f.hash, {
        baseUrl: `${origin}${base}`,
        peerId: sess?.peerId || '',
        filename: f.filename || '',
        mode: 'spa',
      });
    } catch {}
    navigator.clipboard?.writeText(url);
    setCopiedHash(f.hash);
    setTimeout(() => setCopiedHash(''), 2000);
  };

  const saveFileTags = async (hash, tags) => {
    await ws.admin('POST', `/tags/sha/${hash}`, { tags });
    setFileTags((prev) => ({ ...prev, [hash]: tags }));
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
