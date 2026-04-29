// API 请求模块：封装与后端的所有 HTTP 通信和本地存储配置
const STORAGE_KEY = 'peerdrive_api_base';
const AUTH_TOKEN_KEY = 'peerdrive_auth_token';
const DEFAULT_API = 'https://wsl-3000.moonchan.xyz';

// 获取 API 基础地址（从 localStorage 读取）
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

function getApiBaseUrl() {
  return getApiBase();
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

// 通用 HTTP 请求封装，自动注入 Auth Token
async function request(method, path, body = null) {
  const opts = { method, headers: {} };
  const token = getAuthToken();
  if (token) opts.headers['Authorization'] = 'Bearer ' + token;
  if (body) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const url = `${getApiBase()}${path}`;
  const res = await fetch(url, opts);
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new Error(err.error || err.message);
  }
  return res.json();
}

/* ---- file ---- */
export const verifyFile = (hash) => request('GET', `/files/verify/${hash}`);
export const getDownloadUrl = (hash) => `${getApiBase()}/sha256sum/${hash}`;
export const registerLocalFile = (path, filename) =>
  request('POST', '/files/register_local', { path, filename: filename || path.split('/').pop() });
export const registerURL = (url, filename = '') =>
  request('POST', '/files/register_url', { url, filename });
export const registerFolder = (folderPath) =>
  request('POST', '/files/register_folder', { folder_path: folderPath });

/* ---- file system browse ---- */
export const browseDir = (dirPath = '/') =>
  request('GET', `/files/browse?path=${encodeURIComponent(dirPath)}`);

/* ---- anon collections ---- */
export const createAnonCollection = (entries, friendly_name = '', tags = []) => {
  const normalized = entries.map(e => ({
    path: e.path,
    providers: e.providers || [{ type: "sha256", value: e.hash, mime_type: e.mime_type || '' }],
  }));
  return request('POST', '/anon/collections', { entries: normalized, friendly_name, tags });
};
export const getAnonCollection = (hash) => request('GET', `/anon/collections/${hash}`);
export const getAnonFileDownloadUrl = (hash, p) => `${getApiBase()}/anon/collections/${hash}/${p}`;
export const forkAnonCollection = (source_hash, add_entries, remove_paths, friendly_name = '') =>
  request('POST', '/anon/collections/fork', { source_hash, add_entries, remove_paths, friendly_name });

/* ---- user collections ---- */
export const createUserCollection = (username, collection_name, visibility = 'public', tags = []) =>
  request('POST', '/collections', { username, collection_name, visibility, tags });
export const getUserCollections = (username) =>
  request('GET', `/collections/${username}`);
export const getUserCollection = (username, coll) =>
  request('GET', `/collections/${username}/${coll}`);
export const updateCollectionTags = (username, coll, tags) =>
  request('POST', `/collections/${username}/${coll}/tags`, { tags });
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
  `${getApiBase()}/${username}/${coll}/${filepath}`;

/* ---- P2P Detail + Stats ---- */
export const getPeersDetail = () => request('GET', '/p2p/peers/detail');
export const getPeerDetail = (peerId) => request('GET', `/p2p/peers/detail/${peerId}`);
export const getP2PStats = () => request('GET', '/p2p/stats');
export const getConnections = () => request('GET', '/p2p/connections');

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
export const getSignalPeers = () => request('GET', '/p2p/status').then(r => r.signal_peers || []);
export const getP2PTopology = () => request('GET', '/p2p/topology');
export const getP2PQuality = () => request('GET', '/p2p/quality');

/* ---- P2P BT ---- */
export const getBTStatus = () => request('GET', '/p2p/bt/status');
export const btAnnounce = (hash) => request('POST', '/p2p/bt/announce', { hash });
export const btFind = (hash) => request('POST', '/p2p/bt/find', { hash });

