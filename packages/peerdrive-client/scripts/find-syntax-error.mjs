// find-syntax-error.mjs — 定位 client.js 的 "missing ) after argument list"。
//
// 为什么不用 node --check：它报的行列号是"引擎放弃解析的位置"，不是真正缺括号的地方，
// 指向的往往是下游第一个被动 token。本脚本逐字符维护 括号栈 / 三种引号 / 两种注释 状态，
// 第一次失配或扫描结束时仍未闭合的位置就是真正的问题点。
//
// 用法: node scripts/find-syntax-error.mjs [文件]

import { readFileSync } from 'node:fs'
import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const target = resolve(ROOT, process.argv[2] || 'src/client.js')
const src = readFileSync(target, 'utf8')

const OPEN = '([{'
const CLOSE = { ')': '(', ']': '[', '}': '{' }
const stack = []
let line = 1
let col = 0
let sq = null // 单/双引号
let tpl = 0 // 模板字符串嵌套深度（`${}` 内可有字符串）
let block = false
let lineComment = false
const problems = []

// 模板字符串里 ${ } 的花括号要当括号处理，但它内部又可能嵌字符串 —— 用一个哨兵记状态。
// 简化：把 ${ 之间的内容当普通代码扫，遇到未配平的 } 时若在模板内则回退。
function mark(kind) {
  return `line ${line} col ${col} (${kind})`
}

for (let i = 0; i < src.length; i++) {
  const ch = src[i]
  const nx = src[i + 1]
  col++
  if (ch === '\n') { line++; col = 0; lineComment = false; continue }

  if (block) { if (ch === '*' && nx === '/') { block = false; i++; col++ } continue }
  if (lineComment) continue

  if (sq) {
    if (ch === '\\') { i++; col++; continue }
    if (ch === sq) sq = null
    continue
  }
  if (tpl > 0) {
    // 模板内：只关心 ${ 的花括号配平与嵌套模板
    if (ch === '\\') { i++; col++; continue }
    if (ch === '`') { tpl++; continue }
    if (ch === '$' && nx === '{') { stack.push({ ch: '{', line, col, tpl: true }); i++; col++; continue }
    if (ch === '}' && stack.length && stack[stack.length - 1].tpl) { stack.pop(); continue }
    if (OPEN.includes(ch)) stack.push({ ch, line, col })
    if (CLOSE[ch]) {
      const t = stack.pop()
      if (!t || t.ch !== CLOSE[ch]) {
        problems.push(`失配：第 ${line} 行第 ${col} 列遇到 '${ch}'，但栈顶是 ${t ? `'${t.ch}' @ ${t.line}:${t.col}` : '空'}`)
        break
      }
    }
    continue
  }

  if (ch === '/' && nx === '*') { block = true; i++; col++; continue }
  if (ch === '/' && nx === '/') { lineComment = true; i++; col++; continue }
  if (ch === '"' || ch === "'") { sq = ch; continue }
  if (ch === '`') { tpl = 1; continue }
  if (OPEN.includes(ch)) { stack.push({ ch, line, col }); continue }
  if (CLOSE[ch]) {
    const t = stack.pop()
    if (!t || t.ch !== CLOSE[ch]) {
      problems.push(`失配：第 ${line} 行第 ${col} 列遇到 '${ch}'，但栈顶是 ${t ? `'${t.ch}' @ ${t.line}:${t.col}` : '空'}`)
      break
    }
  }
}

if (problems.length) {
  for (const p of problems) console.log(p)
} else if (stack.length) {
  console.log(`扫描到文件末尾，${stack.length} 个未闭合：`)
  for (const t of stack.slice(0, 8)) console.log(`  '${t.ch}' 开启于 ${t.line}:${t.col}`)
} else if (sq) {
  console.log(`括号配平，但有未闭合的引号 '${sq}'（起始行未记录，扫描结束于 ${mark('end')}）`)
} else if (block) {
  console.log('括号配平，但有未闭合的块注释')
} else {
  console.log('括号 / 引号 / 注释全部配平 —— 语法错在别处（可能是可选链、?. 之类被改坏）')
}
