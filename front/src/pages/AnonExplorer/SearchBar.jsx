import { extractHash } from './utils';

export default function SearchBar({ inputVal, loading, onChange, onSearch, onBack }) {
  const handlePaste = (e) => {
    const h = extractHash(e.clipboardData.getData('text'));
    if (h) {
      e.preventDefault();
      onChange(h);
    }
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') onSearch();
  };

  return (
    <div className="h-14 flex items-center px-4 shrink-0 gap-3 border-b border-gray-800">
      <button onClick={onBack} className="text-gray-400 hover:text-white text-lg">←</button>
      <input type="text" value={inputVal}
        onChange={e => onChange(e.target.value)}
        onPaste={handlePaste}
        onKeyDown={handleKeyDown}
        placeholder="输入 SHA256 Hash 粘贴合集链接"
        className="flex-1 bg-gray-800 border border-gray-600 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500" />
      <button onClick={onSearch} disabled={loading}
        className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50">
        {loading ? '...' : '查看'}
      </button>
    </div>
  );
}
