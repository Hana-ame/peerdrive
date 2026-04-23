import { useState } from 'react'
import { API_BASE } from './config'
import './App.css'

function App() {
  const [status, setStatus] = useState<string>('')
  const [nodeInfo, setNodeInfo] = useState<{ peer_id: string; addrs: string[] } | null>(null)
  const [loading, setLoading] = useState(false)

  const checkPing = async () => {
    setLoading(true)
    try {
      const res = await fetch(`${API_BASE}/ping`)
      const text = await res.text()
      setStatus(text)
    } catch (e) {
      setStatus('Error: ' + (e as Error).message)
    }
    setLoading(false)
  }

  const getNodeInfo = async () => {
    setLoading(true)
    try {
      const res = await fetch(`${API_BASE}/p2p/node`)
      const data = await res.json()
      setNodeInfo(data)
    } catch (e) {
      setStatus('Error: ' + (e as Error).message)
    }
    setLoading(false)
  }

  return (
    <>
      <section id="center">
        <h1>Peerdrive</h1>
        <p>P2P 文件分享系统</p>
      </section>

      <section id="controls">
        <button onClick={checkPing} disabled={loading}>
          Check /ping
        </button>
        <button onClick={getNodeInfo} disabled={loading}>
          Get /p2p/node
        </button>
      </section>

      {status && (
        <section id="result">
          <h3>/ping Response:</h3>
          <pre>{status}</pre>
        </section>
      )}

      {nodeInfo && (
        <section id="result">
          <h3>/p2p/node Response:</h3>
          <pre>{JSON.stringify(nodeInfo, null, 2)}</pre>
        </section>
      )}
    </>
  )
}

export default App