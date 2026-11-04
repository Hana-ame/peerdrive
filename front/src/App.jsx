// App.jsx — Low-Cognitive-Load Cloud Drive Shell with Direct Canonical URL routing (#/f/:sha256 & #/c/:hash),
// Smart Omni-Jump Bar, Progressive-Disclosure Navigation, and Floating TransferDock.
import React, { useEffect, useState, Suspense, lazy } from 'react';
import { HashRouter, Routes, Route, Link, useNavigate, useLocation } from 'react-router-dom';
import { registerSW } from './platform/shared/swBridge';
import { parseDirectTarget } from './platform/shared';
import ConnectionStatus from './lib/ConnectionStatus';
import * as ws from './platform/transport-ws';
import { I18nProvider, useI18n } from './context/I18nContext';
import LanguageSelector from './components/LanguageSelector';
import ErrorBoundary from './components/ErrorBoundary';
import { MobileNavDrawer, MobileBottomBar } from './components/MobileNav';
import TransferDock from './components/netdisk/TransferDock';
import { AppProvider } from './context/AppContext';
import { getNodeSession, onNodeSession } from './lib/nodeSession';

const Connect = lazy(() => import('./features/node/pages/Connect'));
const Drive = lazy(() => import('./features/drive/pages/Drive'));
const Collections = lazy(() => import('./features/collection/pages/Collections'));
const CollectionView = lazy(() => import('./features/collection/pages/CollectionView'));
const Settings = lazy(() => import('./features/settings/pages/Settings'));
const Transfers = lazy(() => import('./features/transfers/pages/Transfers'));
const DirectFileView = lazy(() => import('./features/direct/pages/DirectFileView'));
const BT = lazy(() => import('./features/bt/pages/BT'));
const IPFS = lazy(() => import('./features/ipfs/pages/IPFS'));
const NodeControl = lazy(() => import('./features/node/pages/NodeControl'));
const Iwara = lazy(() => import('./features/iwara/pages/Iwara'));
const Display = lazy(() => import('./features/display/pages/Display'));

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

const ALWAYS_NAV = [
  { to: '/', label: 'Connect', labelKey: 'nav.connect', icon: '🌐', core: true },
  { to: '/display', label: 'Display', labelKey: 'nav.display', icon: '📺', core: false },
];

// 为什么按 core 与扩展分层（认知负荷优化，Hick's Law）：
// 主工作区仅突出「我的网盘 / 合集管理 / 传输列表」3 个高频核心入口；
// 节点底层控制与可选插件（BT/IPFS/Iwara/设置）收束至右侧次级区，避免 10 个平铺标签造成视觉轰炸。
const OWNER_NAV = [
  { to: '/drive', label: 'Drive', labelKey: 'nav.drive', icon: '☁️', core: true },
  { to: '/collections', label: 'Collections', labelKey: 'nav.collections', icon: '📦', core: true },
  { to: '/transfers', label: 'Transfers', labelKey: 'nav.transfers', icon: '⚡', core: true },
  { to: '/node', label: 'Node', labelKey: 'nav.node', icon: '💻', core: false },
  { to: '/bt', label: 'BT', labelKey: 'nav.bt', icon: '🧲', core: false },
  { to: '/ipfs', label: 'IPFS', labelKey: 'nav.ipfs', icon: '🧊', core: false },
  { to: '/iwara', label: 'Iwara', labelKey: 'nav.iwara', icon: '🎬', core: false },
  { to: '/settings', label: 'Settings', labelKey: 'nav.settings', icon: '⚙️', core: false },
];

const GUEST_NAV = [
  { to: '/drive', label: 'Drive', labelKey: 'nav.drive', icon: '☁️', core: true },
  { to: '/collections', label: 'Collections', labelKey: 'nav.collections', icon: '📦', core: true },
  { to: '/transfers', label: 'Transfers', labelKey: 'nav.transfers', icon: '⚡', core: true },
];

function QuickJumpInput() {
  const navigate = useNavigate();
  const [val, setVal] = useState('');
  const [kind, setKind] = useState('file'); // 'file' | 'collection'

  const handleSubmit = (e) => {
    e.preventDefault();
    const raw = val.trim();
    if (!raw) return;
    // 智能识别：若粘贴内容自带 /f/、/c/、?hash= 等 URL 特征，无需用户手选下拉框即可自动推断
    const target = parseDirectTarget(raw, kind);
    if (target.kind === 'unknown' || !target.route) return;
    navigate(target.route);
    setVal('');
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="hidden lg:flex items-center gap-1 bg-white/[0.04] border border-white/[0.08] rounded-xl px-2.5 py-1 max-w-xs focus-within:border-brand-400/50 transition-colors"
      data-testid="quick-jump-form"
      title="智能直达栏：直接粘贴分享链接、文件 SHA256 或合集 Hash 即可秒开"
    >
      <select
        value={kind}
        onChange={(e) => setKind(e.target.value)}
        aria-label="Direct address target type"
        className="bg-transparent text-[11px] text-gray-400 focus:outline-none cursor-pointer"
      >
        <option value="file" className="bg-gray-900">📄 文件</option>
        <option value="collection" className="bg-gray-900">📦 合集</option>
      </select>
      <input
        type="text"
        value={val}
        onChange={(e) => setVal(e.target.value)}
        placeholder="粘贴分享链接或唯一码直达…"
        className="bg-transparent text-xs text-gray-200 placeholder-gray-500 focus:outline-none w-40 font-mono"
        data-testid="quick-jump-input"
      />
      <button
        type="submit"
        className="text-[11px] px-2 py-0.5 rounded-md bg-brand-500/20 text-brand-300 hover:bg-brand-500/30 transition-colors font-medium"
      >
        直达
      </button>
    </form>
  );
}

