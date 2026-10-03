// API request module: encapsulates all communication with the backend and the
// local storage configuration.
// Migration note (2026-08-17): backend communication now goes through the
// local WS session /ws/peer admin frames (ws.js); it no longer fetches HTTP
// directly — the management plane is exposed only to local WS (WebRTC/peerjs
// does not implement management verbs, preventing privilege-plane bugs). HTTP
// endpoints keep their original paths as legacy (for backward compatibility
// with old clients/curl/integration tests, see the marker in
// back/internal/router/router.go).
// Callers (pages) are unaffected: the request() signature is unchanged, and
// the error semantics match the fetch version (structured Error.status/
// Error.data body, 409 conflict list, etc.).
import * as ws from './ws.js';
const STORAGE_KEY = 'peerdrive_api_base';
const AUTH_TOKEN_KEY = 'peerdrive_auth_token';
// Default backend address: prefer the build-time injected VITE_API_BASE, only
// fall back to the default value if that's absent.
//
// Why not hard-code a bunch of addresses: the backend address is deployment
// configuration, not code. Hard-coding means
//   - changing the deployment requires modifying source and rebuilding;
//   - the repo keeps a plaintext http public IP long-term (browsers block
//     mixed content on https pages directly, so that default backend was
//     never actually usable).
// Supply VITE_API_BASE at build time (or ship a .env.production); the
// settings page can still change it at runtime.
const DEFAULT_API = (typeof import.meta !== 'undefined' && import.meta.env && import.meta.env.VITE_API_BASE) || 'https://wsl-3000.moonchan.xyz';

/* ---- Multi-backend management ---- */
const BACKENDS_KEY = 'peerdrive_backends';
const CURRENT_BACKEND_KEY = 'peerdrive_current_backend_id';

// Preset backend list (only written when there is no local record; afterwards
// localStorage is authoritative).
// Only one is kept: the plaintext http public IP has been removed — it was
// being blocked by browsers as mixed content on https pages, and keeping it
// only made people think "it's configured but can't connect". Add more
// backends yourself on the settings page, or inject them at build time via
// VITE_BACKENDS (JSON array).
const DEFAULT_BACKENDS = [
  { id: 'wsl', name: 'WSL', url: DEFAULT_API },
];

function getBackends() {
  try {
    const raw = localStorage.getItem(BACKENDS_KEY);
    if (raw) {
      const list = JSON.parse(raw);
      if (Array.isArray(list) && list.length > 0) return list;
    }
  } catch {}
  // Initialize the default backend
  setBackends(DEFAULT_BACKENDS);
  return DEFAULT_BACKENDS;
}

function setBackends(list) {
  localStorage.setItem(BACKENDS_KEY, JSON.stringify(list));
}

function getCurrentBackendId() {
  return localStorage.getItem(CURRENT_BACKEND_KEY) || 'wsl';
}

function setCurrentBackendId(id) {
  localStorage.setItem(CURRENT_BACKEND_KEY, id);
}

function switchBackend(id) {
  const backends = getBackends();
  const target = backends.find(b => b.id === id);
  if (!target) return false;
  setCurrentBackendId(id);
  localStorage.setItem(STORAGE_KEY, target.url);
  // Migration note (2026-08-20): this used to sync STUN/TURN to global
  // localStorage — removed together with the dead STUN/TURN config: no
  // consumer of RTCPeerConnection/iceServers exists anywhere in the frontend
  // (the browser main app only uses the /ws/peer WS session; the
  // peerdrive-media standalone package has its own empty iceServers default,
  // see its core.js), so these settings formed a "written by settings page →
  // read by settings page" closed loop.
  return true;
}

// Read a field of the current backend, falling back to the global value or
// default
function getBackendField(id, key, fallback) {
  const backends = getBackends();
  const target = backends.find(b => b.id === id);
  if (target && target[key] !== undefined) return target[key];
  return fallback;
}

// Update a field of the current backend and persist it
function updateBackendField(id, key, value) {
  const backends = getBackends();
  const idx = backends.findIndex(b => b.id === id);
  if (idx === -1) return false;
  backends[idx] = { ...backends[idx], [key]: value };
  setBackends(backends);
  return true;
}

