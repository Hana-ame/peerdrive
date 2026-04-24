import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { forkCollection } from '../api/client'
import { usePeerdrive } from '../context/PeerdriveContext'
import toast from 'react-hot-toast'

export default function ForkCollection() {
  const { username } = usePeerdrive()
  const navigate = useNavigate()
  const [localName, setLocalName] = useState('')
  const [srcUser, setSrcUser] = useState('')
  const [srcColl, setSrcColl] = useState('')

  const handleFork = async () => {
    if (!username || !localName || !srcUser || !srcColl) return
    try {
      await forkCollection({
        username,
        collection_name: localName,
        source_username: srcUser,
        source_coll_name: srcColl,
      })
      toast.success('Forked!')
      navigate(`/collections/${username}/${localName}`)
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  return (
    <div className="p-4">
      <h1 className="text-2xl font-bold mb-4">Fork a Remote Collection</h1>

      <div className="space-y-3 max-w-md">
        <div>
          <label className="block text-sm text-gray-600">Your local collection name</label>
          <input value={localName} onChange={e => setLocalName(e.target.value)} className="border p-2 rounded w-full" />
        </div>
        <div>
          <label className="block text-sm text-gray-600">Source username</label>
          <input value={srcUser} onChange={e => setSrcUser(e.target.value)} className="border p-2 rounded w-full" />
        </div>
        <div>
          <label className="block text-sm text-gray-600">Source collection name</label>
          <input value={srcColl} onChange={e => setSrcColl(e.target.value)} className="border p-2 rounded w-full" />
        </div>
        <button onClick={handleFork} className="bg-purple-600 text-white px-6 py-2 rounded">
          Fork
        </button>
      </div>
    </div>
  )
}
