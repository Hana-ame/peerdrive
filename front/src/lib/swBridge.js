// swBridge —— Service Worker 桥（断点续传）
// ① 注册 service-worker.js（伪造 fetch /swdrive/ 用）
// ② 监听 SW 的 pd-fetch 消息：SW 请求 hash 的某段流（Range）→ 用当前 PeerJS
//    client.stream(hash, {offset, size}) 逐块回传，实现大文件断点续传式预览。
import { getNodeSession } from './nodeSession';

let registered = false;

export async function registerSW() {
  if (!('serviceWorker' in navigator)) return;
  if (registered) return;
  registered = true;
  try {
    const base = import.meta.env.BASE_URL || '/';
    const swUrl = new URL('service-worker.js', base).toString();
    await navigator.serviceWorker.register(swUrl);
  } catch (e) {
    console.warn('SW register failed:', e);
  }
}

export function swAvailable() {
  return typeof navigator !== 'undefined' && 'serviceWorker' in navigator;
}

if (swAvailable()) {
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