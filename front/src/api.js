// API 请求模块：封装与后端的所有通信和本地存储配置。
// 迁移说明（2026-08-17）：后端通信全面走本地 WS 会话 /ws/peer 的 admin 帧
// （ws.js），不再直接 fetch HTTP——管理面只暴露给本地 WS（WebRTC/peerjs
// 不实现管理 verb，防权限面漏洞）。HTTP 端点保留原路径作 legacy（兼容旧
// 客户端/curl/集成测试，见 back/internal/router/router.go 标记）。
// 调用方（页面）无感：request() 签名不变，错误语义与 fetch 版一致
// （Error.status/Error.data 结构化 body，409 冲突清单等）。
import * as ws from './ws.js';
const STORAGE_KEY = 'peerdrive_api_base';
const AUTH_TOKEN_KEY = 'peerdrive_auth_token';
const DEFAULT_API = 'https://wsl-3000.moonchan.xyz';

/* ---- 多后端管理 ---- */
const BACKENDS_KEY = 'peerdrive_backends';
const CURRENT_BACKEND_KEY = 'peerdrive_current_backend_id';

const DEFAULT_BACKENDS = [
  { id: 'wsl', name: 'WSL', url: 'https://wsl-3000.moonchan.xyz' },
  { id: 'bwh', name: 'BWH', url: 'http://97.64.30.221:3000' },
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
  // 迁移记录（2026-08-20）：原此处同步 STUN/TURN 到全局 localStorage——
  // 已随 STUN/TURN 死配置一并删除：全前端无任何 RTCPeerConnection/iceServers
  // 消费方（浏览器主应用只走 /ws/peer WS 会话；peerdrive-media 独立包自带
  // 空 iceServers 默认，见其 core.js），这些设置是「设置页写→设置页读」闭环。
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

// 通用请求封装：走本地 WS 会话 admin 帧（ws.js 内部转发 gin engine）。
// 语义与旧 fetch 版完全一致：status>=400 → Error(err.status/err.data)
// （409 冲突清单等结构化错误体，见 ws.js handleText admin-resp 分支）。
async function request(method, path, body = null) {
  return ws.admin(method, path, body);
}

/* ---- file ---- */
export const verifyFile = (hash) => request('GET', `/files/verify/${hash}`);
// downloadFile 经 WS req verb 拉取 sha256 内容（ws.js download，返回 Uint8Array）。
// 旧 getDownloadUrl(hash) 返回 HTTP URL 已废弃（HTTP 是 legacy）；调用方需改
// 用本函数或 downloadFileToDisk。
export const downloadFile = (hash) => ws.download(hash);
export const downloadFileToDisk = (hash, filename) => ws.downloadToFile(hash, filename);

// getBlobUrl 经 WS 拉取文件 → objectURL（图片/视频/PDF 预览用），带缓存。
// 旧做法直接 <img src={getDownloadUrl(hash)}> 走 HTTP（legacy）；迁移后预览
// 资源也走 WS。objectURL 生命周期由调用方 revoke（或页面卸载时清理）。
// 内存泄漏防御（发现背景：代码审阅 2026-08-18——blobUrlCache 只增不减，
// 每个不重复 hash 的预览各占一个 Blob 内存 + objectURL，长时间浏览累积）：
// LRU 上限 + 淘汰即 revoke。Map 迭代序 = 插入序，重读 delete+set 刷新位置。
const BLOB_URL_CACHE_MAX = 50;
// 大文件预览防护（发现背景：代码审阅 2026-08-18——download 全量内存
// 组装，超大型文件预览会 OOM）。超过阈值拒绝预览并抛 err.code==='TOO_LARGE'，
// 调用方应提示走 downloadFileToDisk 流式保存。
const BLOB_URL_MAX_BYTES = 200 * 1024 * 1024;
const blobUrlCache = new Map();
// 并发去重（发现背景：代码审阅 2026-08-18——同 hash 并发两次 getBlobUrl
// 会发两次 WS 下载，后完成的覆盖先完成的缓存项，先完成的 objectURL 永久
// 泄漏且白耗带宽）。blobUrlInflight 存进行中的 Promise，命中即共享同一次
// 下载；失败会 settle 后从 map 移除，下次调用自然重试。
const blobUrlInflight = new Map();
export async function getBlobUrl(hash, mime = '') {
  if (blobUrlCache.has(hash)) {
    const url = blobUrlCache.get(hash);
    blobUrlCache.delete(hash); // 重读 → 刷新 LRU 位置（set 后位于末尾）
    blobUrlCache.set(hash, url);
    return url;
  }
  if (blobUrlInflight.has(hash)) return blobUrlInflight.get(hash);
  const p = (async () => {
    // stat 只探大小不发数据（req offset=0 size=0 → meta{total}）
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
      // 逐出最久未用的：正在屏幕上预览的必然近期被 get（位置靠后），
      // 最旧项最可能已离开视图，revoke 其 objectURL 释放 Blob 内存。
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
// downloadAnonFile 经 WS admin GET 拉集合内文件（后端返回文件流 → admin-bin
// 二进制帧）。旧 getAnonFileDownloadUrl(hash, p) 返回 HTTP URL 已废弃。
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
// downloadUserFile 经 WS admin GET 拉用户集合内文件（同 downloadAnonFile）。
// 旧 getUserFileDownloadUrl(username, coll, filepath) 返回 HTTP URL 已废弃。
export const downloadUserFile = (username, coll, filepath) =>
  ws.admin('GET', `/${encodeURIComponent(username)}/${encodeURIComponent(coll)}/${encodePath(filepath)}`);

// 注意：旧 libp2p 双栈面板（P2PPanel/P2PDashboard/P2PTopology/DHTExplorer 双栈查询）
// 及其 api 导出（getP2PStatus/getP2PPeers/dualAnnounce/dualFind 等）已于 2026-08-19
// 随 libp2p 端点删除一并清理——后端 /p2p/* 只剩 forward/auth-status/webrtc-info。

/* ---- P2P BT ---- */
// PeerJS 节点状态（GET /peerjs/node，2026-08-19 起替代已删的 /p2p/status 供前端面板用）。
export const getPeerjsNode = () => request('GET', '/peerjs/node');
export const getBTStatus = () => request('GET', '/bt/status');
export const btAnnounce = (hash) => request('POST', '/bt/announce', { hash });
export const btFind = (hash) => request('POST', '/bt/find', { hash });

/* ---- BT Controller (download management) ---- */
export const btGetDownloads = () => request('GET', '/bt/downloads');
export const btGetDownload = (infohash) => request('GET', `/bt/download/${infohash}`);
export const btMagnetResolve = (uri) => request('POST', '/bt/magnet', { uri });
export const btTorrentUpload = (file) =>
  // admin 二进制上传：/bt/torrent + multipart 字段名 "torrent"
  // （后端 BTTorrentUpload 读 FormFile("torrent")，与 /files/upload 的 "file" 不同）
  ws.upload(file, file?.name, 'torrent', '/bt/torrent');
export const btRemoveDownload = (infohash) => request('DELETE', `/bt/download/${infohash}`);
export const btPauseDownload = (infohash) => request('POST', `/bt/download/${infohash}/pause`);
export const btResumeDownload = (infohash) => request('POST', `/bt/download/${infohash}/resume`);
export const btSeedDownload = (infohash) => request('POST', `/bt/download/${infohash}/seed`);
export const btStopSeed = (infohash) => request('POST', `/bt/download/${infohash}/unseed`);
export const btGetMagnetUri = (infohash) => request('GET', `/bt/download/${infohash}/magnet`);
// downloadTorrentFile 经 WS admin GET 拉 .torrent 文件（二进制响应 → admin-bin）。
// 旧 btGetTorrentUrl(infohash) 返回 HTTP URL 已废弃。
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
// uploadFile 走 admin 二进制分片上传（ws.upload，multipart 字段 "file"）。
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

/* ---- p2p network config（已整体删除，2026-08-20）----
 * bootstrapPeer / relayServer / stunUrl / turnUrl / turnCredential 全部为
 * 「设置页写 localStorage → 设置页读回显」的死闭环，无任何功能消费方：
 * - bootstrap peer：libp2p 概念，后端 PEERDRIVE_BOOTSTRAP_PEER 已随 libp2p
 *   栈删除（doc/LEGACY.md §C）
 * - relay：relay 服务后端已删（doc/LEGACY.md §A），徽章恒 false
 * - STUN/TURN：浏览器主应用不创建 RTCPeerConnection（全部通信走 /ws/peer
 *   WS 会话）；peerdrive-media 独立包默认空 iceServers（浏览器↔Node 内网
 *   场景 host candidate 即可）。未来若做跨网穿透，在 media 包 signaling.config
 *   显式传 iceServers，而不是恢复这组设置页。
 */

/* ---- ipfs gateway config (frontend-only) ---- */
const IPFS_ENABLED_KEY = 'peerdrive_ipfs_enabled';

export function getIPFSEnabled() { return localStorage.getItem(IPFS_ENABLED_KEY) !== 'false'; }

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
// 再 review 2026-08：原实现写到 peerdrive_consent 这个没有任何读取方的键，
// 而实际在设置页生效的是 DATA_CONSENT_KEY（peerdrive_data_consent）；统一改为
// 委托 setDataConsent(true)，避免同意状态出现“已写但读不到”的分叉。
export async function saveConsentLocal() {
  setDataConsent(true);
}

/* ---- alias exports for legacy usage ---- */
// 统一 Collection API（替代旧的 createUserCollection/createAnonCollection 等）
// 一切皆 collection，entry 的 provider 可为 sha256 或 url。
// 注意：匿名分派器（dispatchCreateCollection）按 body 里是否有 username 走
// 用户体系，匿名创建字段是 friendly_name 不是 name（发现背景：再 review 2026-08
// 发现此别名用 name 会导致匿名创建时后端收到空 friendly_name，合集名丢失）。
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
