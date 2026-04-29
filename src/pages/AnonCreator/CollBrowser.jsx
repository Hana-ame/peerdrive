import { useMemo } from 'react';
import { fileIcon } from './utils';
import CollBrowserNav from './CollBrowserNav';
import CollFileRow from './CollFileRow';

export default function CollBrowser({ coll, collViewPath, selectMode, selectedFiles, onLeave, onPathNav, onNavIntoDir, onSaveToNode, onSelectToggle, onToggleFileSelect, onBatchSaveFiles, onFileAdd, onFileSelect }) {
  const currentCollView = useMemo(() => {
    if (!coll?.entries) return { dirs: [], files: [], total: 0 };
    const dirs = new Set();
    const files = [];
    const prefix = collViewPath ? collViewPath + '/' : '';
    for (const e of coll.entries) {
      if (!e.path.startsWith(prefix)) continue;
      const rel = e.path.slice(prefix.length);
      const slash = rel.indexOf('/');
      if (slash === -1) files.push(e);
      else if (rel.slice(0, slash)) dirs.add(rel.slice(0, slash));
    }
    const total = coll.entries.filter(e => e.path.startsWith(prefix)).length;
    return { dirs: Array.from(dirs).sort(), files, total };
  }, [coll, collViewPath]);

  return (
    <div className="flex flex-col h-full">
      <CollBrowserNav coll={coll} collViewPath={collViewPath} totalFiles={currentCollView.total}
        selectMode={selectMode} selectedFileCount={selectedFiles.size}
        onLeave={onLeave} onPathNav={onPathNav} onSaveToNode={onSaveToNode}
        onSelectToggle={onSelectToggle} onBatchSave={onBatchSaveFiles} />

      <div className="flex-1 overflow-y-auto">
        {currentCollView.dirs.length === 0 && currentCollView.files.length === 0 ? (
          <div className="p-4 text-gray-600 text-xs text-center">
            {collViewPath ? '此目录为空' : (
              coll.entries?.map(e => (
                <CollFileRow key={e.path} entry={e} selectMode={selectMode}
                  isSelected={selectedFiles.has(e.path)}
                  onClick={(entry) => { onFileAdd(entry.hash, entry.path, entry.mime_type, entry.size); }}
                  onToggleSelect={onToggleFileSelect}
                  onSelect={(entry) => onFileSelect?.({ hash: entry.hash, path: entry.path, filename: entry.path.split('/').pop(), mime_type: entry.mime_type, size: entry.size })} />
              ))
            )}
          </div>
        ) : (
          <>
            {currentCollView.dirs.map(dir => (
              <div key={dir} onClick={() => onNavIntoDir(dir)}
                className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                <span className="text-lg">📁</span>
                <span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
                <span className="text-gray-600 text-xs">文件夹</span>
              </div>
            ))}
            {currentCollView.files.map(e => (
              <CollFileRow key={e.path} entry={e} selectMode={selectMode}
                isSelected={selectedFiles.has(e.path)}
                onClick={(entry) => { onFileAdd(entry.hash, entry.path, entry.mime_type, entry.size); }}
                onToggleSelect={onToggleFileSelect}
                onSelect={(entry) => onFileSelect?.({ hash: entry.hash, path: entry.path, filename: entry.path.split('/').pop(), mime_type: entry.mime_type, size: entry.size })} />
            ))}
          </>
        )}
      </div>
    </div>
  );
}
