import { useMemo } from 'react';
import { fileIcon } from './utils';
import CollBrowserNav from './CollBrowserNav';
import CollFileRow from './CollFileRow';

// 后端 AnonCollectionEntry 只有 {path, providers, hash?}，没有 mime_type/size 顶层字段；
// mime 在 providers[].mime_type 里（back model/anon.go）。这里统一派生，避免调用方读到 undefined。
const entryMime = (e) => e.mime_type || e.providers?.[0]?.mime_type || '';
const entrySize = (e) => e.size || 0;

export default function CollBrowser({ coll, collViewPath, selectMode, selectedFiles, onLeave, onPathNav, onNavIntoDir, onSaveToNode, onSelectToggle, onToggleFileSelect, onBatchSaveFiles, onFileAdd, onFileSelect }) {
  const currentCollView = useMemo(() => {
    if (!coll?.entries) return { dirs: [], files: [], total: 0 };
    const dirs = new Set();
    const files = [];
    const prefix = collViewPath ? collViewPath + '/' : '';
    for (const e of coll.entries) {
      if (!e.path.startsWith(prefix)) continue;
      const rel = e.path.slice(prefix.length);
      // 坑：编辑器的文件夹条目以 "/" 结尾（如 "dir/"），在浏览时前缀匹配会得到空名行，
      // 直接跳过（它作为目录由 dirs 呈现）。
      if (rel.endsWith('/')) { if (rel.slice(0, -1)) dirs.add(rel.slice(0, -1)); continue; }
      const slash = rel.indexOf('/');
      if (slash === -1) files.push(e);
      else if (rel.slice(0, slash)) dirs.add(rel.slice(0, slash));
    }
    const total = coll.entries.filter(e => e.path.startsWith(prefix)).length;
    return { dirs: Array.from(dirs).sort(), files, total };
  }, [coll, collViewPath]);

  const makeSelectPayload = (entry) => ({
    hash: entry.hash || entry.providers?.[0]?.value || '',
    path: entry.path,
    filename: entry.path.split('/').pop(),
    mime_type: entryMime(entry),
    size: entrySize(entry),
  });

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
                  onClick={(entry) => { onFileAdd(entry.hash || entry.providers?.[0]?.value, entry.path, entryMime(entry), entrySize(entry)); }}
                  onToggleSelect={onToggleFileSelect}
                  onSelect={(entry) => onFileSelect?.(makeSelectPayload(entry))} />
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
                onClick={(entry) => { onFileAdd(entry.hash || entry.providers?.[0]?.value, entry.path, entryMime(entry), entrySize(entry)); }}
                onToggleSelect={onToggleFileSelect}
                onSelect={(entry) => onFileSelect?.(makeSelectPayload(entry))} />
            ))}
          </>
        )}
      </div>
    </div>
  );
}
