// Home page (Module 1): node search / connect (PeerJS consumer)
// Search online peers (public signaling discover) → click connect to dial → on
// success jump to the "Node Control" page; you can also manually fill in a peer id.
import React from 'react';
import { useNavigate } from 'react-router-dom';
import PeerJSConnect from '../../../lib/PeerJSConnect';

export default function Connect() {
  const navigate = useNavigate();
  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-4xl mx-auto">
        <h1 className="text-2xl font-bold mb-1">Node Search / Connect</h1>
        <p className="text-sm text-gray-500 mb-6">
          Search online nodes or enter a peer id to connect; on successful connection, enter the Node Control page.
        </p>
        <div className="bg-white/[0.03] rounded-card border border-white/[0.06] p-5">
          <PeerJSConnect
            onConnected={() => navigate('/node')}
          />
        </div>
      </div>
    </div>
  );
}
