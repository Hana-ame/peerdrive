import { fileIcon, fmtSize, relTime } from './utils';

export default function GenericFilePreview({ mime, filename, size, downloadUrl, createdAt }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 px-8">
      <span className="text-5xl mb-4">{fileIcon(mime)}</span>
      <h3 className="text-xl font-bold text-gray-200 mb-1">{filename}</h3>
      <p className="text-xs text-gray-500 font-mono mb-2">{fmtSize(size || 0)}</p>
      <a href={downloadUrl} target="_blank" rel="noreferrer"
        className="bg-blue-600 hover:bg-blue-700 text-white px-6 py-2.5 rounded-lg text-sm font-medium mb-3">
        ⬇ 下载文件
      </a>
      <p className="text-xs text-gray-600">{relTime(createdAt)}</p>
    </div>
  );
}
