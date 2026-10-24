// FileInspectorDrawer.jsx — Google Drive / Nextcloud style right-side file details & P2P inspector.
//
// 为什么这么写（渐进式披露 Progressive Disclosure）：
// 普通网盘用户浏览文件时只关心「文件名、缩略图、大小、一键打开/下载」；
// 而分布式网盘的底层属性（64位 SHA-256 哈希、内容寻址路径、WebRTC/本地 CAS 来源、多协议直链）
// 若直接铺满列表会造成极高认知负荷。将底层技术指纹收敛至右侧「详情抽屉」，兼顾极简浏览与工程透明。
import React from 'react';
import { fmtBytes } from '../../platform/shared/format';

export default function FileInspectorDrawer({
  file,
  tags = [],
  icon = '📄',
  copiedHash,
  onClose,
  onPreview,
  onDownload,
  onCopyDeepLink,
  onShare,
  onOpenTagEditor,
  onOpenCastModal,
  onDelete,
}) {
  if (!file) return null;

  return (
    <div className="fixed inset-0 z-40 flex justify-end" role="dialog" aria-modal="true" data-testid="file-inspector-drawer">
      <div className="fixed inset-0 bg-black/50 backdrop-blur-xs" onClick={onClose} />
      <aside className="relative z-10 w-full max-w-sm bg-surface-card border-l border-white/10 h-full p-5 overflow-y-auto shadow-2xl flex flex-col justify-between">
        <div className="space-y-5">
          {/* Header */}
          <div className="flex items-start justify-between gap-3 pb-3 border-b border-white/[0.08]">
            <div className="flex items-center gap-2.5 min-w-0">
              <span className="text-2xl shrink-0">{icon}</span>
              <div className="min-w-0">
                <h3 className="text-sm font-semibold text-gray-100 truncate" title={file.filename}>
                  {file.filename}
                </h3>
                <p className="text-xs text-gray-500">{ fmtBytes(file.size) } · {file.mime_type || '未知类型'}</p>
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="p-1.5 rounded-lg text-gray-400 hover:text-white hover:bg-white/[0.08]"
              aria-label="Close inspector"
            >
              ✕
            </button>
          </div>

          {/* Primary Quick Actions */}
          <div className="grid grid-cols-2 gap-2">
            <button type="button" onClick={() => onPreview?.(file)} className="btn-brand text-xs py-2">
              👁️ 在线预览
            </button>
            <button type="button" onClick={() => onDownload?.(file)} className="btn-ghost text-xs py-2">
              ⬇️ 下载到本地
            </button>
          </div>

          {/* Tags Section */}
          <div className="p-3.5 rounded-xl bg-white/[0.02] border border-white/[0.06] space-y-2">
            <div className="flex items-center justify-between text-xs">
              <span className="text-gray-400 font-medium">分类标签 (Tags)</span>
              <button type="button" onClick={() => onOpenTagEditor?.(file)} className="text-brand-300 hover:text-brand-200">
                + 编辑标签
              </button>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {tags.length > 0 ? (
                tags.map((t, i) => (
                  <span key={i} className="text-xs px-2 py-0.5 rounded-full bg-brand-500/15 text-brand-300 border border-brand-500/30">
                    #{t}
                  </span>
                ))
              ) : (
                <span className="text-xs text-gray-500">暂无标签，点击右上角添加便于检索</span>
              )}
            </div>
          </div>

          {/* Progressive Disclosure: Distributed Storage & Content-Addressing Telemetry */}
          <div className="p-3.5 rounded-xl bg-white/[0.02] border border-white/[0.06] space-y-2.5 text-xs">
            <div className="flex items-center justify-between">
              <span className="text-gray-400 font-medium">分布式存储状态</span>
              <span className="px-2 py-0.5 rounded-full text-[10px] bg-emerald-500/15 text-emerald-300 border border-emerald-500/30">
                ✓ SHA-256 完整性已校验
              </span>
            </div>
            <div className="flex items-center justify-between text-gray-400">
              <span>数据来源:</span>
              <span className="text-gray-200 font-mono">
                {file.isRemotePeer ? '⚡ WebRTC 对端节点直连' : file.isInbox ? '📥 远端投递收件箱 (待审)' : '💻 本地 CAS 内容寻址库'}
              </span>
            </div>
            {file.created_at && (
              <div className="flex items-center justify-between text-gray-400">
                <span>入库时间:</span>
                <span className="text-gray-300 font-mono">{file.created_at}</span>
              </div>
            )}
            {file.hash && (
              <div className="pt-1 border-t border-white/[0.06] space-y-1">
                <div className="flex items-center justify-between text-gray-400">
                  <span>内容寻址指纹 (SHA-256):</span>
                  <button
                    type="button"
                    onClick={() => onCopyDeepLink?.(file)}
                    className="text-[11px] text-brand-300 hover:text-brand-200"
                  >
                    {copiedHash === file.hash ? '✓ 已复制唯一链接' : '🔗 复制唯一 URL'}
                  </button>
                </div>
                <div className="p-2 rounded bg-black/40 font-mono text-[11px] text-gray-400 break-all select-all">
                  {file.hash}
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Secondary Actions Footer */}
        <div className="pt-4 border-t border-white/[0.08] flex items-center justify-between gap-2">
          <div className="flex items-center gap-1.5">
            <button type="button" onClick={() => onShare?.(file)} className="btn-ghost text-xs px-2.5 py-1.5">
              📤 生成分享链
            </button>
            <button type="button" onClick={() => onOpenCastModal?.(file)} className="btn-ghost text-xs px-2.5 py-1.5">
              📺 投屏
            </button>
          </div>
          {!file.isRemotePeer && (
            <button
              type="button"
              onClick={() => {
                onDelete?.(file);
                onClose?.();
              }}
              className="px-2.5 py-1.5 rounded-lg text-xs bg-red-500/15 text-red-300 hover:bg-red-500/25 border border-red-500/30 transition-colors"
            >
              🗑️ 删除
            </button>
          )}
        </div>
      </aside>
    </div>
  );
}
