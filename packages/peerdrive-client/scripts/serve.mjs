#!/usr/bin/env node
// serve.mjs — 给 demo 用的极简静态服务器（零依赖，只用 node:http）。
//
// 为什么不用 `python -m http.server`：一是想让人 `npm run demo` 一条命令起得来，
// 二是我们**需要**正确的 .js MIME（浏览器 import ESM 时 MIME 不对会直接拒绝），
// 三是本地演示建议走 http://localhost（安全上下文，crypto.subtle 可用）。
//
// 只服务包根目录，且拒绝跳出根目录的路径（../ 穿越）。
//
// --panel：把根路径指向 dist/panel.html（公共面板）而不是旧 demo。
//   这个模式的用途是「内网多人用」：面板是 http 页面时，信令用普通 ws:// 就行
//   ——浏览器的混合内容规则只拦 **HTTPS** 页面发起的 ws://，http 页面不受限。
//   所以不需要给信令配 TLS，局域网里起这一个静态服务就够了。

import { createServer } from 'node:http'
import { readFile, stat } from 'node:fs/promises'
import { extname, join, normalize, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(fileURLToPath(new URL('..', import.meta.url)))
const PORT = Number(process.env.PORT || 8123)
const HOST = process.env.HOST || '127.0.0.1'
const PANEL = process.argv.includes('--panel')
const INDEX = PANEL ? '/dist/panel.html' : '/demo/consumer.html'

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.map': 'application/json; charset=utf-8',
  '.md': 'text/markdown; charset=utf-8',
}

const server = createServer(async (req, res) => {
  try {
    const url = new URL(req.url, `http://${req.headers.host}`)
    let pathname = decodeURIComponent(url.pathname)
    if (pathname === '/' || pathname === '') pathname = INDEX
    const target = resolve(join(ROOT, normalize(pathname)))
    if (!target.startsWith(ROOT)) {
      res.writeHead(403).end('forbidden')
      return
    }
    const info = await stat(target)
    if (info.isDirectory()) {
      res.writeHead(403).end('directory listing disabled')
      return
    }
    const body = await readFile(target)
    res.writeHead(200, {
      'content-type': MIME[extname(target).toLowerCase()] || 'application/octet-stream',
      'cache-control': 'no-store',
    })
    res.end(body)
  } catch (err) {
    if (err?.code === 'ENOENT') {
      res.writeHead(404).end('not found')
      return
    }
    res.writeHead(500).end(String(err?.message || err))
  }
})

server.listen(PORT, HOST, () => {
  console.log(`peerdrive-client: http://${HOST}:${PORT}/  →  ${INDEX}`)
  if (PANEL) {
    console.log('内网给其他人用：HOST=0.0.0.0 npm run serve:panel，然后把 http://<本机IP>:<port>/ 发给他。')
    console.log('http 页面 + ws:// 信令不会被混合内容拦截（只有 HTTPS 页面才会）。')
  }
  console.log('按 Ctrl+C 停止。')
})
