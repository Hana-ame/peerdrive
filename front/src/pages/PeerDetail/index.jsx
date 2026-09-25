// pages/PeerDetail/index.jsx：对方节点详情——「文件链接」列表 + 保存。
//
// 这是用户目标里最关键的一屏：加入节点之后，看到对方共享的
// **打包好的 collection 或单独的文件**，选中 → 保存 → 从对方下载。
//
// 交互取舍：
//   - 合集默认折叠，展开才请求/展示条目（一个合集几十上百条时全展开会淹没页面）；
//   - 集合与文件两种粒度都支持"选中若干项保存"，另有"保存整个合集"一键；
//   - 保存是服务端任务（见 /p2p/pull*），点击后立刻跳「传输任务」页看进度，
//     不在本页做进度轮询——避免同一份状态在两个页面各轮询一次。
import React, { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import * as api from '../../api';
import SideNav, { MobileNav } from '../../components/netdisk/SideNav';
import FileTable, { Btn } from '../../components/netdisk/FileTable';
import { formatSize, shortPeer } from '../../components/netdisk/format';

export default function PeerDetail() {
  const { peer } = useParams();
  const nav = useNavigate();
  const [data, setData] = useState({ collections: [], files: [] });
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [notice, setNotice] = useState('');
  const [expanded, setExpanded] = useState({});
  const [selected, setSelected] = useState([]);
  const [busy, setBusy] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setErr('');
    setNotice('');
    try {
      const res = await api.getPeerShares(peer);
      setData({
        collections: Array.isArray(res?.collections) ? res.collections : [],
        files: Array.isArray(res?.files) ? res.files : [],
      });
    } catch (e) {
      setErr(e?.message || '无法获取对方共享清单（可能对方离线或未开启共享）');
      setData({ collections: [], files: [] });
    }
    setLoading(false);
  }, [peer]);

  useEffect(() => { load(); }, [load]);

  const toggleSel = (key) => {
    setSelected((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
  };

  // saveSelected 把勾选的文件/条目建成拉取任务。
  // 逐条建任务而不是打包成一个批量请求：后端按条目独立给进度/独立失败，
  // 一个文件坏了不该让整批停住（与 PT 里"选中多个种子"的语义一致）。
  const saveSelected = async () => {
    if (!selected.length) return;
    setBusy('save');
    setErr('');
    let n = 0;
    try {
      for (const key of selected) {
        const parts = key.split('\u0000');
        if (parts[0] === 'f') {
          const f = data.files.find((x) => x.hash === parts[1]);
          if (f) {
            await api.startPull(peer, f.hash, f.name, f.path || f.name);
            n++;
          }
        } else if (parts[0] === 'e') {
          // 合集条目：path 保留目录结构，保存后本地也按同样结构落盘
          await api.startPull(peer, parts[2], parts[3]?.split('/').pop() || '', parts[3] || '');
          n++;
        }
      }
      setNotice(`已创建 ${n} 个保存任务`);
      setSelected([]);
      if (n > 0) nav('/transfers');
    } catch (e) {
      setErr(e?.message || '创建保存任务失败');
    }
    setBusy('');
  };

  const saveCollection = async (coll) => {
    setBusy(coll.hash);
    setErr('');
    try {
      const res = await api.startPullCollection(peer, coll.hash);
      setNotice(`已创建 ${res?.count ?? 0} 个保存任务`);
      nav('/transfers');
    } catch (e) {
      setErr(e?.message || '保存合集失败');
    }
    setBusy('');
  };

  const fileRows = data.files.map((f) => ({
    key: `f\u0000${f.hash}`,
    name: f.name || f.hash,
    hash: f.hash,
    size: f.size,
    mime: f.mime,
    path: f.path,
    source: '单文件',
    title: f.path || f.name,
  }));

  return (
    <div className="flex flex-1 min-h-0">
      <SideNav />
      <div className="flex-1 min-w-0 flex flex-col overflow-hidden">
        <MobileNav />
        <div className="flex-1 overflow-y-auto p-4 md:p-6">
          <div className="flex flex-wrap items-center gap-2 mb-1">
            <button onClick={() => nav('/peers')} className="text-xs text-gray-400 hover:text-white">← 我的节点</button>
          </div>
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <h1 className="font-mono text-base text-gray-100 break-all" title={peer}>{shortPeer(peer, 16)}</h1>
            <span className="text-xs text-gray-500">
              {data.collections.length} 个合集 · {data.files.length} 个文件
            </span>
            <div className="ml-auto flex gap-2">
              <Btn onClick={load} disabled={loading}>{loading ? '加载中…' : '刷新'}</Btn>
              <Btn tone="primary" onClick={saveSelected} disabled={!selected.length || busy === 'save'}>
                {busy === 'save' ? '创建中…' : `保存选中 (${selected.length})`}
              </Btn>
            </div>
          </div>

          {notice && <div className="mb-3 text-xs text-green-400">{notice}</div>}
          {err && <div className="mb-3 text-xs text-red-400">{err}</div>}

          {/* ── 打包好的合集 ── */}
          <h2 className="mb-2 text-sm font-semibold text-gray-200">打包好的合集</h2>
          {loading ? (
            <div className="py-8 text-center text-sm text-gray-500">加载中…</div>
          ) : data.collections.length === 0 ? (
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.06] px-4 py-8 text-center text-sm text-gray-500">
              对方没有共享合集
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {data.collections.map((c) => {
                const open = !!expanded[c.hash];
                const entries = Array.isArray(c.entries) ? c.entries : [];
                const totalSize = entries.reduce((s, e) => s + (Number(e.size) || 0), 0);
                return (
                  <div key={c.hash} className="rounded-lg border border-white/[0.04] bg-white/[0.08]">
                    <div className="flex flex-wrap items-center gap-2 p-3">
                      <button
                        type="button"
                        onClick={() => setExpanded((p) => ({ ...p, [c.hash]: !p[c.hash] }))}
                        className="text-gray-400 hover:text-white w-5"
                        aria-label={open ? '收起' : '展开'}
                      >
                        {open ? '▾' : '▸'}
                      </button>
                      <span>📦</span>
                      <span className="text-sm text-gray-200 truncate">
                        {c.name || `${String(c.hash).slice(0, 12)}…`}
                      </span>
                      <span className="text-[11px] text-gray-500">
                        {entries.length} 个文件{totalSize ? ` · ${formatSize(totalSize)}` : ''}
                      </span>
                      <div className="ml-auto flex gap-2">
                        <Btn
                          tone="primary"
                          disabled={busy === c.hash}
                          onClick={() => saveCollection(c)}
                        >
                          {busy === c.hash ? '创建中…' : '保存整个合集'}
                        </Btn>
                      </div>
                    </div>
                    {open && (
                      <div className="border-t border-white/[0.04]">
                        <FileTable
                          rows={entries.map((e) => {
                            const key = `e\u0000${c.hash}\u0000${e.hash}\u0000${e.path}`;
                            return {
                              key,
                              name: e.path,
                              hash: e.hash,
                              mime: e.mime,
                              source: '合集条目',
                              title: e.path,
                            };
                          })}
                          columns={['hash']}
                          selectable
                          selected={selected}
                          onToggleSelect={toggleSel}
                          empty="该合集没有条目"
                        />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}

          {/* ── 单独文件 ── */}
          <h2 className="mt-8 mb-2 text-sm font-semibold text-gray-200">单独文件</h2>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.06]">
            <FileTable
              rows={fileRows}
              columns={['size', 'hash']}
              loading={loading}
              empty="对方没有共享单独文件"
              selectable
              selected={selected}
              onToggleSelect={toggleSel}
            />
          </div>

          <div className="mt-4 text-[11px] text-gray-600">
            保存会把内容从对方节点拉取到本节点并登记进「我的网盘」；本地已有相同内容时自动跳过。
          </div>
        </div>
      </div>
    </div>
  );
}
