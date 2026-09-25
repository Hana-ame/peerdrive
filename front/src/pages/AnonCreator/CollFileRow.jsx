import { fileIcon } from './utils';

export default function CollFileRow({ entry, selectMode, isSelected, onClick, onToggleSelect, onSelect }) {
  // 后端 AnonCollectionEntry 顶层没有 mime_type（在 providers[].mime_type），派生后再给图标用。
  const mime = entry.mime_type || entry.providers?.[0]?.mime_type || '';
  const handleClick = () => {
    if (selectMode) { onToggleSelect(entry.path); return; }
    if (onSelect) onSelect(entry);
  };

  return (
    <div className={`flex items-center gap-3 px-4 py-2.5 hover:bg-white/[0.06] border-b border-white/[0.03] text-sm cursor-pointer`}
      onClick={handleClick}>
      {selectMode && <input type="checkbox" checked={isSelected} readOnly className="shrink-0" />}
      <span className="text-lg">{fileIcon(mime, entry.path)}</span>
      <span className="text-brand-300 truncate flex-1 font-mono text-xs">{(entry.path || '').split('/').pop() || 'file'}</span>
      {!selectMode && (
        <button onClick={(e) => { e.stopPropagation(); onClick(entry); }}
          className="text-brand-400 text-xs px-2 py-0.5 rounded bg-brand-600/20 hover:bg-brand-600/40 shrink-0">+</button>
      )}
    </div>
  );
}
