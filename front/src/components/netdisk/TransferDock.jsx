// TransferDock.jsx — Google Drive / Dropbox style collapsible bottom-right transfer tray.
//
// 为什么这么写（认知负荷降低 & RELEASE-READINESS 缺口补齐）：
// 传统 P2P 工具迫使用户切到专门的「传输页」盯进度条，打断网盘浏览心流。
// 本微组件订阅 peerdrive-core 的 globalProgressHub 单例，在有活跃或刚完成的传输时
// 自动在右下角浮现紧凑进度托盘，可一键折叠为胶囊或跳转 #/transfers，保持主界面零干扰。
import React, { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { globalProgressHub, fmtBytes, fmtRate } from '../../platform/shared';

export default function TransferDock() {
  const [snap, setSnap] = useState(() => globalProgressHub.getSnapshot());
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    return globalProgressHub.subscribe((next) => {
      setSnap({ ...next, tasks: [...(next.tasks || [])] });
    });
  }, []);

  const tasks = snap?.tasks || [];
  if (tasks.length === 0) return null;

  const activeCount = snap.totalActive || 0;
  const recentTasks = tasks.slice(-4).reverse();

  return (
    <div
      data-testid="transfer-dock"
      className="fixed bottom-16 md:bottom-5 right-4 z-40 w-80 max-w-[calc(100vw-2rem)] rounded-2xl bg-surface-raised/95 backdrop-blur-xl border border-white/10 shadow-2xl overflow-hidden transition-all"
    >
      {/* Header Bar */}
      <div className="px-3.5 py-2.5 bg-white/[0.03] border-b border-white/[0.06] flex items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex items-center gap-2 text-xs font-medium text-gray-200 hover:text-white focus:outline-none min-w-0"
        >
          <span className={`w-2 h-2 rounded-full shrink-0 ${activeCount > 0 ? 'bg-brand-400 animate-pulse' : 'bg-emerald-400'}`} />
          <span className="truncate">
            {activeCount > 0 ? `正在传输 ${activeCount} 项…` : `已完成 ${tasks.length} 项传输`}
          </span>
        </button>
        <div className="flex items-center gap-1.5 shrink-0">
          {snap.totalCompleted > 0 && (
            <button
              type="button"
              onClick={() => globalProgressHub.clearCompleted()}
              className="text-[11px] text-gray-400 hover:text-gray-200 px-1.5 py-0.5 rounded hover:bg-white/[0.06]"
              title="清理已完成项"
            >
              清理
            </button>
          )}
          <Link
            to="/transfers"
            className="text-[11px] text-brand-300 hover:text-brand-200 px-1.5 py-0.5 rounded hover:bg-brand-500/10"
          >
            详情
          </Link>
          <button
            type="button"
            onClick={() => setCollapsed((c) => !c)}
            className="text-xs text-gray-400 hover:text-white px-1"
            aria-label={collapsed ? 'Expand transfer tray' : 'Collapse transfer tray'}
          >
            {collapsed ? '▲' : '▼'}
          </button>
        </div>
      </div>

      {/* Task List Body */}
      {!collapsed && (
        <div className="p-2.5 space-y-2 max-h-56 overflow-y-auto">
          {recentTasks.map((t) => {
            const isRunning = t.status === 'running' || t.status === 'queued';
            const isDone = t.status === 'done';
            const isErr = t.status === 'error';
            return (
              <div key={t.id} className="p-2 rounded-xl bg-white/[0.02] border border-white/[0.05] text-xs space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-gray-200 font-medium truncate" title={t.name || t.hash}>
                    {t.name || (t.hash ? `${t.hash.slice(0, 10)}…` : '传输任务')}
                  </span>
                  <span className="text-[10px] font-mono shrink-0 text-gray-400">
                    {isDone ? '✓ 完成' : isErr ? '⚠️ 失败' : `${t.pct ?? 0}%`}
                  </span>
                </div>
                <div className="w-full h-1.5 bg-white/[0.06] rounded-full overflow-hidden">
                  <div
                    className={`h-full transition-all duration-200 ${
                      isErr ? 'bg-red-500' : isDone ? 'bg-emerald-500' : 'bg-brand-500'
                    }`}
                    style={{ width: `${isDone ? 100 : Math.max(4, t.pct || 0)}%` }}
                  />
                </div>
                <div className="flex items-center justify-between text-[10px] text-gray-500 font-mono">
                  <span>
                    {fmtBytes(t.received)}
                    {t.total > 0 ? ` / ${fmtBytes(t.total)}` : ''}
                  </span>
                  <span>{isRunning && t.rateBps > 0 ? fmtRate(t.rateBps) : (t.transport || 'P2P')}</span>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
