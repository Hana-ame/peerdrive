// swBridge — Service Worker bridge (resume-from-breakpoint)
// ① Register service-worker.js (fake fetch /swdrive/ for use)
// ② Listen for the SW's pd-fetch messages: SW requests a segment (Range) of a
//    hash → use the current PeerJS client.stream(hash, {offset, size}) to
//    send back in chunks, implementing resumable preview of large files.
import { getNodeSession } from './nodeSession';

let registered = false;
const RELOAD_FLAG = 'pd-sw-reloaded';

export async function registerSW() {
  if (!('serviceWorker' in navigator)) return;
  if (registered) return;
  registered = true;
  try {
    const base = import.meta.env.BASE_URL || '/';
    const swUrl = new URL('service-worker.js', base).toString();
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
    (async () => {
      try {
        for await (const chunk of client.stream(d.hash, { offset: d.offset, size: d.size })) {
          const buf = chunk.slice().buffer;
          port.postMessage({ chunk: buf }, [buf]);
        }
        port.postMessage({ done: true });
      } catch (e) {
        port.postMessage({ error: e?.message || String(e) });
      }
    })();
  });
}