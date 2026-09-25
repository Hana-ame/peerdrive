// netdisk/TransferRow.jsx：传输任务行（进度/取消）。
//
// 形态参考 qBittorrent / Cloudreve 的下载列表：名称 + 进度条 + 速度无关的
// 数字（已收/总量）+ 状态 + 取消。任务详情（目标路径、错误）折叠在第二行，
// 不抢主视觉。
import React from 'react';
import { formatSize, baseName, shortPeer } from './format';

const STATUS_TEXT = {
  running: '下载中',
  done: '已完成',
  failed: '失败',
  cancelled: '已取消',
};

const STATUS_TONE = {
  running: 'text-brand-300',
  done: 'text-green-300',
  failed: 'text-red-300',
  cancelled: 'text-gray-400',
};

export default function TransferRow({ job, onCancel }) {
  const total = Number(job.total);
  const received = Number(job.received) || 0;
  // total <= 0 表示对端没声明大小（见后端 fetchReader.Total 语义）：
  // 此时不画百分比进度，只显示已收字节——假装有进度条是骗人。
  const known = Number.isFinite(total) && total > 0;
  const pct = known ? Math.min(100, Math.round((received / total) * 100)) : 0;
  const running = job.status === 'running';

  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.08] p-3">
      <div className="flex items-center gap-2">
        <span className="truncate text-sm text-gray-200" title={job.path || job.name}>
          {job.name || baseName(job.path) || job.hash?.slice(0, 12)}
        </span>
        {job.skipped && (
          <span className="shrink-0 text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-gray-300">
            本地已有
          </span>
        )}
        <span className={`ml-auto shrink-0 text-xs ${STATUS_TONE[job.status] || 'text-gray-400'}`}>
          {STATUS_TEXT[job.status] || job.status}
        </span>
        {running && (
          <button
            type="button"
            onClick={() => onCancel?.(job)}
            className="shrink-0 text-xs px-2 py-1 rounded border border-white/[0.06] text-gray-300 hover:text-white hover:bg-white/[0.08]"
          >
            取消
          </button>
        )}
      </div>

      <div className="mt-2 h-1.5 rounded bg-white/[0.06] overflow-hidden">
        <div
          className={`h-full transition-all ${job.status === 'failed' ? 'bg-red-600' : job.status === 'done' ? 'bg-green-600' : 'bg-brand-500'}`}
          style={{ width: known ? `${pct}%` : running ? '15%' : '0%' }}
        />
      </div>

      <div className="mt-1.5 flex items-center gap-3 text-[11px] text-gray-500">
        <span>
          {formatSize(received)}
          {known ? ` / ${formatSize(total)}` : ''}
          {known && running ? ` · ${pct}%` : ''}
        </span>
        <span className="truncate" title={job.peer}>来自 {shortPeer(job.peer, 8)}</span>
        {job.collection && <span className="truncate">合集 {String(job.collection).slice(0, 8)}…</span>}
        <span className="ml-auto font-mono">{job.hash?.slice(0, 10)}…</span>
      </div>

      {job.saved_to && job.status === 'done' && (
        <div className="mt-1 text-[11px] text-gray-500 truncate" title={job.saved_to}>
          已保存到 {job.saved_to}
        </div>
      )}
      {job.error && (
        <div className="mt-1 text-[11px] text-red-400 break-all">{job.error}</div>
      )}
    </div>
  );
}
