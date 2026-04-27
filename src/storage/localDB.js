// Browser-local storage for anonymous users.
// Stores files and collections in localStorage, syncs to node when configured.

const DB_VERSION = 1;

function read(key) {
  try { return JSON.parse(localStorage.getItem(key) || 'null'); } catch { return null; }
}
function write(key, val) { localStorage.setItem(key, JSON.stringify(val)); }

// ── Files ──
// Stored as { [hash]: { hash, filename, size, mime_type, blob? (base64 for small files), savedAt } }

export function getLocalFiles() {
  const data = read('peerdrive_local_files') || {};
  return Object.values(data);
}

export function saveLocalFile(hash, meta, blobBase64) {
  const data = read('peerdrive_local_files') || {};
  data[hash] = { hash, filename: meta.filename || meta.path || '', size: meta.size || 0, mime_type: meta.mime_type || '', blob: blobBase64 || null, savedAt: new Date().toISOString() };
  write('peerdrive_local_files', data);
  return data[hash];
}

export function getLocalFile(hash) {
  const data = read('peerdrive_local_files') || {};
  return data[hash] || null;
}

export function removeLocalFile(hash) {
  const data = read('peerdrive_local_files') || {};
  delete data[hash];
  write('peerdrive_local_files', data);
}

// ── Collections ──
// Stored as { [hash]: { hash, friendly_name, entries: [{path, hash}], tags, createdAt } }

export function getLocalCollections() {
  const data = read('peerdrive_local_collections') || {};
  return Object.values(data).sort((a, b) => (b.createdAt || '').localeCompare(a.createdAt || ''));
}

export function saveLocalCollection(collection) {
  const data = read('peerdrive_local_collections') || {};
  const hash = collection.hash || collection._hash || ('local-' + Date.now());
  data[hash] = {
    hash,
    friendly_name: collection.friendly_name || collection.name_preview || '',
    entries: collection.entries || [],
    tags: collection.tags || [],
    createdAt: collection.created_at || new Date().toISOString(),
    _local: true,
  };
  write('peerdrive_local_collections', data);
  return data[hash];
}

export function getLocalCollection(hash) {
  const data = read('peerdrive_local_collections') || {};
  return data[hash] || null;
}

export function removeLocalCollection(hash) {
  const data = read('peerdrive_local_collections') || {};
  delete data[hash];
  write('peerdrive_local_collections', data);
}

// ── Sync to node ──
// When user configures API endpoint, sync localStorage collections to the node

export async function syncToNode(apiBase) {
  const files = getLocalFiles();
  const collections = getLocalCollections();
  const status = { files: 0, collections: 0, errors: [] };

  for (const coll of collections) {
    try {
      const res = await fetch(`${apiBase}/anon/collections`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ entries: coll.entries, friendly_name: coll.friendly_name, tags: coll.tags }),
      });
      if (res.ok) { status.collections++; } else { status.errors.push(coll.friendly_name); }
    } catch (e) { status.errors.push(e.message); }
  }
  return status;
}

// ── Stats ──
export function getLocalStats() {
  return {
    files: Object.keys(read('peerdrive_local_files') || {}).length,
    collections: Object.keys(read('peerdrive_local_collections') || {}).length,
    totalSize: getLocalFiles().reduce((s, f) => s + (f.size || 0), 0),
  };
}
