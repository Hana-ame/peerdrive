// 模块④：设置 —— 连接方式（WS 管理面 / PeerJS）、后端地址、认证（reg server 注册/登录）。
import React, { useState, useEffect, useCallback } from 'react';
import * as ws from '../ws';
import PeerJSConnect from '../lib/PeerJSConnect';

function getApiBase() {
  return localStorage.getItem('peerdrive_api_base') || 'https://wsl-3000.moonchan.xyz';
}

export default function Settings() {
  const [connMode, setConnMode] = useState('ws'); // 'ws' | 'peerjs'
  const [apiBaseInput, setApiBaseInput] = useState(getApiBase());
  const [pingOk, setPingOk] = useState(null); // null | true | false
  const [err, setErr] = useState('');

  // 认证（reg server）
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
    setErr('后端地址已保存，请刷新页面生效。');
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
      try { const j = await res.json(); if (j?.error) msg = j.error; } catch (e) { /* 忽略 */ }
      throw new Error(msg);
    }
    return res.json();
  };

  const doAuth = async (kind) => {
    setAuthErr('');
    if (!username.trim() || !password) { setAuthErr('请填用户名和密码'); return; }
    try {
      const d = await regCall(`/auth/${kind}`, { username: username.trim(), password });
      const token = d?.token;
      if (token) {
        localStorage.setItem('peerdrive_auth_token', token);
        setAuthUser(d.username || username.trim());
        setPassword('');
      } else {
        setAuthErr('响应里没有 token');
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
    setAuthUser(localStorage.getItem('peerdrive_auth_token') ? '已认证（有本地 token）' : '');
  }, []);

  const label = 'block text-xs text-gray-400 mb-1';
  const input = 'w-full bg-white/[0.06] px-2.5 py-1.5 text-xs font-mono rounded border border-white/[0.1] focus:outline-none focus:border-brand-500';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-3xl mx-auto space-y-6">
        <div>
          <h1 className="text-2xl font-bold">设置</h1>
          <p className="text-sm text-gray-500 mt-0.5">节点连接方式、后端地址与认证</p>
        </div>

        {err && (
          <div className="text-xs text-yellow-300 bg-yellow-400/10 border border-yellow-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {/* 连接方式 */}
        <section className="card-surface p-5 space-y-3">
          <h2 className="text-sm font-semibold text-gray-200">连接方式</h2>
          <div className="flex gap-1">
            <button onClick={() => setConnMode('ws')}
              className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                connMode === 'ws' ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
              }`}>WebSocket / HTTP（管理面）</button>
            <button onClick={() => setConnMode('peerjs')}
              className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                connMode === 'peerjs' ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
              }`}>PeerJS（信令拨号）</button>
          </div>
          <p className="text-[10px] text-gray-500">
            {connMode === 'ws'
              ? '直连后端节点管理面（/ws/peer）：管理自己的节点、文件与合集。'
              : '经公共信令按 peer id 拨号对端节点（消费端）：搜索在线节点、看共享并保存。'}
          </p>
        </section>

        {connMode === 'ws' && (
          <section className="card-surface p-5 space-y-3">
            <h2 className="text-sm font-semibold text-gray-200">后端地址（管理面）</h2>
            <div>
              <label className={label}>API 基础地址</label>
              <div className="flex gap-2">
                <input value={apiBaseInput} onChange={e => setApiBaseInput(e.target.value)} className={input + ' flex-1'} />
                <button onClick={saveBase} className="px-3 py-1.5 text-xs bg-brand-600 text-white rounded-lg hover:bg-brand-500">保存</button>
              </div>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <span className={`w-2 h-2 rounded-full ${pingOk === null ? 'bg-white/[0.16]' : pingOk ? 'bg-green-500' : 'bg-red-500'}`} />
              <span className={pingOk === null ? 'text-gray-500' : pingOk ? 'text-green-400' : 'text-red-400'}>
                {pingOk === null ? '检测中...' : pingOk ? '已连接' : '连接失败'}
              </span>
              <button onClick={testPing} className="text-gray-500 hover:text-white ml-1">重试</button>
            </div>
          </section>
        )}

        {connMode === 'peerjs' && (
          <section className="card-surface p-5 space-y-3">
            <h2 className="text-sm font-semibold text-gray-200">PeerJS 节点连接</h2>
            <PeerJSConnect />
          </section>
        )}

        {/* 认证 */}
        <section className="card-surface p-5 space-y-3">
          <h2 className="text-sm font-semibold text-gray-200">认证（注册服务器）</h2>
          <div>
            <label className={label}>注册服务器地址</label>
            <input value={regUrl} onChange={e => {
              setRegUrl(e.target.value);
              localStorage.setItem('peerdrive_reg_server_url', e.target.value);
            }} className={input} />
          </div>
          {authUser ? (
            <div className="text-xs text-gray-300 flex items-center gap-3">
              <span className="text-green-400">✓ {authUser}</span>
              <button onClick={logout} className="px-2 py-1 text-[11px] bg-white/[0.05] hover:bg-white/[0.1] rounded text-gray-300">退出</button>
            </div>
          ) : (
            <>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                <div>
                  <label className={label}>用户名</label>
                  <input value={username} onChange={e => setUsername(e.target.value)} className={input} />
                </div>
                <div>
                  <label className={label}>密码</label>
                  <input type="password" value={password} onChange={e => setPassword(e.target.value)}
                    className={input} onKeyDown={e => { if (e.key === 'Enter') doAuth('login'); }} />
                </div>
              </div>
              <div className="flex gap-2">
                <button onClick={() => doAuth('register')} className="btn-brand">注册</button>
                <button onClick={() => doAuth('login')} className="btn-ghost">登录</button>
              </div>
            </>
          )}
          {authErr && <p className="text-xs text-red-400">{authErr}</p>}
          <p className="text-[10px] text-gray-600">
            注册/登录走注册服务器（默认 account.moonchan.xyz）。浏览器跨域调用需该服务开放 CORS。
          </p>
        </section>
      </div>
    </div>
  );
}