function addBackend(name, url) {
  const backends = getBackends();
  const id = name.toLowerCase().replace(/[^a-z0-9]+/g, '-') + '-' + Date.now();
  backends.push({ id, name, url });
  setBackends(backends);
  return id;
}

function removeBackend(id) {
  let backends = getBackends();
  const defIds = DEFAULT_BACKENDS.map(b => b.id);
  if (defIds.includes(id)) return false; // default backends cannot be removed
  backends = backends.filter(b => b.id !== id);
  setBackends(backends);
  if (getCurrentBackendId() === id) {
    // Switch back to the first available backend
    if (backends.length > 0) {
      switchBackend(backends[0].id);
    }
  }
  return true;
}

function updateBackend(id, fields) {
  const backends = getBackends();
  const idx = backends.findIndex(b => b.id === id);
  if (idx === -1) return false;
  backends[idx] = { ...backends[idx], ...fields };
  setBackends(backends);
  if (fields.url && getCurrentBackendId() === id) {
    localStorage.setItem(STORAGE_KEY, backends[idx].url);
  }
  return true;
}

// Get the API base address (read from localStorage)
function getApiBase() {
  return localStorage.getItem(STORAGE_KEY) || DEFAULT_API;
}

// When user sets API endpoint like "http://host:3000#token123"
// Extract the token from the URL fragment and store separately.
// The stored base URL is always the clean URL without fragment.
function setApiBase(url) {
  const hashIdx = url.indexOf('#');
  if (hashIdx >= 0) {
    const token = url.slice(hashIdx + 1);
    const baseUrl = url.slice(0, hashIdx);
    localStorage.setItem(STORAGE_KEY, baseUrl);
    localStorage.setItem(AUTH_TOKEN_KEY, token);
  } else {
    localStorage.setItem(STORAGE_KEY, url);
  }
}

// Returns the auth token from URL fragment or settings page, or empty string.
// URL fragment token (peerdrive_auth_token) is always sent when present.
// Legacy settings token (peerdrive_auth_key) requires the toggle to be enabled.
function getAuthToken() {
  const fragmentToken = localStorage.getItem(AUTH_TOKEN_KEY);
  if (fragmentToken) return fragmentToken;
  if (localStorage.getItem('peerdrive_auth_header_enabled') === 'true') {
    return localStorage.getItem('peerdrive_auth_key') || '';
  }
  return '';
}

// Generic request wrapper: goes through the local WS session admin frames
// (ws.js internally forwards to the gin engine).
// Semantics are identical to the old fetch version: status>=400 → Error
// (err.status/err.data) (structured error bodies like the 409 conflict list,
// see the ws.js handleText admin-resp branch).
async function request(method, path, body = null) {
  return ws.admin(method, path, body);
}

/* ---- file ---- */
export const verifyFile = (hash) => request('GET', `/files/verify/${hash}`);
// downloadFile pulls sha256 content via the WS req verb (ws.js download,
// returns Uint8Array).
// The old getDownloadUrl(hash), which returned an HTTP URL, is deprecated
// (HTTP is legacy); callers must use this function or downloadFileToDisk.
export const downloadFile = (hash) => ws.download(hash);
export const downloadFileToDisk = (hash, filename) => ws.downloadToFile(hash, filename);

