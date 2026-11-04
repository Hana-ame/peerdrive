// protocol-ssot.test.mjs — Issue #396 drift guard for the single frame-protocol source.
//
// 发现背景：issue #396 在 baseline 666fff5 上测得 core/client/media 各有一份帧解析实现
// （client 212 行、media 94 行、core 还是 34 行的残缺版）。复核 491f286 时两份副本已经
// 变成 re-export shim，但 `packages/peerdrive-media/dist/` 里仍躺着旧实现的构建产物
// （旧 parseFrame 只认 `{type}`，不认 v2 `{event,req_id}` envelope 与 `PD02` 32B 头）。
//
// 本文件只做**静态**检查，不需要 npm install：任何一份实现重新出现在源码里、shim 里
// 混进实现、或 front 不再引用 core，CI 立即变红。这里刻意不比对哈希——core 的协议实现
// 本来就该继续演进，锁死的是「只有一份」这个结构，而不是某一行内容。
import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { basename, dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(HERE, '..', '..', '..')
const CORE = join(ROOT, 'packages', 'peerdrive-core')

const toPosix = (p) => p.split('\\').join('/')
const rel = (p) => toPosix(relative(ROOT, p))

/** 收集 packages/ 与 front/src/ 下的源码（跳过 node_modules / dist / 隐藏目录）。 */
function walkSource(dir, out = []) {
  if (!existsSync(dir)) return out
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walkSource(p, out)
    else if (/\.(m?js|jsx)$/.test(name)) out.push(p)
  }
  return out
}

const sourceFiles = [
  ...walkSource(join(ROOT, 'packages')),
  ...walkSource(join(ROOT, 'front', 'src')),
]

describe('Issue #396: frame protocol has exactly one implementation', () => {
  it('defines each protocol primitive in peerdrive-core only', () => {
    const primitives = [
      ['parseFrame', /export\s+function\s+parseFrame\s*\(/, 'packages/peerdrive-core/src/protocol.js'],
      ['isBinaryFrame', /export\s+function\s+isBinaryFrame\s*\(/, 'packages/peerdrive-core/src/protocol.js'],
      ['toUint8Array', /export\s+function\s+toUint8Array\s*\(/, 'packages/peerdrive-core/src/protocol.js'],
      ['encodeBinaryHeader32', /export\s+function\s+encodeBinaryHeader32\s*\(/, 'packages/peerdrive-core/src/protocol.js'],
      ['decodeBinaryHeader32', /export\s+function\s+decodeBinaryHeader32\s*\(/, 'packages/peerdrive-core/src/protocol.js'],
      ['FrameDispatcher', /export\s+class\s+FrameDispatcher\b/, 'packages/peerdrive-core/src/dispatcher.js'],
      ['Sha256', /export\s+class\s+Sha256\b/, 'packages/peerdrive-core/src/sha256.js'],
    ]

    for (const [name, pattern, expected] of primitives) {
      const hits = sourceFiles
        .filter((file) => pattern.test(readFileSync(file, 'utf8')))
        .map(rel)
      assert.deepEqual(
        hits,
        [expected],
        `${name} 只允许定义在 ${expected}，实际命中: ${hits.join(', ') || '(none)'}`,
      )
    }
  })

  it('has no protocol.js outside core + the two re-export shims', () => {
    const hits = sourceFiles.filter((file) => basename(file) === 'protocol.js').map(rel).sort()
    assert.deepEqual(hits, [
      'packages/peerdrive-client/src/protocol.js',
      'packages/peerdrive-core/src/protocol.js',
      'packages/peerdrive-media/src/protocol.js',
    ])
  })

  it('keeps client/media shims re-export-only and wired to existing core files', () => {
    const shims = [
      {
        file: 'packages/peerdrive-client/src/protocol.js',
        modules: ['protocol', 'url', 'queue', 'dispatcher', 'security'],
      },
      { file: 'packages/peerdrive-client/src/sha256.js', modules: ['sha256'] },
      { file: 'packages/peerdrive-media/src/protocol.js', modules: ['protocol'] },
    ]
    const coreModule = '(protocol|sha256|url|queue|dispatcher|security)'
    const coreTarget = new RegExp(`(?:^|/)peerdrive-core/(?:src/)?${coreModule}(?:\\.js)?$`)

    for (const { file, modules } of shims) {
      const abs = join(ROOT, file)
      const text = readFileSync(abs, 'utf8')
      assert.doesNotMatch(text, /\b(function|class)\b|=>/, `${file}: shim 里不允许出现实现`)

      const targets = text
        .split('\n')
        .map((line) => line.trim())
        .filter((line) => line && !line.startsWith('//'))
        .map((line) => {
          const m = line.match(/^export \* from '([^']+)'$/)
          assert.ok(m, `${file}: 只允许整行 re-export，实际: ${line}`)
          return m[1]
        })

      for (const target of targets) {
        assert.match(target, coreTarget, `${file}: 只能 re-export peerdrive-core，实际: ${target}`)
        if (target.startsWith('.')) {
          assert.ok(existsSync(resolve(dirname(abs), target)), `${file}: re-export 目标不存在: ${target}`)
        }
      }
      for (const module of modules) {
        assert.ok(
          targets.some((t) => new RegExp(`(?:^|/)peerdrive-core/(?:src/)?${module}(?:\\.js)?$`).test(t)),
          `${file}: 缺少对 peerdrive-core/${module} 的 re-export`,
        )
      }
    }
  })

  it('keeps every consumer wired to peerdrive-core (not an orphan package)', () => {
    const frontPkg = JSON.parse(readFileSync(join(ROOT, 'front', 'package.json'), 'utf8'))
    assert.equal(
      frontPkg.dependencies?.['peerdrive-core'],
      'file:../packages/peerdrive-core',
      'front/package.json 必须声明 peerdrive-core 的 file 依赖',
    )

    const frontHits = sourceFiles
      .filter((file) => file.includes(`${join('front', 'src')}`))
      .filter((file) => /from\s+['"][^'"]*packages\/peerdrive-core\/src\/index\.js['"]/.test(readFileSync(file, 'utf8')))
      .map(rel)
    assert.ok(frontHits.length > 0, 'front/src 必须直接 import peerdrive-core/src/index.js')

    // core 的 exports 必须覆盖 shim 可能使用的 package 子路径（供消费端改为
    // `import 'peerdrive-core/protocol'` 时不至于解析失败）。
    const corePkg = JSON.parse(readFileSync(join(CORE, 'package.json'), 'utf8'))
    for (const subpath of ['./protocol', './sha256', './url', './queue', './dispatcher', './security']) {
      assert.ok(corePkg.exports?.[subpath], `peerdrive-core/package.json 缺少 exports['${subpath}']`)
    }
  })
})
