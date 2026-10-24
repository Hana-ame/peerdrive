// check-dist.mjs — Issue #396: the committed dist/ must be exactly what the source builds.
//
// 发现背景：media 的 dist 是 force-add 入库的发布产物（根 .gitignore 忽略 dist/）。
// 去重后源码已切到 peerdrive-core，但 dist 一直没重建，旧协议实现留在产物里；CI 只跑
// `npm run build` 不比对，所以从没红过。本脚本在 build 之后把 dist 暂存再和 HEAD 比，
// 过期（含被 .gitignore 吞掉的新 chunk）即失败。用法：`npm run check:dist`。
import { spawnSync } from 'node:child_process'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const CWD = dirname(dirname(fileURLToPath(import.meta.url)))
const git = (args) => spawnSync('git', args, { cwd: CWD, encoding: 'utf8' })

// dist/ is ignored at repo root; -f is required for a fresh artifact (e.g. a new chunk) to enter the index.
const add = git(['add', '-f', '--', 'dist'])
if (add.status !== 0) {
  process.stderr.write(add.stderr || 'git add failed\n')
  process.exit(1)
}

const diff = git(['diff', '--cached', '--quiet', '--', 'dist'])
if (diff.status === 0) {
  console.log('OK: dist/ matches the source build')
  process.exit(0)
}
if (diff.status !== 1) {
  process.stderr.write(diff.stderr || 'git diff failed\n')
  process.exit(1)
}

process.stderr.write(git(['diff', '--cached', '--stat', '--', 'dist']).stdout || '')
console.error('ERROR: dist/ is stale — run `npm run build`, then commit the regenerated dist/')
process.exit(1)
