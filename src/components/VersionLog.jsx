import { useState, useEffect } from 'react';
import { getVersionLog, rollbackVersion } from '../api';

export default function VersionLog({ username, collName, triggerRefresh }) {
  const [versions, setVersions] = useState([]);

  useEffect(() => {
    if (username && collName) loadLog();
  }, [username, collName, triggerRefresh]);

  const loadLog = async () => {
    try {
      const res = await getVersionLog(username, collName);
      setVersions(res.data || []);
    } catch (e) { console.error(e); }
  };

  const handleRollback = async (vid) => {
    if (!window.confirm(`确定要回滚到版本 #${vid} 吗？这将覆盖当前工作区。`)) return;
    try {
      await rollbackVersion(username, collName, vid);
      alert('回滚成功！');
      window.location.reload();
    } catch (e) {
      alert(`回滚失败: ${e.message}`);
    }
  };

  return (
    <div>
      <h3 className="text-lg font-bold mb-4 text-gray-300">版本历史</h3>
      <div className="space-y-3">
        {versions.length === 0
          ? <p className="text-gray-500 text-sm">暂无历史记录</p>
          : versions.map((v, idx) => (
              <div key={v.id} className="bg-gray-700 p-3 rounded border border-gray-600 relative group">
                <div className="flex justify-between items-start mb-1">
                  <span className="text-blue-400 font-bold text-xs">v{v.version_number}</span>
                  <span className="text-gray-500 text-xs">{new Date(v.created_at).toLocaleTimeString()}</span>
                </div>
                <p className="text-sm text-white mb-2">{v.commit_message || 'No message'}</p>

                {idx > 0 && (
                  <button
                    onClick={() => handleRollback(v.id)}
                    className="absolute top-2 right-2 text-xs text-gray-500 hover:text-yellow-400 opacity-0 group-hover:opacity-100 transition-opacity"
                  >
                    ↩ Rollback
                  </button>
                )}
              </div>
            ))
        }
      </div>
    </div>
  );
}
