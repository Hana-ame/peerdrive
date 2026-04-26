import React, { useState } from 'react';

function fileIcon(mime) {
  if (!mime) return '📄';
  if (mime.startsWith('image/')) return '🖼️';
  if (mime.startsWith('video/')) return '🎬';
  if (mime.startsWith('audio/')) return '🎵';
  if (mime.startsWith('text/')) return '📝';
  if (mime.includes('pdf')) return '📕';
  if (mime.includes('zip') || mime.includes('tar') || mime.includes('gzip') || mime.includes('rar')) return '📦';
  return '📄';
}

function fmtSize(b) {
  if (!b) return '';
  if (b < 1024) return b + ' B';
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
  return (b / 1073741824).toFixed(1) + ' GB';
}

function buildTree(entries) {
  if (!entries || entries.length === 0) return [];
  const node = {};
  for (const e of entries) {
    const parts = (e.path || '').split('/');
    let cur = node;
    for (let i = 0; i < parts.length; i++) {
      const seg = parts[i];
      if (!seg) continue;
      const isLast = i === parts.length - 1;
      if (!cur[seg]) {
        cur[seg] = { _children: {}, _files: [] };
      }
      if (isLast) {
        cur[seg]._files.push({ name: seg, hash: e.hash, path: e.path, size: e.size, mime_type: e.mime_type });
      }
      cur = cur[seg]._children;
    }
  }
  function toArray(obj, prefix = '') {
    const result = [];
    for (const key of Object.keys(obj).sort()) {
      const item = obj[key];
      const fullPath = prefix ? `${prefix}/${key}` : key;
      const hasChildren = Object.keys(item._children).length > 0;
      if (item._files.length === 1 && item._files[0].name === key && !hasChildren) {
        result.push({ name: key, path: fullPath, isDir: false, ...item._files[0] });
      } else {
        result.push({
          name: key,
          path: fullPath,
          isDir: true,
          children: toArray(item._children, fullPath),
          files: item._files,
        });
      }
    }
    return result;
  }
  return toArray(node, '');
}

export function buildFlatTree(entries) {
  return buildTree(entries);
}

