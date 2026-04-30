import { useState, useRef } from 'react';

function parseTags(s) {
  return (s || '').split(/[,;]/).map(t => t.trim()).filter(Boolean);
}

export default function EditorToolbar({ fname, tags, entryCount, validCount, saving, onFname, onTags, onSave }) {
  const [inputVal, setInputVal] = useState('');
  const tagList = parseTags(tags);
  const inputRef = useRef(null);

  const addTag = (raw) => {
    const t = raw.trim();
    if (!t) return;
    if (tagList.includes(t)) { setInputVal(''); return; }
    onTags([...tagList, t].join(', '));
    setInputVal('');
  };

  const removeTag = (t) => {
    onTags(tagList.filter(x => x !== t).join(', '));
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter') { e.preventDefault(); addTag(inputVal); }
    if (e.key === 'Backspace' && !inputVal && tagList.length) removeTag(tagList[tagList.length - 1]);
  };

  const handlePaste = (e) => {
    const paste = e.clipboardData.getData('text');
    if (/[,;]/.test(paste)) {
      e.preventDefault();
      const parts = paste.split(/[,;]/).map(t => t.trim()).filter(Boolean);
      const newTags = [...tagList];
      for (const p of parts) { if (!newTags.includes(p)) newTags.push(p); }
      onTags(newTags.join(', '));
    }
  };

  return (
    <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-800 shrink-0 flex-wrap">
      <input value={fname} onChange={e => onFname(e.target.value)} placeholder="合集名称" className="bg-gray-800 text-xs px-2 py-1.5 rounded border border-gray-700 w-28 focus:outline-none focus:border-blue-500" />

      <div className="flex items-center gap-1 flex-1 min-w-0 bg-gray-800 rounded border border-gray-700 px-1.5 py-0.5 cursor-text" onClick={() => inputRef.current?.focus()}>
        {tagList.map((t, i) => (
          <span key={i} className="flex items-center gap-0.5 text-[10px] bg-blue-900/50 text-blue-300 pl-1.5 pr-0.5 py-0.5 rounded-full whitespace-nowrap">
            {t}
            <button onClick={(e) => { e.stopPropagation(); removeTag(t); }} className="text-blue-400 hover:text-red-400 leading-none">&times;</button>
          </span>
        ))}
        <input ref={inputRef} value={inputVal}
          onChange={e => { const v = e.target.value; if (/[,;]/.test(v)) { addTag(v.replace(/[,;]/g, '')); } else { setInputVal(v); } }}
          onKeyDown={handleKeyDown} onPaste={handlePaste}
          placeholder={tagList.length ? '' : '标签: a, b'}
          className="bg-transparent text-xs px-0.5 py-1 min-w-[60px] flex-1 border-none outline-none text-gray-300 placeholder-gray-600" />
      </div>

      <span className="text-[10px] text-gray-500 shrink-0">{validCount} 个文件</span>
      <button onClick={onSave} disabled={saving || !validCount}
        className="bg-green-600 hover:bg-green-700 disabled:opacity-40 text-sm px-3 py-1.5 rounded font-medium shrink-0">💾 保存</button>
    </div>
  );
}
