import React, { useState, useEffect, useRef } from 'react';
import * as ws from '../../platform/transport-ws';
import { getNodeSession, onNodeSession } from '../../lib/nodeSession';
import { previewBlobCache, previewLimit } from '../../lib/cache';
import { swControlled, ensureSWReady } from '../../platform/shared/swBridge';
import { STORAGE_KEY_API_BASE } from '../../platform/shared/storageKeys';
import { withPSKQuery } from '../../platform/shared/nodePSK';

/**
 * FileThumbnail renders lazy-loaded image and video thumbnails for files via content-addressed SHA-256.
 * It uses IntersectionObserver for lazy loading and module-level previewBlobCache with LRU eviction.
 */
export default function FileThumbnail({
  sha,
  filename = '',
  mimeType = '',
  fallbackIcon = '📄',
  className = 'w-full h-full',
  imgClassName = 'w-full h-full object-cover',
}) {
  const [url, setUrl] = useState('');
  const [status, setStatus] = useState('idle'); // idle | loading | ok | error | none
  const [isVisible, setIsVisible] = useState(false);
  const [session, setSession] = useState(() => getNodeSession());
  const elRef = useRef(null);

  useEffect(() => {
    return onNodeSession((s) => setSession(s));
  }, []);

  const ext = (filename || '').split('.').pop().toLowerCase();
  const isImg = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico'].includes(ext) ||
    String(mimeType || '').startsWith('image/');
  const isVid = ['mp4', 'webm', 'ogg', 'mov', 'mkv', 'avi', 'm4v'].includes(ext) ||
    String(mimeType || '').startsWith('video/');
  const isMedia = isImg || isVid;

  useEffect(() => {
    if (!sha || !isMedia) {
      setStatus('none');
      return;
    }

    if (previewBlobCache.has(sha)) {
      setUrl(previewBlobCache.get(sha));
      setStatus('ok');
      return;
    }

    if (!('IntersectionObserver' in window)) {
      setIsVisible(true);
      return;
    }

    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) {
        setIsVisible(true);
        observer.disconnect();
      }
    }, { rootMargin: '100px' });

    if (elRef.current) observer.observe(elRef.current);
    return () => observer.disconnect();
  }, [sha, isMedia]);

  useEffect(() => {
    if (!sha || !isMedia || !isVisible || status === 'ok') return;

    if (previewBlobCache.has(sha)) {
      setUrl(previewBlobCache.get(sha));
      setStatus('ok');
      return;
    }

    let alive = true;
    setStatus('loading');

    const loadMediaUrl = async () => {
      const activeSession = session || getNodeSession();

      // If it's a video and Service Worker or local HTTP is available, use direct streaming URL
      if (isVid) {
        if (activeSession?.client) {
          let ready = swControlled();
          if (!ready) {
            ready = await ensureSWReady();
          }
          if (ready) {
            const base = import.meta.env.BASE_URL || '/';
            return `${base}swdrive/${encodeURIComponent(sha)}?name=${encodeURIComponent(filename || 'video.mp4')}`;
          }
        } else if (ws.getStatus() === 'open') {
          const rawBase = typeof window !== 'undefined' ? localStorage.getItem(STORAGE_KEY_API_BASE) || '' : '';
          const apiBase = rawBase.replace(/\/+$/, '');
          return withPSKQuery(`${apiBase}/sha256sum/${encodeURIComponent(sha)}/${encodeURIComponent(filename || 'video.mp4')}`);
        }
      }

      // Otherwise fetch bytes via P2P stream or WS download
      const fetchBytes = async () => {
        if (activeSession?.client) {
          const chunks = [];
          for await (const chunk of activeSession.client.stream(sha)) {
            chunks.push(chunk);
          }
          return new Blob(chunks, { type: mimeType || (isVid ? 'video/mp4' : 'image/jpeg') });
        }
        const bytes = await ws.download(sha);
        return new Blob([bytes], { type: mimeType || (isVid ? 'video/mp4' : 'image/jpeg') });
      };

      const blob = await previewLimit(fetchBytes);
      const objectUrl = URL.createObjectURL(blob);
      previewBlobCache.set(sha, objectUrl, blob.size || 0);
      return objectUrl;
    };

    loadMediaUrl()
      .then((mediaUrl) => {
        if (!alive) return;
        setUrl(mediaUrl);
        setStatus('ok');
      })
      .catch(() => {
        if (alive) {
          setStatus('error');
        }
      });

    return () => {
      alive = false;
    };
  }, [sha, isMedia, isVid, isVisible, status, mimeType, session, filename]);

  if (isVid && status === 'ok' && url) {
    return (
      <div ref={elRef} className={`relative overflow-hidden ${className}`}>
        <video
          src={url}
          className={imgClassName}
          preload="metadata"
          muted
          playsInline
          onLoadedData={(e) => {
            try { e.target.currentTime = 0.1; } catch {}
          }}
        />
        <span className="absolute bottom-1 right-1 px-1 py-0.5 rounded text-[9px] bg-black/60 text-white/90 backdrop-blur-sm pointer-events-none flex items-center gap-0.5">
          ▶
        </span>
      </div>
    );
  }

  if (isImg && status === 'ok' && url) {
    return (
      <div ref={elRef} className={`relative overflow-hidden ${className}`}>
        <img src={url} alt={filename} className={imgClassName} />
      </div>
    );
  }

  if (isMedia && status === 'loading') {
    return (
      <div ref={elRef} className={`relative flex items-center justify-center bg-gray-900/60 ${className}`}>
        <span className="text-xs opacity-50 animate-pulse">⏳</span>
      </div>
    );
  }

  return (
    <div ref={elRef} className={`relative flex items-center justify-center bg-gray-900/40 ${className}`}>
      <span className="text-2xl opacity-40">{fallbackIcon}</span>
    </div>
  );
}
