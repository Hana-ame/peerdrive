import { useNavigate } from 'react-router-dom';
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
  const url = api.getAnonFileDownloadUrl(searchHash, entry.path) + '?inline=1';
  const dlUrl = api.getAnonFileDownloadUrl(searchHash, entry.path);
  const filename = (entry.path || '').split('/').pop() || 'file';

  // 嵌套合集
  if (allCollHashes.has(entry.hash)) {
    return <NestedCollectionLink filename={filename} onClick={() => navigate(`/anon/collections/${entry.hash}`)} />;
  }

  const isImage = mime.startsWith('image/') || ['png','jpg','jpeg','gif','webp','svg','bmp','ico'].includes(ext);
  const isText = mime.startsWith('text/') || ['json','js','jsx','ts','tsx','css','html','xml','md','yaml','yml','toml','ini','cfg','conf','sh','bash','py','go','rs','java','c','cpp','h','log','txt'].includes(ext);
  const isPdf = mime === 'application/pdf' || ext === 'pdf';

  if (isImage) return <ImagePreview url={url} downloadUrl={dlUrl} filename={filename} />;
  if (isText) return <TextPreview url={url} downloadUrl={dlUrl} filename={filename} hash={entry.hash} created={collection.created_at} />;
  if (isPdf) return <PdfPreview url={url} downloadUrl={dlUrl} filename={filename} />;

  return <GenericFilePreview mime={mime} filename={filename} size={collection.entries?.[0]?.size || 0} downloadUrl={dlUrl} createdAt={collection.created_at} />;
}
