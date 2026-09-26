// 模块③：合集 —— 浏览本机匿名合集 / 从网盘文件新建 / 查看条目并下载。
import React, { useState, useEffect, useCallback } from 'react';
import * as ws from '../ws';

const VIS_LABEL = {
  public: { icon: '🌐', label: '公开访问' },
  restricted: { icon: '👥', label: '仅限权限' },
  private: { icon: '🔒', label: '仅自己' },
};

function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? ts : d.toLocaleString('zh-CN', { hour12: false });
}

export default function Collections() {
  const [tab, setTab] = useState('list'); // list | create
  const [collections, setCollections] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  // 新建态
  const [files, setFiles] = useState([]);
  const [selected, setSelected] = useState([]);
  const [name, setName] = useState('');
  const [visibility, setVisibility] = useState('public');

  // 详情态
  const [detail, setDetail] = useState(null); // {hash, data}

  const loadList = useCallback(async () => {
    setErr('');
    try {
      const res = await ws.admin('GET', '/anon/collections');
      setCollections(Array.isArray(res) ? res : []);
    } catch (e) { setErr(e?.message || String(e)); setCollections([]); }
  }, []);
  useEffect(() => { loadList(); }, [loadList]);

  const openCreate = async () => {
    setErr('');
    setTab('create');
    setSelected([]);
    setName('');
    try {
      const res = await ws.admin('GET', '/files');
      setFiles(Array.isArray(res) ? res : []);
    } catch (e) { setErr(e?.message || String(e)); setFiles([]); }
  };

  const toggleFile = (hash) => {
    setSelected(prev => prev.includes(hash) ? prev.filter(h => h !== hash) : [...prev, hash]);
  };

  const create = async () => {
    if (!name.trim()) { setErr('请填合集名称'); return; }
    const chosen = files.filter(f => selected.includes(f.hash));
    if (chosen.length === 0) { setErr('请至少选择一个文件'); return; }
    const entries = chosen.map(f => ({
      path: f.filename,
      providers: [{ type: 'sha256', value: f.hash, mime_type: f.mime_type }],
    }));
    setBusy(true);
    try {
      await ws.admin('POST', '/anon/collections', {
        friendly_name: name.trim(),
        entries,
        tags: [],
        visibility,
      });
      setTab('list');
      await loadList();
    } catch (e) { setErr(e?.message || String(e)); }
    finally { setBusy(false); }
  };

  const openDetail = async (c) => {
    if (detail?.hash === (c.hash || c.current_hash)) { setDetail(null); return; }
    setErr('');
    setDetail({ hash: c.hash || c.current_hash, data: null });
    try {
      const d = await ws.admin('GET', `/anon/collections/${c.hash || c.current_hash}`);
      setDetail({ hash: c.hash || c.current_hash, data: d });
    } catch (e) {
      setErr(e?.message || String(e));
      setDetail(null);
    }
  };

  const downloadEntry = async (hash, path) => {
    try { await ws.downloadToFile(hash, path || 'download'); }
    catch (e) { setErr(e?.message || String(e)); }
  };

  const th = 'text-left text-xs uppercase tracking-wider text-gray-500 px-3 py-2 font-medium';

  return (
    <div className="p-8 overflow-y-auto h-full">
      <div className="max-w-5xl mx-auto">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h1 className="text-2xl font-bold">合集</h1>
            <p className="text-sm text-gray-500 mt-0.5">内容寻址版本化的文件合集（匿名 / 哈希寻址）</p>
          </div>
          <button onClick={openCreate} className="btn-brand">+ 新建合集</button>
        </div>

        {err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
        )}

        {tab === 'list' && (
          collections === null ? (
            <div className="text-center py-16 text-gray-500 text-sm">读取中...</div>
          ) : collections.length === 0 ? (
            <div className="text-center py-20 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
              <p>还没有任何合集</p>
              <p className="text-xs text-gray-600 mt-1">点「新建合集」从网盘文件创建。</p>
            </div>
          ) : (
            <div className="card-surface overflow-hidden">
              <table className="w-full text-sm">
                <thead className="bg-white/[0.03]">
                  <tr>
                    <th className={th}>名称</th>
                    <th className={th}>可见性</th>
                    <th className={th}>版本</th>
                    <th className={th}>条目</th>
                    <th className={th}>创建时间</th>
                  </tr>
                </thead>
                <tbody>
                  {collections.map((c, i) => {
                    const v = VIS_LABEL[c.visibility] || VIS_LABEL.public;
                    return (
                      <React.Fragment key={c.hash || i}>
                        <tr onClick={() => openDetail(c)}
                          className="border-t border-white/[0.04] hover:bg-white/[0.02] cursor-pointer">
                          <td className="px-3 py-2 text-gray-200">{c.friendly_name || '未命名'}</td>
                          <td className="px-3 py-2 text-gray-500">{v.icon} {v.label}</td>
                          <td className="px-3 py-2 text-gray-500">v{c.version}</td>
                          <td className="px-3 py-2 text-gray-500">{c.entry_count ?? c.entries?.length ?? '—'}</td>
                          <td className="px-3 py-2 text-gray-500">{fmtTime(c.created_at)}</td>
                        </tr>
                        {detail?.hash === (c.hash || c.current_hash) && (
                          <tr className="bg-white/[0.02]">
                            <td colSpan={5} className="px-4 py-3">
                              {detail.data === null ? (
                                <span className="text-xs text-gray-500">读取中...</span>
                              ) : (
                                <div>
                                  <p className="text-xs text-gray-500 mb-2">
                                    hash：<span className="font-mono text-gray-400 break-all">{detail.hash}</span>
                                  </p>
                                  {Array.isArray(detail.data.entries) && detail.data.entries.length > 0 ? (
                                    <ul className="space-y-1">
                                      {detail.data.entries.map((e, ei) => (
                                        <li key={ei} className="flex items-center gap-3 text-xs">
                                          <span className="flex-1 truncate text-gray-300">{e.path}</span>
                                          <span className="font-mono text-xs text-gray-600">{String(e.hash || '').slice(0, 12)}…</span>
                                          <button onClick={() => downloadEntry(e.hash, e.path)}
                                            className="px-2 py-0.5 text-xs bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 rounded">下载</button>
                                        </li>
                                      ))}
                                    </ul>
                                  ) : (
                                    <p className="text-xs text-gray-600">该合集没有条目。</p>
                                  )}
                                </div>
                              )}
                            </td>
                          </tr>
                        )}
                      </React.Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )
        )}

        {tab === 'create' && (
          <div className="card-surface p-5 space-y-4">
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">合集名称</label>
              <input value={name} onChange={e => setName(e.target.value)} placeholder="合集名称"
                className="input-base max-w-md" />
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">可见性</label>
              <div className="flex gap-1">
                {Object.entries(VIS_LABEL).map(([k, v]) => (
                  <button key={k} onClick={() => setVisibility(k)}
                    className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                      visibility === k ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
                    }`}>
                    {v.icon} {v.label}
                  </button>
                ))}
              </div>
            </div>
            <div>
              <label className="block text-xs text-gray-400 mb-1.5">选择网盘文件（{selected.length} 个已选）</label>
              {files.length === 0 ? (
                <p className="text-xs text-gray-600">网盘还没有文件，先去「我的网盘」上传。</p>
              ) : (
                <ul className="border border-white/[0.06] rounded-lg divide-y divide-white/[0.04] max-h-72 overflow-y-auto">
                  {files.map((f, i) => (
                    <li key={f.hash || i}
                      onClick={() => toggleFile(f.hash)}
                      className={`flex items-center gap-3 px-3 py-2 text-xs cursor-pointer hover:bg-white/[0.03] ${
                        selected.includes(f.hash) ? 'bg-brand-500/10' : ''
                      }`}>
                      <input type="checkbox" readOnly checked={selected.includes(f.hash)} className="accent-brand-500 pointer-events-none" />
                      <span className="flex-1 truncate text-gray-300">{f.filename}</span>
                      <span className="text-gray-500 shrink-0">{f.size} B</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div className="flex gap-2">
              <button onClick={create} disabled={busy || selected.length === 0}
                className="btn-brand">创建合集</button>
              <button onClick={() => setTab('list')} className="btn-ghost">取消</button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}