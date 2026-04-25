import React, { useState } from 'react';
import Sha256Manager from './components/Sha256Manager';
import PathRegistrar from './components/PathRegistrar';
import AnonCollectionManager from './components/AnonCollectionManager';

const tabs = [
  { key: 'sha256', label: 'SHA256 寻址', icon: '#' },
  { key: 'path',   label: '注册路径',  icon: '~' },
  { key: 'anon',   label: '匿名合集',  icon: '@' },
];

export default function App() {
  const [view, setView] = useState('sha256');
  const [hashForAnon, setHashForAnon] = useState('');

  return (
    <div className="flex h-screen bg-zinc-950 text-zinc-200">
      {/* Sidebar */}
      <aside className="w-56 border-r border-zinc-800 flex flex-col shrink-0">
        <div className="h-14 flex items-center px-5 border-b border-zinc-800">
          <span className="text-lg font-bold tracking-tight text-white">Peerdrive</span>
        </div>
        <nav className="flex-1 p-3 space-y-1">
          {tabs.map(tab => (
            <button
              key={tab.key}
              onClick={() => setView(tab.key)}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors
                ${view === tab.key 
                  ? 'bg-zinc-800 text-white font-medium' 
                  : 'text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800/50'
                }`}
            >
              <span className="text-xs font-mono opacity-60">{tab.icon}</span>
              {tab.label}
            </button>
          ))}
        </nav>
        <div className="p-3 border-t border-zinc-800">
          <div className="flex items-center gap-2 text-xs text-zinc-500">
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
            节点运行中
          </div>
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 overflow-y-auto p-8">
        {view === 'sha256' && <Sha256Manager />}
        {view === 'path'   && <PathRegistrar onHashGenerated={hash => { setHashForAnon(hash); setView('anon'); }} />}
        {view === 'anon'   && <AnonCollectionManager initialHash={hashForAnon} />}
      </main>
    </div>
  );
}