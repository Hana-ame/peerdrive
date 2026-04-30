// 文件树组件：将合集条目渲染为可拖拽/展开/重命名/移动到/新建文件夹的目录树
import React, { useState, useRef, useEffect } from 'react';

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
    const isDirEntry = (e.path || '').endsWith('/');
    const parts = (e.path || '').replace(/\/$/, '').split('/');
    let cur = node;
    for (let i = 0; i < parts.length; i++) {
      const seg = parts[i];
      if (!seg) continue;
      const isLast = i === parts.length - 1;
      if (!cur[seg]) cur[seg] = { _children: {}, _files: [] };
      if (isLast && !isDirEntry) cur[seg]._files.push({ name: seg, hash: e.hash || e.providers?.[0]?.value || '', path: e.path, size: e.size, mime_type: e.mime_type, providers: e.providers });
      cur = cur[seg]._children;
    }
  }
  function toArray(obj, prefix = '') {
    const result = [];
    for (const key of Object.keys(obj).sort()) {
      const item = obj[key];
      const fullPath = prefix ? `${prefix}/${key}` : key;
      const hasChildren = Object.keys(item._children).length > 0;
      const allFilesMatchKey = item._files.length > 0 && item._files.every(f => f.name === key);
      if (!hasChildren && allFilesMatchKey) {
        for (const f of item._files) result.push({ name: key, path: fullPath, isDir: false, ...f });
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
  const [showMoveModal, setShowMoveModal] = useState(null); // { path, isDir }
  const [moveTarget, setMoveTarget] = useState('');
  const [contextMenu, setContextMenu] = useState(null); // { x, y, node }
  const newFolderRef = useRef(null);
  const ctxMenuRef = useRef(null);

  // Close context menu on outside click or Escape
  useEffect(() => {
    const close = () => setContextMenu(null);
    const onKey = (e) => { if (e.key === 'Escape') close(); };
    const onClick = (e) => { if (ctxMenuRef.current && !ctxMenuRef.current.contains(e.target)) close(); };
    if (contextMenu) {
      document.addEventListener('click', onClick);
      document.addEventListener('keydown', onKey);
      return () => { document.removeEventListener('click', onClick); document.removeEventListener('keydown', onKey); };
    }
  }, [contextMenu]);

  const toggle = (path) => setExpanded(prev => { const n = new Set(prev); n.has(path) ? n.delete(path) : n.add(path); return n; });
  const submitNewFolder = () => {
    const n = newFolderName.trim();
    if (n) entryActions?.onNewFolder?.(n);
    setNewFolderName('');
    setShowNewFolder(false);
  };

  // 获取所有可用目录（用于移动到目标选择）
  const getAllDirs = (nodes, prefix = '') => {
    let dirs = [];
    for (const n of nodes) {
      if (!n.isDir) continue;
      const p = prefix ? `${prefix}/${n.name}` : n.name;
      dirs.push(p);
      if (n.children) dirs = dirs.concat(getAllDirs(n.children, p));
    }
    return dirs;
  };

  // 执行移动：重命名路径到目标目录下
  const executeMove = (srcPath, targetDir) => {
    const name = srcPath.split('/').pop();
    const newPath = targetDir ? `${targetDir}/${name}` : name;
    if (newPath !== srcPath) entryActions?.onRename?.(srcPath, newPath);
    setShowMoveModal(null);
  };

  const renderEntries = (nodes, depth, parentPath) => {
    const rows = [];
    for (const node of nodes) {
      if (!node.isDir) {
        const isDragging = dragOverPath === node.path;
        rows.push(
          <div key={node.path}
            className={`flex items-center gap-2 py-2 px-2 hover:bg-gray-800/50 group text-sm ${isDragging ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
            style={{ paddingLeft: `${depth * 20 + 8}px` }}
            draggable
            onDragStart={(e) => { e.dataTransfer.setData('application/peerdrive-entry', JSON.stringify({ hash: node.hash || node.providers?.[0]?.value || '', path: node.path, name: node.name, mime_type: node.mime_type, size: node.size, providers: node.providers })); e.dataTransfer.effectAllowed = 'move'; }}
            onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setDragOverPath(node.path); }}
            onDragLeave={() => setDragOverPath(null)}
            onDrop={(e) => {
              e.preventDefault(); setDragOverPath(null);
              try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed }); } } catch (e) { console.error('FileTree drop error:', e); }
            }}
            onDoubleClick={(e) => { e.stopPropagation(); if (entryActions?.onRename) setRenaming(node.path); }}
            onContextMenu={(e) => { e.preventDefault(); setContextMenu({ x: e.clientX, y: e.clientY, node }); }}
          >
            <span className="w-4 shrink-0" />
            <span className="text-base">{fileIcon(node.mime_type)}</span>
            {renaming === node.path ? (
              <input autoFocus defaultValue={node.path}
                onBlur={() => setRenaming(null)}
                onKeyDown={(e) => { if (e.key === 'Enter') { let nv = e.target.value; entryActions?.onRename?.(node.path, nv); setRenaming(null); } else if (e.key === 'Escape') setRenaming(null); }}
                className="flex-1 bg-gray-700 px-1.5 py-0.5 rounded text-xs font-mono border border-blue-500 outline-none" />
            ) : (
              <span className="text-blue-300 font-mono truncate flex-1 text-xs">{node.name}</span>
            )}
            <span className="text-gray-500 text-[10px] shrink-0 hidden sm:inline">{fmtSize(node.size)}</span>
            {/* 移动到按钮 */}
            <button onClick={(e) => { e.stopPropagation(); setShowMoveModal({ path: node.path, isDir: false }); }}
              className="text-gray-600 hover:text-yellow-400 opacity-0 group-hover:opacity-100 text-xs px-1 shrink-0 hidden sm:inline" title="移动到...">→📁</button>
            {entryActions?.onRemove && (
              <button onClick={() => entryActions.onRemove(node)} className="text-gray-600 hover:text-red-400 opacity-0 group-hover:opacity-100 text-lg px-1 shrink-0">×</button>
            )}
          </div>
        );
        continue;
      }
      const isExp = expanded.has(node.path);
      const dropOver = dragOverPath === node.path;
      const allDirs = getAllDirs(tree);

      rows.push(
        <div key={node.path} className="flex flex-col">
          <div
            className={`flex items-center gap-2 py-2 px-2 hover:bg-gray-800/50 cursor-pointer group text-sm ${dropOver ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
            style={{ paddingLeft: `${depth * 20 + 8}px` }}
            onClick={() => toggle(node.path)}
            onDoubleClick={(e) => { e.stopPropagation(); if (entryActions?.onRename) setRenaming(node.path + '/'); }}
            draggable={node.isDir}
            onDragStart={(e) => { e.dataTransfer.setData('application/peerdrive-path', node.path); e.dataTransfer.effectAllowed = 'move'; }}
            onDragOver={(e) => { if (node.isDir) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setDragOverPath(node.path); } }}
            onDragLeave={() => setDragOverPath(null)}
            onDrop={(e) => {
              e.preventDefault(); setDragOverPath(null);
              if (node.isDir) {
                try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed, targetDir: node.path }); } } catch (e) { console.error('FileTree drop error:', e); }
              }
            }}
          >
            <span className="w-4 text-center shrink-0 text-sm">{isExp ? '▾' : '▸'}</span>
            <span className="text-base">{isExp ? '📂' : '📁'}</span>
            {renaming === (node.path + '/') ? (
              <input autoFocus defaultValue={node.path + '/'}
                onBlur={() => setRenaming(null)}
                onKeyDown={(e) => { if (e.key === 'Enter') { let nv = e.target.value; entryActions?.onRename?.(node.path + '/', nv.endsWith('/') ? nv : nv + '/'); setRenaming(null); } else if (e.key === 'Escape') setRenaming(null); }}
                className="flex-1 bg-gray-700 px-1.5 py-0.5 rounded text-xs font-mono border border-blue-500 outline-none" />
            ) : (
              <span className="text-gray-200 font-mono truncate text-xs">{node.name}/</span>
            )}
            <span className="text-gray-500 ml-auto text-[10px] shrink-0 hidden sm:inline">{node.files.length + (node.children ? node.children.length : 0)} 项</span>
            <button onClick={(e) => { e.stopPropagation(); setShowMoveModal({ path: node.path, isDir: true }); }}
              className="text-gray-600 hover:text-yellow-400 opacity-0 group-hover:opacity-100 text-xs px-1 shrink-0 hidden sm:inline" title="移动条目到此目录">→📁</button>
          </div>
          {isExp && (
            <div>
              {renderEntries(node.children || [], depth + 1, node.path)}
              {node.files.map((f, i) => (
                <div key={f.path + i}
                  className={`flex items-center gap-2 py-2 px-2 hover:bg-gray-800/50 group text-sm ${dragOverPath === f.path ? 'bg-blue-900/40 ring-1 ring-blue-500/50' : ''}`}
                  style={{ paddingLeft: `${(depth + 1) * 20 + 8}px` }}
                  onDoubleClick={(e) => { e.stopPropagation(); if (entryActions?.onRename) setRenaming(f.path); }}
                  onContextMenu={(e) => { e.preventDefault(); setContextMenu({ x: e.clientX, y: e.clientY, node: f }); }}
                  draggable
                  onDragStart={(e) => { e.dataTransfer.setData('application/peerdrive-entry', JSON.stringify({ hash: f.hash || f.providers?.[0]?.value || '', path: f.path, name: f.name, mime_type: f.mime_type, size: f.size, providers: f.providers })); e.dataTransfer.effectAllowed = 'move'; }}
                  onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setDragOverPath(f.path); }}
                  onDragLeave={() => setDragOverPath(null)}
                  onDrop={(e) => {
                    e.preventDefault(); setDragOverPath(null);
                    try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed }); } } catch (e) { console.error('FileTree drop error:', e); }
                  }}
                >
                  <span className="w-4 shrink-0" />
                  <span className="text-base">{fileIcon(f.mime_type)}</span>
                  {renaming === f.path ? (
                    <input autoFocus defaultValue={f.path}
                      onBlur={() => setRenaming(null)}
                      onKeyDown={(e) => { if (e.key === 'Enter') { let nv = e.target.value; entryActions?.onRename?.(f.path, nv); setRenaming(null); } else if (e.key === 'Escape') setRenaming(null); }}
                      className="flex-1 bg-gray-700 px-1.5 py-0.5 rounded text-xs font-mono border border-blue-500 outline-none" />
                  ) : (
                    <span className="text-blue-300 font-mono truncate flex-1 text-xs">{f.name}</span>
                  )}
                  <span className="text-gray-500 text-[10px] shrink-0 hidden sm:inline">{fmtSize(f.size)}</span>
                  <span className="text-gray-500 text-[10px] font-mono shrink-0 max-w-[80px] truncate hidden md:inline">{(f.hash || f.providers?.[0]?.value || '').substring(0, 8)}</span>
                  <button onClick={(e) => { e.stopPropagation(); setShowMoveModal({ path: f.path, isDir: false }); }}
                    className="text-gray-600 hover:text-yellow-400 opacity-0 group-hover:opacity-100 text-xs px-1 shrink-0 hidden sm:inline" title="移动到...">→📁</button>
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

  // 移动到弹窗
  const allDirs = getAllDirs(tree);
  const moveItemName = showMoveModal?.path?.split('/').pop() || '';

  if (entries.length === 0) {
    return (
      <div className="flex items-center justify-center h-full text-gray-600 text-xs"
        onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }}
        onDrop={(e) => {
          e.preventDefault();
          try { const raw = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (raw) { const data = raw.startsWith('{') ? JSON.parse(raw) : { hash: '', name: raw, path: raw }; entryActions?.onDrop?.({ ...data, targetDir: '' }); } } catch (e) { console.error('FileTree drop error:', e); }
        }}>拖拽文件到此处 — 从左侧拖拽或点击 + 添加文件</div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* 工具栏 */}
      <div className="flex items-center gap-2 px-2 py-1.5 border-b border-gray-800 shrink-0 flex-wrap">
        {showNewFolder ? (
          <div className="flex items-center gap-1.5">
            <input ref={newFolderRef} autoFocus value={newFolderName} onChange={e => setNewFolderName(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') submitNewFolder(); if (e.key === 'Escape') { setShowNewFolder(false); setNewFolderName(''); } }}
              placeholder="文件夹名称" className="bg-gray-700 px-2 py-1 rounded text-xs border border-blue-500 outline-none w-28" />
            <button onClick={submitNewFolder} className="text-xs bg-blue-600 hover:bg-blue-700 px-2 py-0.5 rounded">确定</button>
            <button onClick={() => { setShowNewFolder(false); setNewFolderName(''); }} className="text-xs text-gray-500 hover:text-white">取消</button>
          </div>
        ) : (
          <button onClick={() => setShowNewFolder(true)}
            className="text-xs bg-gray-700 hover:bg-gray-600 px-2 py-1 rounded">+ 新建文件夹</button>
        )}
        <span className="text-[10px] text-gray-500">{entries.length} 条目</span>
        <span className="text-[10px] text-gray-600 hidden sm:inline">| 双击重命名 | 拖拽移动</span>
      </div>

      {/* 移动到弹窗 */}
      {showMoveModal && (
        <div className="absolute inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setShowMoveModal(null)}>
          <div className="bg-gray-800 p-4 rounded-xl border border-gray-600 shadow-2xl w-72" onClick={e => e.stopPropagation()}>
            <h3 className="text-sm font-bold mb-2">移动 "{moveItemName}" 到</h3>
            <div className="max-h-48 overflow-y-auto space-y-0.5 mb-3">
              <button onClick={() => executeMove(showMoveModal.path, '')}
                className="w-full text-left px-2 py-1 text-xs hover:bg-gray-700 rounded text-gray-300">
                📂 / (根目录)
              </button>
              {allDirs.filter(d => d !== showMoveModal.path && !d.startsWith(showMoveModal.path + '/')).map(d => (
                <button key={d} onClick={() => executeMove(showMoveModal.path, d)}
                  className="w-full text-left px-2 py-1 text-xs hover:bg-gray-700 rounded text-gray-300">
                  📁 {d}
                </button>
              ))}
              {allDirs.length <= 1 && <p className="text-[10px] text-gray-600 px-2">暂无其他目录</p>}
            </div>
            <div className="flex justify-end">
              <button onClick={() => setShowMoveModal(null)} className="text-xs text-gray-500 hover:text-white">取消</button>
            </div>
          </div>
        </div>
      )}

      {/* 树形视图 */}
      <div className="overflow-y-auto flex-1 select-none"
        onDragOver={(e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; }}
        onDrop={(e) => {
          e.preventDefault();
          try { const d = e.dataTransfer.getData('application/peerdrive-file') || e.dataTransfer.getData('application/peerdrive-entry') || e.dataTransfer.getData('text/plain'); if (d) { const parsed = d.startsWith('{') ? JSON.parse(d) : { hash: '', name: d, path: d }; entryActions?.onDrop?.({ ...parsed, targetDir: '' }); } } catch (e) { console.error('FileTree drop error:', e); }
        }}>
        {renderEntries(tree, 0, '')}
      </div>

      {/* 右键菜单 */}
      {contextMenu && (
        <div ref={ctxMenuRef}
          className="absolute z-50 bg-gray-800 border border-gray-600 rounded-lg shadow-xl py-1 min-w-[140px]"
          style={{ left: contextMenu.x, top: contextMenu.y, position: 'fixed' }}>
          <button
            onClick={() => { setRenaming(contextMenu.node.path); setContextMenu(null); }}
            className="w-full text-left px-3 py-1.5 text-xs text-gray-300 hover:bg-gray-700 flex items-center gap-2">
            ✏️ 重命名
          </button>
          {entryActions?.onRename && (
            <button
              onClick={() => { setShowMoveModal({ path: contextMenu.node.path, isDir: contextMenu.node.isDir }); setContextMenu(null); }}
              className="w-full text-left px-3 py-1.5 text-xs text-gray-300 hover:bg-gray-700 flex items-center gap-2">
              →📁 移动到...
            </button>
          )}
          {entryActions?.onRemove && (
            <button
              onClick={() => { entryActions.onRemove(contextMenu.node); setContextMenu(null); }}
              className="w-full text-left px-3 py-1.5 text-xs text-red-400 hover:bg-gray-700 flex items-center gap-2">
              🗑️ 删除
            </button>
          )}
        </div>
      )}
    </div>
  );
}
