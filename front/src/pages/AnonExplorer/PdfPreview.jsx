export default function PdfPreview({ url, downloadUrl, filename, onDownload }) {
  return (
    <div className="flex flex-col py-4 px-2 h-full">
      <div className="flex items-center justify-between mb-2 px-2">
        <h3 className="text-sm font-bold text-gray-200 truncate">{filename}</h3>
        <a href="#" onClick={e => { e.preventDefault(); if (onDownload) onDownload(); }}
          className="text-xs text-blue-400 hover:underline ml-3 shrink-0">⬇ 下载 PDF</a>
      </div>
      {url ? (
        <iframe src={url + '#view=FitH'} className="flex-1 w-full rounded-lg border border-gray-700" title="PDF Preview" />
      ) : (
        <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">预览加载中...</div>
      )}
    </div>
  );
}
