import React from 'react';

export default function NodeHeader({ session, onDisconnect }) {
  return (
    <>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold">Node Control</h1>
          <p className="text-sm text-gray-500 mt-0.5">Connected to peer node — browse shared content and save</p>
        </div>
        <button onClick={onDisconnect} className="btn-ghost">Disconnect</button>
      </div>

      {/* Node info */}
      <div className="card-surface p-4 mb-4">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-xs">
          <div>
            <span className="block text-[10px] text-gray-500 mb-1">Peer Node</span>
            <span className="text-gray-200 font-mono text-sm break-all">{session.peerId}</span>
          </div>
          <div>
            <span className="block text-[10px] text-gray-500 mb-1">Local Identity</span>
            <span className="text-gray-400 font-mono text-sm break-all">{session.myId || session.client?.id || '—'}</span>
          </div>
          <div>
            <span className="block text-[10px] text-gray-500 mb-1">Status</span>
            <span className="text-green-400">Connected</span>
          </div>
        </div>
      </div>
    </>
  );
}
