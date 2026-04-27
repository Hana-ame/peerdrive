/* Peerdrive Service Worker — PWA offline & background sync */

const CACHE_NAME = 'peerdrive-v1';
const STATIC_ASSETS = [
  '/',
  '/index.html',
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