// getBlobUrl pulls the file via WS → objectURL (for image/video/PDF preview),
// with caching.
// The old approach used <img src={getDownloadUrl(hash)}> over HTTP (legacy);
// after the migration preview resources also go over WS. The objectURL
// lifetime is revoked by the caller (or cleaned up on page unload).
// Memory-leak defense (discovery background: code review 2026-08-18 —
// blobUrlCache only grew and never shrank, with each distinct hash's preview
// occupying a Blob in memory + an objectURL, accumulating over long browsing):
// LRU cap + revoke-on-eviction. Map iteration order = insertion order, so
// re-reading via delete+set refreshes the position.
const BLOB_URL_CACHE_MAX = 50;
// Large-file preview guard (discovery background: code review 2026-08-18 —
// download assembled everything in memory, so very large file previews would
// OOM). Above the threshold, preview is refused and throws err.code==='TOO_LARGE';
// callers should prompt the user to use downloadFileToDisk for streaming save.
const BLOB_URL_MAX_BYTES = 200 * 1024 * 1024;
const blobUrlCache = new Map();
// Concurrency deduplication (discovery background: code review 2026-08-18 —
// two concurrent getBlobUrl calls for the same hash issued two WS downloads,
// the later-completing one overwrote the earlier cache entry, and the
// earlier's objectURL leaked permanently while wasting bandwidth).
// blobUrlInflight holds in-flight Promises; a hit shares the same download;
// on failure it's removed from the map after settling, so the next call
// naturally retries.
const blobUrlInflight = new Map();
export async function getBlobUrl(hash, mime = '') {
  if (blobUrlCache.has(hash)) {
    const url = blobUrlCache.get(hash);
    blobUrlCache.delete(hash); // re-read → refresh LRU position (set moves it to the end)
    blobUrlCache.set(hash, url);
    return url;
  }
  if (blobUrlInflight.has(hash)) return blobUrlInflight.get(hash);
  const p = (async () => {
    // stat only probes the size without sending data (req offset=0 size=0 → meta{total})
    const total = await ws.stat(hash);
    if (total > BLOB_URL_MAX_BYTES) {
      const err = new Error(`file too large for preview (>${BLOB_URL_MAX_BYTES / 1024 / 1024}MB)`);
      err.code = 'TOO_LARGE';
      throw err;
    }
    const data = await ws.download(hash);
    const blob = new Blob([data], mime ? { type: mime } : undefined);
    const url = URL.createObjectURL(blob);
    blobUrlCache.set(hash, url);
    if (blobUrlCache.size > BLOB_URL_CACHE_MAX) {
      // Evict the least recently used: what's currently on screen has
      // necessarily been read recently (position toward the back), so the
      // oldest entry is most likely out of view — revoke its objectURL to
      // release Blob memory.
      const oldest = blobUrlCache.keys().next().value;
      URL.revokeObjectURL(blobUrlCache.get(oldest));
      blobUrlCache.delete(oldest);
    }
    return url;
  })().finally(() => blobUrlInflight.delete(hash));
  blobUrlInflight.set(hash, p);
  return p;
}
export const revokeBlobUrl = (hash) => {
  const url = blobUrlCache.get(hash);
  if (url) {
    URL.revokeObjectURL(url);
    blobUrlCache.delete(hash);
  }
};
export const registerLocalFile = (path, filename) =>
  request('POST', '/collections/register-local', { path, filename: filename || path.split('/').pop() });
export const registerURL = async (url, filename = '') => {
  // Backend RegisterURL returns {hash,size,mime,filename} (back file.go:120); the field is mime, not mime_type;
  // RegisterLocalFile only returns {hash,filename}. Uniformly backfill mime_type/size for backward compatibility with old callers.
  const res = await request('POST', '/collections/register-url', { url, filename });
  return { ...res, mime_type: res.mime_type || res.mime || '', size: res.size || 0 };
};
export const registerFolder = (folderPath) =>
  request('POST', '/collections/register-folder', { folder_path: folderPath });

/* ---- file system browse ---- */
export const browseDir = (dirPath = '/') =>
  request('GET', `/files/browse?path=${encodeURIComponent(dirPath)}`);

/* ---- anon collections ---- */
// createAnonCollection: visibility/access_list correspond to the permission fields of the backend model.AnonCollection.
// Pitfall: access_list is an array of account names, NOT the hashes produced by /access/list — the old version
// used to pass access_list_hash, and the backend's CreateAnonCollection only reads the access_list field, causing
// the "restricted only" collection to be created with an empty list → the backend returns 400 directly.
export const createAnonCollection = (entries, friendly_name = '', tags = [], visibility = '', access_list = []) => {
  const normalized = entries.map(e => ({
    path: e.path,
    providers: e.providers || [{ type: "sha256", value: e.hash, mime_type: e.mime_type || '' }],
  }));
  return request('POST', '/collections', { entries: normalized, friendly_name, tags, visibility, access_list });
};
export const getAnonCollection = (hash) => request('GET', `/collections/${hash}`);

