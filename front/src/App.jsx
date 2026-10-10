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
const Display = lazy(() => import('./features/display/pages/Display'));

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

// Navigation groups (Issue #77 / Issue #265):
// - Always available (standalone/consumer pages): Connect (/), Display (/display)
// - Available for Local Node Owner (WS open): Drive, NodeControl, Collections, Transfers, BT, IPFS, Iwara, Settings
// - Available for Remote Guest (WebRTC Peer): Shared Collections, Shared Drive, Transfers
const ALWAYS_NAV = [
  { to: '/', label: 'Connect', icon: '🌐' },
  { to: '/display', label: 'Display', icon: '📺' },
];

const OWNER_NAV = [
  { to: '/drive', label: 'Drive', icon: '☁️' },
  { to: '/node', label: 'Node', icon: '💻' },
  { to: '/collections', label: 'Collections', icon: '📦' },
  { to: '/transfers', label: 'Transfers', icon: '⚡' },
  { to: '/bt', label: 'BT', icon: '🧲' },
  { to: '/ipfs', label: 'IPFS', icon: '🧊' },
  { to: '/iwara', label: 'Iwara', icon: '🎬' },
  { to: '/settings', label: 'Settings', icon: '⚙️' },
];

const GUEST_NAV = [
  { to: '/collections', label: 'Collections', icon: '📦' },
  { to: '/drive', label: 'Drive', icon: '☁️' },
  { to: '/transfers', label: 'Transfers', icon: '⚡' },
];

import { MobileNavDrawer, MobileBottomBar } from './components/MobileNav';

function Nav({ onOpenDrawer, isOpen, isGuest, peerId, navItems }) {
  return (
    <nav className="h-14 bg-surface-raised/70 backdrop-blur-xl border-b border-white/[0.06] flex items-center px-3 md:px-6 gap-2 shrink-0 select-none">
      {/* Mobile Drawer Toggle */}
      <button
        type="button"
        onClick={onOpenDrawer}
        className="md:hidden p-2 rounded-lg text-gray-400 hover:text-white hover:bg-white/[0.08] transition-colors -ml-1"
        aria-label="Open menu drawer"
        data-testid="mobile-menu-button"
      >
        <span className="text-lg leading-none">☰</span>
      </button>

      <Link to="/" className="text-lg font-bold bg-gradient-to-r from-brand-300 via-brand-400 to-cyan-300 bg-clip-text text-transparent tracking-tight mr-2 md:mr-4 shrink-0">
        Peerdrive
      </Link>

      {isGuest && peerId && (
        <span className="hidden lg:inline-flex items-center text-[10px] px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-300 border border-emerald-500/20 font-mono mr-2" title={`WebRTC 访客模式: ${peerId}`}>
          访客 · {peerId.slice(0, 8)}
        </span>
      )}

      <div className="hidden md:flex items-center gap-1">
        {ALWAYS_NAV.map(it => (
          <Link key={it.to} to={it.to}
            className="text-sm text-gray-400 hover:text-white px-2.5 py-1.5 rounded-lg hover:bg-white/[0.06] transition-colors">{it.label}</Link>
        ))}
        {isOpen && (
          <>
            <span className="w-px h-4 bg-white/10 mx-1.5" />
            {navItems.map(it => (
              <Link key={it.to} to={it.to}
                className="text-sm text-gray-400 hover:text-white px-2.5 py-1.5 rounded-lg hover:bg-white/[0.06] transition-colors">{it.label}</Link>
            ))}
          </>
        )}
      </div>

      {/* Live local-node session pill */}
      <ConnectionStatus />
    </nav>
  );
}

import { AppProvider } from './context/AppContext';
import { getNodeSession, onNodeSession } from './lib/nodeSession';

export default function App() {
  const [isWsOpen, setIsWsOpen] = React.useState(ws.getStatus() === 'open');
  const [peerSession, setPeerSession] = React.useState(() => getNodeSession());
  const [drawerOpen, setDrawerOpen] = React.useState(false);

  useEffect(() => {
    registerSW();
    const offWs = ws.onStatus((status) => {
      setIsWsOpen(status === 'open');
    });
    const offNode = onNodeSession((sess) => {
      setPeerSession(sess);
    });
    return () => {
      offWs();
      offNode();
    };
  }, []);

  const isPeerOpen = Boolean(peerSession?.client && peerSession?.peerId);
  const isOpen = isWsOpen || isPeerOpen;
  const isGuest = !isWsOpen && isPeerOpen;
  const currentNav = isGuest ? GUEST_NAV : OWNER_NAV;

  return (
    <HashRouter>
      <AppProvider>
        <div className="flex flex-col h-screen text-gray-200">
          <Nav
            onOpenDrawer={() => setDrawerOpen(true)}
            isOpen={isOpen}
            isGuest={isGuest}
            peerId={peerSession?.peerId}
            navItems={currentNav}
          />

          <MobileNavDrawer
            isOpen={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            connected={isOpen}
            navItems={{ always: ALWAYS_NAV, connected: currentNav }}
          />

          <div className="flex-1 overflow-hidden has-mobile-nav md:pb-0">
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
                <Route path="/display" element={<Display />} />
                <Route path="/display/:channel" element={<Display />} />
                <Route path="*" element={<Placeholder title="Page Not Found" />} />
              </Routes>
            </Suspense>
          </div>

          <MobileBottomBar connected={isOpen} />
        </div>
      </AppProvider>
    </HashRouter>
  );
}

