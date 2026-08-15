import FileSourceRow from './FileSourceRow';

export default function TimelineView({ filtered, onAdd, onDragStart }) {
  if (filtered.length === 0) return <p className="p-4 text-gray-600 text-xs">无匹配文件</p>;

  const sorted = [...filtered].sort((a, b) => (b.created_at || '').localeCompare(a.created_at || ''));
  let lastDate = '';
  return sorted.map(f => {
    const d = f.created_at ? f.created_at.split('T')[0] : '';
    const showDate = d !== lastDate;
    lastDate = d;
    return (
      <div key={f.hash}>
        {showDate && <div className="px-4 py-2 text-[10px] text-gray-500 bg-gray-900/50 sticky top-0">{d}</div>}
        <FileSourceRow file={f} onAdd={onAdd} onDragStart={onDragStart} />
      </div>
    );
  });
}
