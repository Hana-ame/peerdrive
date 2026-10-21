// run-media-node-e2e.mjs — 独立浏览器 E2E（带代理，连远程 peersignal）
// 前置：media-node 运行中 + static-serve (5176)。
// 运行：node scripts/run-media-node-e2e.mjs
import { chromium } from 'playwright'

const baseURL = 'http://127.0.0.1:5176/demo/media-node-e2e.html'
const PROXY = 'http://172.29.80.1:10809' // WSL 宿主代理（远程信令不可直连）

const browser = await chromium.launch({
  headless: true,
  args: [
    '--disable-features=WebRtcHideLocalIpsWithMdns',
    `--proxy-server=${PROXY}`,
  ],
})

try {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()

  console.log('── 浏览器经 Go media-node ECH 加载真实 twimg 图片 ──')
  const errors = []
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })
  page.on('pageerror', (e) => errors.push(String(e)))

  await page.goto(baseURL)
  await page.waitForSelector('#twimg-box img', { timeout: 60000 })
  console.log('[ok] img 元素出现')

  const loaded = await page.$eval('#twimg-box img', (el) => el.complete && el.naturalWidth > 0)
  console.log(`[${loaded ? 'ok' : 'FAIL'}] 图片实际解码 naturalWidth>0: ${loaded}`)

  const info = await page.$eval('#twimg-box', (el) => el.textContent)
  console.log(`[info] ${info.slice(0, 120)}`)

  if (errors.length) {
    console.log('[console errors]')
    for (const e of errors.slice(0, 5)) console.log('  ' + e.slice(0, 200))
  }
  console.log(loaded ? '===== PASS =====' : '===== FAIL =====')
} finally {
  await browser.close()
}