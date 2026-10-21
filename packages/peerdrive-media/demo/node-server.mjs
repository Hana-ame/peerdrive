// node-server.mjs — demo：Node 端资源提供者。
// 运行：
//   npm run demo:signal   # 终端1：本地信令（peerjs-server, 127.0.0.1:9100）
//   npm run demo:server   # 终端2：本文件（资源服务 + peer 提供者）
// 然后浏览器打开 demo/vanilla.html（npm run demo:serve 或任意静态服务）。
//
// 注意：端口固定 9100/9090（本机 9000 系列被 librefs/s3proxy 占用）；冲突时
// 同步改 demo:signal 端口与本文件 SIGNAL_PORT、demo/vanilla.html 的 SIGNAL。
import http from 'node:http'
import { createPeerMediaServer, allowPrefix } from '../src/node/server.js'

const SIGNAL_PORT = 9100
const HTTP_PORT = 9090
const PEER_ID = 'demo-node'

// —— 本地资源服务：生成几张小图 + 一段视频数据 ——
function svgBytes(color) {
  return Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="480" height="270">` +
      `<rect width="480" height="270" fill="${color}"/>` +
      `<text x="240" y="140" font-size="32" fill="white" text-anchor="middle">peerjs media demo</text>` +
      `</svg>`,
  )
}
// 假视频：SVG 动画不是真视频，这里用字节填充仅演示 MIME 分派与流式传输
const FAKE_VIDEO = Buffer.alloc(4 * 1024 * 1024)
for (let i = 0; i < FAKE_VIDEO.length; i++) FAKE_VIDEO[i] = (i * 31) & 0xff

const routes = {
  '/img/red.svg': { type: 'image/svg+xml', body: svgBytes('#e74c3c') },
  '/img/green.svg': { type: 'image/svg+xml', body: svgBytes('#2ecc71') },
  '/img/blue.svg': { type: 'image/svg+xml', body: svgBytes('#3498db') },
  '/video/fake.mp4': { type: 'video/mp4', body: FAKE_VIDEO },
}

const httpServer = http.createServer((req, res) => {
  const u = new URL(req.url, `http://127.0.0.1:${HTTP_PORT}`)
  const route = routes[u.pathname]
  if (!route) { res.writeHead(404); res.end('not found'); return }
  res.writeHead(200, { 'content-type': route.type, 'content-length': route.body.length })
  res.end(route.body)
})
httpServer.listen(HTTP_PORT, '127.0.0.1', () => {
  console.log(`demo: 资源服务 http://127.0.0.1:${HTTP_PORT}`)
})

// —— peer 提供者：网页端连 PEER_ID 加载上述 URL ——
// 启动竞态：若信令（demo:signal）未就绪，peerjs 连接会悬挂 15s 超时。
// 重试 5 次 × 3s——文档/README 仍建议先起信令再起本脚本。
let provider
for (let attempt = 1; attempt <= 5; attempt++) {
  try {
    provider = await createPeerMediaServer({
      peerId: PEER_ID,
      signaling: { host: '127.0.0.1', port: SIGNAL_PORT, secure: false, key: 'peerjs', path: '/' },
      // 白名单：只允许本 demo 资源服务（安全边界，见 server.js 文件头注释）
      allow: allowPrefix([`http://127.0.0.1:${HTTP_PORT}/`]),
      onRequest: ({ url, peer, status, bytes, ms }) => {
        console.log(`demo: ${peer} <- ${url} [${status}] ${(bytes / 1024).toFixed(1)}KB in ${ms}ms`)
      },
    })
    break
  } catch (err) {
    console.log(`demo: 信令连接失败（第 ${attempt} 次）：${err.message}`)
    if (attempt === 5) throw err
    await new Promise((r) => setTimeout(r, 3000))
  }
}
console.log(`demo: peer "${provider.peerId}" 已上线（信令 127.0.0.1:${SIGNAL_PORT}）`)
console.log(`demo: 打开 demo/vanilla.html 查看效果`)

process.on('SIGINT', () => {
  provider.close()
  httpServer.close()
  process.exit(0)
})