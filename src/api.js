const STORAGE_KEY = 'peerdrive_api_base';
const DEFAULT_API = 'https://wsl-3000.moonchan.xyz';

function getApiBase() {
  return localStorage.getItem(STORAGE_KEY) || DEFAULT_API;
}

function setApiBase(url) {
  localStorage.setItem(STORAGE_KEY, url);
}

function getApiBaseUrl() {
  return getApiBase();
}

async function request(method, path, body = null) {
  const opts = { method, headers: {} };
  if (body) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  if (localStorage.getItem('peerdrive_auth_header_enabled') === 'true') {
    const token = localStorage.getItem('peerdrive_auth_key');
    if (token) opts.headers['Authorization'] = `Bearer ${token}`;
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
export const registerFolder = (folderPath) =>
  request('POST', '/files/register_folder', { folder_path: folderPath });

/* ---- file system browse ---- */
export const browseDir = (dirPath = '/') =>
  request('GET', `/files/browse?path=${encodeURIComponent(dirPath)}`);

/* ---- anon collections ---- */
export const createAnonCollection = (entries, friendly_name = '', tags = []) =>
  request('POST', '/anon/collections', { entries, friendly_name, tags });
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

/* ---- P2P BT ---- */
export const getBTStatus = () => request('GET', '/p2p/bt/status');
export const btAnnounce = (hash) => request('POST', '/p2p/bt/announce', { hash });
export const btFind = (hash) => request('POST', '/p2p/bt/find', { hash });

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
  return fetch(`${getApiBase()}/files/upload`, { method: 'POST', body: fd }).then(r => {
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
export { getApiBase, setApiBase, DEFAULT_API };

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
