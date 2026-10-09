// Frontend rebuild (Module 1, 2026-09-25): app shell + routing.
// Home page = node search / connect (PeerJS consumer); other modules are being
// rebuilt one by one (in-construction placeholder).
import React, { useEffect, Suspense, lazy } from 'react';
import { BrowserRouter, Routes, Route, Link } from 'react-router-dom';
import { registerSW } from './lib/swBridge';
import ConnectionStatus from './lib/ConnectionStatus';

const Connect = lazy(() => import('./pages/Connect'));
const Drive = lazy(() => import('./pages/Drive'));
const Collections = lazy(() => import('./features/collection/pages/Collections'));
const CollectionView = lazy(() => import('./features/collection/pages/CollectionView'));
const Settings = lazy(() => import('./pages/Settings'));
const Transfers = lazy(() => import('./pages/Transfers'));
const BT = lazy(() => import('./pages/BT'));
const IPFS = lazy(() => import('./pages/IPFS'));
const NodeControl = lazy(() => import('./pages/NodeControl'));
const Iwara = lazy(() => import('./pages/Iwara'));

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

function RouteLoading() {
  return (
    <div className="p-8 h-full overflow-y-auto flex items-center justify-center">
      <div className="flex items-center gap-3 text-gray-400">
        <div className="w-4 h-4 border-2 border-brand-400 border-t-transparent rounded-full animate-spin" />
        <span className="text-sm">Loading...</span>
      </div>
    </div>
  );
}

// Navigation only keeps currently active entries (2026-09-26: netdisk/collections/
// transfers/BT/IPFS/settings are temporarily unused and hidden from nav; page
// routes are kept, so direct URL access still works). The iwara page is a
// standalone viewer that works without the rest of the netdisk chain (its data
// comes from the echproxy iwara client, not from the local file index), so it
// is linked from the nav.
const NAV = [
  { to: '/', label: 'Connect' },
  { to: '/iwara', label: 'Iwara' },
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
          <Suspense fallback={<RouteLoading />}>
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
              <Route path="/iwara" element={<Iwara />} />
              <Route path="/iwara/:id" element={<Iwara />} />
              <Route path="*" element={<Placeholder title="Page Not Found" />} />
            </Routes>
          </Suspense>
        </div>
      </div>
    </BrowserRouter>
  );
}
