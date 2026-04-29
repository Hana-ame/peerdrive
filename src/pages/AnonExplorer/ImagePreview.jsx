export default function ImagePreview({ url, downloadUrl, filename }) {
  return (
    <div className="flex flex-col items-center py-8 px-4">
      <h3 className="text-sm font-bold text-gray-200 mb-2">{filename}</h3>
      <img src={url} alt={filename} className="max-w-full max-h-[70vh] object-contain rounded-lg border border-gray-700" />
      <a href={downloadUrl} target="_blank" rel="noreferrer" className="text-xs text-blue-400 hover:underline mt-3">⬇ 下载原图</a>
    </div>
  );
}
