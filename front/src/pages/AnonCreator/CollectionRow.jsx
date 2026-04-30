import { collDisplayName } from './utils';

export default function CollectionRow({ collection, selectMode, isSelected, onClick, onToggleSelect }) {
  const handleClick = () => {
    if (selectMode) { onToggleSelect(collection.hash); return; }
    onClick(collection.hash);
  };

  return (
    <div className={`px-4 py-3 border-b border-gray-800/50 cursor-pointer hover:bg-gray-800 ${isSelected ? 'bg-blue-900/30' : ''}`}
      onClick={handleClick}>
      <div className="flex items-center gap-2">
        {selectMode && <input type="checkbox" checked={isSelected} readOnly className="shrink-0" />}
        <span className="text-lg">📦</span>
        <span className="text-blue-300 truncate flex-1 text-sm font-medium">{collDisplayName(collection)}</span>
      </div>
      <div className="flex items-center gap-2 mt-1 ml-8">
        {collection.tags?.map(t => <span key={t} className="text-[10px] bg-blue-900/50 text-blue-300 px-1.5 py-0.5 rounded-full">{t}</span>)}
        <span className="text-[10px] text-gray-600">{collection.entry_count || 0} 文件</span>
        <span className="text-[10px] text-gray-500">{collection.created_at ? new Date(collection.created_at).toLocaleDateString() : ''}</span>
      </div>
    </div>
  );
}
