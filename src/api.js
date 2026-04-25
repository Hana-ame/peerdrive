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

// 1. SHA256
export const verifyFile = (hash) => request('GET', `/files/verify/${hash}`);
export const getDownloadUrl = (hash) => `${API_BASE}/sha256sum/${hash}`;

// 2. 注册 Path
export const registerLocalFile = (path, filename) => 
  request('POST', '/files/register_local', { path, filename: filename || path.split('/').pop() });

// 3. 匿名合集
export const createAnonCollection = (entries) => request('POST', '/anon/collections', { entries });
export const getAnonCollection = (hash) => request('GET', `/anon/collections/${hash}`);
export const getAnonFileDownloadUrl = (hash, filepath) => `${API_BASE}/anon/collections/${hash}/entries/${filepath}`;
export const forkAnonCollection = (source_hash, add_entries, remove_paths) => 
  request('POST', '/anon/collections/fork', { source_hash, add_entries, remove_paths });