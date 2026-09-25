// pages/Drive/index.jsx：「我的网盘」——本节点持有的文件与合集。
//
// 版式参考 Nextcloud Files / Cloudreve 的"我的文件"主区：顶部工具条
// （上传 / 刷新 / 统计），下面是一张文件表格；再下面一行"我的合集"
// （本节点打包好的 collection，可直接进浏览页）。
//
// 数据来源：
//   - 文件：GET /files（file_index 的本地管理清单；跨节点"保存"下来的文件
//     也会出现在这里，因为 PeerPuller 保存后做了索引登记）
//   - 合集：GET /anon/collections
//   - 共享范围：GET /peerjs/share（每行文件带是否已共享）
//
// 「自由选择共享内容」落在这一页：上传/登记进来只是"我持有"，是否对外提供是
// 另一件事——逐行勾选（按 hash），或整目录共享（见 doc/NETDISK.md M2.6）。
// 勾选即生效，不用重启节点；后端落在 storage/share_scope.json。
//
// 每条共享声明还带一个**级别**（doc/NETDISK.md §12.6）：
//   public   列在共享清单里，谁都能下载
//   unlisted 不列在清单里，但知道 hash 的人能下载
//   private  不列在清单里，只有自己和好友能下载
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../../api';
import SideNav, { MobileNav } from '../../components/netdisk/SideNav';
import FileTable, { Btn } from '../../components/netdisk/FileTable';
import { formatSize } from '../../components/netdisk/format';

// LEVELS 三档共享级别（顺序 = 由宽到严，与后端 model.Level* 一致）。
export const LEVELS = [
  { v: 'public', label: '公开', hint: '列在共享清单里，谁都能下载' },
  { v: 'unlisted', label: '不列出', hint: '不列在清单里，知道 hash 的人能下载' },
  { v: 'private', label: '私密', hint: '不列在清单里，只有自己和好友能下载' },
];

export const labelOfLevel = (v) => (LEVELS.find((l) => l.v === v) || {}).label || v || '公开';

// selectedIds 兼容两种返回形态：新后端回 [{id, level}]，老后端回 [hash]。
export const selectedIds = (res) => (res?.selected || res?.files || []).map((it) => (
  typeof it === 'string' ? it : it?.id
)).filter(Boolean);

