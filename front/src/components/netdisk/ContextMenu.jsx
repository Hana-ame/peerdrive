import React, { useEffect, useRef } from 'react';

/**
 * ContextMenu — Floating context menu with viewport-boundary flipping and keyboard escape.
 * Inspired by modern netdisk interfaces (Nextcloud, Alist, Google Drive).
 */
export default function ContextMenu({ x, y, items, onClose }) {
  const menuRef = useRef(null);

  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') {
        onClose();
      }
    };
    const handleClickOutside = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        onClose();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    window.addEventListener('pointerdown', handleClickOutside);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      window.removeEventListener('pointerdown', handleClickOutside);
    };
  }, [onClose]);

  // Adjust position to stay within viewport bounds
  let left = x;
  let top = y;
  if (typeof window !== 'undefined') {
    const width = 180;
    const height = (items?.length || 5) * 36 + 16;
    if (left + width > window.innerWidth) {
      left = Math.max(8, window.innerWidth - width - 8);
    }
    if (top + height > window.innerHeight) {
      top = Math.max(8, window.innerHeight - height - 8);
    }
  }

  return (
    <div
      ref={menuRef}
      data-testid="drive-context-menu"
      style={{ left: `${left}px`, top: `${top}px` }}
      className="fixed z-50 min-w-[180px] bg-[#1a1d24]/95 backdrop-blur-md border border-white/10 rounded-xl shadow-2xl py-1.5 text-xs text-gray-200 select-none animate-in fade-in zoom-in-95 duration-100"
      onClick={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.preventDefault()}
    >
      {items.map((item, idx) => {
        if (item.divider) {
          return <div key={`div-${idx}`} className="my-1 border-t border-white/[0.08]" />;
        }
        return (
          <button
            key={item.label || idx}
            disabled={item.disabled}
            onClick={() => {
              onClose();
              item.onClick?.();
            }}
            className={`w-full px-3 py-1.5 flex items-center gap-2 text-left transition-colors ${
              item.danger
                ? 'text-red-400 hover:bg-red-500/15 hover:text-red-300'
                : 'text-gray-300 hover:bg-white/[0.08] hover:text-white'
            } ${item.disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer'}`}
          >
            {item.icon && <span className="text-sm shrink-0 w-4 text-center">{item.icon}</span>}
            <span className="truncate">{item.label}</span>
          </button>
        );
      })}
    </div>
  );
}