// The three visibility options: constants are defined in src/constants.js
// (components cannot take constants from this module —
// tests/setup.js does a full automock of this module, and non-function exports
// get lost; see the comments in constants.js).
// This only forwards them, so the old `api.VISIBILITY` usage style keeps working.
export { VISIBILITY, VISIBILITY_PUBLIC, VISIBILITY_RESTRICTED, VISIBILITY_PRIVATE } from './constants.js';

// Switch the permission level of an existing collection.
// Pitfall: collections are content-addressed; changing the permission writes a
// new JSON → a new hash is returned, and the old hash is still the old
// permission's snapshot.
// Callers must use res.hash as the collection's new identity; do not keep
// sharing with the old hash.
export const setAnonCollectionVisibility = (hash, visibility, access_list = []) =>
  request('PUT', `/anon/collections/${encodeURIComponent(hash)}/visibility`, { visibility, access_list });

// Account directory: prefer the regserver proxy endpoint /reg/users (includes
// groups at /reg/groups).
// Background: the account directory belongs to the "registration service"
// module, which has no frontend landing page yet, so we cannot throw here —
// callers get an empty array and fall back to manual @id entry (AccountPicker
// has this fallback path built in).
export const listKnownAccounts = async () => {
  try {
    const res = await request('GET', '/reg/users');
    const list = Array.isArray(res) ? res : (res?.users || []);
    if (list.length > 0) return list;
  } catch {}
  return [];
};

// listKnownGroups: groups are used for "quick share to the whole group"; the
// service may also be absent → empty array.
export const listKnownGroups = async () => {
  try {
    const res = await request('GET', '/reg/groups');
    const list = Array.isArray(res) ? res : (res?.groups || []);
    if (list.length > 0) return list;
  } catch {}
  return [];
};
// Download URL virtual paths must be encodeURIComponent'd segment by segment
// (filenames may contain spaces/#/? etc.; not encoding breaks the URL; the
// backend gin *filepath already decodes URL.Path, so the encoded form still
// compares correctly on the server side)
const encodePath = (p) => (p || '').split('/').map(encodeURIComponent).join('/');
// downloadAnonFile pulls a file inside a collection via WS admin GET (backend
// returns a file stream → admin-bin binary frame). The old getAnonFileDownloadUrl(hash, p), which returned an HTTP URL, is deprecated.
export const downloadAnonFile = (hash, p) =>
  ws.admin('GET', `/collections/${encodeURIComponent(hash)}/${encodePath(p)}`);

/* ---- user collections ---- */
export const createUserCollection = (username, collection_name, visibility = 'public', tags = []) =>
  request('POST', '/collections', { username, collection_name, visibility, tags });
export const getUserCollections = (username) =>
  request('GET', `/collections/${username}`);
export const getUserCollection = (username, coll) =>
  request('GET', `/collections/${username}/${coll}`);
export const addCollectionEntry = (username, coll, path, hash) =>
  request('POST', `/collections/${username}/${coll}/entries`, { path, hash });
export const removeCollectionEntry = (username, coll, path) =>
  request('DELETE', `/collections/${username}/${coll}/entries/${encodePath(path)}`);
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
// downloadUserFile pulls a file inside a user collection via WS admin GET
// (same as downloadAnonFile).
// The old getUserFileDownloadUrl(username, coll, filepath), which returned an HTTP URL, is deprecated.
export const downloadUserFile = (username, coll, filepath) =>
  ws.admin('GET', `/${encodeURIComponent(username)}/${encodeURIComponent(coll)}/${encodePath(filepath)}`);

