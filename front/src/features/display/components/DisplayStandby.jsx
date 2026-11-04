import React from 'react';

export default function DisplayStandby({
  currentTime,
  screenName,
  screenId,
  onCopyScreenId,
}) {
  return (
    <div className="flex flex-col items-center justify-center text-center p-8 z-10 max-w-xl">
      {/* Clock */}
      <div className="text-6xl md:text-8xl font-thin tracking-wider font-mono text-gray-200 mb-2">
        {currentTime.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
      </div>
      <div className="text-sm md:text-base text-gray-400 mb-10">
        {currentTime.toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })}
      </div>

      {/* Screen Card */}
      <div className="bg-neutral-900/60 border border-white/10 rounded-2xl p-6 backdrop-blur-md shadow-2xl w-full flex flex-col items-center gap-4">
        <div className="w-12 h-12 rounded-xl bg-brand-500/10 border border-brand-500/30 flex items-center justify-center text-2xl text-brand-400">
          📺
        </div>

        <div>
          <h1 className="text-xl font-bold text-white tracking-tight">{screenName}</h1>
          <p className="text-xs text-gray-400 mt-0.5">受控展示大屏已上线，随时接收投屏内容</p>
        </div>

        <div className="flex items-center gap-2 bg-black/40 px-3 py-1.5 rounded-lg border border-white/5 font-mono text-xs text-gray-300">
          <span>屏幕标识:</span>
          <span className="text-brand-300 font-bold">{screenId}</span>
          <button
            onClick={onCopyScreenId}
            className="ml-2 text-gray-400 hover:text-white transition"
            title="复制屏幕ID"
          >
            📋
          </button>
        </div>

        <div className="text-xs text-gray-500 text-center max-w-sm">
          在手机或电脑打开 Peerdrive 网盘，在文件菜单点击「投屏」，即可实时将图片或视频推送到此屏幕。
        </div>
      </div>
    </div>
  );
}
