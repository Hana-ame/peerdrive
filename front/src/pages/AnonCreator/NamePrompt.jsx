export default function NamePrompt({ onSkip, onClose }) {
  return (
    <div className="flex items-center gap-2 px-3 py-1.5 bg-gray-800 border-b border-gray-700 shrink-0">
      <span className="text-xs text-gray-400">合集名称:</span>
      <div className="flex-1" />
      <button onClick={onSkip} className="bg-gray-600 hover:bg-gray-500 text-sm px-3 py-1.5 rounded font-medium">留空</button>
      <button onClick={onClose} className="text-xs text-gray-500 hover:text-white px-1">✕</button>
    </div>
  );
}
