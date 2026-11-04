// verify-panel-zero-config.mjs —— 验证「零输入面板」：浏览器只打开 /panel，
// 不带任何 URL 参数，不手填任何东西，节点清单应该自己出来。
//
// 为什么单独写：verify-panel.mjs 总是自己拼 node/host/port 进去，
// 跑绿了只说明「参数齐全时能用」，证明不了零学习成本那一步。
// 这个脚本刻意**一个参数都不给**——不给就是不给，脚本里没有任何兜底拼接。
//
// 需要：playwright-core（client 包零依赖，故意不在本包依赖里）。
//   npm i -D playwright-core   或在已装它的目录下运行
const { join } = await import('node:path')
const ROOT = join(import.meta.dirname, '..')

let chromium
try {
  ;({ chromium } = await import('playwright-core'))
} catch (e) {
  try {
    const { createRequire } = await import('node:module')
    const req = createRequire(join(process.cwd(), 'noop.cjs'))
    ;({ chromium } = req('playwright-core'))
  } catch (e2) {
    console.error('需要 playwright-core')
    process.exit(2)
  }
}

// 默认打本机 3000 —— 与 `peerdrive serve` 的默认端口一致。
const PANEL_URL = process.env.PANEL_URL || 'http://127.0.0.1:3000/panel'
const TIMEOUT = Number(process.env.TIMEOUT || 45000)

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let failed = 0
const ok = (m) => console.log('  PASS ' + m)
const bad = (m) => { console.log('  FAIL ' + m); failed++ }

const browser = await chromium.launch({ channel: process.env.PW_CHANNEL || 'msedge' })
const page = await browser.newPage()

const errors = []
page.on('pageerror', (e) => errors.push(String(e)))

try {
  // 刻意裸地址打开：一个查询参数都不给。
  console.log('打开（零参数）: ' + PANEL_URL)
  await page.goto(PANEL_URL, { waitUntil: 'domcontentloaded', timeout: 30000 })

  // 等注入脚本把 URL 补全：node 参数来自 /peerjs/node 反查。
  const deadline = Date.now() + TIMEOUT
  let sawNodeParam = false
  while (Date.now() < deadline) {
    if (new URL(page.url()).searchParams.get('node')) { sawNodeParam = true; break }
    await sleep(300)
  }
  if (sawNodeParam) ok('注入脚本补上了 node 参数（零输入自动发现）')
  else bad(`没等到 node 参数自动补全（超时 ${TIMEOUT}ms）`)

  // 真连接：点「自动搜索」向信令问在线节点。
  //
  // ⚠️ 早先这里用 /已连接|在线/ 匹配正文，结果 **永远绿**：
  // 「在线节点」「连接节点」这些**标签文字**本身就命中了这条正则，
  // 面板其实一条都没连上，测试照样 PASS。第三次「永远绿的测试」。
  // 现在改成「搜出来的节点 id 必须等于本节点的 id」——标签文字匹配不上它。
  await page.evaluate(() => {
    const b = [...document.querySelectorAll('button')].find((x) => /自动搜索/.test(x.textContent || ''))
    if (b) b.click()
  })

  let found = false
  const dl = Date.now() + TIMEOUT
  while (Date.now() < dl) {
    const txt = (await page.textContent('body').catch(() => '')) || ''
    // 比对 node 参数那个 id，**而不是**去匹配某个 id 形态。
    // 早先写死 /peerdrive-[0-9a-f]{6,}/ 是因为默认 id 形如 peerdrive-64373d50；
    // 但节点也可以用 PEERDRIVE_PEERJS_ID 指定成任意字符串
    // （e2e.yml 里就是 node-a / node-zero），那种配置下这条正则永远匹配不上 ——
    // 用例会变成「只有默认 id 才绿」的地雷。
    // 正确口径：搜索结果里必须出现 URL 上那个**实际的** node id。
    const want = new URL(page.url()).searchParams.get('node')
    if (want && txt.includes(want)) { found = true; break }
    await sleep(500)
  }
  if (found) ok('自动搜索列出了本节点（信令链路真通，不是标签文字命中）')
  else bad('自动搜索没列出本节点')

  if (errors.length) bad('页面报错: ' + errors.slice(0, 2).join(' | '))
  else ok('无页面级 JS 错误')

  console.log('RESULT: ' + (failed ? failed + ' FAILED' : 'ALL PASS'))
} catch (e) {
  bad('异常: ' + (e && e.message))
  console.log('RESULT: ' + failed + ' FAILED')
} finally {
  await browser.close().catch(() => {})
}
process.exit(failed ? 1 : 0)