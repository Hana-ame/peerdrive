import { collDisplayName } from './utils';

export default function CollBrowserNav({ coll, collViewPath, totalFiles, selectMode, selectedFileCount, onLeave, onPathNav, onSaveToNode, onSelectToggle, onBatchSave }) {
  return (
    <div className="flex flex-col px-3 py-2 border-b border-gray-800 shrink-0 gap-1 bg-gray-900 sticky top-0 z-10">
      <div className="flex items-center gap-2">
        <button onClick={onLeave} className="text-gray-400 hover:text-white text-sm">← 合集列表</button>
        <span className="text-gray-300 text-sm font-bold truncate">📦 {coll.friendly_name || collDisplayName(coll)}</span>
      </div>
      {collViewPath && (
        <div className="flex items-center gap-1 text-[10px] ml-6">
          <button onClick={() => onPathNav('')} className="text-gray-500 hover:text-white">📦</button>
          {collViewPath.split('/').map((p, i) => (
            <span key={i} className="flex items-center gap-1">
              <span className="text-gray-600">/</span>
              <button onClick={() => onPathNav(collViewPath.split('/').slice(0, i+1).join('/'))}
                className="text-gray-400 hover:text-white">{p}</button>
            </span>
          ))}
        </div>
      )}
      <div className="flex items-center gap-2 text-[10px]">
        <span className="text-gray-600">{totalFiles} 项</span>
        <div className="flex-1" />
        {!collViewPath && (
          <button onClick={onSaveToNode}
            className="text-[10px] bg-blue-600 hover:bg-blue-700 px-2 py-0.5 rounded">💾 保存到本机</button>
        )}
        <button onClick={onSelectToggle} className={`text-[10px] px-2 py-0.5 rounded ${selectMode ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400'}`}>
          {selectMode ? '取消' : '选择文件'}
        </button>
      </div>
      {selectMode && selectedFileCount > 0 && (
        <button onClick={onBatchSave}
          className="text-[10px] bg-blue-600 hover:bg-blue-700 px-2 py-1 rounded">
          💾 保存选中的 {selectedFileCount} 个文件为新合集
        </button>
      )}
    </div>
  );
}
