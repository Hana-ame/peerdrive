// verify-panel-share.mjs — 真浏览器验证「固定身份 + 三档共享级别 + 分享链接」。
//
// 和 verify-panel.mjs 的分工：那边验"能不能用"（连得上、拉得到、sha256 一致），
// 这边验"给谁看"这条策略链真的接上了——三档级别最容易假装在生效：
//   · unlisted 只要在清单里露一次脸，语义就废了；
//   · private 只要没接到 req 上，就是"设了等于没设"；
//   · 面板 id 只要每次随机，好友名单就永远匹配不上（private 等于谁都不给）。
// 这三条在单测里都测不到（单测没有真实浏览器、没有真实 PeerJS 握手），只能端到端钉。
//
// 前置：
//   1. `npm run build:panel`（产物必须最新）
//   2. 一个在跑的 peerdrive 节点 + 自托管信令（./scripts/netdisk-local-demo.sh，
//      或自己起：见下面环境变量）
//   3. 节点共享目录里要有一个名字含 TARGET_NAME 的文件，并已 register_folder
//   4. playwright-core + 一个 Chromium 内核浏览器（PW_CHANNEL=default 用自带内核）
//
// 用法:
//   NODE_PORT=3001 SIG_PORT=9100 NODE_ID=node-a PW_CHANNEL=default \
//     node scripts/verify-panel-share.mjs
//   可选：TARGET_NAME=secret（默认 secret，用来在候选清单里挑目标文件）
//         PANEL_FILE=<产物绝对路径>

import { createRequire } from 'node:module'
import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// playwright-core 不在本包依赖里（client 包保持零依赖），按与 verify-panel.mjs
// 相同的方式解析：脚本所在目录 → 当前工作目录。
let chromium
try {
  ;({ chromium } = await import('playwright-core'))
} catch (e) {
  try {
    const req0 = createRequire(join(process.cwd(), 'noop.cjs'))
    ;({ chromium } = req0('playwright-core'))
  } catch (e2) {
    console.error('需要 playwright-core：npm i -D playwright-core，或在已装它的目录下运行')
    process.exit(2)
  }
}

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const PANEL = process.env.PANEL_FILE || join(ROOT, 'dist', 'panel.html')
const NODE = process.env.NODE_ID || 'node-a'
const NODE_PORT = process.env.NODE_PORT || '3001'
const SIG_PORT = process.env.SIG_PORT || '9100'
const SIG_KEY = process.env.SIG_KEY || 'peerjs'
const TARGET_NAME = process.env.TARGET_NAME || 'secret'
const CHANNEL = process.env.PW_CHANNEL || 'default'

const API = `http://127.0.0.1:${NODE_PORT}`
let pass = 0
let fail = 0
const ok = (m) => { pass++; console.log('  PASS ' + m) }
const bad = (m) => { fail++; console.log('  FAIL ' + m) }

const put = (body) => fetch(`${API}/peerjs/share`, {
  method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
}).then((r) => r.json())
const get = () => fetch(`${API}/peerjs/share`).then((r) => r.json())

// waitLog 在面板日志里等一句话（面板的结果全落在这块日志里，比查 DOM 稳）
async function waitLog(page, needle, ms = 40000) {
  const t0 = Date.now()
  while (Date.now() - t0 < ms) {
    const txt = await page.evaluate(() => document.querySelector('#log')?.textContent || '')
    if (txt.includes(needle)) return true
    await page.waitForTimeout(500)
  }
  return false
}

const scope0 = await get()
const target = (scope0.files || []).find((f) => String(f.name || '').includes(TARGET_NAME))
if (!target) {
  console.error(`候选清单里没有名字含 "${TARGET_NAME}" 的文件：` + JSON.stringify(scope0).slice(0, 300))
  process.exit(2)
}
console.log(`  目标文件：${target.name}  ${target.hash.slice(0, 16)}…`)

// 只留单文件勾选（清掉目录来源）：合并规则是取最宽松，目录那条 public 会把
// unlisted 顶成 public，这一轮就验不出东西了。
await put({ enable: true, dirs: [], files: [{ id: target.hash, level: 'unlisted' }] })

const launchOpts = { headless: true }
if (CHANNEL && CHANNEL !== 'default' && CHANNEL !== 'none') launchOpts.channel = CHANNEL
const browser = await chromium.launch(launchOpts)
try {
  const page = await browser.newPage({ acceptDownloads: true })
  const url = `file://${PANEL}?node=${NODE}&host=127.0.0.1&port=${SIG_PORT}` +
    `&path=/&key=${SIG_KEY}&secure=0&auto=1&hash=${target.hash}`
  await page.goto(url, { waitUntil: 'load', timeout: 60000 })
  await page.waitForFunction(() => typeof window.Peer !== 'undefined', null, { timeout: 30000 })

  // 1. 固定身份：private 的好友名单认的是这个 id
  const myId = await page.evaluate(() => window.__panel.myId())
  if (/^pd-panel-/.test(myId || '')) ok('面板有固定 id：' + myId)
  else bad('id 不是 pd-panel- 前缀：' + myId)

  if (await waitLog(page, '已建立 WebRTC 直连')) ok('已连上节点')
  else bad('没连上节点（看节点与信令日志）')

  // 2. unlisted：清单里没有它，但链接给了取回入口
  const inList = await page.evaluate(() => document.querySelector('#shares')?.textContent || '')
  if (!inList.includes(TARGET_NAME)) ok('unlisted 的内容不出现在共享清单里')
  else bad('unlisted 却出现在清单里')

  await page.waitForSelector('#btn-linked-get', { timeout: 20000 })
  ok('链接带来的 hash 有单独取回入口（unlisted 的出口）')
  await page.click('#btn-linked-get')
  if (await waitLog(page, 'sha256 已校验')) ok('unlisted：凭链接取回成功')
  else bad('unlisted 取回失败')

  // 3. private：陌生人被拒
  await put({ files: [{ id: target.hash, level: 'private' }] })
  await page.click('#btn-linked-get')
  if (await waitLog(page, '拉取失败')) ok('private：陌生人被拒')
  else bad('private 没拦住陌生人')

  // 4. 固定 id 进好友名单 → 同一个页面、同一条链接立刻能取
  await put({ friends: [myId] })
  await page.click('#btn-linked-get')
  if (await waitLog(page, 'sha256 已校验')) ok('private：好友（固定 id 进了名单）能取回')
  else bad('好友仍取不到 private')
} finally {
  await browser.close()
}
console.log(`\n  => ${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
