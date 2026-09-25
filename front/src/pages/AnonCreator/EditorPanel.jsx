import { useState } from 'react';
import FileTree from '../../components/FileTree';
import EditorToolbar from './EditorToolbar';
import NamePrompt from './NamePrompt';
import VisibilityPicker, { VISIBILITY_PUBLIC } from './VisibilityPicker';

export default function EditorPanel({
  fname, tags, entries, saving, showNamePrompt, toastMsg, toastErr, entryActions,
  visibility, accessList, operator, onFname, onTags, onSave, onAddUrl, onCloseNamePrompt,
  onVisibilityChange, onRequestAccounts, onBroadcast,
}) {
  const validCount = entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value)).length;
  const [showUrlInput, setShowUrlInput] = useState(false);
  const [urlValue, setUrlValue] = useState('');
  const [urlName, setUrlName] = useState('');
  const [urlRegister, setUrlRegister] = useState(false);
  const [urlLoading, setUrlLoading] = useState(false);

  const handleUrlSubmit = async () => {
    const u = urlValue.trim();
    if (!u) return;
    setUrlLoading(true);
    await onAddUrl(u, urlName.trim() || '', urlRegister);
    setUrlValue('');
    setUrlName('');
    setUrlLoading(false);
    setShowUrlInput(false);
  };

  return (
    <div className="flex-1 flex flex-col min-h-[200px]">
      {toastMsg && (
        <div className={`px-3 py-1 text-xs shrink-0 ${toastErr ? 'text-red-400 bg-red-500/10' : 'text-green-400 bg-green-500/10'}`}>{toastMsg}</div>
      )}

      <EditorToolbar fname={fname} tags={tags} entryCount={entries.length}
        validCount={validCount} saving={saving}
        onFname={onFname} onTags={onTags} onSave={onSave}
        onShowUrlInput={() => setShowUrlInput(true)} />

      {/* 广播权限三选项：只有 public 才允许广播（非公开的合集广播出去也没人能拉，
          反而把 hash 泄露给 DHT 上的陌生人） */}
      <div className="px-3 py-1.5 border-b border-white/[0.04] bg-white/[0.06] shrink-0">
        <VisibilityPicker visibility={visibility} accessList={accessList}
          operator={operator}
          onChange={onVisibilityChange} onRequestAccounts={onRequestAccounts} />
        {visibility === VISIBILITY_PUBLIC && (
          <div className="flex items-center gap-2 mt-1.5">
            <button onClick={onBroadcast} disabled={saving || validCount === 0}
              className="text-[11px] px-2 py-1 rounded bg-emerald-600 hover:bg-emerald-500 disabled:opacity-40 text-white font-medium">
              📡 保存并广播
            </button>
            <span className="text-[10px] text-gray-500">广播后网络上任何节点都能按 hash 拉取</span>
          </div>
        )}
      </div>

      {/* URL 添加行 */}
      {showUrlInput && (
        <div className="flex items-center gap-1.5 px-3 py-1.5 border-b border-white/[0.04] shrink-0 bg-white/[0.08]">
          <span className="text-[10px] text-gray-500 shrink-0">URL:</span>
          <input value={urlValue} onChange={e => setUrlValue(e.target.value)}
            placeholder="https://..."
            className="flex-1 bg-white/[0.06] text-xs px-2 py-1.5 rounded border border-white/[0.06] focus:outline-none focus:border-brand-500 font-mono"
            onKeyDown={e => { if (e.key === 'Enter') handleUrlSubmit(); if (e.key === 'Escape') setShowUrlInput(false); }}
            autoFocus />
          <input value={urlName} onChange={e => setUrlName(e.target.value)}
            placeholder="文件名（可选）"
            className="w-24 bg-white/[0.06] text-xs px-2 py-1.5 rounded border border-white/[0.06] focus:outline-none focus:border-brand-500 hidden md:block" />
          <label className="flex items-center gap-1 text-[10px] text-gray-500 cursor-pointer shrink-0">
            <input type="checkbox" checked={urlRegister} onChange={e => setUrlRegister(e.target.checked)} className="w-3 h-3" />
            下载
          </label>
          <button onClick={handleUrlSubmit} disabled={urlLoading || !urlValue.trim()}
            className="bg-brand-600 hover:bg-brand-700 disabled:opacity-40 text-xs px-2 py-1.5 rounded shrink-0">+</button>
          <button onClick={() => setShowUrlInput(false)} className="text-gray-500 hover:text-white text-xs px-1 shrink-0">×</button>
        </div>
      )}

      {showNamePrompt && (
        <NamePrompt onSkip={onSave} onClose={onCloseNamePrompt} />
      )}

      <div className="flex-1 overflow-hidden">
        <FileTree entries={entries} entryActions={entryActions} />
      </div>
    </div>
  );
}
