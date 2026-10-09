// CollectionBrowser.jsx — folder-style browser for a collection manifest.
//
// 为什么独立成组件：CollectionView 页面（按 sha 加载 / 粘贴 JSON 两条入口）只负责
// 拿到 collection 对象，展示逻辑（面包屑导航、preview 缩略图、按 sha 取数/下载）
// 全在这里，页面与测试都能直接喂给它一个已解析的 JSON。
//
// 交互设计：
//   - 条目按 path 段构建目录树（见 lib/collectionTree.js），当前目录是"进入"式
//     导航（点文件夹进入子目录），面包屑可随时跳回任意祖先；
//   - 叶子文件条目：有 preview sha → 按 sha 取预览文件（通常图片）渲染缩略图，
//     点缩略图/Open 弹大图（访问）；无 preview → 占位图标，Open 退化为下载；
//   - Download 按钮走 ws.downloadToFile(sha, basename)，与 Drive 页同一取数通道。
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import * as ws from '../../../ws';
import { buildTree, descend, basename, entrySha, previewSha } from '../lib/collectionTree';
import { fmtBytes } from '../../../lib/format';
import { previewBlobCache } from '../../../lib/cache';

// usePreviewCache：按 preview sha 缓存 blob URL + 去重并发请求。
// 升级为模块级 previewBlobCache（带 32MB 内存预算与 LRU revoke 回收）：
// 跨组件导航与同一合集多次浏览均可命中缓存，无需重复下载。
function usePreviewCache() {
  const inflight = useRef(new Map()); // preview sha → Promise<blob URL>（并发去重）

  return useCallback((sha) => {
    if (previewBlobCache.has(sha)) return Promise.resolve(previewBlobCache.get(sha));
    if (inflight.current.has(sha)) return inflight.current.get(sha);
    const p = ws.download(sha)
      .then((bytes) => {
        // 取数通道与文件下载同一条（ws.download → req 帧），预览文件也是
        // sha-文件系统里的内容寻址对象，字节回来直接包成 blob。
        const url = URL.createObjectURL(new Blob([bytes]));
        previewBlobCache.set(sha, url, bytes.byteLength || 0);
        inflight.current.delete(sha);
        return url;
      })
      .catch((err) => {
        inflight.current.delete(sha);
        throw err;
      });
    inflight.current.set(sha, p);
    return p;
  }, []);
}

// PreviewThumb：预览缩略图。status 四态 none(无 preview)/loading/ok/error，
// 无 preview 与取数失败都落到占位（前者无数据、后者避免红叉误导——失败可能只是
// 该节点本地没有这份预览文件，不代表文件本体不可用）。
function PreviewThumb({ sha, loadPreview, size = 40 }) {
  const [state, setState] = useState({ status: sha ? 'loading' : 'none', url: '' });
  useEffect(() => {
    if (!sha) { setState({ status: 'none', url: '' }); return; }
    let alive = true;
    setState({ status: 'loading', url: '' });
    loadPreview(sha)
      .then((url) => { if (alive) setState({ status: 'ok', url }); })
      .catch(() => { if (alive) setState({ status: 'error', url: '' }); });
    return () => { alive = false; };
  }, [sha, loadPreview]);

  const box = { width: size, height: size };
  if (state.status === 'ok') {
    return <img src={state.url} alt="preview" style={box} className="object-cover rounded-lg bg-white/[0.04] shrink-0" />;
  }
  // 占位：无 preview 用 📄，取数失败用 ⚠️，加载中用转圈
  const icon = state.status === 'error' ? '⚠️' : state.status === 'loading' ? '⏳' : '📄';
  return (
    <div style={box} className="flex items-center justify-center rounded-lg bg-white/[0.05] text-lg shrink-0" aria-label="no preview">
      {icon}
    </div>
  );
}

