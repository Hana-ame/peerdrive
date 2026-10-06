#!/usr/bin/env node
// 文档引用体检：查 `路径` 与 `文件.go:行号` 引用是否还对得上代码。
//
// 起因（2026-10-06）：连续两轮文档修复都漏了同一类问题。
//   第一轮查「路径存在吗」→ 漏了裸写的 `main.go:行号`（换了文件名没换行号）
//   第二轮查「行号越界吗」→ 漏了「行号有效但内容已变」（心跳 30s 实际是 5s）
// 两次的教训是：**存在性检查只能证明路径没坏，证明不了它说的是不是那件事**。
// 所以这里把能机械判定的部分固化下来，避免第三次靠人肉重扫。
//
// 用法：node scripts/check-doc-refs.mjs [--warn]
//   --warn  只报告不失败（默认：报告了越界/缺文件就 exit 1）

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, basename, dirname, relative } from 'node:path'

const WARN_ONLY = process.argv.includes('--warn')
const ROOT = new URL('..', import.meta.url).pathname.replace(/\/$/, '')
const SKIP_DIRS = new Set(['.git', 'node_modules', 'dist', 'testdata', 'archive'])
const MAX_LINES = 80 // 单文件最多展示多少条，超出只给汇总

// ---------- 建索引：一次遍历，O(n) ----------
const byName = new Map()   // basename(去 _test) -> [实现文件相对路径]
const testIdx = new Map() // 原始 basename -> [相对路径]（回退用）
const lineCount = new Map() // 相对路径 -> 行数
function walk(dir) {
  let entries
  try { entries = readdirSync(dir) } catch { return }
  for (const e of entries) {
    if (SKIP_DIRS.has(e)) continue
    const p = join(dir, e)
    let st
    try { st = statSync(p) } catch { continue }
    if (st.isDirectory()) walk(p)
    else if (e.endsWith('.go')) {
      const rel = relative(ROOT, p)
      // 文档引用的是**实现文件**；把 _test.go 收进候选会让 admin.go 之类
      // 「本来唯一」的引用变成假歧义（admin.go / admin_test.go 两份，行数都够）。
      // 单独存一份完整表，仅在实现文件找不到时才回退到测试文件。
      const isTest = e.endsWith('_test.go')
      const key = isTest ? e.replace(/_test\.go$/, '.go') : e
      if (!byName.has(key)) byName.set(key, [])
      if (!isTest) byName.get(key).push(rel)
      if (!testIdx.has(e)) testIdx.set(e, [])
      testIdx.get(e).push(rel)
      try { lineCount.set(rel, readFileSync(p, 'utf8').split('\n').length) } catch {}
    }
  }
}
for (const d of ['back', 'front', 'packages']) walk(join(ROOT, d))

// ---------- 收集文档 ----------
const docs = []
function collectDocs(dir) {
  for (const e of readdirSync(dir)) {
    if (SKIP_DIRS.has(e)) continue
    const p = join(dir, e)
    const st = statSync(p)
    if (st.isDirectory()) collectDocs(p)
    else if (e.endsWith('.md')) docs.push(p)
  }
}
collectDocs(join(ROOT, 'doc'))

// ---------- 检查 ----------
const missing = []   // 引用了不存在的路径
const outOfRange = [] // 行号超出文件长度
const ambiguous = []  // 同名文件多份，无法唯一确定

// 路径引用。必须排除 `xxx.go:123` 这种带行号的（那是 lineRe 的活），
// 否则 app.go:409 会被当成一个名为 "app.go:409" 的路径而误报缺失。
// 行号引用。注意：这里必须捕获**尽可能长的路径**。
// 第一版写成 /(?<![/\w-])([\w./-]+\.go):(\d+)/ —— 结果对
// `back/internal/controller/file.go:30` 只捕获到 `file.go:30`（负向后视把 `controller/` 挡掉了），
// 于是把 80 处**已经带完整路径**的引用误报成「同名歧义」。
// 改成先吃完整路径，吃不到再退化为 basename，才对。
const lineRe = /(?:((?:\.{0,2}\/)?(?:[\w-]+\/)*[\w-]+\.go)|([\w-]+\.go)):(\d+)(?:-(\d+))?/g

