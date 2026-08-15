// 应用根组件：全局状态 (AppContext/PageContext) + 路由定义
import React, { useState, createContext } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useParams, useNavigate } from 'react-router-dom';
import Plaza from './pages/Plaza';
import Explorer from './pages/Explorer';
import AnonCreator from './pages/AnonCreator/index';
import AnonExplorer from './pages/AnonExplorer/index';
import Settings from './pages/Settings';
import P2PDashboard from './pages/P2PDashboard';
import IPFSPanel from './pages/IPFSPanel';
import BTPanel from './pages/BTPanel';
import BTController from './pages/BTController';
import P2PPanel from './pages/P2PPanel';
import P2PTopology from './pages/P2PTopology';
import DHTExplorer from './pages/DHTExplorer';
import Navbar from './components/Navbar';
import MobileNav from './components/MobileNav';
import LLMAssistant from './components/LLMAssistant';
import { getDataConsent } from './api';

export const AppContext = createContext();
export const PageContext = createContext({ pageContext: null, setPageContext: () => {} });

// 64 位 hex 视为合集 hash，转向统一 /c/:hash 路由
const HASH_RE = /^[a-f0-9]{64}$/i;
function HashRedirect({ children }) {
  const { username, collName } = useParams();
  const navigate = useNavigate();
  if (username && HASH_RE.test(username)) {
    navigate(`/c/${username}`, { replace: true });
    return null;
  }
  if (collName && HASH_RE.test(collName)) {
    navigate(`/c/${collName}`, { replace: true });
    return null;
  }
  return children;
}

export default function App() {
  const [username, setUsername] = useState(localStorage.getItem('peerdrive_username') || '');
  const [nodeInfo, setNodeInfo] = useState(null);
  const [pageContext, setPageContext] = useState(null);
  const [dataConsent, setDataConsent] = useState(getDataConsent());

  // 处理用户名变更，持久化到 localStorage
  const handleUsernameChange = (val) => {
    setUsername(val);
    localStorage.setItem('peerdrive_username', val);
  };

  return (
    <AppContext.Provider value={{ username, setUsername: handleUsernameChange, nodeInfo, setNodeInfo }}>
      <PageContext.Provider value={{ pageContext, setPageContext }}>
        <BrowserRouter>
          <div className="flex flex-col h-screen bg-gray-950 text-gray-200">
            <Navbar />
            <div className="flex-1 overflow-hidden">
              <Routes>
                <Route path="/" element={<Plaza />} />
                <Route path="/files" element={<Navigate to="/" replace />} />
                <Route path="/:username/:collName" element={
                  // 64 位 hex → 视为合集 hash，转统一路由
                  <HashRedirect><Explorer /></HashRedirect>
                } />
                <Route path="/create" element={<AnonCreator />} />
                <Route path="/c/:hash" element={<AnonExplorer />} />
                <Route path="/anon/collections/:hash" element={<AnonExplorer />} />
                <Route path="/anon" element={<AnonExplorer />} />
                <Route path="/p2p" element={<P2PPanel />} />
                <Route path="/ipfs" element={<IPFSPanel />} />
                <Route path="/bt" element={<BTController />} />
                <Route path="/bt/controller" element={<Navigate to="/bt" replace />} />
                <Route path="/bt/status" element={<BTPanel />} />
                <Route path="/bt/dht" element={<DHTExplorer />} />
                <Route path="/p2p/dashboard" element={<P2PDashboard />} />
                <Route path="/p2p/topology" element={<P2PTopology />} />
                <Route path="/ipfs/dht" element={<DHTExplorer />} />
                <Route path="/settings" element={<Settings dataConsent={dataConsent} setDataConsent={setDataConsent} />} />
              </Routes>
            </div>
            <LLMAssistant />
            <MobileNav />
          </div>
        </BrowserRouter>
      </PageContext.Provider>
    </AppContext.Provider>
  );
}
