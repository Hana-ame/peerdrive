// SAMPLE_ENTRY keeps the layout inspectable when the node has no iwara module.
// It is marked sample:true so the cards render a badge and never show a fake
// download link — an empty downloadUrl renders a disabled control instead.
export const SAMPLE_ENTRY = {
  id: 'sample-0001',
  title: 'Sample video (placeholder)',
  author: 'SampleUploader',
  authorId: 'u-sample',
  cover: '',
  duration: 182,
  views: 12800,
  uploaded: 0,
  resolutions: [
    { name: '1080p', downloadUrl: '' },
    { name: '720p', downloadUrl: '' },
  ],
  sample: true,
};

// fmtDuration: seconds → m:ss / h:mm:ss. 0 and missing both render '—' rather
// than '0:00' — a real zero-length video is not the same as "not reported".
export function fmtDuration(sec) {
  if (!sec || sec <= 0) return '—';
  const s = Math.round(sec);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  const p2 = (n) => String(n).padStart(2, '0');
  return h ? `${h}:${p2(m)}:${p2(r)}` : `${m}:${p2(r)}`;
}

// fmtCount: view counts → 950 / 12.8k / 1.2M, so the column stays narrow.
export function fmtCount(n) {
  if (!n || n <= 0) return '—';
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e4) return `${(n / 1e3).toFixed(1)}k`;
  return String(n);
}

// fmtDate: unix seconds → 'YYYY-MM-DD HH:mm'. 0 renders '—'.
export function fmtDate(ts) {
  if (!ts || ts <= 0) return '—';
  const d = new Date(ts * 1000);
  if (Number.isNaN(d.getTime())) return '—';
  const p2 = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
}

export function iwaraUrl(entry) {
  return `https://www.iwara.tv/videos/${entry.id}`;
}

// parseIds: pasted text -> one id per request. Also splits on full-width commas
// (， and 、), which is how a Chinese keyboard enters the separator when someone
// pastes a list out of a note or chat message.
export function parseIds(text) {
  return String(text || '')
    .split(/[\s,，、]+/)
    .map((t) => t.trim())
    .filter(Boolean);
}

// entryQualities: the resolution rows worth offering as download choices, empty
// names dropped so the <select> never has a blank first option.
export function entryQualities(entry) {
  return (Array.isArray(entry.resolutions) ? entry.resolutions : [])
    .filter((r) => r && r.name);
}

// pickedUrl: the download URL for the quality the user picked on this entry.
// The selection lives in the page (not the backend) — the backend returns every
// resolution, picking one is purely a display choice.
export function pickedUrl(entry, quality) {
  const list = entryQualities(entry);
  const name = quality[entry.id] || (list[0] && list[0].name);
  return (list.find((r) => r.name === name) || {}).downloadUrl || '';
}
