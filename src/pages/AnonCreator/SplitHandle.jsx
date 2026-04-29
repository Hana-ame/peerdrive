export default function SplitHandle({ onMouseDown }) {
  return (
    <div className="w-1 bg-gray-700 hover:bg-blue-600 cursor-col-resize shrink-0 relative group" onMouseDown={onMouseDown}>
      <div className="absolute inset-y-0 -left-1 -right-1" />
    </div>
  );
}
