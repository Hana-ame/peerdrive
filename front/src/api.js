// API 请求模块：封装与后端的所有 HTTP 通信和本地存储配置
const STORAGE_KEY = 'peerdrive_api_base';
const AUTH_TOKEN_KEY = 'peerdrive_auth_token';
const DEFAULT_API = 'https://wsl-3000.moonchan.xyz';

/* ---- 多后端管理 ---- */
const BACKENDS_KEY = 'peerdrive_backends';
const CURRENT_BACKEND_KEY = 'peerdrive_current_backend_id';

const DEFAULT_BACKENDS = [
  { id: 'wsl', name: 'WSL', url: 'https://wsl-3000.moonchan.xyz', stun_url: 'stun:stun.l.google.com:19302', turn_url: '', turn_credential: '' },
  { id: 'bwh', name: 'BWH', url: 'http://97.64.30.221:3000', stun_url: 'stun:stun.l.google.com:19302', turn_url: '', turn_credential: '' },
];

function getBackends() {
  try {
    const raw = localStorage.getItem(BACKENDS_KEY);
    if (raw) {
      const list = JSON.parse(raw);
      if (Array.isArray(list) && list.length > 0) return list;
    }
  } catch {}
  // 初始化默认后端
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
  // 同步 STUN/TURN 配置到全局
  if (target.stun_url !== undefined) localStorage.setItem(STUN_URL_KEY, target.stun_url);
  if (target.turn_url !== undefined) localStorage.setItem(TURN_URL_KEY, target.turn_url);
  if (target.turn_credential !== undefined) localStorage.setItem(TURN_CREDENTIAL_KEY, target.turn_credential);
  return true;
}

// 读取当前后端的某个字段，fallback 到全局值或默认值
function getBackendField(id, key, fallback) {
  const backends = getBackends();
  const target = backends.find(b => b.id === id);
  if (target && target[key] !== undefined) return target[key];
  return fallback;
}

// 更新当前后端的字段并落盘
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
  if (defIds.includes(id)) return false; // 默认后端不可删除
  backends = backends.filter(b => b.id !== id);
  setBackends(backends);
  if (getCurrentBackendId() === id) {
    // 切回第一个可用后端
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
  request('POST', '/collections/register-local', { path, filename: filename || path.split('/').pop() });
export const registerURL = async (url, filename = '') => {
  // 后端 RegisterURL 返回 {hash,size,mime,filename}（back file.go:120），字段是 mime 不是 mime_type；
  // RegisterLocalFile 只返回 {hash,filename}。统一补 mime_type/size 兼容旧调用方。
  const res = await request('POST', '/collections/register-url', { url, filename });
  return { ...res, mime_type: res.mime_type || res.mime || '', size: res.size || 0 };
};
export const registerFolder = (folderPath) =>
  request('POST', '/collections/register-folder', { folder_path: folderPath });

/* ---- file system browse ---- */
export const browseDir = (dirPath = '/') =>
  request('GET', `/files/browse?path=${encodeURIComponent(dirPath)}`);

/* ---- anon collections ---- */
export const createAnonCollection = (entries, friendly_name = '', tags = [], visibility = '', access_list_hash = '') => {
  const normalized = entries.map(e => ({
    path: e.path,
    providers: e.providers || [{ type: "sha256", value: e.hash, mime_type: e.mime_type || '' }],
  }));
  return request('POST', '/collections', { entries: normalized, friendly_name, tags, visibility, access_list_hash });
};
export const getAnonCollection = (hash) => request('GET', `/collections/${hash}`);
// 下载 URL 的虚拟路径必须逐段 encodeURIComponent（文件名可能含空格/#/? 等，
// 不编码会破坏 URL；后端 gin *filepath 已对 URL.Path 解码，编码后服务端比对仍正确）
const encodePath = (p) => (p || '').split('/').map(encodeURIComponent).join('/');
export const getAnonFileDownloadUrl = (hash, p) => `${getApiBase()}/collections/${encodeURIComponent(hash)}/${encodePath(p)}`;
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
export const pullUserCollection = (username, coll) =>
  request('POST', '/actions/pull', { username, collection_name: coll });
export const getUserFileDownloadUrl = (username, coll, filepath) =>
  `${getApiBase()}/${encodeURIComponent(username)}/${encodeURIComponent(coll)}/${encodePath(filepath)}`;

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
// 后端 POST /p2p/connect 绑定 {addr}，要求完整 multiaddr（含 /p2p/<peer_id>，见 back p2p.go ConnectPeer）。
// 坑：旧实现发 {peer_id, addrs} 与后端字段不匹配，req.Addr 为空串导致每次连接都 500。
export const connectPeer = (peerId, addrs = []) => {
  const list = Array.isArray(addrs) ? addrs : [addrs];
  const withPeer = list.filter(Boolean).map(a =>
    a.includes('/p2p/') ? a : `${a}/p2p/${peerId}`);
  if (withPeer.length === 0) return request('POST', '/p2p/connect', { addr: `/p2p/${peerId}` });
  return request('POST', '/p2p/connect', { addr: withPeer[0] });
};
export const p2pAnnounce = (hash) => request('POST', '/p2p/announce', { hash });
export const p2pFetch = (peerId, hash) => request('POST', '/p2p/fetch', { peer_id: peerId, hash });
// 后端 POST /p2p/sync 绑定 {peer_id, hash, file_hashes, target_dir}（back p2p.go SyncFromPeer），
// 旧实现发 collection_name 被忽略 → 400 "no files to sync"。
export const p2pSync = (peerId, hash) =>
  request('POST', '/p2p/sync', { peer_id: peerId, hash });
// 后端 POST /p2p/push 绑定 {hash, entries, target_dir}（PushSync），old collection_name 同理失效。
export const p2pPush = (peerId, hash) =>
  request('POST', '/p2p/push', { peer_id: peerId, hash });
export const p2pRequestFile = (hash) => request('POST', '/p2p/request-file', { hash });
export const getWSInfo = () => request('GET', '/p2p/ws/info');
export const getSignalPeers = () => request('GET', '/p2p/status').then(r => r.signal_peers || []);
export const getP2PTopology = () => request('GET', '/p2p/topology');
export const getP2PQuality = () => request('GET', '/p2p/quality');

/* ---- P2P BT ---- */
export const getBTStatus = () => request('GET', '/bt/status');
export const btAnnounce = (hash) => request('POST', '/bt/announce', { hash });
export const btFind = (hash) => request('POST', '/bt/find', { hash });

/* ---- BT Controller (download management) ---- */
export const btGetDownloads = () => request('GET', '/bt/downloads');
export const btGetDownload = (infohash) => request('GET', `/bt/download/${infohash}`);
export const btMagnetResolve = (uri) => request('POST', '/bt/magnet', { uri });
export const btTorrentUpload = (file) => {
  const fd = new FormData();
  fd.append('torrent', file);
  const token = getAuthToken();
  const headers = {};
  if (token) headers['Authorization'] = 'Bearer ' + token;
  return fetch(`${getApiBase()}/bt/torrent`, { method: 'POST', body: fd, headers }).then(r => {
    if (!r.ok) throw new Error(`Torrent upload failed: ${r.status}`);
    return r.json();
  });
};
export const btRemoveDownload = (infohash) => request('DELETE', `/bt/download/${infohash}`);
export const btPauseDownload = (infohash) => request('POST', `/bt/download/${infohash}/pause`);
export const btResumeDownload = (infohash) => request('POST', `/bt/download/${infohash}/resume`);
export const btSeedDownload = (infohash) => request('POST', `/bt/download/${infohash}/seed`);
export const btStopSeed = (infohash) => request('POST', `/bt/download/${infohash}/unseed`);
export const btGetStats = () => request('GET', '/bt/stats');
export const btGetTorrentUrl = (infohash) => `${getApiBase()}/bt/download/${infohash}/torrent`;
export const btGetMagnetUri = (infohash) => request('GET', `/bt/download/${infohash}/magnet`);
export const btSeedCollection = (collectionHash) => request('POST', '/bt/seed-collection', { collection_hash: collectionHash });

/* ---- P2P Dual ---- */
export const dualAnnounce = (hash) => request('POST', '/p2p/dual/announce', { hash });
export const dualFind = (hash) => request('POST', '/p2p/dual/find', { hash });

export { getApiBaseUrl as WS_TRANSFER_URL_BASE };
// 运行时计算 WS 地址：后端可在设置页切换，避免模块加载时固化的旧地址。
export function getWSTransferURL() {
  return getApiBase().replace(/^http/, 'ws') + '/ws/transfer';
}
// 兼容旧代码（注意：这是模块加载时的快照，切换后端后不刷新；新代码请用 getWSTransferURL()）。
export const WS_TRANSFER_URL = getWSTransferURL();

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
  const data = await request('GET', '/ipfs');
  return data;
}