export default function CollectionBrowser({ collection, onError }) {
  const loadPreview = usePreviewCache();
  const [currentPath, setCurrentPath] = useState([]); // 当前目录段序列（[] = 根）
  const [modal, setModal] = useState(null); // 大图弹窗的文件节点
  const [err, setErr] = useState('');

  // collection 换了 → 回到根目录，避免残留旧树的面包屑
  const root = useMemo(() => buildTree(collection?.entries || []), [collection]);
  useEffect(() => { setCurrentPath([]); }, [root]);

  const currentNode = useMemo(() => descend(root, currentPath), [root, currentPath]);

  const fail = (e) => {
    const msg = e?.message || String(e);
    setErr(msg);
    onError?.(msg);
  };

  // 访问：有 preview → 弹大图；无 preview → 退化为下载
  const openFile = (node) => {
    if (previewSha(node.entry)) { setModal(node); return; }
    downloadFile(node);
  };

  const downloadFile = async (node) => {
    const sha = entrySha(node.entry);
    if (!sha) return; // 无取数键的脏条目按钮置灰，见 collectionTree.normalizeEntry
    try {
      await ws.downloadToFile(sha, node.name);
    } catch (e) {
      fail(e);
    }
  };

  const closeModal = () => setModal(null);

  const crumb = (active) =>
    `px-2 py-0.5 rounded transition-colors ${active ? 'bg-white/[0.06] text-gray-200' : 'text-gray-500 hover:text-white'}`;

  return (
    <div>
      {err && (
        <div className="mb-3 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">{err}</div>
      )}

      {/* 面包屑：根 + 每段可点，当前段高亮 */}
      <nav className="flex items-center gap-0.5 text-xs mb-3 flex-wrap" aria-label="breadcrumb">
        <button onClick={() => setCurrentPath([])} className={crumb(currentPath.length === 0)}>Collection</button>
        {currentPath.map((seg, i) => (
          <React.Fragment key={`${seg}-${i}`}>
            <span className="text-gray-600 select-none">/</span>
            <button
              onClick={() => setCurrentPath(currentPath.slice(0, i + 1))}
              className={crumb(i === currentPath.length - 1)}>
              {seg}
            </button>
          </React.Fragment>
        ))}
      </nav>

      {currentNode && currentNode.children.length > 0 ? (
        <ul className="card-surface divide-y divide-white/[0.04] overflow-hidden">
          {currentNode.children.map((node, i) => (
            node.type === 'dir' ? (
              // 目录行：整行可点进入
              <li key={`d-${node.name}`}>
                <button
                  onClick={() => setCurrentPath([...currentPath, node.name])}
                  className="w-full flex items-center gap-3 px-3 py-2 text-left hover:bg-white/[0.03]">
                  <span className="text-lg shrink-0">📁</span>
                  <span className="flex-1 truncate text-sm text-gray-200">{node.name}</span>
                  <span className="text-[11px] text-gray-600 shrink-0">{node.children.length} items</span>
                  <span className="text-gray-600 shrink-0">›</span>
                </button>
              </li>
            ) : (
              // 文件行：缩略图(可点访问) + 名称/大小 + Open/Download
              <li key={`f-${node.name}-${i}`} className="flex items-center gap-3 px-3 py-2 hover:bg-white/[0.02]">
                <button
                  onClick={() => openFile(node)}
                  className="shrink-0 rounded-lg focus:outline-none"
                  title={previewSha(node.entry) ? 'View preview' : 'No preview — download file'}>
                  <PreviewThumb sha={previewSha(node.entry)} loadPreview={loadPreview} size={40} />
                </button>
                <div className="flex-1 min-w-0">
                  <div className="truncate text-sm text-gray-200">{node.name}</div>
                  <div className="text-[11px] text-gray-600">
                    {fmtBytes(node.entry.size)}
                    {node.entry.mime ? ` · ${node.entry.mime}` : ''}
                  </div>
                </div>
                <button
                  onClick={() => openFile(node)}
                  className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 shrink-0">
                  {previewSha(node.entry) ? 'Preview' : 'Open'}
                </button>
                <button
                  onClick={() => downloadFile(node)}
                  disabled={!entrySha(node.entry)}
                  className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 shrink-0 disabled:opacity-40 disabled:cursor-not-allowed">
                  Download
                </button>
              </li>
            )
          ))}
        </ul>
      ) : (
        <div className="text-center py-14 text-gray-500 border-2 border-dashed border-white/10 rounded-card">
          <p className="text-sm">This folder is empty</p>
          <p className="text-xs text-gray-600 mt-1">The collection JSON has no entries under this path.</p>
        </div>
      )}

      {/* 大图弹窗：预览文件访问（按 preview sha 取数），附真实文件下载 */}
      {modal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-6" onClick={closeModal}>
          <div
            className="card-surface max-w-3xl w-full p-5 rounded-xl max-h-full overflow-y-auto"
            onClick={(e) => e.stopPropagation()}>
            <div className="flex items-start justify-between gap-4 mb-3">
              <div className="min-w-0">
                <div className="text-sm font-medium text-gray-200 truncate">{basename(modal.entry.path)}</div>
                <div className="text-[11px] text-gray-500 mt-0.5 font-mono break-all">
                  sha: {entrySha(modal.entry).slice(0, 16)}…
                  {modal.entry.size != null ? ` · ${fmtBytes(modal.entry.size)}` : ''}
                </div>
              </div>
              <button onClick={closeModal} className="text-gray-500 hover:text-white text-lg leading-none shrink-0">✕</button>
            </div>
            <div className="flex justify-center bg-white/[0.03] rounded-lg p-2">
              <PreviewThumb sha={previewSha(modal.entry)} loadPreview={loadPreview} size={420} />
            </div>
            <div className="flex gap-2 mt-3 justify-end">
              <button onClick={() => downloadFile(modal)} className="btn-brand">Download file</button>
              <button onClick={closeModal} className="btn-ghost">Close</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
