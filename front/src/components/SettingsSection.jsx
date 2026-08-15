// 设置区域卡片组件：标题/描述/子内容 + 保存按钮
import React, { useState } from 'react';

export default function SettingsSection({ title, description, children, onSave, saveDisabled, id }) {
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    if (!onSave) return;
    setSaving(true);
    try {
      await onSave();
    } finally {
      setSaving(false);
    }
    setSaved(true);
    setTimeout(() => setSaved(false), 2000);
  };

  return (
    <div id={id} className="bg-gray-800 rounded-lg p-3 md:p-5 mb-3 md:mb-5 border border-gray-700">
      <h3 className="text-sm font-bold text-gray-200 mb-1">{title}</h3>
      {description && (
        <p className="text-xs text-gray-500 mb-3 md:mb-4">{description}</p>
      )}
      <div className="space-y-2 md:space-y-3">
        {children}
      </div>
      {onSave && (
        <div className="flex items-center gap-3 mt-3 md:mt-4 pt-3 border-t border-gray-700/50">
          <button
            onClick={handleSave}
            disabled={saveDisabled || saving}
            className="bg-blue-600 hover:bg-blue-500 disabled:opacity-40 px-3 md:px-4 py-1.5 md:py-2 rounded text-xs md:text-sm font-medium transition-colors"
          >
            {saving ? '保存中...' : '保存设置'}
          </button>
          {saved && (
            <span className="text-green-400 text-xs md:text-sm transition-opacity duration-1000">✓ 已保存</span>
          )}
        </div>
      )}
    </div>
  );
}
