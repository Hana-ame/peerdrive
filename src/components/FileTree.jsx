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
      if (!cur[seg]) cur[seg] = { _children: {}, _files: [] };
      if (isLast) cur[seg]._files.push({ name: seg, hash: e.hash, path: e.path, size: e.size, mime_type: e.mime_type });
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
        result.push({ name: key, path: fullPath, isDir: true, children: toArray(item._children, fullPath), files: item._files });
      }
    }
    return result;
  }
  return toArray(node, '');
}

export function buildFlatTree(entries) { return buildTree(entries); }

export default function FileTree({ entries, entryActions }) {
  const tree = buildTree(entries);
  const [expanded, setExpanded] = useState(new Set());
  const [renaming, setRenaming] = useState(null);
  const [dragOverPath, setDragOverPath] = useState(null);
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [newFolderName, setNewFolderName] = useState('');

  const toggle = (path) => setExpanded(prev => { const n = new Set(prev); n.has(path) ? n.delete(path) : n.add(path); return n; });
  const submitNewFolder = () => {
    const n = newFolderName.trim();
    if (n) entryActions?.onNewFolder?.(n);
    setNewFolderName('');
    setShowNewFolder(false);
  };
  const openNewFolder = () => {
    entryActions?.onNewFolder?.('新建文件夹');
  };

  const renderEntries = (nodes, depth, parentPath) => {
    const rows = [];
    for (const node of nodes) {
      if (!node.isDir) {
        rows.push(
          <div key={node.path} className="flex items-center gap-2 py-2 px-3 hover:bg-gray-800/50 group text-sm"
            style={{ paddingLeft: `${depth * 20 + 12}px` }}>
            <span className="w-5 shrink-0" />
            <span className="text-lg">{fileIcon(node.mime_type)}</span>
            <span className="text-blue-300 font-mono truncate flex-1">{node.name}</span>
            <span className="text-gray-500 text-xs shrink-0">{fmtSize(node.size)}</span>
            {entryActions?.onRemove && (
              <button onClick={() => entryActions.onRemove(node)} className="text-gray-600 hover:text-red-400 opacity-0 group-hover:opacity-100 text-lg px-1 shrink-0">×</button>
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
            className={`flex items-center gap-2 py-2 px-3 hover:bg-gray-800/50 cursor-pointer group text-sm ${dropOver ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
            style={{ paddingLeft: `${depth * 20 + 12}px` }}
            onClick={() => toggle(node.path)}
            draggable={node.isDir}
            onDragStart={(e) => { e.dataTransfer.setData('application/peerdrive-path', node.path); e.dataTransfer.effectAllowed = 'move'; }}
            onDragOver={(e) => { if (node.isDir) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setDragOverPath(node.path); } }}
            onDragLeave={() => setDragOverPath(null)}
            onDrop={(e) => {
              e.preventDefault(); setDragOverPath(null);
              if (node.isDir) {
                try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed, targetDir: node.path }); } } catch {}
              }
            }}
          >
            <span className="w-5 text-center shrink-0 text-base">{isExp ? '▾' : '▸'}</span>
            <span className="text-lg">{isExp ? '📂' : '📁'}</span>
            <span className="text-gray-200 font-mono truncate">{node.name}/</span>
            <span className="text-gray-500 ml-auto text-xs shrink-0">{node.files.length + (node.children ? node.children.length : 0)} 项</span>
          </div>
          {isExp && (
            <div>
              {renderEntries(node.children || [], depth + 1, node.path)}
              {node.files.map((f, i) => (
                <div key={f.path + i}
                  className={`flex items-center gap-2 py-2 px-3 hover:bg-gray-800/50 group text-sm ${dragOverPath === f.path ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
                  style={{ paddingLeft: `${(depth + 1) * 20 + 12}px` }}
                  onDoubleClick={() => { if (entryActions?.onRename) setRenaming(f.path); }}
                  draggable
                  onDragStart={(e) => { e.dataTransfer.setData('application/peerdrive-entry', JSON.stringify({ hash: f.hash, path: f.path, name: f.name, mime_type: f.mime_type, size: f.size })); e.dataTransfer.effectAllowed = 'move'; }}
                  onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setDragOverPath(f.path); }}
                  onDragLeave={() => setDragOverPath(null)}
                    onDrop={(e) => {
                      e.preventDefault(); setDragOverPath(null);
                      try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed, targetDir: f.path }); } } catch {}
                  }}
                >
                  <span className="w-5 shrink-0" />
                  <span className="text-lg">{fileIcon(f.mime_type)}</span>
                  {renaming === f.path ? (
                    <input autoFocus defaultValue={f.path}
                      onBlur={() => setRenaming(null)}
                      onKeyDown={(e) => { if (e.key === 'Enter') { entryActions?.onRename?.(f.path, e.target.value); setRenaming(null); } else if (e.key === 'Escape') setRenaming(null); }}
                      className="flex-1 bg-gray-700 px-2 py-1 rounded text-sm font-mono border border-blue-500 outline-none" />
                  ) : (
                    <span className="text-blue-300 font-mono truncate flex-1">{f.name}</span>
                  )}
                  <span className="text-gray-500 text-xs shrink-0">{fmtSize(f.size)}</span>
                  <span className="text-gray-500 text-xs font-mono shrink-0 max-w-[100px] truncate">{(f.hash || '').substring(0, 10)}</span>
                  {entryActions?.onRemove && (
                    <button onClick={(e) => { e.stopPropagation(); entryActions.onRemove(f); }}
                      className="text-gray-600 hover:text-red-400 opacity-0 group-hover:opacity-100 text-lg px-1 shrink-0">×</button>
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
      <div className="flex items-center justify-center h-full text-gray-600 text-sm"
        onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }}
        onDrop={(e) => {
          e.preventDefault();
          try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed, targetDir: '' }); } } catch {}
        }}>空目录 — 从左侧拖拽或点击添加文件</div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-3 px-3 py-2 border-b border-gray-800 shrink-0">
        {showNewFolder ? (
          <div className="flex items-center gap-2">
            <input autoFocus value={newFolderName} onChange={e => setNewFolderName(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') submitNewFolder(); if (e.key === 'Escape') setShowNewFolder(false); }}
              placeholder="文件夹名称" className="bg-gray-700 px-2 py-1 rounded text-sm border border-blue-500 outline-none w-32" />
            <button onClick={submitNewFolder} className="text-sm bg-blue-600 hover:bg-blue-700 px-2 py-1 rounded">确定</button>
            <button onClick={() => setShowNewFolder(false)} className="text-sm text-gray-500 hover:text-white">取消</button>
          </div>
        ) : (
          <button onClick={openNewFolder} className="text-sm bg-gray-700 hover:bg-gray-600 px-3 py-1 rounded">+ 新建文件夹</button>
        )}
        <span className="text-sm text-gray-500">{entries.length} 个条目</span>
      </div>
      <div className="overflow-y-auto flex-1 select-none"
        onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }}
        onDrop={(e) => {
          e.preventDefault();
          try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed, targetDir: '' }); } } catch {}
        }}>
        {renderEntries(tree, 0, '')}
      </div>
    </div>
  );
}
