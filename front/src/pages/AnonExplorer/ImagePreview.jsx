export default function ImagePreview({ url, downloadUrl, filename, onDownload }) {
  return (
    <div className="flex flex-col items-center py-8 px-4">
      <h3 className="text-sm font-bold text-gray-200 mb-2">{filename}</h3>
      {url ? (
        <img src={url} alt={filename} className="max-w-full max-h-[70vh] object-contain rounded-lg border border-gray-700" />
      ) : (
        <div className="text-gray-600 text-sm py-8">预览加载中...</div>
      )}
      <a href="#" onClick={e => { e.preventDefault(); if (onDownload) onDownload(); }}
        className="text-xs text-blue-400 hover:underline mt-3">⬇ 下载原图</a>
    </div>
  );
}
