export default function NamePrompt({ onAiName, onSkip, onClose }) {
  return (
    <div className="flex items-center gap-2 px-3 py-1.5 bg-gray-800 border-b border-gray-700 shrink-0">
      <span className="text-xs text-gray-400">合集名称:</span>
      <button onClick={onAiName} className="text-xs bg-purple-700 hover:bg-purple-600 px-2 py-1 rounded">🤖 AI 推荐</button>
      <button onClick={onSkip} className="text-xs bg-gray-600 hover:bg-gray-500 px-2 py-1 rounded">留空</button>
      <button onClick={onClose} className="text-xs text-gray-500 hover:text-white">✕</button>
    </div>
  );
}
