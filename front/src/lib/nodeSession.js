// 已连接的对端节点会话（跨页面共享：连接页 → 节点控制页）
// 只存引用；连接断开/失效时清空。
let session = null; // { client, peerId }

export function setNodeSession(s) {
  session = s;
}

export function getNodeSession() {
  return session;
}

export function clearNodeSession() {
  if (session && session.client && typeof session.client.close === 'function') {
    try { session.client.close(); } catch (e) { /* 忽略 */ }
  }
  session = null;
}