// Frontend rebuild (Module 1, 2026-09-25): app shell + routing.
// Home page = node search / connect (PeerJS consumer); other modules are being
// rebuilt one by one (in-construction placeholder).
import React, { useEffect } from 'react';
import { BrowserRouter, Routes, Route, Link } from 'react-router-dom';
import { registerSW } from './lib/swBridge';
import ConnectionStatus from './lib/ConnectionStatus';
import Connect from './pages/Connect';
import Drive from './pages/Drive';
import Collections from './features/collection/pages/Collections';
import CollectionView from './features/collection/pages/CollectionView';
import Settings from './pages/Settings';
import Transfers from './pages/Transfers';
import BT from './pages/BT';
import IPFS from './pages/IPFS';
import NodeControl from './pages/NodeControl';

// In-construction placeholder page (Modules 2, 3, 4, 5, 6 to be replaced one by one)
function Placeholder({ title }) {
  return (
    <div className="p-8 h-full overflow-y-auto">
      <div className="max-w-3xl mx-auto text-center py-24">
        <h2 className="text-xl font-semibold text-gray-200 mb-3">{title}</h2>
        <p className="text-sm text-gray-500">Module under construction, being rebuilt one by one.</p>
      </div>
    </div>
  );
}

// Navigation only keeps currently active entries (2026-09-26: netdisk/collections/
// transfers/BT/IPFS/settings are temporarily unused and hidden from nav; page
// routes are kept, so direct URL access still works)
const NAV = [
  { to: '/', label: 'Connect' },
];

function Nav() {
  return (
    <nav className="h-14 bg-surface-raised/70 backdrop-blur-xl border-b border-white/[0.06] flex items-center px-3 md:px-6 gap-1 md:gap-2 shrink-0">
      <Link to="/" className="text-lg font-bold bg-gradient-to-r from-brand-300 via-brand-400 to-cyan-300 bg-clip-text text-transparent tracking-tight mr-4 shrink-0">Peerdrive</Link>
      <div className="hidden md:flex items-center gap-1">
        {NAV.map(it => (
          <Link key={it.to} to={it.to}
            className="text-sm text-gray-400 hover:text-white px-2.5 py-1.5 rounded-lg hover:bg-white/[0.06] transition-colors">{it.label}</Link>
        ))}
      </div>
      <div className="md:hidden flex items-center gap-1 ml-auto">
        {NAV.map(it => (
          <Link key={it.to} to={it.to} className="text-xs text-gray-400 hover:text-white px-2 py-1.5 rounded hover:bg-white/[0.06]">{it.label}</Link>
        ))}
      </div>
      {/* Live local-node session pill (audit P7): subscribes to ws.onStatus — the nav
          used to carry zero connection signal. ml-auto right-aligns it on desktop (the
          md:hidden block above it is display:none there, so two ml-autos don't fight);
          on mobile it shares the trailing space with the compact nav links. */}
      <ConnectionStatus />
    </nav>
  );
}

export default function App() {
  useEffect(() => { registerSW(); }, []);
  return (
    <BrowserRouter basename={import.meta.env.BASE_URL}>
      <div className="flex flex-col h-screen text-gray-200">
        <Nav />
        <div className="flex-1 overflow-hidden">
          <Routes>
            <Route path="/" element={<Connect />} />
            <Route path="/node" element={<NodeControl />} />
            <Route path="/drive" element={<Drive />} />
            <Route path="/collections" element={<Collections />} />
            <Route path="/collection" element={<CollectionView />} />
            <Route path="/collection/:hash" element={<CollectionView />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/transfers" element={<Transfers />} />
            <Route path="/bt" element={<BT />} />
            <Route path="/ipfs" element={<IPFS />} />
            <Route path="*" element={<Placeholder title="Page Not Found" />} />
          </Routes>
        </div>
      </div>
    </BrowserRouter>
  );
}
