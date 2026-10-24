import React from 'react';
import { useI18n } from '../context/I18nContext';

export default function LanguageSelector({ compact = false }) {
  const { lang, toggleLang } = useI18n();

  return (
    <button
      type="button"
      onClick={toggleLang}
      title={lang === 'zh' ? 'Switch to English' : '切换到中文'}
      className="flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full border border-white/[0.08] bg-white/[0.04] text-gray-300 hover:bg-white/[0.08] hover:text-white transition-colors focus:outline-none select-none"
      data-testid="language-selector"
    >
      <span className="text-xs">🌐</span>
      <span className="font-medium text-[11px] font-mono">
        {lang === 'zh' ? '中 / EN' : 'EN / 中'}
      </span>
    </button>
  );
}
