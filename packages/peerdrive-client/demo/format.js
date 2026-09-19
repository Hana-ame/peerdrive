// format.js — demo 的展示格式化。
//
// 故意不复用 front/src/components/netdisk/format.js：这个包要能单独发布/单独
// 打开（`npm run demo` 只服务包目录），跨目录 import 会让演示依赖外部仓库结构。

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
