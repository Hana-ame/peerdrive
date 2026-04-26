import React, { useState, useEffect } from 'react';
import * as api from '../api';
import { useNavigate } from 'react-router-dom';

export default function Settings() {
  const [apiBase, setApiBase] = useState(api.getApiBase());
  const [authKey, setAuthKey] = useState(localStorage.getItem('peerdrive_auth_key') || '');
  const [saved, setSaved] = useState(false);
  const [nodeInfo, setNodeInfo] = useState(null);
  const [pingOk, setPingOk] = useState(null);
  const [testHash, setTestHash] = useState('');
  const [testResult, setTestResult] = useState(null);
  const [showKey, setShowKey] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    testPing();
  }, [apiBase]);

  const testPing = async () => {
    try {
      await api.ping();
      setPingOk(true);
      const info = await api.getNodeInfo().catch(() => null);
      setNodeInfo(info);
    } catch {
      setPingOk(false);
      setNodeInfo(null);
    }
  };

  const handleSave = () => {
    let url = apiBase.trim();
    if (url && !url.startsWith('http')) url = 'https://' + url;
    api.setApiBase(url);
    localStorage.setItem('peerdrive_auth_key', authKey.trim());
    setSaved(true);
    setTimeout(() => setSaved(false), 2000);
    testPing();
  };

  const handleReset = () => {
    api.setApiBase(api.DEFAULT_API);
    setApiBase(api.DEFAULT_API);
    setAuthKey('');
    localStorage.removeItem('peerdrive_auth_key');
    testPing();
  };

  const handleTestHash = async () => {
    const h = (testHash.match(/[a-f0-9]{64}/i) || [])[0] || testHash.trim().toLowerCase();
    if (h.length !== 64) { setTestResult({ ok: false, msg: '无效 hash' }); return; }
    try {
      const info = await api.verifyFile(h);
      setTestResult({ ok: true, msg: `可用: ${info.type || 'blob'}, ${info.size || '?'} bytes` });
    } catch (e) {
      setTestResult({ ok: false, msg: e.message });
    }
  };

  const addKeyToUrl = () => {
    if (!authKey.trim()) return;
    const url = new URL(window.location.href);
    url.hash = 'key=' + authKey.trim();
    window.history.replaceState({}, '', url.toString());
  };

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-gray-950">
      <div className="flex-1 max-w-2xl mx-auto p-6 overflow-y-auto">
        <div className="flex items-center gap-3 mb-6">
          <button onClick={() => navigate(-1)} className="text-gray-400 hover:text-white text-sm">← 返回</button>
          <h2 className="text-xl font-bold">设置</h2>
        </div>

        {/* Node endpoint */}
        <div className="bg-gray-800 rounded-lg p-4 mb-4 border border-gray-700">
          <h3 className="text-sm font-bold mb-3 flex items-center gap-2">
            节点端点
            <span className={`w-2 h-2 rounded-full ${pingOk === null ? 'bg-gray-500' : pingOk ? 'bg-green-500' : 'bg-red-500'}`} />
            <span className="text-[10px] text-gray-500 font-normal">
              {pingOk === null ? '检测中...' : pingOk ? '已连接' : '未连接'}
            </span>
          </h3>
          <div className="flex gap-2 mb-3">
            <input
              value={apiBase}
              onChange={e => setApiBase(e.target.value)}
              placeholder="https://your-node.com"
              className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
            />
            <button onClick={handleSave}
              className="bg-blue-600 hover:bg-blue-700 px-4 py-2 rounded text-sm font-medium">
              保存
            </button>
            <button onClick={handleReset}
              className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm">
              重置
            </button>
          </div>
          {saved && <p className="text-green-400 text-xs">已保存</p>}
          {nodeInfo && (
            <pre className="text-[10px] text-gray-500 bg-gray-900 p-2 rounded mt-2 overflow-x-auto">
              {JSON.stringify(nodeInfo, null, 2)}
            </pre>
          )}
        </div>

        {/* Auth Key */}
        <div className="bg-gray-800 rounded-lg p-4 mb-4 border border-gray-700">
          <h3 className="text-sm font-bold mb-3">认证密钥 (Key)</h3>
          <div className="flex gap-2 mb-2">
            <input
              type={showKey ? 'text' : 'password'}
              value={authKey}
              onChange={e => setAuthKey(e.target.value)}
              placeholder="访问密钥..."
              className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
            />
            <button onClick={() => setShowKey(!showKey)}
              className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm">
              {showKey ? '隐藏' : '显示'}
            </button>
          </div>
          <p className="text-[10px] text-gray-500">
            密钥会自动保存到浏览器。也可以通过 URL #key=xxx 传递。（
            <button onClick={addKeyToUrl} className="text-blue-400 hover:underline">写入 URL 哈希</button>
            ）
          </p>
        </div>

        {/* File hash test */}
        <div className="bg-gray-800 rounded-lg p-4 mb-4 border border-gray-700">
          <h3 className="text-sm font-bold mb-3">文件 Hash 验证测试</h3>
          <div className="flex gap-2 mb-2">
            <input
              value={testHash}
              onChange={e => setTestHash(e.target.value)}
              onPaste={e => {
                const t = e.clipboardData.getData('text');
                const m = (t.match(/[a-f0-9]{64}/i) || [])[0];
                if (m) { e.preventDefault(); setTestHash(m); }
              }}
              placeholder="输入 SHA256 hash 验证可用性..."
              className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
            />
            <button onClick={handleTestHash}
              className="bg-gray-600 hover:bg-gray-500 px-4 py-2 rounded text-sm">
              验证
            </button>
          </div>
          {testResult && (
            <p className={`text-xs ${testResult.ok ? 'text-green-400' : 'text-red-400'}`}>
              {testResult.ok ? '✓' : '✗'} {testResult.msg}
            </p>
          )}
        </div>

        {/* Quick links */}
        <div className="bg-gray-800 rounded-lg p-4 border border-gray-700">
          <h3 className="text-sm font-bold mb-3">快捷链接</h3>
          <div className="space-y-2 text-sm">
            <div><button onClick={() => navigate('/files')} className="text-blue-400 hover:underline">文件管理器</button></div>
            <div><button onClick={() => navigate('/anon/create')} className="text-blue-400 hover:underline">创建匿名合集</button></div>
            <div><button onClick={() => navigate('/anon')} className="text-blue-400 hover:underline">匿名合集浏览器</button></div>
          </div>
        </div>
      </div>
    </div>
  );
}
