const API_BASE = 'https://wsl-3000.moonchan.xyz';

async function request(method, path, body = null, isFormData = false) {
  const opts = { method, headers: {}, credentials: 'include' };
  const authKey = localStorage.getItem('peerdrive_authkey');
  if (authKey) {
    opts.headers['Authorization'] = `Bearer ${authKey}`;
  }

  if (body && !isFormData) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  } else if (body && isFormData) {
    opts.body = body;
  }
  const res = await fetch(`${API_BASE}${path}`, opts);
  if (res.status === 409) {
    const errData = await res.json().catch(() => ({}));
    const error = new Error(errData.message || 'Conflict');
    error.status = 409; error.data = errData; throw error;
  }
  if (!res.ok) {
    const errData = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
    throw new Error(errData.error || errData.message || `HTTP ${res.status}`);
  }
  return res.json();
}

// === Auth ===
export const register = (username, password) => request('POST', '/auth/register', { username, password });
export const login = (username, password) => request('POST', '/auth/login', { username, password });
export const logout = () => request('POST', '/auth/logout');
export const getMe = () => request('GET', '/auth/me');

// === 基础 & 文件 ===
export const uploadFile = (file) => {
  const formData = new FormData(); formData.append('file', file);
  return request('POST', '/files/upload', formData, true);
};
export const downloadFileByPath = (username, collName, path) =>
  `${API_BASE}/${username}/${collName}/${path}`;

// === 合集 ===
export const createCollection = (username, collection_name) =>
  request('POST', '/collections', { username, collection_name });
export const listCollections = (username) => request('GET', `/collections/${username}`);
export const getCollection = (username, collName) =>
  request('GET', `/collections/${username}/${collName}`);
export const addEntry = (username, collName, path, hash) =>
  request('POST', `/collections/${username}/${collName}/entries`, { path, hash });
export const deleteEntry = (username, collName, path) =>
  request('DELETE', `/collections/${username}/${collName}/entries/${path}`);

// === 版本控制 ===
export const commitVersion = (username, collName, commit_message) =>
  request('POST', `/collections/${username}/${collName}/commit`, { commit_message });
export const getVersionLog = (username, collName) =>
  request('GET', `/collections/${username}/${collName}/log`);
export const rollbackVersion = (username, collName, version_id) =>
  request('POST', `/collections/${username}/${collName}/rollback/${version_id}`, { version_id });

// === 协作 ===
export const forkCollection = (username, collection_name, source_username, source_coll_name) =>
  request('POST', '/actions/fork', { username, collection_name, source_username, source_coll_name });
export const mergeCollection = (username, collection_name, source_username, source_coll_name, strategy = 'ours') =>
  request('POST', '/actions/merge', { username, collection_name, source_username, source_coll_name, strategy });

// === 本地同步 ===
export const saveLocal = (req) => request('POST', '/local/save', req);
export const getLocalStatus = (hash) => request('GET', `/local/status/${hash}`);

// === 搜索 ===
export const searchCollections = (query) => request('GET', `/collections/search?q=${encodeURIComponent(query)}`);

// === P2P ===
export const getNodeInfo = () => request('GET', '/p2p/node');
export const getPeers = () => request('GET', '/p2p/peers');
