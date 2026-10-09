// DataState.jsx — Unified three-phase state container (Loading Skeleton, Empty State, Error with Retry).
//
// 架构设计（issue #149）：
// 1. Loading: 结构化骨架屏（Skeleton），避免 spinner 的跳动与等待焦虑。
// 2. Empty: 语义化文案 + 明确的引导动作（CTA button），绝不留空白死胡同。
// 3. Error: 结构化错误分类（未连接/超时/鉴权/缺失）+ 幂等重试（Retry）+ 降级替代建议。

import React from 'react';
import { classifyError } from '../../platform/shared/errorClassifier';

export function SkeletonTable({ rows = 5, cols = 4 }) {
  return (
    <div className="card-surface overflow-hidden p-4 space-y-3 animate-pulse" aria-label="Loading skeleton">
      <div className="h-4 bg-white/[0.06] rounded w-1/4 mb-4" />
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="flex items-center gap-4 py-2 border-t border-white/[0.04]">
          {Array.from({ length: cols }).map((_, c) => (
            <div
              key={c}
              className="h-3.5 bg-white/[0.04] rounded"
              style={{
                width: c === 0 ? '35%' : c === cols - 1 ? '15%' : '20%',
              }}
            />
          ))}
        </div>
      ))}
    </div>
  );
}

export function SkeletonCards({ count = 6 }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-4 animate-pulse" aria-label="Loading skeleton">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="card-surface p-4 rounded-xl space-y-3 border border-white/[0.04]">
          <div className="w-8 h-8 rounded bg-white/[0.06]" />
          <div className="h-3.5 bg-white/[0.06] rounded w-3/4" />
          <div className="h-2.5 bg-white/[0.04] rounded w-1/2" />
        </div>
      ))}
    </div>
  );
}

export function EmptyState({
  icon = '📂',
  title = 'No items found',
  description = 'No records are available at this moment.',
  actionLabel,
  onAction,
}) {
  return (
    <div className="text-center py-16 px-4 card-surface rounded-2xl border-2 border-dashed border-white/10 flex flex-col items-center justify-center">
      <div className="text-4xl mb-3">{icon}</div>
      <h3 className="text-sm font-semibold text-gray-200">{title}</h3>
      <p className="text-xs text-gray-500 mt-1 max-w-sm">{description}</p>
      {actionLabel && onAction && (
        <button
          onClick={onAction}
          className="btn-brand text-xs mt-4 px-4 py-1.5"
        >
          {actionLabel}
        </button>
      )}
    </div>
  );
}

export function ErrorState({
  error,
  onRetry,
  fallbackAction,
}) {
  const info = classifyError(error);

  return (
    <div className="card-surface p-6 rounded-2xl border border-red-500/20 bg-red-500/[0.03] text-center flex flex-col items-center max-w-lg mx-auto my-6">
      <div className="text-3xl mb-2">{info.icon}</div>
      <h3 className="text-sm font-semibold text-red-300">{info.title}</h3>
      <p className="text-xs text-gray-400 mt-1.5">{info.message}</p>
      <p className="text-[11px] text-gray-500 mt-1 italic">{info.suggestion}</p>

      <div className="flex items-center gap-3 mt-5">
        {onRetry && (
          <button
            onClick={onRetry}
            className="btn-brand text-xs px-4 py-1.5 flex items-center gap-1.5"
          >
            <span>🔄</span>
            <span>Retry</span>
          </button>
        )}
        {fallbackAction && (
          <button
            onClick={fallbackAction.onAction}
            className="btn-ghost text-xs px-3 py-1.5"
          >
            {fallbackAction.label}
          </button>
        )}
      </div>
    </div>
  );
}

export default function DataState({
  loading,
  error,
  empty,
  onRetry,
  skeletonType = 'table', // 'table' | 'cards'
  skeletonRows = 5,
  emptyProps = {},
  fallbackAction,
  children,
}) {
  if (loading) {
    return skeletonType === 'cards' ? (
      <SkeletonCards count={skeletonRows} />
    ) : (
      <SkeletonTable rows={skeletonRows} />
    );
  }

  if (error) {
    return (
      <ErrorState
        error={error}
        onRetry={onRetry}
        fallbackAction={fallbackAction}
      />
    );
  }

  if (empty) {
    return <EmptyState {...emptyProps} />;
  }

  return children;
}
