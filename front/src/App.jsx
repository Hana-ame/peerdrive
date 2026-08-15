// 应用根组件：全局状态 (AppContext/PageContext) + 路由定义
import React, { useState, createContext } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useParams } from 'react-router-dom';
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
import FileManager from './pages/FileManager';
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
  // 坑：旧写法在 render 期间调 navigate()（副作用），react-router 会告警且 StrictMode 下双重触发。
  // 改为声明式 <Navigate>，render 期间只返回元素。
  if (username && HASH_RE.test(username)) {
    return <Navigate to={`/c/${username}`} replace />;
  }
  if (collName && HASH_RE.test(collName)) {
    return <Navigate to={`/c/${collName}`} replace />;
  }
  return children;
}

// 全局错误边界：避免单个页面/组件抛错导致整个 React 树白屏
class AppErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidCatch(error, info) {
    console.error('Peerdrive UI error:', error, info);
  }

  handleRetry = () => this.setState({ error: null });

  render() {
    if (this.state.error) {
      return (
        <div className="flex h-screen items-center justify-center bg-gray-950 text-gray-200 p-6">
          <div className="text-center">
            <p className="text-2xl mb-2">😵</p>
            <p className="text-sm mb-4">界面出错了，请重试或刷新页面</p>
            <button
              onClick={this.handleRetry}
              className="bg-blue-600 hover:bg-blue-700 text-white text-sm px-4 py-2 rounded"
            >
              重试
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
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
        <AppErrorBoundary>
          <BrowserRouter>
          <div className="flex flex-col h-screen bg-gray-950 text-gray-200">
            <Navbar />
            <div className="flex-1 overflow-hidden">
              <Routes>
                <Route path="/" element={<Plaza />} />
                <Route path="/files" element={<FileManager />} />
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
                {/* 未知路径兜底回首页，避免空白页 */}
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </div>
            <LLMAssistant />
            <MobileNav />
          </div>
        </BrowserRouter>
          </AppErrorBoundary>
      </PageContext.Provider>
    </AppContext.Provider>
  );
}