// 路径引用。三条约束缺一不可（每条都对应一版踩过的坑）：
//   1. 顶层目录白名单 —— 否则 `/panel` `/ping` `/health` 这类 URL 会被当路径（第二版 2511 条误报的成因）
//   2. 排除 `*.go` —— 带行号的归 lineRe 管
//   3. 只认反引号 —— 文档里的裸词多半是散文不是引用
const TOP = ['back', 'front', 'packages', 'scripts', 'doc', 'echo', '.github']
const pathRe = new RegExp('`((?:' + TOP.join('|') + ')(?:/[A-Za-z0-9_.@-]+)+/?)`', 'g')

for (const doc of docs) {
  const rel = relative(ROOT, doc)
  const text = readFileSync(doc, 'utf8')
  const linesArr = text.split('\n')
  linesArr.forEach((line, i) => {
    for (const m of line.matchAll(pathRe)) {
      const p = m[1].replace(/[.,;:]$/, '')
      if (p.includes('*')) continue
      // ⚠️ 早先这里还 skip 了 p.endsWith('/')，理由是「目录不用查」。
      // 结果文档里 90% 的路径引用都是带尾斜杠的目录（`back/internal/panel/`），
      // 全部逃过检查 —— 负向对照里把 panel/ 改成不存在，照样报绿。
      // 尾斜杠只是写法差异，剥掉再查，目录该在就在。
      if (/\.go$|\.(js|jsx|mjs|ts|tsx|tsx?)s?$/i.test(p) && /:\d+$/.test(p)) continue // 交给 lineRe
      const probe = p.replace(/\/+$/, '')            // 剥尾斜杠
      try {
        const st = statSync(join(ROOT, probe))
        // 反引号里写了尾斜杠却指向文件（或反之）——不算失效，但提醒一句容易看错
        if (p.endsWith('/') && !st.isDirectory()) missing.push({ where: `${rel}:${i + 1}`, ref: `${p}（是文件，不是目录）` })
      } catch { missing.push({ where: `${rel}:${i + 1}`, ref: p }) }
    }
    for (const m of line.matchAll(lineRe)) {
      const full = m[1]           // 带目录的完整路径（可能为空）
      const base = m[2]           // 裸文件名（仅在 full 为空时存在）
      const path = full ?? base
      const hi = Number(m[4] ?? m[3])
      let target = null
      if (full) {
        // 「带目录的」也有两类：仓库根相对（back/internal/x.go），
        // 和包名简写（serverapp/app.go、controller/p2p.go）。后者要在包目录下找。
        try { statSync(join(ROOT, path)); target = path } catch {
          const dir = dirname(path)
          const cands = byName.get(basename(path)) ?? []
          target = cands.find(c => c.includes(`/${dir}/`)) ?? null
          // ⚠️ 这里原来 push 进 missing，把「定位不到」误报成「文件不存在」。
          // 两者对读者的意义完全不同：前者要人确认指哪份，后者是明确的坏引用。
          if (!target) { ambiguous.push({ where: `${rel}:${i + 1}`, ref: `${path}:${m[3]}`, cands }); continue }
        }
      } else {
        // 裸文件名：同名可能有多份，需消歧
        const cands = byName.get(base) ?? []
        if (cands.length === 0) {
          // 实现文件找不到才看测试文件（文档偶尔引用测试里的辅助函数）
          const tests = testIdx.get(base) ?? []
          if (tests.length) { target = tests[0]; continue }
          missing.push({ where: `${rel}:${i + 1}`, ref: `${path}:${m[3]}` }); continue
        }
        if (cands.length === 1) target = cands[0]
        else {
          const fits = cands.filter(c => (lineCount.get(c) ?? 0) >= hi)
          if (fits.length === 1) target = fits[0]
          else if (fits.length > 1) {
            // 多份都能容纳。用**上下文消歧**：同一行/上文若已出现某个包名（controller/
            // transport/ service/ ...），取包含该包的那份。取不到就报歧义，别猜。
            // ⚠️ 早先用 text.slice(0, text.indexOf(line)) 取上文：文档里出现重复行时
            // indexOf 永远返回首次出现的位置，上下文被算成同一段。改为按行号直接切片。
            // 消歧：最近 40 行内出现过的包名，取包含它的候选。
            // 越靠近当前行权重越高（倒序找第一个命中就停）。
            const win = linesArr.slice(Math.max(0, i - 40), i + 1)
            const PKGS = ['controller','transport','service','repository','model','source',
                          'provider','router','peerjs','signalserver','media-node','echclient',
                          'peersignal','downloader','config','serverapp','regserver','panel']
            let target = null
            for (let k = win.length - 1; k >= 0 && !target; k--) {
              for (const pk of PKGS) {
                if (win[k].includes(pk + '/') || win[k].includes(pk + '.')) {
                  const hit = fits.find(c => c.includes(`/${pk}/`))
                  if (hit) { target = hit; break }
                }
              }
            }
            if (!target) ambiguous.push({ where: `${rel}:${i + 1}`, ref: `${path}:${m[3]}`, cands: fits })
            continue
          } else { ambiguous.push({ where: `${rel}:${i + 1}`, ref: `${path}:${m[3]}`, cands }); continue }
        }
      }
      const total = lineCount.get(target)
      if (total !== undefined && hi > total) {
        outOfRange.push({ where: `${rel}:${i + 1}`, ref: `${path}:${m[2]}`, target, hi, total })
      }
    }
  })
}