/* ---- BT Controller (download management) ---- */
export const btGetDownloads = () => request('GET', '/p2p/bt/downloads');
export const btGetDownload = (infohash) => request('GET', `/p2p/bt/download/${infohash}`);
export const btMagnetResolve = (uri) => request('POST', '/p2p/bt/magnet', { uri });
export const btTorrentUpload = (file) => {
  const fd = new FormData();
  fd.append('torrent', file);
  const token = getAuthToken();
  const headers = {};
  if (token) headers['Authorization'] = 'Bearer ' + token;
  return fetch(`${getApiBase()}/p2p/bt/torrent`, { method: 'POST', body: fd, headers }).then(r => {
    if (!r.ok) throw new Error(`Torrent upload failed: ${r.status}`);
    return r.json();
  });
};
export const btRemoveDownload = (infohash) => request('DELETE', `/p2p/bt/download/${infohash}`);
export const btPauseDownload = (infohash) => request('POST', `/p2p/bt/download/${infohash}/pause`);
export const btResumeDownload = (infohash) => request('POST', `/p2p/bt/download/${infohash}/resume`);
export const btSeedDownload = (infohash) => request('POST', `/p2p/bt/download/${infohash}/seed`);
export const btStopSeed = (infohash) => request('POST', `/p2p/bt/download/${infohash}/unseed`);
export const btGetStats = () => request('GET', '/p2p/bt/stats');

/* ---- P2P Dual ---- */
export const dualAnnounce = (hash) => request('POST', '/p2p/dual/announce', { hash });
export const dualFind = (hash) => request('POST', '/p2p/dual/find', { hash });

export { getApiBaseUrl as WS_TRANSFER_URL_BASE };
export const WS_TRANSFER_URL = getApiBase().replace(/^http/, 'ws') + '/ws/transfer';

/* ---- anon collection commit ---- */
export const commitAnonCollection = (source_hash, entries, commit_message = '') =>
  request('POST', '/anon/collections/commit', { source_hash, entries, commit_message });

export const listAnonCollections = () => request('GET', '/anon/collections');

/* ---- search ---- */
export const searchCollections = (q) =>
  request('GET', `/collections/search?q=${encodeURIComponent(q)}`);

export const listPublicCollections = (q = '') =>
  request('GET', `/collections/public${q ? '?q=' + encodeURIComponent(q) : ''}`);

export const setCollectionVisibility = (username, coll, visibility) =>
  request('POST', `/collections/${username}/${coll}/visibility`, { visibility });

