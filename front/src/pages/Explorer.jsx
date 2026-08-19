// 用户合集浏览器：查看/上传/提交/合并/同步指定用户合集的条目和版本历史
import React, { useContext, useState, useEffect, useMemo } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { AppContext, PageContext } from '../App';
import * as api from '../api';
import VersionLog from '../components/VersionLog';

export default function Explorer() {
  const { username: contextUser } = useContext(AppContext);
  const { setPageContext } = useContext(PageContext);
  const { username: paramUser, collName } = useParams();
  const username = paramUser || contextUser;

  const navigate = useNavigate();
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(true);
  const [refreshTrigger, setRefreshTrigger] = useState(0);
  const [navPath, setNavPath] = useState('');

  const [commitMsg, setCommitMsg] = useState('');
  const [showMergeModal, setShowMergeModal] = useState(false);
  const [mergeSrc, setMergeSrc] = useState({ user: '', coll: '', hash: '', strategy: 'ours' });
  const [mergeCollections, setMergeCollections] = useState([]);
  const [mergeCustom, setMergeCustom] = useState(false);
  const [mergeConflicts, setMergeConflicts] = useState(null);

  const [showSyncModal, setShowSyncModal] = useState(false);
  const [syncConfig, setSyncConfig] = useState({ path: '', include: '', exclude: '' });
  const [syncStatus, setSyncStatus] = useState(null);

  useEffect(() => { loadEntries(); }, [username, collName, refreshTrigger]);

  useEffect(() => {
    setPageContext({ type: 'explorer', username, collectionName: collName, entryCount: entries.length, entries: entries.map(e => ({ path: e.path, hash: (e.file_hash || '').substring(0, 16) })) });
  }, [entries, username, collName]);

  const loadEntries = async () => {
    setLoading(true);
    try {
      const col = await api.getUserCollection(username, collName);
      setEntries(col.entries || []);
    } catch (e) { console.error(e); setEntries([]); }
    setLoading(false);
  };

  // folder-based navigation
  const navIn = (dir) => setNavPath(prev => prev ? `${prev}/${dir}` : dir);
  const navBack = () => { const p = navPath.split('/'); p.pop(); setNavPath(p.join('/')); };

  const currentItems = useMemo(() => {
    const dirs = new Set();
    const files = [];
    const prefix = navPath ? navPath + '/' : '';
    for (const e of entries) {
      const p = e.path || '';
      if (p.startsWith(prefix)) {
        const rest = p.slice(prefix.length);
        const slash = rest.indexOf('/');
        if (slash === -1) files.push(e);
        else dirs.add(rest.slice(0, slash));
      }
    }
    return { dirs: Array.from(dirs).sort(), files };
  }, [entries, navPath]);

  const handleUpload = async (e) => {
    const file = e.target.files[0]; if (!file) return;
    try {
      const uploadRes = await api.uploadFile(file);
      await api.addEntry(username, collName, file.name, uploadRes.hash);
      loadEntries();
    } catch (err) { alert(`上传失败: ${err.message}`); }
    e.target.value = '';
  };

  const handleCommit = async () => {
    if (!commitMsg) return alert("请输入版本说明");
    try {
      await api.commitVersion(username, collName, commitMsg);
      setCommitMsg('');
      setRefreshTrigger(prev => prev + 1);
    } catch (e) { alert(`保存失败: ${e.message}`); }
  };

  const handleDelete = async (path) => {
    if(!window.confirm(`移除 ${path}？`)) return;
    try {
      await api.deleteEntry(username, collName, path);
      loadEntries();
    } catch(e) { alert(`移除失败: ${e.message}`); }
  };

  const loadMergeSources = async () => {
    setMergeCustom(false);
    setMergeConflicts(null);
    try {
      const [anon, pub] = await Promise.all([
        api.listAnonCollections().catch(() => []),
        api.listPublicCollections().catch(() => ({ collections: [] })),
      ]);
      const merged = [
        ...(Array.isArray(anon) ? anon.map(c => ({ label: `匿名: ${c.friendly_name || c.name_preview || c.hash?.substring(0,12)}`, user: '', coll: '', hash: c.hash })) : []),
        ...((pub.collections || pub.data || []).map(c => ({ label: `${c.username}/${c.collection_name}`, user: c.username, coll: c.collection_name, hash: '' }))),
        { label: '自定义...', user: '', coll: '', hash: '' },
      ];
      setMergeCollections(merged);
      setMergeSrc(prev => ({ ...prev, user: '', coll: '', hash: '' }));
    } catch (e) { console.error(e); }
  };

  // 匿名合集合并（客户端实现）：后端 /actions/merge 只接受用户合集对（source_username/
  // source_coll_name），没有匿名源端点（发现背景：合并弹窗把匿名合集也列入源下拉，选中后
  // onChange 按 "user/coll" 匹配永远落空 → 静默失败，什么都合并不了）。
  // 语义对齐后端：ours=冲突保留本地，theirs=冲突采用源，manual=先列出冲突 409 让用户选。
  const mergeAnonIntoLocal = async (hash, strategy) => {
    const coll = await api.getAnonCollection(hash);
    const anonEntries = coll.entries || [];
    const local = await api.getUserCollection(username, collName);
    const localMap = {};
    for (const e of local.entries || []) {
      localMap[e.path] = e.file_hash || e.providers?.[0]?.value || '';
    }
    const conflicts = [];
    let added = 0;
    for (const e of anonEntries) {
      const p = e.path;
      // 只取 sha256 provider 作为可添加的内容 hash；URL-only 条目无法写入
      // 用户集合的 addCollectionEntry 端点（只接受 sha256），必须跳过。
      // 坑：这里不能写 providers[0].value——若第一个 provider 是 url，会把
      // URL 字符串当成 hash 提交给后端，后端校验失败且合并中途停止。
      const h = e.hash || e.providers?.find(p => p.type === 'sha256')?.value || '';
      if (!h) continue;
      if (localMap[p] === h) continue;
      if (localMap[p] && localMap[p] !== h) {
        if (strategy === 'manual') { conflicts.push({ path: p, local_hash: localMap[p], source_hash: h }); continue; }
        if (strategy === 'ours') continue;
      }
      await api.addCollectionEntry(username, collName, p, h);
      added++;
    }
    if (strategy === 'manual' && conflicts.length) {
      const err = new Error('合并冲突，请选择解决策略');
      err.status = 409;
      err.data = { conflicts };
      throw err;
    }
    return { total_entries: added };
  };

  const handleMerge = async (strategy = mergeSrc.strategy) => {
    try {
      const res = mergeSrc.hash
        ? await mergeAnonIntoLocal(mergeSrc.hash, strategy)
        : await api.mergeCollection(username, mergeSrc.user, collName, mergeSrc.coll, strategy);
      alert(`合并成功! 总条目: ${res.total_entries || 0}`);
      setShowMergeModal(false);
      setMergeConflicts(null);
      loadEntries();
    } catch (e) {
      // A4: manual 策略后端返回 409 + 冲突清单，旧实现只 alert 错误消息，
      // 冲突无处展示、无法转换策略重试 → 在弹窗内展示冲突并支持 ours/theirs 重试
      if (e.status === 409 && e.data?.conflicts?.length) {
        setMergeConflicts(e.data.conflicts);
        return;
      }
      alert(`合并失败: ${e.message}`);
    }
  };

  const handleSaveLocal = async () => {
    try {
      const col = await api.getUserCollection(username, collName);
      if (!col.current_hash) return alert("此合集尚未保存任何版本，无法保存快照到本地");
      await api.saveLocal({
        collection_hash: col.current_hash,
        local_path: syncConfig.path,
        include: syncConfig.include.split(',').filter(Boolean).map(s => s.trim()),
        exclude: syncConfig.exclude.split(',').filter(Boolean).map(s => s.trim()),
      });
      alert("保存请求已发送！");
      await refreshSyncStatus(col.current_hash);
    } catch (e) { alert(`保存失败: ${e.message}`); }
  };

  const refreshSyncStatus = async (hash) => {
    try {
      const status = await api.getLocalStatus(hash);
      setSyncStatus(status);
    } catch (e) { console.error("Sync status error:", e); }
  };

  return (
    <div className="flex flex-1 overflow-hidden relative h-full">
      <div className="flex-1 flex flex-col bg-gray-900">
        <div className="h-16 bg-gray-800 border-b border-gray-700 flex items-center px-4 md:px-6 justify-between shrink-0 gap-2">
          <div className="flex items-center space-x-2 md:space-x-4 min-w-0">
            <button onClick={() => navigate('/')} className="text-gray-400 hover:text-white shrink-0 text-sm md:text-base">
              ←
            </button>
            <div className="h-6 w-px bg-gray-700 shrink-0 hidden md:block"></div>
            <span className="hidden md:block text-gray-400 hover:text-white shrink-0 text-sm" onClick={() => navigate('/')}>返回广场</span>
            <div className="h-6 w-px bg-gray-700 shrink-0"></div>
            <h2 className="text-sm md:text-base font-bold text-gray-200 truncate">
              {username}/{collName}
            </h2>
          </div>

          <div className="flex items-center gap-1.5 md:gap-3 shrink-0">
            <button onClick={() => setShowSyncModal(true)} className="bg-indigo-600 hover:bg-indigo-500 px-2.5 md:px-4 py-1.5 rounded text-xs md:text-sm whitespace-nowrap">
              同步到本地
            </button>
            <label className="bg-blue-600 hover:bg-blue-700 px-2.5 md:px-4 py-1.5 rounded text-xs md:text-sm cursor-pointer flex items-center whitespace-nowrap">
              上传 <input type="file" className="hidden" onChange={handleUpload} />
            </label>
            <button onClick={() => { setShowMergeModal(true); loadMergeSources(); }} className="bg-gray-700 hover:bg-gray-600 px-2.5 md:px-4 py-1.5 rounded text-xs md:text-sm whitespace-nowrap">
              合并
            </button>
          </div>
        </div>

        {/* commit bar */}
        <div className="h-12 bg-gray-850 border-b border-gray-800 flex items-center px-6 gap-3 shrink-0">
          <input type="text" placeholder="版本说明（可选）..."
            value={commitMsg} onChange={(e) => setCommitMsg(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') handleCommit(); }}
            className="bg-gray-800 border border-gray-700 px-3 py-1.5 rounded text-sm flex-1 max-w-md focus:outline-none focus:border-blue-500" />
          <button onClick={handleCommit} className="bg-blue-600 hover:bg-blue-700 px-4 py-1.5 rounded text-sm font-medium" title="保存当前版本，之后可恢复到此状态">
            提交版本
          </button>
        </div>

        {/* file entries with folder navigation */}
        <div className="flex-1 overflow-y-auto">
          {/* breadcrumb */}
          <div className="flex items-center gap-2 px-6 py-2 border-b border-gray-800 text-xs">
            <button onClick={() => setNavPath('')} className={`hover:text-white ${!navPath ? 'text-white' : 'text-gray-500'}`}>
              📂 /
            </button>
            {navPath.split('/').map((p, i) => (
              <span key={i} className="flex items-center gap-1">
                <span className="text-gray-600">›</span>
                <button
                  onClick={() => { const parts = navPath.split('/'); setNavPath(parts.slice(0, i + 1).join('/')); }}
                  className={`hover:text-white ${i === navPath.split('/').length - 1 ? 'text-white' : 'text-gray-400'}`}>
                  {p}
                </button>
              </span>
            ))}
            <span className="ml-auto text-gray-600">{currentItems.dirs.length + currentItems.files.length} 项</span>
          </div>

          {loading ? <div className="py-10 text-center text-gray-500">加载中...</div> :
           entries.length === 0 ? <div className="py-10 text-center text-gray-500">空目录，请上传文件</div> :
           <>
            {navPath && (
              <div onClick={navBack} className="flex items-center gap-3 px-6 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                <span className="text-lg">📁</span>
                <span className="text-gray-400">..</span>
              </div>
            )}
            {currentItems.dirs.map(dir => (
              <div key={dir} onClick={() => navIn(dir)}
                className="flex items-center gap-3 px-6 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm group">
                <span className="text-xl">📁</span>
                <span className="text-yellow-400 font-mono truncate flex-1 text-sm">{dir}</span>
                <span className="text-gray-600 text-xs opacity-0 group-hover:opacity-100">进入</span>
              </div>
            ))}
            {currentItems.files.map(entry => (
              <div key={entry.id || entry.path} className="flex items-center px-6 py-3 border-b border-gray-800/50 hover:bg-gray-800/30 transition-colors group">
                <span className="mr-3 text-xl">📄</span>
                <span className="font-mono text-sm text-blue-300 truncate flex-1">{(entry.path || '').split('/').pop() || 'file'}</span>
                <span className="text-xs text-gray-500 font-mono mr-4 truncate max-w-[120px]">{(entry.file_hash || '').substring(0, 12)}...</span>
                <div className="flex space-x-3 opacity-0 group-hover:opacity-100 transition-opacity">
                  <a href="#" onClick={e => {
                    e.preventDefault();
                    // 迁移后集合文件下载走 WS（旧 downloadFileByPath HTTP URL 是 legacy）
                    api.downloadUserFile(username, collName, entry.path).then(buf => {
                      const blob = new Blob([buf]);
                      const u = URL.createObjectURL(blob);
                      const a = document.createElement('a');
                      a.href = u;
                      a.download = (entry.path || '').split('/').pop() || 'file';
                      document.body.appendChild(a);
                      a.click();
                      document.body.removeChild(a);
                      setTimeout(() => URL.revokeObjectURL(u), 5000);
                    }).catch(err => alert('下载失败: ' + err.message));
                  }} className="text-blue-400 hover:underline text-xs">下载</a>
                  <button onClick={() => handleDelete(entry.path)} className="text-red-400 hover:underline text-xs">移除</button>
                </div>
              </div>
            ))}
           </>
          }
        </div>
      </div>

      <div className="w-80 bg-gray-800 border-l border-gray-700 overflow-y-auto shrink-0 hidden md:block">
        <VersionLog username={username} collName={collName} triggerRefresh={refreshTrigger} />
      </div>

      {showMergeModal && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-96 border border-gray-600 shadow-2xl">
            <h3 className="text-lg font-bold mb-4">合并</h3>
            <p className="text-sm text-gray-400 mb-4">将其他合集的条目合并到当前 <span className="text-white">{collName}</span></p>
            <div className="mb-3">
              <label className="block text-xs text-gray-500 mb-1">源合集</label>
              <select
                value={mergeSrc.user && mergeSrc.coll ? `${mergeSrc.user}/${mergeSrc.coll}` : (mergeSrc.hash || (mergeCustom ? '__custom__' : ''))}
                onChange={(e) => {
                  const val = e.target.value;
                  if (val === '__custom__') { setMergeCustom(true); setMergeSrc(prev => ({ ...prev, user: '', coll: '', hash: '' })); }
                  else {
                    setMergeCustom(false); setMergeConflicts(null);
                    // 坑：匿名合集选项 value 是 hash（user/coll 为空），旧实现
                    // 只按 "user/coll" 查找 → 永远匹配不到 → 合并源为空、静默失败
                    const item = mergeCollections.find(c => (c.user && c.coll) ? `${c.user}/${c.coll}` === val : c.hash === val);
                    if (item) setMergeSrc(prev => ({ ...prev, user: item.user || '', coll: item.coll || '', hash: item.hash || '' }));
                  }
                }}
                className="w-full bg-gray-700 p-2 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
              >
                <option value="">-- 选择合集 --</option>
                {mergeCollections.map((c, i) => {
                  const val = c.user && c.coll ? `${c.user}/${c.coll}` : (c.hash || `custom_${i}`);
                  if (c.label === '自定义...') return <option key="__custom__" value="__custom__">自定义...</option>;
                  return <option key={i} value={val}>{c.label}</option>;
                })}
              </select>
            </div>
            {mergeCustom && (
              <>
                <input type="text" placeholder="源用户名" value={mergeSrc.user} onChange={(e)=>setMergeSrc({...mergeSrc, user: e.target.value})} className="w-full bg-gray-700 p-2 rounded mb-2 text-sm" />
                <input type="text" placeholder="源合集名" value={mergeSrc.coll} onChange={(e)=>setMergeSrc({...mergeSrc, coll: e.target.value})} className="w-full bg-gray-700 p-2 rounded mb-3 text-sm" />
              </>
            )}
            <select value={mergeSrc.strategy} onChange={(e)=>setMergeSrc({...mergeSrc, strategy: e.target.value})} className="w-full bg-gray-700 p-2 rounded mb-6 text-sm">
              <option value="ours">冲突保留本地</option>
              <option value="theirs">冲突采用远端</option>
              <option value="manual">冲突时报错</option>
            </select>

            {mergeConflicts && (
              <div className="mb-4 bg-gray-900 p-3 rounded border border-yellow-700/60">
                <p className="text-xs font-bold text-yellow-400 mb-2">合并冲突 ({mergeConflicts.length})：请选择解决策略后重试</p>
                <div className="max-h-40 overflow-y-auto space-y-1 mb-3">
                  {mergeConflicts.map((cf, i) => (
                    <div key={i} className="text-[11px] font-mono text-gray-400 flex justify-between gap-2">
                      <span className="truncate">{cf.path}</span>
                      <span className="shrink-0">{(cf.local_hash || '').substring(0, 8)} → {(cf.source_hash || '').substring(0, 8)}</span>
                    </div>
                  ))}
                </div>
                <div className="flex gap-2">
                  <button onClick={() => handleMerge('ours')} className="flex-1 px-3 py-1.5 bg-gray-700 hover:bg-gray-600 rounded text-xs">保留本地重试</button>
                  <button onClick={() => handleMerge('theirs')} className="flex-1 px-3 py-1.5 bg-teal-700 hover:bg-teal-600 rounded text-xs">采用远端重试</button>
                </div>
              </div>
            )}
            <div className="flex justify-end space-x-3">
              <button onClick={() => setShowMergeModal(false)} className="px-4 py-2 bg-gray-600 rounded text-sm">取消</button>
              <button onClick={() => handleMerge()} disabled={!mergeSrc.user && !mergeSrc.hash}
                className="px-4 py-2 bg-teal-600 hover:bg-teal-700 rounded text-sm disabled:opacity-40 disabled:cursor-not-allowed">
                执行合并
              </button>
            </div>
          </div>
        </div>
      )}

      {showSyncModal && (
        <div className="absolute inset-0 bg-black/60 flex items-center justify-center z-50">
          <div className="bg-gray-800 p-6 rounded-xl w-[500px] border border-gray-600 shadow-2xl">
            <h3 className="text-lg font-bold mb-4">保存合集到本地</h3>
            <div className="space-y-4 mb-6">
              <div>
                <label className="block text-xs text-gray-400 mb-1">本地绝对路径</label>
                <input type="text" placeholder="/home/user/my_project" value={syncConfig.path} onChange={(e)=>setSyncConfig({...syncConfig, path: e.target.value})}
                  className="w-full bg-gray-700 p-2 rounded text-sm focus:ring-1 focus:ring-indigo-500 outline-none" />
              </div>
              <div>
                <label className="block text-xs text-gray-400 mb-1">包含文件 (逗号分隔, 选填)</label>
                <input type="text" placeholder="main.go, *.js" value={syncConfig.include} onChange={(e)=>setSyncConfig({...syncConfig, include: e.target.value})}
                  className="w-full bg-gray-700 p-2 rounded text-sm focus:ring-1 focus:ring-indigo-500 outline-none" />
              </div>
              <div>
                <label className="block text-xs text-gray-400 mb-1">排除文件 (逗号分隔, 选填)</label>
                <input type="text" placeholder="node_modules, .git" value={syncConfig.exclude} onChange={(e)=>setSyncConfig({...syncConfig, exclude: e.target.value})}
                  className="w-full bg-gray-700 p-2 rounded text-sm focus:ring-1 focus:ring-indigo-500 outline-none" />
              </div>
            </div>
            {syncStatus && (
              <div className="bg-gray-900 p-3 rounded mb-6 border border-gray-700">
                <div className="flex justify-between text-xs mb-2">
                  <span className="text-gray-400">同步状态: {syncStatus.saved_files}/{syncStatus.total_files} 已保存</span>
                  <button onClick={() => refreshSyncStatus(syncStatus.collection_hash)} className="text-indigo-400 hover:underline">刷新</button>
                </div>
              </div>
            )}
            <div className="flex justify-end space-x-3">
              <button onClick={() => setShowSyncModal(false)} className="px-4 py-2 bg-gray-600 rounded text-sm">取消</button>
              <button onClick={handleSaveLocal} className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 rounded text-sm">开始保存</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
