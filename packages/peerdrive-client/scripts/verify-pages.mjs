// verify-pages.mjs — 验证**线上**（GitHub Pages）的公共面板能正常用。
//
// 和 verify-panel.mjs 的分工：
//   verify-panel.mjs  验本地产物（file://）→ 连节点 → 拉文件 → 校验 sha256（功能全链路）
//   verify-pages.mjs  验线上托管本身（HTTPS 源）→ 页面骨架 / bundle 注入 /
//                     peerjs 能否取到 / 混合内容守卫是否生效（部署链路）
//
// 为什么线上要单独验：多出来的是「HTTPS + 第三方托管」这两个变量 ——
//   - Pages 的构建流程会不会动到产物（.nojekyll、路径）；
//   - peerjs 是线上唯一的外部依赖，CDN 被墙/超时就整页不可用；
//   - HTTPS 源上 ws:// 信令必被混合内容拦截，面板必须给出明确提示而不是"点了没反应"。
// 这三条本地验不到，而它们恰好是上线后最可能出问题的地方。
//
// 前置：playwright-core + 一个 Chromium 内核浏览器（默认复用本机 Edge）。
// 用法:
//   node scripts/verify-pages.mjs                       # 默认验 https://hana-ame.github.io/peerdrive/
//   node scripts/verify-pages.mjs https://example.com/  # 验别的部署
//   PW_CHANNEL=chromium node scripts/verify-pages.mjs   # Linux/CI

const URL = process.argv[2] || process.env.PANEL_URL || 'https://hana-ame.github.io/peerdrive/'
const CHANNEL = process.env.PW_CHANNEL || 'msedge'

// playwright-core 不在本包依赖里（client 包保持零依赖），按与 verify-panel.mjs
// 相同的方式解析：脚本所在目录 → 当前工作目录。
let chromium
try {
  ;({ chromium } = await import('playwright-core'))
} catch (e) {
  try {
    const { createRequire } = await import('node:module')
    const req = createRequire(process.cwd().replace(/\\/g, '/') + '/noop.cjs')
    ;({ chromium } = req('playwright-core'))
  } catch (e2) {
    console.error('需要 playwright-core：npm i -D playwright-core，或在已装它的目录下运行')
    process.exit(2)
  }
}

let pass = 0
let fail = 0
const ok = (m) => { pass++; console.log('  PASS ' + m) }
const bad = (m) => { fail++; console.log('  FAIL ' + m) }

// PW_CHANNEL=default/none 时不传 channel，用 playwright 自带的 chromium
const launchOpts = { headless: true }
if (CHANNEL && CHANNEL !== 'default' && CHANNEL !== 'none') launchOpts.channel = CHANNEL
const browser = await chromium.launch(launchOpts)
try {
  const page = await browser.newPage()
  const errors = []
  page.on('pageerror', (e) => errors.push(String(e)))
  await page.goto(URL, { waitUntil: 'load', timeout: 45000 })

  // 1. 页面骨架：Pages 有可能把别的目录当根发布出去，先确认拿到的是面板
  if (await page.locator('#in-node').count()) ok('面板骨架已渲染（节点 id 输入框存在）')
  else bad('面板骨架没渲染出来 —— 线上拿到的可能不是面板产物')

  // 2. bundle 注入：产物里的 client 源码是否完整可用
  await page.waitForFunction(() => !!window.PeerDrive, null, { timeout: 15000 }).catch(() => {})
  const apiCount = await page.evaluate(() => (window.PeerDrive ? Object.keys(window.PeerDrive).length : 0))
  if (apiCount >= 20) ok(`client bundle 已注入（${apiCount} 个 API）`)
  else bad(`bundle 没注入或导出不全（${apiCount} 个）`)

  // 3. peerjs —— 线上唯一的外部依赖
  await page.waitForFunction(() => typeof window.Peer !== 'undefined', null, { timeout: 20000 }).catch(() => {})
  const hasPeer = await page.evaluate(() => typeof window.Peer !== 'undefined')
  if (hasPeer) ok('peerjs 已加载（CDN 可达）')
  else bad('peerjs 没加载 —— CDN 不可达，需要先 npm run vendor:peerjs 并一起发布')

  // 4. 混合内容守卫：HTTPS 源 + ws 信令（secure=0）必须明确提示
  const p2 = await browser.newPage()
  await p2.goto(URL + (URL.includes('?') ? '&' : '?') + 'secure=0', { waitUntil: 'load', timeout: 45000 })
  await p2.waitForTimeout(1500)
  const warned = await p2.evaluate(() =>
    (document.querySelector('#log')?.textContent || '').includes('混合内容'),
  )
  if (warned) ok('HTTPS + ws:// 时给出「混合内容」提示')
  else bad('HTTPS + ws:// 没有提示 —— 用户会卡在「连不上但没报错」')
  await p2.close()

  if (errors.length) bad('页面有 JS 报错: ' + errors.slice(0, 3).join(' | '))
  else ok('无 JS 运行时错误')

  if (process.env.SCREENSHOT !== '0') {
    await page.screenshot({ path: 'pages-verify.png', fullPage: true })
    console.log('  （截图 pages-verify.png）')
  }
} finally {
  await browser.close()
}

console.log(`\n${URL}  =>  ${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
