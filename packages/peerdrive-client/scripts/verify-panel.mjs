// verify-panel.mjs — 用真实浏览器端到端验证公共面板（dist/panel.html）。
//
// 为什么需要它：面板是**单文件、file:// 打开**的形态，单元测试碰不到它；
// 而它踩过的坑（peerjs CDN 没加载 / 信令没开 CORS / 把自己 id 显示成对端）
// 全都只在真浏览器里才暴露。这个脚本用 file:// 打开产物、驱动真实点击，
// 断言全链路：
//   读：连上 → 清单 → 保存 → 预览 → sha256 一致
//   写：自动搜索在线节点 → 本地文件入库 → 按 hash 取回校验
//       → 网络入库（内网地址必须被 SSRF 防护拒 / 给了 PULL_URL 就真抓一次）
//
// 前置：
//   1. 一个在跑的 peerdrive 节点 + 自托管信令（推荐 ./scripts/netdisk-local-demo.sh）
//      自动搜索这一步额外要求信令能回 CORS 头（不然只能是手工填 id）
//   2. `npm run build:panel`（产物必须是最新的）
//   3. playwright-core + 一个 Chromium 内核浏览器（默认复用本机 Edge）：
//        npm i -D playwright-core        # 或全局装
//        默认 channel=msedge；Linux/CI 上可用 PW_CHANNEL=chromium
//
// 用法:
//   SIG_HOST=172.29.89.192 SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs
//   可选：SIG_PATH=/ SIG_KEY=peerjs SIG_SECURE=0 PW_CHANNEL=msedge PANEL_FILE=<绝对路径>
//         PULL_URL=<一个节点能访问的公网 URL>  给了才会跑"真的抓一次网络入库"
//         PUT_FILE=<一个本地文件>              不给就当场生成一份随机内容

