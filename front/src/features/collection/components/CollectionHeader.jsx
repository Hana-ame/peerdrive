import React from 'react';

export default function CollectionHeader({
  navigate,
  isGuest,
  tab,
  setTab,
  viewMode,
  setViewMode,
  loadList,
  canCreate,
  openCreate,
}) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 sm:gap-4 mb-5">
      <div>
        <div className="flex flex-wrap items-center gap-2 sm:gap-3">
          <h1 className="text-2xl font-bold">Collections</h1>
          <div className="flex items-center bg-white/[0.06] p-0.5 rounded-lg text-xs shrink-0 border border-white/[0.08]">
            <button
              type="button"
              onClick={() => navigate('/drive')}
              className="px-2.5 py-1 rounded text-gray-400 hover:text-white transition-colors"
            >
              Files
            </button>
            <span className="px-2.5 py-1 rounded bg-brand-600 text-white font-medium">
              Collections
            </span>
          </div>
          {isGuest && (
            <span className="text-[11px] px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-400 border border-blue-500/20 font-medium shrink-0">
              Visitor (Read Only)
            </span>
          )}
        </div>
        <p className="text-sm text-gray-500 mt-0.5">
          {isGuest
            ? 'Browsing public collections shared by remote peer node'
            : 'Content-addressed versioned file collections (anonymous / hash-addressed)'}
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {tab === 'list' && (
          <div className="flex items-center bg-white/[0.04] p-0.5 rounded-lg border border-white/[0.06] text-xs">
            <button
              onClick={() => setViewMode('list')}
              className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'list' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
              title="List View"
            >
              ☰ List
            </button>
            <button
              onClick={() => setViewMode('grid')}
              className={`px-2.5 py-1 rounded transition-colors ${viewMode === 'grid' ? 'bg-brand-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}
              title="Grid View"
            >
              ▦ Grid
            </button>
          </div>
        )}
        <button
          onClick={loadList}
          className="btn-ghost !px-2.5 !py-1 !text-xs text-gray-400 hover:text-white transition-colors flex items-center gap-1"
          title="Refresh collections"
        >
          <span>🔄</span>
          <span>Refresh</span>
        </button>
        {tab === 'list' ? (
          <button
            disabled={!canCreate}
            onClick={canCreate ? openCreate : undefined}
            className={
              canCreate
                ? 'btn-brand !px-3 !py-1 !text-xs flex items-center gap-1.5 shadow-sm'
                : '!px-3 !py-1 !text-xs flex items-center gap-1.5 rounded-lg bg-white/[0.04] text-gray-500 border border-white/[0.08] cursor-not-allowed select-none opacity-60 transition-colors'
            }
            title={
              canCreate
                ? 'Create a new collection from local files'
                : isGuest
                ? 'Visitors cannot create collections (read-only)'
                : 'Node connection required to create collections'
            }
          >
            <span className={canCreate ? '' : 'opacity-50'}>➕</span>
            <span>New Collection</span>
          </button>
        ) : (
          <button
            onClick={() => setTab('list')}
            className="btn-ghost !px-3 !py-1 !text-xs text-gray-300 hover:text-white flex items-center gap-1.5"
            title="Back to Collections list"
          >
            <span>←</span>
            <span>Back to List</span>
          </button>
        )}
      </div>
    </div>
  );
}