// Note: the old libp2p dual-stack panel (P2PPanel/P2PDashboard/P2PTopology/
// DHTExplorer dual-stack queries)
// and its api exports (getP2PStatus/getP2PPeers/dualAnnounce/dualFind, etc.)
// were cleaned up on 2026-08-19 along with the libp2p endpoint deletion —
// the backend /p2p/* now only has forward/auth-status/webrtc-info.

/* ---- P2P BT ---- */
// PeerJS node status (GET /peerjs/node, replaces the deleted /p2p/status for
// the frontend panel as of 2026-08-19).
export const getPeerjsNode = () => request('GET', '/peerjs/node');

/* ---- Node market / my nodes / peer nodes (netdisk targets M1-M3, see doc/NETDISK.md)----
 * The information architecture aligns with a normal netdisk: "my nodes /
 * other people's nodes / nodes joined from the market".
 * All requests go through the admin frames of /ws/peer (consistent with the
 * other APIs); backend routes are in
 * back/internal/router/peerjs_routes.go and /p2p/pull* in router.go.
 */
// Market list: nodes online at the discovery server ∪ the local joined list
// (offline ones are kept, they don't "disappear").
export const getNodeMarket = () => request('GET', '/peerjs/nodes');
// Nodes I have joined (used by the list page; compared with the market list,
// only entries with joined=true are kept).
export const getJoinedNodes = () => request('GET', '/peerjs/nodes/joined');
// Join/leave. Joining is persisted and makes this node auto-connect to it
// after a signaling reconnection.
export const joinNode = (peer) => request('POST', '/peerjs/nodes/join', { peer });
export const leaveNode = (peer) =>
  request('DELETE', `/peerjs/nodes/join?peer=${encodeURIComponent(peer)}`);
// Peer node's share list: packaged collections (with entries) and standalone
// files — i.e. the "file links" list.
// If not directly connected, the backend actively dials and waits a moment
// (see controller.GetPeerShares).
export const getPeerShares = (peer) =>
  request('GET', `/peerjs/nodes/${encodeURIComponent(peer)}/shares`);

/* ---- This node's share scope (doc/NETDISK.md M2.6)----
 * "What content am I willing to give out" is the operator's runtime choice:
 * a whole directory / a single file / a collection,
 * and the three sources are unioned. Changing it doesn't require restarting
 * the node (the backend persists it to storage/share_scope.json).
 * Note the difference from getPeerShares: that asks **the peer** what they
 * share; this manages **your own**.
 */
// Current scope + optional file list (each line carries shared / by_dir, so
// the frontend can render checkboxes directly).
export const getShareScope = () => request('GET', '/peerjs/share');
// Partial update: only pass the fields to change (enable / dirs / files /
// collections / friends);
// unchanged fields keep their values. Entries look like {id, level} (strings
// are also accepted, with the level treated as public).
export const setShareScope = (patch) => request('PUT', '/peerjs/share', patch || {});
// Check/uncheck several files (by hash), with an optional level:
//   public   listed in the share list, anyone can download
//   unlisted not listed, but anyone who knows the hash can download
//   private  not listed, only yourself and friends can download
// If level is omitted, the backend keeps the level that hash already has
// (or public if it has none).
export const setFilesShared = (hashes, shared = true, level = '') =>
  request('POST', '/peerjs/share/files', { hashes, shared, level });
// Cross-node pull and save (server-side task style, with progress/cancel).
export const getPullJobs = () => request('GET', '/p2p/pull');
export const startPull = (peer, hash, name = '', path = '') =>
  request('POST', '/p2p/pull', { peer, hash, name, path });
export const startPullCollection = (peer, collection) =>
  request('POST', '/p2p/pull/collection', { peer, collection });
export const cancelPull = (id) => request('POST', '/p2p/pull/cancel', { id });

export const getBTStatus = () => request('GET', '/bt/status');
export const btAnnounce = (hash) => request('POST', '/bt/announce', { hash });
export const btFind = (hash) => request('POST', '/bt/find', { hash });

