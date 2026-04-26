const API_BASE = 'https://wsl-3000.moonchan.xyz';

async function request(method, path, body = null) {
  const opts = { method, headers: {} };
  if (body) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(`${API_BASE}${path}`, opts);
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new Error(err.error || err.message);
  }
  return res.json();
}

/* ---- file ---- */
export const verifyFile = (hash) => request('GET', `/files/verify/${hash}`);
export const getDownloadUrl = (hash) => `${API_BASE}/sha256sum/${hash}`;
export const registerLocalFile = (path, filename) =>
  request('POST', '/files/register_local', { path, filename: filename || path.split('/').pop() });
export const registerFolder = (folderPath) =>
  request('POST', '/files/register_folder', { folder_path: folderPath });

/* ---- anon collections ---- */
export const createAnonCollection = (entries, friendly_name = '') =>
  request('POST', '/anon/collections', { entries, friendly_name });
export const getAnonCollection = (hash) => request('GET', `/anon/collections/${hash}`);
export const getAnonFileDownloadUrl = (hash, p) => `${API_BASE}/anon/collections/${hash}/${p}`;
export const forkAnonCollection = (source_hash, add_entries, remove_paths, friendly_name = '') =>
  request('POST', '/anon/collections/fork', { source_hash, add_entries, remove_paths, friendly_name });

/* ---- user collections ---- */
export const createUserCollection = (username, collection_name) =>
  request('POST', '/collections', { username, collection_name });
export const getUserCollections = (username) =>
  request('GET', `/collections/${username}`);
export const getUserCollection = (username, coll) =>
  request('GET', `/collections/${username}/${coll}`);
export const addCollectionEntry = (username, coll, path, hash) =>
  request('POST', `/collections/${username}/${coll}/entries`, { path, hash });
export const removeCollectionEntry = (username, coll, path) =>
  request('DELETE', `/collections/${username}/${coll}/entries/${path}`);
export const commitCollection = (username, coll, commit_message = '') =>
  request('POST', `/collections/${username}/${coll}/commit`, { commit_message });
export const getVersionLog = (username, coll) =>
  request('GET', `/collections/${username}/${coll}/log`);
export const rollbackVersion = (username, coll, vid) =>
  request('POST', `/collections/${username}/${coll}/rollback/${vid}`);
export const forkUserCollection = (username, source_username, coll, source_coll) =>
  request('POST', '/actions/fork', { username, source_username, collection_name: coll, source_coll_name: source_coll });
export const mergeUserCollection = (username, source_username, coll, source_coll, strategy = 'ours') =>
  request('POST', '/actions/merge', { username, source_username, collection_name: coll, source_coll_name: source_coll, strategy });
export const pullUserCollection = (username, coll) =>
  request('POST', '/actions/pull', { username, collection_name: coll });
export const getUserFileDownloadUrl = (username, coll, filepath) =>
  `${API_BASE}/${username}/${coll}/${filepath}`;

/* ---- P2P ---- */
export const getP2PStatus = () => request('GET', '/p2p/status');
export const getP2PNode = () => request('GET', '/p2p/node');
export const getP2PPeers = () => request('GET', '/p2p/peers');
export const getP2PDiscovered = () => request('GET', '/p2p/discovered');
export const pingPeer = (peerId) => request('GET', `/p2p/ping/${peerId}`);
export const connectPeer = (peerId, addrs) =>
  request('POST', '/p2p/connect', { peer_id: peerId, addrs });
export const p2pAnnounce = (hash) => request('POST', '/p2p/announce', { hash });
export const p2pFetch = (peerId, hash) => request('POST', '/p2p/fetch', { peer_id: peerId, hash });
export const p2pSync = (peerId, collectionName) =>
  request('POST', '/p2p/sync', { peer_id: peerId, collection_name: collectionName });
export const p2pPush = (peerId, collectionName) =>
  request('POST', '/p2p/push', { peer_id: peerId, collection_name: collectionName });
export const p2pRequestFile = (hash) => request('POST', '/p2p/request-file', { hash });
export const getWSInfo = () => request('GET', '/p2p/ws/info');

export const WS_TRANSFER_URL = API_BASE.replace(/^http/, 'ws') + '/ws/transfer';

/* ---- anon collection commit ---- */
export const commitAnonCollection = (source_hash, entries, commit_message = '') =>
  request('POST', '/anon/collections/commit', { source_hash, entries, commit_message });

/* ---- search ---- */
export const searchCollections = (q) =>
  request('GET', `/collections/search?q=${encodeURIComponent(q)}`);

/* ---- file upload/delete ---- */
export const uploadFile = (file) => {
  const fd = new FormData();
  fd.append('file', file);
  return fetch(`${API_BASE}/files/upload`, { method: 'POST', body: fd }).then(r => {
    if (!r.ok) throw new Error(`Upload failed: ${r.status}`);
    return r.json();
  });
};
export const deleteFile = (hash) => request('DELETE', `/files/${hash}`);

/* ---- task status ---- */
export const getTasks = () => request('GET', '/tasks');
export const getTaskStatus = (id) => request('GET', `/tasks/${id}`);

/* ---- health ---- */
export const ping = () => request('GET', '/ping');

/* ---- alias exports for legacy usage ---- */
export const createCollection = createUserCollection;
export const listCollections = getUserCollections;
export const getCollection = getUserCollection;
export const addEntry = addCollectionEntry;
export const deleteEntry = removeCollectionEntry;
export const commitVersion = commitCollection;
export const downloadFileByPath = getUserFileDownloadUrl;
export const mergeCollection = mergeUserCollection;
export const getNodeInfo = getP2PNode;

/* ---- local sync ---- */
export const saveLocal = (body) => request('POST', '/local/save', body);

export const getLocalStatus = (hash) => request('GET', `/local/status/${hash}`);

export const listFiles = (sort = 'time') => request('GET', `/files?sort=${sort}`);
