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
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../../api';
import SideNav, { MobileNav } from '../../components/netdisk/SideNav';
import FileTable, { Btn } from '../../components/netdisk/FileTable';
import { formatSize } from '../../components/netdisk/format';

export default function Drive() {
  const [files, setFiles] = useState([]);
  const [colls, setColls] = useState([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState('');
  const [notice, setNotice] = useState('');
  const fileInput = useRef(null);

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

          {notice && <div className="mb-3 text-xs text-green-400">{notice}</div>}
          {err && <div className="mb-3 text-xs text-red-400">{err}</div>}

          <div className="rounded-lg border border-gray-800 bg-gray-900/40">
            <FileTable
              rows={rows}
              loading={loading}
              empty="还没有文件。可以在「节点市场」加入别人的节点，把对方共享的文件保存过来。"
              actions={(row) => (
                <>
                  <Btn onClick={() => onPreview(row)} disabled={busy === row.hash}>预览</Btn>
                  <Btn onClick={() => onDownload(row)} disabled={busy === row.hash}>下载</Btn>
                  <Btn tone="danger" onClick={() => onDelete(row)} disabled={busy === row.hash}>删除</Btn>
                </>
              )}
            />
          </div>

          <h2 className="mt-8 mb-3 text-sm font-semibold text-gray-200">
            我的合集 <span className="text-xs font-normal text-gray-500">（打包好的文件链接，别人可经 share 帧看到）</span>
          </h2>
          {colls.length === 0 ? (
            <div className="rounded-lg border border-gray-800 bg-gray-900/40 px-4 py-8 text-center text-sm text-gray-500">
              还没有合集。在「创建合集」里把文件打成一个包，就能整包分享给别人。
            </div>
          ) : (
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
              {colls.map((c) => (
                <Link
                  key={c.hash}
                  to={`/c/${c.hash}`}
                  className="rounded-lg border border-gray-800 bg-gray-900/50 p-3 hover:border-gray-700 hover:bg-gray-800/50 transition-colors"
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
