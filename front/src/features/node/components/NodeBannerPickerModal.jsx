import React, { useState } from 'react';
import { BANNER_PRESETS, getNodeBanner, setNodeBanner, getBannerStyle } from '../../../utils/nodeBanner';
import { useI18n } from '../../../context/I18nContext';

export default function NodeBannerPickerModal({ peerId, isOpen, onClose }) {
  const { t } = useI18n();
  const current = getNodeBanner(peerId);
  const [selectedPreset, setSelectedPreset] = useState(current.type === 'preset' ? current.value : 'cyberpunk');
  const [customUrl, setCustomUrl] = useState(current.type === 'url' ? current.value : '');
  const [useUrl, setUseUrl] = useState(current.type === 'url');

  if (!isOpen) return null;

  const previewConfig = useUrl && customUrl.trim()
    ? { type: 'url', value: customUrl.trim() }
    : { type: 'preset', value: selectedPreset, preset: BANNER_PRESETS.find((p) => p.id === selectedPreset) };

  const handleSave = () => {
    if (useUrl && customUrl.trim()) {
      setNodeBanner(peerId, customUrl.trim());
    } else {
      setNodeBanner(peerId, selectedPreset);
    }
    onClose();
  };

  const handleReset = () => {
    setNodeBanner(peerId, '');
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/75 backdrop-blur-sm" role="dialog" aria-modal="true">
      <div className="bg-surface-card border border-white/[0.12] rounded-2xl max-w-lg w-full p-5 shadow-2xl overflow-hidden flex flex-col gap-4 text-gray-200">
        <div className="flex items-center justify-between border-b border-white/[0.08] pb-3">
          <div className="flex items-center gap-2">
            <span className="text-lg">🎨</span>
            <h3 className="font-semibold text-sm">{t('banner.modalTitle')}</h3>
          </div>
          <button onClick={onClose} className="p-1 rounded-lg text-gray-400 hover:text-white hover:bg-white/[0.08]">✕</button>
        </div>

        {/* Preview Banner */}
        <div className="space-y-1.5">
          <span className="text-xs text-gray-400">{t('banner.preview')}</span>
          <div
            className="h-28 w-full rounded-xl relative overflow-hidden border border-white/[0.1] flex items-end p-3 shadow-inner"
            style={getBannerStyle(previewConfig)}
          >
            <div className="absolute inset-0 bg-gradient-to-t from-black/80 via-black/20 to-transparent" />
            <div className="relative z-10 flex items-center gap-2.5">
              <div className="w-10 h-10 rounded-xl bg-surface-card/90 border border-white/20 flex items-center justify-center text-lg shadow-lg">
                💻
              </div>
              <div>
                <p className="font-mono text-xs font-semibold text-white drop-shadow truncate max-w-[280px]">
                  {peerId || 'Node ID'}
                </p>
                <span className="text-[10px] text-emerald-300 drop-shadow flex items-center gap-1">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
                  {t('card.online')}
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Presets Grid */}
        <div className="space-y-2">
          <label className="text-xs font-medium text-gray-400 flex items-center justify-between">
            <span>{t('banner.presetThemes')}</span>
            <button
              type="button"
              onClick={() => setUseUrl(!useUrl)}
              className="text-[11px] text-brand-400 hover:underline"
            >
              {useUrl ? '切换到预设' : '使用自定义 URL'}
            </button>
          </label>

          {!useUrl ? (
            <div className="grid grid-cols-4 gap-2">
              {BANNER_PRESETS.map((p) => {
                const isSelected = selectedPreset === p.id;
                return (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() => setSelectedPreset(p.id)}
                    className={`h-12 rounded-lg relative overflow-hidden border transition-all flex flex-col justify-end p-1.5 text-left ${isSelected ? 'border-brand-400 ring-2 ring-brand-400/40 scale-[1.02]' : 'border-white/10 hover:border-white/30'}`}
                    style={{ background: p.gradient }}
                  >
                    <span className="text-[10px] font-medium text-white truncate drop-shadow">
                      {p.name.split(' ')[0]}
                    </span>
                  </button>
                );
              })}
            </div>
          ) : (
            <div className="space-y-1">
              <input
                type="url"
                value={customUrl}
                onChange={(e) => setCustomUrl(e.target.value)}
                placeholder="https://example.com/banner.png"
                className="w-full bg-white/[0.06] px-3 py-1.5 text-xs font-mono rounded-lg border border-white/[0.1] focus:outline-none focus:border-brand-500"
              />
              <p className="text-[10px] text-gray-500">支持直链图片地址 (HTTPS / HTTP / Data URL)</p>
            </div>
          )}
        </div>

        {/* Actions */}
        <div className="flex items-center justify-between pt-2 border-t border-white/[0.08]">
          <button
            type="button"
            onClick={handleReset}
            className="text-xs text-gray-400 hover:text-gray-200 px-2 py-1 rounded"
          >
            {t('banner.reset')}
          </button>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-xs text-gray-300 bg-white/[0.06] hover:bg-white/[0.1] rounded-lg transition-colors"
            >
              {t('banner.cancel')}
            </button>
            <button
              type="button"
              onClick={handleSave}
              className="px-4 py-1.5 text-xs font-medium text-white bg-brand-600 hover:bg-brand-500 rounded-lg shadow transition-colors"
            >
              {t('banner.save')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
