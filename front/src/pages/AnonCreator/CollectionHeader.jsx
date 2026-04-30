import { useState } from 'react';
import { Link } from 'react-router-dom';
import { COLL_SORT_OPTS } from './constants';
import SearchHistory from './SearchHistory';
import { addSearchHistory, loadSearchHistory } from './utils';

export default function CollectionHeader({ collSearch, collTagFilter, collSort, selectMode, selectedCount, allCollTags, searchHistory, collectionsLen, onSearch, onTag, onSort, onSelectToggle, onBatchSave, onSearchHistoryUpdate }) {
  const [showHistory, setShowHistory] = useState(false);

  const handleSearch = (val) => {
    onSearch(val);
    setShowHistory(true);
  };

  const handleSelect = (q) => {
    onSearch(q);
    setShowHistory(false);
    addSearchHistory(q);
    if (onSearchHistoryUpdate) onSearchHistoryUpdate(loadSearchHistory());
  };

  const handleClear = () => {
    localStorage.removeItem('peerdrive_search_history');
    if (onSearchHistoryUpdate) onSearchHistoryUpdate([]);
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      addSearchHistory(collSearch);
      if (onSearchHistoryUpdate) onSearchHistoryUpdate(loadSearchHistory());
      setShowHistory(false);
    }
  };

  return (
    <div className="shrink-0 bg-gray-900">
      <div className="flex items-center gap-2 px-3 pt-2 pb-1">
        <h3 className="text-sm font-bold text-gray-300 flex items-center gap-1">📦 合集</h3>
        <span className="text-[10px] text-gray-600">{collectionsLen}</span>
        <div className="flex-1" />
        <button onClick={onSelectToggle} className={`text-[10px] px-2 py-0.5 rounded ${selectMode ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'}`}>
          {selectMode ? '退出选择' : '选择'}
        </button>
        <Link to="/" className="text-[10px] bg-gray-800 text-gray-400 hover:text-white px-2 py-0.5 rounded">广场</Link>
      </div>

      <div className="px-2 pb-1 relative">
        <input value={collSearch} onChange={e => handleSearch(e.target.value)}
          onFocus={() => setShowHistory(true)}
          onBlur={() => setTimeout(() => setShowHistory(false), 200)}
          onKeyDown={handleKeyDown}
          placeholder="搜索合集名称 / tag..." className="w-full bg-gray-800 text-xs px-3 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
        <SearchHistory visible={showHistory && searchHistory.length > 0 && !collSearch} searchHistory={searchHistory}
          onSelect={handleSelect} onClear={handleClear} />
      </div>

      <div className="px-2 pb-1.5 space-y-1">
        <div className="flex gap-0.5">
          {COLL_SORT_OPTS.map(o => (
            <button key={o.v} onClick={() => onSort(o.v)}
              className={`text-[10px] px-2 py-0.5 rounded ${collSort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
          ))}
        </div>
        {allCollTags.length > 0 && (
          <div className="flex gap-0.5 flex-wrap">
            <button onClick={() => onTag('')}
              className={`text-[10px] px-1.5 py-0.5 rounded-full ${!collTagFilter ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400'}`}>全部</button>
            {allCollTags.map(t => (
              <button key={t} onClick={() => onTag(t === collTagFilter ? '' : t)}
                className={`text-[10px] px-1.5 py-0.5 rounded-full ${collTagFilter===t?'bg-blue-600 text-white':'bg-gray-700 text-gray-300 hover:bg-gray-600'}`}>{t}</button>
            ))}
          </div>
        )}
      </div>

      {selectMode && selectedCount > 0 && (
        <div className="px-2 pb-1.5">
          <button onClick={onBatchSave}
            className="w-full text-xs bg-blue-600 hover:bg-blue-700 px-2 py-1 rounded">
            💾 保存选中的 {selectedCount} 个合集到本机
          </button>
        </div>
      )}
    </div>
  );
}
