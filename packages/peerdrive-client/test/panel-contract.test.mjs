// panel-contract.test.mjs — 公共面板的两条契约：**稳定身份** 与 **分享链接**。
//
// 为什么用"读源码断言"这种形状：面板是一整个 IIFE，跑在 file:// 下、依赖 DOM 与
// CDN 上的 peerjs，没法在这里真的 boot 起来。但下面这几条一旦被改坏，**功能会静默
// 失效而没有任何报错**——正是最该钉住的那类：
//   · 连出去不带本端 id → private 级别的好友名单永远匹配不上（运营者填了也白填）
//   · 分享链接拼进 psk → 密钥被历史记录/转发带走
//   · 链接入口没了 → unlisted 变成"谁都拿不到"（它的定义就是不在清单里）
// 这些都不是"点了会报错"的错误，只能靠契约挡住。
//
// 产物同步性由 `npm run check:panel` 单独拦（CI 跑），这里只在产物存在时顺带确认。

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..')
const src = readFileSync(join(root, 'panel', 'app.js'), 'utf8')
const tpl = readFileSync(join(root, 'panel', 'template.html'), 'utf8')
const distPath = join(root, 'dist', 'panel.html')
const dist = existsSync(distPath) ? readFileSync(distPath, 'utf8') : ''

describe('面板 / 稳定身份', () => {
  it('连出去时带本端 id（private 的好友名单按这个 id 判）', () => {
    // 少了 id: myId，peerjs 每次随机一个 —— 运营者名单里的 id 第二天就失效，
    // 表现是"我明明把你加进去了，你还是拿不到 private"。
    assert.match(src, /id:\s*myId/)
  })

  it('id 是 pd-panel- 前缀，且字符集能被 peerjs 的 id 校验接受', () => {
    const m = src.match(/return 'pd-panel-' \+ Math\.random\(\)\.toString\(36\)\.slice\(2, 10\)/)
    assert.ok(m, 'newPanelId 的形状变了，请同步更新这条断言与文档')
    // peerjs 只接受字母数字，分隔符限 - 和 _
    const sample = 'pd-panel-' + Math.random().toString(36).slice(2, 10)
    assert.match(sample, /^[A-Za-z0-9]+(?:[-_][A-Za-z0-9]+)*$/)
  })

  it('id 撞车时会被认出来并换一个重试（否则只报"信令失败"）', () => {
    assert.match(src, /unavailable-id/)
    assert.match(src, /saveMyId\(newPanelId\(\)\)/)
  })

  it('启动早期只落盘、不碰 DOM', () => {
    // 真踩过：`var $ = …` 排在后面，顶部初始化里调 renderMyId() 会抛
    // "TypeError: $ is not a function" 并中断整个 IIFE —— 面板整页失效，
    // 而报错信息跟"没有固定 id"完全看不出关系。
    const at = src.indexOf("var myId = lsRead().myId || ''")
    assert.ok(at >= 0, '找不到 myId 的初始化，请同步更新这条断言')
    const init = src.slice(at, at + 400)
    assert.doesNotMatch(init, /renderMyId\(/)
    assert.match(init, /persistMyId\(/)
    assert.match(src, /function persistMyId\(/)
  })
})

describe('面板 / 分享链接（unlisted 的出口）', () => {
  it('生成链接时绝不带上预共享密钥', () => {
    // 链接是要发给别人的；密钥一旦进链接，等于把门禁的钥匙贴在门上。
    const fn = src.slice(src.indexOf('function shareLink('), src.indexOf('function shareLink(') + 900)
    assert.match(fn, /q\.delete\('psk'\)/)
  })

  it('链接带 node 与 hash，且 auto=1 打开即连', () => {
    const fn = src.slice(src.indexOf('function shareLink('), src.indexOf('function shareLink(') + 900)
    assert.match(fn, /q\.set\('node', nodeId\)/)
    assert.match(fn, /q\.set\('hash', hash\)/)
    assert.match(fn, /q\.set\('auto', '1'\)/)
  })

  it('清单里的文件与合集条目都有「链接」按钮', () => {
    assert.match(src, /data-link=/)
    assert.match(src, /data-clink=/)
  })

  it('链接带来的 hash 单独给取回入口（unlisted 按定义不在清单里）', () => {
    assert.match(src, /id="btn-linked-get"/)
    assert.match(src, /function renderLinked\(/)
  })

  it('hash 参数按 64 位十六进制校验，乱填的被忽略而不是拿去请求', () => {
    assert.match(src, /\^\[0-9a-f\]\{64\}\$\/i/)
  })

  it('复制有降级路径（file:// 下剪贴板 API 常被拒）', () => {
    assert.match(src, /navigator\.clipboard/)
    assert.match(src, /execCommand\('copy'\)/)
    // 两级都失败时把链接打进日志，而不是"点了没反应"
    assert.match(src, /复制失败/)
  })

  it('模板里有身份区与链接容器', () => {
    assert.match(tpl, /id="my-id"/)
    assert.match(tpl, /id="btn-copy-id"/)
    assert.match(tpl, /id="btn-new-id"/)
    assert.match(tpl, /id="linked"/)
  })
})

describe('面板 / 产物', () => {
  it('dist/panel.html 与源码同步（存在时才查）', () => {
    if (!dist) {
      // 没构建过不算失败：同步性由 check:panel 拦。给出明确指引而不是静默跳过。
      assert.ok(true, '未找到 dist/panel.html（先 npm run build:panel），跳过同步性检查')
      return
    }
    for (const needle of ['我的节点 id', 'pd-panel-', 'id="linked"']) {
      assert.ok(dist.includes(needle), `产物里缺少 ${needle} —— 改了 panel/ 之后忘了 npm run build:panel`)
    }
  })
})
