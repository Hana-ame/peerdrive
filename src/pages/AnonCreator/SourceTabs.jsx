export default function SourceTabs({ active, onChange, onSystemReset }) {
  return (
    <div className="flex bg-gray-800 rounded mx-2 mt-2 shrink-0">
      <button onClick={() => onChange('timeline')} className={`flex-1 px-3 py-2 text-sm rounded ${active === 'timeline' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>🕐 时间线</button>
      <button onClick={() => onChange('registered')} className={`flex-1 px-3 py-2 text-sm rounded ${active === 'registered' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>📁 已注册</button>
      <button onClick={() => { if (onSystemReset) onSystemReset(); onChange('system'); }} className={`flex-1 px-3 py-2 text-sm rounded ${active === 'system' ? 'bg-blue-600 text-white font-medium' : 'text-gray-400 hover:text-white'}`}>🖥️ 本机</button>
    </div>
  );
}
