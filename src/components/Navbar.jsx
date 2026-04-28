// 顶部导航栏 + 全局搜索面板（Ctrl+K 打开）
import React, { useState, useEffect, useRef } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import * as api from '../api';
import { listAnonCollections, searchCollections, listFiles } from '../api';

// 全局搜索面板：搜索合集和文件，键盘导航选择
function SearchPanel({ open, onClose }) {
  const [q, setQ] = useState('');
  const [results, setResults] = useState({ anon: [], public: [], files: [] });
  const [loading, setLoading] = useState(false);
  const [activeIdx, setActiveIdx] = useState(0);
  const inputRef = useRef(null);
  const nav = useNavigate();

  useEffect(() => {
    if (open) {
      setQ('');
      setResults({ anon: [], public: [], files: [] });
      setActiveIdx(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  useEffect(() => {
    if (!q.trim()) { setResults({ anon: [], public: [], files: [] }); return; }
    const t = setTimeout(async () => {
      setLoading(true);
      try {
        const [anon, pub, flist] = await Promise.all([
          listAnonCollections().then(d => (d || []).filter(c =>
            (c.friendly_name || '').toLowerCase().includes(q.toLowerCase()) ||
            (c.hash || '').toLowerCase().includes(q.toLowerCase())
          ).slice(0, 3)),
          searchCollections(q).then(d => (d.collections || d.data || []).slice(0, 3)),
          listFiles('time').then(d => (d || []).filter(f =>
            (f.filename || '').toLowerCase().includes(q.toLowerCase())
          ).slice(0, 3)),
        ]);
        setResults({ anon, public: pub, files: flist });
      } catch { setResults({ anon: [], public: [], files: [] }); }
      setLoading(false);
    }, 200);
    return () => clearTimeout(t);
  }, [q]);

  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') { onClose(); return; }
      if (!open) return;
      const all = [...results.anon, ...results.public, ...results.files];
      const total = all.length;
      if (e.key === 'ArrowDown') { e.preventDefault(); setActiveIdx(i => Math.min(i + 1, total - 1)); }
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

  const all = [...results.anon, ...results.public, ...results.files];
  const hasAnon = results.anon.length > 0;
  const hasPublic = results.public.length > 0;
  const hasFiles = results.files.length > 0;
  const hasAny = hasAnon || hasPublic || hasFiles;

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
            placeholder="搜索合集、文件..."
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
              <p>输入关键词搜索合集或文件</p>
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

          {hasFiles && (
            <div>
              <div className="px-4 py-1.5 text-[10px] text-gray-500 uppercase tracking-wider bg-gray-800/50">注册文件</div>
              {results.files.map((f, i) => {
                const idx = results.anon.length + results.public.length + i;
                return (
                  <div key={f.hash} onClick={() => { nav('/files'); onClose(); }}
                    className={`flex items-center gap-3 px-4 py-2 cursor-pointer hover:bg-gray-700 ${activeIdx === idx ? 'bg-gray-700' : ''}`}>
                    <span>📄</span>
                    <span className="text-blue-300 truncate">{f.filename}</span>
                    <span className="text-gray-600 text-[10px] ml-auto">{f.mime_type}</span>
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

  return (
    <>
      <nav className="h-14 bg-gray-800 border-b border-gray-700 flex items-center px-6 justify-between shrink-0">
        <div className="flex items-center space-x-4">
          <Link to="/" className="text-xl font-bold text-blue-400 hover:text-blue-300">Peerdrive</Link>
          <div className="flex items-center space-x-1">
            <Link to="/files" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700">文件管理</Link>
            <button onClick={() => nav('/')} className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700">探索合集</button>
            <Link to="/anon/create" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700">创建合集</Link>
            <div className="relative group">
              <Link to="/p2p" className="text-sm text-gray-400 hover:text-white px-2 py-1 rounded hover:bg-gray-700 inline-flex items-center gap-1">
                P2P 网络
                <svg className="w-3 h-3 text-gray-500 group-hover:text-gray-300 transition-transform group-hover:rotate-180" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" /></svg>
              </Link>
              <div className="absolute top-full left-0 mt-1 w-44 bg-gray-800 border border-gray-700 rounded-lg shadow-xl opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all duration-150 z-50">
                <Link to="/p2p" className="block px-4 py-2 text-sm text-gray-300 hover:text-white hover:bg-gray-700 rounded-t-lg">双栈总控</Link>
                <Link to="/p2p/ipfs" className="block px-4 py-2 text-sm text-gray-300 hover:text-white hover:bg-gray-700">IPFS / libp2p</Link>
                <Link to="/p2p/bt/controller" className="block px-4 py-2 text-sm text-gray-300 hover:text-white hover:bg-gray-700">📥 BT 下载器</Link>
                <Link to="/p2p/bt" className="block px-4 py-2 text-sm text-gray-300 hover:text-white hover:bg-gray-700 rounded-b-lg">BT DHT</Link>
              </div>
            </div>
          </div>
        </div>

        <div className="flex items-center space-x-3">
          {/* Auth Status Indicator */}
          {!authLoading && (
            <div className="flex items-center gap-1.5 px-2 py-1 rounded bg-gray-800/60 border border-gray-700/50 text-xs">
              <span className={`w-1.5 h-1.5 rounded-full ${authStatus?.authenticated ? 'bg-green-400' : 'bg-yellow-500'}`} />
              <span className="text-gray-400">
                {authStatus?.authenticated ? `👤 ${authStatus.username || '已认证'}` : '👤 匿名'}
              </span>
            </div>
          )}
          <button
            onClick={() => setSearchOpen(true)}
            className="flex items-center gap-2 bg-gray-700 hover:bg-gray-600 px-3 py-1.5 rounded-md text-xs text-gray-400 min-w-[200px]"
          >
            <span>🔍</span>
            <span>搜索...</span>
            <kbd className="ml-auto text-[10px] text-gray-500 bg-gray-800 px-1.5 py-0.5 rounded">Ctrl+K</kbd>
          </button>
          <Link to="/settings" className="text-gray-500 hover:text-gray-300 text-sm" title="设置">⚙</Link>
        </div>
      </nav>
      <SearchPanel open={searchOpen} onClose={() => setSearchOpen(false)} />
    </>
  );
}
