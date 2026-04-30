export default function NestedCollectionLink({ filename, onClick }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 px-8 cursor-pointer" onClick={onClick}>
      <span className="text-5xl mb-4">📦</span>
      <h3 className="text-xl font-bold text-purple-300 mb-1">{filename}</h3>
      <p className="text-xs text-purple-500 font-mono mb-6">嵌套合集 — 点击打开</p>
    </div>
  );
}
