export default function NamePrompt({ onSkip, onClose }) {
  return (
    <div className="flex items-center gap-2 px-3 py-1.5 bg-white/[0.06] border-b border-white/[0.06] shrink-0">
      <span className="text-xs text-gray-400">合集名称:</span>
      <div className="flex-1" />
      <button onClick={onSkip} className="bg-white/[0.1] hover:bg-white/[0.16] text-sm px-3 py-1.5 rounded font-medium">留空</button>
      <button onClick={onClose} className="text-xs text-gray-500 hover:text-white px-1">✕</button>
    </div>
  );
}
