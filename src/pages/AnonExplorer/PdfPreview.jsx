export default function PdfPreview({ url, downloadUrl, filename }) {
  return (
    <div className="flex flex-col py-4 px-2 h-full">
      <div className="flex items-center justify-between mb-2 px-2">
        <h3 className="text-sm font-bold text-gray-200 truncate">{filename}</h3>
        <a href={downloadUrl} target="_blank" rel="noreferrer" className="text-xs text-blue-400 hover:underline ml-3 shrink-0">⬇ 下载 PDF</a>
      </div>
      <iframe src={url + '#view=FitH'} className="flex-1 w-full rounded-lg border border-gray-700" title="PDF Preview" />
    </div>
  );
}
