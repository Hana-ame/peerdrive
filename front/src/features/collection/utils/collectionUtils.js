export const VIS_LABEL = {
  public: { icon: '🌐', label: 'Public' },
  restricted: { icon: '👥', label: 'Authorized Only' },
  private: { icon: '🔒', label: 'Self Only' },
};

export function fmtTime(ts) {
  if (!ts) return '—';
  const d = new Date(ts);
  return isNaN(d) ? ts : d.toLocaleString('en-US', { hour12: false });
}
