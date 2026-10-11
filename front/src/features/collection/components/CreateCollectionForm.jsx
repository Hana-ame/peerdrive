import React from 'react';

export default function CreateCollectionForm({
  name,
  setName,
  tagsInput,
  setTagsInput,
  visibility,
  setVisibility,
  accessPolicy,
  setAccessPolicy,
  passcode,
  setPasscode,
  files,
  selected,
  toggleFile,
  create,
  busy,
  onCancel,
  visLabels,
}) {
  return (
    <div className="card-surface p-5 space-y-4">
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">Collection Name</label>
        <input
          value={name}
          onChange={e => setName(e.target.value)}
          placeholder="Collection name"
          className="input-base max-w-md"
        />
      </div>
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">Tags (comma-separated)</label>
        <input
          value={tagsInput}
          onChange={e => setTagsInput(e.target.value)}
          placeholder="e.g. photos, project-alpha, backup"
          className="input-base max-w-md text-xs"
        />
        <p className="text-[11px] text-gray-500 mt-1">Optional tags to organize and search collections.</p>
      </div>
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">Visibility</label>
        <div className="flex gap-1">
          {Object.entries(visLabels).map(([k, v]) => (
            <button
              key={k}
              type="button"
              onClick={() => setVisibility(k)}
              className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
                visibility === k ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
              }`}
            >
              {v.icon} {v.label}
            </button>
          ))}
        </div>
      </div>
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">Access Policy (Extraction Mode)</label>
        <div className="flex gap-1 mb-2">
          <button
            type="button"
            onClick={() => setAccessPolicy('public')}
            className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
              accessPolicy === 'public' ? 'bg-brand-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
            }`}
          >
            🌐 Open
          </button>
          <button
            type="button"
            onClick={() => setAccessPolicy('protected')}
            className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
              accessPolicy === 'protected' ? 'bg-amber-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
            }`}
          >
            🔑 Protected (Passcode)
          </button>
          <button
            type="button"
            onClick={() => setAccessPolicy('private')}
            className={`px-3 py-1.5 text-xs rounded-lg transition-colors ${
              accessPolicy === 'private' ? 'bg-red-600 text-white' : 'bg-white/[0.04] text-gray-400 hover:text-white'
            }`}
          >
            🔒 Private
          </button>
        </div>
        {accessPolicy === 'protected' && (
          <input
            type="text"
            value={passcode}
            onChange={e => setPasscode(e.target.value)}
            placeholder="Set extraction passcode / 提取码"
            className="input-base max-w-md text-xs mt-1"
          />
        )}
      </div>
      <div>
        <label className="block text-xs text-gray-400 mb-1.5">
          Select Netdisk Files ({selected.length} selected)
        </label>
        {files.length === 0 ? (
          <p className="text-xs text-gray-600">No files in netdisk yet. Go to "My Cloud Drive" to upload first.</p>
        ) : (
          <ul className="border border-white/[0.06] rounded-lg divide-y divide-white/[0.04] max-h-72 overflow-y-auto">
            {files.map((f, i) => (
              <li
                key={f.hash || i}
                onClick={() => toggleFile(f.hash)}
                className={`flex items-center gap-3 px-3 py-2 text-xs cursor-pointer hover:bg-white/[0.03] ${
                  selected.includes(f.hash) ? 'bg-brand-500/10' : ''
                }`}
              >
                <input
                  type="checkbox"
                  readOnly
                  checked={selected.includes(f.hash)}
                  className="accent-brand-500 pointer-events-none"
                />
                <span className="flex-1 truncate text-gray-300">{f.filename}</span>
                <span className="text-gray-500 shrink-0">{f.size} B</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="flex gap-2">
        <button
          type="button"
          onClick={create}
          disabled={busy || selected.length === 0}
          className="btn-brand"
        >
          Create Collection
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="btn-ghost"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
