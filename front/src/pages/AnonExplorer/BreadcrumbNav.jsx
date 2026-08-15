export default function BreadcrumbNav({ navPath, fname, onNavigate }) {
  if (!navPath) return null;
  return (
    <div className="flex items-center gap-1 px-4 py-2 bg-gray-800/80 border-b border-gray-700/50 text-xs shrink-0">
      <button onClick={() => onNavigate('')} className="text-blue-400 hover:text-blue-300 font-medium flex items-center gap-1">
        📦 {fname || '合集'}
      </button>
      {navPath.split('/').map((p, i) => (
        <span key={i} className="flex items-center gap-1">
          <span className="text-gray-600">›</span>
          <button onClick={() => { const parts = navPath.split('/'); onNavigate(parts.slice(0, i + 1).join('/')); }}
            className="text-blue-400 hover:text-blue-300 hover:underline font-medium">{p}</button>
        </span>
      ))}
    </div>
  );
}
