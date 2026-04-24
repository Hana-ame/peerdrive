import { useState } from 'react'
import { getNodeInfo, getPeers, ping } from '../api/client'
import toast from 'react-hot-toast'

export default function P2PInfo() {
  const [nodeInfo, setNodeInfo] = useState<any>(null)
  const [peers, setPeers] = useState<any[]>([])
  const [pingResult, setPingResult] = useState('')

  const loadNode = async () => {
    try {
      const r = await getNodeInfo()
      setNodeInfo(r)
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const loadPeers = async () => {
    try {
      const r = await getPeers()
      setPeers(r.peers || [])
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const handlePing = async () => {
    try {
      const r = await ping()
      setPingResult(r)
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  return (
    <div className="p-4">
      <h1 className="text-2xl font-bold mb-4">P2P Info</h1>
      <div className="flex gap-2 mb-4">
        <button onClick={loadNode} className="bg-blue-600 text-white px-4 py-2 rounded">Node Info</button>
        <button onClick={loadPeers} className="bg-green-600 text-white px-4 py-2 rounded">Peers</button>
        <button onClick={handlePing} className="bg-orange-600 text-white px-4 py-2 rounded">Ping</button>
      </div>
      {nodeInfo && (
        <div className="mb-4">
          <h2 className="font-bold">Node</h2>
          <pre className="bg-gray-100 p-2 rounded text-xs">{JSON.stringify(nodeInfo, null, 2)}</pre>
        </div>
      )}
      {peers.length > 0 && (
        <div className="mb-4">
          <h2 className="font-bold">Peers ({peers.length})</h2>
          <pre className="bg-gray-100 p-2 rounded text-xs">{JSON.stringify(peers, null, 2)}</pre>
        </div>
      )}
      {pingResult && (
        <div className="mb-4">
          <h2 className="font-bold">Ping</h2>
          <pre className="bg-gray-100 p-2 rounded text-xs">{pingResult}</pre>
        </div>
      )}
    </div>
  )
}
