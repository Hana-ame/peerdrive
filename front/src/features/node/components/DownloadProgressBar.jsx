import React from 'react';

export default function DownloadProgressBar({ downloading }) {
  if (!downloading) return null;

  const pct = downloading.total
    ? Math.min(100, Math.round((downloading.loaded / downloading.total) * 100))
    : 0;

  return (
    <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-50 card-surface px-5 py-3 flex items-center gap-3 shadow-xl border border-white/10">
      <p className="text-xs text-gray-300 max-w-[240px] truncate">Downloading: {downloading.name}</p>
      <div className="w-48 h-1.5 bg-white/[0.08] rounded overflow-hidden">
        <div className="h-full bg-brand-500 transition-all" style={{ width: `${pct}%` }} />
      </div>
      <span className="text-xs text-gray-400 w-12 text-right">{pct}%</span>
    </div>
  );
}
