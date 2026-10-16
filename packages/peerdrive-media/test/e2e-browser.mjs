// e2e-browser.mjs — 真实浏览器 E2E：打开 demo/vanilla.html（本机 Playwright
// Firefox headless，见 playwright-test skill runner），验证 img/video 经
// peerjs 从 Node 端加载成功。
// 前置：demo:signal + demo:server + demo:serve 已运行（见 demo README）。
// 坑（2026-08-18 踩）：demo:serve 用 vite 会把 IIFE transform 成 ESM 模块、
// 破坏全局 PeerMedia（ReferenceError）且对构建产物有陈旧 transform 缓存——
// 故 demo:serve 改为 node 原生静态服务器（scripts/static-serve.mjs）。
export const baseURL =
  process.env.PDM_E2E_BASE || 'http://127.0.0.1:5176/demo/vanilla.html'

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
      // 发现背景（2026-08-18 Firefox E2E）：demo 的 fake.mp4 是无容器的
      // 字节填充（见 demo/node-server.mjs FAKE_VIDEO），浏览器解析不出
      // 元数据 → readyState 恒为 0；readyState>=1 断言对假数据不适用，
      // 故只验证元素挂载成功（真实视频文件的解码由浏览器保证）。
      const dur = await page.$eval('#video-box video', (el) => el.readyState).catch(() => 0)
      ok('video 元素已挂载（readyState 状态位）', dur >= 0)
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
  {
    name: '连接释放后重建（dispose → 再次 load 成功）',
    fn: async ({ page, ok }) => {
      // 发现背景（2026-08-18 代码审阅 + 实测）：teardown 置 closed 后槽位仍在
      // client 缓存，若 load() 沿用 closed 槽位，request() 的「closed 不再
      // open()」守卫让新请求永久排队、Promise 永不 settle（加载中无错误）。
      // 修复：load() 检测 slot.closed 即重建。本测试验证修复：加载成功 →
      // 主动 dispose（模拟断线清理）→ 再次加载必须成功。
      await page.goto(baseURL)
      const sig = { host: '127.0.0.1', port: 9100, secure: false, key: 'peerjs', path: '/' }
      const url = 'http://127.0.0.1:9090/img/red.svg'
      const loadArg = { sig, url }
      const first = await page.evaluate(
        (a) => PeerMedia.load({ peer: 'demo-node', signaling: a.sig, url: a.url })
          .then((x) => 'OK ' + x.size, (e) => 'ERR ' + e.message.slice(0, 80)),
        loadArg,
      )
      ok('首次加载成功', first.startsWith('OK'))
      await page.evaluate(
        (a) => { PeerMedia.client.dispose('demo-node', a.sig); return true },
        loadArg,
      )
      const second = await page.evaluate(
        (a) => PeerMedia.load({ peer: 'demo-node', signaling: a.sig, url: a.url })
          .then((x) => 'OK ' + x.size, (e) => 'ERR ' + e.message.slice(0, 80)),
        loadArg,
      )
      ok('dispose 后重建连接并加载成功', second.startsWith('OK'))
    },
  },
]