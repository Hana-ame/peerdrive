// 顶部导航栏 + 全局搜索面板（Ctrl+K 打开）
import React, { useState, useEffect, useRef, useCallback } from 'react';
import { createPortal } from 'react-dom';
import { Link, useNavigate } from 'react-router-dom';
import * as api from '../api';
import { listAnonCollections, searchCollections } from '../api';

// ── 下拉菜单组件（Portal 到 body，避免被 overflow 容器裁剪）──
// 支持 hover（桌面）和 click（触摸）两种交互
function NavDropdown({ label, to, items }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef(null);
  const [pos, setPos] = useState({ top: 0, left: 0 });
  const timerRef = useRef(null);

  const updatePos = useCallback(() => {
    if (triggerRef.current) {
      const r = triggerRef.current.getBoundingClientRect();
      setPos({ top: r.bottom + 4, left: r.left });
    }
  }, []);

  // 桌面端 hover
  const handleEnter = () => {
    clearTimeout(timerRef.current);
    updatePos();
    setOpen(true);
  };
  const handleLeave = () => {
    timerRef.current = setTimeout(() => setOpen(false), 150);
  };

  // 触摸端/桌面端 click 切换
  const handleToggle = (e) => {
    e.preventDefault();
    e.stopPropagation();
    clearTimeout(timerRef.current);
    updatePos();
    setOpen(prev => !prev);
  };

  // 点击外部关闭
  useEffect(() => {
    if (!open) return;
    const handleClick = (e) => {
      if (triggerRef.current && !triggerRef.current.contains(e.target)) {
        setOpen(false);
      }
    };
    // 延迟添加，避免触发当前点击
    const id = setTimeout(() => document.addEventListener('click', handleClick), 0);
    return () => { clearTimeout(id); document.removeEventListener('click', handleClick); };
  }, [open]);

  // 窗口滚动/resize 时更新菜单位置
  useEffect(() => {
    if (!open) return;
    const onUpdate = () => updatePos();
    window.addEventListener('scroll', onUpdate, true);
    window.addEventListener('resize', onUpdate);
    return () => {
      window.removeEventListener('scroll', onUpdate, true);
      window.removeEventListener('resize', onUpdate);
    };
  }, [open, updatePos]);

  return (
    <>
      <div ref={triggerRef} className="relative group flex items-center flex-shrink-0" onMouseEnter={handleEnter} onMouseLeave={handleLeave}>
        <Link to={to} onClick={() => setOpen(false)}
          className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700 inline-flex items-center gap-1">
          {label}
        </Link>
        <button onClick={handleToggle}
          className="text-gray-500 hover:text-gray-300 p-1 rounded hover:bg-gray-700 transition-colors"
          aria-label={`${label} 子菜单`}>
          <svg className={`w-3 h-3 transition-transform ${open ? 'rotate-180' : ''}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
          </svg>
        </button>
      </div>
      {open &&
        createPortal(
          <div
            className="fixed z-[100] w-36 bg-gray-800 border border-gray-700 rounded-lg shadow-xl"
            style={{ top: pos.top, left: pos.left }}
          >
            {items.map((item, i) => (
              <Link key={item.to} to={item.to}
                onClick={() => setOpen(false)}
                className={`block px-4 py-2 text-sm text-gray-300 hover:text-white hover:bg-gray-700 ${i === 0 ? 'rounded-t-lg' : ''} ${i === items.length - 1 ? 'rounded-b-lg' : ''}`}>
                {item.label}
              </Link>
            ))}
          </div>,
          document.body
        )}
    </>
  );
}

// 全局搜索面板：搜索合集和文件，键盘导航选择
function SearchPanel({ open, onClose }) {
  const [q, setQ] = useState('');
  const [results, setResults] = useState({ anon: [], public: [] });
  const [loading, setLoading] = useState(false);
  const [activeIdx, setActiveIdx] = useState(0);
  const inputRef = useRef(null);
  const nav = useNavigate();

  useEffect(() => {
    if (open) {
      setQ('');
      setResults({ anon: [], public: [] });
      setActiveIdx(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  useEffect(() => {
    if (!q.trim()) { setResults({ anon: [], public: [] }); return; }
    const t = setTimeout(async () => {
      setLoading(true);
      try {
        const [anon, pub] = await Promise.all([
          listAnonCollections().then(d => (d || []).filter(c =>
            (c.friendly_name || '').toLowerCase().includes(q.toLowerCase()) ||
            (c.hash || '').toLowerCase().includes(q.toLowerCase())
          ).slice(0, 3)),
          searchCollections(q).then(d => (d.collections || d.data || []).slice(0, 3)),
        ]);
        setResults({ anon, public: pub });
        if (anon.length + pub.length === 0) setActiveIdx(0);
      } catch { setResults({ anon: [], public: [] }); setActiveIdx(0); }
      setLoading(false);
    }, 200);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') { onClose(); return; }
      if (!open) return;
      const all = [...results.anon, ...results.public];
      const total = all.length;
      // 坑：total=0 时旧实现 Math.min(i+1, total-1) → -1，activeIdx 变负（高亮无意义）。
      // 空结果时钳到 0；结果变化后也重置回顶部。
      if (e.key === 'ArrowDown') { e.preventDefault(); setActiveIdx(i => total === 0 ? 0 : Math.min(i + 1, total - 1)); }
      if (e.key === 'ArrowUp') { e.preventDefault(); setActiveIdx(i => Math.max(i - 1, 0)); }
      if (e.key === 'Enter' && all[activeIdx]) {
        const r = all[activeIdx];
        if (r.hash && !r.username) nav(`/anon/collections/${r.hash}`);
        else if (r.username && r.collection_name) nav(`/${r.username}/${r.collection_name}`);
        onClose();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, results, activeIdx, onClose, nav]);

  if (!open) return null;

  const all = [...results.anon, ...results.public];
  const hasAnon = results.anon.length > 0;
  const hasPublic = results.public.length > 0;
  const hasAny = hasAnon || hasPublic;

  return (
    <>
      <div className="fixed inset-0 bg-black/50 z-40" onClick={onClose} />
      <div className="fixed top-[20%] left-1/2 -translate-x-1/2 w-[560px] max-w-[95vw] z-50 bg-gray-800 border border-gray-600 rounded-xl shadow-2xl overflow-hidden">
        <div className="flex items-center px-4 py-3 border-b border-gray-700 gap-3">
          <span className="text-gray-500 text-lg">🔍</span>
          <input
            ref={inputRef}
            value={q}
            onChange={e => { setQ(e.target.value); setActiveIdx(0); }}
            placeholder="搜索合集..."
            className="flex-1 bg-transparent text-sm outline-none text-gray-200 placeholder-gray-500"
          />
          <kbd className="text-[10px] text-gray-500 bg-gray-700 px-1.5 py-0.5 rounded">Esc</kbd>
        </div>

        <div className="max-h-[400px] overflow-y-auto text-sm">
          {loading && <div className="px-4 py-6 text-center text-gray-500 text-xs">搜索中...</div>}

          {!loading && !hasAny && q && (
            <div className="px-4 py-8 text-center text-gray-500 text-xs">未找到匹配结果</div>
          )}

          {!q && !loading && (
            <div className="px-4 py-8 text-center text-gray-600 text-xs">
              <p>输入关键词搜索合集</p>
              <p className="mt-1 text-gray-700">Ctrl+K 快速打开 · ↑↓ 选择 · Enter 打开 · Esc 关闭</p>
            </div>
          )}

          {hasAnon && (
            <div>
              <div className="px-4 py-1.5 text-[10px] text-gray-500 uppercase tracking-wider bg-gray-800/50">我的合集</div>
              {results.anon.map((c, i) => {
                const idx = i;
                return (
                  <div key={c.hash} onClick={() => { nav(`/anon/collections/${c.hash}`); onClose(); }}
                    className={`flex items-center gap-3 px-4 py-2 cursor-pointer hover:bg-gray-700 ${activeIdx === idx ? 'bg-gray-700' : ''}`}>
                    <span>📦</span>
                    <span className="text-blue-300 truncate">{c.friendly_name || c.hash?.substring(0, 12) + '...'}</span>
                    <span className="text-gray-600 text-[10px] ml-auto">匿名</span>
                  </div>
                );
              })}
            </div>
          )}

          {hasPublic && (
            <div>
              <div className="px-4 py-1.5 text-[10px] text-gray-500 uppercase tracking-wider bg-gray-800/50">公开合集</div>
              {results.public.map((c, i) => {
                const idx = results.anon.length + i;
                return (
                  <div key={c.id || c.collection_name} onClick={() => { nav(`/${c.username}/${c.collection_name}`); onClose(); }}
                    className={`flex items-center gap-3 px-4 py-2 cursor-pointer hover:bg-gray-700 ${activeIdx === idx ? 'bg-gray-700' : ''}`}>
                    <span>📦</span>
                    <span className="text-blue-300 truncate">{c.collection_name}</span>
                    <span className="text-gray-600 text-[10px] ml-auto">{c.username}</span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </>
  );
}

export default function Navbar() {
  const nav = useNavigate();
  const [searchOpen, setSearchOpen] = useState(false);
  const [authStatus, setAuthStatus] = useState(null);
  const [authLoading, setAuthLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem('peerdrive_auth_key');
    const headerEnabled = localStorage.getItem('peerdrive_auth_header_enabled') === 'true';
    if (!token || !headerEnabled) {
      setAuthStatus({ authenticated: false, username: '' });
      setAuthLoading(false);
      return;
    }
    (async () => {
      try {
        const status = await api.getAuthStatus();
        setAuthStatus(status);
      } catch {
        setAuthStatus({ authenticated: false, username: '' });
      }
      setAuthLoading(false);
    })();
  }, []);

  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setSearchOpen(true);
      }
      if (e.key === '/' && e.target === document.body && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        setSearchOpen(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // 2026-08-19：P2P 网络下拉（/p2p 系列旧 libp2p 面板）已随 libp2p 端点删除移除——
  // 节点互联状态改看设置页/后端 /peerjs/node；BT/IPFS 面板后端仍在，保留。
  const btItems = [
    { to: '/bt', label: 'BT 下载器' },
    { to: '/bt/status', label: 'BT DHT 状态' },
    { to: '/bt/dht', label: 'BT DHT 采样' },
  ];
  const ipfsItems = [
    { to: '/ipfs', label: 'IPFS 总览' },
  ];

  const allNavItems = [
    { to: '/', label: '合集' },
    { to: '/create', label: '创建合集' },
    { label: 'BT', children: btItems },
    { label: 'IPFS', children: ipfsItems },
  ];

  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);

  // 移动端菜单点击后导航并关闭
  const handleMobileNav = (to) => {
    nav(to);
    setMobileMenuOpen(false);
  };

  return (
    <>
      <nav className="h-14 bg-gray-800 border-b border-gray-700 flex items-center px-3 md:px-6 gap-2 md:gap-4 shrink-0">
        {/* 汉堡菜单按钮（移动端） */}
        <button onClick={() => setMobileMenuOpen(!mobileMenuOpen)}
          className="md:hidden text-gray-400 hover:text-white p-1.5 rounded hover:bg-gray-700 transition-colors"
          aria-label="菜单">
          <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            {mobileMenuOpen
              ? <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              : <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            }
          </svg>
        </button>

        <Link to="/" className="text-lg md:text-xl font-bold text-blue-400 hover:text-blue-300 inline-flex items-center shrink-0">Peerdrive</Link>

        {/* 桌面端导航链接 */}
        <div className="hidden md:flex items-center">
          <Link to="/" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700 inline-flex items-center">合集</Link>
          <Link to="/create" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700 inline-flex items-center">创建合集</Link>
          <NavDropdown label="BT" to="/bt" items={btItems} />
          <NavDropdown label="IPFS" to="/ipfs" items={ipfsItems} />
        </div>

        <div className="flex items-center space-x-2 md:space-x-3 shrink-0 ml-auto">
          {!authLoading && (
            <div className="flex items-center gap-1.5 px-2 py-1 rounded bg-gray-800/60 border border-gray-700/50 text-xs">
              <span className={`w-1.5 h-1.5 rounded-full ${authStatus?.authenticated ? 'bg-green-400' : 'bg-yellow-500'}`} />
              <span className="text-gray-400 whitespace-nowrap">
                {authStatus?.authenticated ? `👤 ${authStatus.username || '已认证'}` : '👤 匿名'}
              </span>
            </div>
          )}
          <button
            onClick={() => setSearchOpen(true)}
            className="flex items-center gap-2 bg-gray-700 hover:bg-gray-600 px-3 py-1.5 rounded-md text-xs text-gray-400 min-w-0 md:min-w-[200px]"
          >
            <span>🔍</span>
            <span className="hidden md:inline">搜索...</span>
            <kbd className="ml-auto text-[10px] text-gray-500 bg-gray-800 px-1.5 py-0.5 rounded hidden md:inline">Ctrl+K</kbd>
          </button>
          <Link to="/settings" className="text-gray-500 hover:text-gray-300 text-sm inline-flex items-center flex-shrink-0" title="设置">⚙</Link>
        </div>
      </nav>

      {/* ── 移动端抽屉菜单 ── */}
      {mobileMenuOpen && createPortal(
        <div className="md:hidden fixed inset-0 z-30" onClick={() => setMobileMenuOpen(false)}>
          <div className="absolute inset-0 bg-black/50" />
          <div className="absolute top-14 left-0 right-0 bg-gray-800 border-b border-gray-700 shadow-xl"
            onClick={(e) => e.stopPropagation()}>
            <div className="py-2">
              {allNavItems.map((item, i) => (
                item.children ? (
                  <div key={i}>
                    <div className="px-4 py-2 text-[10px] text-gray-500 uppercase tracking-wider">{item.label}</div>
                    {item.children.map((child, j) => (
                      <button key={j} onClick={() => handleMobileNav(child.to)}
                        className="w-full text-left px-6 py-2.5 text-sm text-gray-300 hover:text-white hover:bg-gray-700/50 transition-colors">
                        {child.label}
                      </button>
                    ))}
                  </div>
                ) : (
                  <button key={i} onClick={() => handleMobileNav(item.to)}
                    className="w-full text-left px-4 py-2.5 text-sm text-gray-300 hover:text-white hover:bg-gray-700/50 transition-colors">
                    {item.label}
                  </button>
                )
              ))}
            </div>
          </div>
        </div>,
        document.body
      )}

      <SearchPanel open={searchOpen} onClose={() => setSearchOpen(false)} />
    </>
  );
}