// 已知豁免：这些条目**故意**指向不存在的路径，理由写在文件里，不算回归。
//   · design/*-DSH-*  —— 提案文档，bundles/ harness/ 是目标结构不是现状（文件头已标注）
//   · NETDISK / REFACTOR / testing —— 历史记录里提到的已删除文件（已就地标注）
//   · operation-manual      —— 重写说明里列的旧路径清单
// 新问题若落在这些文件里，仍然会被报出来（豁免按「行内是否已有标注」判定太脆，
// 这里按整文件豁免，代价是这几个文件不再受检查——已在 README 注明）。
const EXEMPT = new Set([
  'doc/design/FRONTEND-DSH-INSPIRED.md',
  'doc/design/FRONTEND-DSH-KERNEL.md',
  'doc/design/PEERDRIVE-DSH-INSPIRED.md',
  'doc/guide/operation-manual.md',
  'doc/NETDISK.md',
  'doc/REFACTOR.md',
  'doc/testing/README.md',
  // 这份文档的正文**故意**列举「上一版列的 151 个路径已不存在」，
  // 那些失效路径本身就是内容，不能算回归。
  'doc/FILE-REFERENCE.md',
  // 已删除目录的原文清单（文件里已就地标注「已删除」，保留作重构追溯）
  'doc/guide/FRONTEND.md',
  'doc/layers/L8-frontend/pages.md',
])
const realMissing = missing.filter(r => !EXEMPT.has(r.where.split(':')[0]))
const realAmbiguous = ambiguous.filter(r => !EXEMPT.has(r.where.split(':')[0]))

const show = (title, rows) => {
  if (!rows.length) return
  console.log(`\n${title}（${rows.length}）`)
  for (const r of rows.slice(0, MAX_LINES)) {
    console.log(`  ✗ ${r.where}  ${r.ref}` + (r.target ? `  → ${r.target} 只有 ${r.total} 行` : '') +
      (r.cands ? `  → 候选 ${r.cands.join(' / ')}` : ''))
  }
  if (rows.length > MAX_LINES) console.log(`  …… 另有 ${rows.length - MAX_LINES} 条`)
}

console.log(`扫描 ${docs.length} 个文档（已跳过 archive）`)
console.log(`  路径缺失 ${realMissing.length} · 行号越界 ${outOfRange.length} · 同名歧义 ${realAmbiguous.length}` +
  `   （已豁免 ${EXEMPT.size} 个有意保留失效引用的文档）`)
show('路径缺失（需修）', realMissing)
show('行号越界（需修）', outOfRange)
if (process.env.ALL) show('同名文件歧义（裸文件名简写，需人工确认）', realAmbiguous)
else if (realAmbiguous.length) console.log(`  （另有 ${realAmbiguous.length} 处裸文件名简写无法自动定位，ALL=1 展开）`)

const bad = realMissing.length + outOfRange.length
if (bad && !WARN_ONLY) {
  console.log(`\nRESULT: ${bad} 处需要处理`)
  process.exit(1)
}
console.log('\nRESULT: 无越界与缺失')