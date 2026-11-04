import React from 'react';
import { CATEGORIES } from '../utils/categories';

export default function DriveCategoryBar({
  selectedCategory,
  setSelectedCategory,
  searchQuery,
  setSearchQuery,
  selectedTag,
  setSelectedTag,
  allTagsWithCounts,
}) {
  return (
    <>
      {/* Category Toolbar (All / Documents / Images / Videos / Audio / Archives / Code) */}
      <div className="flex items-center gap-1.5 overflow-x-auto pb-2 mb-3 text-xs border-b border-white/[0.06]">
        {CATEGORIES.map(cat => (
          <button
            key={cat.id}
            onClick={() => setSelectedCategory(cat.id)}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg whitespace-nowrap transition-colors ${
              selectedCategory === cat.id
                ? 'bg-brand-600/30 text-brand-300 border border-brand-500/40 font-medium'
                : 'text-gray-400 hover:text-white hover:bg-white/[0.04]'
            }`}
          >
            <span>{cat.icon}</span>
            <span>{cat.label}</span>
          </button>
        ))}
      </div>

      {/* Search & Tag Filter Bar */}
      <div className="mb-4 space-y-2">
        <div className="relative">
          <span className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500 text-xs">
            🔍
          </span>
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search files by name, hash, or tags..."
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
              All
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
    </>
  );
}
