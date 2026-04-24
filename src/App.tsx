import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router-dom'
import { Toaster } from 'react-hot-toast'
import { PeerdriveProvider } from './context/PeerdriveContext'
import Dashboard from './pages/Dashboard'
import CollectionDetail from './pages/CollectionDetail'
import ForkCollection from './pages/ForkCollection'
import P2PInfo from './pages/P2PInfo'
import TaskStatus from './pages/TaskStatus'
import './App.css'

function Navbar() {
  const location = useLocation()
  return (
    <nav className="navbar">
      <Link to="/" className="brand">Peerdrive</Link>
      <div className="nav-links">
        <Link to="/" className={location.pathname === '/' ? 'active' : ''}>Dashboard</Link>
        <Link to="/p2p" className={location.pathname === '/p2p' ? 'active' : ''}>P2P</Link>
        <Link to="/fork" className={location.pathname === '/fork' ? 'active' : ''}>Fork</Link>
        <Link to="/tasks" className={location.pathname === '/tasks' ? 'active' : ''}>Tasks</Link>
      </div>
    </nav>
  )
}

function App() {
  return (
    <PeerdriveProvider>
      <BrowserRouter>
        <Toaster position="top-right" />
        <Navbar />
        <main>
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/collections/:username/:collName" element={<CollectionDetail />} />
            <Route path="/fork" element={<ForkCollection />} />
            <Route path="/p2p" element={<P2PInfo />} />
            <Route path="/tasks" element={<TaskStatus />} />
          </Routes>
        </main>
      </BrowserRouter>
    </PeerdriveProvider>
  )
}

export default App
