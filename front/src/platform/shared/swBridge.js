// swBridge — Service Worker bridge (resume-from-breakpoint)
// ① Register service-worker.js (fake fetch /swdrive/ for use)
// ② Listen for the SW's pd-fetch messages: SW requests a segment (Range) of a
//    hash → use the current PeerJS client.stream(hash, {offset, size}) to
//    send back in chunks, implementing resumable preview of large files.
import { getNodeSession } from '../../lib/nodeSession';

let registered = false;
const RELOAD_FLAG = 'pd-sw-reloaded';

export async function registerSW() {
  if (!('serviceWorker' in navigator)) return;
  if (registered) return;
  registered = true;
  try {
    const base = (import.meta.env.BASE_URL || '/').replace(/\/+$/, '') + '/';
    const origin = (typeof window !== 'undefined' && window.location?.origin) ? window.location.origin : 'http://localhost';
    const swUrl = new URL(base + 'service-worker.js', origin).href;
    const reg = await navigator.serviceWorker.register(swUrl);
    // If SW has already taken control (controller is non-null), this page can
    // intercept /swdrive directly, no reload needed
    if (navigator.serviceWorker.controller) return;
    // First registration: this page isn't yet controlled by the SW
    // (controller=null), /swdrive will 404 → reload once to let SW take over
    await navigator.serviceWorker.ready;
    if (!navigator.serviceWorker.controller) {
      if (!sessionStorage.getItem(RELOAD_FLAG)) {
        sessionStorage.setItem(RELOAD_FLAG, '1');
        window.location.reload();
      }
    }
  } catch (e) {
    console.warn('SW register failed:', e);
  }
}

// Whether the current page is controlled by the SW (determines whether
// /swdrive will be intercepted)
export function swControlled() {
  return typeof navigator !== 'undefined' && !!navigator.serviceWorker?.controller;
}

// Asynchronously wait for SW activation if not yet controlling (prevents missing streamable requests)
export async function ensureSWReady() {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return false;
  if (navigator.serviceWorker.controller) return true;
  try {
    await navigator.serviceWorker.ready;
    return !!navigator.serviceWorker.controller;
  } catch {
    return false;
  }
}

// The message listener must be registered early (even before the SW becomes the
// controller), otherwise the SW's stream requests won't be received
if (typeof navigator !== 'undefined' && 'serviceWorker' in navigator) {
  navigator.serviceWorker.addEventListener('message', (ev) => {
    const d = ev.data;
    if (!d || d.type !== 'pd-fetch') return;
    const port = ev.ports && ev.ports[0];
    if (!port) return;
    const session = getNodeSession();
    const client = session && session.client;
    if (!client) {
      port.postMessage({ error: 'no active peer connection' });
      return;
    }

    const abortCtrl = new AbortController();
    port.onmessage = (e) => {
      if (e.data?.abort) {
        abortCtrl.abort();
      }
    };

    (async () => {
      const t0 = performance.now();
      let bytesSent = 0;
      try {
        console.log(`[swBridge] Stream start: hash=${d.hash?.slice(0, 8)} offset=${d.offset} size=${d.size}`);
        for await (const chunk of client.stream(d.hash, { offset: d.offset, size: d.size, signal: abortCtrl.signal })) {
          if (abortCtrl.signal.aborted) break;
          bytesSent += chunk.byteLength;
          const buf = chunk.slice().buffer;
          port.postMessage({ chunk: buf }, [buf]);
        }
        if (!abortCtrl.signal.aborted) {
          const sec = (performance.now() - t0) / 1000;
          const mbps = sec > 0 ? (bytesSent / sec / (1024 * 1024)).toFixed(2) : '0';
          console.log(`[swBridge] Stream done: hash=${d.hash?.slice(0, 8)} sent=${bytesSent} bytes in ${sec.toFixed(2)}s (${mbps} MB/s)`);
          port.postMessage({ done: true });
        }
      } catch (e) {
        if (!abortCtrl.signal.aborted) {
          console.warn(`[swBridge] Stream error: hash=${d.hash?.slice(0, 8)}`, e);
          port.postMessage({ error: e?.message || String(e) });
        }
      }
    })();
  });
}