/* ---- BT Controller (download management) ---- */
export const btGetDownloads = () => request('GET', '/bt/downloads');
export const btGetDownload = (infohash) => request('GET', `/bt/download/${infohash}`);
export const btMagnetResolve = (uri) => request('POST', '/bt/magnet', { uri });
export const btTorrentUpload = (file) =>
  // admin binary upload: /bt/torrent + multipart field name "torrent"
  // (the backend BTTorrentUpload reads FormFile("torrent"), which differs from "file" in /files/upload)
  ws.upload(file, file?.name, 'torrent', '/bt/torrent');
export const btRemoveDownload = (infohash) => request('DELETE', `/bt/download/${infohash}`);
export const btPauseDownload = (infohash) => request('POST', `/bt/download/${infohash}/pause`);
export const btResumeDownload = (infohash) => request('POST', `/bt/download/${infohash}/resume`);
export const btSeedDownload = (infohash) => request('POST', `/bt/download/${infohash}/seed`);
export const btStopSeed = (infohash) => request('POST', `/bt/download/${infohash}/unseed`);
export const btGetMagnetUri = (infohash) => request('GET', `/bt/download/${infohash}/magnet`);
// downloadTorrentFile pulls a .torrent file via WS admin GET (binary response
// → admin-bin). The old btGetTorrentUrl(infohash), which returned an HTTP URL, is deprecated.
export const downloadTorrentFile = (infohash) =>
  ws.admin('GET', `/bt/download/${infohash}/torrent`);
export const btSeedCollection = (collectionHash) => request('POST', '/bt/seed-collection', { collection_hash: collectionHash });

/* ---- anon collection commit ---- */
export const listAnonCollections = () => request('GET', '/anon/collections');

/* ---- search ---- */
export const searchCollections = (q) =>
  request('GET', `/collections/search?q=${encodeURIComponent(q)}`);

export const listPublicCollections = (q = '') =>
  request('GET', `/collections/public${q ? '?q=' + encodeURIComponent(q) : ''}`);

/* ---- file upload/delete ---- */
// uploadFile uses admin binary chunked upload (ws.upload, multipart field
// "file").
export const uploadFile = (file) => ws.upload(file, file?.name, 'file');
export const deleteFile = (hash) => request('DELETE', `/files/${hash}`);

/* ---- health ---- */
export const ping = () => request('GET', '/ping');

/* ---- settings ---- */
export { getApiBase, setApiBase, getAuthToken, DEFAULT_API };
export { getBackends, setBackends, getCurrentBackendId, setCurrentBackendId, switchBackend, addBackend, removeBackend, updateBackend, getBackendField, updateBackendField, DEFAULT_BACKENDS };

/* ---- llm ---- */
const LLM_ENDPOINT_KEY = 'peerdrive_llm_endpoint';
const LLM_MODEL_KEY = 'peerdrive_llm_model';
const LLM_APIKEY_KEY = 'peerdrive_llm_apikey';
const LLM_BODY_KEY = 'peerdrive_llm_body_template';
const DATA_CONSENT_KEY = 'peerdrive_data_consent';

const DEFAULT_LLM_ENDPOINT = 'https://siliconflow.moonchan.xyz';
const DEFAULT_LLM_MODEL = 'Qwen/Qwen3-8B';
const DEFAULT_LLM_BODY = JSON.stringify({
  model: 'Qwen/Qwen3-8B',
  messages: [],
  stream: true,
  max_tokens: 4096,
  temperature: 0.7,
}, null, 2);

export function getLlmEndpoint() { return localStorage.getItem(LLM_ENDPOINT_KEY) || DEFAULT_LLM_ENDPOINT; }
export function setLlmEndpoint(v) { localStorage.setItem(LLM_ENDPOINT_KEY, v); }
export function getLlmModel() { return localStorage.getItem(LLM_MODEL_KEY) || DEFAULT_LLM_MODEL; }
export function setLlmModel(v) { localStorage.setItem(LLM_MODEL_KEY, v); }
export function getLlmApiKey() { return localStorage.getItem(LLM_APIKEY_KEY) || ''; }
export function setLlmApiKey(v) { localStorage.setItem(LLM_APIKEY_KEY, v); }
export function getLlmBodyTemplate() { return localStorage.getItem(LLM_BODY_KEY) || DEFAULT_LLM_BODY; }
export function setLlmBodyTemplate(v) { localStorage.setItem(LLM_BODY_KEY, v); }
export function getDataConsent() { return localStorage.getItem(DATA_CONSENT_KEY) === 'true'; }
export function setDataConsent(v) { localStorage.setItem(DATA_CONSENT_KEY, v ? 'true' : 'false'); }

