// vendor-peerjs.mjs — 把 peerjs 下载到 dist/ 同目录，让公共面板彻底离线可用。
//
// 为什么需要它：
//   dist/panel.html 默认按「同目录副本 → jsdelivr → unpkg」回退加载 peerjs。
//   内网 / 离线 / CDN 被墙的场景下前两个源都取不到，这时只要把 peerjs.min.js
//   放到面板**同一个目录**里，面板就不依赖任何外部网络了
//   （整个依然是单目录、零服务器：file:// 双击或用任意静态托管）。
//
// 用法: node scripts/vendor-peerjs.mjs
//   HTTPS_PROXY / HTTP_PROXY 会被 node 18+ 的 fetch 自动忽略 —— 需要代理时用
//   NODE_EXTRA_CA_CERTS 配证书，或手工用浏览器/curl 下载到 dist/peerjs.min.js。

import { createWriteStream } from 'node:fs'
import { mkdirSync, statSync } from 'node:fs'
import { Readable } from 'node:stream'
import { pipeline } from 'node:stream/promises'
import { createHash } from 'node:crypto'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const DIST = join(ROOT, 'dist')
const OUT = join(DIST, 'peerjs.min.js')

const VERSION = '1.5.5'
const SOURCES = [
  `https://cdn.jsdelivr.net/npm/peerjs@${VERSION}/dist/peerjs.min.js`,
  `https://unpkg.com/peerjs@${VERSION}/dist/peerjs.min.js`,
  `https://registry.npmjs.org/peerjs/-/peerjs-${VERSION}.tgz`, // 兜底：需要解 tar，最后再考虑
]

async function download(url) {
  console.log('尝试 ' + url)
  const res = await fetch(url, { redirect: 'follow' })
  if (!res.ok) throw new Error('HTTP ' + res.status)
  if (url.endsWith('.tgz')) throw new Error('暂不支持 tarball 源，请换用浏览器/CDN 直链下载')
  const body = await res.arrayBuffer()
  if (body.byteLength < 20000) throw new Error('内容过小（' + body.byteLength + 'B），可能不是 peerjs')
  return Buffer.from(body)
}

mkdirSync(DIST, { recursive: true })
let lastErr = null
for (const url of SOURCES) {
  try {
    const buf = await download(url)
    await pipeline(Readable.from(buf), createWriteStream(OUT))
    const sha = createHash('sha256').update(buf).digest('hex')
    console.log(`peerjs: 已写入 dist/peerjs.min.js（${(statSync(OUT).size / 1024).toFixed(1)} KB, sha256 ${sha.slice(0, 12)}…）`)
    console.log('peerjs: 现在 dist/panel.html 不再需要联网取 peerjs。')
    process.exit(0)
  } catch (e) {
    lastErr = e
    console.log('  失败：' + e.message)
  }
}
console.error('peerjs: 全部源都失败了 —— ' + (lastErr && lastErr.message))
console.error('peerjs: 可以手工下载 https://cdn.jsdelivr.net/npm/peerjs@' + VERSION + '/dist/peerjs.min.js 放到 dist/ 目录。')
process.exit(1)
