import React, { useState } from 'react';
import { createQRMatrix, generateQRSVGPath } from '../utils/qr';

export default function QRCodeModal({ isOpen, onClose, peerId, sigHost, sigPort, psk }) {
  const [copied, setCopied] = useState(false);

  if (!isOpen || !peerId) return null;

  // Build connection token / URI string
  const token = `peerdrive://peer/${encodeURIComponent(peerId)}?host=${encodeURIComponent(sigHost || '')}&port=${encodeURIComponent(sigPort || '')}&psk=${encodeURIComponent(psk || '')}`;

  let svgData = null;
  try {
    const matrix = createQRMatrix(token);
    svgData = generateQRSVGPath(matrix);
  } catch (e) {
    console.error('Failed to generate QR code:', e);
  }

  const handleCopy = async () => {
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(token);
      } else {
        const ta = document.createElement('textarea');
        ta.value = token;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (e) {
      console.error('Copy failed:', e);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm">
      <div className="bg-surface-raised border border-white/10 rounded-2xl p-6 max-w-sm w-full shadow-2xl flex flex-col items-center text-center">
        <div className="flex items-center justify-between w-full mb-4">
          <h3 className="text-base font-semibold text-white flex items-center gap-2">
            <span>📱</span> 节点直连二维码与连接串
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="text-gray-400 hover:text-white p-1 rounded-lg text-lg leading-none"
          >
            ✕
          </button>
        </div>

        <p className="text-xs text-gray-400 mb-4">
          在无公共 MQTT 或离线局域网环境下，对方扫描此二维码或粘贴输入下方字符串即可直连。
        </p>

        {svgData ? (
          <div className="bg-white p-3 rounded-xl shadow-inner mb-4 flex items-center justify-center">
            <svg
              viewBox={`0 0 ${svgData.size} ${svgData.size}`}
              className="w-48 h-48 shape-rendering-crisp"
              shapeRendering="crispEdges"
            >
              <rect width="100%" height="100%" fill="#ffffff" />
              <path d={svgData.d} fill="#111827" />
            </svg>
          </div>
        ) : (
          <div className="text-red-400 text-xs mb-4">二维码生成失败</div>
        )}

        <div className="w-full bg-white/[0.04] border border-white/[0.08] rounded-xl p-2.5 mb-4 text-left">
          <div className="text-[10px] text-gray-400 mb-1 flex items-center justify-between">
            <span>连接字符串 (Peer Token)</span>
            <span className="font-mono text-brand-300">ID: {peerId.slice(0, 12)}…</span>
          </div>
          <div className="text-xs font-mono text-gray-200 break-all select-all line-clamp-2">
            {token}
          </div>
        </div>

        <div className="flex items-center gap-3 w-full">
          <button
            type="button"
            onClick={handleCopy}
            className="flex-1 py-2 px-3 rounded-xl bg-brand-500 hover:bg-brand-400 text-white text-xs font-medium transition-colors flex items-center justify-center gap-1.5"
          >
            {copied ? '✓ 已复制连接串' : '📋 复制连接字符串'}
          </button>
          <button
            type="button"
            onClick={onClose}
            className="py-2 px-4 rounded-xl bg-white/[0.08] hover:bg-white/[0.12] text-gray-300 text-xs font-medium transition-colors"
          >
            关闭
          </button>
        </div>
      </div>
    </div>
  );
}