import { resolve, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { writeFileSync, readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { createHash } from 'node:crypto'

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

// PANEL_URL 给的是 http(s) 地址时走「托管模式」（例如 npm run serve:panel 起的内网服务）。
// 这个模式的价值：http 页面发起 ws:// 不会被混合内容拦截，所以内网用 ws 信令就行，
// 不必给信令配 TLS —— 只有 HTTPS 托管的面板（如 GitHub Pages）才必须 wss。
// PSK：节点开了预共享密钥门禁时用它（面板会从链接读入，然后立刻从地址栏抹掉）。
// 自检时带上它，就能顺带验证"带密钥能过门禁"这条路径。
const PSK = process.env.PSK || ''
// PULL_URL：给一个节点能访问的公网 URL 时，会真的跑一次「网络入库」并检查 hash。
// 不写就只验 SSRF 拒绝路径（那条是确定性的，不需要外网）。
const PULL_URL = process.env.PULL_URL || ''
const BASE = process.env.PANEL_URL || 'file://' + PANEL
const url =
  BASE +
  (BASE.includes('?') ? '&' : '?') +
  `node=${NODE_ID}&host=${SIG.host}&port=${SIG.port}&path=${encodeURIComponent(SIG.path)}` +
  `&key=${SIG.key}&secure=${SIG.secure ? 1 : 0}&auto=1` +
  (PSK ? `&psk=${encodeURIComponent(PSK)}` : '')

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let failed = 0
const ok = (m) => console.log('  PASS ' + m)
const bad = (m) => { console.log('  FAIL ' + m); failed++ }
const note = (m) => console.log('  note ' + m)

/** logText 取日志全文；面板把每个动作的成败都写进了 #log，UI 之外的判断都看它。 */
const logText = () => page.locator('#log').innerText()

/** waitLog 轮询日志直到出现期望文本（比 sleep 死等稳，也比少给时间少错报）。 */
async function waitLog(re, timeoutMs = 45000) {
  const end = Date.now() + timeoutMs
  for (;;) {
    const txt = await logText().catch(() => '')
    const m = txt.match(re)
    if (m) return m
    if (Date.now() > end) return null
    await sleep(400)
  }
}

console.log('== 打开 ' + url)
// PW_CHANNEL=default/none 时不传 channel，用 playwright 自带的 chromium
// （CI 上装的是 bundled chromium，没有 Edge，写死 msedge 必然启动失败）
const launchOpts = { headless: true }
if (CHANNEL && CHANNEL !== 'default' && CHANNEL !== 'none') launchOpts.channel = CHANNEL
const browser = await chromium.launch(launchOpts)
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
//   优先复用面板已建立的连接（window.__panel）；拿不到才自己拨一次。
//   复用而非重拨的原因：CI 上同一台机器的第二次 WebRTC 握手并不总是成功，
//   而这一步要验的是「取回来的内容 sha256 对不对」，不是「能不能再连一次」。
const pulled = await page.evaluate(async (sig) => {
  const PD = window.PeerDrive
  const s = window.__panel && window.__panel.get(sig.node)
  const c = (s && s.client) ||
    (await PD.connectToPeer(window.Peer, sig.node, {
      peerOptions: sig.opts, idleTimeoutMs: 60000, verbTimeoutMs: 45000,
    }))
  const snap = await c.shares({ timeoutMs: 45000 })
  const f = snap.files[0]
  const bytes = await c.fetch(f.hash)
  const hex = await PD.sha256Hex(bytes)
  return { name: f.name, size: bytes.byteLength, expect: f.hash, got: hex, reused: !!(s && s.client) }
}, { node: NODE_ID, opts: SIG }).catch((e) => ({ err: String(e && e.message || e) }))
pulled.err
  ? bad('API 拉取失败：' + pulled.err)
  : pulled.got === pulled.expect
    ? ok(`拉取 ${pulled.name}（${pulled.size}B）sha256 与清单一致${pulled.reused ? '（复用面板连接）' : '（新拨连接）'}`)
    : bad(`sha256 不一致 got=${pulled.got} want=${pulled.expect}`)

// [8] 自动搜索在线节点：向信令问「现在谁在线」，结果要渲染成可点的列表
await page.locator('#btn-discover').click()
let found = 0
for (let i = 0; i < 40; i++) {
  found = await page.locator('#discovered .node').count()
  if (found) break
  await sleep(400)
}
found > 0
  ? ok(`自动搜索到 ${found} 个在线节点：${(await page.locator('#discovered').innerText()).replace(/\s+/g, ' ').slice(0, 90)}`)
  : bad('自动搜索没有列出任何节点（日志：' + (await waitLog(/自动搜索/, 0) ? (await logText()).split('\n').filter((l) => /自动搜索/.test(l)).join(' | ') : '无') + '）')

// [9] 本地入库：选一个本地文件 → upload 动词 → 拿回 sha256 → 按 hash 取回校验
//     文件名带随机串，避免重复跑时撞上同名条目（内容寻址下同名不同内容是两份）。
const putFile = process.env.PUT_FILE || (() => {
  const dir = mkdtempSync(join(tmpdir(), 'pdput-'))
  const p = join(dir, 'upload-' + Date.now().toString(36) + '.txt')
  writeFileSync(p, 'peerdrive 面板本地入库自检内容 ' + Date.now() + '\n'.repeat(3))
  return p
})()
const putBytes = readFileSync(putFile)
const putExpect = createHash('sha256').update(putBytes).digest('hex')
await page.setInputFiles('#in-file', putFile)
await page.locator('#btn-put').click()
const putDone = await waitLog(/入库完成：.*→ sha256 ([0-9a-f]{64})/, 60000)
putDone
  ? ok(`本地入库完成 ${putDone[1].slice(0, 12)}…（${putBytes.length}B，方才的文件选择器=${putFile.split(/[\\/]/).pop()}）`)
  : bad('本地入库没有走到「入库完成」（见日志尾部）')
if (putDone) {
  putDone[1] === putExpect
    ? ok('节点返回的 sha256 与本地内容 sha256 一致')
    : bad(`节点返回的 sha256 与本地不一致 got=${putDone[1]} want=${putExpect}`)

  // [9b] 取回校验：入库成功不等于能取回来——这一步才是闭环
  const row = page.locator('#tasks tbody tr', { hasText: '本地入库' }).first()
  await row.locator('button[data-verify]').click()
  const verified = await waitLog(/取回校验(通过|失败)/, 60000)
  verified && verified[1] === '通过'
    ? ok('按 hash 取回校验通过（入库 → 内容寻址 → 取回 闭环成立）')
    : bad('取回校验没有通过：' + (verified ? verified[0] : '无日志'))
}

// [10] 网络入库：挑自律的那一面——内网地址必须被节点拒
//      这条是确定性的（不需要外网），验的是「pull 的 SSRF 防护真的长在节点上」，
//      而不是「面板会不会报错」：如果节点照单全收，这个地址就是被打了。
await page.fill('#in-url', 'http://127.0.0.1:' + SIG.port + '/status')
await page.locator('#btn-pull').click()
const rejected = await waitLog(/网络?入库失败|抓取 http:\/\/127\.0\.0\.1.*失败/, 45000)
const whyBool = rejected ? /内网|本机|private|loopback/i.test(await logText()) : false
rejected && whyBool
  ? ok('网络入库对内网地址被节点拒绝（SSRF 防护生效）')
  : bad(rejected ? '网络入库失败了，但不是 SSRF 拒绝（原因见日志）' : '网络入库既没成功也没报错')

// [11] 给了 PULL_URL 就真抓一次公网内容，并校验 sha256
if (PULL_URL) {
  await page.fill('#in-url', PULL_URL)
  await page.fill('#in-url-name', '')
  await page.locator('#btn-pull').click()
  const pulled2 = await waitLog(/网络入库完成：.*→ sha256 ([0-9a-f]{64})/, 90000)
  pulled2 ? ok(`网络入库成功 ${pulled2[1].slice(0, 12)}…`) : bad('网络入库（公网 URL）未完成')
} else {
  note('未给 PULL_URL，跳过「真的抓一次公网 URL」（只验了 SSRF 拒绝路径）')
}

// [12] 全程不能有未捕获的页面异常（面板是给陌生人打开的，白屏最糟）
const pageErrors = logs.filter((l) => /pageerror/.test(l))
pageErrors.length === 0 ? ok('无未捕获页面异常') : bad('页面抛了异常：' + pageErrors.join(' | '))

console.log('\n== 浏览器控制台日志（尾部）')
for (const l of logs.slice(-8)) console.log('  ' + l)
// 面板自己的 #log 才是"用户看到的那份真相"：控制台里不一定有，而断言都基于它
console.log('== 面板 #log（尾部）')
for (const l of (await logText()).split('\n').slice(-14)) console.log('  ' + l)

await browser.close()
console.log(failed === 0 ? '\nRESULT: ALL PASS' : `\nRESULT: ${failed} FAILED`)
process.exit(failed === 0 ? 0 : 1)
