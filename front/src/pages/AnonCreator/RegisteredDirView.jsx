// 已注册文件「按目录」视图：把 provider_path 当目录树下钻浏览。
// 背景：左面板曾把这个视图写丢（只剩注释占位），用户看到「已注册（按目录）是空的」。
import FileSourceRow from './FileSourceRow';

// provider_path 可能是 POSIX(/a/b) 也可能是 Windows(D:\a\b)，统一按 '/' 切分。
const seg = (p) => (p || '').replace(/\\/g, '/').split('/').filter(Boolean);

export const dirOf = (p) => seg(p).slice(0, -1).join('/');

export default function RegisteredDirView({ files, dirPath = '', onDirPath, onAdd, onDragStart, onSelect }) {
  const depth = dirPath ? seg(dirPath).length : 0;

  // 当前目录下的直接子目录：路径比当前层深一级的那个 segment
  const childDirs = new Set();
  const here = [];
  for (const f of files) {
    const s = seg(f.provider_path || f.filename || '');
    if (s.length <= depth) continue; // 理论上不会发生（depth 超过路径层级）
    if (depth > 0 && seg(dirPath).join('/') !== s.slice(0, depth).join('/')) continue;
    if (s.length === depth + 1) here.push(f);
    else childDirs.add(s.slice(0, depth + 1).join('/'));
  }

  const goUp = () => {
    const s = seg(dirPath);
    s.pop();
    onDirPath(s.join('/'));
  };

  const dirList = Array.from(childDirs).sort((a, b) => a.localeCompare(b, undefined, { numeric: true }));

  return (
    <>
      <div className="flex items-center gap-2 px-3 py-2 border-b border-white/[0.04] text-xs">
        <button onClick={goUp} disabled={!dirPath} className="text-gray-400 hover:text-white disabled:opacity-30">←</button>
        <span className="text-gray-300 font-mono text-xs truncate">/{dirPath}</span>
        <div className="flex-1" />
        <span className="text-[10px] text-gray-600">{here.length} 文件</span>
      </div>

      {dirList.map(d => (
        <div key={d} onClick={() => onDirPath(d)}
          className="flex items-center gap-3 px-4 py-2.5 hover:bg-white/[0.06] cursor-pointer border-b border-white/[0.03] text-sm">
          <span className="text-lg">📁</span>
          {/* 只显示末级目录名，全路径塞进 title 便于悬停查看 */}
          <span className="text-yellow-400 font-mono truncate flex-1 text-xs" title={d}>
            {seg(d).pop() || d}
          </span>
        </div>
      ))}

      {here.map(f => (
        <FileSourceRow key={f.hash} file={f} onAdd={onAdd} onDragStart={onDragStart}
          onSelect={onSelect ? () => onSelect(f) : undefined} />
      ))}

      {dirList.length === 0 && here.length === 0 && (
        <p className="p-4 text-gray-600 text-xs text-center">此目录为空</p>
      )}
    </>
  );
}
