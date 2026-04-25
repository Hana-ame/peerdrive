import React, { useState } from 'react'
import * as api from '../api'

function Tree({ entries }) {
  const root = {}
  for (const e of entries) {
    const parts = e.path.split('/')
    let node = root
    for (let i = 0; i < parts.length; i++) {
      const seg = parts[i]
      if (i === parts.length - 1) {
        if (!node._files) node._files = []
        node._files.push({ name: seg, hash: e.hash })
      } else {
        if (!node[seg]) node[seg] = {}
        node = node[seg]
      }
    }
  }
  const renderNode = (node, depth = 0) => {
    const lines = []
    const dirs = Object.keys(node).filter(k => k !== '_files').sort()
    const files = (node._files || []).sort((a, b) => a.name.localeCompare(b.name))
    for (const name of dirs) {
      lines.push(
        <div key={name + depth} style={{ paddingLeft: depth * 16 + 'px' }}
          className="text-zinc-300 text-sm font-mono">
          <span className="text-amber-400">📁 {name}/</span>
        </div>
      )
      lines.push(...renderNode(node[name], depth + 1))
    }
    for (const f of files) {
      lines.push(
        <div key={f.name + f.hash} style={{ paddingLeft: depth * 16 + 'px' }}
          className="text-zinc-400 text-sm font-mono flex items-center gap-2 group">
          <span>📄 {f.name}</span>
          <span className="text-xs text-zinc-600 truncate max-w-[120px]">{f.hash}</span>
        </div>
      )
    }
    return lines
  }
  return <div className="mt-3 space-y-0.5">{renderNode(root)}</div>
}

