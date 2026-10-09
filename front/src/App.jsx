// Frontend rebuild (Module 1, 2026-09-25): app shell + routing.
// Home page = node search / connect (PeerJS consumer); other modules are being
// rebuilt one by one (in-construction placeholder).
import React, { useEffect, Suspense, lazy } from 'react';
import { HashRouter, Routes, Route, Link } from 'react-router-dom';
import { registerSW } from './platform/shared/swBridge';
import ConnectionStatus from './lib/ConnectionStatus';

const Connect = lazy(() => import('./features/node/pages/Connect'));
const Drive = lazy(() => import('./features/drive/pages/Drive'));
const Collections = lazy(() => import('./features/collection/pages/Collections'));
const CollectionView = lazy(() => import('./features/collection/pages/CollectionView'));
const Settings = lazy(() => import('./features/settings/pages/Settings'));
const Transfers = lazy(() => import('./features/transfers/pages/Transfers'));
const BT = lazy(() => import('./features/bt/pages/BT'));
const IPFS = lazy(() => import('./features/ipfs/pages/IPFS'));
const NodeControl = lazy(() => import('./features/node/pages/NodeControl'));
const Iwara = lazy(() => import('./features/iwara/pages/Iwara'));

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

import * as ws from './platform/transport-ws';

// Navigation groups (Issue #77):
// - Always available (standalone/consumer pages): Connect (/), Iwara (/iwara)
// - Available when connected to local node: Drive, NodeControl, Collections, Transfers, BT, IPFS, Settings
const ALWAYS_NAV = [
  { to: '/', label: 'Connect' },
  { to: '/iwara', label: 'Iwara' },
];

const CONNECTED_NAV = [
  { to: '/drive', label: 'Drive' },
  { to: '/node', label: 'Node' },
  { to: '/collections', label: 'Collections' },
  { to: '/transfers', label: 'Transfers' },
  { to: '/bt', label: 'BT' },
  { to: '/ipfs', label: 'IPFS' },
  { to: '/settings', label: 'Settings' },
];

function Nav() {
  const [isOpen, setIsOpen] = React.useState(ws.getStatus() === 'open');

  React.useEffect(() => {
    return ws.onStatus((status) => {
      setIsOpen(status === 'open');
    });
  }, []);

  return (
    <nav className="h-14 bg-surface-raised/70 backdrop-blur-xl border-b border-white/[0.06] flex items-center px-3 md:px-6 gap-1 md:gap-2 shrink-0">
      <Link to="/" className="text-lg font-bold bg-gradient-to-r from-brand-300 via-brand-400 to-cyan-300 bg-clip-text text-transparent tracking-tight mr-4 shrink-0">Peerdrive</Link>
      <div className="hidden md:flex items-center gap-1">
        {ALWAYS_NAV.map(it => (
          <Link key={it.to} to={it.to}
            className="text-sm text-gray-400 hover:text-white px-2.5 py-1.5 rounded-lg hover:bg-white/[0.06] transition-colors">{it.label}</Link>
        ))}
        {isOpen && (
          <>
            <span className="w-px h-4 bg-white/10 mx-1.5" />
            {CONNECTED_NAV.map(it => (
              <Link key={it.to} to={it.to}
                className="text-sm text-gray-400 hover:text-white px-2.5 py-1.5 rounded-lg hover:bg-white/[0.06] transition-colors">{it.label}</Link>
            ))}
          </>
        )}
      </div>
      <div className="md:hidden flex items-center gap-1 ml-auto">
        {ALWAYS_NAV.map(it => (
          <Link key={it.to} to={it.to} className="text-xs text-gray-400 hover:text-white px-2 py-1.5 rounded hover:bg-white/[0.06]">{it.label}</Link>
        ))}
        {isOpen && (
          <div className="flex items-center gap-1 border-l border-white/10 pl-1 ml-1">
            <Link to="/drive" className="text-xs text-gray-400 hover:text-white px-2 py-1.5 rounded hover:bg-white/[0.06]">Drive</Link>
            <Link to="/collections" className="text-xs text-gray-400 hover:text-white px-2 py-1.5 rounded hover:bg-white/[0.06]">Collections</Link>
          </div>
        )}
      </div>
      {/* Live local-node session pill */}
      <ConnectionStatus />
    </nav>
  );
}

import { AppProvider } from './context/AppContext';

export default function App() {
  useEffect(() => { registerSW(); }, []);
  return (
    <HashRouter>
      <AppProvider>
        <div className="flex flex-col h-screen text-gray-200">
          <Nav />
          <div className="flex-1 overflow-hidden">
            <Suspense fallback={<RouteLoading />}>
            <Routes>
              <Route path="/" element={<Connect />} />
              <Route path="/node" element={<NodeControl />} />
              <Route path="/drive" element={<Drive />} />
              <Route path="/drive/:hash" element={<Drive />} />
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
    </AppProvider>
  </HashRouter>
);
}
