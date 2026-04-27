// 版本历史面板：展示合集提交历史，支持回滚到指定版本
import React, { useState, useEffect } from 'react';
import { getVersionLog, rollbackVersion } from '../api';

export default function VersionLog({ username, collName, triggerRefresh }) {
  const [versions, setVersions] = useState([]);

  useEffect(() => { if (username && collName) loadLog(); }, [username, collName, triggerRefresh]);

  const loadLog = async () => {
    try {
      const res = await getVersionLog(username, collName);
      setVersions(res.data || []);
    }
    catch (e) { console.error(e); }
  };

  const handleRollback = async (vid) => {
    if(!window.confirm(`回滚到 v${vid}？这将覆盖当前未提交的工作区。`)) return;
    try {
      await rollbackVersion(username, collName, vid);
      alert('回滚成功！'); window.location.reload();
    } catch(e) { alert(`回滚失败: ${e.message}`); }
  };

  return (
    <div className="p-4">
      <h3 className="text-lg font-bold mb-4 text-gray-300 flex items-center">
        版本历史
      </h3>
      <div className="space-y-3">
        {versions.length === 0 ? <p className="text-gray-500 text-sm">暂无版本</p> : (
          versions.map((v, idx) => (
            <div key={v.id} className="bg-gray-700/50 p-3 rounded border border-gray-600 relative group hover:border-blue-500 transition-colors">
              <div className="flex justify-between items-start mb-1">
                <span className="text-blue-400 text-xs">v{v.version_number}</span>
                <span className="text-gray-500 text-xs">{v.created_at ? new Date(v.created_at).toLocaleDateString() : ''}</span>
              </div>
              <p className="text-sm text-white mb-2 truncate">{v.commit_message || v.message || 'No message'}</p>
              {idx > 0 && (
                <button
                  onClick={() => handleRollback(v.id)}
                  className="text-xs text-yellow-400 hover:text-yellow-300 opacity-0 group-hover:opacity-100 transition-opacity mt-1 block"
                >
                  回滚到此版本
                </button>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
