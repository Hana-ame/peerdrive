export default function Toast({ message, isError }) {
  if (!message) return null;
  return (
    <div className={`px-4 py-1.5 text-xs shrink-0 ${isError ? 'text-red-400 bg-red-500/10' : 'text-green-400 bg-green-500/10'}`}>
      {message}
    </div>
  );
}
