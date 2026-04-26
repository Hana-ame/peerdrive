import React, { useState, createContext } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Plaza from './pages/Plaza';
import Explorer from './pages/Explorer';
import FileManager from './pages/FileManager';
import AnonCreator from './pages/AnonCreator';
import AnonExplorer from './pages/AnonExplorer';
import Settings from './pages/Settings';
import Navbar from './components/Navbar';

export const AppContext = createContext();

export default function App() {
  const [username, setUsername] = useState(localStorage.getItem('peerdrive_username') || '');
  const [nodeInfo, setNodeInfo] = useState(null);

  const handleUsernameChange = (val) => {
    setUsername(val);
    localStorage.setItem('peerdrive_username', val);
  };

  return (
    <AppContext.Provider value={{ username, setUsername: handleUsernameChange, nodeInfo, setNodeInfo }}>
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
              <Route path="/settings" element={<Settings />} />
            </Routes>
          </div>
        </div>
      </BrowserRouter>
    </AppContext.Provider>
  );
}
