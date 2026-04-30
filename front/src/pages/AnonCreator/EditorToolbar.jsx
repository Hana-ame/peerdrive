import { llmSuggest } from './utils';

export default function EditorToolbar({ fname, tags, entryCount, validCount, saving, onFname, onTags, onAiName, onSave }) {
  const handleAiClick = async () => {
    try { const n = await llmSuggest(''); if (n) onFname(n); } catch {}
  };

  return (
    <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-800 shrink-0 flex-wrap">
      <input value={fname} onChange={e => onFname(e.target.value)} placeholder="合集名称" className="bg-gray-800 text-xs px-2 py-1.5 rounded border border-gray-700 w-28 focus:outline-none focus:border-blue-500" />
      <input value={tags} onChange={e => onTags(e.target.value)} placeholder="标签: a, b" className="bg-gray-800 text-xs px-2 py-1.5 rounded border border-gray-700 w-24 focus:outline-none focus:border-blue-500" />
      <button onClick={onAiName || handleAiClick} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1 rounded shrink-0" title="AI 推荐名称">🤖</button>
      <div className="flex-1" />
      <span className="text-[10px] text-gray-500">{validCount} 个文件</span>
      <button onClick={onSave} disabled={saving || !validCount}
        className="bg-green-600 hover:bg-green-700 disabled:opacity-40 text-sm px-3 py-1.5 rounded font-medium">💾 保存</button>
    </div>
  );
}
