import { useState } from 'react';
import Sidebar from './components/Sidebar';
import Dashboard from './components/Dashboard';

function App() {
  const [config, setConfig] = useState({
    username: 'alice',
    currentColl: 'my_project',
  });

  return (
    <div className="flex h-screen bg-gray-900 text-white font-sans">
      <Sidebar config={config} setConfig={setConfig} />
      <main className="flex-1 flex flex-col overflow-hidden">
        <Dashboard config={config} />
      </main>
    </div>
  );
}

export default App;
