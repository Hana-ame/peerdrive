// 可见性选择器：公开 / 受限 / 私密 三选一
import React from 'react';

const OPTIONS = [
  { value: 'public', label: '公开访问', icon: '🌐', desc: '所有人可见，可 P2P 广播' },
  { value: 'restricted', label: '指定用户', icon: '👥', desc: '仅限选择的用户和群组访问' },
  { value: 'private', label: '仅自己', icon: '🔒', desc: '只有你可以访问' },
];

export default function VisibilityPicker({ value, onChange }) {
  return (
    <div className="flex gap-0.5">
      {OPTIONS.map(o => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={`flex-1 px-2 py-1.5 rounded text-xs text-center transition-colors ${
            value === o.value
              ? 'bg-blue-600 text-white'
              : 'bg-gray-800 text-gray-400 hover:text-white hover:bg-gray-700'
          }`}
          title={o.desc}
        >
          <span className="mr-1">{o.icon}</span>
          {o.label}
        </button>
      ))}
    </div>
  );
}
