// CollectionView.jsx — Self-contained canonical Collection page (#/c/:hash and #/collection/:hash).
//
// Supports loading any collection manifest across:
//   1. PeerJS WebRTC DataChannel (auto-connecting to ?peer=<peerId> if specified in URL)
//   2. Local WebSocket (/ws/peer downloadStream or /anon/collections/:hash)
//   3. Direct HTTP (/c/:hash fallback)
//   4. Pasted Collection JSON
//
// Security & Anti-Malicious-AI Hardening:
//   - Parses JSON exclusively via safeJsonParse (blocking __proto__ prototype pollution & deep nesting bombs)
//   - Sanitizes all manifest entries via sanitizeCollectionManifest (stripping "../" traversal, Bidi spoofing,
//     and flagging Indirect Prompt Injection payloads)
//   - Validates ?signal= URLs via validateSignalOrApiUrl before PeerJS dial
import React, { useCallback, useEffect, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import CollectionBrowser from '../components/CollectionBrowser';
import { manifestCache } from '../../../lib/cache';
import { getNodeSession, setNodeSession } from '../../../lib/nodeSession';
import {
  buildCollectionUrl,
  safeJsonParse,
  sanitizeCollectionManifest,
  sanitizeUntrustedText,
  validateSignalOrApiUrl,
  MAX_MANIFEST_BYTES,
} from '../../../../../packages/peerdrive-core/src/index.js';

function parseManifest(data) {
  return sanitizeCollectionManifest(data);
}

function parseManifestText(rawText) {
  const obj = safeJsonParse(rawText, { maxBytes: MAX_MANIFEST_BYTES, throwOnError: true });
  return sanitizeCollectionManifest(obj);
}

async function readStreamAsText(readableStream) {
  const reader = readableStream.getReader();
  const decoder = new TextDecoder('utf-8');
  let result = '';
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        result += decoder.decode();
        break;
      }
      if (value) {
        result += decoder.decode(value, { stream: true });
      }
    }
  } finally {
    reader.releaseLock();
  }
  return result;
}

