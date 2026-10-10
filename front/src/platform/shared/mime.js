// mime.js — MIME detection and media kind classification.
//
// 为什么独立模块：
// 1. 规范统一：根据 issue #150，优先信服务器声明的 MIME，缺失时本地通过扩展名兜底。
// 2. 分流策略：驱动 Drive 页面与 Collection 浏览器的按 MIME 内联预览（图片、视频、音频、PDF、代码/文本）。
// 3. 内存与性能保护：文本/代码预览必须截断（MAX_TEXT_PREVIEW_BYTES = 1MB），防止大 manifest / 文件爆内存。

export const MAX_TEXT_PREVIEW_BYTES = 1024 * 1024; // 1MB 截断保护

export const EXT_TO_MIME = {
  // Images
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  png: 'image/png',
  gif: 'image/gif',
  webp: 'image/webp',
  bmp: 'image/bmp',
  svg: 'image/svg+xml',
  avif: 'image/avif',
  ico: 'image/x-icon',

  // Video
  mp4: 'video/mp4',
  webm: 'video/webm',
  mov: 'video/quicktime',
  m4v: 'video/x-m4v',
  ogv: 'video/ogg',
  mkv: 'video/x-matroska',

  // Audio
  mp3: 'audio/mpeg',
  wav: 'audio/wav',
  flac: 'audio/flac',
  ogg: 'audio/ogg',
  oga: 'audio/ogg',
  aac: 'audio/aac',
  m4a: 'audio/m4a',
  opus: 'audio/opus',

  // PDF
  pdf: 'application/pdf',

  // Text / Code
  txt: 'text/plain',
  md: 'text/markdown',
  markdown: 'text/markdown',
  json: 'application/json',
  csv: 'text/csv',
  html: 'text/html',
  htm: 'text/html',
  xml: 'text/xml',
  css: 'text/css',
  js: 'text/javascript',
  jsx: 'text/javascript',
  ts: 'text/typescript',
  tsx: 'text/typescript',
  go: 'text/plain',
  py: 'text/plain',
  sh: 'text/plain',
  bash: 'text/plain',
  yaml: 'text/yaml',
  yml: 'text/yaml',
  toml: 'text/plain',
  log: 'text/plain',
  ini: 'text/plain',
  conf: 'text/plain',
  env: 'text/plain',
};

// extOf extracts extension without leading dot (lowercase).
export function extOf(filename) {
  const m = String(filename || '').toLowerCase().match(/\.([a-z0-9]+)$/);
  return m ? m[1] : '';
}

// mimeOf resolves the effective MIME type:
// 1. If explicitMime is non-empty and specific, returns it (cleaning parameters like charset).
// 2. SVG special case: Go http.DetectContentType flags SVG as "text/plain; charset=utf-8",
//    so svg is always normalized to "image/svg+xml".
// 3. If explicitMime is generic (text/plain, application/octet-stream), prefers extension-based MIME.
export function mimeOf(filename, explicitMime = '') {
  const ext = extOf(filename);
  const extMime = EXT_TO_MIME[ext] || '';

  // SVG override: standard library sniffing frequently classifies SVG markup as plain text
  if (ext === 'svg') {
    return 'image/svg+xml';
  }

  if (explicitMime && explicitMime !== 'folder' && explicitMime !== 'unknown') {
    const clean = explicitMime.trim().toLowerCase().split(';')[0].trim();
    if ((clean === 'text/plain' || clean === 'application/octet-stream') && extMime) {
      return extMime;
    }
    return clean;
  }
  return extMime;
}

// kindOf categorizes a file into previewable kinds:
// 'image' | 'video' | 'audio' | 'pdf' | 'text' | null
export function kindOf(filename, explicitMime = '') {
  const mime = mimeOf(filename, explicitMime);
  if (mime.startsWith('image/')) return 'image';
  if (mime.startsWith('video/')) return 'video';
  if (mime.startsWith('audio/')) return 'audio';
  if (mime === 'application/pdf') return 'pdf';
  if (
    mime.startsWith('text/') ||
    mime === 'application/json' ||
    mime === 'application/xml' ||
    mime === 'application/javascript'
  ) {
    return 'text';
  }
  return null;
}