function Nav({ onOpenDrawer, isOpen, navItems }) {
  const { t } = useI18n();
  const location = useLocation();
  const coreItems = navItems.filter((it) => it.core);
  const extItems = navItems.filter((it) => !it.core);

  const navLinkCls = (to) => {
    const active = location.pathname === to || (to !== '/' && location.pathname.startsWith(to + '/'));
    return `text-sm px-2.5 py-1.5 rounded-lg transition-colors whitespace-nowrap flex items-center gap-1.5 ${
      active
        ? 'bg-brand-500/20 text-brand-200 font-medium border border-brand-500/30'
        : 'text-gray-400 hover:text-white hover:bg-white/[0.06]'
    }`;
  };

  return (
    <nav className="h-14 bg-surface-raised/85 backdrop-blur-xl border-b border-white/[0.08] flex items-center px-3 md:px-6 gap-2 shrink-0 select-none">
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

      <Link
        to="/"
        className="text-lg font-bold bg-gradient-to-r from-brand-300 via-brand-400 to-cyan-300 bg-clip-text text-transparent tracking-tight mr-2 md:mr-3 shrink-0"
      >
        Peerdrive
      </Link>

      <div className="hidden md:flex items-center gap-1 min-w-0 overflow-x-auto no-scrollbar">
        {ALWAYS_NAV.map((it) => (
          <Link key={it.to} to={it.to} className={navLinkCls(it.to)}>
            <span>{it.labelKey ? t(it.labelKey) : it.label}</span>
          </Link>
        ))}
        {isOpen && (
          <>
            <span className="w-px h-4 bg-white/10 mx-1 shrink-0" />
            {coreItems.map((it) => (
              <Link key={it.to} to={it.to} className={navLinkCls(it.to)}>
                <span>{it.labelKey ? t(it.labelKey) : it.label}</span>
              </Link>
            ))}
            {extItems.length > 0 && (
              <>
                <span className="w-px h-4 bg-white/10 mx-1 shrink-0" />
                {extItems.map((it) => (
                  <Link key={it.to} to={it.to} className={navLinkCls(it.to)}>
                    <span>{it.labelKey ? t(it.labelKey) : it.label}</span>
                  </Link>
                ))}
              </>
            )}
          </>
        )}
      </div>

      {/* Right side: Quick Canonical URL/Hash Jump + Language Selector + Dual-Channel Status */}
      <div className="ml-auto flex items-center gap-1.5 sm:gap-2 shrink-0">
        <QuickJumpInput />
        <div className="hidden sm:flex items-center">
          <LanguageSelector />
        </div>
        <ConnectionStatus />
      </div>
    </nav>
  );
}

function OfflineBanner({ isOnline }) {
  const { t } = useI18n();
  if (isOnline) return null;
  return (
    <div
      role="status"
      className="bg-amber-500/20 border-b border-amber-500/30 text-amber-200 text-xs px-4 py-1.5 flex items-center justify-center gap-2 text-center shrink-0"
    >
      <span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse" />
      <span>{t('offline.banner')}</span>
    </div>
  );
}

export default function App() {
  const [isWsOpen, setIsWsOpen] = React.useState(ws.getStatus() === 'open');
  const [peerSession, setPeerSession] = React.useState(() => getNodeSession());
  const [drawerOpen, setDrawerOpen] = React.useState(false);
  const [isOnline, setIsOnline] = React.useState(() =>
    typeof navigator !== 'undefined' && 'onLine' in navigator ? navigator.onLine : true
  );

  useEffect(() => {
    registerSW();
    const handleOnline = () => setIsOnline(true);
    const handleOffline = () => setIsOnline(false);
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    const offWs = ws.onStatus((status) => {
      setIsWsOpen(status === 'open');
    });
    const offNode = onNodeSession((sess) => {
      setPeerSession(sess);
    });
    return () => {
      window.removeEventListener('online', handleOnline);
      window.removeEventListener('offline', handleOffline);
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
      <I18nProvider>
        <AppProvider>
          <div className="flex flex-col h-screen text-gray-200 w-full max-w-full overflow-x-hidden bg-surface-base">
            <Nav
              onOpenDrawer={() => setDrawerOpen(true)}
              isOpen={isOpen}
              isGuest={isGuest}
              peerId={peerSession?.peerId}
              navItems={currentNav}
            />

            <OfflineBanner isOnline={isOnline} />

            <MobileNavDrawer
              isOpen={drawerOpen}
              onClose={() => setDrawerOpen(false)}
              connected={isOpen}
              navItems={{ always: ALWAYS_NAV, connected: currentNav }}
            />

            <div className="flex-1 min-w-0 overflow-hidden has-mobile-nav md:pb-0 relative">
              <ErrorBoundary>
                <Suspense fallback={<RouteLoading />}>
                  <Routes>
                    <Route path="/" element={<Connect />} />
                    <Route path="/node" element={<NodeControl />} />
                    <Route path="/drive" element={<Drive />} />
                    <Route path="/drive/:hash" element={<Drive />} />
                    {/* Canonical self-contained SHA256 file URL (invariant #2) */}
                    <Route path="/f/:sha256" element={<DirectFileView />} />
                    <Route path="/files/:sha256" element={<DirectFileView />} />
                    {/* Canonical self-contained Collection URL */}
                    <Route path="/c/:hash" element={<CollectionView />} />
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
              </ErrorBoundary>
            </div>

            <TransferDock />
            <MobileBottomBar connected={isOpen} />
          </div>
        </AppProvider>
      </I18nProvider>
    </HashRouter>
  );
}
