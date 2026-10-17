// DirectFileView.jsx — canonical self-contained file URL page (#/f/:sha256, invariant #2).
//
// Orchestration only: reads sha256 + ?peer/?node/?name/?signal from the route,
// pulls bytes through the existing transport primitives in priority order
// (peerjs client → WS downloadStream → HTTP /f/:sha256 fallback), then hands the
// bytes to DirectFilePreview for kind-based rendering. All parsing/formatting
// lives in platform/shared or peerdrive-core — nothing algorithmic inline.
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession, setNodeSession } from '../../../lib/nodeSession';
import {
  buildFileUrl,
  sanitizeFilename,
  sanitizeUntrustedText,
  validateSignalOrApiUrl,
} from '../../../platform/shared';
import { kindOf, mimeOf, MAX_TEXT_PREVIEW_BYTES } from '../../../platform/shared/mime';

// autoDialPeer dials the ?peer=<peerId> owner over PeerJS when no matching
// session exists yet — a #/f/ deep link can arrive from a node this browser has
// never connected to (same dance as CollectionView).
async function autoDialPeer(queryPeer, querySignal) {
  const { connectToPeer } = await import('peerdrive-client');
  const PeerModule = await import('peerjs');
  const PeerCtor = PeerModule.Peer || PeerModule.default?.Peer || PeerModule.default;
  if (!PeerCtor) return null;
  const sigOpts = {};
  if (querySignal) {
    const sigCheck = validateSignalOrApiUrl(querySignal);
    if (sigCheck.valid && sigCheck.url) {
      const u = sigCheck.url;
      sigOpts.host = u.hostname;
      sigOpts.port = u.port ? Number(u.port) : u.protocol === 'https:' || u.protocol === 'wss:' ? 443 : 80;
      sigOpts.secure = u.protocol === 'https:' || u.protocol === 'wss:';
      sigOpts.path = u.pathname || '/';
    }
  }
  const client = await connectToPeer(PeerCtor, queryPeer, sigOpts);
  return { client, peerId: queryPeer, signalUrl: sigOpts.host ? querySignal : '' };
}

// readAll concatenates an async-iterable of Uint8Array chunks into one buffer.
async function readAll(stream) {
  const chunks = [];
  let received = 0;
  for await (const chunk of stream) {
    chunks.push(chunk);
    received += chunk.byteLength;
  }
  const out = new Uint8Array(received);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.byteLength;
  }
  return out;
}

// DirectFilePreview renders the fetched bytes by extension kind. Text is decoded
// (truncated to MAX_TEXT_PREVIEW_BYTES) into a <pre> — React escapes it, so it
// cannot execute script; media uses blob URLs which run nothing beyond the
// media pipeline. Everything else (kind === null) returns null and the parent
// shows the download affordance instead.
function DirectFilePreview({ bytes, name }) {
  const kind = kindOf(name);
  const mime = mimeOf(name) || 'application/octet-stream';
  const url = useMemo(
    () => (kind && kind !== 'text' ? URL.createObjectURL(new Blob([bytes], { type: mime })) : ''),
    [bytes, kind, mime],
  );
  useEffect(() => () => {
    if (url) URL.revokeObjectURL(url);
  }, [url]);

  if (kind === 'text') {
    const text = new TextDecoder('utf-8').decode(bytes.subarray(0, MAX_TEXT_PREVIEW_BYTES));
    return (
      <pre className="whitespace-pre-wrap break-all font-mono text-xs text-gray-300 bg-black/30 rounded-lg p-4 max-h-[60vh] overflow-auto">
        {text}
      </pre>
    );
  }
  if (kind === 'image') return <img src={url} alt={name} className="max-w-full max-h-[70vh] object-contain rounded-lg" />;
  if (kind === 'video') return <video src={url} controls className="max-w-full max-h-[70vh] rounded-lg" />;
  if (kind === 'audio') return <audio src={url} controls className="w-full" />;
  if (kind === 'pdf') return <iframe src={url} title={name} className="w-full h-[70vh] rounded-lg border border-white/10" />;
  return null;
}

