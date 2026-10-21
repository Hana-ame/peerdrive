// scripts/static-serve.mjs — demo 静态服务器（node 原生 http，跨平台）。
// 为什么不用 vite serve：vite dev 会把 IIFE 构建产物当 ESM 模块 transform，
// 破坏 `var PeerMedia` 的全局暴露（浏览器报 ReferenceError，e2e-browser.mjs
// 2026-08-18 踩到）；静态文件服务器直接吐磁盘原样内容。
// 端口固定 5176（与 e2e-browser.mjs baseURL 一致；5175 留给 vite dev 其他用途）。
import http from 'node:http'
import { readFile } from 'node:fs/promises'
import { extname, join, normalize } from 'node:path'

const PORT = 5176
const ROOT = new URL('../', import.meta.url).pathname

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.mjs': 'text/javascript; charset=utf-8',
  '.json': 'application/json',
}

http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${PORT}`)
  const p = normalize(join(ROOT, decodeURIComponent(url.pathname)))
  if (!p.startsWith(ROOT)) { res.writeHead(403); res.end('forbidden'); return }
  try {
    const body = await readFile(p)
    res.writeHead(200, {
      'content-type': MIME[extname(p)] || 'application/octet-stream',
      'cache-control': 'no-store', // 开发调试：build 后无需重启服务器
    })
    res.end(body)
  } catch {
    res.writeHead(404); res.end('not found')
  }
}).listen(PORT, '127.0.0.1', () => {
  console.log(`demo: static server http://127.0.0.1:${PORT} (root: ${ROOT})`)
})