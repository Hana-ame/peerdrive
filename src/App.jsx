import React, { useState, createContext, useEffect } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Plaza from './pages/Plaza';
import Explorer from './pages/Explorer';
import Navbar from './components/Navbar';
import { getMe } from './api';

export const AppContext = createContext();

export default function App() {
  const [username, setUsername] = useState('alice');
  const [user, setUser] = useState(null);

  useEffect(() => {
    const initAuth = async () => {
      try {
        const me = await getMe();
        setUser(me);
        setUsername(me.username);
      } catch (e) {}
    };
    initAuth();
  }, []);

  return (
    <AppContext.Provider value={{ username, setUsername, user, setUser }}>
      <BrowserRouter>
        <div className="flex flex-col h-screen bg-gray-900 text-white font-sans">
          <Navbar />
          <main className="flex-1 overflow-hidden">
            <Routes>
              <Route path="/" element={<Plaza />} />
              <Route path="/:username/:collName" element={<Explorer />} />
            </Routes>
          </main>
        </div>
      </BrowserRouter>
    </AppContext.Provider>
  );
}
