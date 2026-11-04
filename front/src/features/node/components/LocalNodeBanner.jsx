import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import * as ws from '../../../platform/transport-ws';
import { useI18n } from '../../../context/I18nContext';

export default function LocalNodeBanner() {
  const [isWsOpen, setIsWsOpen] = useState(() => ws.getStatus() === 'open');
  const navigate = useNavigate();
  const { t } = useI18n();

  useEffect(() => {
    return ws.onStatus((status) => setIsWsOpen(status === 'open'));
  }, []);

  if (!isWsOpen) return null;

  return (
    <div className="mb-6 p-4 rounded-xl bg-gradient-to-r from-emerald-500/10 via-brand-500/10 to-teal-500/10 border border-emerald-500/30 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-emerald-500/20 border border-emerald-500/30 flex items-center justify-center text-xl shrink-0">
          💻
        </div>
        <div>
          <div className="flex items-center gap-2">
            <span className="font-semibold text-emerald-300">{t('connect.localOnline')}</span>
            <span className="text-[10px] px-2 py-0.5 rounded-full bg-emerald-500/20 text-emerald-300 font-mono">127.0.0.1:3000</span>
          </div>
          <p className="text-xs text-gray-400 mt-0.5">
            {t('connect.localDesc')}
          </p>
        </div>
      </div>
      <div className="flex items-center gap-2 shrink-0">
        <button
          onClick={() => navigate('/drive')}
          className="px-3.5 py-1.5 text-xs font-medium rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white shadow-lg shadow-emerald-900/30 transition-all flex items-center gap-1.5"
        >
          <span>☁️ {t('connect.goToDrive')}</span>
        </button>
        <button
          onClick={() => navigate('/collections')}
          className="px-3 py-1.5 text-xs font-medium rounded-lg bg-white/[0.08] hover:bg-white/[0.14] text-gray-200 border border-white/[0.1] transition-all flex items-center gap-1.5"
        >
          <span>📦 {t('connect.goToCollections')}</span>
        </button>
      </div>
    </div>
  );
}