export default function CollectionBuilder() {
  /* --- build state --- */
  const [entries, setEntries] = useState([])
  const [regPath, setRegPath] = useState('')
  const [regLoading, setRegLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [buildError, setBuildError] = useState('')
  const [lastHash, setLastHash] = useState('')
  const [quickMode, setQuickMode] = useState(false)

  /* --- browse state --- */
  const [viewHash, setViewHash] = useState('')
  const [collection, setCollection] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  /* --- fork state --- */
  const [forkAddPath, setForkAddPath] = useState('')
  const [forkAddHash, setForkAddHash] = useState('')
  const [forkRemovePath, setForkRemovePath] = useState('')

  /* --- friendly name --- */
  const [friendlyName, setFriendlyName] = useState('')

  /* --- build helpers --- */
  const autoFriendlyName = (folderPath) => {
    if (friendlyName) return // don't overwrite manual input
    const trimmed = folderPath.replace(/\/+$/, '')
    const basename = trimmed.split('/').pop() || ''
    if (basename) setFriendlyName(basename)
  }

  /* ======== build helpers ======== */
  const registerFolder = async () => {
    if (!regPath.trim()) return
    setRegLoading(true)
    setBuildError('')
    try {
      const res = await api.registerFolder(regPath.trim())
      const newOnes = (res.registered || []).map(f => ({
        path: f.filename || f.path || '',
        hash: f.hash || '',
      }))
      if (newOnes.length === 0) {
        setBuildError('目录中没有文件')
        return
      }
      autoFriendlyName(regPath.trim())
      setRegPath('')
      setEntries(prev => {
        const seen = new Set(prev.map(e => e.path))
        return [...prev, ...newOnes.filter(e => !seen.has(e.path))]
      })
    } catch (e) {
      setBuildError(e.message)
    } finally {
      setRegLoading(false)
    }
  }

  const registerFile = async () => {
    if (!regPath.trim()) return
    setRegLoading(true)
    setBuildError('')
    try {
      const res = await api.registerLocalFile(regPath.trim())
      const entry = { path: res.filename || '', hash: res.hash }
      setRegPath('')
      setEntries(prev => {
        if (prev.find(e => e.path === entry.path)) return prev
        return [...prev, entry]
      })
    } catch (e) {
      setBuildError(e.message)
    } finally {
      setRegLoading(false)
    }
  }

  const addManual = () => {
    setEntries(prev => [...prev, { path: '', hash: '' }])
  }

  const updateEntry = (idx, field, value) => {
    setEntries(prev => prev.map((e, i) => i === idx ? { ...e, [field]: value } : e))
  }

  const removeEntry = (idx) => {
    setEntries(prev => prev.filter((_, i) => i !== idx))
  }

  const moveEntry = (idx, dir) => {
    setEntries(prev => {
      const next = [...prev]
      const target = idx + dir
      if (target < 0 || target >= next.length) return prev
      ;[next[idx], next[target]] = [next[target], next[idx]]
      return next
    })
  }

  const handleCreate = async (e) => {
    e && e.preventDefault()
    const valid = entries.filter(e => e.path && e.hash)
    if (valid.length === 0) {
      setBuildError('请至少添加一个有效条目（path 和 hash 都不能为空）')
      return
    }
    setCreating(true)
    setBuildError('')
    try {
      const res = await api.createAnonCollection(valid)
      setLastHash(res.hash)
      setViewHash(res.hash)
      fetchCollection(res.hash)
    } catch (e) {
      setBuildError(e.message)
    } finally {
      setCreating(false)
    }
  }

  const handleQuickCreate = async () => {
    if (!regPath.trim()) return
    setQuickMode(true)
    setBuildError('')
    try {
      const res = await api.registerFolder(regPath.trim())
      const list = (res.registered || []).map(f => ({
        path: f.filename || f.path || '',
        hash: f.hash || '',
      }))
      if (list.length === 0) {
        setBuildError('目录中没有文件')
        setQuickMode(false)
        return
      }
      autoFriendlyName(regPath.trim())
      const coll = await api.createAnonCollection(list)
      setRegPath('')
      setEntries(list)
      setLastHash(coll.hash)
      setViewHash(coll.hash)
      fetchCollection(coll.hash)
    } catch (e) {
      setBuildError(e.message)
    } finally {
      setQuickMode(false)
    }
  }

  /* ======== browse helpers ======== */
  const fetchCollection = async (hash) => {
    if (!hash) return
    setLoading(true)
    setError('')
    try {
      const res = await api.getAnonCollection(hash)
      setCollection(res)
      setViewHash(hash)
    } catch {
      setError('合集未找到')
      setCollection(null)
    } finally {
      setLoading(false)
    }
  }

  const handleFork = async (e) => {
    e.preventDefault()
    const add_entries = forkAddPath && forkAddHash ? [{ path: forkAddPath, hash: forkAddHash }] : []
    const remove_paths = forkRemovePath ? [forkRemovePath] : []
    try {
      const res = await api.forkAnonCollection(viewHash, add_entries, remove_paths)
      setForkAddPath(''); setForkAddHash(''); setForkRemovePath('')
      fetchCollection(res.hash)
    } catch (err) {
      alert('Fork 失败: ' + err.message)
    }
  }

  const validEntries = entries.filter(e => e.path && e.hash)

  /* ======== render ======== */
  return (
    <div className="max-w-4xl mx-auto space-y-10">
      {/* ===================== BUILD ===================== */}
      <section>
        <h2 className="text-xl font-semibold mb-2">合集构建器</h2>
        <p className="text-zinc-400 text-sm mb-6">注册服务器文件，编排路径，生成不可变匿名合集。</p>

        {/* quick create bar */}
        <div className="flex flex-wrap items-end gap-2 mb-5 bg-zinc-900 border border-zinc-800 rounded-lg p-4">
          <div className="flex-1 min-w-[300px]">
            <label className="block text-xs text-zinc-500 mb-1.5">服务器路径</label>
            <input
              value={regPath} onChange={e => setRegPath(e.target.value)}
              placeholder="/storage/mydata/ 或 /tmp/a.txt"
              className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                         focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600"
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); registerFolder() } }}
            />
          </div>
          <div className="flex gap-2">
            <button onClick={registerFolder} disabled={regLoading}
              className="px-4 py-2 rounded-lg text-sm font-medium transition-colors
                         bg-zinc-800 text-zinc-200 hover:bg-zinc-700 disabled:opacity-40">
              {regLoading ? '注册中' : '📂 注册文件夹'}
            </button>
            <button onClick={registerFile} disabled={regLoading}
              className="px-4 py-2 rounded-lg text-sm font-medium transition-colors
                         bg-zinc-800 text-zinc-200 hover:bg-zinc-700 disabled:opacity-40">
              📄 注册文件
            </button>
            <button onClick={handleQuickCreate} disabled={quickMode || !regPath.trim()}
              className="px-4 py-2 rounded-lg text-sm font-medium transition-colors
                         bg-amber-600 text-white hover:bg-amber-500 disabled:opacity-40">
              {quickMode ? '创建中' : '⚡ 一键创建合集'}
            </button>
          </div>
        </div>

        <p className="text-xs text-zinc-600 mb-2">
          <span className="text-amber-400/60">一键创建</span>：直接注册文件夹并生成合集。
          <span className="text-zinc-500 ml-4">注册文件夹/文件</span>：添加到下方条目列表中继续编排。
        </p>

        {buildError && (
          <div className="mb-4 bg-red-900/20 border border-red-900/40 rounded-lg px-4 py-3 text-sm text-red-400">
            {buildError}
          </div>
        )}

        {/* friendly name */}
        {entries.length > 0 && (
          <div className="mb-4 flex items-center gap-2">
            <label className="text-xs text-zinc-500 whitespace-nowrap">合集名称</label>
            <input
              value={friendlyName} onChange={e => setFriendlyName(e.target.value)}
              placeholder="可选，留空则无名称"
              className="bg-zinc-900 border border-zinc-700 rounded px-3 py-1.5 text-sm
                         focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 max-w-xs"
            />
          </div>
        )}

        {/* entry list */}
        {entries.length > 0 && (
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg overflow-hidden">
            <div className="flex items-center justify-between px-4 py-2.5 border-b border-zinc-800 bg-zinc-900/50">
              <span className="text-xs text-zinc-400">
                共 <span className="text-zinc-200 font-mono">{validEntries.length}</span> 个有效条目
                {validEntries.length !== entries.length &&
                  <span className="text-red-400 ml-1">({entries.length - validEntries.length} 个未完成)</span>
                }
              </span>
              <button onClick={addManual}
                className="text-xs text-zinc-400 hover:text-zinc-200 px-2 py-1 rounded border border-zinc-700 hover:border-zinc-500 transition-colors">
                + 手动添加
              </button>
            </div>

            <div className="max-h-96 overflow-y-auto">
              {entries.map((e, idx) => (
                <div key={idx} className="flex items-center gap-2 px-4 py-2 border-b border-zinc-800/50 hover:bg-zinc-800/30 transition-colors group">
                  {/* drag handle / move */}
                  <div className="flex flex-col gap-px opacity-0 group-hover:opacity-100 transition-opacity">
                    <button onClick={() => moveEntry(idx, -1)} disabled={idx === 0}
                      className="text-zinc-600 hover:text-zinc-300 text-xs leading-none disabled:opacity-20">&blacktriangle;</button>
                    <button onClick={() => moveEntry(idx, 1)} disabled={idx === entries.length - 1}
                      className="text-zinc-600 hover:text-zinc-300 text-xs leading-none disabled:opacity-20">&blacktriangledown;</button>
                  </div>

                  <div className="flex-1">
                    <input
                      value={e.path} onChange={ev => updateEntry(idx, 'path', ev.target.value)}
                      placeholder="相对路径 如 docs/readme.txt"
                      className={`w-full bg-transparent text-sm font-mono outline-none py-0.5
                        ${e.path ? 'text-zinc-300' : 'text-zinc-600'}`}
                    />
                  </div>

                  <div className="w-1/3 min-w-[200px]">
                    <input
                      value={e.hash} onChange={ev => updateEntry(idx, 'hash', ev.target.value)}
                      placeholder="SHA256"
                      className={`w-full bg-transparent text-xs font-mono outline-none py-0.5 truncate block
                        ${e.hash ? 'text-zinc-500' : 'text-zinc-600'}`}
                    />
                  </div>

                  <button onClick={() => removeEntry(idx)}
                    className="text-zinc-700 hover:text-red-400 text-lg opacity-0 group-hover:opacity-100 transition-all">
                    &times;
                  </button>
                </div>
              ))}
            </div>
          </div>
        )}

        {entries.length === 0 && !buildError && (
          <div className="bg-zinc-900/50 border border-dashed border-zinc-800 rounded-lg p-10 text-center text-zinc-600 text-sm">
            条目列表为空 &mdash; 注册文件夹/文件或手动添加条目开始构建合集
          </div>
        )}

        {/* tree preview */}
        {validEntries.length > 0 && (
          <details className="mt-3 group">
            <summary className="cursor-pointer text-xs text-zinc-500 hover:text-zinc-300 transition-colors select-none">
              目录结构预览
            </summary>
            <div className="bg-zinc-900/50 border border-zinc-800 rounded-lg p-4 pb-3">
              <Tree entries={validEntries} />
            </div>
          </details>
        )}

        {/* create button */}
        {validEntries.length > 0 && (
          <div className="mt-4">
            <button onClick={handleCreate} disabled={creating}
              className="bg-white text-black px-6 py-3 rounded-lg text-sm font-bold
                         hover:bg-zinc-200 disabled:opacity-40 transition-all">
              {creating ? '创建中...' : `生成不可变合集（${validEntries.length} 个文件）`}
            </button>
            {lastHash && (
              <span className="ml-4 text-xs text-emerald-400">
                最近创建：<code className="font-mono text-emerald-300">{lastHash.slice(0, 16)}...</code>
              </span>
            )}
          </div>
        )}
      </section>

      {/* ===================== BROWSE ===================== */}
      <section className="border-t border-zinc-800 pt-10">
        <h2 className="text-xl font-semibold mb-2">浏览合集</h2>
        <p className="text-zinc-400 text-sm mb-5">通过哈希值查看合集内容或 Fork 已有合集。</p>

        <form onSubmit={e => { e.preventDefault(); fetchCollection(viewHash) }} className="flex gap-2">
          <input
            value={viewHash} onChange={e => setViewHash(e.target.value)}
            placeholder="Collection Hash"
            className="flex-1 bg-zinc-900 border border-zinc-700 rounded-lg px-4 py-2.5 text-sm
                       focus:outline-none focus:border-zinc-500 placeholder:text-zinc-600 font-mono"
          />
          <button type="submit" disabled={loading}
            className="bg-white text-black px-5 py-2.5 rounded-lg text-sm font-medium
                       hover:bg-zinc-200 disabled:opacity-40 transition-colors">
            {loading ? '加载中' : '查看'}
          </button>
        </form>

        {error && (
          <div className="mt-3 bg-red-900/20 border border-red-900/40 rounded-lg px-4 py-3 text-sm text-red-400">
            {error}
          </div>
        )}

        {collection && (
          <div className="mt-4 bg-zinc-900 border border-zinc-800 rounded-xl overflow-hidden">
            <div className="flex items-center justify-between px-5 py-3 border-b border-zinc-800 bg-zinc-900/50">
              <div className="flex items-center gap-4 text-xs text-zinc-400">
                <span>Version <span className="text-zinc-200 font-mono">{collection.version}</span></span>
                <span>{new Date(collection.created_at).toLocaleString()}</span>
                {(friendlyName || collection.name) && (
                  <span className="text-zinc-300 font-medium">{friendlyName || collection.name}</span>
                )}
              </div>
              <span className="text-xs text-zinc-600 font-mono truncate max-w-xs">{viewHash}</span>
            </div>
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-zinc-800 text-zinc-500 text-xs">
                  <th className="text-left py-2.5 px-5 font-medium">Path</th>
                  <th className="text-left py-2.5 px-5 font-medium w-48">Hash</th>
                  <th className="text-right py-2.5 px-5 font-medium w-20">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800">
                {collection.entries.map((entry, idx) => (
                  <tr key={idx} className="hover:bg-zinc-800/50 transition-colors">
                    <td className="py-2.5 px-5 font-mono text-zinc-300">{entry.path}</td>
                    <td className="py-2.5 px-5 font-mono text-xs text-zinc-500 truncate max-w-[12rem]">{entry.hash}</td>
                    <td className="py-2.5 px-5 text-right">
                      <a href={api.getAnonFileDownloadUrl(viewHash, entry.path)} target="_blank" rel="noreferrer"
                        className="text-blue-400 hover:text-blue-300 text-xs transition-colors">下载</a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {collection && (
          <div className="mt-4">
            <details className="group">
              <summary className="cursor-pointer text-sm text-zinc-400 hover:text-zinc-200 transition-colors select-none">
                Fork 此合集
              </summary>
              <form onSubmit={handleFork} className="mt-3 bg-zinc-900 border border-zinc-800 rounded-lg p-4 space-y-3">
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-xs text-zinc-500 mb-1">新增文件 Path</label>
                    <input value={forkAddPath} onChange={e => setForkAddPath(e.target.value)} placeholder="new.txt"
                      className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                               focus:outline-none focus:border-zinc-500" />
                  </div>
                  <div>
                    <label className="block text-xs text-zinc-500 mb-1">新增文件 Hash</label>
                    <input value={forkAddHash} onChange={e => setForkAddHash(e.target.value)} placeholder="sha256..."
                      className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                               focus:outline-none focus:border-zinc-500" />
                  </div>
                </div>
                <div>
                  <label className="block text-xs text-zinc-500 mb-1">移除文件 Path</label>
                  <input value={forkRemovePath} onChange={e => setForkRemovePath(e.target.value)} placeholder="old.txt"
                    className="w-full bg-zinc-950 border border-zinc-700 rounded px-3 py-2 text-sm font-mono
                             focus:outline-none focus:border-zinc-500" />
                </div>
                <button type="submit"
                  className="bg-purple-600 text-white px-4 py-2 rounded-lg text-sm font-medium hover:bg-purple-500 transition-colors">
                  执行 Fork
                </button>
              </form>
            </details>
          </div>
        )}
      </section>
    </div>
  )
}
