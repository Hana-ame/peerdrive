// Node control page: the control screen for the remote node after a successful connection.
// Gets the connected peer from the global session: node info / shares (files · collections) / save / disconnect.
import React, { useState, useEffect, useCallback, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { getNodeSession, clearNodeSession } from '../../../lib/nodeSession';
import { kindOf, mimeOf } from '../../../platform/shared/mime';
import NodeGuestView from '../components/NodeGuestView';
import NodeDisconnectedView from '../components/NodeDisconnectedView';
import NodeHeader from '../components/NodeHeader';
import NodeShareList from '../components/NodeShareList';
import NodeMediaPreview from '../components/NodeMediaPreview';
import DownloadProgressBar from '../components/DownloadProgressBar';

export default function NodeControl() {
  const navigate = useNavigate();
  const session = getNodeSession();
  const isWsOpen = ws.getStatus() === 'open';
  const isGuest = !isWsOpen && Boolean(session?.client && session?.peerId);

  // 1. Permission gate: guest mode only allows drive and collection browsing
  if (isGuest) return <NodeGuestView />;
  if (!session?.client || !session.peerId) return <NodeDisconnectedView />;

  // 2. State & Data fetching
  const [share, setShare] = useState(null); // null = loading
  const [err, setErr] = useState('');
  const [collOpen, setCollOpen] = useState(null);
  const [downloading, setDownloading] = useState(null); // {hash,name,loaded,total}
  const [preview, setPreview] = useState(null); // {hash,name,kind,url?,text?,loading,error,imgIndex?}
  const [viewer, setViewer] = useState({ scale: 1, x: 0, y: 0 });
  const dragRef = useRef(null);

  const loadShares = useCallback(async () => {
    if (!session?.client) return;
    setErr('');
    try {
      const s = await session.client.shares();
      setShare(s);
    } catch (e) {
      setErr(e?.message || String(e));
      setShare({ total: 0, files: [], collections: [] });
    }
  }, [session]);

  useEffect(() => { loadShares(); }, [loadShares]);

  // 3. Actions: File saving & Download
  const save = async (item) => {
    if (downloading) return;
    const name = item.path || item.name || 'download';
    const total = item.size || 0;
    setDownloading({ hash: item.hash, name, loaded: 0, total });
    try {
      const chunks = [];
      let loaded = 0;
      for await (const chunk of session.client.stream(item.hash)) {
        chunks.push(chunk);
        loaded += chunk.byteLength;
        setDownloading(d => (d && d.hash === item.hash ? { ...d, loaded } : d));
        await new Promise(r => setTimeout(r, 0));
      }
      const blob = new Blob(chunks, { type: mimeOf(name) });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = String(name).split(/[\\/]/).pop() || 'download';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 15000);
    } catch (e) {
      setErr(e?.message || String(e));
    } finally {
      setDownloading(null);
    }
  };

  const openFile = (item) => {
    const name = item.path || item.name || '';
    if (kindOf(name)) openPreview(item);
    else save(item);
  };

  // 4. Media Preview Engine
  const openPreview = async (item) => {
    window.history.pushState({ pdPreview: true }, '');
    const name = item.path || item.name || '';
    const kind = kindOf(name);
    if (!kind) {
      setPreview({ hash: item.hash, name, kind: null, error: 'This type does not support preview. You can download to view.' });
      return;
    }
    const imgList = (share?.files || []).filter(f => kindOf(f.path || f.name) === 'image');
    const imgIdx = kind === 'image' ? imgList.findIndex(f => f.hash === item.hash) : null;
    if (kind === 'text') {
      setPreview({ hash: item.hash, name, kind, loading: true, error: '', text: '', url: '', imgIndex: imgIdx });
      const dec = new TextDecoder('utf-8');
      let acc = '';
      try {
        for await (const chunk of session.client.stream(item.hash)) {
          acc += dec.decode(chunk, { stream: true });
          setPreview(p => (p && p.hash === item.hash ? { ...p, text: acc } : p));
          await new Promise(r => setTimeout(r, 0));
        }
        acc += dec.decode();
        setPreview(p => (p && p.hash === item.hash ? { ...p, text: acc, loading: false } : p));
      } catch (e) {
        setPreview(p => (p && p.hash === item.hash ? { ...p, loading: false, error: e?.message || String(e) } : p));
      }
      return;
    }
    if (kind === 'image') {
      setViewer({ scale: 1, x: 0, y: 0 });
      const total = item.size || 0;
      setPreview({ hash: item.hash, name, kind, loading: true, error: '', text: '', url: '', progress: { loaded: 0, total }, imgIndex: imgIdx });
      const chunks = [];
      let loaded = 0;
      try {
        for await (const chunk of session.client.stream(item.hash)) {
          chunks.push(chunk);
          loaded += chunk.byteLength;
          if (loaded % (1024 * 1024) < chunk.byteLength || loaded >= total) {
            setPreview(p => {
              if (!p || p.hash !== item.hash) return p;
              if (p.url) URL.revokeObjectURL(p.url);
              return {
                ...p,
                url: URL.createObjectURL(new Blob(chunks, { type: mimeOf(name) })),
                progress: { loaded, total },
              };
            });
            await new Promise(r => setTimeout(r, 0));
          }
        }
        setPreview(p => (p && p.hash === item.hash ? { ...p, loading: false, progress: { loaded: loaded || total, total } } : p));
      } catch (e) {
        setPreview(p => (p && p.hash === item.hash ? { ...p, loading: false, error: e?.message || String(e) } : p));
      }
      return;
    }
    const url = `${import.meta.env.BASE_URL}swdrive/${encodeURIComponent(item.hash)}?name=${encodeURIComponent(name)}${item.size ? '&size=' + item.size : ''}`;
    setPreview({ hash: item.hash, name, kind, loading: false, error: '', text: '', url, imgIndex: imgIdx });
  };

  const closePreview = () => {
    setPreview(p => {
      if (p?.url) URL.revokeObjectURL(p.url);
      return null;
    });
  };

  const fallbackBlob = async () => {
    const cur = preview;
    if (!cur || !cur.hash) return;
    try {
      const blob = await session.client.fetchBlob(cur.hash, { name: cur.name || '', mime: mimeOf(cur.name || '') });
      const u = URL.createObjectURL(blob);
      setPreview(p => (p && p.hash === cur.hash ? { ...p, url: u, error: '', _blob: true } : p));
    } catch (e) {
      setPreview(p => (p && p.hash === cur.hash ? { ...p, error: 'Preview failed: ' + (e?.message || String(e)) } : p));
    }
  };

  const onMediaError = () => {
    if (preview?._blob) {
      setPreview(p => (p ? { ...p, error: 'Media loading failed (return to list or download to view)' } : p));
      return;
    }
    fallbackBlob();
  };

  useEffect(() => {
    const onPop = () => {
      if (preview) {
        setPreview(p => {
          if (p?.url) URL.revokeObjectURL(p.url);
          return null;
        });
        setViewer({ scale: 1, x: 0, y: 0 });
      }
    };
    window.addEventListener('popstate', onPop);
    return () => window.removeEventListener('popstate', onPop);
  }, [preview]);

  const zoomBy = (factor) => setViewer(v => ({ ...v, scale: Math.min(5, Math.max(0.1, +(v.scale * factor).toFixed(2))) }));
  const resetZoom = () => setViewer({ scale: 1, x: 0, y: 0 });
  const goImg = (delta) => {
    const imgList = (share?.files || []).filter(f => kindOf(f.path || f.name) === 'image');
    if (imgList.length < 2 || preview?.imgIndex == null) return;
    const next = (preview.imgIndex + delta + imgList.length) % imgList.length;
    openPreview(imgList[next]);
  };
  const onDragStart = (e) => {
    if (preview?.kind !== 'image' || viewer.scale <= 1) return;
    dragRef.current = { sx: e.clientX, sy: e.clientY, ox: viewer.x, oy: viewer.y };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };
  const onDragMove = (e) => {
    if (!dragRef.current) return;
    setViewer(v => ({ ...v, x: dragRef.current.ox + (e.clientX - dragRef.current.sx), y: dragRef.current.oy + (e.clientY - dragRef.current.sy) }));
  };
  const onDragEnd = () => { dragRef.current = null; };

  const disconnect = () => {
    clearNodeSession();
    navigate('/');
  };

  // 5. Clean Layout Orchestration
  return (
    <div className="p-4 sm:p-8 overflow-y-auto h-full">
      <div className="max-w-4xl mx-auto">
        <NodeHeader session={session} onDisconnect={disconnect} />

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        <h2 className="text-sm font-semibold text-gray-200 mb-2">
          Peer Shares{share ? ` (${share.total ?? 0} items)` : ''}
        </h2>

        <NodeShareList
          share={share}
          collOpen={collOpen}
          setCollOpen={setCollOpen}
          onOpenFile={openFile}
        />
      </div>

      <DownloadProgressBar downloading={downloading} />

      <NodeMediaPreview
        preview={preview}
        share={share}
        viewer={viewer}
        zoomBy={zoomBy}
        resetZoom={resetZoom}
        goImg={goImg}
        onDragStart={onDragStart}
        onDragMove={onDragMove}
        onDragEnd={onDragEnd}
        onMediaError={onMediaError}
        closePreview={closePreview}
        setPreview={setPreview}
      />
    </div>
  );
}
