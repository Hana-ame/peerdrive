// Three-tier sync: localStorage ↔ Reg Server ↔ Node
// Each tier backs up the others. Any tier online = full recovery.

import * as localDB from './localDB';

// ── Sync State ──
// Tracks last sync time for each tier pair

function syncState() {
  return JSON.parse(localStorage.getItem('peerdrive_sync_state') || '{}');
}
function updateSyncState(tier, ts) {
  const s = syncState();
  s[tier] = ts || new Date().toISOString();
  localStorage.setItem('peerdrive_sync_state', JSON.stringify(s));
}

// ── Hasher: stable ID for dedup across tiers ──
// Use SHA256-like fingerprint from entries for collection identity

function collectionFingerprint(coll) {
  const entries = (coll.entries || []).map(e => `${e.path}:${e.hash}`).sort().join(',');
  const name = coll.friendly_name || '';
  return `${name}|${entries}`;
}

// ── Tier 1: localStorage (always available) ──

async function syncFromLocal() {
  return {
    files: localDB.getLocalFiles(),
    collections: localDB.getLocalCollections(),
    source: 'localStorage',
  };
}

// ── Tier 2: Registration Server ──

async function syncFromRegServer(apiBase, authToken) {
  try {
    const res = await fetch(`${apiBase}/storage/sync`, {
      headers: { 'Authorization': `Bearer ${authToken}` },
    });
    if (!res.ok) return null;
    return await res.json();
  } catch { return null; }
}

async function pushToRegServer(apiBase, authToken, data) {
  try {
    const res = await fetch(`${apiBase}/storage/sync`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${authToken}` },
      body: JSON.stringify(data),
    });
    return res.ok;
  } catch { return false; }
}

// ── Tier 3: Local Node ──

async function syncFromNode(apiBase) {
  try {
    const [files, collections] = await Promise.all([
      fetch(`${apiBase}/files`).then(r => r.ok ? r.json() : null).catch(() => null),
      fetch(`${apiBase}/anon/collections`).then(r => r.ok ? r.json() : null).catch(() => null),
    ]);
    return { files, collections, source: 'node' };
  } catch { return null; }
}

async function pushToNode(apiBase, collections) {
  let count = 0;
  for (const coll of collections) {
    try {
      const res = await fetch(`${apiBase}/anon/collections`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ entries: coll.entries, friendly_name: coll.friendly_name, tags: coll.tags }),
      });
      if (res.ok) count++;
    } catch {}
  }
  return count;
}

// ── Merge Strategy: newest wins, no delete ──

function mergeCollections(...arrays) {
  const map = new Map();
  for (const arr of arrays) {
    if (!arr) continue;
    for (const c of arr) {
      const key = c.hash || collectionFingerprint(c);
      const existing = map.get(key);
      if (!existing || (c.createdAt || c.created_at || '') > (existing.createdAt || existing.created_at || '')) {
        map.set(key, c);
      }
    }
  }
  return Array.from(map.values());
}

// ── Three-way sync orchestration ──

export async function syncAll({ regUrl, regToken, nodeUrl }) {
  const results = [];
  let localData = await syncFromLocal();

  // Pull from reg server (if authenticated)
  if (regUrl && regToken) {
    const regData = await syncFromRegServer(regUrl, regToken);
    if (regData) {
      const merged = mergeCollections(localData.collections, regData.collections || []);
      for (const c of merged) {
        if (!localData.collections.find(l => l.hash === c.hash)) {
          localDB.saveLocalCollection(c);
        }
      }
      results.push({ tier: 'reg-server', status: 'synced', items: merged.length });
      updateSyncState('regServer');
    }
  }

  // Pull from node (if configured)
  if (nodeUrl) {
    const nodeData = await syncFromNode(nodeUrl);
    if (nodeData) {
      const merged = mergeCollections(localData.collections, nodeData.collections || []);
      for (const c of merged) {
        if (!localData.collections.find(l => l.hash === c.hash)) {
          localDB.saveLocalCollection(c);
        }
      }
      results.push({ tier: 'node', status: 'synced', items: merged.length });
      updateSyncState('node');
    }
  }

  // Push local → reg server
  if (regUrl && regToken && localData.collections.length > 0) {
    const pushed = await pushToRegServer(regUrl, regToken, { collections: localData.collections });
    if (pushed) results.push({ tier: 'local→reg', status: 'pushed' });
  }

  // Push local → node
  if (nodeUrl && localData.collections.length > 0) {
    const pushed = await pushToNode(nodeUrl, localData.collections);
    if (pushed > 0) results.push({ tier: 'local→node', status: 'pushed', count: pushed });
  }

  return results;
}

// ── Recovery: restore from any available tier ──

export async function recoverFromAny(source) {
  // Try reg server first, then node, then localStorage as last resort
  for (const tier of ['regServer', 'node', 'localStorage']) {
    if (source[tier]) {
      const data = tier === 'regServer' ? await syncFromRegServer(source.regUrl, source.regToken)
                 : tier === 'node' ? await syncFromNode(source.nodeUrl)
                 : await syncFromLocal();
      if (data && (data.collections?.length > 0 || data.files?.length > 0)) {
        return { tier, data };
      }
    }
  }
  return { tier: 'none', data: null };
}
