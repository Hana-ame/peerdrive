// pages/Market/index.jsx：「节点市场」——发现别人的节点并加入。
//
// 用户类比是 PT/BT 站点：这里是"资源目录"，加入节点 = 订阅这个对端，
// 之后它成为本节点的常驻对端（信令重连后自动连，见后端 SetExtraPeers）。
//
// 数据来源：GET /peerjs/nodes = 发现服务器在线节点 ∪ 本地已加入清单。
// 加入/移出只改本地清单，不改变对方任何状态。
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import * as api from '../../api';
import SideNav, { MobileNav } from '../../components/netdisk/SideNav';
import NodeCard from '../../components/netdisk/NodeCard';
import { Btn } from '../../components/netdisk/FileTable';
import { shortPeer } from '../../components/netdisk/format';

export default function Market() {
  const nav = useNavigate();
  const [self, setSelf] = useState(null);
  const [nodes, setNodes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState('');
  const [q, setQ] = useState('');
  const [onlyJoined, setOnlyJoined] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setErr('');
    try {
      const res = await api.getNodeMarket();
      setNodes(Array.isArray(res?.nodes) ? res.nodes : []);
      setSelf(res?.self || null);
    } catch (e) {
      setNodes([]);
      setErr(e?.message || '市场列表加载失败（可能是发现服务器不可达）');
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const onJoin = async (node) => {
    setBusy(node.peer_id);
    setErr('');
    try {
      await api.joinNode(node.peer_id);
      await load();
    } catch (e) {
      setErr(e?.message || '加入失败');
    }
    setBusy('');
  };

  const onLeave = async (node) => {
    setBusy(node.peer_id);
    try {
      await api.leaveNode(node.peer_id);
      await load();
    } catch (e) {
      setErr(e?.message || '移出失败');
    }
    setBusy('');
  };

  const onOpen = (node) => nav(`/peers/${encodeURIComponent(node.peer_id)}`);

  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase();
    return nodes.filter((n) => {
      if (onlyJoined && !n.joined) return false;
      if (!kw) return true;
      return String(n.peer_id).toLowerCase().includes(kw)
        || String(n.node_type || '').toLowerCase().includes(kw);
    });
  }, [nodes, q, onlyJoined]);

  const connectedCount = nodes.filter((n) => n.connected).length;
  const joinedCount = nodes.filter((n) => n.joined).length;

  return (
    <div className="flex flex-1 min-h-0">
      <SideNav />
      <div className="flex-1 min-w-0 flex flex-col overflow-hidden">
        <MobileNav />
        <div className="flex-1 overflow-y-auto p-4 md:p-6">
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <h1 className="text-lg font-semibold text-gray-100">节点市场</h1>
            <span className="text-xs text-gray-500">
              在线 {nodes.filter((n) => n.online).length} · 直连 {connectedCount} · 已加入 {joinedCount}
            </span>
            <div className="ml-auto flex items-center gap-2">
              <input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="按节点 id 筛选"
                className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 placeholder-gray-500 w-40"
              />
              <label className="flex items-center gap-1 text-xs text-gray-400">
                <input type="checkbox" checked={onlyJoined} onChange={(e) => setOnlyJoined(e.target.checked)} />
                只看已加入
              </label>
              <Btn onClick={load} disabled={loading}>{loading ? '加载中…' : '刷新'}</Btn>
            </div>
          </div>

          {/* 本节点卡片：明确告诉用户"我在网络里是谁"，市场里的一切都是相对它而言 */}
          <div className="mb-5 rounded-lg border border-blue-900/50 bg-blue-950/20 p-3">
            <div className="text-[11px] uppercase tracking-wider text-blue-400/80 mb-1">我的节点</div>
            <div className="font-mono text-sm text-gray-100 break-all">
              {self?.peer_id || '（未启用 PeerJS，节点不在网络中）'}
            </div>
            <div className="mt-1 text-xs text-gray-400">
              {self ? (
                <>
                  共享 {self.shares?.collections || 0} 个合集 · {self.shares?.files || 0} 个文件
                  {!self.shares?.collections && !self.shares?.files && (
                    <span className="text-gray-500">
                      　（未开启对外共享，别人看不到你的内容——用 PEERDRIVE_SHARE_ENABLE 开启）
                    </span>
                  )}
                </>
              ) : (
                '启动时设置 PEERDRIVE_PEERJS_ENABLE=true 即可加入网络'
              )}
            </div>
          </div>

          {err && <div className="mb-3 text-xs text-red-400">{err}</div>}

          {loading && !nodes.length ? (
            <div className="py-12 text-center text-sm text-gray-500">加载中…</div>
          ) : filtered.length === 0 ? (
            <div className="rounded-lg border border-gray-800 bg-gray-900/40 px-4 py-12 text-center text-sm text-gray-500">
              <div className="text-3xl mb-2 opacity-60">🏪</div>
              {nodes.length === 0
                ? '当前没有发现其它节点。确认双方都开启了节点级发现（PEERDRIVE_DISCOVER_PRESENCE），或直接加入已知节点 id。'
                : '没有匹配的节点。'}
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {filtered.map((n) => (
                <NodeCard
                  key={n.peer_id}
                  node={n}
                  busy={busy === n.peer_id}
                  onJoin={onJoin}
                  onLeave={onLeave}
                  onOpen={onOpen}
                />
              ))}
            </div>
          )}

          {/* 手动加入：发现服务器不可达/不想等发现时，直接填对方节点 id */}
          <ManualJoin onJoined={load} />

          <div className="mt-6 text-[11px] text-gray-600">
            提示：节点 id 形如 {shortPeer('peerdrive-1a2b3c4d', 12)}。
            「加入」会把对方记在本节点的常驻对端清单里（重启后仍会自动连接）。
          </div>
        </div>
      </div>
    </div>
  );
}

// ManualJoin 手动按 id 加入节点。
// 存在的理由：发现服务器不可达、或双方不在同一发现房间时，用户仍然应该能
// 通过运维给出的 peer id 直接建立关系（BT 里"手动添加 tracker/peer"的等价物）。
function ManualJoin({ onJoined }) {
  const [peer, setPeer] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');

  const submit = async (e) => {
    e.preventDefault();
    const id = peer.trim();
    if (!id) return;
    setBusy(true);
    setMsg('');
    try {
      await api.joinNode(id);
      setMsg(`已加入 ${id}`);
      setPeer('');
      onJoined?.();
    } catch (err) {
      setMsg(err?.message || '加入失败');
    }
    setBusy(false);
  };

  return (
    <form onSubmit={submit} className="mt-6 rounded-lg border border-gray-800 bg-gray-900/40 p-3">
      <div className="text-xs text-gray-400 mb-2">手动加入节点（知道对方 id 时不用等发现）</div>
      <div className="flex gap-2">
        <input
          value={peer}
          onChange={(e) => setPeer(e.target.value)}
          placeholder="peer id，例如 peerdrive-1a2b3c4d"
          className="flex-1 bg-gray-800 border border-gray-700 rounded px-2 py-1.5 text-xs text-gray-200 placeholder-gray-500"
        />
        <Btn tone="primary" disabled={busy || !peer.trim()}>{busy ? '加入中…' : '加入'}</Btn>
      </div>
      {msg && <div className="mt-2 text-[11px] text-gray-400">{msg}</div>}
    </form>
  );
}
