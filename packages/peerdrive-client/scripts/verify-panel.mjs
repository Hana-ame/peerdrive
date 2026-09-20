// verify-panel.mjs — 用真实浏览器端到端验证公共面板（dist/panel.html）。
//
// 为什么需要它：面板是**单文件、file:// 打开**的形态，单元测试碰不到它；
// 而它踩过的坑（peerjs CDN 没加载 / 信令没开 CORS / 把自己 id 显示成对端）
// 全都只在真浏览器里才暴露。这个脚本用 file:// 打开产物、驱动真实点击，
// 断言「连上 → 清单 → 保存 → 预览 → sha256 一致」全链路。
//
// 前置：
//   1. 一个在跑的 peerdrive 节点 + 自托管信令（推荐 ./scripts/netdisk-local-demo.sh）
//   2. `npm run build:panel`（产物必须是最新的）
//   3. playwright-core + 一个 Chromium 内核浏览器（默认复用本机 Edge）：
//        npm i -D playwright-core        # 或全局装
//        默认 channel=msedge；Linux/CI 上可用 PW_CHANNEL=chromium
//
// 用法:
//   SIG_HOST=172.29.89.192 SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs
//   可选：SIG_PATH=/ SIG_KEY=peerjs SIG_SECURE=0 PW_CHANNEL=msedge PANEL_FILE=<绝对路径>

import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const SIG = {
  host: process.env.SIG_HOST || '127.0.0.1',
  port: Number(process.env.SIG_PORT || 9100),
  path: process.env.SIG_PATH || '/',
  key: process.env.SIG_KEY || 'peerjs',
  secure: process.env.SIG_SECURE === '1',
}
const NODE_ID = process.env.NODE_ID || 'node-a'
const CHANNEL = process.env.PW_CHANNEL || 'msedge'
const PANEL = process.env.PANEL_FILE || join(ROOT, 'dist', 'panel.html')

// playwright-core 不在本包依赖里（client 包保持零依赖）。这里按「脚本所在目录
// 向上找 → 当前工作目录向上找」两种方式解析，谁先命中用谁：
// 本包没有 node_modules，把它装在别的目录（例如 npm i -g playwright-core 后
// cd 到那个目录）也能跑。
let chromium
try {
  ;({ chromium } = await import('playwright-core'))
} catch (e) {
  try {
    const { createRequire } = await import('node:module')
    const req = createRequire(join(process.cwd(), 'noop.cjs'))
    ;({ chromium } = req('playwright-core'))
  } catch (e2) {
    console.error('需要 playwright-core：npm i -D playwright-core，或在已装它的目录下运行')
    process.exit(2)
  }
}

const url =
  'file://' + PANEL +
  `?node=${NODE_ID}&host=${SIG.host}&port=${SIG.port}&path=${encodeURIComponent(SIG.path)}` +
  `&key=${SIG.key}&secure=${SIG.secure ? 1 : 0}&auto=1`

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let failed = 0
const ok = (m) => console.log('  PASS ' + m)
const bad = (m) => { console.log('  FAIL ' + m); failed++ }

console.log('== 打开 ' + url)
const browser = await chromium.launch({ channel: CHANNEL, headless: true })
const page = await browser.newPage({ acceptDownloads: true })
const logs = []
page.on('console', (m) => logs.push(`[${m.type()}] ${m.text()}`))
page.on('pageerror', (e) => logs.push(`[pageerror] ${e.message}`))
await page.goto(url)

// [1] peerjs 加载（CDN 回退链 / 同目录 vendored 副本）
let peerLoaded = false
for (let i = 0; i < 30; i++) {
  peerLoaded = await page.evaluate(() => typeof window.Peer !== 'undefined')
  if (peerLoaded) break
  await sleep(500)
}
peerLoaded ? ok('peerjs 已加载') : bad('window.Peer 未就绪（CDN 或同目录副本都取不到）')

// [2] 内联 bundle 的 API 面
const apiCount = await page.evaluate(() => Object.keys(window.PeerDrive || {}).length)
apiCount >= 20 ? ok(`内联 bundle 暴露 ${apiCount} 个 API`) : bad(`bundle API 异常：${apiCount}`)

// [3] auto=1 自动连接 + 清单渲染
let rows = 0
try {
  await page.waitForSelector('#shares tbody tr', { timeout: 40000 })
  rows = await page.locator('#shares tbody tr').count()
} catch (e) { /* 统一报 */ }
rows > 0 ? ok(`自动连接成功（${NODE_ID}），清单 ${rows} 行`) : bad('#shares 没有清单行')
if (rows > 0) console.log('  清单: ' + (await page.locator('#shares').innerText()).replace(/\s+/g, ' ').slice(0, 160))

// [4] 「我的临时 id」必须是本端，不是对端（第一版就错在这）
const myId = (await page.locator('#my-id').innerText()).trim()
myId && myId !== NODE_ID ? ok(`我的临时 id = ${myId}（非对端 id）`) : bad(`我的临时 id 显示成了 ${myId}`)

// [5] 点「保存」→ 真的触发下载 + 任务转「完成」
if (rows > 0) {
  const dl = page.waitForEvent('download', { timeout: 30000 }).catch(() => null)
  await page.locator('#shares tbody tr').first().locator('button', { hasText: '保存' }).click()
  const download = await dl
  download ? ok(`点击「保存」触发下载：${download.suggestedFilename()}`) : bad('点击「保存」没有触发下载')
  let taskDone = false
  for (let i = 0; i < 30; i++) {
    if (/完成/.test(await page.locator('#tasks').innerText())) { taskDone = true; break }
    await sleep(500)
  }
  taskDone ? ok('传输任务列表显示「完成」') : bad('传输任务没有变成完成')

  // [6] 点「预览」→ 渲染出内容
  await page.locator('#shares tbody tr').first().locator('button', { hasText: '预览' }).click()
  let peek = ''
  for (let i = 0; i < 20; i++) {
    peek = await page.locator('#preview').innerText()
    if (peek && !/加载预览/.test(peek)) break
    await sleep(400)
  }
  peek.trim() ? ok('预览有内容：' + peek.trim().slice(0, 40)) : bad('预览没渲染出内容')
}

// [7] API 层独立再验一次 sha256（不经 UI）
const pulled = await page.evaluate(async (sig) => {
  const PD = window.PeerDrive
  const c = await PD.connectToPeer(window.Peer, sig.node, { peerOptions: sig.opts, idleTimeoutMs: 60000 })
  const snap = await c.shares()
  const f = snap.files[0]
  const bytes = await c.fetch(f.hash)
  const hex = await PD.sha256Hex(bytes)
  c.close()
  return { name: f.name, size: bytes.byteLength, expect: f.hash, got: hex }
}, { node: NODE_ID, opts: SIG }).catch((e) => ({ err: String(e && e.message || e) }))
pulled.err
  ? bad('API 拉取失败：' + pulled.err)
  : pulled.got === pulled.expect
    ? ok(`拉取 ${pulled.name}（${pulled.size}B）sha256 与清单一致`)
    : bad(`sha256 不一致 got=${pulled.got} want=${pulled.expect}`)

console.log('\n== 页面日志（尾部）')
for (const l of logs.slice(-10)) console.log('  ' + l)

await browser.close()
console.log(failed === 0 ? '\nRESULT: ALL PASS' : `\nRESULT: ${failed} FAILED`)
process.exit(failed === 0 ? 0 : 1)
