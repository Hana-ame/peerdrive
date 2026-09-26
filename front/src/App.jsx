// 应用根组件：全局状态 (AppContext/PageContext) + 路由定义
import React, { useState, createContext } from 'react';
import { BrowserRouter, Routes, Route, Navigate, useParams } from 'react-router-dom';
import Plaza from './pages/Plaza';
import Explorer from './pages/Explorer';
import AnonCreator from './pages/AnonCreator/index';
import AnonExplorer from './pages/AnonExplorer/index';
import Settings from './pages/Settings';
import IPFSPanel from './pages/IPFSPanel';
import BTPanel from './pages/BTPanel';
import BTController from './pages/BTController';
import DHTExplorer from './pages/DHTExplorer';
import Navbar from './components/Navbar';
import LLMAssistant from './components/LLMAssistant';
// 网盘主界面（M4）：我的网盘 / 节点市场 / 我的节点 / 对方节点详情 / 传输任务。
// 信息架构与数据来源见 doc/NETDISK.md。
import Drive from './pages/Drive';
import Market from './pages/Market';
import Peers from './pages/Peers';
import PeerDetail from './pages/PeerDetail';
import Transfers from './pages/Transfers';
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

// Fill 给网盘页面提供一个"确定高度 + 横向 flex"的容器。
//
// 为什么需要：网盘页自身是 `flex flex-1 min-h-0`（左侧栏 + 可滚动内容区），
// 而 <Routes> 的父级 div 是块级容器——直接在块级里放 flex 行会让
// overflow-y-auto 拿不到确定高度，内容被 overflow-hidden 裁掉而不是滚动。
// 这里包一层 h-full 的 flex 行，让页面的 h-full/flex-1 有参照物。
// 既有页面（Plaza/Explorer/Settings…）自带 h-full，不受影响，故不改造。
function Fill({ children }) {
  return <div className="h-full flex overflow-hidden">{children}</div>;
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
        <div className="flex h-screen items-center justify-center bg-transparent text-gray-200 p-6">
          <div className="text-center">
            <p className="text-2xl mb-2">😵</p>
            <p className="text-sm mb-4">界面出错了，请重试或刷新页面</p>
            <button
              onClick={this.handleRetry}
              className="bg-brand-600 hover:bg-brand-700 text-white text-sm px-4 py-2 rounded"
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
          <BrowserRouter basename={import.meta.env.BASE_URL}>
          <div className="flex flex-col h-screen text-gray-200">
            <Navbar />
            <div className="flex-1 overflow-hidden">
              <Routes>
                <Route path="/" element={<Plaza />} />
                {/* ── 网盘主链路（M1-M3 的界面）──
                    /peers/:peer 是两个静态+参数段，与下面的 /:username/:collName
                    不冲突（react-router v6 按特异性排序，静态段优先）。 */}
                <Route path="/drive" element={<Fill><Drive /></Fill>} />
                <Route path="/market" element={<Fill><Market /></Fill>} />
                <Route path="/peers" element={<Fill><Peers /></Fill>} />
                <Route path="/peers/:peer" element={<Fill><PeerDetail /></Fill>} />
                <Route path="/transfers" element={<Fill><Transfers /></Fill>} />
                <Route path="/:username/:collName" element={
                  // 64 位 hex → 视为合集 hash，转统一路由
                  <HashRedirect><Explorer /></HashRedirect>
                } />
                <Route path="/create" element={<AnonCreator />} />
                <Route path="/c/:hash" element={<AnonExplorer />} />
                <Route path="/anon/collections/:hash" element={<AnonExplorer />} />
                <Route path="/anon" element={<AnonExplorer />} />
                {/* 旧 libp2p P2P 面板路由（/p2p、/p2p/dashboard、/p2p/topology）2026-08-19 随
                    libp2p 端点删除移除；/ipfs/dht 并入 /bt/dht（DHTExplorer 现仅 BEP51 采样） */}
                <Route path="/ipfs" element={<IPFSPanel />} />
                <Route path="/bt" element={<BTController />} />
                <Route path="/bt/controller" element={<Navigate to="/bt" replace />} />
                <Route path="/bt/status" element={<BTPanel />} />
                <Route path="/bt/dht" element={<DHTExplorer />} />
                <Route path="/settings" element={<Settings dataConsent={dataConsent} setDataConsent={setDataConsent} />} />
                {/* 未知路径兜底回首页，避免空白页 */}
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </div>
            <LLMAssistant />
          </div>
        </BrowserRouter>
          </AppErrorBoundary>
      </PageContext.Provider>
    </AppContext.Provider>
  );
}
