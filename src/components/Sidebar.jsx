import { useState, useEffect } from 'react';
import { getNodeInfo, getPeers, forkCollection, mergeCollection } from '../api';

export default function Sidebar({ config, setConfig }) {
  const [nodeInfo, setNodeInfo] = useState(null);
  const [peers, setPeers] = useState([]);

  const [forkUser, setForkUser] = useState('');
  const [forkColl, setForkColl] = useState('');

  const [mergeUser, setMergeUser] = useState('');
  const [mergeColl, setMergeColl] = useState('');
  const [mergeStrategy, setMergeStrategy] = useState('ours');

  useEffect(() => { loadP2PInfo(); }, []);

  const loadP2PInfo = async () => {
    try {
      const node = await getNodeInfo(); setNodeInfo(node);
      const p = await getPeers(); setPeers(p.peers || []);
    } catch (e) { console.error(e); }
  };

  const handleFork = async () => {
    try {
      const res = await forkCollection(config.username, config.currentColl, forkUser, forkColl);
      alert(`Fork 成功! 复制了 ${res.entries_count} 个条目`);
    } catch (e) { alert(`Fork 失败: ${e.message}`); }
  };

  const handleMerge = async () => {
    try {
      const res = await mergeCollection(config.username, config.currentColl, mergeUser, mergeColl, mergeStrategy);
      alert(`Merge 完成! 发现 ${res.conflicts_found} 个冲突，总计 ${res.total_entries} 条目`);
    } catch (e) {
      if (e.status === 409 && e.data?.conflicts) {
        alert(`合并冲突!\n以下路径有冲突，请手动解决:\n${e.data.conflicts.map(c => c.path).join('\n')}`);
      } else {
        alert(`Merge 失败: ${e.message}`);
      }
    }
  };

  return (
    <aside className="w-72 bg-gray-800 p-4 flex flex-col border-r border-gray-700 overflow-y-auto">
      <h1 className="text-2xl font-bold mb-6 text-blue-400">Peerdrive</h1>

      <div className="mb-6 space-y-2">
        <label className="block text-sm text-gray-400">当前节点用户</label>
        <input type="text" value={config.username}
          onChange={(e) => setConfig({...config, username: e.target.value})}
          className="w-full bg-gray-700 p-2 rounded" />
        <label className="block text-sm text-gray-400">当前合集</label>
        <input type="text" value={config.currentColl}
          onChange={(e) => setConfig({...config, currentColl: e.target.value})}
          className="w-full bg-gray-700 p-2 rounded" />
      </div>

      <div className="mb-6 border-t border-gray-700 pt-4">
        <h3 className="font-bold mb-2 text-gray-300">Fork 远端</h3>
        <input type="text" placeholder="远端用户名" value={forkUser}
          onChange={(e) => setForkUser(e.target.value)}
          className="w-full bg-gray-700 p-2 rounded mb-2 text-sm" />
        <input type="text" placeholder="远端合集名" value={forkColl}
          onChange={(e) => setForkColl(e.target.value)}
          className="w-full bg-gray-700 p-2 rounded mb-2 text-sm" />
        <button onClick={handleFork}
          className="w-full bg-indigo-600 hover:bg-indigo-700 p-2 rounded text-sm">
          Fork to Local
        </button>
      </div>

      <div className="mb-6 border-t border-gray-700 pt-4">
        <h3 className="font-bold mb-2 text-gray-300">Merge 合并</h3>
        <input type="text" placeholder="源用户名" value={mergeUser}
          onChange={(e) => setMergeUser(e.target.value)}
          className="w-full bg-gray-700 p-2 rounded mb-2 text-sm" />
        <input type="text" placeholder="源合集名" value={mergeColl}
          onChange={(e) => setMergeColl(e.target.value)}
          className="w-full bg-gray-700 p-2 rounded mb-2 text-sm" />
        <select value={mergeStrategy}
          onChange={(e) => setMergeStrategy(e.target.value)}
          className="w-full bg-gray-700 p-2 rounded mb-2 text-sm">
          <option value="ours">策略: Ours (冲突保留本地)</option>
          <option value="theirs">策略: Theirs (冲突采用远端)</option>
          <option value="manual">策略: Manual (冲突报错)</option>
        </select>
        <button onClick={handleMerge}
          className="w-full bg-teal-600 hover:bg-teal-700 p-2 rounded text-sm">
          Merge into Local
        </button>
      </div>

      <div className="mt-auto border-t border-gray-700 pt-4">
        <h3 className="font-bold mb-2 text-gray-300 flex justify-between">
          P2P 网络 <button onClick={loadP2PInfo} className="text-xs text-blue-400">刷新</button>
        </h3>
        {nodeInfo ? (
          <div className="text-xs text-gray-400 mb-2">
            <p>ID: <span className="text-green-400 font-mono">{nodeInfo.peer_id?.substring(0, 8)}...</span></p>
          </div>
        ) : <p className="text-xs text-red-400">节点未连接</p>}
        <div className="space-y-1 max-h-32 overflow-y-auto">
          {peers.length === 0
            ? <p className="text-xs text-gray-500">无已连接对等节点</p>
            : peers.map((p, i) =>
                <div key={i} className="text-xs bg-gray-700 p-1 rounded font-mono truncate">{p}</div>
              )
          }
        </div>
      </div>
    </aside>
  );
}
