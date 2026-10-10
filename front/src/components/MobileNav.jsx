import React from 'react';
import { Link, useLocation } from 'react-router-dom';

/**
 * MobileNavDrawer — Slide-over drawer menu for mobile viewports (Issue #235).
 *
 * Provides complete access to all pages (always-available and connected pages)
 * on mobile screens when the desktop topbar menu collapses.
 */
export function MobileNavDrawer({ isOpen, onClose, connected, navItems }) {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden" aria-modal="true" role="dialog">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/70 backdrop-blur-sm transition-opacity"
        onClick={onClose}
        data-testid="mobile-drawer-backdrop"
      />

      {/* Drawer panel */}
      <div className="fixed inset-y-0 left-0 max-w-xs w-full bg-surface-card border-r border-white/10 p-5 shadow-2xl flex flex-col justify-between overflow-y-auto">
        <div>
          <div className="flex items-center justify-between pb-4 mb-4 border-b border-white/[0.08]">
            <span className="text-lg font-bold bg-gradient-to-r from-brand-300 via-brand-400 to-cyan-300 bg-clip-text text-transparent">
              Peerdrive
            </span>
            <button
              onClick={onClose}
              className="text-gray-400 hover:text-white p-2 rounded-lg hover:bg-white/[0.08] transition-colors"
              aria-label="Close menu"
            >
              ✕
            </button>
          </div>

          <div className="space-y-1">
            <div className="text-[11px] font-medium text-gray-500 uppercase tracking-wider px-3 py-1">
              General
            </div>
            {navItems.always.map((it) => (
              <Link
                key={it.to}
                to={it.to}
                onClick={onClose}
                className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-gray-300 hover:text-white hover:bg-white/[0.06] transition-colors"
              >
                <span>{it.icon || '📄'}</span>
                <span>{it.label}</span>
              </Link>
            ))}

            {connected && (
              <>
                <div className="text-[11px] font-medium text-gray-500 uppercase tracking-wider px-3 py-1 mt-4">
                  Node Services
                </div>
                {navItems.connected.map((it) => (
                  <Link
                    key={it.to}
                    to={it.to}
                    onClick={onClose}
                    className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-gray-300 hover:text-white hover:bg-white/[0.06] transition-colors"
                  >
                    <span>{it.icon || '📁'}</span>
                    <span>{it.label}</span>
                  </Link>
                ))}
              </>
            )}
          </div>
        </div>

        <div className="pt-4 border-t border-white/[0.08] text-xs text-gray-500 text-center">
          Peerdrive WebRTC Node
        </div>
      </div>
    </div>
  );
}

/**
 * MobileBottomBar — Floating bottom navigation bar for quick mobile switching (Issue #235).
 * Shows core navigation tabs (Connect, Drive, Collections, Transfers) with touch-friendly targets.
 */
export function MobileBottomBar({ connected }) {
  const location = useLocation();

  const tabs = [
    { to: '/', label: 'Connect', icon: '🌐' },
    ...(connected
      ? [
          { to: '/drive', label: 'Drive', icon: '☁️' },
          { to: '/collections', label: 'Collections', icon: '📦' },
          { to: '/transfers', label: 'Transfers', icon: '⚡' },
        ]
      : [{ to: '/display', label: 'Display', icon: '📺' }]),
  ];

  return (
    <nav
      className="md:hidden fixed bottom-0 left-0 right-0 z-40 bg-surface-card/90 backdrop-blur-lg border-t border-white/[0.08] px-2 py-1 flex items-center justify-around mobile-nav-safe"
      data-testid="mobile-bottom-bar"
    >
      {tabs.map((tab) => {
        const isActive = location.pathname === tab.to;
        return (
          <Link
            key={tab.to}
            to={tab.to}
            className={`flex flex-col items-center justify-center flex-1 py-1.5 px-1 rounded-lg transition-colors select-none ${
              isActive
                ? 'text-brand-300 font-medium'
                : 'text-gray-400 hover:text-gray-200'
            }`}
          >
            <span className="text-lg leading-none mb-1">{tab.icon}</span>
            <span className="text-[10px] tracking-tight truncate">{tab.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}
