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
import * as ws from '../../../platform/transport-ws';
import { buildTree, descend, basename, entrySha, previewSha, fetchableSource } from '../../../platform/shared/collectionTree';
import { fmtBytes } from '../../../platform/shared/format';
import { kindOf, mimeOf } from '../../../platform/shared/mime';
import { previewBlobCache, previewLimit } from '../../../lib/cache';
import FilePreviewModal from '../../../components/netdisk/FilePreviewModal';
import { getNodeSession } from '../../../lib/nodeSession';

// usePreviewCache：按 preview sha 缓存 blob URL + 去重并发请求。
// 升级为模块级 previewBlobCache（带 32MB 内存预算与 LRU revoke 回收）：
// 跨组件导航与同一合集多次浏览均可命中缓存，无需重复下载。
function usePreviewCache() {
  const inflight = useRef(new Map()); // preview sha → Promise<blob URL>（并发去重）

  return useCallback((sha) => {
    if (previewBlobCache.has(sha)) return Promise.resolve(previewBlobCache.get(sha));
    if (inflight.current.has(sha)) return inflight.current.get(sha);

    const fetchBytes = async () => {
      const session = getNodeSession();
      if (session?.client && ws.getStatus() !== 'open') {
        const chunks = [];
        for await (const chunk of session.client.stream(sha)) {
          chunks.push(chunk);
        }
        return new Blob(chunks);
      }
      try {
        const bytes = await ws.download(sha);
        return new Blob([bytes]);
      } catch (e) {
        if (session?.client) {
          const chunks = [];
          for await (const chunk of session.client.stream(sha)) {
            chunks.push(chunk);
          }
          return new Blob(chunks);
        }
        throw e;
      }
    };

    const p = previewLimit(fetchBytes)
      .then((blob) => {
        const url = URL.createObjectURL(blob);
        previewBlobCache.set(sha, url, blob.size || 0);
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
// 增加 IntersectionObserver 按需加载：只有当缩略图滚动进入视口时才触发 loadPreview。
function PreviewThumb({ sha, loadPreview, size = 40 }) {
  const [state, setState] = useState({ status: sha ? 'idle' : 'none', url: '' });
  const [isVisible, setIsVisible] = useState(false);
  const elRef = useRef(null);

  useEffect(() => {
    if (!sha) { setState({ status: 'none', url: '' }); return; }
    if (!('IntersectionObserver' in window)) {
      setIsVisible(true);
      return;
    }
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) {
        setIsVisible(true);
        observer.disconnect();
      }
    }, { rootMargin: '100px' });

    if (elRef.current) observer.observe(elRef.current);
    return () => observer.disconnect();
  }, [sha]);

  useEffect(() => {
    if (!sha || !isVisible) return;
    let alive = true;
    setState({ status: 'loading', url: '' });
    loadPreview(sha)
      .then((url) => { if (alive) setState({ status: 'ok', url }); })
      .catch(() => { if (alive) setState({ status: 'error', url: '' }); });
    return () => { alive = false; };
  }, [sha, isVisible, loadPreview]);

  const box = { width: size, height: size };
  if (state.status === 'ok') {
    return <img ref={elRef} src={state.url} alt="preview" style={box} className="object-cover rounded-lg bg-white/[0.04] shrink-0" />;
  }
  // 占位：无 preview 用 📄，取数失败用 ⚠️，加载中用转圈，未进视口 idle 用 📄
  const icon = state.status === 'error' ? '⚠️' : state.status === 'loading' ? '⏳' : '📄';
  return (
    <div ref={elRef} style={box} className="flex items-center justify-center rounded-lg bg-white/[0.05] text-lg shrink-0" aria-label="no preview">
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

  // 访问：可预览（显式 preview sha 或音视频/文本媒体）→ 弹预览弹窗；不可预览 → 退化为下载（见 issue 测试契约）
  const isPreviewable = (node) => {
    if (previewSha(node.entry)) return true;
    const name = node.entry?.name || basename(node.entry?.path) || node.name;
    const kind = kindOf(name, node.entry?.mime);
    return kind === 'video' || kind === 'audio' || kind === 'text';
  };

  const openFile = (node) => {
    if (isPreviewable(node)) {
      setModal(node);
      return;
    }
    downloadFile(node);
  };

  const triggerBrowserDownload = (url, fileName) => {
    const a = document.createElement('a');
    a.href = url;
    a.download = fileName;
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  };

  const downloadFile = async (node) => {
    const src = fetchableSource(node.entry);
    if (!src) return;
    const fileName = node.entry?.name || basename(node.entry?.path) || node.name;
    if (src.type === 'sha') {
      const session = getNodeSession();
      if (session?.client && ws.getStatus() !== 'open') {
        try {
          await session.client.saveAs(src.value, fileName);
          return;
        } catch (e) {
          fail(e);
          return;
        }
      }
      try {
        await ws.downloadToFile(src.value, fileName);
        return;
      } catch (e) {
        if (session?.client) {
          try {
            await session.client.saveAs(src.value, fileName);
            return;
          } catch { /* fall through to fallbackUrl */ }
        }
        // 若 sha 下载失败但条目包含备选 URL/ECHURL/private，尝试回退下载
        const s = node.entry?.source;
        const fallbackUrl = s?.url || s?.['ech-url'] || s?.private?.url;
        if (!fallbackUrl) {
          fail(e);
          return;
        }
        triggerBrowserDownload(fallbackUrl, fileName);
        return;
      }
    }
    // 远程 URL / ECH-URL 备选源
    if (src.value) {
      triggerBrowserDownload(src.value, fileName);
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
              // 文件行：缩略图(可点访问) + 名称/大小/元数据 + Open/Download
              <li key={`f-${node.name}-${i}`} className="flex items-center gap-3 px-3 py-2 hover:bg-white/[0.02]">
                <button
                  onClick={() => openFile(node)}
                  className="shrink-0 rounded-lg focus:outline-none"
                  title={isPreviewable(node) ? 'View preview' : 'No preview — download file'}>
                  <PreviewThumb sha={previewSha(node.entry)} loadPreview={loadPreview} size={40} />
                </button>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 truncate text-sm text-gray-200">
                    <span className="truncate">{node.entry?.name || node.name}</span>
                    {node.entry?.source && (
                      <span className="px-1 py-0.2 rounded text-[10px] bg-blue-500/10 text-blue-400 border border-blue-500/20 uppercase shrink-0 font-mono">
                        {fetchableSource(node.entry)?.type || 'src'}
                      </span>
                    )}
                  </div>
                  <div className="text-[11px] text-gray-600">
                    {fmtBytes(node.entry.size)}
                    {node.entry.mime ? ` · ${node.entry.mime}` : ''}
                    {node.entry.created_at ? ` · ${new Date(node.entry.created_at * 1000).toLocaleDateString()}` : ''}
                  </div>
                </div>
                <button
                  onClick={() => openFile(node)}
                  className="text-[11px] px-2 py-1 rounded bg-white/[0.05] hover:bg-white/[0.1] text-gray-300 shrink-0">
                  {isPreviewable(node) ? 'Preview' : 'Open'}
                </button>
                <button
                  onClick={() => downloadFile(node)}
                  disabled={!fetchableSource(node.entry)}
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

      {/* 媒体预览弹窗：MIME 分流预览（图片、视频、音频、PDF、代码/文本），附真实文件下载 */}
      {modal && (() => {
        const modalName = basename(modal.entry.path) || modal.name;
        const modalKind = kindOf(modalName, modal.entry.mime);
        const modalHash = (modalKind === 'video' || modalKind === 'audio')
          ? (entrySha(modal.entry) || previewSha(modal.entry))
          : (previewSha(modal.entry) || entrySha(modal.entry));
        return (
          <FilePreviewModal
            file={{
              hash: modalHash,
              filename: modalName,
              size: modal.entry.size,
              mime_type: modal.entry.mime,
            }}
            fetchBlob={async (sha) => {
              const effectiveType = modal.entry.mime || mimeOf(modalName, '') || 'application/octet-stream';
              const session = getNodeSession();
              if (session?.client && ws.getStatus() !== 'open') {
                const chunks = [];
                for await (const chunk of session.client.stream(sha)) {
                  chunks.push(chunk);
                }
                return new Blob(chunks, { type: effectiveType });
              }
              try {
                const u8 = await ws.download(sha);
                return new Blob([u8], { type: effectiveType });
              } catch (e) {
                if (session?.client) {
                  const chunks = [];
                  for await (const chunk of session.client.stream(sha)) {
                    chunks.push(chunk);
                  }
                  return new Blob(chunks, { type: effectiveType });
                }
                throw e;
              }
            }}
            onClose={closeModal}
            onDownload={() => downloadFile(modal)}
          />
        );
      })()}
    </div>
  );

}
