import { useMemo } from 'react';
import FileRow from './FileRow';

export default function FileList({ entries, navPath, allCollHashes, searchHash, collection, onNavIn, onNestedCollClick }) {
  const currentItems = useMemo(() => {
    const dirs = new Set();
    const files = [];
    const prefix = navPath ? navPath + '/' : '';
    for (const e of entries) {
      const p = e.path || '';
      if (p.startsWith(prefix)) {
        const rest = p.slice(prefix.length);
        const slash = rest.indexOf('/');
        if (slash === -1) files.push(e);
        else dirs.add(rest.slice(0, slash));
      }
    }
    return { dirs: Array.from(dirs).sort(), files };
  }, [entries, navPath]);

  if (currentItems.dirs.length === 0 && currentItems.files.length === 0) {
    return <div className="text-center text-gray-600 py-10 text-sm">此目录为空</div>;
  }

  return (
    <div>
      {currentItems.dirs.map(dir => (
        <div key={dir} onClick={() => onNavIn(dir)}
          className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
          <span className="text-xl">📁</span>
          <span className="text-gray-200 font-mono truncate flex-1">{dir}</span>
          <span className="text-gray-600 text-xs">文件夹</span>
        </div>
      ))}
      {currentItems.files.map(f => (
        <FileRow key={f.path} file={f} isNestedColl={allCollHashes.has(f.hash)}
          searchHash={searchHash} collection={collection} onNestedCollClick={onNestedCollClick} />
      ))}
    </div>
  );
}
