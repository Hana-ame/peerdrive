// Connected peer node session (shared across pages: connect page → node control page)
// Only stores references; cleared when the connection is broken/invalid.
let session = null; // { client, peerId }

export function setNodeSession(s) {
  session = s;
}

export function getNodeSession() {
  return session;
}

export function clearNodeSession() {
  if (session && session.client && typeof session.client.close === 'function') {
    try { session.client.close(); } catch (e) { /* ignore */ }
  }
  session = null;
}