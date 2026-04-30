export default function SearchHistory({ visible, searchHistory, onSelect, onClear }) {
  if (!visible || searchHistory.length === 0) return null;
  return (
    <div className="absolute top-full left-2 right-2 bg-gray-800 border border-gray-700 rounded shadow-lg z-20 max-h-32 overflow-y-auto">
      <div className="text-[10px] text-gray-500 px-2 py-0.5">最近搜索</div>
      {searchHistory.map((h, i) => (
        <div key={i} className="px-3 py-1 text-xs text-gray-400 hover:bg-gray-700 cursor-pointer"
          onMouseDown={() => onSelect(h)}>
          🕐 {h}
        </div>
      ))}
      <div className="text-[10px] text-gray-600 px-2 py-0.5 cursor-pointer hover:text-red-400 border-t border-gray-700"
        onMouseDown={onClear}>清除历史</div>
    </div>
  );
}
