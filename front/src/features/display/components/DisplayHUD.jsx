import React from 'react';

export default function DisplayHUD({
  showHud,
  connected,
  registered,
  screenName,
  channel,
  media,
  onClearMedia,
  onCopyScreenId,
  copied,
  screenId,
  onToggleFullscreen,
}) {
  return (
    <div
      className={`absolute top-4 left-4 right-4 z-50 flex items-center justify-between transition-opacity duration-300 ${
        showHud ? 'opacity-100' : 'opacity-0 pointer-events-none'
      }`}
    >
      <div className="flex items-center gap-2 bg-neutral-900/80 backdrop-blur-md px-3 py-1.5 rounded-full border border-white/10 text-xs">
        <span
          className={`w-2.5 h-2.5 rounded-full ${
            connected && registered ? 'bg-emerald-500 animate-pulse' : 'bg-red-500'
          }`}
        />
        <span className="font-medium text-gray-200">{screenName}</span>
        <span className="text-gray-500">|</span>
        <span className="text-gray-400">频道: {channel}</span>
        {media?.isLiveStream && (
          <span className="flex items-center gap-1.5 bg-red-500/20 text-red-400 px-2.5 py-0.5 rounded-full border border-red-500/30 text-[11px] font-semibold animate-pulse ml-1">
            <span className="w-1.5 h-1.5 rounded-full bg-red-500" />
            LIVE #{media.seq}
          </span>
        )}
      </div>

      <div className="flex items-center gap-2">
        {media && (
          <button
            onClick={onClearMedia}
            className="bg-neutral-900/80 hover:bg-neutral-800 text-xs text-red-400 px-3 py-1.5 rounded-full border border-white/10 transition"
            title="清空画面 (C)"
          >
            清空画面
          </button>
        )}

        <button
          onClick={onCopyScreenId}
          className="bg-neutral-900/80 hover:bg-neutral-800 text-xs text-gray-300 px-3 py-1.5 rounded-full border border-white/10 transition"
          title="复制屏幕ID用于定向遥控"
        >
          {copied ? '已复制屏幕ID' : `ID: ${screenId}`}
        </button>

        <button
          onClick={onToggleFullscreen}
          className="bg-neutral-900/80 hover:bg-neutral-800 text-xs text-gray-300 p-2 rounded-full border border-white/10 transition"
          title="全屏切换 (F)"
        >
          ⛶
        </button>
      </div>
    </div>
  );
}
