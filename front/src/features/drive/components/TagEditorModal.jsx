import React from 'react';

export default function TagEditorModal({
  file,
  tagInputText,
  setTagInputText,
  onSave,
  onClose,
}) {
  if (!file) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4"
      onClick={onClose}
    >
      <div
        className="card-surface max-w-md w-full p-5 rounded-xl border border-white/10"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 className="text-base font-semibold text-gray-200 mb-2">Edit File Tags</h3>
        <p className="text-xs text-gray-400 mb-3 truncate">
          File: <span className="text-gray-200">{file.filename}</span>
        </p>
        <div className="space-y-3">
          <div>
            <label className="block text-xs text-gray-400 mb-1">Tags (comma-separated)</label>
            <input
              type="text"
              value={tagInputText}
              onChange={(e) => setTagInputText(e.target.value)}
              placeholder="e.g. project, invoice, favorite"
              className="input-base w-full text-xs"
              autoFocus
            />
            <p className="text-[11px] text-gray-500 mt-1">
              Tags are associated with this file's content hash (SHA256).
            </p>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <button onClick={onClose} className="btn-ghost text-xs">
              Cancel
            </button>
            <button onClick={onSave} className="btn-brand text-xs">
              Save Tags
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
