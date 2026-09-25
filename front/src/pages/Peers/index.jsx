// pages/Peers/index.jsx：「我的节点」——已加入的节点及其直连状态。
//
// 与「节点市场」的分工：市场是"可以发现谁"（含未加入的），这里是"我已经
// 订阅了谁"。之所以分成两页：市场条目会随发现在线状态频繁变化，而我的节点
// 是稳定清单，用户需要的是一个不会被发现服务器波动干扰的固定入口。
import React, { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import * as api from '../../api';
import SideNav, { MobileNav } from '../../components/netdisk/SideNav';
import NodeCard from '../../components/netdisk/NodeCard';
import { Btn } from '../../components/netdisk/FileTable';

export default function Peers() {
  const nav = useNavigate();
  const [nodes, setNodes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setErr('');
    try {
      const res = await api.getJoinedNodes();
      setNodes(Array.isArray(res?.nodes) ? res.nodes : []);
    } catch (e) {
      setNodes([]);
      setErr(e?.message || '已加入节点加载失败');
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  // 自动刷新：直连状态（connected）只在建立连接后才会变 true，用户刚加入
  // 时希望看到它"点亮"，不必手动刷新。有数据时每 8s 静默刷新一次。
  useEffect(() => {
    const t = setInterval(() => { load(); }, 8000);
    return () => clearInterval(t);
  }, [load]);

  const onLeave = async (node) => {
    if (!window.confirm(`移出 ${node.peer_id}？（不会删除已保存的文件）`)) return;
    setBusy(node.peer_id);
    try {
      await api.leaveNode(node.peer_id);
      await load();
    } catch (e) {
      setErr(e?.message || '移出失败');
    }
    setBusy('');
  };

  return (
    <div className="flex flex-1 min-h-0">
      <SideNav />
      <div className="flex-1 min-w-0 flex flex-col overflow-hidden">
        <MobileNav />
        <div className="flex-1 overflow-y-auto p-4 md:p-6">
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <h1 className="text-lg font-semibold text-gray-100">我的节点</h1>
            <span className="text-xs text-gray-500">
              {nodes.length} 个已加入 · {nodes.filter((n) => n.connected).length} 个直连中
            </span>
            <div className="ml-auto flex gap-2">
              <Btn onClick={() => nav('/market')}>去市场添加</Btn>
              <Btn onClick={load} disabled={loading}>{loading ? '加载中…' : '刷新'}</Btn>
            </div>
          </div>

          {err && <div className="mb-3 text-xs text-red-400">{err}</div>}

          {loading && !nodes.length ? (
            <div className="py-12 text-center text-sm text-gray-500">加载中…</div>
          ) : nodes.length === 0 ? (
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.06] px-4 py-12 text-center text-sm text-gray-500">
              <div className="text-3xl mb-2 opacity-60">🔗</div>
              还没有加入任何节点。去「节点市场」找一个节点加入，就能看到对方的文件链接。
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {nodes.map((n) => (
                <NodeCard
                  key={n.peer_id}
                  node={n}
                  busy={busy === n.peer_id}
                  onLeave={onLeave}
                  onOpen={(node) => nav(`/peers/${encodeURIComponent(node.peer_id)}`)}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
