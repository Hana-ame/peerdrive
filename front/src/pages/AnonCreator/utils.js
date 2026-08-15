import { LLM_CHAT, SEARCH_HISTORY_KEY, MAX_HISTORY } from './constants';

export function fileIcon(m, p) {
  if (m) {
    if (m.startsWith('image/')) return '🖼️';
    if (m.startsWith('video/')) return '🎬';
    if (m.startsWith('audio/')) return '🎵';
    if (m.startsWith('text/')) return '📝';
    if (m.includes('pdf')) return '📕';
    if (m.includes('zip') || m.includes('tar') || m.includes('gzip') || m.includes('rar')) return '📦';
  }
  const ext = (p || '').split('.').pop()?.toLowerCase();
  if (['png','jpg','jpeg','gif','webp','svg','bmp','ico'].includes(ext)) return '🖼️';
  if (['mp4','avi','mkv','mov','webm'].includes(ext)) return '🎬';
  if (['mp3','wav','flac','ogg'].includes(ext)) return '🎵';
  if (['pdf'].includes(ext)) return '📕';
  if (['zip','rar','7z','tar','gz','gzip'].includes(ext)) return '📦';
  if (['txt','md','json','js','ts','jsx','tsx','css','html','xml','yaml','yml','py','go','rs','java','c','cpp','h','log','cfg','ini','conf','sh','bash'].includes(ext)) return '📝';
  return '📄';
}

export function fmtSize(b) {
  if (!b) return '-';
  if (b < 1024) return b + ' B';
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
  return (b / 1073741824).toFixed(1) + ' GB';
}

export function loadSearchHistory() {
  // 坑：旧版本可能写入过非数组 JSON（如对象），SearchHistory 组件会直接 .map 崩溃。
  // 强制确保返回数组。
  try {
    const v = JSON.parse(localStorage.getItem(SEARCH_HISTORY_KEY) || '[]');
    return Array.isArray(v) ? v.filter(i => typeof i === 'string') : [];
  } catch { return []; }
}
export function saveSearchHistory(items) {
  try { localStorage.setItem(SEARCH_HISTORY_KEY, JSON.stringify(items.slice(0, MAX_HISTORY))); } catch {}
}
export function addSearchHistory(q) {
  if (!q?.trim()) return;
  const h = loadSearchHistory().filter(i => i !== q);
  h.unshift(q);
  saveSearchHistory(h);
}

export async function llmSuggest(names) {
  const res = await fetch(LLM_CHAT, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model: 'Qwen/Qwen3-8B', messages: [{ role: 'user', content: `请用3-5个中文字为以下文件集取一个简洁的合集名称,只输出名称: ${names}` }], max_tokens: 20, stream: false }),
  });
  const d = await res.json();
  return d.choices?.[0]?.message?.content?.trim()?.replace(/["""'']/g, '') || null;
}

export function collDisplayName(c) {
  if (c.friendly_name) return c.friendly_name;
  if (c.name_preview && c.name_preview !== '未命名') return c.name_preview;
  if (c.entry_count > 0) return `${c.entry_count} 个文件`;
  return '空合集';
}
