// 用户/群组选择器 — Steam 风格，显示昵称@id，支持按群组快速选择
import React, { useState, useEffect, useMemo } from 'react';
import * as api from '../api';

export default function UserGroupPicker({ selected, onChange, onClose }) {
  const [users, setUsers] = useState([]);
  const [groups, setGroups] = useState([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [error, setError] = useState('');

  useEffect(() => { loadData(); }, []);

  const loadData = async () => {
    setLoading(true);
    try {
      const [userRes, groupRes] = await Promise.all([
        api.listRegUsers().catch(() => ({ users: [] })),
        api.listRegGroups().catch(() => ({ groups: [] })),
      ]);
      setUsers(userRes.users || []);
      setGroups(groupRes.groups || []);
      setError('');
    } catch (e) {
      setError('无法加载用户列表: ' + e.message);
    }
    setLoading(false);
  };

  const filteredUsers = useMemo(() => {
    if (!search) return users;
    const q = search.toLowerCase();
    return users.filter(u =>
      (u.username || '').toLowerCase().includes(q) ||
      (u.nickname || '').toLowerCase().includes(q)
    );
  }, [users, search]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);

  const toggleUser = (username) => {
    const next = new Set(selectedSet);
    next.has(username) ? next.delete(username) : next.add(username);
    onChange([...next]);
  };

  const selectGroup = (groupName) => {
    // Find all members of this group and add them
    const next = new Set(selectedSet);
    // Group members are loaded on demand, so just add the group itself
    if (next.has('group:' + groupName)) {
      next.delete('group:' + groupName);
    } else {
      next.add('group:' + groupName);
    }
    onChange([...next]);
  };

  const selectedGroupSet = useMemo(() => {
    return new Set(selected.filter(s => s.startsWith('group:')));
  }, [selected]);

  if (loading) return <div className="p-3 text-gray-500 text-xs">加载用户列表...</div>;

  return (
    <div className="bg-gray-800 border border-gray-700 rounded-lg overflow-hidden" style={{ maxHeight: '360px' }}>
      {/* 搜索栏 */}
      <div className="p-2 border-b border-gray-700 shrink-0">
        <input
          autoFocus
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="搜索用户..."
          className="w-full bg-gray-900 text-xs px-2 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-500"
        />
      </div>

      {error && <div className="p-2 text-red-400 text-xs">{error}</div>}

      {/* 群组快速选择 */}
      {groups.length > 0 && !search && (
        <div className="p-2 border-b border-gray-700">
          <div className="text-[10px] text-gray-500 mb-1">群组快速选择</div>
          <div className="flex flex-wrap gap-1">
            {groups.map(g => (
              <button
                key={g.name}
                onClick={() => selectGroup(g.name)}
                className={`text-xs px-2 py-0.5 rounded-full ${
                  selectedGroupSet.has('group:' + g.name)
                    ? 'bg-blue-600 text-white'
                    : 'bg-gray-700 text-gray-300 hover:bg-gray-600'
                }`}
              >
                👥 {g.name}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* 用户列表 */}
      <div className="overflow-y-auto" style={{ maxHeight: '220px' }}>
        {filteredUsers.length === 0 ? (
          <div className="p-3 text-gray-600 text-xs text-center">
            {search ? '无匹配用户' : '暂无注册用户'}
          </div>
        ) : (
          filteredUsers.map(u => (
            <div
              key={u.username}
              onClick={() => toggleUser(u.username)}
              className={`flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-gray-700/50 text-sm ${
                selectedSet.has(u.username) ? 'bg-blue-900/30' : ''
              }`}
            >
              <input
                type="checkbox"
                checked={selectedSet.has(u.username)}
                onChange={() => toggleUser(u.username)}
                className="rounded shrink-0"
              />
              <span className="w-6 h-6 rounded-full bg-gray-600 flex items-center justify-center text-xs shrink-0">
                {/* nickname/username 都为空时 (u.nickname||u.username) 是空串，[0] 为 undefined，toUpperCase 会崩 */}
                {((u.nickname || u.username || '?')[0] || '?').toUpperCase()}
              </span>
              <span className="text-gray-300 truncate flex-1 text-xs">
                {u.nickname || u.username}
              </span>
              <span className="text-gray-500 text-[10px] shrink-0">@{u.username}</span>
            </div>
          ))
        )}
      </div>

      {/* 底部 */}
      <div className="flex items-center gap-2 p-2 border-t border-gray-700 shrink-0">
        <span className="text-[10px] text-gray-500">
          已选 {selected.filter(s => !s.startsWith('group:')).length} 用户{selected.filter(s => s.startsWith('group:')).length > 0 ? ` + ${selected.filter(s => s.startsWith('group:')).length} 群组` : ''}
        </span>
        <div className="flex-1" />
        <button onClick={onClose} className="text-xs bg-blue-600 hover:bg-blue-700 px-3 py-1 rounded">确定</button>
      </div>
    </div>
  );
}
