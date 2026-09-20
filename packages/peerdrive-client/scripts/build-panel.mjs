// build-panel.mjs — 把 peerdrive-client 的源码内联成一个**单文件**公共面板。
//
// 为什么需要它：
//   demo/consumer.html 用 `<script type="module">` + `import '../src/index.js'`，
//   这意味着必须有个 HTTP 服务器把 src/ 一起提供出去——`file://` 会因为 CORS
//   直接失败，丢到静态托管上还得连同整个包目录一起传。
//   而需求里网盘的 UI 形态是「一个公用 panel，用 peerjs 连进去」，不该要求
//   用户本地起任何 server。所以这里把三个模块拼成一个 IIFE 塞进 HTML：
//   产物 dist/panel.html 双击能开、能单独传到任意静态空间（Pages / CDN / OSS）。
//
// 为什么自己拼而不用打包器：
//   三个文件全是纯 ESM、零外部依赖、没有 export default，用 DFS 拓扑序 + 剥掉
//   import/export 关键字就够了。真上 esbuild 反而要在本包里引入 devDependency
//   ——而这个包最大的卖点就是「零运行时依赖」，不值得。
//
// 用法: node scripts/build-panel.mjs [--check]
//   --check  只比较产物是否与现有 dist/panel.html 一致（CI 用来防漂移），不做写入。

import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'src')
const DIST = join(ROOT, 'dist')
const OUT = join(DIST, 'panel.html')
const TEMPLATE = join(ROOT, 'panel', 'template.html')
const APP = join(ROOT, 'panel', 'app.js')

/** 剥掉 ESM 的 import/export 语法，同时收集导出的符号名。 */
function transpile(code, file, exportsOut) {
  const lines = code.split('\n')
  const kept = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]

    // 单行 import 或跨行 import（client.js 的第二个 import 就是跨行的）：
    // 从 `import` 起一路收集到含有 `from '...'` 的那一行为止，整体丢弃。
    if (/^\s*import\b/.test(line)) {
      while (i < lines.length && !/from\s+['"][^'"]+['"]\s*;?\s*$/.test(lines[i])) i++
      continue
    }

    // re-export：`export { A, B } from './x.js'` —— 这些符号来自已经内联的模块，
    // 本身不需要再生成声明，但请求出现在导出名单里。
    const reExport = line.match(/^\s*export\s*\{([^}]*)\}\s*from\s*['"][^'"]+['"]\s*;?\s*$/)
    if (reExport) {
      for (const name of namesOf(reExport[1])) exportsOut.add(name)
      continue
    }

    // export 列表（不带 from）：保留名字
    const list = line.match(/^\s*export\s*\{([^}]*)\}\s*;?\s*$/)
    if (list) {
      for (const name of namesOf(list[1])) exportsOut.add(name)
      continue
    }

    // export const/let/var/function/class/async function → 去掉 export 前缀
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
      // 兼容 `A as B`：重命名后对外可见的是 B
      const m = s.split(/\s+as\s+/)
      return (m[1] || m[0]).trim()
    })

// 拓扑顺序（手写的依赖序，比动态解析可靠）：sha256 ← protocol ← client
const ORDER = ['sha256.js', 'protocol.js', 'client.js']

function buildBundle() {
  const exported = new Set()
  const parts = [
    '// ===== 以下由 scripts/build-panel.mjs 从 src/ 内联生成，勿手改 =====',
    ";(function () {",
    "'use strict'",
  ]
  for (const file of ORDER) {
    const code = readFileSync(join(SRC, file), 'utf8')
    parts.push(`// ---- src/${file} ----`)
    parts.push(transpile(code, file, exported))
  }
  //index.js 是 `export *` 的门面，本身不产生代码，只需把它的导出算进去
  exported.add('Sha256')
  exported.add('sha256Hex')
  const names = [...exported].sort()
  parts.push('// ---- 导出（对应 src/index.js 的 export *） ----')
  parts.push(`  const api = { ${names.join(', ')} }`)
  // ESM 之外再挂一层 CommonJS 出口，方便有人在 node 里 require 这个产物做断言
  parts.push(`  if (typeof window !== 'undefined') { window.PeerDrive = api }`)
  parts.push(`  if (typeof globalThis !== 'undefined') { globalThis.PeerDriveBuiltin = api }`)
  parts.push('})()')
  return { bundle: parts.join('\n'), names }
}

function main() {
  const { bundle, names } = buildBundle()
  const template = readFileSync(TEMPLATE, 'utf8')
  const app = readFileSync(APP, 'utf8')

  // 先校验 bundle 语法：拼接出错时在这里就炸比让浏览器白屏好排查
  new Function(bundle)

  // 版本标识必须**确定性**：早期版本这里写的是构建时间，结果同一次提交只要重建
  // 产物就 diff 出时间差，`--check`（CI 防漂移）会永远报「产物过期」。
  // 改成 bundle 内容指纹：既能一眼看出产物是不是同源，也保证可重复构建。
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