/* ---- auth header toggle ---- */
const AUTH_HEADER_KEY = 'peerdrive_auth_header_enabled';
export function getAuthHeaderEnabled() { return localStorage.getItem(AUTH_HEADER_KEY) === 'true'; }
export function setAuthHeaderEnabled(v) { localStorage.setItem(AUTH_HEADER_KEY, v ? 'true' : 'false'); }

/* ---- follow redirects ---- */
const FOLLOW_REDIRECTS_KEY = 'peerdrive_follow_redirects';
export function getFollowRedirects() { return localStorage.getItem(FOLLOW_REDIRECTS_KEY) !== 'false'; }
export function setFollowRedirects(v) { localStorage.setItem(FOLLOW_REDIRECTS_KEY, v ? 'true' : 'false'); }

/* ---- p2p network config (removed entirely, 2026-08-20)----
 * bootstrapPeer / relayServer / stunUrl / turnUrl / turnCredential were all
 * a dead closed loop of "settings page writes to localStorage → settings
 * page reads it back for display", with no functional consumer:
 * - bootstrap peer: a libp2p concept; the backend PEERDRIVE_BOOTSTRAP_PEER
 *   was deleted along with the libp2p stack (doc/archive/LEGACY.md §C)
 * - relay: the relay service was removed from the backend (doc/archive/LEGACY.md §A), the badge is always false
 * - STUN/TURN: the browser main app doesn't create RTCPeerConnection (all
 *   communication goes over the /ws/peer WS session); the peerdrive-media
 *   standalone package defaults to empty iceServers (for browser↔Node
 *   intranet scenarios, host candidates suffice). If cross-network
 *   punching is done in the future, explicitly pass iceServers in the media
 *   package's signaling.config rather than reviving this settings group.
 */

/* ---- ipfs gateway config (frontend-only) ---- */
const IPFS_ENABLED_KEY = 'peerdrive_ipfs_enabled';

export function getIPFSEnabled() { return localStorage.getItem(IPFS_ENABLED_KEY) !== 'false'; }

/* ---- IPFS compat layer (server-side) ---- */

// getIPFSCompatStatus queries the server's IPFS compatibility layer status.
export async function getIPFSCompatStatus() {
  const data = await request('GET', '/ipfs');
  return data;
}

// setIPFSCompatEnabled enables or disables IPFS compatibility mode via the
// server API.
export async function setIPFSCompatEnabled(enabled) {
  const data = await request('POST', '/ipfs/toggle', { enabled });
  return data;
}

/* ---- IPFS pin & gateway ---- */

// pinCID pins the given CID (downloads from the IPFS gateway and caches
// permanently).
export async function pinCID(cid) {
  const data = await request('POST', `/ipfs/pin/${cid}`);
  return data;
}

// unpinCID unpins the given CID.
export async function unpinCID(cid) {
  const data = await request('DELETE', `/ipfs/pin/${cid}`);
  return data;
}

// listPins lists all pinned CIDs.
export async function listPins() {
  const data = await request('GET', '/ipfs/pins');
  return data;
}

// getIPFSGatewayStatus checks the health of all IPFS gateways.
export async function getIPFSGatewayStatus() {
  const data = await request('GET', '/ipfs/gateways');
  return data;
}

