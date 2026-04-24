import { useState, useEffect } from 'react'
import { useParams } from 'react-router-dom'
import {
  getCollection, addEntry, removeEntry, commitCollection,
  getVersionLog, rollbackCollection, uploadFile, pullCollection,
  mergeFromSource, verifyFile,
} from '../api/client'
import { usePeerdrive } from '../context/PeerdriveContext'
import toast from 'react-hot-toast'

export default function CollectionDetail() {
  const { username: paramUser, collName } = useParams()
  const { username } = usePeerdrive()
  const [collection, setCollection] = useState<any>(null)
  const [entries, setEntries] = useState<any[]>([])
  const [versions, setVersions] = useState<any[]>([])
  const [entryPath, setEntryPath] = useState('')
  const [entryHash, setEntryHash] = useState('')
  const [commitMsg, setCommitMsg] = useState('')
  const [uploading, setUploading] = useState(false)
  const [sourceUser, setSourceUser] = useState('')
  const [sourceColl, setSourceColl] = useState('')

  const load = async () => {
    if (!paramUser || !collName) return
    try {
      const r = await getCollection(paramUser, collName)
      setCollection(r.collection)
      setEntries(r.entries || [])
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const loadVersions = async () => {
    if (!paramUser || !collName) return
    try {
      const r = await getVersionLog(paramUser, collName)
      setVersions(r.data || [])
    } catch {}
  }

  useEffect(() => { load(); loadVersions() }, [paramUser, collName])

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setUploading(true)
    try {
      const r = await uploadFile(file)
      const path = entryPath || file.name
      await addEntry(paramUser!, collName!, path, r.hash)
      toast.success(`Uploaded: ${r.hash.slice(0, 12)}...`)
      setEntryHash(r.hash)
      load()
    } catch (e: any) {
      toast.error(e.message)
    }
    setUploading(false)
  }

  const handleAddEntry = async () => {
    if (!entryPath || !entryHash) return
    try {
      await addEntry(paramUser!, collName!, entryPath, entryHash)
      toast.success('Entry added')
      setEntryPath('')
      setEntryHash('')
      load()
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handleRemove = async (path: string) => {
    try {
      await removeEntry(paramUser!, collName!, path)
      toast.success('Entry removed')
      load()
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handleCommit = async () => {
    if (!commitMsg.trim()) return
    try {
      await commitCollection(paramUser!, collName!, commitMsg)
      toast.success('Committed')
      setCommitMsg('')
      loadVersions()
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handleRollback = async (vid: number) => {
    try {
      await rollbackCollection(paramUser!, collName!, vid)
      toast.success('Rolled back')
      load()
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handlePull = async () => {
    try {
      const r = await pullCollection(paramUser!, collName!)
      toast.success(JSON.stringify(r))
      load()
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handleMerge = async () => {
    if (!sourceUser || !sourceColl) return
    try {
      const r = await mergeFromSource({
        username: paramUser!,
        collection_name: collName!,
        source_username: sourceUser,
        source_coll_name: sourceColl,
        strategy: 'theirs',
      })
      toast.success(`Merged: ${r.message} (conflicts: ${r.conflicts_found})`)
      load()
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  if (!collection) return <p className="p-4">Loading...</p>

  return (
    <div className="p-4">
      <h1 className="text-2xl font-bold mb-1">{paramUser}/{collName}</h1>
      <p className="text-sm text-gray-500 mb-4">Created: {collection.created_at}</p>

      <div className="mb-6 border rounded p-4">
        <h2 className="text-lg font-bold mb-2">Add Entry</h2>
        <div className="flex gap-2 mb-2">
          <input value={entryPath} onChange={e => setEntryPath(e.target.value)} placeholder="Path (e.g. dir/file.txt)" className="border p-2 rounded flex-1" />
          <input value={entryHash} onChange={e => setEntryHash(e.target.value)} placeholder="SHA256 hash" className="border p-2 rounded flex-1" />
          <button onClick={handleAddEntry} className="bg-blue-600 text-white px-4 py-2 rounded">Add</button>
        </div>
        <div>
          <label className="block mb-1 text-sm text-gray-600">Upload file:</label>
          <input type="file" onChange={handleUpload} disabled={uploading} className="block" />
        </div>
      </div>

      <div className="mb-6 border rounded p-4">
        <h2 className="text-lg font-bold mb-2">Entries ({entries.length})</h2>
        <table className="w-full text-sm">
          <thead><tr className="text-left border-b"><th className="py-1">Path</th><th>Hash</th><th></th></tr></thead>
          <tbody>
            {entries.map((e: any) => (
              <tr key={e.id} className="border-b">
                <td className="py-1">{e.path}</td>
                <td className="font-mono text-xs">{e.file_hash.slice(0, 16)}...</td>
                <td>
                  <button onClick={() => handleRemove(e.path)} className="text-red-500 text-xs">Remove</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {entries.length === 0 && <p className="text-gray-400 text-sm mt-2">No entries</p>}
      </div>

      <div className="mb-6 border rounded p-4">
        <h2 className="text-lg font-bold mb-2">Commit</h2>
        <div className="flex gap-2">
          <input value={commitMsg} onChange={e => setCommitMsg(e.target.value)} placeholder="Commit message" className="border p-2 rounded flex-1" />
          <button onClick={handleCommit} className="bg-green-600 text-white px-4 py-2 rounded">Commit</button>
        </div>
      </div>

      <div className="mb-6 border rounded p-4">
        <h2 className="text-lg font-bold mb-2">Versions</h2>
        {versions.map((v: any) => (
          <div key={v.id} className="flex justify-between items-center border-b py-2">
            <div>
              <span className="font-bold">v{v.version_number}</span>
              <span className="text-sm ml-2 text-gray-600">{v.commit_message}</span>
              <span className="text-xs ml-2 text-gray-400">{v.created_at}</span>
            </div>
            <button onClick={() => handleRollback(v.id)} className="text-red-500 text-xs">Rollback</button>
          </div>
        ))}
        {versions.length === 0 && <p className="text-gray-400 text-sm">No versions</p>}
      </div>

      <div className="border rounded p-4">
        <h2 className="text-lg font-bold mb-2">Merge / Pull</h2>
        <div className="flex gap-2 mb-2">
          <input value={sourceUser} onChange={e => setSourceUser(e.target.value)} placeholder="Source username" className="border p-2 rounded flex-1" />
          <input value={sourceColl} onChange={e => setSourceColl(e.target.value)} placeholder="Source collection" className="border p-2 rounded flex-1" />
          <button onClick={handleMerge} className="bg-purple-600 text-white px-4 py-2 rounded">Merge (theirs)</button>
        </div>
        <button onClick={handlePull} className="bg-orange-600 text-white px-4 py-2 rounded text-sm">Pull</button>
      </div>
    </div>
  )
}
