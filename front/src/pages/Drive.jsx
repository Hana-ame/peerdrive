// Module ②: My Cloud Drive —— file management for this node (goes through WS admin frames to the backend)
// List / upload / download / delete / generate share links.
import React, { useState, useEffect, useCallback, useRef } from 'react';
import { useParams } from 'react-router-dom';
import * as ws from '../ws';

function fmtBytes(n) {
  if (!n || n === 0) return '—';
  const u = ['B', 'KB', 'MB', 'GB'];
  let v = n, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(v < 10 ? 1 : 0) + ' ' + u[i];
}

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  if (isNaN(d)) return ts;
  return d.toLocaleString('en-US', { hour12: false });
}

export default function Drive() {
  const { hash: deepHash } = useParams();
  const [files, setFiles] = useState(null); // null = loading
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [shareLink, setShareLink] = useState('');
  const [copiedHash, setCopiedHash] = useState('');
  const fileRef = useRef(null);

  const load = useCallback(async () => {
    setErr('');
    try {
      const res = await ws.admin('GET', '/files');
      setFiles(Array.isArray(res) ? res : []);
    } catch (e) {
      setErr(e?.message || String(e));
      setFiles([]);
    }
  }, []);
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

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';
  const td = 'px-3 py-2';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">My Cloud Drive</h1>
            <p className="text-sm text-gray-500 mt-0.5">Files registered on this node (content-addressed storage)</p>
          </div>
          <button
            onClick={() => fileRef.current?.click()}
            disabled={busy}
            className="btn-brand"
          >
            {busy ? 'Uploading...' : '+ Upload File'}
          </button>
          <input ref={fileRef} type="file" className="hidden" onChange={onPick} />
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

        {files === null ? (
          <div className="text-center py-16 text-gray-500 text-sm">Loading...</div>
        ) : files.length === 0 ? (
          <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
            <p className="mb-2">No files yet</p>
            <p className="text-xs text-gray-600">Click "Upload File" in the top right or register local paths on the backend.</p>
          </div>
        ) : (
          <div className="card-surface overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-white/[0.03]">
                <tr>
                  <th className={th}>Name</th>
                  <th className={th}>Size</th>
                  <th className={th}>Type</th>
                  <th className={th}>Registered</th>
                  <th className={th + ' text-right'}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {files.map((f, i) => {
                  const isTarget = deepHash && f.hash === deepHash;
                  return (
                    <tr
                      key={f.hash || i}
                      className={`border-t border-white/[0.04] transition-colors ${
                        isTarget
                          ? 'bg-brand-500/15 border-l-2 border-l-brand-400 hover:bg-brand-500/20'
                          : 'hover:bg-white/[0.02]'
                      }`}
                    >
                      <td className={td + ' text-gray-200 max-w-[260px] truncate'}>
                        <div className="flex items-center gap-1.5">
                          {isTarget && (
                            <span className="inline-block w-1.5 h-1.5 rounded-full bg-brand-400 shrink-0" title="Deep linked" />
                          )}
                          <span className="truncate">{f.filename}</span>
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
      </div>
    </div>
  );
}
