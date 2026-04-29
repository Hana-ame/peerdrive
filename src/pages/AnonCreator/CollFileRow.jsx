import { fileIcon } from './utils';

export default function CollFileRow({ entry, selectMode, isSelected, onClick, onToggleSelect }) {
  const handleClick = () => {
    if (selectMode) { onToggleSelect(entry.path); return; }
    onClick(entry);
  };

  return (
    <div className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm ${selectMode ? 'cursor-pointer' : ''}`}
      onClick={handleClick}>
      {selectMode && <input type="checkbox" checked={isSelected} readOnly className="shrink-0" />}
      <span className="text-lg">{fileIcon(entry.mime_type)}</span>
      <span className="text-blue-300 truncate flex-1 font-mono text-xs">{entry.path.split('/').pop()}</span>
      {!selectMode && <button className="text-blue-400 text-xs px-2 py-0.5 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>}
    </div>
  );
}
