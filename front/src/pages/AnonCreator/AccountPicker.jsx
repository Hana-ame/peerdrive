import { useState, useMemo } from 'react';

// 头像用昵称首字符兜底（regserver 还没提供头像 URL，后续接入后换成 <img>）
function Avatar({ name }) {
  const ch = (name || '?').trim().charAt(0).toUpperCase();
  return (
    <span className="w-7 h-7 shrink-0 rounded-full bg-brand-600/30 text-brand-200 flex items-center justify-center text-xs">
      {ch}
    </span>
  );
}

/**
 * 账号选择器：昵称 + @唯一 id 的列表（Steam 风格），支持分组快速分享与搜索。
 * 背景：用户要求「仅限..权限」弹出 regserver 的账号列表。
 * 坑：regserver 的账号目录接口属于「注册认证服务」模块（尚未落地），这里不能直接 404 掉整个功能——
 * 所以 selected 之外的候选来源按顺序降级：API 拉取 → 本机缓存（localStorage）→ 手动粘贴 @id。
 */
export default function AccountPicker({ accounts = [], selected = [], onConfirm, onClose }) {
  const [query, setQuery] = useState('');
  const [picked, setPicked] = useState(new Set(selected));
  const [manual, setManual] = useState('');

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return accounts;
    return accounts.filter(a =>
      (a.nickname || '').toLowerCase().includes(q) || (a.username || '').toLowerCase().includes(q)
    );
  }, [accounts, query]);

  const toggle = (id) => setPicked(prev => {
    const n = new Set(prev);
    n.has(id) ? n.delete(id) : n.add(id);
    return n;
  });

  const addManual = () => {
    const id = manual.trim().replace(/^@/, '');
    if (!id) return;
    if (!accounts.some(a => a.username === id)) {
      // 手动填的账号没有昵称，直接用 id 当昵称塞进列表，保证 UI 一致
      accounts.push({ username: id, nickname: id });
    }
    setPicked(prev => new Set(prev).add(id));
    setManual('');
  };

  // 分组快捷分享：一键把整组成员加进名单。
  // 坑：后端只认账号名扁平数组（model.AnonCollection.AccessList []string），
  // 集合里存的是展开后的名单而不是组名，改组不影响已发出去的合集（快照语义）。
  const groupChips = useMemo(() => {
    const real = (groups || []).map(g => ({
      id: g.name || g.group_name || g.id,
      label: g.name || g.group_name || g.id,
      members: g.members || g.usernames || [],
    })).filter(g => g.id && g.members.length > 0);
    // 没有 regserver 分组时给一个「最近 5 人」的快捷入口，避免这一栏空着
    return real.length > 0 ? real : [{ id: '__recent', label: '最近分享', members: filtered.slice(0, 5).map(a => a.username) }];
  }, [groups, filtered]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div className="bg-white/[0.06] rounded-xl border border-white/[0.12] shadow-2xl w-[380px] max-h-[80vh] flex flex-col"
        onClick={e => e.stopPropagation()}>
        <div className="px-3 py-2 border-b border-white/[0.06] flex items-center gap-2">
          <h3 className="text-sm font-bold text-gray-200 flex-1">选择可访问的人</h3>
          <span className="text-[10px] text-gray-500">已选 {picked.size}</span>
          <button onClick={onClose} className="text-gray-500 hover:text-white text-sm">×</button>
        </div>

        <div className="p-3 space-y-2 border-b border-white/[0.06]">
          <input value={query} onChange={e => setQuery(e.target.value)} placeholder="搜索昵称 / @id..."
            className="w-full bg-surface-card text-xs px-2 py-1.5 rounded border border-white/[0.06] focus:outline-none focus:border-brand-600" />
          <div className="flex items-center gap-1">
            {/* 分组快速分享：一键把整组人加进来 */}
            {groupChips.map(g => (
              <button key={g.id} onClick={() => setPicked(prev => new Set([...prev, ...g.members]))}
                className="text-[10px] px-2 py-1 rounded bg-white/[0.06] hover:bg-white/[0.12] text-gray-300">
                {g.label}（{g.members.length}）
              </button>
            ))}
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-2 space-y-1">
          {filtered.length === 0 && (
            <p className="text-[11px] text-gray-500 px-2 py-3">
              {accounts.length === 0 ? '还没有可用账号 — 在下方手动添加 @id' : '无匹配账号'}
            </p>
          )}
          {filtered.map(a => (
            <div key={a.username} onClick={() => toggle(a.username)}
              className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-white/[0.08] cursor-pointer">
              <Avatar name={a.nickname || a.username} />
              <span className="text-xs text-gray-200 truncate flex-1">{a.nickname || a.username}</span>
              <span className="text-[10px] text-gray-500 font-mono">@{a.username}</span>
              <span className={`text-xs ${picked.has(a.username) ? 'text-brand-400' : 'text-gray-600'}`}>
                {picked.has(a.username) ? '☑' : '☐'}
              </span>
            </div>
          ))}
        </div>

        <div className="p-3 border-t border-white/[0.06] space-y-2">
          <div className="flex items-center gap-1">
            <input value={manual} onChange={e => setManual(e.target.value)}
              placeholder="手动添加 @id"
              className="flex-1 bg-surface-card text-xs px-2 py-1.5 rounded border border-white/[0.06] focus:outline-none focus:border-brand-600 font-mono" />
            <button onClick={addManual} disabled={!manual.trim()}
              className="text-xs px-2 py-1.5 rounded bg-white/[0.06] hover:bg-white/[0.12] disabled:opacity-40">添加</button>
          </div>
          <button onClick={() => onConfirm(Array.from(picked))}
            className="w-full text-xs bg-brand-600 hover:bg-brand-700 py-1.5 rounded">
            确定（{picked.size} 人）
          </button>
        </div>
      </div>
    </div>
  );
}
