// 移动端底部导航栏（md 断点以下可见）
import React from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

const tabs = [
  { path: '/', label: 'Home', icon: '🏠' },
  { path: '/files', label: 'Files', icon: '📁' },
  { path: '/create', label: 'Create', icon: '➕' },
  { path: '/p2p', label: 'P2P', icon: '🌐' },
  { path: '/settings', label: 'Settings', icon: '⚙' },
];

export default function MobileNav() {
  const location = useLocation();
  const navigate = useNavigate();

  // 设置页面全屏显示，隐藏底部导航
  if (location.pathname.startsWith('/settings')) return null;

  const isActive = (tabPath) => {
    if (tabPath === '/') return location.pathname === '/';
    return location.pathname.startsWith(tabPath);
  };

  return (
    <nav
      className="fixed bottom-0 left-0 right-0 z-50 md:hidden bg-gray-900/95 backdrop-blur-md border-t border-gray-800 mobile-nav-safe"
      style={{ paddingBottom: 'env(safe-area-inset-bottom, 0px)' }}
    >
      <div className="flex items-center justify-around h-16 max-w-lg mx-auto">
        {tabs.map((tab) => {
          const active = isActive(tab.path);
          return (
            <button
              key={tab.path}
              onClick={() => navigate(tab.path)}
              className={`flex flex-col items-center justify-center flex-1 min-h-[44px] px-1 py-1 rounded-lg transition-colors touch-friendly ${
                active
                  ? 'text-blue-400'
                  : 'text-gray-500 hover:text-gray-300 active:text-gray-400'
              }`}
              aria-label={tab.label}
              title={tab.label}
            >
              <span className="text-xl leading-none mb-0.5">{tab.icon}</span>
              <span className="text-[10px] leading-tight font-medium">{tab.label}</span>
              {active && (
                <span className="absolute top-0 left-1/2 -translate-x-1/2 w-6 h-0.5 bg-blue-400 rounded-full" />
              )}
            </button>
          );
        })}
      </div>
    </nav>
  );
}
