// Home page (Module 1): node search / connect (PeerJS consumer)
// Search online peers (public signaling discover) → click connect to dial → on
// success jump to the "Node Control" page; you can also manually fill in a peer id.
import React from 'react';
import { useNavigate } from 'react-router-dom';
import PeerJSConnect from '../../../lib/PeerJSConnect';
import LocalNodeBanner from '../components/LocalNodeBanner';
import { useI18n } from '../../../context/I18nContext';

export default function Connect() {
  const navigate = useNavigate();
  const { t } = useI18n();

  return (
    <div className="p-4 sm:p-8 overflow-y-auto h-full">
      <div className="max-w-4xl mx-auto">
        <LocalNodeBanner />
        <h1 className="text-2xl font-bold mb-1">{t('connect.title')}</h1>
        <p className="text-sm text-gray-500 mb-6">
          {t('connect.subtitle')}
        </p>
        <div className="bg-white/[0.03] rounded-card border border-white/[0.06] p-5">
          <PeerJSConnect
            onConnected={() => navigate('/drive')}
          />
        </div>
      </div>
    </div>
  );
}
