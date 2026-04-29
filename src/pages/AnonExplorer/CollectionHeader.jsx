import * as api from '../../api';

export default function CollectionHeader({ navPath, fname, entries, tags, isSingleFile, totalFiles, isLocal, searchHash, onBack, onSave, onToast }) {
  const handleShare = async () => {
    try {
      const share = await api.createShare(searchHash, 'collection', fname || '合集');
      const url = api.getShareUrl(share.token);
      await navigator.clipboard.writeText(url);
      onToast('分享链接已复制: ' + url, false);
    } catch(e) { onToast('分享失败: ' + e.message, true); }
  };

  const handleBroadcast = async () => {
    if (!searchHash) return;
    try {
      await api.p2pAnnounce(searchHash);
      onToast('广播成功', false);
    } catch(e) { onToast('广播失败: ' + e.message, true); }
  };

  const handleSave = async () => {
    if (isLocal) return;
    onSave();
  };

  return (
    <div className="flex items-center gap-2 px-4 py-2.5 border-b border-gray-800 shrink-0 flex-wrap">
      {navPath ? (
        <button onClick={onBack} className="text-gray-400 hover:text-white text-sm shrink-0">← 返回</button>
      ) : (
        <div className="flex items-center gap-2 min-w-0">
          {entries.length === 1 ? (
            <h2 className="text-base font-bold truncate">📄 {isSingleFile ? entries[0].path.split('/').pop() : (fname || '合集')}</h2>
          ) : (
            <h2 className="text-base font-bold truncate">📦 {fname || '合集'}</h2>
          )}
          {entries.length === 1 && <span className="text-[10px] bg-amber-900/40 text-amber-400 px-1.5 py-0.5 rounded-full shrink-0">单文件</span>}
          {tags?.length > 0 && (
            <div className="flex gap-1">
              {tags.map((t, i) => <span key={i} className="text-[10px] bg-blue-900/50 text-blue-300 px-2 py-0.5 rounded-full">{t}</span>)}
            </div>
          )}
        </div>
      )}
      <div className="flex-1 min-w-0" />
      <span className="text-xs text-gray-600 shrink-0">{totalFiles} 项</span>
      <div className="flex items-center gap-1 shrink-0">
        <button onClick={handleShare} className="bg-purple-600 hover:bg-purple-700 px-3 py-1 rounded text-xs">🔗 分享</button>
        <button onClick={handleBroadcast} className="bg-emerald-700 hover:bg-emerald-600 text-white px-3 py-1 rounded text-xs">📡 广播</button>
        <button onClick={handleSave}
          className={`px-3 py-1 rounded text-xs ${isLocal ? 'bg-green-500/20 text-green-400' : 'bg-blue-600 hover:bg-blue-700 text-white'}`}>
          {isLocal ? '✓ 已保存' : '💾 保存到本机'}
        </button>
      </div>
    </div>
  );
}
