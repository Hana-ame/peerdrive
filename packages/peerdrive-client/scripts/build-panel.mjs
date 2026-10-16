// build-panel.mjs — 把 peerdrive-core 与 peerdrive-client 的源码及模块化 panel/*.js
// 内联成一个**单文件**公共面板 dist/panel.html（Issue #396 单一协议源 + Issue #404 模块化 + 安全加固）。
//
// 用法: node scripts/build-panel.mjs [--check]

import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'src')
const CORE_SRC = resolve(ROOT, '..', 'peerdrive-core', 'src')
const DIST = join(ROOT, 'dist')
const OUT = join(DIST, 'panel.html')
const TEMPLATE = join(ROOT, 'panel', 'template.html')
const APP = join(ROOT, 'panel', 'app.js')
const APP_SHARES = join(ROOT, 'panel', 'app-shares.js')
const APP_TRANSFERS = join(ROOT, 'panel', 'app-transfers.js')

/** 剥掉 ESM 的 import/export 语法，同时收集导出的符号名。 */
function transpile(code, file, exportsOut) {
  const lines = code.split('\n')
  const kept = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]

    if (/^\s*import\b/.test(line)) {
      while (i < lines.length && !/from\s+['"][^'"]+['"]\s*;?\s*$/.test(lines[i])) i++
      continue
    }

    const reExport = line.match(/^\s*export\s*\{([^}]*)\}\s*from\s*['"][^'"]+['"]\s*;?\s*$/)
    if (reExport) {
      for (const name of namesOf(reExport[1])) exportsOut.add(name)
      continue
    }

    const starExport = line.match(/^\s*export\s*\*\s*from\s*['"][^'"]+['"]\s*;?\s*$/)
    if (starExport) {
      continue
    }

    const list = line.match(/^\s*export\s*\{([^}]*)\}\s*;?\s*$/)
    if (list) {
      for (const name of namesOf(list[1])) exportsOut.add(name)
      continue
    }

    const decl = line.match(/^\s*export\s+(async\s+function|function|class|const|let|var)\s+([A-Za-z_$][\w$]*)/)
    if (decl) {
      exportsOut.add(decl[2])
      kept.push(line.replace(/^(\s*)export\s+/, '$1'))
      continue
    }

    if (/^\s*export\b/.test(line)) {
      throw new Error(`${file}: 无法处理的 export 语句 -> ${line.trim()}（扩展 transpile 前请先看清它）`)
    }

    kept.push(line)
  }
  return kept.join('\n')
}

const namesOf = (raw) =>
  raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const m = s.split(/\s+as\s+/)
      return (m[1] || m[0]).trim()
    })

// 拓扑顺序：从唯一协议源 peerdrive-core 内联 sha256 ← security ← protocol ← url ← dispatcher ← queue，再内联 peerdrive-client/src/client.js
const SOURCES = [
  { dir: CORE_SRC, file: 'sha256.js', label: 'peerdrive-core/src/sha256.js' },
  { dir: CORE_SRC, file: 'security.js', label: 'peerdrive-core/src/security.js' },
  { dir: CORE_SRC, file: 'protocol.js', label: 'peerdrive-core/src/protocol.js' },
  { dir: CORE_SRC, file: 'url.js', label: 'peerdrive-core/src/url.js' },
  { dir: CORE_SRC, file: 'dispatcher.js', label: 'peerdrive-core/src/dispatcher.js' },
  { dir: CORE_SRC, file: 'queue.js', label: 'peerdrive-core/src/queue.js' },
  { dir: SRC, file: 'client.js', label: 'src/client.js' },
]

function buildBundle() {
  const exported = new Set()
  const parts = [
    '// ===== 以下由 scripts/build-panel.mjs 从 peerdrive-core 与 src/ 内联生成，勿手改 =====',
    ";(function () {",
    "'use strict'",
  ]
  for (const item of SOURCES) {
    const code = readFileSync(join(item.dir, item.file), 'utf8')
    parts.push(`// ---- ${item.label} ----`)
    parts.push(transpile(code, item.label, exported))
  }
  exported.add('Sha256')
  exported.add('sha256Hex')
  const names = [...exported].sort()
  parts.push('// ---- 导出（对应 src/index.js 的 export *） ----')
  parts.push(`  const api = { ${names.join(', ')} }`)
  parts.push(`  if (typeof window !== 'undefined') { window.PeerDrive = api }`)
  parts.push(`  if (typeof globalThis !== 'undefined') { globalThis.PeerDriveBuiltin = api }`)
  parts.push('})()')
  return { bundle: parts.join('\n'), names }
}

function buildAppCode() {
  let app = readFileSync(APP, 'utf8')
  if (existsSync(APP_TRANSFERS)) {
    const transfers = readFileSync(APP_TRANSFERS, 'utf8')
    app = app.replace('/*__PANEL_TRANSFERS_PART__*/', () => transfers)
  }
  if (existsSync(APP_SHARES)) {
    const shares = readFileSync(APP_SHARES, 'utf8')
    app = app.replace('/*__PANEL_SHARES_PART__*/', () => shares)
  }
  return app
}

function main() {
  const { bundle, names } = buildBundle()
  const template = readFileSync(TEMPLATE, 'utf8')
  const app = buildAppCode()

  new Function(bundle)
  new Function(app)

  const bundleSha = createHash('sha256').update(bundle, 'utf8').digest('hex')
  const html = template
    .replace('/*__PANEL_BUNDLE__*/', () => bundle)
    .replace('/*__PANEL_APP__*/', () => app)
    .replace('__PANEL_BUILD_ID__', () => 'bundle ' + bundleSha.slice(0, 8))

  if (process.argv.includes('--check')) {
    if (!existsSync(OUT)) {
      console.error('panel: dist/panel.html 不存在，请先 `npm run build:panel`')
      process.exit(1)
    }
    const cur = readFileSync(OUT, 'utf8')
    if (cur !== html) {
      console.error('panel: dist/panel.html 已过期（src/ 或 panel/ 改过没重新构建），请跑 `npm run build:panel`')
      process.exit(1)
    }
    console.log(`panel: 产物与源码一致（${names.length} 个导出）`)
    return
  }

  mkdirSync(DIST, { recursive: true })
  writeFileSync(OUT, html)
  const sha = createHash('sha256').update(html, 'utf8').digest('hex')
  console.log(`panel: 已生成 dist/panel.html（${(html.length / 1024).toFixed(1)} KB, sha256 ${sha.slice(0, 12)}…）`)
  console.log(`panel: 导出 ${names.length} 个符号: ${names.join(', ')}`)
}

main()
