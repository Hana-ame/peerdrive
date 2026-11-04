import React, { useState, useEffect } from 'react';
import { getNodeBanner, getBannerStyle } from '../../../utils/nodeBanner';
import { useI18n } from '../../../context/I18nContext';

const CAP_LABELS = {
  req: { name: '下载', title: '内容寻址文件拉取/下载 (req)' },
  share: { name: '共享', title: '共享清单查询 (share)' },
  index: { name: '索引', title: '文件索引与检索 (index)' },
  pull: { name: '转存', title: '跨节点拉取与转存 (pull)' },
  display: { name: '投屏', title: '大屏协同展示与控制 (display)' },
  stream: { name: '直播流', title: '实时流媒体分片广播 (stream)' },
  fwd: { name: '转发', title: '端口穿透与转发 (fwd)' },
  bt: { name: 'BT', title: 'BitTorrent 传输支持 (bt)' },
  ipfs: { name: 'IPFS', title: 'IPFS 内容解析寻址 (ipfs)' },
};

export default function NodeMarketCard({ node, onConnect, onPickBanner }) {
  const { t } = useI18n();
  const [bannerConfig, setBannerConfig] = useState(() => getNodeBanner(node?.peerId));
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const handleBannerChange = (e) => {
      if (e.detail?.peerId === node?.peerId) {
        setBannerConfig(getNodeBanner(node?.peerId));
      }
    };
    window.addEventListener('peerdrive:banner-change', handleBannerChange);
    return () => window.removeEventListener('peerdrive:banner-change', handleBannerChange);
  }, [node?.peerId]);

  const auth = node.loadInfo?.auth || (node.loadInfo?.shares?.countsHidden ? 'open' : 'open');
  const isPsk = auth === 'psk' || Boolean(node.psk);
  const sharesCount = typeof node.loadInfo?.shares_count === 'number'
    ? node.loadInfo.shares_count
    : (node.loadInfo?.shares?.collections ?? (Array.isArray(node.collections) ? node.collections.length : 0));
  const caps = Array.isArray(node.loadInfo?.caps) ? node.loadInfo.caps : [];

  const handleCopy = (e) => {
    e.stopPropagation();
    try {
      navigator.clipboard.writeText(node.peerId);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {}
  };

  return (
    <div className="group rounded-2xl bg-surface-card border border-white/[0.08] hover:border-brand-500/40 shadow-lg hover:shadow-brand-500/5 transition-all overflow-hidden flex flex-col justify-between">
      <div>
        {/* Banner Header */}
        <div
          className="h-24 w-full relative p-3 flex justify-between items-start transition-transform group-hover:scale-[1.01]"
          style={getBannerStyle(bannerConfig)}
        >
          <div className="absolute inset-0 bg-gradient-to-t from-surface-card via-black/20 to-transparent pointer-events-none" />
          
          <span className="relative z-10 px-2 py-0.5 rounded-full text-[10px] font-medium bg-black/50 text-emerald-300 border border-emerald-500/30 backdrop-blur-md flex items-center gap-1">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
            {t('card.online')}
          </span>

          <button
            type="button"
            onClick={() => onPickBanner?.(node.peerId)}
            className="relative z-10 px-2 py-0.5 rounded-full text-[10px] bg-black/60 hover:bg-black/80 text-gray-200 border border-white/20 backdrop-blur-md transition-colors flex items-center gap-1 opacity-80 group-hover:opacity-100"
            title={t('card.changeBanner')}
          >
            <span>🎨</span>
            <span>{t('card.changeBanner')}</span>
          </button>
        </div>

        {/* Card Body */}
        <div className="px-4 pb-3 -mt-6 relative z-10 space-y-3">
          {/* Avatar & Peer ID */}
          <div className="flex items-center gap-2.5">
            <div className="w-11 h-11 rounded-xl bg-surface-raised border border-white/10 flex items-center justify-center text-xl shadow-xl shrink-0">
              💻
            </div>
            <div className="min-w-0 flex-1">
              <div className="flex items-center justify-between gap-1">
                <span className="font-mono text-xs font-semibold text-gray-200 truncate" title={node.peerId}>
                  {node.peerId}
                </span>
                <button
                  type="button"
                  onClick={handleCopy}
                  className="text-[10px] text-gray-400 hover:text-brand-300 transition-colors shrink-0"
                  title={t('card.copyId')}
                >
                  {copied ? t('card.copied') : '📋'}
                </button>
              </div>
              {node.nodeType && (
                <span className="text-[10px] text-gray-500 font-mono">[{node.nodeType}]</span>
              )}
            </div>
          </div>

          {/* Access & Shares Badges */}
          <div className="flex flex-wrap items-center gap-1.5">
            <span className={`px-2 py-0.5 rounded-md text-[10px] font-medium border ${isPsk ? 'bg-amber-500/10 text-amber-300 border-amber-500/20' : 'bg-emerald-500/10 text-emerald-300 border-emerald-500/20'}`}>
              {isPsk ? '🔒 需密码' : '🔓 公开免密'}
            </span>
            <span className="px-2 py-0.5 rounded-md text-[10px] bg-white/[0.04] text-gray-300 border border-white/[0.06] font-mono">
              {sharesCount > 0 ? `📦 ${sharesCount} 个合集` : '📭 无公开内容'}
            </span>
          </div>

          {/* Capabilities */}
          {caps.length > 0 && (
            <div className="space-y-1">
              <span className="text-[10px] text-gray-500 uppercase tracking-wider font-semibold">{t('card.caps')}:</span>
              <div className="flex flex-wrap gap-1">
                {caps.map((c) => {
                  const info = CAP_LABELS[c] || { name: c, title: c };
                  return (
                    <span
                      key={c}
                      title={info.title}
                      className="px-1.5 py-0.5 rounded text-[10px] bg-white/[0.03] hover:bg-white/[0.08] text-gray-300 border border-white/[0.06] font-mono cursor-help transition-colors"
                    >
                      {info.name}
                    </span>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Card Footer */}
      <div className="px-4 py-3 border-t border-white/[0.06] bg-white/[0.01]">
        <button
          type="button"
          onClick={() => onConnect?.(node.peerId)}
          className="w-full py-2 px-3 text-xs font-medium rounded-xl bg-brand-600 hover:bg-brand-500 text-white shadow-md shadow-brand-900/30 transition-all flex items-center justify-center gap-1.5"
        >
          <span>🚀</span>
          <span>{t('connect.connectBtn')}</span>
        </button>
      </div>
    </div>
  );
}
