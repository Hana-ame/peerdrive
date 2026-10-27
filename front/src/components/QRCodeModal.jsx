import React, { useState, useEffect } from 'react';
import { createQRMatrix, generateQRSVGPath } from '../utils/qr';
import { useI18n } from '../context/I18nContext';

export default function QRCodeModal({ isOpen, onClose, peerId, sigHost, sigPort, psk }) {
  const [copied, setCopied] = useState(false);
  const { t } = useI18n();

  useEffect(() => {
    if (!isOpen) return;
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') onClose?.();
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

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

  const handleDownloadSVG = () => {
    if (!svgData) return;
    const svgContent = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${svgData.size} ${svgData.size}" width="300" height="300"><rect width="100%" height="100%" fill="#ffffff"/><path d="${svgData.d}" fill="#111827"/></svg>`;
    const blob = new Blob([svgContent], { type: 'image/svg+xml;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `peerdrive-node-${peerId}.svg`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm">
      <div className="bg-surface-raised border border-white/10 rounded-2xl p-6 max-w-sm w-full shadow-2xl flex flex-col items-center text-center">
        <div className="flex items-center justify-between w-full mb-4">
          <h3 className="text-base font-semibold text-white flex items-center gap-2">
            <span>📱</span> {t('qr.title')}
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="text-gray-400 hover:text-white p-1 rounded-lg text-lg leading-none"
            title={t('qr.close')}
          >
            ✕
          </button>
        </div>

        <p className="text-xs text-gray-400 mb-4">
          {t('qr.desc')}
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
          <div className="text-red-400 text-xs mb-4">{t('qr.failed')}</div>
        )}

        <div className="w-full bg-white/[0.04] border border-white/[0.08] rounded-xl p-2.5 mb-4 text-left">
          <div className="text-[10px] text-gray-400 mb-1 flex items-center justify-between">
            <span>{t('qr.tokenLabel')}</span>
            <span className="font-mono text-brand-300">ID: {peerId.slice(0, 12)}…</span>
          </div>
          <div className="text-xs font-mono text-gray-200 break-all select-all line-clamp-2">
            {token}
          </div>
        </div>

        <div className="flex flex-col gap-2 w-full">
          <div className="flex items-center gap-2 w-full">
            <button
              type="button"
              onClick={handleCopy}
              className="flex-1 py-2 px-3 rounded-xl bg-brand-500 hover:bg-brand-400 text-white text-xs font-medium transition-colors flex items-center justify-center gap-1.5"
            >
              {copied ? t('qr.copied') : t('qr.copy')}
            </button>
            {svgData && (
              <button
                type="button"
                onClick={handleDownloadSVG}
                className="py-2 px-3 rounded-xl bg-white/[0.08] hover:bg-white/[0.14] text-gray-200 text-xs font-medium border border-white/[0.1] transition-colors flex items-center justify-center gap-1.5"
                title={t('qr.downloadSvg')}
              >
                {t('qr.downloadSvg')}
              </button>
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            className="w-full py-1.5 rounded-xl bg-transparent hover:bg-white/[0.05] text-gray-400 hover:text-gray-300 text-xs transition-colors"
          >
            {t('qr.close')}
          </button>
        </div>
      </div>
    </div>
  );
}
