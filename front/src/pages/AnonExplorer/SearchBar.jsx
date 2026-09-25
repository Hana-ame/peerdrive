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
    <div className="h-14 flex items-center px-4 shrink-0 gap-3 border-b border-white/[0.04]">
      <button onClick={onBack} className="text-gray-400 hover:text-white text-lg">←</button>
      <input type="text" value={inputVal}
        onChange={e => onChange(e.target.value)}
        onPaste={handlePaste}
        onKeyDown={handleKeyDown}
        placeholder="输入 SHA256 Hash 粘贴合集链接"
        className="flex-1 bg-white/[0.06] border border-white/[0.12] px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-brand-500" />
      <button onClick={onSearch} disabled={loading}
        className="bg-brand-600 hover:bg-brand-700 px-4 py-2 rounded text-sm font-medium disabled:opacity-50">
        {loading ? '...' : '查看'}
      </button>
    </div>
  );
}
