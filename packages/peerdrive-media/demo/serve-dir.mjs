#!/usr/bin/env node
// serve-dir.mjs — 以指定 path serve 本地文件（HTTP + WebRTC 双通道）。
//
// 用法：
//   node serve-dir.mjs --dir /path/to/files [--port 9090] [--signal-port 9100] [--peer-id my-node]
//
// 架构：
//   浏览器 → PeerJS 信令 → WebRTC DataChannel → Node（本进程）
//        → fetch 本地 HTTP 静态服务 → 分块流回浏览器
//
// 为什么两阶段（HTTP + WebRTC）而不是 Node 端直接读文件发 WebRTC：
//   peerdrive-media 的 server.js 只接受 fetch URL——设计上是「URL 资源
//   提供者」，不直接读 fs。本地文件经 HTTP 转一道，复用同一套协议、
//   背压、keepalive、白名单逻辑，不改核心代码。

import http from 'node:http'
import { createReadStream, statSync, readdirSync } from 'node:fs'
import { join, normalize, extname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createPeerMediaServer, allowPrefix } from '../src/node/server.js'

// ---- CLI 参数解析 ----
const args = process.argv.slice(2)
function getArg(name, fallback) {
  const idx = args.indexOf(name)
  return idx >= 0 && args[idx + 1] ? args[idx + 1] : fallback
}

const DIR = getArg('--dir', getArg('-d', '.'))
const HTTP_PORT = parseInt(getArg('--http-port', getArg('-p', '9090')), 10)
const SIGNAL_PORT = parseInt(getArg('--signal-port', getArg('-s', '9100')), 10)
const PEER_ID = getArg('--peer-id', getArg('-id', 'serve-dir'))
const HOST = getArg('--host', '127.0.0.1')

// ---- MIME 类型映射（覆盖常见媒体 + 文档格式）----
const MIME = {
  '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg',
  '.gif': 'image/gif', '.svg': 'image/svg+xml', '.webp': 'image/webp',
  '.ico': 'image/x-icon', '.bmp': 'image/bmp',
  '.mp4': 'video/mp4', '.webm': 'video/webm', '.ogv': 'video/ogg',
  '.mov': 'video/quicktime', '.mkv': 'video/x-matroska',
  '.mp3': 'audio/mpeg', '.wav': 'audio/wav', '.ogg': 'audio/ogg',
  '.m4a': 'audio/mp4', '.flac': 'audio/flac', '.aac': 'audio/aac',
  '.pdf': 'application/pdf', '.json': 'application/json',
  '.js': 'application/javascript', '.css': 'text/css',
  '.html': 'text/html', '.htm': 'text/html',
  '.txt': 'text/plain', '.md': 'text/markdown',
  '.xml': 'application/xml', '.csv': 'text/csv',
  '.zip': 'application/zip', '.gz': 'application/gzip',
  '.tar': 'application/x-tar', '.wasm': 'application/wasm',
  '.ttf': 'font/ttf', '.woff': 'font/woff', '.woff2': 'font/woff2',
}

function guessMime(path) {
  return MIME[extname(path).toLowerCase()] || 'application/octet-stream'
}

// ---- 静态文件 HTTP 服务 ----
const resolvedDir = normalize(DIR)

const httpServer = http.createServer((req, res) => {
  const u = new URL(req.url, `http://${HOST}:${HTTP_PORT}`)
  let filePath = normalize(join(resolvedDir, u.pathname))

  // 防目录穿越：解析后必须在 resolvedDir 内
  if (!filePath.startsWith(resolvedDir)) {
    res.writeHead(403); res.end('forbidden'); return
  }

  let stat
  try { stat = statSync(filePath) } catch { res.writeHead(404); res.end('not found'); return }

  if (stat.isDirectory()) {
    // 目录：列文件列表（简单文本）
    const entries = readdirSync(filePath, { withFileTypes: true })
      .map(e => `${e.isDirectory() ? '📁' : '📄'} ${e.name}`)
      .join('\n')
    res.writeHead(200, { 'content-type': 'text/plain; charset=utf-8' })
    res.end(entries || '(empty)')
    return
  }

  res.writeHead(200, {
    'content-type': guessMime(filePath),
    'content-length': stat.size,
    'content-range': `bytes 0-${stat.size - 1}/${stat.size}`,
  })
  const stream = createReadStream(filePath)
  stream.on('error', () => { res.writeHead(500); res.end('read error') })
  stream.pipe(res)
})

httpServer.listen(HTTP_PORT, HOST, () => {
  console.log(`[serve-dir] HTTP 静态服务: http://${HOST}:${HTTP_PORT}/`)
  console.log(`[serve-dir] 服务目录: ${resolvedDir}`)
})

// ---- WebRTC 提供者：浏览器连 PEER_ID，请求 http://HOST:HTTP_PORT/... 的文件 ----
let provider
for (let attempt = 1; attempt <= 5; attempt++) {
  try {
    provider = await createPeerMediaServer({
      peerId: PEER_ID,
      signaling: { host: '127.0.0.1', port: SIGNAL_PORT, secure: false, key: 'peerjs', path: '/' },
      allow: allowPrefix([`http://${HOST}:${HTTP_PORT}/`]),
      onRequest: ({ url, peer, status, bytes, ms }) => {
        const kb = bytes > 1024 * 1024
          ? (bytes / 1024 / 1024).toFixed(1) + 'MB'
          : (bytes / 1024).toFixed(1) + 'KB'
        console.log(`[serve-dir] ${peer} ← ${url} [${status}] ${kb} in ${ms}ms`)
      },
    })
    break
  } catch (err) {
    console.log(`[serve-dir] 信令连接失败（第 ${attempt} 次）：${err.message}`)
    if (attempt === 5) throw err
    await new Promise((r) => setTimeout(r, 3000))
  }
}

console.log(`[serve-dir] peer "${provider.peerId}" 已上线（信令 127.0.0.1:${SIGNAL_PORT}）`)
console.log(`[serve-dir] 浏览器端连接方式：`)
console.log(`  peerId:    ${PEER_ID}`)
console.log(`  signal:    ws://127.0.0.1:${SIGNAL_PORT}/`)
console.log(`  baseUrl:   http://${HOST}:${HTTP_PORT}/`)
console.log(`[serve-dir] 示例：<img src="http://${HOST}:${HTTP_PORT}/photo.png">（浏览器侧经 WebRTC 加载）`)

process.on('SIGINT', () => {
  console.log('\n[serve-dir] shutting down...')
  provider?.close()
  httpServer.close()
  process.exit(0)
})
