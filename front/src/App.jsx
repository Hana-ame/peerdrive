// 前端重做（模块①，2026-09-25）：应用外壳 + 路由。
// 首页 = 节点搜索 / 连接（PeerJS 消费端）；其余模块按计划逐个重做（建设中占位）。
import React from 'react';
import { BrowserRouter, Routes, Route, Link } from 'react-router-dom';
import Connect from './pages/Connect';
import Drive from './pages/Drive';
import Collections from './pages/Collections';
import Settings from './pages/Settings';

// 建设中占位页（模块②③④⑤⑥逐个替换）
function Placeholder({ title }) {
  return (
    <div className="p-8 h-full overflow-y-auto">
      <div className="max-w-3xl mx-auto text-center py-24">
        <h2 className="text-xl font-semibold text-gray-200 mb-3">{title}</h2>
        <p className="text-sm text-gray-500">模块建设中，正在逐个重做。</p>
      </div>
    </div>
  );
}

const NAV = [
  { to: '/', label: '连接节点' },
  { to: '/drive', label: '我的网盘' },
  { to: '/collections', label: '合集' },
  { to: '/settings', label: '设置' },
  { to: '/transfers', label: '传输任务' },
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
    </nav>
  );
}

export default function App() {
  return (
    <BrowserRouter basename={import.meta.env.BASE_URL}>
      <div className="flex flex-col h-screen text-gray-200">
        <Nav />
        <div className="flex-1 overflow-hidden">
          <Routes>
            <Route path="/" element={<Connect />} />
            <Route path="/drive" element={<Drive />} />
            <Route path="/collections" element={<Collections />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/transfers" element={<Placeholder title="传输任务" />} />
            <Route path="*" element={<Placeholder title="页面不存在" />} />
          </Routes>
        </div>
      </div>
    </BrowserRouter>
  );
}