const FREE_LLM_MODELS = [
  'Qwen/Qwen3-8B',
  'Qwen/Qwen3.5-4B',
  'Qwen/Qwen2.5-7B-Instruct',
  'deepseek-ai/DeepSeek-R1-0528-Qwen3-8B',
  'deepseek-ai/DeepSeek-R1-Distill-Qwen-7B',
  'deepseek-ai/DeepSeek-OCR',
  'THUDM/GLM-4-9B-0414',
  'THUDM/GLM-Z1-9B-0414',
  'THUDM/GLM-4.1V-9B-Thinking',
  'tencent/Hunyuan-MT-7B',
  'internlm/internlm2_5-7b-chat',
  'PaddlePaddle/PaddleOCR-VL',
  'PaddlePaddle/PaddleOCR-VL-1.5',
];
export { DEFAULT_LLM_ENDPOINT, DEFAULT_LLM_MODEL, DEFAULT_LLM_BODY, FREE_LLM_MODELS };

// Save data consent locally.
// Note: the old name uploadConsent is easily mistaken for uploading to the
// server, but the backend has no /consent endpoint;
// this only records it locally, avoiding misleading states like "consent record already uploaded".
// Re-review 2026-08: the original implementation wrote to peerdrive_consent,
// a key with no reader,
// while what actually takes effect on the settings page is DATA_CONSENT_KEY
// (peerdrive_data_consent); unified to
// delegate to setDataConsent(true), avoiding a "written but can't be read"
// divergence in consent state.
export async function saveConsentLocal() {
  setDataConsent(true);
}

/* ---- alias exports for legacy usage ---- */
// Unified Collection API (replaces the old createUserCollection/
// createAnonCollection, etc.)
// Everything is a collection; an entry's provider can be sha256 or url.
// Note: the anonymous dispatcher (dispatchCreateCollection) decides based on
// whether the body has a username whether to go through the
// user system; anonymous creation uses the field friendly_name, not name
// (discovery background: re-review 2026-08
// found that using name in this alias caused the backend to receive an empty
// friendly_name on anonymous creation, losing the collection name).
export const createCollection = (entries, name = '', tags = []) =>
  request('POST', '/collections', { friendly_name: name, entries, tags });
export const listCollections = () => request('GET', '/collections');
export const getCollection = (id) => request('GET', `/collections/${id}`);
export const addEntry = addCollectionEntry;
export const deleteEntry = removeCollectionEntry;
export const commitVersion = commitCollection;
export const downloadFileByPath = downloadUserFile;
export const mergeCollection = mergeUserCollection;

/* ---- access list ---- */
export const createAccessList = (users, groups) =>
  request('POST', '/access/list', { users: users || [], groups: groups || [] });
export const getAccessList = (hash) => request('GET', `/access/list/${hash}`);

/* ---- regserver proxy ---- */
export const listRegUsers = () => request('GET', '/reg/users');
export const listRegGroups = () => request('GET', '/reg/groups');
export const getGroupMembers = (name) => request('GET', `/reg/groups/${encodeURIComponent(name)}/members`);

/* ---- local sync ---- */
export const saveLocal = (body) => request('POST', '/local/save', body);

export const getLocalStatus = (hash) => request('GET', `/local/status/${hash}`);

export const listFiles = (sort = 'time') => request('GET', `/files?sort=${sort}`);

/* ---- auth status ---- */
export const getAuthStatus = () => request('GET', '/p2p/auth/status');

/* ---- group management (registration server) ---- */
export async function getUserGroups(regServerUrl, username, token) {
  const res = await fetch(`${regServerUrl}/auth/group/${encodeURIComponent(username)}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function addUserToGroup(regServerUrl, username, groupName, token) {
  const res = await fetch(`${regServerUrl}/auth/group/${encodeURIComponent(username)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ group_name: groupName }),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

/* ---- comments ---- */
export async function getComments(regServerUrl, hash) {
  const res = await fetch(`${regServerUrl}/comments/${encodeURIComponent(hash)}`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function postComment(regServerUrl, hash, content, token) {
  const res = await fetch(`${regServerUrl}/comments/${encodeURIComponent(hash)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ content }),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

const REG_SERVER_KEY = 'peerdrive_reg_server_url';

export function setRegServerUrl(url) {
  localStorage.setItem(REG_SERVER_KEY, url);
}

export const getBEP51Sample = () => request('GET', '/bt/bep51/sample');