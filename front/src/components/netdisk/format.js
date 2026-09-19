// netdisk/format.js：网盘界面的展示格式化。
//
// 单独一个文件而不是散在各组件里：同一份"大小/时间/节点 id"的展示规则
// 在文件表格、节点卡片、传输列表三处都要用，散着写必然出现
// "文件页显示 1.5 MB、传输页显示 1572864 字节" 这类不一致。

// formatSize 人类可读大小（网盘习惯：KB/MB/GB 十进制，与 macOS Finder 一致）。
export function formatSize(bytes) {
  const n = Number(bytes);
  if (!Number.isFinite(n) || n < 0) return '—';
  if (n < 1000) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

// formatTime ISO 字符串 / Unix 秒 → 本地时间；无效值回 '—'。
export function formatTime(v) {
  if (v === null || v === undefined || v === '') return '—';
  let d;
  if (typeof v === 'number') {
    // 秒级时间戳（后端 lastSeen 是 Unix 秒）与毫秒级都要能吃下
    d = new Date(v < 1e12 ? v * 1000 : v);
  } else {
    d = new Date(v);
  }
  if (Number.isNaN(d.getTime())) return '—';
  const pad = (x) => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// formatRelative 相对时间（节点"最近在线"用；绝对值对用户没意义）。
export function formatRelative(v) {
  if (!v) return '—';
  const d = typeof v === 'number' ? new Date(v < 1e12 ? v * 1000 : v) : new Date(v);
  if (Number.isNaN(d.getTime())) return '—';
  const diff = Date.now() - d.getTime();
  if (diff < 0) return '刚刚';
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hour = Math.floor(min / 60);
  if (hour < 24) return `${hour} 小时前`;
  return `${Math.floor(hour / 24)} 天前`;
}

// shortPeer 节点 id 中段省略（卡片宽度有限，但要保留头尾以便区分）。
export function shortPeer(id, keep = 10) {
  const s = String(id || '');
  if (s.length <= keep * 2 + 3) return s;
  return `${s.slice(0, keep)}…${s.slice(-6)}`;
}

// shortHash 内容 hash 展示（列表里只显示前 12 位，够肉眼比对）。
export function shortHash(h, n = 12) {
  const s = String(h || '');
  return s.length > n ? `${s.slice(0, n)}…` : s;
}

// baseName 从路径取文件名（对端给的 path 可能带目录）。
export function baseName(p) {
  const s = String(p || '');
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
  return i >= 0 ? s.slice(i + 1) : s;
}
