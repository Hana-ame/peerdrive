import { API_BASE } from '../config'

async function request<T = any>(method: string, path: string, body?: any): Promise<T> {
  const opts: RequestInit = { method, headers: {} }
  if (body instanceof FormData) {
    opts.body = body
  } else if (body) {
    opts.headers = { 'Content-Type': 'application/json' }
    opts.body = JSON.stringify(body)
  }
  const res = await fetch(`${API_BASE}${path}`, opts)
  const type = res.headers.get('content-type') || ''
  if (type.includes('application/json')) {
    const data = await res.json()
    if (!res.ok) throw new Error(data.error || res.statusText)
    return data
  }
  if (!res.ok) throw new Error(res.statusText)
  return res.text() as any
}

export const ping = () => request<string>('GET', '/ping')

export const uploadFile = (file: File) => {
  const fd = new FormData()
  fd.append('file', file)
  return request<{ hash: string; filename: string }>('POST', '/files/upload', fd)
}

export const verifyFile = (hash: string) =>
  request<{ hash: string; filename: string; provider: string; path: string }>('GET', `/files/verify/${hash}`)

export const deleteFile = (hash: string) =>
  request<{ message: string }>('DELETE', `/files/${hash}`)

export const registerLocalFile = (path: string, filename: string) =>
  request<{ hash: string; filename: string }>('POST', '/files/register_local', { path, filename })

export const registerFolder = (folderPath: string) =>
  request<{ registered: { filename: string; hash: string }[] }>('POST', '/files/register_folder', { folder_path: folderPath })

export const diffVersions = (versionA: number, versionB: number) =>
  request<{ added: any[]; removed: any[]; modified: any[] }>('POST', '/files/diff', { version_a: versionA, version_b: versionB })

export const listCollections = (username: string) =>
  request<{ data: any[] }>('GET', `/collections/${username}`)

export const getCollection = (username: string, collName: string) =>
  request<{ collection: any; entries: any[] }>('GET', `/collections/${username}/${collName}`)

export const createCollection = (username: string, collectionName: string) =>
  request('POST', '/collections', { username, collection_name: collectionName })

export const addEntry = (username: string, collName: string, path: string, hash: string) =>
  request('POST', `/collections/${username}/${collName}/entries`, { path, hash })

export const removeEntry = (username: string, collName: string, path: string) =>
  request('DELETE', `/collections/${username}/${collName}/entries/${encodeURIComponent(path)}`)

export const commitCollection = (username: string, collName: string, commitMessage: string) =>
  request<{ version_number: number }>('POST', `/collections/${username}/${collName}/commit`, { commit_message: commitMessage })

export const getVersionLog = (username: string, collName: string) =>
  request<{ data: any[] }>('GET', `/collections/${username}/${collName}/log`)

export const rollbackCollection = (username: string, collName: string, versionId: number) =>
  request('POST', `/collections/${username}/${collName}/rollback/${versionId}`)

export const forkCollection = (payload: {
  username: string
  collection_name: string
  source_username: string
  source_coll_name: string
}) => request('POST', '/actions/fork', payload)

export const pullCollection = (username: string, collName: string) =>
  request('POST', '/actions/pull', { username, collection_name: collName })

export const mergeFromSource = (payload: {
  username: string
  collection_name: string
  source_username: string
  source_coll_name: string
  strategy: string
}) => request('POST', '/actions/merge', payload)

export const getTaskStatus = (id: number) =>
  request<{ task: any }>('GET', `/tasks/${id}`)

export const getNodeInfo = () =>
  request<{ peer_id: string; addrs: string[] }>('GET', '/p2p/node')

export const getPeers = () =>
  request<{ peers: any[] }>('GET', '/p2p/peers')