export default function FileTree({ entries, entryActions }) {
  const tree = buildTree(entries);
  const [expanded, setExpanded] = useState(new Set());
  const [renaming, setRenaming] = useState(null);
  const [dragOverPath, setDragOverPath] = useState(null);

  const toggle = (path) => {
    setExpanded(prev => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const handleDoubleClick = (entry) => {
    if (entryActions?.onRename) {
      setRenaming(entry.path);
    }
  };

  const handleNewFolder = () => {
    const name = prompt('新建文件夹名称:');
    if (!name || !name.trim()) return;
    entryActions?.onNewFolder?.(name.trim());
  };

  const renderEntries = (nodes, depth, parentPath) => {
    const rows = [];
    for (const node of nodes) {
      if (!node.isDir) {
        rows.push(
          <div key={node.path} className="flex items-center gap-1 py-1 px-2 hover:bg-gray-800/50 group text-xs"
            style={{ paddingLeft: `${depth * 16 + 8}px` }}>
            <span className="w-4 shrink-0" />
            <span>{fileIcon(node.mime_type)}</span>
            <span className="text-blue-300 font-mono truncate flex-1">{node.name}</span>
            <span className="text-gray-600 text-[10px] shrink-0">{fmtSize(node.size)}</span>
            {entryActions?.onRemove && (
              <button onClick={() => entryActions.onRemove(node)} className="text-gray-600 hover:text-red-400 opacity-0 group-hover:opacity-100 text-sm px-1 shrink-0">×</button>
            )}
          </div>
        );
        continue;
      }
      const isExp = expanded.has(node.path);
      const dropOver = dragOverPath === node.path;
      rows.push(
        <div key={node.path} className="flex flex-col">
          <div
            className={`flex items-center gap-1 py-1 px-2 hover:bg-gray-800/50 cursor-pointer group text-xs ${dropOver ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
            style={{ paddingLeft: `${depth * 16 + 8}px` }}
            onClick={() => toggle(node.path)}
            draggable={node.isDir}
            onDragStart={(e) => {
              e.dataTransfer.setData('application/peerdrive-path', node.path);
              e.dataTransfer.effectAllowed = 'move';
            }}
            onDragOver={(e) => {
              if (node.isDir) {
                e.preventDefault();
                e.dataTransfer.dropEffect = 'copy';
                setDragOverPath(node.path);
              }
            }}
            onDragLeave={() => setDragOverPath(null)}
            onDrop={(e) => {
              e.preventDefault();
              setDragOverPath(null);
              if (node.isDir) {
                try {
                  const data = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry');
                  if (data) {
                    const parsed = JSON.parse(data);
                    entryActions?.onDrop?.({ ...parsed, targetDir: node.path });
                  }
                } catch {}
              }
            }}
          >
            <span className="w-4 text-center shrink-0">{isExp ? '▾' : '▸'}</span>
            <span className="text-gray-400">{isExp ? '📂' : '📁'}</span>
            <span className="text-gray-200 font-mono truncate">{node.name}/</span>
            <span className="text-gray-600 ml-auto text-[10px] shrink-0">
              {node.files.length + (node.children ? node.children.length : 0)} 项
            </span>
          </div>
          {isExp && (
            <div>
              {renderEntries(node.children || [], depth + 1, node.path)}
              {node.files.map((f, i) => (
                <div
                  key={f.path + i}
                  className={`flex items-center gap-1 py-1 px-2 hover:bg-gray-800/50 group text-xs ${dragOverPath === f.path ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
                  style={{ paddingLeft: `${(depth + 1) * 16 + 8}px` }}
                  onDoubleClick={() => handleDoubleClick(f)}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData('application/peerdrive-entry', JSON.stringify({ hash: f.hash, path: f.path, name: f.name, mime_type: f.mime_type, size: f.size }));
                    e.dataTransfer.effectAllowed = 'move';
                  }}
                  onDragOver={(e) => {
                    e.preventDefault();
                    e.dataTransfer.dropEffect = 'copy';
                    setDragOverPath(f.path);
                  }}
                  onDragLeave={() => setDragOverPath(null)}
                  onDrop={(e) => {
                    e.preventDefault();
                    setDragOverPath(null);
                    try {
                      const data = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry');
                      if (data) {
                        const parsed = JSON.parse(data);
                        entryActions?.onDrop?.({ ...parsed, targetDir: f.path });
                      }
                    } catch {}
                  }}
                >
                  <span className="w-4 shrink-0" />
                  <span>{fileIcon(f.mime_type)}</span>
                  {renaming === f.path ? (
                    <input
                      autoFocus
                      defaultValue={f.path}
                      onBlur={() => setRenaming(null)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          entryActions?.onRename?.(f.path, e.target.value);
                          setRenaming(null);
                        } else if (e.key === 'Escape') {
                          setRenaming(null);
                        }
                      }}
                      className="flex-1 bg-gray-700 px-1 py-0.5 rounded text-xs font-mono border border-blue-500 outline-none"
                    />
                  ) : (
                    <span className="text-blue-300 font-mono truncate flex-1">{f.name}</span>
                  )}
                  <span className="text-gray-600 text-[10px] shrink-0">{fmtSize(f.size)}</span>
                  <span className="text-gray-600 text-[10px] font-mono shrink-0 max-w-[80px] truncate">{(f.hash || '').substring(0, 8)}</span>
                  {entryActions?.onRemove && (
                    <button
                      onClick={(e) => { e.stopPropagation(); entryActions.onRemove(f); }}
                      className="text-gray-600 hover:text-red-400 opacity-0 group-hover:opacity-100 text-sm px-1 shrink-0"
                    >
                      ×
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      );
    }
    return rows;
  };

  if (entries.length === 0) {
    return (
      <div
        className="flex items-center justify-center h-full text-gray-600 text-xs"
        onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }}
        onDrop={(e) => {
          e.preventDefault();
          try {
            const data = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry');
            if (data) {
              const parsed = JSON.parse(data);
              entryActions?.onDrop?.({ ...parsed, targetDir: '' });
            }
          } catch {}
        }}
      >
        空目录 — 从左侧拖拽或点击添加文件
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-2 py-1 border-b border-gray-800 shrink-0">
        <button onClick={handleNewFolder} className="text-[10px] bg-gray-700 hover:bg-gray-600 px-2 py-0.5 rounded">+ 新建文件夹</button>
        <span className="text-[10px] text-gray-500">{entries.length} 个条目</span>
      </div>
      <div className="overflow-y-auto flex-1 select-none">
        {renderEntries(tree, 0, '')}
      </div>
    </div>
  );
}
