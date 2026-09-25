import React, { useContext, useState, useEffect, useMemo, useRef } from 'react';
import * as api from '../../api';
import { useNavigate, useLocation } from 'react-router-dom';
import { PageContext } from '../../App';
import { loadSearchHistory } from './utils';
import { VISIBILITY } from '../../constants';
import LeftPanel from './LeftPanel';
import MiddlePanel from './MiddlePanel';
import RightPanel from './RightPanel';
import AccountPicker from './AccountPicker';

export default function AnonCreator() {
  const nav = useNavigate();
  const { setPageContext } = useContext(PageContext);
  const navState = useLocation().state || {};

  // ===== 数据 =====
  const [files, setFiles] = useState([]);
  const [fLoading, setFLoading] = useState(true);
  const [collections, setCollections] = useState([]);

  // ===== 左侧筛选 =====
  const [leftSourceTab, setLeftSourceTab] = useState('local');
  const [leftSortKey, setLeftSortKey] = useState('created_at');
  const [leftSortOrder, setLeftSortOrder] = useState('desc');
  const [leftTypeFilters, setLeftTypeFilters] = useState([]);
  const [search, setSearch] = useState('');
  // 已注册·按目录 的下钻路径（'' = 根）。切换 tab 时重置，避免带着深层路径回来找不到内容。
  const [regDirPath, setRegDirPath] = useState('');

  // ===== 合集管理 =====
  const [collSort, setCollSort] = useState('time');
  const [collSearch, setCollSearch] = useState('');
  const [collTagFilter, setCollTagFilter] = useState([]);
  const [enteredColl, setEnteredColl] = useState(null);
  const [enteredCollFiles, setEnteredCollFiles] = useState(null);
  const [collViewPath, setCollViewPath] = useState('');

  // ===== 编辑器 =====
  const [fname, setFname] = useState('');
  const [tags, setTags] = useState('');
  const [entries, setEntries] = useState([]);
  const [saving, setSaving] = useState(false);
  const [showNamePrompt, setShowNamePrompt] = useState(false);
  const [toastMsg, setToastMsg] = useState('');
  const [toastErr, setToastErr] = useState(false);

  // ===== 广播权限（三选项）=====
  // public 才能广播；restricted 必须真挑到人；private 依赖本节点 operator 账号。
  const [visibility, setVisibility] = useState(VISIBILITY.PUBLIC);
  const [accessList, setAccessList] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [groups, setGroups] = useState([]);
  const [operator, setOperator] = useState('');
  const [showAccountPicker, setShowAccountPicker] = useState(false);

  // ===== 选择模式 =====
  const [selectMode, setSelectMode] = useState(false);
  const [selectedColls, setSelectedColls] = useState(new Set());
  const [selectedFiles, setSelectedFiles] = useState(new Set());

  // ===== 搜索历史 =====
  const [searchHistory, setSearchHistory] = useState(loadSearchHistory());

  // ===== 系统浏览 =====
  const [sysPath, setSysPath] = useState('/');
  const [sysEntries, setSysEntries] = useState([]);
  const [sysLoading, setSysLoading] = useState(false);

  // ===== 预览选中 =====
  const [selectedFile, setSelectedFile] = useState(null);

  // ===== 副作用 =====
  useEffect(() => { loadFiles(); loadCollections(); }, []);
  useEffect(() => {
    if (leftSourceTab === 'local') {
      setSysLoading(true);
      api.browseDir(sysPath).then(res => {
        res.sort((a, b) => (a.is_dir === b.is_dir) ? a.name.localeCompare(b.name) : (a.is_dir ? -1 : 1));
        setSysEntries(res);
      }).catch(() => setSysEntries([])).finally(() => setSysLoading(false));
    }
  }, [leftSourceTab, sysPath]);
  useEffect(() => {
    if (navState.forkFrom) {
      const c = navState.forkFrom;
      // 坑：forkFrom 来自 Plaza 的 AnonCollectionSummary（/anon/collections），
      // summary 只有 hash/friendly_name/entry_count，没有 entries 字段 → 直接 setEntries(c.entries||[]) 永远是空合集。
      // 必须按哈希拉取完整合集；公开合集经 Plaza.handleFork 传 sourceHash=current_hash，
      // 匿名合集传 sourceHash=hash（再 review 2026-08 补 current_hash 支持）。
      const forkHash = navState.sourceHash || c.hash || c.current_hash;
      setFname((c.friendly_name || '') + ' (副本)');
      const loadFork = async () => {
        if (!forkHash) return;
        try {
          const full = await api.getAnonCollection(forkHash);
          setEntries(full?.entries || []);
        } catch (e) { console.error('fork load failed:', e); }
      };
      loadFork();
      nav('/create', { replace: true });
    } else if (navState.draftFrom) {
      setEntries(navState.draftFrom.entries || []);
      setFname(navState.draftFrom.friendlyName || '');
      nav('/create', { replace: true });
    } else if (navState.editFrom) {
      const c = navState.editFrom;
      setEntries(c.entries || []);
      setFname(c.friendly_name || '');
      nav('/create', { replace: true });
    }
  }, []);
  useEffect(() => {
    setPageContext({ type: 'anonCreator', fileCount: files.length, entryCount: entries.length, friendlyName: fname });
  }, [files, entries, fname]);

  // ===== 数据加载 =====
  const loadFiles = async () => {
    setFLoading(true);
    try { setFiles(await api.listFiles('time') || []); } catch { setFiles([]); }
    setFLoading(false);
  };
  const loadCollections = async () => {
    try { setCollections(await api.listAnonCollections() || []); } catch { setCollections([]); }
  };
  // operator 是本节点登录 regserver 的账号（后端 nodestate.GetOperator()），
  // 匿合集的 Owner 就取它 —— 没有 operator 时「仅自己」会生成谁都读不了的合集，
  // 所以必须在 UI 之前拿到它。挂在首屏一次性拉取，不放进保存路径串行等待。
  useEffect(() => {
    api.getAuthStatus()
      .then(res => setOperator(res?.operator || res?.username || ''))
      .catch(() => setOperator(''));
  }, []);

  // ===== 过滤 & 排序 =====
  // 「已注册」口径：后端 ListAllFiles（repository/file_repo.go:96）LEFT JOIN file_providers，
  // 本地注册过的文件才带 provider_path/provider_type，纯 URL/远端来源的条目这两个字段是空串。
  // 坑：旧实现按 f.providers?.length > 0 过滤——FileListItem 根本没有 providers 字段，
  // 结果恒为 false，整个「已注册」列表永远是空的。
  const isRegisteredFile = (f) => !!(f?.provider_path || f?.provider_type);

  // 时间比较必须转成数值：created_at 是 RFC3339 字符串，localeCompare 比较会把
  // 时区后缀（+08:00 / Z）混在一起，跨时区写入的记录排出来顺序是乱的。
  const parseTime = (s) => { const t = Date.parse(s || ''); return Number.isNaN(t) ? 0 : t; };

  const filteredFiles = useMemo(() => {
    let result = [...files];

    // 来源过滤
    if (leftSourceTab === 'registered' || leftSourceTab === 'registered_dir') {
      result = result.filter(isRegisteredFile);
    }

    // 文本搜索（本地电脑模式由 SystemBrowse 就地过滤 browse 结果，不走这里）
    if (search && leftSourceTab !== 'local' && leftSourceTab !== 'collections') {
      const q = search.toLowerCase();
      result = result.filter(f =>
        (f.filename || '').toLowerCase().includes(q) ||
        (f.provider_path || '').toLowerCase().includes(q)
      );
    }

    // 类型筛选（多选）
    if (leftTypeFilters.length > 0 && (leftSourceTab === 'all' || leftSourceTab === 'registered')) {
      result = result.filter(f => {
        const mime = f.mime_type || '';
        const ext = (f.filename || '').split('.').pop()?.toLowerCase() || '';
        if (leftTypeFilters.includes('document')) {
          if (mime === 'application/pdf' || ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx'].includes(ext)) return true;
        }
        return leftTypeFilters.some(t => mime.startsWith(t));
      });
    }

    // 排序
    result.sort((a, b) => {
      let cmp = 0;
      switch (leftSortKey) {
        // 坑：文件名必须 numeric 感知，否则 a2.png 会排到 a10.png 后面
        case 'name': cmp = String(a.filename || '').localeCompare(String(b.filename || ''), undefined, { numeric: true, sensitivity: 'base' }); break;
        case 'size': cmp = (Number(a.size) || 0) - (Number(b.size) || 0); break;
        default: cmp = parseTime(a.created_at) - parseTime(b.created_at); break;
      }
      // 次级键：主序相同时用文件名兜底，保证顺序稳定（否则并列项顺序随机跳动）
      if (cmp === 0) cmp = String(a.filename || '').localeCompare(String(b.filename || ''), undefined, { numeric: true });
      return leftSortOrder === 'asc' ? cmp : -cmp;
    });

    return result;
  }, [files, leftSourceTab, search, leftTypeFilters, leftSortKey, leftSortOrder]);

  const filteredCollections = useMemo(() => {
    return collections.filter(c => {
      if (collSearch && !(c.friendly_name || c.name_preview || '').toLowerCase().includes(collSearch.toLowerCase())) return false;
      if (collTagFilter.length > 0 && !collTagFilter.some(f => (c.tags || []).some(t => t.toLowerCase().includes(f.toLowerCase())))) return false;
      return true;
    }).sort((a, b) => {
      switch (collSort) {
        case 'name': return (a.friendly_name || a.name_preview || '').localeCompare(b.friendly_name || b.name_preview || '');
        case 'count': return (b.entry_count || 0) - (a.entry_count || 0);
        default: return (b.created_at || '').localeCompare(a.created_at || '');
      }
    });
  }, [collections, collSearch, collTagFilter, collSort]);

  const allCollTags = useMemo(() =>
    [...new Set(collections.flatMap(c => c.tags || []))].sort()
  , [collections]);

  // ===== 提示 =====
  const toastTimerRef = useRef(null);
  // 坑：旧实现每次 toast 都开新 setTimeout，前一个 toast 的定时器可能在后一个
  // toast 未到 3s 时清掉消息（快速连续操作时提示一闪而过）。
  const showToast = (msg, err = false) => {
    if (toastTimerRef.current) clearTimeout(toastTimerRef.current);
    setToastMsg(msg); setToastErr(err);
    toastTimerRef.current = setTimeout(() => setToastMsg(''), 3000);
  };

  // ===== 合集浏览 =====
  const enterCollection = async (hash) => {
    try {
      const c = await api.getAnonCollection(hash);
      if (!c?.entries?.length) { showToast('空合集', true); return; }
      setEnteredColl(c);
      setEnteredCollFiles(c.entries || []);
      setCollViewPath('');
    } catch { showToast('无法加载合集', true); }
  };

  const leaveCollection = () => {
    setEnteredColl(null);
    setEnteredCollFiles(null);
    setCollViewPath('');
    setSelectMode(false);
    setSelectedColls(new Set());
    setSelectedFiles(new Set());
  };

  const navIntoDir = (dir) => setCollViewPath(prev => prev ? `${prev}/${dir}` : dir);

  // ===== 条目操作 =====
  const addEntry = (hash, path, mime_type, size) => {
    // 坑：旧实现有 500ms lastClick 防抖，handleSysAddFolder/拖拽等程序化批量 add 会被静默丢弃
    //（walkDir 不重试，加了 N 个但实际只进 1-2 个）。改为在 reducer 内按 path+hash 去重，去掉时间闸。
    // 坑：合集条目可能只有 URL provider（没有 sha256）。旧实现把 CollBrowser 传进来的
    // providers[0].value 一律当成 sha256，URL-only 条目会生成非法 64 位 hash 被后端拒绝。
    // 这里按形态识别 URL，存成 url provider。
    const isUrl = typeof hash === 'string' && /^https?:\/\//i.test(hash);
    const providers = hash ? [{ type: isUrl ? 'url' : 'sha256', value: hash, mime_type: mime_type || '' }] : [];
    setEntries(prev => {
      const dup = prev.some(e =>
        e.path === path && (e.hash || e.providers?.[0]?.value) === hash
      );
      if (dup) return prev;
      return [...prev, { hash: isUrl ? '' : hash, path, providers, mime_type, size }];
    });
  };
  const removeEntry = (entry) => {
    setEntries(prev => prev.filter(e => {
      const eh = e.hash || e.providers?.[0]?.value;
      const th = entry.hash || entry.providers?.[0]?.value;
      return !(e.path === entry.path && eh === th);
    }));
  };
  const renameEntry = (oldPath, newPath) => {
    // 坑：旧实现只改 path 精确相等的条目 → 重命名文件夹（dir/ → newdir/）时
    // dir/a.txt 等子条目路径不变，合集内容损坏（孤儿条目浮到根部）。
    // 目录重命名必须把子路径前缀一并改掉。
    const isDir = oldPath.endsWith('/');
    // 目录路径统一补尾斜杠：外部调用（FileTree 已补，但其他调用方可能只传
    // 纯目录名）若不带 '/'，会让子条目拼接成 "newdir" + "/a.txt" 而目录自身
    // 变成 "newdir"（与集合内部目录约定不一致）。
    const normalizedNewPath = isDir && newPath && !newPath.endsWith('/') ? newPath + '/' : newPath;
    const oldPrefix = isDir ? oldPath : oldPath + '/';
    setEntries(prev => prev.map(e => {
      if (e.path === oldPath) return { ...e, path: normalizedNewPath };
      if (isDir && (e.path || '').startsWith(oldPrefix)) return { ...e, path: normalizedNewPath + (e.path || '').slice(oldPath.length) };
      return e;
    }));
  };

  // 移动条目到目标目录（含目录本身：连带子条目）。用于 FileTree 树内拖拽/移动弹窗。
  // 坑：树内拖拽是"移动"语义（去源），不是 onDrop 的"添加"语义（复制）——
  // 旧实现拖进文件夹后源条目残留 = 重复副本。
  const moveEntry = (oldPath, targetDir) => {
    const isDir = oldPath.endsWith('/');
    const base = oldPath.replace(/\/$/, '');
    const name = base.split('/').pop();
    const newPath = (targetDir ? targetDir + '/' : '') + name;
    const oldPrefix = isDir ? oldPath : oldPath + '/';
    // 防呆：目标是自己或自己子目录 → 拒绝，避免把目录拖进自己怀里。
    // 注意：检查必须放在 setEntries 回调外面，state updater 应保持纯函数，
    // 不能在 updater 里做 showToast 这种副作用（StrictMode 下会重复触发）。
    if (targetDir && (targetDir === base || targetDir.startsWith(base + '/'))) {
      showToast('不能移动到自身或其子目录', true);
      return;
    }
    const dst = newPath + (isDir ? '/' : '');
    if (dst === oldPath) return;
    setEntries(prev => prev.flatMap(e => {
      if (e.path === oldPath) return [{ ...e, path: dst }];
      if (isDir && (e.path || '').startsWith(oldPrefix)) return [{ ...e, path: dst + (e.path || '').slice(oldPath.length) }];
      return [e];
    }));
  };

  const handleFileAdd = (hash, path, mime_type, size) => {
    addEntry(hash, path, mime_type, size);
    showToast(`已添加: ${(path || '').split('/').pop()}`);
  };

  // 添加 URL 条目：可选择是否注册（下载文件获取 sha256）
  const handleAddUrl = async (url, filename, shouldRegister) => {
    const name = filename || url.split('/').pop() || 'url-file';
    if (shouldRegister) {
      try {
        const res = await api.registerURL(url, name);
        if (res?.hash) {
          addEntry(res.hash, name, res.mime_type || '', res.size || 0);
          showToast(`已注册 URL: ${name}`);
        }
      } catch (e) {
        showToast(`URL 注册失败: ${e.message}`, true);
      }
    } else {
      const providers = [{ type: "url", value: url }];
      setEntries(prev => [...prev, { hash: '', path: name, providers, mime_type: '', size: 0 }]);
      showToast(`已添加 URL: ${name}`);
    }
  };

  // 从本地电脑添加文件：先注册再添加
  const handleSysAddFile = async (sysPath, name, size) => {
    try {
      const res = await api.registerLocalFile(sysPath, name);
      if (res?.hash) {
        addEntry(res.hash, name, res.mime_type || '', size);
        showToast(`已添加: ${name}`);
      }
    } catch (e) {
      showToast(`注册失败: ${e.message}`, true);
    }
  };

  // 从本地电脑添加文件夹：递归列出文件后逐一注册添加
  const handleSysAddFolder = async (dirPath, dirName) => {
    let added = 0;
    const walkDir = async (path, prefix) => {
      let entries;
      try {
        entries = await api.browseDir(path);
      } catch { return; }
      if (!entries || entries.length === 0) return;
      for (const e of entries) {
        const relPath = prefix ? prefix + '/' + e.name : e.name;
        if (e.is_dir) {
          await walkDir(e.path, relPath);
        } else {
          try {
            const res = await api.registerLocalFile(e.path, relPath);
            if (res?.hash) {
              addEntry(res.hash, dirName + '/' + relPath, res.mime_type || '', e.size || 0);
              added++;
            }
          } catch {}
        }
      }
    };
    await walkDir(dirPath, '');
    if (added > 0) showToast(`已添加 ${added} 个文件到 ${dirName}/`);
    else showToast(`空文件夹已忽略: ${dirName}`, true);
  };

  // ===== 保存合集 =====
  // 权限校验必须在前端先拦一道：restricted 没挑人会生成「谁都打不开」的合集，
  // 后端也会 400，但本地提示比一轮往返后再报错清楚得多。
  // 坑：accessList 和 visibility 必须同一个动作里改，否则「切到指定权限但名单还留着上一次的人」，
  // 后端会收到旧名单；pick() 里切换时清空名单就是为此。
  const pickVisibility = (v, list = []) => {
    setVisibility(v);
    setAccessList(v === VISIBILITY.RESTRICTED ? list : []);
  };

  const openAccountPicker = async () => {
    // 账号目录来自 regserver 代理；服务缺席时拿到空数组，AccountPicker 走手动 @id 兜底
    const [acc, grp] = await Promise.all([api.listKnownAccounts(), api.listKnownGroups()]);
    setAccounts(acc);
    setGroups(grp);
    setShowAccountPicker(true);
  };

  const handleSave = async (broadcast = false) => {
    const valid = entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value));
    if (!valid.length) { showToast('请先添加文件', true); return; }
    if (!fname.trim() && !showNamePrompt) {
      setShowNamePrompt(true);
      return;
    }
    if (visibility === VISIBILITY.RESTRICTED && accessList.length === 0) {
      showToast('请至少选择一个可访问的账号', true);
      setShowAccountPicker(true);
      return;
    }
    if (visibility === VISIBILITY.PRIVATE && !operator) {
      showToast('未绑定 regserver 账号，无法设为仅自己', true);
      setVisibility(VISIBILITY.PUBLIC);
      return;
    }
    setShowNamePrompt(false);
    setSaving(true);
    try {
      const tagList = tags.split(/[,;]/).map(t => t.trim()).filter(Boolean);
      const res = await api.createAnonCollection(valid, fname.trim(), tagList, visibility, accessList);
      loadCollections();
      if (broadcast) {
        // 广播 = 存入 BT DHT/做种：DHT announce 让 Peerua 网络能查到这个 hash，
        // seed-collection 让本节点挂着做种。任一失败都不算合集创建失败。
        try { await api.btSeedCollection(res.hash); } catch {}
        try {
          await api.btAnnounce(res.hash);
          showToast('合集已创建并广播');
        } catch { showToast('合集已创建，广播未成功（P2P 未连接）', true); }
      } else {
        showToast('合集创建成功');
      }
      nav(`/anon/collections/${res.hash}`);
    } catch (err) { showToast(`创建失败: ${err.message}`, true); }
    setSaving(false);
  };

  const saveCollToNode = async (coll) => {
    if (!coll?.entries?.length) return;
    try {
      const name = (coll.friendly_name || '合集') + ' (副本)';
      const res = await api.createAnonCollection(coll.entries, name);
      showToast('已保存到本机');
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
    } catch(e) { showToast('保存失败: ' + e.message, true); }
  };

  const batchSaveFiles = async () => {
    const selectedEntries = enteredCollFiles?.filter(f => selectedFiles.has(f.path)) || [];
    if (!selectedEntries.length) { showToast('请选择文件', true); return; }
    try {
      const name = (enteredColl?.friendly_name || '选集') + ' (选集)';
      const res = await api.createAnonCollection(selectedEntries.map(e => ({ path: e.path, hash: e.hash, providers: e.providers })), name);
      showToast(`已保存 ${selectedEntries.length} 个文件`);
      loadCollections();
      nav(`/anon/collections/${res.hash}`);
    } catch(e) { showToast('保存失败: ' + e.message, true); }
  };

  const batchSaveColls = async () => {
    const sel = collections.filter(c => selectedColls.has(c.hash));
    if (!sel.length) { showToast('请选择合集', true); return; }
    try {
      for (const c of sel) {
        const full = await api.getAnonCollection(c.hash);
        if (full?.entries?.length) {
          await api.createAnonCollection(full.entries, (full.friendly_name || c.friendly_name || '合集') + ' (副本)');
        }
      }
      showToast(`已保存 ${sel.length} 个合集`);
      loadCollections();
    } catch(e) { showToast('批量保存失败: ' + e.message, true); }
  };

  // ===== 选择切换 =====
  const toggleCollSelect = (hash) => {
    setSelectedColls(prev => { const n = new Set(prev); n.has(hash) ? n.delete(hash) : n.add(hash); return n; });
  };
  const toggleFileSelect = (path) => {
    setSelectedFiles(prev => { const n = new Set(prev); n.has(path) ? n.delete(path) : n.add(path); return n; });
  };
  const toggleSelectMode = () => {
    setSelectMode(!selectMode);
    setSelectedColls(new Set());
    setSelectedFiles(new Set());
  };

  // ===== 拖拽 =====
  const onDragStartFile = (e, f) => {
    const payload = JSON.stringify({
      hash: f.hash || '',
      path: f.filename || f.name || '',
      name: f.filename || f.name || '',
      mime_type: f.mime_type || '',
      size: f.size || 0,
      sysPath: f.sysPath || f.path || '',
    });
    e.dataTransfer.setData('text/plain', payload);
    e.dataTransfer.setData('application/peerdrive-file', payload);
    e.dataTransfer.effectAllowed = 'copy';
  };

  // ===== 条目操作集合 =====
  const entryActions = {
    onRemove: removeEntry,
    onRename: renameEntry,
    onMove: moveEntry,
    onNewFolder: (name) => addEntry('', name + '/'),
    onDrop: async (data) => {
      const d = data.targetDir || '';
      const n = data.name || data.path || 'untitled';
      const filePath = d ? `${d}/${n}` : n;
      let h = data.hash;
      if (!h && data.sysPath) {
        try {
          const res = await api.registerLocalFile(data.sysPath, n);
          h = res.hash;
        } catch (e) { console.error('drop register fail:', e); return; }
      }
      if (h) addEntry(h, filePath, data.mime_type || '', data.size || 0);
    },
  };

  const handleSourceTabChange = (tab) => {
    setLeftSourceTab(tab);
    if (tab === 'local') setSysPath('/');
    if (tab === 'registered_dir') setRegDirPath('');
  };

  // 移动端面板切换
  const [mobilePanel, setMobilePanel] = useState('files');

  const handleFileSelect = (file) => {
    setSelectedFile(file);
  };

  // 选择文件后自动切换到预览面板（移动端）
  useEffect(() => {
    if (selectedFile && window.innerWidth < 768) {
      setMobilePanel('preview');
    }
  }, [selectedFile]);

  return (
    <div className="flex flex-1 overflow-hidden h-full bg-transparent">
      {/* ── 移动端面板切换标签 ── */}
      <div className="md:hidden sticky top-0 z-10 bg-surface-raised/95 backdrop-blur border-b border-white/[0.04]">
        <div className="flex p-1 gap-1">
          {[
            { id: 'files', label: '📁 文件', count: files.length },
            { id: 'preview', label: '👁 预览' },
            { id: 'editor', label: '✏️ 编辑器', count: entries.length },
          ].map(tab => (
            <button key={tab.id} onClick={() => setMobilePanel(tab.id)}
              className={`flex-1 text-xs py-2 rounded transition-colors ${
                mobilePanel === tab.id
                  ? 'bg-brand-600 text-white font-medium'
                  : 'text-gray-400 hover:text-white hover:bg-white/[0.06]'
              }`}>
              {tab.label}{tab.count !== undefined ? ` (${tab.count})` : ''}
            </button>
          ))}
        </div>
      </div>

      {/* 左列：筛选 + 文件列表 */}
      <div className={`w-[380px] shrink-0 flex-col overflow-hidden border-r border-white/[0.04] ${
        mobilePanel === 'files' ? 'flex' : 'hidden'
      } md:flex`}>
        <LeftPanel
          sourceTab={leftSourceTab} sortKey={leftSortKey} sortOrder={leftSortOrder}
          typeFilters={leftTypeFilters} search={search}
          files={files} filteredFiles={filteredFiles}
          collections={collections} filteredCollections={filteredCollections}
          collSearch={collSearch} collTagFilter={collTagFilter} collSort={collSort}
          allCollTags={allCollTags}
          enteredColl={enteredColl} collViewPath={collViewPath}
          enteredCollFiles={enteredCollFiles}
          selectMode={selectMode} selectedColls={selectedColls}
          selectedFiles={selectedFiles}
          sysPath={sysPath} sysEntries={sysEntries} sysLoading={sysLoading}
          searchHistory={searchHistory}
          regDirPath={regDirPath}
          onSourceTab={handleSourceTabChange}
          onRegDirPath={setRegDirPath}
          onSortKey={setLeftSortKey}
          onSortOrder={() => setLeftSortOrder(prev => prev === 'asc' ? 'desc' : 'asc')}
          onTypeFilter={setLeftTypeFilters}
          onSearch={setSearch}
          onFileSelect={handleFileSelect}
          onFileAdd={handleFileAdd}
          onDragStart={onDragStartFile}
          onCollSearch={setCollSearch} onCollTag={setCollTagFilter}
          onCollSort={setCollSort}
          onEnterColl={enterCollection} onLeaveColl={leaveCollection}
          onPathNav={setCollViewPath} onNavIntoDir={navIntoDir}
          onSaveToNode={saveCollToNode}
          onSelectToggle={toggleSelectMode}
          onToggleCollSelect={toggleCollSelect}
          onToggleFileSelect={toggleFileSelect}
          onBatchSaveColls={batchSaveColls}
          onBatchSaveFiles={batchSaveFiles}
          onSysNav={setSysPath} onSysAdd={addEntry} onSysAddFile={handleSysAddFile} onSysAddFolder={handleSysAddFolder}
          onSearchHistorySelect={(q) => { setCollSearch(q); }}
          onSearchHistoryUpdate={setSearchHistory}
        />
      </div>

      {/* 中列：文件预览 */}
      <div className={`flex-1 flex-col overflow-hidden bg-transparent ${
        mobilePanel === 'preview' ? 'flex' : 'hidden'
      } md:flex`}>
        <MiddlePanel selectedFile={selectedFile} />
      </div>

      {/* 右列：编辑器 */}
      <div className={`w-[420px] shrink-0 border-l border-white/[0.04] bg-surface-card flex-col overflow-hidden ${
        mobilePanel === 'editor' ? 'flex' : 'hidden'
      } md:flex`}>
        <RightPanel
          fname={fname} tags={tags} entries={entries} saving={saving}
          showNamePrompt={showNamePrompt} toastMsg={toastMsg} toastErr={toastErr}
          entryActions={entryActions}
          onFname={setFname} onTags={setTags} onSave={handleSave}
          onAddUrl={handleAddUrl}
          onCloseNamePrompt={() => setShowNamePrompt(false)}
          visibility={visibility} accessList={accessList} operator={operator}
          onVisibilityChange={pickVisibility}
          onRequestAccounts={openAccountPicker}
          onBroadcast={() => handleSave(true)}
        />
      </div>

      {/* 账号选择器（仅 restricted 档位用到；弹层放父层，避免编辑器重渲染丢状态） */}
      {showAccountPicker && (
        <AccountPicker accounts={accounts} groups={groups} selected={accessList}
          onConfirm={(list) => {
            setVisibility(VISIBILITY.RESTRICTED);
            setAccessList(list);
            setShowAccountPicker(false);
            if (list.length === 0) setVisibility(VISIBILITY.PUBLIC);
          }}
          onClose={() => setShowAccountPicker(false)} />
      )}
    </div>
  );
}
