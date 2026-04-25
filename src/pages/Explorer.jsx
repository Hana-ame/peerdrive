import React, { useContext, useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { AppContext } from '../App';
import { getCollection, uploadFile, addEntry, deleteEntry, commitVersion, downloadFileByPath, mergeCollection } from '../api';
import VersionLog from '../components/VersionLog';

export default function Explorer() {
  const { username: contextUser } = useContext(AppContext);
  const { username: paramUser, collName } = useParams();
  const username = paramUser || contextUser;

  const navigate = useNavigate();
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(true);
  const [refreshTrigger, setRefreshTrigger] = useState(0);

  const [commitMsg, setCommitMsg] = useState('');
  const [showMergeModal, setShowMergeModal] = useState(false);
  const [mergeSrc, setMergeSrc] = useState({ user: '', coll: '', strategy: 'ours' });

  useEffect(() => { loadEntries(); }, [username, collName, refreshTrigger]);

  const loadEntries = async () => {
    setLoading(true);
    try {
      const res = await getCollection(username, collName);
      setEntries(res.entries || []);
    } catch (e) { console.error(e); setEntries([]); }
    setLoading(false);
  };

  const handleUpload = async (e) => {
    const file = e.target.files[0]; if (!file) return;
    try {
      const uploadRes = await uploadFile(file);
      await addEntry(username, collName, file.name, uploadRes.hash);
      loadEntries();
    } catch (err) { alert(`上传失败: ${err.message}`); }
    e.target.value = '';
  };

  const handleCommit = async () => {
    if (!commitMsg) return alert("请输入提交信息");
    try {
      await commitVersion(username, collName, commitMsg);
      setCommitMsg('');
      setRefreshTrigger(prev => prev + 1);
    } catch (e) { alert(`Commit 失败: ${e.message}`); }
  };

  const handleDelete = async (path) => {
    if(!window.confirm(`移除 ${path}？`)) return;
    try {
      await deleteEntry(username, collName, path);
      loadEntries();
    } catch(e) { alert(`移除失败: ${e.message}`); }
  };

  const handleMerge = async () => {
    try {
      const res = await mergeCollection(username, collName, mergeSrc.user, mergeSrc.coll, mergeSrc.strategy);
      alert(`合并成功! 冲突: ${res.conflicts_found}, 总条目: ${res.total_entries}`);
      setShowMergeModal(false);
      loadEntries();
    } catch(e) {
      if (e.status === 409) alert(`存在冲突，请处理:\n${e.data.conflicts.map(c => c.path).join('\n')}`);
      else alert(`合并失败: ${e.message}`);
    }
  };

  return (
    <div className="flex flex-1 overflow-hidden relative">
      <div className="flex-1 flex flex-col bg-gray-900">
        <div className="h-16 bg-gray-800 border-b border-gray-700 flex items-center px-6 justify-between shrink-0">
          <div className="flex items-center space-x-4">
            <button onClick={() => navigate('/')} className="text-gray-400 hover:text-white">
              ← 返回广场
            </button>
            <div className="h-6 w-px bg-gray-700"></div>
            <div className="flex bg-gray-900 rounded overflow-hidden border border-gray-600">
              <input
                type="text" placeholder="Commit message..."
                value={commitMsg} onChange={(e) => setCommitMsg(e.target.value)}
                className="bg-transparent px-3 py-1.5 text-sm w-64 focus:outline-none"
              />
              <button onClick={handleCommit} className="bg-green-600 hover:bg-green-700 px-4 text-sm font-medium">
                Commit
              </button>
            </div>
          </div>

          <div className="flex items-center space-x-3">
            <label className="bg-blue-600 hover:bg-blue-700 px-4 py-1.5 rounded text-sm cursor-pointer flex items-center">
              上传文件 <input type="file" className="hidden" onChange={handleUpload} />
            </label>
            <button onClick={() => setShowMergeModal(true)} className="bg-gray-700 hover:bg-gray-600 px-4 py-1.5 rounded text-sm">
              Merge
            </button>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-6">
          <table className="w-full text-left">
            <thead className="text-gray-400 text-xs border-b border-gray-700">
              <tr>
                <th className="pb-3 font-medium w-12">类型</th>
                <th className="pb-3 font-medium">文件路径</th>
                <th className="pb-3 font-medium w-40">Hash</th>
                <th className="pb-3 font-medium w-28 text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-800">
              {loading ? <tr><td colSpan="4" className="py-4 text-gray-500">加载中...</td></tr> :
                entries.length === 0 ? <tr><td colSpan="4" className="py-10 text-center text-gray-500">空目录，请上传文件</td></tr> :
                entries.map(entry => (
                  <tr key={entry.id || entry.path} className="group hover:bg-gray-800/50">
                    <td className="py-3 text-gray-400">📄</td>
                    <td className="py-3 font-mono text-sm text-blue-300">{entry.path}</td>
                    <td className="py-3 text-xs text-gray-500 font-mono truncate">{entry.file_hash?.substring(0,16)}...</td>
                    <td className="py-3 text-right space-x-3 opacity-0 group-hover:opacity-100 transition-opacity">
                      <a href={downloadFileByPath(username, collName, entry.path)} target="_blank" rel="noreferrer" className="text-blue-400 hover:underline text-xs">下载</a>
                      <button onClick={() => handleDelete(entry.path)} className="text-red-400 hover:underline text-xs">移除</button>
                    </td>
                  </tr>
                ))
              }
            </tbody>
          </table>
        </div>
      </div>

      <div className="w-80 bg-gray-800 border-l border-gray-700 overflow-y-auto shrink-0">
        <VersionLog username={username} collName={collName} triggerRefresh={refreshTrigger} />
      </div>

      {showMergeModal && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-96 border border-gray-600 shadow-2xl">
            <h3 className="text-lg font-bold mb-4">Merge 合并</h3>
            <p className="text-sm text-gray-400 mb-4">将远端合集的条目合并到当前 <span className="text-white">{collName}</span></p>
            <input type="text" placeholder="源用户名" value={mergeSrc.user} onChange={(e)=>setMergeSrc({...mergeSrc, user: e.target.value})} className="w-full bg-gray-700 p-2 rounded mb-3 text-sm" />
            <input type="text" placeholder="源合集名" value={mergeSrc.coll} onChange={(e)=>setMergeSrc({...mergeSrc, coll: e.target.value})} className="w-full bg-gray-700 p-2 rounded mb-3 text-sm" />
            <select value={mergeSrc.strategy} onChange={(e)=>setMergeSrc({...mergeSrc, strategy: e.target.value})} className="w-full bg-gray-700 p-2 rounded mb-6 text-sm">
              <option value="ours">冲突保留本地</option>
              <option value="theirs">冲突采用远端</option>
              <option value="manual">冲突时报错</option>
            </select>
            <div className="flex justify-end space-x-3">
              <button onClick={() => setShowMergeModal(false)} className="px-4 py-2 bg-gray-600 rounded text-sm">取消</button>
              <button onClick={handleMerge} className="px-4 py-2 bg-teal-600 hover:bg-teal-700 rounded text-sm">执行合并</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
