import React, { useState, createContext } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Plaza from './pages/Plaza';
import Explorer from './pages/Explorer';
import FileManager from './pages/FileManager';
import AnonCreator from './pages/AnonCreator';
import AnonExplorer from './pages/AnonExplorer';
import Settings from './pages/Settings';
import P2PDashboard from './pages/P2PDashboard';
import Navbar from './components/Navbar';
import LLMAssistant from './components/LLMAssistant';
import { getDataConsent } from './api';

export const AppContext = createContext();
export const PageContext = createContext({ pageContext: null, setPageContext: () => {} });

export default function App() {
  const [username, setUsername] = useState(localStorage.getItem('peerdrive_username') || '');
  const [nodeInfo, setNodeInfo] = useState(null);
  const [pageContext, setPageContext] = useState(null);
  const [dataConsent, setDataConsent] = useState(getDataConsent());

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
                <Route path="/files" element={<FileManager />} />
                <Route path="/:username/:collName" element={<Explorer />} />
                <Route path="/anon/create" element={<AnonCreator />} />
                <Route path="/anon/collections/:hash" element={<AnonExplorer />} />
                <Route path="/anon" element={<AnonExplorer />} />
                <Route path="/p2p" element={<P2PDashboard />} />
                <Route path="/settings" element={<Settings dataConsent={dataConsent} setDataConsent={setDataConsent} />} />
              </Routes>
            </div>
            <LLMAssistant />
          </div>
        </BrowserRouter>
      </PageContext.Provider>
    </AppContext.Provider>
  );
}
