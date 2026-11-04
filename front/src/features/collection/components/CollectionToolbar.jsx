import React from 'react';

export default function CollectionToolbar({
  searchQuery,
  setSearchQuery,
  allTagsWithCounts,
  selectedTag,
  setSelectedTag,
  totalCollections,
}) {
  return (
    <div className="mb-4 space-y-3">
      <div className="flex items-center gap-3">
        <div className="relative flex-1">
          <span className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500">
            🔍
          </span>
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search collections by name, hash, or tags..."
            className="input-base pl-9 w-full text-xs"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute inset-y-0 right-0 pr-3 flex items-center text-gray-500 hover:text-white text-xs"
            >
              ✕
            </button>
          )}
        </div>
      </div>

      {allTagsWithCounts.length > 0 && (
        <div className="flex items-center gap-1.5 flex-wrap text-xs">
          <span className="text-gray-500 text-[11px] mr-1">Tags:</span>
          <button
            onClick={() => setSelectedTag('')}
            className={`px-2 py-0.5 rounded-full border transition-colors text-[11px] ${
              selectedTag === ''
                ? 'bg-brand-600 text-white border-brand-500'
                : 'bg-white/[0.04] text-gray-400 border-white/[0.08] hover:text-white'
            }`}
          >
            All ({totalCollections})
          </button>
          {allTagsWithCounts.map(({ tag, count }) => (
            <button
              key={tag}
              onClick={() => setSelectedTag(selectedTag === tag ? '' : tag)}
              className={`px-2 py-0.5 rounded-full border transition-colors text-[11px] ${
                selectedTag === tag
                  ? 'bg-brand-600 text-white border-brand-500'
                  : 'bg-white/[0.04] text-gray-400 border-white/[0.08] hover:text-white'
              }`}
            >
              #{tag} <span className="opacity-60 text-[10px]">({count})</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
