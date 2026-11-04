export const CATEGORIES = [
  { id: 'all', label: 'All Files', icon: '📁' },
  { id: 'inbox', label: 'Inbox (待审)', icon: '📥' },
  { id: 'docs', label: 'Documents', icon: '📄', ext: ['pdf', 'doc', 'docx', 'txt', 'md', 'rtf', 'odt', 'csv', 'xls', 'xlsx', 'ppt', 'pptx'] },
  { id: 'images', label: 'Images', icon: '🖼️', ext: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico'] },
  { id: 'videos', label: 'Videos', icon: '🎬', ext: ['mp4', 'mkv', 'avi', 'mov', 'webm', 'flv', 'wmv'] },
  { id: 'audio', label: 'Audio', icon: '🎵', ext: ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'wma'] },
  { id: 'archives', label: 'Archives', icon: '📦', ext: ['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'] },
  { id: 'code', label: 'Code', icon: '💻', ext: ['js', 'ts', 'jsx', 'tsx', 'go', 'py', 'rs', 'c', 'cpp', 'h', 'java', 'html', 'css', 'json', 'sh'] },
];

export function getFileExt(filename) {
  const parts = String(filename || '').split('.');
  return parts.length > 1 ? parts.pop().toLowerCase() : '';
}

export function getFileCategory(f) {
  if (f.isDir) return 'folder';
  const ext = getFileExt(f.filename);
  for (const cat of CATEGORIES) {
    if (cat.ext && cat.ext.includes(ext)) return cat.id;
  }
  const mime = String(f.mime_type || '').toLowerCase();
  if (mime.startsWith('image/')) return 'images';
  if (mime.startsWith('video/')) return 'videos';
  if (mime.startsWith('audio/')) return 'audio';
  if (mime.startsWith('text/')) return 'docs';
  return 'other';
}

export function getFileIcon(f) {
  if (f.isDir) return '📁';
  const cat = getFileCategory(f);
  const found = CATEGORIES.find(c => c.id === cat);
  return found ? found.icon : '📄';
}
