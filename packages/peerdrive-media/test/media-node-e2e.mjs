// media-node-e2e.mjs — 浏览器 E2E：peerdrive-media (IIFE) 连 Go media-node，
// 经内置 ECH 加载真实 twimg 图片。
// 前置：media-node 运行中（/tmp/media-node）+ static-serve (5176)。
export const baseURL = 'http://127.0.0.1:5176/demo/media-node-e2e.html'

export const tests = [
  {
    name: '浏览器经 Go media-node ECH 加载真实 twimg 图片',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL)
      await page.waitForSelector('#twimg-box img', { timeout: 45000 })
      ok('img 元素出现', true)
      const loaded = await page.$eval('#twimg-box img', (el) => el.complete && el.naturalWidth > 0)
      ok('图片实际解码（naturalWidth>0）', loaded)
      const info = await page.$eval('#twimg-box', (el) => el.textContent)
      ok('状态信息', info.includes('✅'))
      const size = await page.$eval('#twimg-box', (el) => el.querySelector('.ok')?.textContent || '')
      ok('收到内容字节', size.includes('B'))
    },
  },
]