export default function DirectFileView() {
  const { sha256 } = useParams();
  const [searchParams] = useSearchParams();
  const queryPeer = sanitizeUntrustedText(searchParams.get('peer') || searchParams.get('node') || '', 64);
  const querySignal = searchParams.get('signal') || searchParams.get('url') || '';
  const urlName = sanitizeFilename(searchParams.get('name') || '', sha256 || 'file');

  const [state, setState] = useState({ phase: 'loading', bytes: null, err: '' });
  const [copied, setCopied] = useState(false);

  const load = useCallback(async (hash) => {
    setState({ phase: 'loading', bytes: null, err: '' });
    try {
      let session = getNodeSession();
      if (queryPeer && (!session?.client || session.peerId !== queryPeer)) {
        try {
          const dialed = await autoDialPeer(queryPeer, querySignal);
          if (dialed) {
            session = dialed;
            setNodeSession(dialed);
          }
        } catch {
          /* fall through to WS/HTTP */
        }
      }
      let bytes;
      if (session?.client) {
        bytes = await readAll(session.client.stream(hash));
      } else if (ws.getStatus?.() === 'open') {
        bytes = await readAll(ws.downloadStream(hash));
      } else {
        bytes = await ws.httpDownload(hash, { name: urlName });
      }
      setState({ phase: 'ready', bytes, err: '' });
    } catch (e) {
      setState({ phase: 'error', bytes: null, err: sanitizeUntrustedText(e?.message || String(e), 512) });
    }
  }, [queryPeer, querySignal, urlName]);

  useEffect(() => {
    if (sha256) load(sha256);
  }, [sha256, load]);

  const copyCanonicalLink = () => {
    try {
      const origin = typeof window !== 'undefined' ? window.location.origin : '';
      const base = (import.meta.env?.BASE_URL || '/').replace(/\/$/, '');
      const sess = getNodeSession();
      const url = buildFileUrl(sha256, {
        baseUrl: `${origin}${base}`,
        peerId: sess?.peerId || queryPeer || '',
        name: urlName,
        mode: 'spa',
      });
      navigator.clipboard?.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {}
  };

  const download = () => {
    if (!state.bytes) return;
    const blobUrl = URL.createObjectURL(new Blob([state.bytes], { type: mimeOf(urlName) || 'application/octet-stream' }));
    const a = document.createElement('a');
    a.href = blobUrl;
    a.download = sanitizeFilename(urlName);
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(() => URL.revokeObjectURL(blobUrl), 5000);
  };

  return (
    <div className="p-4 sm:p-8 overflow-y-auto overflow-x-hidden h-full max-w-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
          <div className="min-w-0">
            <h1 className="text-2xl font-bold">File</h1>
            <p className="text-sm text-gray-500 mt-0.5 break-all font-mono">{sha256}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2 shrink-0">
            {state.phase === 'ready' && (
              <>
                <button type="button" onClick={copyCanonicalLink} className="btn-ghost text-xs">
                  {copied ? '✓ 已复制文件链接' : '🔗 复制唯一 URL (#/f/)'}
                </button>
                <button type="button" onClick={download} className="btn-ghost text-xs">
                  ⬇️ 下载
                </button>
              </>
            )}
          </div>
        </div>

        {state.phase === 'loading' && (
          <div className="flex items-center justify-center py-24">
            <div className="w-5 h-5 border-2 border-brand-400 border-t-transparent rounded-full animate-spin" />
            <span className="ml-3 text-sm text-gray-400">拉取 {urlName}…</span>
          </div>
        )}

        {state.phase === 'error' && (
          <div className="card-surface p-5">
            <p className="text-sm text-red-400">加载失败：{state.err}</p>
            <p className="text-xs text-gray-500 mt-2 break-all font-mono">{sha256}</p>
          </div>
        )}

        {state.phase === 'ready' && state.bytes && (
          <div className="card-surface p-5">
            <div className="flex items-center justify-between gap-2 mb-3">
              <p className="text-sm text-gray-300 break-all">{urlName}</p>
              <span className="text-xs text-gray-500 shrink-0">{state.bytes.byteLength} bytes</span>
            </div>
            <DirectFilePreview bytes={state.bytes} name={urlName} />
            {kindOf(urlName) === null && (
              <div className="mt-4">
                <button type="button" onClick={download} className="btn-brand text-xs">
                  ⬇️ 下载文件
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
