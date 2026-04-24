import { useState, useEffect } from 'react';
import { getCollection, uploadFile, addEntry, deleteEntry, commitVersion, downloadFileByPath } from '../api';
import VersionLog from './VersionLog';

export default function Dashboard({ config }) {
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(false);
  const [commitMsg, setCommitMsg] = useState('');
  const [refreshTrigger, setRefreshTrigger] = useState(0);

  const { username, currentColl } = config;

  useEffect(() => {
    if (username && currentColl) loadEntries();
  }, [username, currentColl, refreshTrigger]);

  const loadEntries = async () => {
    setLoading(true);
    try {
      const res = await getCollection(username, currentColl);
      setEntries(res.entries || []);
    } catch (e) {
      console.error(e);
      setEntries([]);
    }
    setLoading(false);
  };

  const handleUploadAndAdd = async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    try {
      const uploadRes = await uploadFile(file);
      const hash = uploadRes.hash;
      await addEntry(username, currentColl, file.name, hash);
      loadEntries();
      alert('上传并添加成功！请记得 Commit 保存版本。');
    } catch (err) {
      alert(`操作失败: ${err.message}`);
    }
    e.target.value = '';
  };

  const handleDeleteEntry = async (path) => {
    if (!window.confirm(`确定要从工作区移除 ${path} 吗？`)) return;
    try {
      await deleteEntry(username, currentColl, path);
      loadEntries();
    } catch (e) {
      alert(`删除失败: ${e.message}`);
    }
  };

  const handleCommit = async () => {
    if (!commitMsg) return alert("请输入提交信息");
    try {
      await commitVersion(username, currentColl, commitMsg);
      setCommitMsg('');
      alert('Commit 成功！');
      setRefreshTrigger(prev => prev + 1);
    } catch (e) {
      alert(`Commit 失败: ${e.message}`);
    }
  };

  return (
    <div className="flex-1 flex overflow-hidden">
      <div className="flex-1 flex flex-col p-6 border-r border-gray-700">
        <div className="flex justify-between items-center mb-6">
          <div>
            <h2 className="text-3xl font-bold">{username}/{currentColl}</h2>
            <p className="text-gray-400 text-sm mt-1">工作区 (待提交变更)</p>
          </div>
          <div className="flex space-x-2">
            <div className="flex bg-gray-800 rounded overflow-hidden border border-gray-600">
              <input
                type="text"
                value={commitMsg}
                onChange={(e) => setCommitMsg(e.target.value)}
                placeholder="Commit message..."
                className="bg-transparent px-3 py-2 text-sm w-64 focus:outline-none"
              />
              <button onClick={handleCommit}
                className="bg-green-600 hover:bg-green-700 px-4 py-2 text-sm font-bold">
                Commit
              </button>
            </div>
            <label className="bg-purple-600 hover:bg-purple-700 px-4 py-2 rounded text-sm font-bold cursor-pointer flex items-center">
              + 上传文件
              <input type="file" className="hidden" onChange={handleUploadAndAdd} />
            </label>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto bg-gray-800 rounded-lg">
          {loading
            ? <p className="p-4 text-gray-400">加载中...</p>
            : (
              <table className="w-full text-left">
                <thead className="bg-gray-700 text-gray-400 text-sm">
                  <tr>
                    <th className="p-3 w-16">类型</th>
                    <th className="p-3">路径</th>
                    <th className="p-3 w-32">SHA256 Hash</th>
                    <th className="p-3 w-24 text-right">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-700">
                  {entries.length === 0
                    ? <tr><td colSpan="4" className="p-4 text-center text-gray-500">暂无文件，请上传或 Fork</td></tr>
                    : entries.map((entry) => (
                        <tr key={entry.id || entry.path} className="hover:bg-gray-750">
                          <td className="p-3"><span className="text-green-400 text-lg">📄</span></td>
                          <td className="p-3 font-mono text-sm text-blue-300">{entry.path}</td>
                          <td className="p-3 text-xs text-gray-500 font-mono truncate w-32">
                            {entry.file_hash?.substring(0, 16)}...
                          </td>
                          <td className="p-3 text-right space-x-2">
                            <a href={downloadFileByPath(username, currentColl, entry.path)}
                              target="_blank" rel="noreferrer"
                              className="text-blue-400 hover:underline text-xs">下载</a>
                            <button onClick={() => handleDeleteEntry(entry.path)}
                              className="text-red-400 hover:underline text-xs">移除</button>
                          </td>
                        </tr>
                      ))
                  }
                </tbody>
              </table>
            )
          }
        </div>
      </div>

      <div className="w-80 bg-gray-800 p-4 overflow-y-auto">
        <VersionLog username={username} collName={currentColl} triggerRefresh={refreshTrigger} />
      </div>
    </div>
  );
}
