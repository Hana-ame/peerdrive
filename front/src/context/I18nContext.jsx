import React, { createContext, useContext, useState, useEffect, useCallback, useMemo } from 'react';
import { translate } from '../utils/i18n';

export const I18nContext = createContext(null);

const STORAGE_KEY = 'peerdrive_lang';

export function I18nProvider({ children }) {
  const [lang, setLangState] = useState(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved === 'zh' || saved === 'en') return saved;
      return typeof navigator !== 'undefined' && navigator.language?.startsWith('en') ? 'en' : 'zh';
    } catch {
      return 'zh';
    }
  });

  const setLang = useCallback((newLang) => {
    const l = newLang === 'en' ? 'en' : 'zh';
    setLangState(l);
    try {
      localStorage.setItem(STORAGE_KEY, l);
    } catch {}
    if (typeof document !== 'undefined') {
      document.documentElement.lang = l;
    }
  }, []);

  const toggleLang = useCallback(() => {
    setLang(lang === 'zh' ? 'en' : 'zh');
  }, [lang, setLang]);

  const t = useCallback((key, params) => {
    return translate(lang, key, params);
  }, [lang]);

  const value = useMemo(() => ({
    lang,
    setLang,
    toggleLang,
    t,
  }), [lang, setLang, toggleLang, t]);

  return (
    <I18nContext.Provider value={value}>
      {children}
    </I18nContext.Provider>
  );
}

export function useI18n() {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    return {
      lang: 'zh',
      setLang: () => {},
      toggleLang: () => {},
      t: (k, p) => translate('zh', k, p),
    };
  }
  return ctx;
}
