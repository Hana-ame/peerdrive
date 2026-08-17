// e2e-browser-local.mjs — 本机 Playwright Firefox（headless）E2E runner。
// 复用 e2e-browser.mjs 的 tests 数组；前置：demo 三服务已起
// （demo:signal@9100 + demo:server@9090 + vite demo@5175）。
// 背景：早期版本连宿主 Windows Edge CDP 9222（playwright-test skill 路线），
// 但本机 ~/.cache/ms-playwright 就有 firefox-1509 缓存，playwright-core 1.59.1
// 直接 launch 即可，无需跨机 CDP。
import { firefox } from '/home/lumin/.claude/skills/playwright-test/node_modules/playwright-core/index.mjs'
import { tests, baseURL } from './e2e-browser.mjs'

const browser = await firefox.launch({
  headless: true,
  // 关 mDNS host candidate 混淆（xxx.local 对端解析不了，ICE 不通）——
  // 与 playwright-test skill 的 test-runner.mjs 同配置，2026-08-18 踩坑。
  firefoxUserPrefs: { 'media.peerconnection.ice.obfuscate_host_addresses': false },
})
let failed = 0
for (const t of tests) {
  const page = await browser.newPage()
  const results = []
  const ok = (name, pass) => results.push([name, pass])
  try {
    await t.fn({ page, ok })
    const bad = results.filter(([, p]) => !p)
    if (bad.length) {
      failed++
      console.log(`FAIL ${t.name} -> ${JSON.stringify(bad)}`)
    } else {
      console.log(`PASS ${t.name}`)
    }
  } catch (e) {
    failed++
    console.log(`FAIL ${t.name} -> ${e.message}`)
  } finally {
    await page.close()
  }
}
await browser.close()
console.log(`\n${tests.length - failed}/${tests.length} passed`)
process.exit(failed ? 1 : 0)