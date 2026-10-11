import React, { useState, useEffect, useRef } from 'react';
import * as ws from '../../platform/transport-ws';
import { getNodeSession } from '../../lib/nodeSession';
import { previewBlobCache, previewLimit } from '../../lib/cache';

/**
 * FileThumbnail renders lazy-loaded image thumbnails for files via content-addressed SHA-256.
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
  const elRef = useRef(null);

  const ext = (filename || '').split('.').pop().toLowerCase();
  const isImg = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico'].includes(ext) ||
    String(mimeType || '').startsWith('image/');

  useEffect(() => {
    if (!sha || !isImg) {
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
  }, [sha, isImg]);

  useEffect(() => {
    if (!sha || !isImg || !isVisible || status === 'ok') return;

    if (previewBlobCache.has(sha)) {
      setUrl(previewBlobCache.get(sha));
      setStatus('ok');
      return;
    }

    let alive = true;
    setStatus('loading');

    const fetchBytes = async () => {
      const session = getNodeSession();
      if (session?.client) {
        const chunks = [];
        for await (const chunk of session.client.stream(sha)) {
          chunks.push(chunk);
        }
        return new Blob(chunks, { type: mimeType || 'image/jpeg' });
      }
      const bytes = await ws.download(sha);
      return new Blob([bytes], { type: mimeType || 'image/jpeg' });
    };

    previewLimit(fetchBytes)
      .then((blob) => {
        if (!alive) return;
        const objectUrl = URL.createObjectURL(blob);
        previewBlobCache.set(sha, objectUrl, blob.size || 0);
        setUrl(objectUrl);
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
  }, [sha, isImg, isVisible, status, mimeType]);

  if (isImg && status === 'ok' && url) {
    return (
      <div ref={elRef} className={`relative overflow-hidden ${className}`}>
        <img src={url} alt={filename} className={imgClassName} />
      </div>
    );
  }

  if (isImg && status === 'loading') {
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
