// e2e-browser.mjs — 真实浏览器 E2E：宿主 Edge（CDP）打开 demo/vanilla.html，
// 验证 img/video 经 peerjs 从 Node 端加载成功。
// 前置：demo:signal + demo:server + vite(5175) 已运行（见 demo README）。
export const baseURL = 'http://localhost:5175/demo/vanilla.html'

export const tests = [
  {
    name: 'mount() 图片加载',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL)
      // mount-box 成功时换成 <img>
      await page.waitForSelector('#mount-box img', { timeout: 30000 })
      ok('img 元素挂载', true)
      const loaded = await page.$eval('#mount-box img', (el) => el.complete && el.naturalWidth > 0)
      ok('图片实际解码（naturalWidth>0）', loaded)
      const box = await page.$eval('#mount-box', (el) => el.className)
      ok('状态类不再是 err', !box.includes('err'))
    },
  },
  {
    name: 'load() 手动挂载 + MIME/size 信息',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL)
      await page.waitForSelector('#load-box img', { timeout: 30000 })
      const info = await page.$eval('#load-box .ok', (el) => el.textContent)
      ok('MIME/size 文本显示', /image\/svg\+xml/.test(info))
      const loaded = await page.$eval('#load-box img', (el) => el.complete && el.naturalWidth > 0)
      ok('图片实际解码', loaded)
    },
  },
  {
    name: '视频加载（4MB 流式）',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL)
      await page.waitForSelector('#video-box video', { timeout: 60000 })
      const hasSrc = await page.$eval('#video-box video', (el) => el.src.startsWith('blob:'))
      ok('video src 是 blob URL', hasSrc)
      // 等视频元数据（假视频字节可能无合法容器——只验证元素存在与可播放尝试）
      const dur = await page.$eval('#video-box video', (el) => el.readyState).catch(() => 0)
      ok('readyState >= 1', dur >= 1)
    },
  },
  {
    name: '白名单拒绝路径',
    fn: async ({ page, ok }) => {
      await page.goto(baseURL)
      await page.waitForFunction(
        () => document.getElementById('deny-box').textContent.includes('符合预期'),
        { timeout: 30000 },
      )
      ok('白名单外 URL 被 Node 端拒绝', true)
    },
  },
]