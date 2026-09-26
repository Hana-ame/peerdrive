/* Peerdrive Service Worker — PWA offline & background sync */

const CACHE_NAME = 'peerdrive-v1';
const STATIC_ASSETS = [
  './',
  './index.html',
];

/* ─── Install: cache app shell ─── */
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => {
      return cache.addAll(STATIC_ASSETS);
    }).then(() => self.skipWaiting())
  );
});

/* ─── Activate: clean old caches ─── */
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.map((k) => {
        if (k !== CACHE_NAME) return caches.delete(k);
      }))
    ).then(() => self.clients.claim())
  );
});

/* ─── Fetch: network-first for API, cache-first for static ─── */
self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);

  /* 断点续传：伪造 fetch，把 PeerJS 流喂给 img/video/audio（支持 Range） */
  if (url.pathname.includes('/swdrive/')) {
    event.respondWith(servePeerDriveStream(event.request));
    return;
  }

  /* API calls — network first, fall back to cache */
  if (url.pathname.startsWith('/api/') || url.port === '7373' || url.hostname === 'localhost' && url.port) {
    event.respondWith(
      fetch(request)
        .then((response) => {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
          return response;
        })
        .catch(() => caches.match(request).then((cached) => cached || new Response('Offline', { status: 503 })))
    );
    return;
  }

  /* Static assets — cache-first */
  if (
    request.destination === 'style' ||
    request.destination === 'script' ||
    request.destination === 'font' ||
    request.destination === 'image' ||
    request.destination === 'document'
  ) {
    event.respondWith(
      caches.match(request).then((cached) => {
        const fetchPromise = fetch(request).then((response) => {
          if (response && response.status === 200) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
          }
          return response;
        }).catch(() => cached);
        return cached || fetchPromise;
      })
    );
    return;
  }

  /* Everything else — network only */
  event.respondWith(fetch(request).catch(() => caches.match('/index.html')));
});

/* ─── Background Sync: retry pending uploads ─── */
self.addEventListener('sync', (event) => {
  if (event.tag === 'sync-uploads') {
    event.waitUntil(processPendingUploads());
  }
});

async function processPendingUploads() {
  try {
    const cache = await caches.open(CACHE_NAME);
    const requests = await cache.keys();
    const uploadReqs = requests.filter((r) => r.url.includes('/api/upload'));

    for (const req of uploadReqs) {
      try {
        const res = await fetch(req);
        if (res.ok) {
          await cache.delete(req);
        }
      } catch {
        /* Will retry on next sync */
      }
    }
  } catch {
    /* Silent fail */
  }
}

/* ─── Listen for messages from the app ─── */
self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

/* ═══ 断点续传：伪造 fetch（/swdrive/<hash>），经 MessageChannel 向页面要 PeerJS 流 ═══ */

function parseRangeHeader(rangeHeader) {
  if (!rangeHeader) return null;
  const m = rangeHeader.match(/bytes=(\d*)-(\d*)/);
  if (!m) return null;
  return {
    start: m[1] === '' ? null : parseInt(m[1], 10),
    end: m[2] === '' ? null : parseInt(m[2], 10),
  };
}

function guessMimeSw(name) {
  const ext = String(name || '').toLowerCase().split('.').pop();
  const map = {
    jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
    webp: 'image/webp', bmp: 'image/bmp', svg: 'image/svg+xml', avif: 'image/avif',
    mp4: 'video/mp4', webm: 'video/webm', mov: 'video/quicktime', m4v: 'video/x-m4v', ogv: 'video/ogg',
    mp3: 'audio/mpeg', wav: 'audio/wav', flac: 'audio/flac', ogg: 'audio/ogg', aac: 'audio/aac',
    m4a: 'audio/mp4', opus: 'audio/opus',
    txt: 'text/plain', md: 'text/markdown', json: 'application/json', csv: 'text/csv',
    html: 'text/html', htm: 'text/html', css: 'text/css', js: 'text/javascript',
  };
  return map[ext] || 'application/octet-stream';
}

function servePeerDriveStream(request) {
  return new Promise((resolve) => {
    const url = new URL(request.url);
    // /peerdrive/swdrive/<hash> 或 /swdrive/<hash>：取最后一段（去掉路径前缀）
    const seg = url.pathname.split('/').filter(Boolean);
    const hash = decodeURIComponent(seg[seg.length - 1] || '');
    const name = url.searchParams.get('name') || '';
    const sizeParam = parseInt(url.searchParams.get('size') || '', 10);
    const total = Number.isFinite(sizeParam) && sizeParam > 0 ? sizeParam : null;

    // Range 解析（支持播放器 seek）：bytes=a-b / bytes=a- / bytes=-n
    const range = parseRangeHeader(request.headers.get('Range'));
    let offset = 0;
    let reqLen = null; // 请求段长度（null=全量）
    let status = 200;
    if (range && (range.start != null || range.end != null)) {
      status = 206;
      if (range.start != null) {
        offset = range.start;
      } else if (total != null && range.end != null) {
        offset = Math.max(0, total - range.end); // 后缀段
      }
      let end = null;
      if (range.end != null) end = range.end;
      else if (total != null && range.start != null) end = total - 1;
      if (end != null && offset <= end) reqLen = end - offset + 1;
    }
    const peerSize = reqLen != null ? reqLen : -1; // 向页面要的字节数（-1=到末尾）

    self.clients.matchAll({ includeUncontrolled: true }).then((clients) => {
      if (!clients.length) {
        resolve(new Response('no peer connection', { status: 502 }));
        return;
      }
      const chan = new MessageChannel();
      let ctrl = null;
      const stream = new ReadableStream({
        start(c) { ctrl = c; },
      });
      chan.port1.onmessage = (ev) => {
        const m = ev.data;
        try {
          if (m.error) ctrl.error(new Error(m.error));
          else if (m.done) ctrl.close();
          else if (m.chunk) ctrl.enqueue(new Uint8Array(m.chunk));
        } catch (e) { /* stream 已关闭 */ }
      };
      // 只发给第一个 client（当前持有 PeerJS 连接的页面）
      clients[0].postMessage(
        { type: 'pd-fetch', hash, name, offset, size: peerSize, port: chan.port2 },
        [chan.port2]
      );

      const headers = { 'Accept-Ranges': 'bytes', 'Content-Type': guessMimeSw(name) };
      if (status === 206 && total != null) {
        const end = reqLen != null ? offset + reqLen - 1 : total - 1;
        headers['Content-Range'] = 'bytes ' + offset + '-' + end + '/' + total;
        if (reqLen != null) headers['Content-Length'] = String(reqLen);
      } else if (status === 206) {
        headers['Content-Range'] = 'bytes ' + offset + '-' + (reqLen != null ? offset + reqLen - 1 : '*') + '/' + '*';
      } else if (total != null) {
        headers['Content-Length'] = String(total); // 全量 + 真实长度：播放器才知道可 seek
      }
      resolve(new Response(stream, { status, headers }));
    });
  });
}
