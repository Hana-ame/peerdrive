// 设置页：节点连接 / 认证 / 存储 / LLM / WebDAV / 关于，七个配置分区
import React, { useState, useEffect, useCallback } from 'react';
import * as api from '../api';
import { useNavigate } from 'react-router-dom';
import SettingsSection from '../components/SettingsSection';

// 侧边栏导航配置
const SECTIONS = [
  { id: 'node', label: '节点连接' },
  { id: 'auth', label: '认证' },
  { id: 'storage', label: '存储管理' },
  { id: 'ipfs', label: 'IPFS 兼容' },
  { id: 'llm', label: 'LLM 配置' },
  { id: 'webdav', label: 'WebDAV' },
  { id: 'about', label: '关于' },
];

const PEERDRIVE_VERSION = '0.0.1';

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(1)} ${units[i]}`;
}

function formatLimit(bytes) {
  if (bytes >= 1024 * 1024 * 1024) return (bytes / (1024 * 1024 * 1024)).toFixed(0) + 'GB';
  if (bytes >= 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(0) + 'MB';
  if (bytes >= 1024) return (bytes / 1024).toFixed(0) + 'KB';
  return bytes + 'B';
}

export default function Settings({ dataConsent, setDataConsent }) {
  const navigate = useNavigate();
  const [activeSection, setActiveSection] = useState('node');

  // ─── Node Connection ─────────────────────────────────
  const [apiBase, setApiBase] = useState(api.getApiBase());
  const [pingOk, setPingOk] = useState(null);
  const [nodeInfo, setNodeInfo] = useState(null);
  const [bootstrapPeer, setBootstrapPeer] = useState(api.getBootstrapPeer());
  const [relayServer, setRelayServer] = useState(api.getRelayServer());
  const [stunUrl, setStunUrl] = useState(api.getStunUrl());
  const [turnUrl, setTurnUrl] = useState(api.getTurnUrl());
  const [turnCredential, setTurnCredential] = useState(api.getTurnCredential());
  const [followRedirects, setFollowRedirects] = useState(api.getFollowRedirects());
  const [ipfsEnabled, setIpfsEnabled] = useState(api.getIPFSEnabled());
  const [btDhtEnabled, setBtDhtEnabled] = useState(localStorage.getItem('peerdrive_bt_dht_enabled') !== 'false');

  // ─── Auth ────────────────────────────────────────────
  const [regServer, setRegServer] = useState(localStorage.getItem('peerdrive_reg_server') || '');
  const [regUsername, setRegUsername] = useState('');
  const [regPassword, setRegPassword] = useState('');
  const [regToken, setRegToken] = useState(localStorage.getItem('peerdrive_auth_key') || '');
  const [regLoading, setRegLoading] = useState(false);
  const [regError, setRegError] = useState('');
  const [authHeaderEnabled, setAuthHeaderEnabled] = useState(api.getAuthHeaderEnabled());
  const [copied, setCopied] = useState(false);
  const [copiedBlock, setCopiedBlock] = useState(null);

  // ─── Group Management ───────────────────────────────
  const [groups, setGroups] = useState([]);
  const [groupsLoading, setGroupsLoading] = useState(false);
  const [groupName, setGroupName] = useState('');
  const [groupError, setGroupError] = useState('');

  // ─── Group Management ────────────────────────────────
  const getAuthTokenForReg = () => {
    const ft = localStorage.getItem('peerdrive_auth_token');
    if (ft) return ft;
    if (localStorage.getItem('peerdrive_auth_header_enabled') === 'true') {
      return localStorage.getItem('peerdrive_auth_key') || '';
    }
    return '';
  };

  const loadGroups = async () => {
    const token = getAuthTokenForReg();
    const username = regUsername || localStorage.getItem('peerdrive_username');
    if (!regServer || !token || !username) {
      setGroups([]);
      return;
    }
    setGroupsLoading(true);
    setGroupError('');
    try {
      const data = await api.getUserGroups(regServer, username, token);
      setGroups(data.groups || []);
    } catch (e) {
      setGroupError('获取分组失败: ' + e.message);
      setGroups([]);
    }
    setGroupsLoading(false);
  };

  const handleJoinGroup = async () => {
    const gn = groupName.trim();
    if (!gn) return;
    const token = getAuthTokenForReg();
    const username = regUsername || localStorage.getItem('peerdrive_username');
    if (!regServer || !token || !username) {
      setGroupError('请先登录');
      return;
    }
    setGroupsLoading(true);
    setGroupError('');
    try {
      await api.addUserToGroup(regServer, username, gn, token);
      setGroupName('');
      await loadGroups();
    } catch (e) {
      setGroupError('加入分组失败: ' + e.message);
    }
    setGroupsLoading(false);
  };

  // ─── Storage ─────────────────────────────────────────
  const [fileStats, setFileStats] = useState(null);
  const [storageLoading, setStorageLoading] = useState(false);

  // ─── LLM ─────────────────────────────────────────────
  const [llmEndpoint, setLlmEndpoint] = useState(api.getLlmEndpoint());
  const [llmModel, setLlmModel] = useState(api.getLlmModel());
  const [llmApiKey, setLlmApiKey] = useState(api.getLlmApiKey());
  const [llmBody, setLlmBody] = useState(api.getLlmBodyTemplate());
  const [showLlmKey, setShowLlmKey] = useState(false);

  // ─── About ───────────────────────────────────────────
  const [consentUploading, setConsentUploading] = useState(false);
  const [consentMsg, setConsentMsg] = useState('');

  // ─── IPFS Compat ─────────────────────────────────────
  const [ipfsBlockCount, setIpfsBlockCount] = useState(0);
  const [ipfsLoading, setIpfsLoading] = useState(false);
  const [ipfsToggling, setIpfsToggling] = useState(false);

  // ─── Ping ────────────────────────────────────────────
  const testPing = useCallback(async () => {
    try {
      await api.ping();
      setPingOk(true);
      const info = await api.getNodeInfo().catch(() => null);
      setNodeInfo(info);
    } catch {
      setPingOk(false);
      setNodeInfo(null);
    }
  }, []);

  useEffect(() => { testPing(); }, [apiBase, testPing]);

  // ─── Scroll spy for sidebar active section ───────────
  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined') return;
    const els = SECTIONS.map(s => document.getElementById(s.id)).filter(Boolean);
    if (!els.length) return;
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          setActiveSection(entry.target.id);
        }
      }
    }, { rootMargin: '-80px 0px -80% 0px' });
    els.forEach(el => observer.observe(el));
    return () => observer.disconnect();
  }, []);

  // ─── Node save handler ───────────────────────────────
  const handleSaveNode = () => {
    let url = apiBase.trim();
    if (url && !url.startsWith('http')) url = 'https://' + url;
    api.setApiBase(url);
    setApiBase(url);
    api.setBootstrapPeer(bootstrapPeer.trim());
    api.setRelayServer(relayServer.trim());
    api.setStunUrl(stunUrl.trim());
    api.setTurnUrl(turnUrl.trim());
    api.setTurnCredential(turnCredential.trim());
    testPing();
  };

  // ─── Auth ────────────────────────────────────────────
  const regApiCall = async (path, body) => {
    const res = await fetch(`${regServer}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    return data;
  };

  const handleRegister = async () => {
    if (!regServer || !regUsername || !regPassword) return setRegError('请填写完整');
    setRegLoading(true);
    setRegError('');
    try {
      await regApiCall('/auth/register', { username: regUsername, password: regPassword });
      const data = await regApiCall('/auth/login', { username: regUsername, password: regPassword });
      setRegToken(data.token || '');
      localStorage.setItem('peerdrive_auth_key', data.token || '');
      setRegPassword('');
    } catch (e) {
      setRegError(e.message);
    }
    setRegLoading(false);
  };

  const handleLogin = async () => {
    if (!regServer || !regUsername || !regPassword) return setRegError('请填写完整');
    setRegLoading(true);
    setRegError('');
    try {
      const data = await regApiCall('/auth/login', { username: regUsername, password: regPassword });
      setRegToken(data.token || '');
      localStorage.setItem('peerdrive_auth_key', data.token || '');
      setRegPassword('');
    } catch (e) {
      setRegError(e.message);
    }
    setRegLoading(false);
  };

  const handleSaveRegServer = () => {
    localStorage.setItem('peerdrive_reg_server', regServer.trim());
  };

  const handleCopyToken = () => {
    try {
      navigator.clipboard.writeText(regToken);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard not available
    }
  };

  const handleToggleAuthHeader = () => {
    const v = !authHeaderEnabled;
    setAuthHeaderEnabled(v);
    api.setAuthHeaderEnabled(v);
  };

  // ─── Storage ─────────────────────────────────────────
  const loadFileStats = useCallback(async () => {
    setStorageLoading(true);
    try {
      const files = await api.listFiles();
      const totalSize = ((files && files.files) || files || []).reduce((sum, f) => sum + (f.size || 0), 0);
      const count = (files && files.files) ? files.files.length : (files || []).length;
      setFileStats({ count, totalSize });
    } catch {
      setFileStats(null);
    }
    setStorageLoading(false);
  }, []);

  useEffect(() => { loadFileStats(); }, [loadFileStats]);

  // ─── IPFS compat effect ─────────────────────────
  const loadIPFSStatus = useCallback(async () => {
    setIpfsLoading(true);
    try {
      const data = await api.getIPFSCompatStatus();
      setIpfsEnabled(data.enabled);
      setIpfsBlockCount(data.block_count || 0);
    } catch {
      setIpfsEnabled(false);
      setIpfsBlockCount(0);
    }
    setIpfsLoading(false);
  }, []);

  useEffect(() => { loadIPFSStatus(); }, [loadIPFSStatus]);

  const handleIPFSToggle = async () => {
    setIpfsToggling(true);
    try {
      const data = await api.setIPFSCompatEnabled(!ipfsEnabled);
      setIpfsEnabled(data.enabled);
      if (data.enabled) {
        await loadIPFSStatus();
      } else {
        setIpfsBlockCount(0);
      }
    } catch {
      alert('切换 IPFS 兼容模式失败');
    }
    setIpfsToggling(false);
  };

  const handleGC = () => {
    alert('垃圾回收功能即将推出');
  };

  const handleClearCache = () => {
    if (confirm('确定清除所有本地缓存？这将清除所有本地存储设置和缓存数据。')) {
      const keys = Object.keys(localStorage);
      keys.forEach(k => {
        if (k.startsWith('peerdrive_')) localStorage.removeItem(k);
      });
      window.location.reload();
    }
  };

  // ─── LLM ─────────────────────────────────────────────
  const handleSaveLlm = () => {
    api.setLlmEndpoint(llmEndpoint.trim());
    api.setLlmModel(llmModel.trim());
    api.setLlmApiKey(llmApiKey.trim());
    api.setLlmBodyTemplate(llmBody);
  };

  const handleResetLlm = () => {
    api.setLlmEndpoint(api.DEFAULT_LLM_ENDPOINT);
    api.setLlmModel(api.DEFAULT_LLM_MODEL);
    api.setLlmApiKey('');
    api.setLlmBodyTemplate(api.DEFAULT_LLM_BODY);
    setLlmEndpoint(api.DEFAULT_LLM_ENDPOINT);
    setLlmModel(api.DEFAULT_LLM_MODEL);
    setLlmApiKey('');
    setLlmBody(api.DEFAULT_LLM_BODY);
  };

  // ─── Data Consent ────────────────────────────────────
  const handleConsentChange = (v) => {
    api.setDataConsent(v);
    setDataConsent(v);
    if (v) {
      setConsentUploading(true);
      setConsentMsg('');
      api.uploadConsent().then(() => {
        setConsentMsg('已上传同意记录');
      }).catch(() => {
        setConsentMsg('上传失败（设置已本地保存）');
      }).finally(() => setConsentUploading(false));
    } else {
      setConsentMsg('');
    }
  };

  return (
    <div className="flex flex-1 h-full bg-gray-950" style={{ overflow: 'hidden' }}>
      {/* ─── Left Sidebar (desktop only) ─── */}
      <aside className="hidden md:flex w-48 lg:w-56 shrink-0 border-r border-gray-800 flex-col bg-gray-900/30">
        <div className="p-4 border-b border-gray-800">
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate(-1)}
              className="text-gray-400 hover:text-white text-sm transition-colors"
            >
              &larr; 返回
            </button>
            <h2 className="text-lg font-bold">设置</h2>
          </div>
        </div>
        <nav className="flex-1 overflow-y-auto p-2 space-y-0.5">
          {SECTIONS.map((s) => (
            <a
              key={s.id}
              href={`#${s.id}`}
              onClick={(e) => {
                e.preventDefault();
                document.getElementById(s.id)?.scrollIntoView({ behavior: 'smooth' });
              }}
              className={`block px-3 py-2 rounded text-sm transition-colors ${
                activeSection === s.id
                  ? 'text-white bg-blue-600/20'
                  : 'text-gray-400 hover:text-white hover:bg-gray-800'
              }`}
            >
              {s.label}
            </a>
          ))}
        </nav>
      </aside>

      {/* ─── Right Content ─── */}
      <main className="flex-1 overflow-y-auto">
        {/* ─── Mobile section nav ─── */}
        <div className="md:hidden sticky top-0 z-10 bg-gray-950/95 backdrop-blur border-b border-gray-800 overflow-x-auto scrollbar-hide">
          <div className="flex items-center gap-1 p-2">
            <button
              onClick={() => navigate(-1)}
              className="shrink-0 text-gray-400 hover:text-white px-2 py-1.5 text-sm transition-colors"
            >
              &larr;
            </button>
            <span className="shrink-0 text-sm font-bold text-gray-200 mr-1">设置</span>
            {SECTIONS.map((s) => (
              <button
                key={s.id}
                onClick={() => {
                  document.getElementById(s.id)?.scrollIntoView({ behavior: 'smooth' });
                }}
                className={`shrink-0 px-2.5 py-1.5 rounded text-xs font-medium transition-colors ${
                  activeSection === s.id
                    ? 'text-white bg-blue-600/30'
                    : 'text-gray-400 hover:text-white hover:bg-gray-800'
                }`}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>

        <div className="p-3 md:p-6">
          <div className="max-w-2xl mx-auto space-y-3 md:space-y-6">

          {/* ==============================================
              1. 节点连接
              ============================================== */}
          <SettingsSection
            id="node"
            title="节点连接"
            description="配置 API 端点、P2P 网络连接和中继服务器参数"
            onSave={handleSaveNode}
          >
            {/* API Endpoint */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">API Endpoint</label>
              <div className="flex gap-2">
                <input
                  value={apiBase}
                  onChange={(e) => setApiBase(e.target.value)}
                  placeholder="https://your-node.com"
                  className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
                />
                <div className="flex items-center gap-1.5 px-2 shrink-0">
                  <span
                    className={`w-2 h-2 rounded-full ${
                      pingOk === null
                        ? 'bg-gray-500'
                        : pingOk
                          ? 'bg-green-500'
                          : 'bg-red-500'
                    }`}
                  />
                  <span className="text-[10px] text-gray-500 whitespace-nowrap">
                    {pingOk === null ? '检测中...' : pingOk ? '已连接' : '未连接'}
                  </span>
                </div>
              </div>
              {nodeInfo && (
                <pre className="text-[10px] text-gray-500 bg-gray-900 p-2 rounded mt-2 overflow-x-auto max-h-24">
                  {JSON.stringify(nodeInfo, null, 2)}
                </pre>
              )}
            </div>

            {/* Bootstrap Peer */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">
                P2P Bootstrap Peer
                <span className="text-gray-600 ml-1">（multiaddr)</span>
              </label>
              <input
                value={bootstrapPeer}
                onChange={(e) => setBootstrapPeer(e.target.value)}
                placeholder="/ip4/.../tcp/.../p2p/..."
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>

            {/* Relay Server */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">Relay 服务器 URL</label>
              <input
                value={relayServer}
                onChange={(e) => setRelayServer(e.target.value)}
                placeholder="wss://relay.example.com"
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>

            {/* STUN / TURN */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">STUN 服务器</label>
              <input
                value={stunUrl}
                onChange={(e) => setStunUrl(e.target.value)}
                placeholder="stun:stun.l.google.com:19302"
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1">TURN 服务器 URL</label>
              <input
                value={turnUrl}
                onChange={(e) => setTurnUrl(e.target.value)}
                placeholder="turn:turn.example.com:3478"
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1">TURN 凭据</label>
              <input
                value={turnCredential}
                onChange={(e) => setTurnCredential(e.target.value)}
                placeholder="username:credential"
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>

            {/* IPFS Gateway + BT DHT Toggles */}
            <div className="border-t border-gray-700/50 pt-3 mt-2 space-y-3">
              <p className="text-xs text-gray-400 mb-1">网络协议</p>

              {/* IPFS Gateway Toggle */}
              <div className="flex items-center gap-3">
                <label className="relative inline-flex items-center cursor-pointer">
                  <input
                    type="checkbox"
                    checked={ipfsEnabled}
                    onChange={() => {
                      const v = !ipfsEnabled;
                      setIpfsEnabled(v);
                      api.setIPFSEnabled(v);
                    }}
                    className="sr-only peer"
                  />
                  <div className="w-9 h-5 bg-gray-600 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600" />
                </label>
                <div>
                  <p className="text-sm text-gray-300">IPFS 网络</p>
                  <p className="text-[10px] text-gray-500">
                    {ipfsEnabled ? 'IPFS 网关: 已启用 (3 个可用)' : 'IPFS 网关: 已关闭'}
                  </p>
                </div>
              </div>

              {/* BT DHT Toggle */}
              <div className="flex items-center gap-3">
                <label className="relative inline-flex items-center cursor-pointer">
                  <input
                    type="checkbox"
                    checked={btDhtEnabled}
                    onChange={() => {
                      const v = !btDhtEnabled;
                      setBtDhtEnabled(v);
                      localStorage.setItem('peerdrive_bt_dht_enabled', v ? 'true' : 'false');
                    }}
                    className="sr-only peer"
                  />
                  <div className="w-9 h-5 bg-gray-600 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600" />
                </label>
                <div>
                  <p className="text-sm text-gray-300">BT DHT 网络</p>
                  <p className="text-[10px] text-gray-500">
                    {btDhtEnabled ? 'BT DHT 已启用' : 'BT DHT 已关闭'}
                  </p>
                </div>
              </div>
            </div>

            {/* Follow Redirects Toggle */}
            <div className="flex items-center gap-3 pt-2">
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={followRedirects}
                  onChange={() => {
                    const v = !followRedirects;
                    setFollowRedirects(v);
                    api.setFollowRedirects(v);
                  }}
                  className="sr-only peer"
                />
                <div className="w-9 h-5 bg-gray-600 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600" />
              </label>
              <div>
                <p className="text-sm text-gray-300">跟随重定向</p>
                <p className="text-[10px] text-gray-500">
                  下载 URL 文件时自动跟随 301/302 重定向
                </p>
              </div>
            </div>
          </SettingsSection>

          {/* ==============================================
              2. 认证
              ============================================== */}
          <SettingsSection
            id="auth"
            title="认证"
            description="注册、登录和 API 认证管理"
            onSave={handleSaveRegServer}
          >
            {/* Registration Server */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">注册服务器 URL</label>
              <input
                value={regServer}
                onChange={(e) => setRegServer(e.target.value)}
                placeholder="https://bwh.moonchan.xyz:4000"
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>

            {/* Username / Password */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">账户</label>
              <div className="flex gap-2 flex-wrap">
                <input
                  value={regUsername}
                  onChange={(e) => setRegUsername(e.target.value)}
                  placeholder="用户名"
                  className="flex-1 min-w-[120px] bg-gray-700 px-3 py-2 rounded text-sm focus:outline-none focus:border-blue-500 border border-gray-600"
                />
                <input
                  type="password"
                  value={regPassword}
                  onChange={(e) => setRegPassword(e.target.value)}
                  placeholder="密码"
                  onKeyDown={(e) => e.key === 'Enter' && handleRegister()}
                  className="flex-1 min-w-[120px] bg-gray-700 px-3 py-2 rounded text-sm focus:outline-none focus:border-blue-500 border border-gray-600"
                />
                <button
                  onClick={handleRegister}
                  disabled={regLoading}
                  className="bg-green-600 hover:bg-green-700 disabled:opacity-40 px-3 py-2 rounded text-sm whitespace-nowrap transition-colors"
                >
                  注册
                </button>
                <button
                  onClick={handleLogin}
                  disabled={regLoading}
                  className="bg-blue-600 hover:bg-blue-700 disabled:opacity-40 px-3 py-2 rounded text-sm whitespace-nowrap transition-colors"
                >
                  登录
                </button>
              </div>
              {regError && <p className="text-xs text-red-400 mt-1">{regError}</p>}
            </div>

            {/* JWT Token */}
            {regToken && (
              <div>
                <label className="block text-xs text-gray-400 mb-1">JWT Token</label>
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={regToken}
                    readOnly
                    className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none border border-gray-600 opacity-80 cursor-default truncate"
                  />
                  <button
                    onClick={handleCopyToken}
                    className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm transition-colors whitespace-nowrap"
                  >
                    {copied ? '已复制' : '复制'}
                  </button>
                </div>
                <p className="text-[10px] text-gray-500 mt-1">
                  Token 已自动保存到本地存储
                </p>
              </div>
            )}

            {/* Auth Header Toggle */}
            <div className="flex items-center gap-3 pt-2">
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={authHeaderEnabled}
                  onChange={handleToggleAuthHeader}
                  className="sr-only peer"
                />
                <div className="w-9 h-5 bg-gray-600 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600" />
              </label>
              <div>
                <p className="text-sm text-gray-300">使用 Token 认证</p>
                <p className="text-[10px] text-gray-500">
                  {authHeaderEnabled
                    ? '已启用 - API 请求将附带 Bearer Token'
                    : '未启用'}
                </p>
              </div>
            </div>

            {/* Auth Status */}
            <div className="border-t border-gray-700/50 pt-3 mt-2">
              <p className="text-xs text-gray-400 mb-2">节点身份</p>
              <div className="flex items-center gap-2">
                <span className={`w-2 h-2 rounded-full ${api.getAuthToken() ? 'bg-green-500' : 'bg-yellow-500'}`} />
                <span className="text-sm text-gray-200">
                  {api.getAuthToken() ? 'Node Owner (已认证)' : '匿名用户 (未认证)'}
                </span>
              </div>
              {api.getAuthToken() && (
                <p className="text-[10px] text-gray-500 mt-1 font-mono truncate">
                  Token: {api.getAuthToken().slice(0, 20)}...
                </p>
              )}
            </div>

            {/* Group Management */}
            <div className="border-t border-gray-700/50 pt-3 mt-2">
              <p className="text-xs text-gray-400 mb-2">分组管理</p>
              <div className="flex gap-2 mb-2">
                <input
                  value={groupName}
                  onChange={(e) => setGroupName(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleJoinGroup()}
                  placeholder="输入分组名称"
                  className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm focus:outline-none focus:border-blue-500 border border-gray-600"
                />
                <button
                  onClick={handleJoinGroup}
                  disabled={groupsLoading || !groupName.trim()}
                  className="bg-green-600 hover:bg-green-700 disabled:opacity-40 px-3 py-2 rounded text-sm transition-colors"
                >
                  加入
                </button>
                <button
                  onClick={loadGroups}
                  disabled={groupsLoading}
                  className="bg-gray-600 hover:bg-gray-500 disabled:opacity-40 px-3 py-2 rounded text-sm transition-colors"
                >
                  刷新
                </button>
              </div>
              {groupError && <p className="text-xs text-red-400 mb-1">{groupError}</p>}
              {groupsLoading ? (
                <p className="text-xs text-gray-500">加载中...</p>
              ) : groups.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {groups.map((g, i) => (
                    <span key={i} className="text-[10px] bg-blue-900/50 text-blue-300 px-2 py-1 rounded-full border border-blue-800/30">
                      {g.group_name}
                    </span>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-gray-500">暂无分组</p>
              )}
              <p className="text-[10px] text-gray-600 mt-1">
                分组允许您与其他用户共享集合访问权限
              </p>
            </div>

            {/* Upload Limits */}
            <div className="border-t border-gray-700/50 pt-3 mt-2">
              <p className="text-xs text-gray-400 mb-2">上传限制</p>
              <p className="text-sm text-gray-200">
                {formatLimit(100 * 1024 * 1024)} (已认证) / {formatLimit(10 * 1024 * 1024)} (匿名)
              </p>
              <p className="text-[10px] text-gray-500 mt-1">
                通过注册服务器认证后可获得更大的上传限额
              </p>
            </div>
          </SettingsSection>

          {/* ==============================================
              3. 存储管理
              ============================================== */}
          <SettingsSection id="storage" title="存储管理" description="查看本地文件存储状态和管理缓存">
            {/* Stats */}
            <div className="grid grid-cols-2 gap-4">
              <div className="bg-gray-900 rounded-lg p-4 text-center">
                <p className="text-2xl font-mono text-blue-400">
                  {storageLoading ? '...' : fileStats != null ? fileStats.count : '-'}
                </p>
                <p className="text-xs text-gray-500 mt-1">文件总数</p>
              </div>
              <div className="bg-gray-900 rounded-lg p-4 text-center">
                <p className="text-2xl font-mono text-green-400">
                  {storageLoading ? '...' : fileStats != null ? formatBytes(fileStats.totalSize) : '-'}
                </p>
                <p className="text-xs text-gray-500 mt-1">总大小</p>
              </div>
            </div>

            {/* Actions */}
            <div className="flex gap-2 flex-wrap pt-1">
              <button
                onClick={handleGC}
                className="bg-amber-600 hover:bg-amber-500 px-4 py-2 rounded text-sm font-medium transition-colors"
              >
                垃圾回收
              </button>
              <button
                onClick={handleClearCache}
                className="bg-red-600/80 hover:bg-red-600 px-4 py-2 rounded text-sm font-medium transition-colors"
              >
                清除缓存
              </button>
              <button
                onClick={loadFileStats}
                className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm transition-colors"
              >
                刷新统计
              </button>
            </div>
          </SettingsSection>

          {/* ==============================================
              4. IPFS 兼容
              ============================================== */}
          <SettingsSection
            id="ipfs"
            title="IPFS 兼容模式"
            description="将文件同步到 IPFS 块存储，使文件可通过 IPFS 网络被其他节点访问"
          >
            <div className="flex items-center gap-3 pt-1">
              <label className="relative inline-flex items-center cursor-pointer">
                <input
                  type="checkbox"
                  checked={ipfsEnabled}
                  onChange={handleIPFSToggle}
                  disabled={ipfsToggling}
                  className="sr-only peer"
                />
                <div className="w-9 h-5 bg-gray-600 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600" />
              </label>
              <div>
                <p className="text-sm text-gray-300">
                  {ipfsEnabled ? '已启用' : '未启用'}
                </p>
                <p className="text-[10px] text-gray-500">
                  开启后文件可以通过 IPFS 网络被其他节点访问
                </p>
              </div>
            </div>

            {/* Stats */}
            <div className="grid grid-cols-2 gap-4 mt-3">
              <div className="bg-gray-900 rounded-lg p-4 text-center">
                <p className="text-2xl font-mono text-blue-400">
                  {ipfsLoading ? '...' : ipfsBlockCount}
                </p>
                <p className="text-xs text-gray-500 mt-1">IPFS 块数</p>
              </div>
              <div className="bg-gray-900 rounded-lg p-4 text-center">
                <p className="text-sm font-mono text-gray-400">{ipfsEnabled ? '活跃' : '未激活'}</p>
                <p className="text-xs text-gray-500 mt-1">状态</p>
              </div>
            </div>

            {ipfsToggling && (
              <p className="text-[10px] text-gray-400 mt-2">处理中...</p>
            )}
          </SettingsSection>

          {/* ==============================================
              5. LLM 配置
              ============================================== */}
          <SettingsSection
            id="llm"
            title="LLM 助手配置"
            description="配置 AI 助手的大语言模型连接参数"
            onSave={handleSaveLlm}
          >
            {/* Endpoint */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">Endpoint</label>
              <input
                value={llmEndpoint}
                onChange={(e) => setLlmEndpoint(e.target.value)}
                placeholder="https://siliconflow.moonchan.xyz"
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              />
            </div>

            {/* Model */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">模型</label>
              <select
                value={api.FREE_LLM_MODELS.includes(llmModel) ? llmModel : '__custom__'}
                onChange={(e) => {
                  if (e.target.value !== '__custom__') setLlmModel(e.target.value);
                }}
                className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
              >
                {api.FREE_LLM_MODELS.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
                <option value="__custom__">自定义...</option>
              </select>
              {!api.FREE_LLM_MODELS.includes(llmModel) && (
                <input
                  value={llmModel}
                  onChange={(e) => setLlmModel(e.target.value)}
                  placeholder="输入自定义模型名"
                  className="w-full bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600 mt-1"
                />
              )}
            </div>

            {/* API Key */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">API Key</label>
              <div className="flex gap-2">
                <input
                  type={showLlmKey ? 'text' : 'password'}
                  value={llmApiKey}
                  onChange={(e) => setLlmApiKey(e.target.value)}
                  placeholder="sk-..."
                  className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none focus:border-blue-500 border border-gray-600"
                />
                <button
                  onClick={() => setShowLlmKey(!showLlmKey)}
                  className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm transition-colors"
                >
                  {showLlmKey ? '隐藏' : '显示'}
                </button>
              </div>
            </div>

            {/* Body Template */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">
                Body JSON 模板
                <span className="text-gray-600 ml-1">
                  （会替换 model + messages 字段）
                </span>
              </label>
              <textarea
                value={llmBody}
                onChange={(e) => setLlmBody(e.target.value)}
                rows={6}
                spellCheck={false}
                className="w-full bg-gray-700 px-3 py-2 rounded text-xs font-mono focus:outline-none focus:border-blue-500 border border-gray-600 resize-y"
              />
            </div>

            {/* Extra actions */}
            <div className="flex items-center gap-3 pt-1">
              <a
                href="https://cloud.siliconflow.cn/i/sRO0U8o0"
                target="_blank"
                rel="noreferrer"
                className="text-[10px] text-blue-400 hover:underline"
              >
                硅基流动注册 &rarr;
              </a>
              <button
                onClick={handleResetLlm}
                className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm transition-colors ml-auto"
              >
                恢复默认
              </button>
            </div>
            <p className="text-[10px] text-gray-600">
              Body JSON 请求体模版，填写{' '}
              <code className="text-gray-500">messages</code> 以外的所有参数。
              默认使用 OpenAI 兼容格式。流式输出默认开启 (
              <code className="text-gray-500">stream: true</code>)。
            </p>
          </SettingsSection>

          {/* ==============================================
              6. WebDAV
              ============================================== */}
          <SettingsSection id="webdav" title="WebDAV 网络驱动器" description="将 Peerdrive 存储挂载为本地网络驱动器">
            {/* Status */}
            <div className="flex items-center gap-3 pt-1">
              <div className="flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-green-500" />
                <span className="text-sm text-gray-200">已启用</span>
              </div>
            </div>

            {/* Mount URL */}
            <div>
              <label className="block text-xs text-gray-400 mb-1">挂载地址</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={api.getApiBase() + '/webdav/'}
                  readOnly
                  className="flex-1 bg-gray-700 px-3 py-2 rounded text-sm font-mono focus:outline-none border border-gray-600 opacity-80 cursor-default"
                />
                <button
                  onClick={() => {
                    try {
                      navigator.clipboard.writeText(api.getApiBase() + '/webdav/');
                    } catch {}
                  }}
                  className="bg-gray-600 hover:bg-gray-500 px-3 py-2 rounded text-sm transition-colors whitespace-nowrap"
                >
                  复制
                </button>
              </div>
            </div>

            {/* OS Instructions */}
            <div className="border-t border-gray-700/50 pt-3 mt-2">
              <p className="text-xs text-gray-400 mb-2">操作系统挂载方法（点击即复制）</p>
              <div className="space-y-3">
                <div>
                  <p className="text-sm text-gray-300 font-medium mb-1">Windows</p>
                  <code
                    onClick={() => {
                      navigator.clipboard.writeText(`net use Z: ${api.getApiBase() + '/webdav/'}`);
                      setCopiedBlock('windows');
                      setTimeout(() => setCopiedBlock(null), 1500);
                    }}
                    className={`block bg-gray-900 text-gray-300 px-3 py-2 rounded text-xs font-mono cursor-pointer border transition-colors ${copiedBlock === 'windows' ? 'border-green-500' : 'border-transparent hover:border-gray-600'}`}
                    title="点击复制"
                  >
                    net use Z: {api.getApiBase() + '/webdav/'}
                  </code>
                </div>
                <div>
                  <p className="text-sm text-gray-300 font-medium mb-1">macOS</p>
                  <p className="text-xs text-gray-400">Finder &rarr; Go &rarr; Connect to Server &rarr; 输入地址</p>
                  <code
                    onClick={() => {
                      navigator.clipboard.writeText(api.getApiBase() + '/webdav/');
                      setCopiedBlock('macos');
                      setTimeout(() => setCopiedBlock(null), 1500);
                    }}
                    className={`block bg-gray-900 text-gray-300 px-3 py-2 rounded text-xs font-mono cursor-pointer border transition-colors mt-1 ${copiedBlock === 'macos' ? 'border-green-500' : 'border-transparent hover:border-gray-600'}`}
                    title="点击复制"
                  >
                    {api.getApiBase() + '/webdav/'}
                  </code>
                </div>
                <div>
                  <p className="text-sm text-gray-300 font-medium mb-1">Linux</p>
                  <code
                    onClick={() => {
                      navigator.clipboard.writeText(`mount -t davfs ${api.getApiBase() + '/webdav/'} /mnt/peerdrive`);
                      setCopiedBlock('linux');
                      setTimeout(() => setCopiedBlock(null), 1500);
                    }}
                    className={`block bg-gray-900 text-gray-300 px-3 py-2 rounded text-xs font-mono cursor-pointer border transition-colors ${copiedBlock === 'linux' ? 'border-green-500' : 'border-transparent hover:border-gray-600'}`}
                    title="点击复制"
                  >
                    mount -t davfs {api.getApiBase() + '/webdav/'} /mnt/peerdrive
                  </code>
                </div>
              </div>
            </div>

            <p className="text-[10px] text-gray-600 mt-3">
              结合端口转发可透过 P2P 网络远程共享 WebDAV 存储。
            </p>
          </SettingsSection>

          {/* ==============================================
              7. 关于
              ============================================== */}
          <SettingsSection id="about" title="关于" description="版本信息和数据管理">
            {/* Version */}
            <div className="flex items-center gap-3">
              <span className="text-xs text-gray-400">Peerdrive Web</span>
              <span className="text-sm font-mono text-gray-200 bg-gray-900 px-2 py-0.5 rounded">
                v{PEERDRIVE_VERSION}
              </span>
            </div>

            {/* Links */}
            <div className="flex flex-wrap gap-3">
              <a
                href="https://github.com/neucn/peerdrive"
                target="_blank"
                rel="noreferrer"
                className="text-xs text-blue-400 hover:underline"
              >
                GitHub
              </a>
            </div>

            {/* Data Consent */}
            <div className="border-t border-gray-700/50 pt-3">
              <label className="flex items-center gap-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={dataConsent}
                  onChange={(e) => handleConsentChange(e.target.checked)}
                  className="w-4 h-4 rounded accent-blue-600"
                />
                <span className="text-sm text-gray-300">
                  我同意采集使用数据以改进服务
                </span>
              </label>
              <p className="text-[10px] text-gray-500 mt-2 ml-7">
                开启后会在每次操作时上传匿名使用统计到注册服务器，帮助改善产品体验。
              </p>
              {consentUploading && (
                <p className="text-[10px] text-gray-400 mt-1 ml-7">上传中...</p>
              )}
              {consentMsg && (
                <p
                  className={`text-[10px] mt-1 ml-7 ${
                    consentMsg.includes('失败') ? 'text-yellow-400' : 'text-green-400'
                  }`}
                >
                  {consentMsg}
                </p>
              )}
            </div>
          </SettingsSection>

          </div>
        </div>
      </main>
    </div>
  );
}