/* ---- file upload/delete ---- */
export const uploadFile = (file) => {
  const fd = new FormData();
  fd.append('file', file);
  const token = getAuthToken();
  const headers = {};
  if (token) headers['Authorization'] = 'Bearer ' + token;
  return fetch(`${getApiBase()}/files/upload`, { method: 'POST', body: fd, headers }).then(r => {
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

/* ---- settings ---- */
export { getApiBase, setApiBase, getAuthToken, DEFAULT_API };

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

/* ---- p2p network config (frontend-only) ---- */
const BOOTSTRAP_PEER_KEY = 'peerdrive_bootstrap_peer';
const RELAY_SERVER_KEY = 'peerdrive_relay_server';
const STUN_URL_KEY = 'peerdrive_stun_url';
const TURN_URL_KEY = 'peerdrive_turn_url';
const TURN_CREDENTIAL_KEY = 'peerdrive_turn_credential';

export function getBootstrapPeer() { return localStorage.getItem(BOOTSTRAP_PEER_KEY) || ''; }
export function setBootstrapPeer(v) { localStorage.setItem(BOOTSTRAP_PEER_KEY, v); }
export function getRelayServer() { return localStorage.getItem(RELAY_SERVER_KEY) || ''; }
export function setRelayServer(v) { localStorage.setItem(RELAY_SERVER_KEY, v); }
export function getStunUrl() { return localStorage.getItem(STUN_URL_KEY) || 'stun:stun.l.google.com:19302'; }
export function setStunUrl(v) { localStorage.setItem(STUN_URL_KEY, v); }
export function getTurnUrl() { return localStorage.getItem(TURN_URL_KEY) || ''; }
export function setTurnUrl(v) { localStorage.setItem(TURN_URL_KEY, v); }
export function getTurnCredential() { return localStorage.getItem(TURN_CREDENTIAL_KEY) || ''; }
export function setTurnCredential(v) { localStorage.setItem(TURN_CREDENTIAL_KEY, v); }

/* ---- ipfs gateway config (frontend-only) ---- */
const IPFS_ENABLED_KEY = 'peerdrive_ipfs_enabled';

export function getIPFSEnabled() { return localStorage.getItem(IPFS_ENABLED_KEY) !== 'false'; }
export function setIPFSEnabled(v) { localStorage.setItem(IPFS_ENABLED_KEY, v ? 'true' : 'false'); }

/* ---- IPFS compat layer (server-side) ---- */

// getIPFSCompatStatus 查询服务器 IPFS 兼容层状态。
export async function getIPFSCompatStatus() {
  const data = await request('GET', '/p2p/ipfs');
  return data;
}

// setIPFSCompatEnabled 通过服务器 API 启用或禁用 IPFS 兼容模式。
export async function setIPFSCompatEnabled(enabled) {
  const data = await request('POST', '/p2p/ipfs/toggle', { enabled });
  return data;
}

/* ---- IPFS pin & gateway ---- */

// pinCID 固定指定 CID（从 IPFS 网关下载并永久缓存）。
export async function pinCID(cid) {
  const data = await request('POST', `/p2p/ipfs/pin/${cid}`);
  return data;
}

// unpinCID 取消固定指定 CID。
export async function unpinCID(cid) {
  const data = await request('DELETE', `/p2p/ipfs/pin/${cid}`);
  return data;
}

// listPins 列出所有已固定的 CID。
export async function listPins() {
  const data = await request('GET', '/p2p/ipfs/pins');
  return data;
}

// getIPFSGatewayStatus 检查所有 IPFS 网关的健康状况。
export async function getIPFSGatewayStatus() {
  const data = await request('GET', '/p2p/ipfs/gateways');
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

export function uploadConsent() {
  localStorage.setItem('peerdrive_consent', JSON.stringify({ agreed: true, timestamp: Date.now() }));
}

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

/* ---- share ---- */
export const createShare = (hash, type, filename) => request('POST', '/shares', { hash, type, filename });
export const listShares = () => request('GET', '/shares');
export const getShareUrl = (token) => `${getApiBase()}/s/${token}`;

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

export async function getRegServerStats(regServerUrl) {
  const res = await fetch(`${regServerUrl}/stats`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

/* ---- service status dashboard ---- */
const REG_SERVER_KEY = 'peerdrive_reg_server_url';

export function getRegServerUrl() {
  return localStorage.getItem(REG_SERVER_KEY) || '';
}

export function setRegServerUrl(url) {
  localStorage.setItem(REG_SERVER_KEY, url);
}

export async function getServiceStats({ regServerUrl } = {}) {
  const results = {
    relay: { ok: false, data: null, error: null },
    reg: { ok: false, data: null, error: null },
    storage: { ok: false, data: null, error: null },
    bt: { ok: false, data: null, error: null },
    ws: { ok: false, data: null, error: null },
  };

  try {
    const data = await getP2PStatus();
    results.relay = { ok: true, data, error: null };
  } catch (e) {
    results.relay = { ok: false, data: null, error: e.message };
  }

  try {
    const data = await getBTStatus();
    results.bt = { ok: true, data, error: null };
  } catch (e) {
    results.bt = { ok: false, data: null, error: e.message };
  }

  try {
    const files = await listFiles();
    const totalFiles = files.length;
    const totalSize = files.reduce((sum, f) => sum + (f.size || 0), 0);
    results.storage = { ok: true, data: { totalFiles, totalSize, files }, error: null };
  } catch (e) {
    results.storage = { ok: false, data: null, error: e.message };
  }

  if (regServerUrl) {
    try {
      const res = await fetch(`${regServerUrl}/ping`);
      let pingData;
      const contentType = res.headers.get('content-type') || '';
      if (contentType.includes('json')) {
        pingData = await res.json();
      } else {
        pingData = { raw: await res.text() };
      }

      let jwtData = null;
      const authEnabled = localStorage.getItem('peerdrive_auth_header_enabled') === 'true';
      const token = localStorage.getItem('peerdrive_auth_key');
      if (authEnabled && token) {
        try {
          const jwtRes = await fetch(`${regServerUrl}/auth/whoami`, {
            headers: { Authorization: `Bearer ${token}` },
          });
          if (jwtRes.ok) jwtData = await jwtRes.json();
        } catch { /* ignore JWT errors */ }
      }

      results.reg = { ok: true, data: { ping: pingData, jwt: jwtData }, error: null };
    } catch (e) {
      results.reg = { ok: false, data: null, error: e.message };
    }
  }

  try {
    const data = await getWSInfo();
    results.ws = { ok: true, data, error: null };
  } catch (e) {
    results.ws = { ok: false, data: null, error: e.message };
  }

  return results;
}
export const getBEP51Sample = () => request('GET', '/p2p/bt/bep51/sample');
