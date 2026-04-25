import React, { useState } from 'react';
import Sha256Manager from './components/Sha256Manager';
import PathRegistrar from './components/PathRegistrar';
import AnonCollectionManager from './components/AnonCollectionManager';

export default function App() {
  const [view, setView] = useState('sha256');
  const [hashForAnon, setHashForAnon] = useState('');

  return (
    <div style={{ display: 'flex', height: '100vh', fontFamily: 'monospace' }}>
      <nav style={{ width: '220px', borderRight: '1px solid #000', padding: '20px' }}>
        <h2 style={{ marginTop: 0 }}>Peerdrive</h2>
        <ul style={{ listStyle: 'none', padding: 0 }}>
          <li><button onClick={() => setView('sha256')} style={{marginBottom:'10px', width:'100%'}}>SHA256</button></li>
          <li><button onClick={() => setView('path')} style={{marginBottom:'10px', width:'100%'}}>注册 Path</button></li>
          <li><button onClick={() => setView('anon')} style={{marginBottom:'10px', width:'100%'}}>匿名合集</button></li>
        </ul>
      </nav>

      <main style={{ flex: 1, padding: '30px', overflowY: 'auto' }}>
        {view === 'sha256' && <Sha256Manager />}
        {view === 'path' && <PathRegistrar onHashGenerated={(hash) => { setHashForAnon(hash); setView('anon'); }} />}
        {view === 'anon' && <AnonCollectionManager initialHash={hashForAnon} />}
      </main>
    </div>
  );
}