export default function CollectionView() {
  const { hash } = useParams();
  const [searchParams] = useSearchParams();
  const queryPeer = sanitizeUntrustedText(searchParams.get('peer') || searchParams.get('node') || '', 64);
  const querySignal = searchParams.get('signal') || searchParams.get('url') || '';

  const [input, setInput] = useState(hash || '');
  const [json, setJson] = useState('');
  const [lastErr, setLastErr] = useState('');
  const [copiedMode, setCopiedMode] = useState('');
  const [state, setState] = useState({ phase: hash ? 'loading' : 'input', collection: null, source: '' });

  const loadBySha = useCallback(async (sha) => {
    setLastErr('');
    const cached = manifestCache.get(sha);
    if (cached) {
      setState({ phase: 'ready', collection: cached, source: sha });
      return;
    }

    setState({ phase: 'loading', collection: null, source: sha });
    try {
      let data;
      let session = getNodeSession();

      // Auto-dial PeerJS if URL carries ?peer=<peerId> and not yet connected
      if (queryPeer && (!session?.client || session.peerId !== queryPeer)) {
        try {
          const { connectToPeer } = await import('peerdrive-client');
          const PeerModule = await import('peerjs');
          const PeerCtor = PeerModule.Peer || PeerModule.default?.Peer || PeerModule.default;
          if (PeerCtor) {
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
            session = { client, peerId: queryPeer, signalUrl: sigOpts.host ? querySignal : '' };
            setNodeSession(session);
          }
        } catch {
          /* continue to existing session or WS/HTTP fallback */
        }
      }

      if (session?.client && typeof ws.getStatus === 'function' && ws.getStatus() === 'closed') {
        try {
          const snap = await session.client.shares();
          const match = (snap?.collections || []).find((c) => c.hash === sha || c.name === sha);
          if (match && Array.isArray(match.entries)) {
            data = parseManifest({
              hash: match.hash,
              friendly_name: match.name,
              entries: match.entries,
              tags: match.tags,
              created_at: '',
            });
          }
        } catch {
          /* ignore */
        }
      }

      if (!data) {
        if (session?.client) {
          try {
            const snap = await session.client.shares();
            const match = (snap?.collections || []).find((c) => c.hash === sha || c.name === sha);
            if (match && Array.isArray(match.entries)) {
              data = parseManifest({
                hash: match.hash,
                friendly_name: match.name,
                entries: match.entries,
                tags: match.tags,
                created_at: '',
              });
            }
          } catch {
            /* ignore */
          }
          if (!data) {
            const chunks = [];
            for await (const chunk of session.client.stream(sha)) {
              chunks.push(chunk);
            }
            const blob = new Blob(chunks);
            const text = await blob.text();
            data = parseManifestText(text);
          }
        } else {
          try {
            const stream = ws.downloadStream(sha);
            const text = await readStreamAsText(stream);
            data = parseManifestText(text);
          } catch {
            data = parseManifest(await ws.admin('GET', `/anon/collections/${sha}`));
          }
        }
      }

      if (!data) {
        throw new Error('Failed to load collection manifest');
      }

      manifestCache.set(sha, data);
      setState({ phase: 'ready', collection: data, source: sha });
    } catch (e) {
      setLastErr(sanitizeUntrustedText(e?.message || String(e), 512));
      setState({ phase: 'error', collection: null, source: sha });
    }
  }, [queryPeer, querySignal]);

  useEffect(() => {
    if (hash) loadBySha(hash);
  }, [hash, loadBySha]);

  const loadJson = () => {
    try {
      const data = parseManifestText(json);
      setLastErr('');
      setState({ phase: 'ready', collection: data, source: 'pasted JSON' });
    } catch (e) {
      setLastErr(sanitizeUntrustedText(e?.message || String(e), 512));
    }
  };

  const copyCanonicalCollectionLink = (mode = 'spa') => {
    if (!state.source || state.source === 'pasted JSON') return;
    try {
      const origin = typeof window !== 'undefined' ? window.location.origin : '';
      const base = (import.meta.env?.BASE_URL || '/').replace(/\/$/, '');
      const session = getNodeSession();
      const url = buildCollectionUrl(state.source, {
        baseUrl: `${origin}${base}`,
        peerId: session?.peerId || queryPeer || '',
        signalUrl: querySignal && validateSignalOrApiUrl(querySignal).valid ? querySignal : '',
        name: state.collection?.friendly_name || '',
        mode,
      });
      navigator.clipboard?.writeText(url);
      setCopiedMode(mode);
      setTimeout(() => setCopiedMode(''), 2000);
    } catch {}
  };

  const inputPanel = (
    <div className="card-surface p-5 space-y-4">
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">Collection SHA</label>
        <div className="flex gap-2">
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="64-hex sha of the collection JSON"
            className="input-base flex-1 font-mono text-xs"
          />
          <button
            onClick={() => input.trim() && loadBySha(input.trim())}
            className="btn-brand shrink-0"
          >
            Load
          </button>
        </div>
        <p className="text-[11px] text-gray-600 mt-1.5">
          The collection itself is a JSON stored in the sha filesystem — enter its sha to fetch it by content address.
        </p>
      </div>
      <div className="border-t border-white/[0.06]" />
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">…or paste collection JSON</label>
        <textarea
          value={json}
          onChange={(e) => setJson(e.target.value)}
          rows={6}
          placeholder={'{\n  "friendly_name": "demo",\n  "entries": [\n    { "path": "photos/a.jpg", "sha": "<64hex>", "preview": "<64hex>" }\n  ]\n}'}
          className="input-base w-full font-mono text-xs"
        />
        <button onClick={loadJson} className="btn-brand mt-2">
          Browse JSON
        </button>
      </div>
    </div>
  );

  return (
    <div className="p-4 sm:p-8 overflow-y-auto overflow-x-hidden h-full max-w-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-5">
          <div>
            <h1 className="text-2xl font-bold">Collection</h1>
            <p className="text-sm text-gray-500 mt-0.5">Browse a collection manifest as a folder tree</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {state.phase === 'ready' && state.source && state.source !== 'pasted JSON' && (
              <>
                <button
                  type="button"
                  onClick={() => copyCanonicalCollectionLink('spa')}
                  className="btn-ghost text-xs"
                >
                  {copiedMode === 'spa' ? '✓ 已复制合集链接' : '🔗 复制唯一 URL (#/c/)'}
                </button>
                <button
                  type="button"
                  onClick={() => copyCanonicalCollectionLink('http')}
                  className="btn-ghost text-xs"
                >
                  {copiedMode === 'http' ? '✓ 已复制直链' : '🌐 复制 HTTP 直链 (/c/)'}
                </button>
              </>
            )}
            {state.phase !== 'input' && (
              <button
                onClick={() => setState({ phase: 'input', collection: null, source: '' })}
                className="btn-ghost"
              >
                Load another
              </button>
            )}
          </div>
        </div>

        {lastErr && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">
            {lastErr}
          </div>
        )}

        {state.collection?._security?.aiInjectionSuspected && (
          <div className="mb-4 text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded-lg px-3.5 py-2.5">
            🛡️ 安全网关提示：该合集清单中检测到异常控制字符或疑似恶意 AI 提示词注入载荷，已完成自动净化隔离。
          </div>
        )}

        {state.phase === 'input' && inputPanel}

        {state.phase === 'loading' && (
          <div className="text-center py-16 text-gray-500 text-sm">
            Loading collection {state.source.slice(0, 16)}…
          </div>
        )}

        {state.phase === 'error' && (
          <div className="text-center py-16 text-gray-500 text-sm">
            <p>Could not load collection {state.source.slice(0, 16)}…</p>
            <button
              onClick={() => setState({ phase: 'input', collection: null, source: '' })}
              className="btn-ghost mt-4"
            >
              Back to input
            </button>
          </div>
        )}

        {state.phase === 'ready' && state.collection && (
          <div>
            <div className="text-xs text-gray-500 mb-3 flex items-center gap-2 flex-wrap">
              {typeof state.collection.friendly_name === 'string' && state.collection.friendly_name && (
                <span className="font-medium text-gray-300">{state.collection.friendly_name}</span>
              )}
              {state.source && state.source !== 'pasted JSON' && (
                <span className="font-mono text-brand-300" title={state.source}>
                  {state.source.slice(0, 16)}…
                </span>
              )}
              <span>{state.collection.entries.length} entries</span>
            </div>
            <CollectionBrowser collection={state.collection} onError={(m) => setLastErr(m)} />
          </div>
        )}
      </div>
    </div>
  );
}