// setIPFSCompatEnabled 通过服务器 API 启用或禁用 IPFS 兼容模式。
export async function setIPFSCompatEnabled(enabled) {
  const data = await request('POST', '/ipfs/toggle', { enabled });
  return data;
}

/* ---- IPFS pin & gateway ---- */

// pinCID 固定指定 CID（从 IPFS 网关下载并永久缓存）。
export async function pinCID(cid) {
  const data = await request('POST', `/ipfs/pin/${cid}`);
  return data;
}

// unpinCID 取消固定指定 CID。
export async function unpinCID(cid) {
  const data = await request('DELETE', `/ipfs/pin/${cid}`);
  return data;
}

// listPins 列出所有已固定的 CID。
export async function listPins() {
  const data = await request('GET', '/ipfs/pins');
  return data;
}

// getIPFSGatewayStatus 检查所有 IPFS 网关的健康状况。
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

// 保存数据同意到本地。
// 注意：旧名称 uploadConsent 容易让人误以为会上传服务器，但后端没有 /consent 端点；
// 这里只做本地记录，避免「已上传同意记录」这类误导性状态。
export async function saveConsentLocal() {
  localStorage.setItem('peerdrive_consent', JSON.stringify({ agreed: true, timestamp: Date.now() }));
}
// 兼容旧调用（保留别名，但新代码应使用 saveConsentLocal）。
export const uploadConsent = saveConsentLocal;

/* ---- alias exports for legacy usage ---- */
// 统一 Collection API（替代旧的 createUserCollection/createAnonCollection 等）
// 一切皆 collection，entry 的 provider 可为 sha256 或 url
export const createCollection = (entries, name = '', tags = []) =>
  request('POST', '/collections', { name, entries, tags });
export const listCollections = () => request('GET', '/collections');
export const getCollection = (id) => request('GET', `/collections/${id}`);
export const addEntry = addCollectionEntry;
export const deleteEntry = removeCollectionEntry;
export const commitVersion = commitCollection;
export const downloadFileByPath = getUserFileDownloadUrl;
export const mergeCollection = mergeUserCollection;
export const getNodeInfo = getP2PNode;

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
export const getBEP51Sample = () => request('GET', '/bt/bep51/sample');
