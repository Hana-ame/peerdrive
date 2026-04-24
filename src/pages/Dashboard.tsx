import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { listCollections, createCollection } from '../api/client'
import { usePeerdrive } from '../context/PeerdriveContext'
import toast from 'react-hot-toast'

export default function Dashboard() {
  const { username, setUsername } = usePeerdrive()
  const [collections, setCollections] = useState<any[]>([])
  const [newName, setNewName] = useState('')
  const [usernameInput, setUsernameInput] = useState(username)

  useEffect(() => {
    if (username) {
      listCollections(username).then(r => setCollections(r.data || [])).catch(() => {})
    }
  }, [username])

  const handleCreate = async () => {
    if (!newName.trim() || !username.trim()) return
    try {
      await createCollection(username, newName.trim())
      toast.success('Collection created')
      setNewName('')
      const r = await listCollections(username)
      setCollections(r.data || [])
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handleSetUser = () => {
    setUsername(usernameInput.trim())
    toast.success('Username set')
  }

  return (
    <div className="p-4">
      <h1 className="text-2xl font-bold mb-4">Peerdrive Dashboard</h1>

      <div className="mb-6 flex gap-2 items-center">
        <input
          value={usernameInput}
          onChange={e => setUsernameInput(e.target.value)}
          placeholder="Your username"
          className="border p-2 rounded"
        />
        <button onClick={handleSetUser} className="bg-blue-600 text-white px-4 py-2 rounded">
          Set
        </button>
      </div>

      {username && (
        <>
          <div className="mb-6 flex gap-2 items-center">
            <input
              value={newName}
              onChange={e => setNewName(e.target.value)}
              placeholder="New collection name"
              className="border p-2 rounded flex-1"
            />
            <button onClick={handleCreate} className="bg-green-600 text-white px-4 py-2 rounded">
              Create
            </button>
          </div>

          <div className="grid gap-4">
            {collections.map((c: any) => (
              <Link
                key={c.id}
                to={`/collections/${username}/${c.collection_name}`}
                className="block border rounded p-4 hover:shadow"
              >
                <h2 className="text-lg font-semibold">{c.collection_name}</h2>
                <p className="text-sm text-gray-500">{c.created_at}</p>
              </Link>
            ))}
            {collections.length === 0 && <p className="text-gray-400">No collections yet</p>}
          </div>
        </>
      )}

      <div className="mt-8 flex gap-4">
        <Link to="/p2p" className="text-blue-600 underline">P2P Info</Link>
        <Link to="/fork" className="text-blue-600 underline">Fork Collection</Link>
      </div>
    </div>
  )
}
