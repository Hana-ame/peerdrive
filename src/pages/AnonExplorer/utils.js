const SHA256_RE = /\b([a-f0-9]{64})\b/i;
export function extractHash(text) { const m = (text || '').match(SHA256_RE); return m ? m[1].toLowerCase() : null; }

export function fileIcon(m, p) {
  if (!m && !p) return '📄';
  if (m) {
    if (m.startsWith('image/')) return '🖼️';
    if (m.startsWith('video/')) return '🎬';
    if (m.startsWith('audio/')) return '🎵';
    if (m.startsWith('text/')) return '📝';
    if (m.includes('pdf')) return '📕';
    if (m.includes('zip') || m.includes('tar') || m.includes('gzip') || m.includes('rar')) return '📦';
  }
  if (p) {
    const ext = p.split('.').pop()?.toLowerCase();
    if (['png','jpg','jpeg','gif','webp','svg','bmp','ico'].includes(ext)) return '🖼️';
    if (['mp4','avi','mkv','mov','webm'].includes(ext)) return '🎬';
    if (['mp3','wav','flac','ogg'].includes(ext)) return '🎵';
    if (['pdf'].includes(ext)) return '📕';
    if (['zip','rar','7z','tar','gz','gzip'].includes(ext)) return '📦';
    if (['txt','md','json','js','ts','jsx','tsx','css','html','xml','yaml','yml','py','go','rs','java','c','cpp','h','log','cfg','ini','conf','sh','bash'].includes(ext)) return '📝';
  }
  return '📄';
}

export function fmtSize(b) {
  if (!b) return '';
  if (b < 1024) return b + ' B';
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB';
  return (b / 1073741824).toFixed(1) + ' GB';
}

export function relTime(ts) {
  if (!ts) return '';
  const d = new Date(ts), now = new Date();
  const diff = Math.floor((now - d) / 1000);
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 604800) return `${Math.floor(diff / 86400)} 天前`;
  return d.toLocaleDateString();
}
