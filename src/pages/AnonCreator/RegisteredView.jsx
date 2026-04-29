import FileSourceRow from './FileSourceRow';

export default function RegisteredView({ filtered, onAdd, onDragStart }) {
  const dirs = new Set();
  const localFiles = [];
  for (const f of filtered) {
    const rel = (f.provider_path || f.filename || '').replace(/^\//, '');
    const slash = rel.indexOf('/');
    if (slash === -1) localFiles.push(f);
    else if (rel.slice(0, slash)) dirs.add(rel.slice(0, slash));
  }

  if (dirs.size === 0 && localFiles.length === 0) return <p className="p-4 text-gray-600 text-xs">此目录为空</p>;

  return (
    <div>
      {Array.from(dirs).sort().map(dir => (
        <div key={dir} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
          <span className="text-lg">📁</span>
          <span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
        </div>
      ))}
      {localFiles.map(f => (
        <FileSourceRow key={f.hash} file={f} onAdd={onAdd} onDragStart={onDragStart} />
      ))}
    </div>
  );
}
