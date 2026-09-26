// swBridge —— Service Worker 桥（断点续传）
// ① 注册 service-worker.js（伪造 fetch /swdrive/ 用）
// ② 监听 SW 的 pd-fetch 消息：SW 请求 hash 的某段流（Range）→ 用当前 PeerJS
//    client.stream(hash, {offset, size}) 逐块回传，实现大文件断点续传式预览。
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
    // SW 已接管（controller 非空）则本页可拦截 /swdrive，直接用
    if (navigator.serviceWorker.controller) return;
    // 首次注册：本页还没被 SW 控制（controller=null），/swdrive 会 404 → 刷新一次让 SW 接管
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

// 当前页是否已被 SW 控制（决定 /swdrive 是否会被拦截）
export function swControlled() {
  return typeof navigator !== 'undefined' && !!navigator.serviceWorker?.controller;
}

// message 监听要尽早注册（即使 SW 尚未成为 controller），否则 SW 问流时收不到
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