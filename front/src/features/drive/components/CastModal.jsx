import React from 'react';

export default function CastModal({
  castFile,
  screens,
  targetScreenId,
  setTargetScreenId,
  targetChannel,
  setTargetChannel,
  castStatus,
  busy,
  handleCast,
  handleCastControl,
  onClose,
}) {
  if (!castFile) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" onClick={onClose}>
      <div className="card-surface max-w-md w-full p-5 rounded-xl border border-white/10" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-base font-semibold text-gray-200">投屏展示 (Cast to Screen)</h3>
          <button onClick={onClose} className="text-gray-400 hover:text-white text-sm">✕</button>
        </div>

        <div className="bg-white/[0.03] p-3 rounded-lg border border-white/5 mb-4">
          <div className="text-xs text-gray-300 font-medium truncate">{castFile.filename}</div>
          <div className="text-[11px] text-gray-500 font-mono mt-0.5 truncate">{castFile.hash}</div>
        </div>

        <div className="space-y-3 mb-4">
          <div>
            <label className="block text-xs text-gray-400 mb-1">目标屏幕 / 频道</label>
            {screens.length > 0 ? (
              <select
                value={targetScreenId}
                onChange={(e) => {
                  const sId = e.target.value;
                  setTargetScreenId(sId);
                  const found = screens.find(s => s.id === sId);
                  if (found) setTargetChannel(found.channel || 'default');
                }}
                className="input-base w-full text-xs"
              >
                {screens.map(s => (
                  <option key={s.id} value={s.id}>
                    {s.name} ({s.id}) - 频道: {s.channel}
                  </option>
                ))}
              </select>
            ) : (
              <div className="text-xs text-amber-400/90 bg-amber-500/10 p-2.5 rounded-lg border border-amber-500/20">
                当前无在线受控屏幕。请在 TV/另一台设备打开 <span className="font-mono underline">/display</span> 页面。
              </div>
            )}
          </div>

          <div>
            <label className="block text-xs text-gray-400 mb-1">广播频道 (可选)</label>
            <input
              type="text"
              value={targetChannel}
              onChange={(e) => setTargetChannel(e.target.value)}
              placeholder="default"
              className="input-base w-full text-xs"
            />
            <p className="text-[10px] text-gray-500 mt-1">留空或选择特定屏幕时直达该屏幕；填写频道时同步广播至频道内所有大屏。</p>
          </div>

          {castStatus && (
            <div className={`text-xs p-2.5 rounded-lg border ${
              castStatus.includes('failed')
                ? 'bg-red-500/10 border-red-500/20 text-red-400'
                : 'bg-emerald-500/10 border-emerald-500/20 text-emerald-300'
            }`}>
              {castStatus}
            </div>
          )}
        </div>

        <div className="flex items-center justify-between pt-2 border-t border-white/5">
          <div className="flex gap-1.5">
            <button
              onClick={() => handleCastControl('play')}
              className="text-xs px-2.5 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300"
              title="播放"
            >
              ▶
            </button>
            <button
              onClick={() => handleCastControl('pause')}
              className="text-xs px-2.5 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300"
              title="暂停"
            >
              ⏸
            </button>
            <button
              onClick={() => handleCastControl('clear')}
              className="text-xs px-2.5 py-1 rounded bg-red-500/10 hover:bg-red-500/20 text-red-400"
              title="清空屏幕"
            >
              清空
            </button>
          </div>

          <div className="flex gap-2">
            <button onClick={onClose} className="btn-ghost text-xs">关闭</button>
            <button onClick={handleCast} disabled={busy} className="btn-brand text-xs">
              {busy ? '投屏中...' : '投屏到此屏幕'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
