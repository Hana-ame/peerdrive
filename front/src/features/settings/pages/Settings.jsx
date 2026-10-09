// Module ④: Settings —— connection mode (WS admin plane / PeerJS), backend address, authentication (reg server register/login).
import React, { useState, useEffect, useCallback } from 'react';
import * as ws from '../../../platform/transport-ws';
import PeerJSConnect from '../../../lib/PeerJSConnect';

function getApiBase() {
  return localStorage.getItem('peerdrive_api_base') || 'https://wsl-3000.moonchan.xyz';
}

export default function Settings() {
  const [connMode, setConnMode] = useState('ws'); // 'ws' | 'peerjs'
  const [apiBaseInput, setApiBaseInput] = useState(getApiBase());
  const [pingOk, setPingOk] = useState(null); // null | true | false
  const [err, setErr] = useState('');

  // Authentication (reg server)
  const [regUrl, setRegUrl] = useState(localStorage.getItem('peerdrive_reg_server_url') || 'https://account.moonchan.xyz');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [authUser, setAuthUser] = useState('');
  const [authErr, setAuthErr] = useState('');

  const testPing = useCallback(async () => {
    setPingOk(null);
    try {
      await ws.admin('GET', '/ping');
      setPingOk(true);
    } catch (e) {
      setPingOk(false);
    }
  }, []);
  useEffect(() => { testPing(); }, [testPing]);

  const saveBase = () => {
    const url = apiBaseInput.trim();
    if (!url) return;
    localStorage.setItem('peerdrive_api_base', url.startsWith('http') ? url : 'https://' + url);
    setErr('Backend address saved. Please refresh the page to take effect.');
    setTimeout(() => window.location.reload(), 800);
  };

  const regCall = async (path, body) => {
    const res = await fetch(`${regUrl.replace(/\/$/, '')}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      let msg = 'HTTP ' + res.status;
      try { const j = await res.json(); if (j?.error) msg = j.error; } catch (e) { /* ignore */ }
      throw new Error(msg);
    }
    return res.json();
  };

  const doAuth = async (kind) => {
    setAuthErr('');
    if (!username.trim() || !password) { setAuthErr('Please enter username and password'); return; }
    try {
      const d = await regCall(`/auth/${kind}`, { username: username.trim(), password });
      const token = d?.token;
      if (token) {
        localStorage.setItem('peerdrive_auth_token', token);
        setAuthUser(d.username || username.trim());
        setPassword('');
      } else {
        setAuthErr('No token in response');
      }
    } catch (e) {
      setAuthErr(e?.message || String(e));
    }
  };

  const logout = () => {
    localStorage.removeItem('peerdrive_auth_token');
    setAuthUser('');
  };

  useEffect(() => {
    setAuthUser(localStorage.getItem('peerdrive_auth_token') ? 'Authenticated (local token present)' : '');
  }, []);

  const label = 'block text-xs text-gray-400 mb-1';
  const input = 'w-full bg-white/[0.06] px-2.5 py-1.5 text-xs font-mono rounded border border-white/[0.1] focus:outline-none focus:border-brand-500';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-3xl mx-auto space-y-6">
        <div>
          <h1 className="text-2xl font-bold">Settings</h1>
          <p className="text-sm text-gray-500 mt-0.5">Node connection method, backend address, and authentication</p>
        </div>

        {err && (
          <div className="text-xs text-yellow-300 bg-yellow-400/10 border border-yellow-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {/* Connection mode */}
        <section className="card-surface p-5 space-y-3">
          <h2 className="text-sm font-semibold text-gray-200">Connection Mode</h2>
          <div className="flex gap-1">
            <button onClick={() => setConnMode('ws')}
              className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                connMode === 'ws' ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
              }`}>WebSocket / HTTP (Admin Plane)</button>
            <button onClick={() => setConnMode('peerjs')}
              className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                connMode === 'peerjs' ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
              }`}>PeerJS (Signaling Dial)</button>
          </div>
          <p className="text-xs text-gray-500">
            {connMode === 'ws'
              ? 'Direct connect to backend node admin plane (/ws/peer): manage your own node, files, and collections.'
              : 'Dial peer nodes by peer id via public signaling (consumer): search online nodes, view shares, and save.'}
          </p>
        </section>

        {connMode === 'ws' && (
          <section className="card-surface p-5 space-y-3">
            <h2 className="text-sm font-semibold text-gray-200">Backend Address (Admin Plane)</h2>
            <div>
              <label className={label}>API Base URL</label>
              <div className="flex gap-2">
                <input value={apiBaseInput} onChange={e => setApiBaseInput(e.target.value)} className={input + ' flex-1'} />
                <button onClick={saveBase} className="px-3 py-1.5 text-xs bg-brand-600 text-white rounded-lg hover:bg-brand-500">Save</button>
              </div>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <span className={`w-2 h-2 rounded-full ${pingOk === null ? 'bg-white/[0.16]' : pingOk ? 'bg-green-500' : 'bg-red-500'}`} />
              <span className={pingOk === null ? 'text-gray-500' : pingOk ? 'text-green-400' : 'text-red-400'}>
                {pingOk === null ? 'Checking...' : pingOk ? 'Connected' : 'Connection failed'}
              </span>
              <button onClick={testPing} className="text-gray-500 hover:text-white ml-1">Retry</button>
            </div>
          </section>
        )}

        {connMode === 'peerjs' && (
          <section className="card-surface p-5 space-y-3">
            <h2 className="text-sm font-semibold text-gray-200">PeerJS Node Connection</h2>
            <PeerJSConnect />
          </section>
        )}

        {/* Authentication */}
        <section className="card-surface p-5 space-y-3">
          <h2 className="text-sm font-semibold text-gray-200">Authentication (Registration Server)</h2>
          <div>
            <label className={label}>Registration Server URL</label>
            <input value={regUrl} onChange={e => {
              setRegUrl(e.target.value);
              localStorage.setItem('peerdrive_reg_server_url', e.target.value);
            }} className={input} />
          </div>
          {authUser ? (
            <div className="text-xs text-gray-300 flex items-center gap-3">
              <span className="text-green-400">✓ {authUser}</span>
              <button onClick={logout} className="px-2 py-1 text-[11px] bg-white/[0.05] hover:bg-white/[0.1] rounded text-gray-300">Logout</button>
            </div>
          ) : (
            <>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                <div>
                  <label className={label}>Username</label>
                  <input value={username} onChange={e => setUsername(e.target.value)} className={input} />
                </div>
                <div>
                  <label className={label}>Password</label>
                  <input type="password" value={password} onChange={e => setPassword(e.target.value)}
                    className={input} onKeyDown={e => { if (e.key === 'Enter') doAuth('login'); }} />
                </div>
              </div>
              <div className="flex gap-2">
                <button onClick={() => doAuth('register')} className="btn-brand">Register</button>
                <button onClick={() => doAuth('login')} className="btn-ghost">Login</button>
              </div>
            </>
          )}
          {authErr && <p className="text-xs text-red-400">{authErr}</p>}
          <p className="text-xs text-gray-600">
            Registration/login goes through the registration server (default account.moonchan.xyz). Cross-origin browser calls require this service to have CORS enabled.
          </p>
        </section>
      </div>
    </div>
  );
}
