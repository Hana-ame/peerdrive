// format.js — demo display formatting.
//
// Intentionally does not reuse front/src/components/netdisk/format.js: this package
// needs to be published/opened standalone (`npm run demo` only serves the package
// directory), cross-directory imports would make the demo depend on external repo structure.

export function formatBytes(bytes) {
  const n = Number(bytes)
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1000) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1000
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

export function formatCount(n) {
  const v = Number(n)
  return Number.isFinite(v) ? String(v) : '0'
}
