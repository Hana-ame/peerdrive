// netdisk/NodeCard.jsx：市场/我的节点里的节点卡片。
//
// 版式参考 BT/PT 站点的资源条目（用户类比里的"PT/BT 思路"）：左侧是身份
// （节点 id），中间是能力摘要（共享了几个合集/文件），右侧是动作
// （加入/移出、进入节点看文件链接）。
//
// 状态用小圆点 + 文案表达（直连 / 在线 / 离线），不用颜色单独承载信息——
// 色觉障碍用户也要能分辨。
import React from 'react';
import { shortPeer, formatRelative } from './format';

function StatusDot({ connected, online }) {
  const cls = connected ? 'bg-green-400' : online ? 'bg-yellow-400' : 'bg-white/[0.1]';
  const text = connected ? '已直连' : online ? '在线' : '离线';
  return (
    <span className="inline-flex items-center gap-1.5 text-[11px] text-gray-400">
      <span className={`w-1.5 h-1.5 rounded-full ${cls}`} />
      {text}
    </span>
  );
}

export default function NodeCard({ node, onJoin, onLeave, onOpen, busy = false }) {
  const shares = node.shares || {};
  const collCount = shares.collections || 0;
  const fileCount = shares.files || 0;
  const hasShares = collCount > 0 || fileCount > 0;

  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.08] p-3 flex flex-col gap-2">
      <div className="flex items-start justify-between gap-2">
        <button
          type="button"
          onClick={() => onOpen?.(node)}
          className="text-left min-w-0"
          title={node.peer_id}
        >
          <div className="font-mono text-sm text-gray-200 truncate">{shortPeer(node.peer_id)}</div>
          <div className="text-[11px] text-gray-500 mt-0.5">
            {node.node_type || '节点'}
            {node.joined && <span className="ml-2 text-brand-400">已加入</span>}
          </div>
        </button>
        <StatusDot connected={node.connected} online={node.online} />
      </div>

      <div className="text-xs text-gray-400">
        {hasShares ? (
          <>
            共享 <span className="text-gray-200">{collCount}</span> 个合集 ·{' '}
            <span className="text-gray-200">{fileCount}</span> 个文件
          </>
        ) : (
          <span className="text-gray-600">未声明共享内容</span>
        )}
      </div>

      <div className="flex items-center justify-between gap-2 pt-1 border-t border-white/[0.04]">
        <span className="text-[11px] text-gray-600">
          {node.online ? `最近在线 ${formatRelative(node.last_seen)}` : '当前离线'}
        </span>
        <div className="flex gap-1.5">
          <button
            type="button"
            onClick={() => onOpen?.(node)}
            className="text-xs px-2 py-1 rounded border border-white/[0.06] text-gray-300 hover:text-white hover:bg-white/[0.08]"
          >
            查看文件
          </button>
          {node.joined ? (
            <button
              type="button"
              disabled={busy}
              onClick={() => onLeave?.(node)}
              className="text-xs px-2 py-1 rounded border border-red-800/50 text-red-300 hover:text-white hover:bg-red-600/30 disabled:opacity-40"
            >
              移出
            </button>
          ) : (
            <button
              type="button"
              disabled={busy}
              onClick={() => onJoin?.(node)}
              className="text-xs px-2 py-1 rounded border border-brand-700/50 text-brand-300 hover:text-white hover:bg-brand-600/30 disabled:opacity-40"
            >
              加入
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
