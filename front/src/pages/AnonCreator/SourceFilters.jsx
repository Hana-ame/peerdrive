import { SORT_OPTS, TYPE_OPTS } from './constants';

export default function SourceFilters({ sort, typeF, search, filteredCount, onSort, onType, onSearch }) {
  return (
    <div className="p-2 border-b border-gray-800 shrink-0 space-y-1.5">
      <div className="flex gap-1">
        {SORT_OPTS.map(o => (
          <button key={o.v} onClick={() => onSort(o.v)} className={`flex-1 text-xs px-2 py-1 rounded ${sort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
        ))}
      </div>
      <div className="flex gap-1">
        {TYPE_OPTS.map(o => (
          <button key={o.v} onClick={() => onType(o.v)} className={`flex-1 text-xs px-2 py-1 rounded ${typeF===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
        ))}
      </div>
      <input value={search} onChange={e => onSearch(e.target.value)} placeholder="搜索文件名..." className="w-full bg-gray-800 text-xs px-3 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
      <span className="text-[10px] text-gray-600">{filteredCount} 个文件</span>
    </div>
  );
}
