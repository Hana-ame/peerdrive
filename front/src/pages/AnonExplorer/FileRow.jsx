import * as api from '../../api';
import { fileIcon, relTime } from './utils';

export default function FileRow({ file, isNestedColl, searchHash, collection, onNestedCollClick }) {
  if (isNestedColl) {
    return (
      <div onClick={() => onNestedCollClick(file.hash)}
        className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
        <span className="text-xl">📦</span>
        <span className="text-purple-300 font-mono truncate flex-1">{file.path.split('/').pop()}</span>
        <span className="text-purple-500 text-xs">合集 →</span>
      </div>
    );
  }
  const url = api.getAnonFileDownloadUrl(searchHash, file.path);
  return (
    <a key={file.path} href={url} target="_blank" rel="noreferrer"
      className="flex items-center gap-3 px-5 py-3 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm block">
      <span className="text-xl">{fileIcon(file.mime_type, file.path)}</span>
      <span className="text-blue-300 font-mono truncate flex-1">{file.path.split('/').pop()}</span>
      <span className="text-gray-600 text-xs">{relTime(collection.created_at)}</span>
    </a>
  );
}
