// pages/Transfers/index.jsx：「传输任务」——跨节点保存的进度与取消。
//
// 形态参考 qBittorrent/Cloudreve 的下载页：任务列表 + 进度 + 取消 + 清理。
// 轮询策略：有 running 任务时 1s 轮询（进度要跟手），全部结束就停（不空转）。
import React, { useCallback, useEffect, useRef, useState } from 'react';
import * as api from '../../api';
import SideNav, { MobileNav } from '../../components/netdisk/SideNav';
import TransferRow from '../../components/netdisk/TransferRow';
import { Btn } from '../../components/netdisk/FileTable';

export default function Transfers() {
  const [jobs, setJobs] = useState([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const timer = useRef(null);

  const load = useCallback(async () => {
    try {
      const res = await api.getPullJobs();
      setJobs(Array.isArray(res?.jobs) ? res.jobs : []);
      setErr('');
    } catch (e) {
      setErr(e?.message || '任务列表加载失败');
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  // 轮询：只在有进行中的任务时开；没有就停（避免长期空转打后端）。
  useEffect(() => {
    const hasRunning = jobs.some((j) => j.status === 'running');
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    if (!hasRunning) return undefined;
    timer.current = setTimeout(load, 1000);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [jobs, load]);

  const onCancel = async (job) => {
    try {
      await api.cancelPull(job.id);
      await load();
    } catch (e) {
      setErr(e?.message || '取消失败');
    }
  };

  const running = jobs.filter((j) => j.status === 'running').length;
  const done = jobs.filter((j) => j.status === 'done').length;
  const failed = jobs.filter((j) => j.status === 'failed').length;

  // 分组展示：进行中 / 已结束。已结束的留在页面（用户要回看"存哪了"），
  // 只是排到下面——后端任务表本身有上限（200）会自然清理最老的。
  const active = jobs.filter((j) => j.status === 'running');
  const finished = jobs.filter((j) => j.status !== 'running');

  return (
    <div className="flex flex-1 min-h-0">
      <SideNav />
      <div className="flex-1 min-w-0 flex flex-col overflow-hidden">
        <MobileNav />
        <div className="flex-1 overflow-y-auto p-4 md:p-6">
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <h1 className="text-lg font-semibold text-gray-100">传输任务</h1>
            <span className="text-xs text-gray-500">
              进行中 {running} · 已完成 {done} · 失败 {failed}
            </span>
            <div className="ml-auto flex gap-2">
              <Btn onClick={load} disabled={loading}>{loading ? '加载中…' : '刷新'}</Btn>
            </div>
          </div>

          {err && <div className="mb-3 text-xs text-red-400">{err}</div>}

          {!jobs.length && !loading ? (
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.06] px-4 py-12 text-center text-sm text-gray-500">
              <div className="text-3xl mb-2 opacity-60">⬇</div>
              还没有传输任务。去「我的节点」选中对方的文件点保存，任务会出现在这里。
            </div>
          ) : (
            <>
              {active.length > 0 && (
                <>
                  <h2 className="mb-2 text-sm font-semibold text-gray-200">进行中</h2>
                  <div className="flex flex-col gap-2 mb-6">
                    {active.map((j) => <TransferRow key={j.id} job={j} onCancel={onCancel} />)}
                  </div>
                </>
              )}
              {finished.length > 0 && (
                <>
                  <h2 className="mb-2 text-sm font-semibold text-gray-200">已结束</h2>
                  <div className="flex flex-col gap-2">
                    {finished.map((j) => <TransferRow key={j.id} job={j} onCancel={onCancel} />)}
                  </div>
                </>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
