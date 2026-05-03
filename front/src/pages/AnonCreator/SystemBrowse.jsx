import { fmtSize } from './utils';

export default function SystemBrowse({ sysPath, sysEntries, sysLoading, onNavTo, onAdd, onAddFile, onAddFolder, onDragStart }) {
  const goUp = () => {
    const p = sysPath.split('/');
    p.pop();
    onNavTo(p.join('/') || '/');
  };

  return (
    <>
      <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-800 text-xs">
        <button onClick={goUp} disabled={sysPath === '/'} className="text-gray-400 hover:text-white disabled:opacity-30">←</button>
        <span className="text-gray-300 font-mono text-xs truncate">{sysPath}</span>
      </div>
      {sysLoading ? (
        <p className="p-4 text-gray-600 text-xs">加载中...</p>
      ) : sysEntries.length === 0 ? (
        <p className="p-4 text-gray-600 text-xs">此目录为空</p>
      ) : (
        sysEntries.map(e => (
          <div key={e.path} draggable={!e.is_dir}
            onDragStart={e.is_dir ? undefined : (ev) => onDragStart(ev, { name: e.name, path: e.path, sysPath: e.path, size: e.size, mime_type: e.mime_type || '' })}
            onClick={() => e.is_dir ? onNavTo(e.path) : null}
            className={`flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 border-b border-gray-800/50 text-sm group ${e.is_dir ? 'cursor-pointer' : ''}`}>
            <span className="text-lg">{e.is_dir ? '📁' : '📄'}</span>
            <span className={`font-mono truncate flex-1 text-xs ${e.is_dir ? 'text-yellow-400' : 'text-blue-300'}`}>{e.name}</span>
            {e.is_dir ? (
              <button onClick={(ev) => { ev.stopPropagation(); onAddFolder(e.path, e.name); }}
                className="text-green-400 opacity-60 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-green-600/20 hover:bg-green-600/40 shrink-0" title="添加整个文件夹">+</button>
            ) : (
              <button onClick={(ev) => { ev.stopPropagation(); onAddFile(e.path, e.name, e.size || 0); }}
                className="text-blue-400 opacity-60 group-hover:opacity-100 text-sm px-2 py-1 rounded bg-blue-600/20 hover:bg-blue-600/40 shrink-0">+</button>
            )}
          </div>
        ))
      )}
    </>
  );
}
