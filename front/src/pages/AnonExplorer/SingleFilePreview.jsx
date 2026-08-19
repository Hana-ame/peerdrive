import { useNavigate } from 'react-router-dom';
import { useState, useEffect, useRef } from 'react';
import * as api from '../../api';
import NestedCollectionLink from './NestedCollectionLink';
import ImagePreview from './ImagePreview';
import TextPreview from './TextPreview';
import PdfPreview from './PdfPreview';
import GenericFilePreview from './GenericFilePreview';

export default function SingleFilePreview({ entry, searchHash, collection, allCollHashes }) {
  const navigate = useNavigate();
  // 后端 AnonCollectionEntry JSON 不返回顶层 mime_type/size，MIME 在 providers[].mime_type 里
  const mime = entry.mime_type || entry.providers?.[0]?.mime_type || '';
  const ext = (entry.path || '').split('.').pop()?.toLowerCase();
  const filename = (entry.path || '').split('/').pop() || 'file';
  // 预览 blob URL：WS 拉取集合文件 → objectURL（旧 getAnonFileDownloadUrl HTTP 是 legacy）
  const [previewUrl, setPreviewUrl] = useState(null);
  const [dlUrl, setDlUrl] = useState(null);
  // 预览用 objectURL 必须随组件卸载 revoke，否则连续浏览多个单文件合集时
  // Blob 内存只增不减（发现背景：再 review 2026-08——原实现只管 cancelled
  // 不管 revoke，AnonExplorer 单文件预览会泄漏 objectURL）。
  const objectUrlRef = useRef(null);

  // URL-only entry：没有 sha256 provider 时后端 WS 拉取只会得到 302 跳转 HTML，
  // 预览/下载都应当直接打开外部链接，不走 WS（发现背景：再 review 2026-08）。
  const shaHash = entry.hash || entry.providers?.find(p => p.type === 'sha256')?.value;
  const urlProvider = entry.providers?.find(p => p.type === 'url');

  useEffect(() => {
    if (!shaHash) return; // URL-only 或空 provider：不发起无效的 WS 拉取
    let cancelled = false;
    const load = async () => {
      try {
        const buf = await api.downloadAnonFile(searchHash, entry.path);
        if (cancelled) return;
        if (objectUrlRef.current) URL.revokeObjectURL(objectUrlRef.current);
        const blob = new Blob([buf], mime ? { type: mime } : undefined);
        const u = URL.createObjectURL(blob);
        objectUrlRef.current = u;
        setPreviewUrl(u);
        setDlUrl(u);
      } catch {
        // 预览失败静默（下载按钮仍可点，报错在按钮处）
      }
    };
    load();
    return () => {
      cancelled = true;
      if (objectUrlRef.current) URL.revokeObjectURL(objectUrlRef.current);
      objectUrlRef.current = null;
    };
  }, [searchHash, entry.path, mime]);

  // 嵌套合集
  if (allCollHashes.has(entry.hash)) {
    return <NestedCollectionLink filename={filename} onClick={() => navigate(`/anon/collections/${entry.hash}`)} />;
  }

  if (!shaHash && urlProvider?.value) {
    return (
      <div className="flex items-center justify-center min-h-[200px] p-8 text-center">
        <div>
          <div className="text-4xl mb-4">🔗</div>
          <p className="text-sm text-gray-300 mb-2">此条目是外部链接，没有本地内容可预览</p>
          <a href={urlProvider.value} target="_blank" rel="noreferrer"
            className="inline-flex items-center gap-2 bg-purple-600 hover:bg-purple-700 text-white px-4 py-2 rounded-lg text-sm font-medium transition-colors">
            打开外部链接 ↗
          </a>
        </div>
      </div>
    );
  }

  const isImage = mime.startsWith('image/') || ['png','jpg','jpeg','gif','webp','svg','bmp','ico'].includes(ext);
  const isText = mime.startsWith('text/') || ['json','js','jsx','ts','tsx','css','html','xml','md','yaml','yml','toml','ini','cfg','conf','sh','bash','py','go','rs','java','c','cpp','h','log','txt'].includes(ext);
  const isPdf = mime === 'application/pdf' || ext === 'pdf';

  const download = async () => {
    try {
      const buf = await api.downloadAnonFile(searchHash, entry.path);
      const blob = new Blob([buf], mime ? { type: mime } : undefined);
      const u = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = u;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(() => URL.revokeObjectURL(u), 5000);
    } catch (err) {
      alert('下载失败: ' + err.message);
    }
  };

  if (isImage) return <ImagePreview url={previewUrl} downloadUrl={dlUrl} filename={filename} onDownload={download} />;
  if (isText) return <TextPreview buf={null} onLoad={api.downloadAnonFile(searchHash, entry.path)} downloadUrl={dlUrl} filename={filename} hash={entry.hash} created={collection.created_at} onDownload={download} />;
  if (isPdf) return <PdfPreview url={previewUrl} downloadUrl={dlUrl} filename={filename} onDownload={download} />;

  return <GenericFilePreview mime={mime} filename={filename} size={collection.entries?.[0]?.size || 0} downloadUrl={dlUrl} createdAt={collection.created_at} onDownload={download} />;
}