export default function Drive() {
  const [files, setFiles] = useState([]);
  const [colls, setColls] = useState([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState('');
  const [notice, setNotice] = useState('');
  // scope = 本节点共享范围（null = 该端点不可用，例如节点没开 peerjs）。
  // 拿不到就整块不渲染：不要让"共享设置加载失败"盖住文件列表本身。
  const [scope, setScope] = useState(null);
  const fileInput = useRef(null);
  // friendDraft 好友名单的输入框草稿（private 内容放行给这些节点 ID）。
  // 为什么要草稿而不是直接改：名单是多行的，每敲一个字就 PUT 一次既打后端，
  // 也会在半截输入（"a,"）时把空项写进去。
  const [friendDraft, setFriendDraft] = useState('');
  // dirDraft / dirLevel 待添加的共享目录（绝对路径）与它的级别。
  const [dirDraft, setDirDraft] = useState('');
  const [dirLevel, setDirLevel] = useState('public');

  // load 拉取文件与合集。两处各自容错：文件列表失败不该让合集也空白。
  const load = useCallback(async () => {
    setLoading(true);
    setErr('');
    try {
      const list = await api.listFiles('time');
      setFiles(Array.isArray(list) ? list : []);
    } catch (e) {
      setFiles([]);
      setErr(e?.message || '文件列表加载失败');
    }
    try {
      const c = await api.listAnonCollections();
      setColls(Array.isArray(c) ? c : []);
    } catch {
      setColls([]);
    }
    // 共享范围单独容错：没开 peerjs 的节点本来就没有"对外共享"这回事
    try {
      const sc = await api.getShareScope();
      setScope(sc);
      setFriendDraft((sc?.friends || []).join(', '));
    } catch {
      setScope(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const onUpload = async (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    setBusy('upload');
    setNotice('');
    try {
      await api.uploadFile(f);
      setNotice(`已上传 ${f.name}`);
      await load();
    } catch (ex) {
      setErr(ex?.message || '上传失败');
    }
    setBusy('');
    if (fileInput.current) fileInput.current.value = '';
  };

  const onDownload = async (row) => {
    setBusy(row.hash);
    setErr('');
    try {
      await api.downloadFileToDisk(row.hash, row.name);
    } catch (ex) {
      setErr(ex?.message || '下载失败');
    }
    setBusy('');
  };

  // 预览：拉一份 blob 到内存开新窗口。大文件由 api 层拒绝（TOO_LARGE），
  // 这里把该错误翻译成用户能理解的提示，并引导去"下载"。
  const onPreview = async (row) => {
    setBusy(row.hash);
    setErr('');
    try {
      const url = await api.getBlobUrl(row.hash, row.mime);
      window.open(url, '_blank', 'noopener');
    } catch (ex) {
      if (ex?.code === 'TOO_LARGE') {
        setErr('文件太大，无法在线预览，请用「下载」保存到本地');
      } else {
        setErr(ex?.message || '预览失败');
      }
    }
    setBusy('');
  };

  const onDelete = async (row) => {
    if (!window.confirm(`删除 ${row.name}？（只移除本节点索引，不影响内容寻址存储）`)) return;
    setBusy(row.hash);
    try {
      await api.deleteFile(row.hash);
      await load();
    } catch (ex) {
      setErr(ex?.message || '删除失败');
    }
    setBusy('');
  };

  // sharedMap：hash → { shared, by_dir, level }。后端算好共享状态（目录共享也算），
  // 前端不自己比前缀——Windows 盘符大小写/分隔符混写会让两份实现跑偏。
  // level 也是后端算的：同一文件被"目录 + 单文件"同时命中时取最宽松的那条，
  // 前端再算一遍迟早和后端口径不一致。
  const sharedMap = useMemo(() => {
    const m = new Map();
    for (const f of scope?.files || []) m.set(f.hash, f);
    return m;
  }, [scope]);

  const sharedCount = useMemo(
    () => (scope?.files || []).filter((f) => f.shared).length,
    [scope],
  );

  // onToggleShare 单行勾选：立刻生效（后端落盘），失败则回滚提示。
  const onToggleShare = async (row) => {
    const cur = sharedMap.get(row.hash);
    const want = !cur?.shared;
    setBusy(row.hash);
    setErr('');
    try {
      // 级别沿用当前值：这是"共享/不共享"开关，不该顺手把级别改回 public
      const res = await api.setFilesShared([row.hash], want, cur?.level || '');
      // 后端返回更新后的完整勾选列表；目录共享带上的文件不在里面，
      // 因此以"请求意图 + by_dir"合并本地状态，避免勾选框闪回。
      const picked = new Set(selectedIds(res));
      setScope((s) => (s ? {
        ...s,
        files: (s.files || []).map((f) => (
          f.hash === row.hash
            ? { ...f, shared: want || f.by_dir, by_dir: f.by_dir }
            : { ...f, shared: f.by_dir || picked.has(f.hash) }
        )),
      } : s));
      setNotice(want ? `已共享 ${row.name}` : `已取消共享 ${row.name}`);
    } catch (ex) {
      setErr(ex?.message || '共享设置失败');
    }
    setBusy('');
  };

  // onSetLevel 改单个文件的共享级别（三档，见 LEVELS）。
  const onSetLevel = async (row, level) => {
    setBusy(row.hash);
    setErr('');
    try {
      await api.setFilesShared([row.hash], true, level);
      setScope((s) => (s ? {
        ...s,
        files: (s.files || []).map((f) => (f.hash === row.hash ? { ...f, shared: true, level } : f)),
      } : s));
      setNotice(`${row.name}：${labelOfLevel(level)}`);
    } catch (ex) {
      setErr(ex?.message || '共享级别设置失败');
    }
    setBusy('');
  };

  // onSaveFriends 保存好友名单（private 内容放行给这些节点）。
  const onSaveFriends = async () => {
    const list = friendDraft.split(/[,，\s]+/).map((s) => s.trim()).filter(Boolean);
    setBusy('friends');
    setErr('');
    try {
      const res = await api.setShareScope({ friends: list });
      setScope((s) => (s ? { ...s, friends: res?.friends || list } : s));
      setNotice(list.length ? `好友名单已保存（${list.length} 个）` : '好友名单已清空');
    } catch (ex) {
      setErr(ex?.message || '好友名单保存失败');
    }
    setBusy('');
  };

  // dirsOf 把 GET 回的目录项统一成 {id, level}（旧落盘可能是纯字符串，
  // 后端也接受字符串写法，但回给前端的一律是对象）。
  const dirsOf = (sc) => (sc?.dirs || []).map((d) => (
    typeof d === 'string' ? { id: d, level: 'public' } : { id: d?.id || '', level: d?.level || 'public' }
  )).filter((d) => d.id);

  // onPutDirs 写回**整份**目录列表。
  // 为什么必须整份：PUT /peerjs/share 的"局部更新"只针对"传不传这个字段"，
  // 传了 dirs 就是整体替换。所以增删一条、改一条级别，都要把原有的带上——
  // 只发新增的那一条会静默清掉其它共享目录。
  const onPutDirs = async (next, noticeText) => {
    setBusy('dirs');
    setErr('');
    try {
      const res = await api.setShareScope({ dirs: next });
      // 后端会归一化（转绝对路径、去重、排序），以它回的为准
      setScope((s) => (s ? { ...s, dirs: res?.dirs || next } : s));
      setNotice(noticeText);
    } catch (ex) {
      // 卷根目录、非法级别会被后端整批拒绝（400），原话转给用户
      setErr(ex?.message || '共享目录设置失败');
    }
    setBusy('');
  };

  const onAddDir = () => {
    const p = dirDraft.trim();
    if (!p) {
      setErr('请填目录的绝对路径');
      return;
    }
    const cur = dirsOf(scope);
    if (cur.some((d) => d.id.toLowerCase() === p.toLowerCase())) {
      setErr('这个目录已经在共享范围里了');
      return;
    }
    setDirDraft('');
    onPutDirs([...cur, { id: p, level: dirLevel }], `已共享目录 ${p}`);
  };

  const onRemoveDir = (id) => onPutDirs(
    dirsOf(scope).filter((d) => d.id !== id),
    `已移除共享目录 ${id}`,
  );

  const onSetDirLevel = (id, level) => onPutDirs(
    dirsOf(scope).map((d) => (d.id === id ? { ...d, level } : d)),
    `${id}：${labelOfLevel(level)}`,
  );

  // onToggleEnable 总开关：关掉 = 对外完全不提供清单（已勾选的内容保留）。
  const onToggleEnable = async () => {
    const want = !scope?.enable;
    setBusy('enable');
    setErr('');
    try {
      const res = await api.setShareScope({ enable: want });
      setScope((s) => (s ? { ...s, enable: !!res?.enable } : s));
      setNotice(want ? '已开启对外共享' : '已关闭对外共享（勾选保留）');
    } catch (ex) {
      setErr(ex?.message || '共享开关设置失败');
    }
    setBusy('');
  };

  const rows = files.map((f) => ({
    key: f.hash,
    name: f.filename || f.hash,
    hash: f.hash,
    size: f.size,
    mime: f.mime_type,
    time: f.created_at,
    source: f.provider_type || '本地',
    title: f.filename,
  }));

  const totalSize = files.reduce((s, f) => s + (Number(f.size) || 0), 0);

  return (
    <div className="flex flex-1 min-h-0">
      <SideNav />
      <div className="flex-1 min-w-0 flex flex-col overflow-hidden">
        <MobileNav />
        <div className="flex-1 overflow-y-auto p-4 md:p-6">
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <h1 className="text-lg font-semibold text-gray-100">我的网盘</h1>
            <span className="text-xs text-gray-500">
              {files.length} 个文件 · {formatSize(totalSize)}
            </span>
            <div className="ml-auto flex gap-2">
              <Btn onClick={load} disabled={loading}>刷新</Btn>
              <Btn tone="primary" onClick={() => fileInput.current?.click()} disabled={busy === 'upload'}>
                {busy === 'upload' ? '上传中…' : '上传文件'}
              </Btn>
              <input ref={fileInput} type="file" className="hidden" onChange={onUpload} />
            </div>
          </div>

          {/* 共享范围总开关：关 = 对外完全不提供清单；开 = 按下面逐行勾选给。
              勾选状态本身保留，所以关了再开不用重选。 */}
          {scope && (
            <div className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-white/[0.04] bg-white/[0.06] px-3 py-2">
              <button
                type="button"
                onClick={onToggleEnable}
                disabled={busy === 'enable'}
                className={`rounded px-2 py-1 text-xs font-medium transition-colors ${
                  scope.enable
                    ? 'bg-emerald-600/20 text-emerald-300 hover:bg-emerald-600/30'
                    : 'bg-white/[0.06] text-gray-400 hover:bg-white/[0.08]'
                }`}
              >
                {scope.enable ? '对外共享：开' : '对外共享：关'}
              </button>
              <span className="text-xs text-gray-500">
                已共享 {sharedCount} / {scope.files?.length || 0} 个文件
                {scope.dirs?.length ? ` · 另含 ${scope.dirs.length} 个共享目录` : ''}
              </span>
              <span className="text-[11px] text-gray-600">
                想共享哪个就勾哪个，即时生效，不用重启节点
              </span>
            </div>
          )}

          {/* 共享级别 + 好友名单：决定"给谁看"。
              公开 = 列出来也给；不列出 = 不列但凭 hash 能给；私密 = 只给自己和好友。 */}
          {scope && (
            <div className="mb-3 rounded-lg border border-white/[0.04] bg-white/[0.06] px-3 py-2">
              <div className="flex flex-wrap items-center gap-2 text-xs text-gray-400">
                <span className="text-gray-300">共享级别</span>
                {LEVELS.map((l) => (
                  <span key={l.v} className="text-[11px] text-gray-500" title={l.hint}>
                    {l.label}＝{l.hint}
                  </span>
                ))}
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <label className="text-xs text-gray-400" htmlFor="friends">好友节点 ID</label>
                <input
                  id="friends"
                  value={friendDraft}
                  onChange={(e) => setFriendDraft(e.target.value)}
                  placeholder="用逗号分隔，例如 pd-alpha,pd-beta"
                  className="min-w-[16rem] flex-1 rounded border border-white/[0.06] bg-surface-raised px-2 py-1 font-mono text-xs text-gray-200"
                />
                <Btn onClick={onSaveFriends} disabled={busy === 'friends'}>保存好友</Btn>
              </div>
              <div className="mt-1 text-[11px] text-gray-600">
                「私密」的内容只放行给这些节点；自己的管理台/面板直连本机永远算自己。
                节点 ID 由对端自报，所以名单只在设了 PSK 的网络里才可靠。
              </div>
            </div>
          )}

          {/* 共享目录：整目录共享（含以后新增的文件）。由目录带上的文件在下面
              表格里标「由共享目录带上的」，逐行取消不了——要在这里改范围。 */}
          {scope && (
            <div className="mb-3 rounded-lg border border-white/[0.04] bg-white/[0.06] px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs text-gray-300">共享目录</span>
                <input
                  id="share-dir"
                  value={dirDraft}
                  onChange={(e) => setDirDraft(e.target.value)}
                  placeholder="目录绝对路径，例如 /home/me/media 或 D:\Media"
                  className="min-w-[16rem] flex-1 rounded border border-white/[0.06] bg-surface-raised px-2 py-1 font-mono text-xs text-gray-200"
                />
                <select
                  value={dirLevel}
                  onChange={(e) => setDirLevel(e.target.value)}
                  aria-label="新目录的共享级别"
                  title={LEVELS.map((l) => `${l.label}：${l.hint}`).join('\n')}
                  className="rounded border border-white/[0.06] bg-surface-card px-1.5 py-1 text-xs text-gray-300"
                >
                  {LEVELS.map((l) => (
                    <option key={l.v} value={l.v}>{l.label}</option>
                  ))}
                </select>
                <Btn tone="primary" onClick={onAddDir} disabled={busy === 'dirs'}>
                  {busy === 'dirs' ? '保存中…' : '添加目录'}
                </Btn>
              </div>

              {dirsOf(scope).length > 0 && (
                <ul className="mt-2 space-y-1">
                  {dirsOf(scope).map((d) => (
                    <li key={d.id} className="flex flex-wrap items-center gap-2 text-xs">
                      <span className="min-w-0 flex-1 truncate font-mono text-gray-300" title={d.id}>{d.id}</span>
                      <select
                        value={d.level}
                        onChange={(e) => onSetDirLevel(d.id, e.target.value)}
                        disabled={busy === 'dirs'}
                        aria-label={`${d.id} 的共享级别`}
                        className="rounded border border-white/[0.06] bg-surface-card px-1.5 py-1 text-xs text-gray-300"
                      >
                        {LEVELS.map((l) => (
                          <option key={l.v} value={l.v}>{l.label}</option>
                        ))}
                      </select>
                      <Btn tone="danger" onClick={() => onRemoveDir(d.id)} disabled={busy === 'dirs'}>移除</Btn>
                    </li>
                  ))}
                </ul>
              )}

              <div className="mt-1 text-[11px] text-gray-600">
                整目录共享会把目录下的文件（含以后新增的）都带上；带上的文件在下面表格里标
                「由共享目录带上的」，要取消得在这里移除目录。卷根目录（<code>/</code>、<code>C:\</code>）会被后端拒绝。
              </div>
            </div>
          )}

          {notice && <div className="mb-3 text-xs text-green-400">{notice}</div>}
          {err && <div className="mb-3 text-xs text-red-400">{err}</div>}

          <div className="rounded-lg border border-white/[0.04] bg-white/[0.06]">
            <FileTable
              rows={rows}
              loading={loading}
              empty="还没有文件。可以在「节点市场」加入别人的节点，把对方共享的文件保存过来。"
              actions={(row) => (
                <>
                  <Btn onClick={() => onPreview(row)} disabled={busy === row.hash}>预览</Btn>
                  <Btn onClick={() => onDownload(row)} disabled={busy === row.hash}>下载</Btn>
                  {/* 共享是独立于"持有"的选择：不勾就只是你自己能看 */}
                  {scope && (
                    <Btn
                      tone={sharedMap.get(row.hash)?.shared ? 'primary' : undefined}
                      onClick={() => onToggleShare(row)}
                      disabled={busy === row.hash}
                      title={sharedMap.get(row.hash)?.by_dir ? '由共享目录带上的，取消需改目录范围' : ''}
                    >
                      {sharedMap.get(row.hash)?.shared ? '取消共享' : '共享'}
                    </Btn>
                  )}
                  {/* 级别只在已共享时可选：没共享的东西谈"给谁看"没有意义
                      （顺手避免"设了 private 但以为已经共享出去了"） */}
                  {scope && sharedMap.get(row.hash)?.shared && (
                    <select
                      value={sharedMap.get(row.hash)?.level || 'public'}
                      onChange={(e) => onSetLevel(row, e.target.value)}
                      disabled={busy === row.hash}
                      aria-label={`${row.name} 的共享级别`}
                      title={LEVELS.map((l) => `${l.label}：${l.hint}`).join('\n')}
                      className="rounded border border-white/[0.06] bg-surface-card px-1.5 py-1 text-xs text-gray-300"
                    >
                      {LEVELS.map((l) => (
                        <option key={l.v} value={l.v}>{l.label}</option>
                      ))}
                    </select>
                  )}
                  <Btn tone="danger" onClick={() => onDelete(row)} disabled={busy === row.hash}>删除</Btn>
                </>
              )}
            />
          </div>

          <h2 className="mt-8 mb-3 text-sm font-semibold text-gray-200">
            我的合集 <span className="text-xs font-normal text-gray-500">（打包好的文件链接，别人可经 share 帧看到）</span>
          </h2>
          {colls.length === 0 ? (
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.06] px-4 py-8 text-center text-sm text-gray-500">
              还没有合集。在「创建合集」里把文件打成一个包，就能整包分享给别人。
            </div>
          ) : (
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
              {colls.map((c) => (
                <Link
                  key={c.hash}
                  to={`/c/${c.hash}`}
                  className="rounded-lg border border-white/[0.04] bg-white/[0.08] p-3 hover:border-white/[0.06] hover:bg-white/[0.06] transition-colors"
                >
                  <div className="text-sm text-gray-200 truncate">
                    {c.friendly_name || `${String(c.hash).slice(0, 12)}…`}
                  </div>
                  <div className="mt-1 text-[11px] text-gray-500">
                    {c.entry_count ?? 0} 个文件 · {c.visibility || 'public'}
                  </div>
                  <div className="mt-1 font-mono text-[10px] text-gray-600 truncate">{c.hash}</div>
                </Link>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
