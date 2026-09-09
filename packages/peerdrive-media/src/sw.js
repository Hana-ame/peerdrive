// sw.js — Service Worker：拦截媒体请求，经 WebRTC 加载
//
// 作用：
//   1. 拦截 <img>/<video>/<audio> 的 fetch 请求
//   2. 通过 WebRTC DataChannel 从 Node 端获取资源
//   3. 返回 Blob 响应，浏览器自动渲染
//
// 注册方式：
//   navigator.serviceWorker.register('/sw.js')

const PDM_PREFIX = '__pdm__'
const CHANNEL = 'pdm-command'

// 配置（通过消息从主线程接收）
let config = null

// 建立消息通道（主线程 ↔ SW）
const messageChannel = new MessageChannel()
messageChannel.port1.start()

self.addEventListener('message', (event) => {
  const data = event.data || {}
  if (data.type === 'pdm-config') {
    config = data.config
  }
  if (data.type === 'pdm-load') {
    // 主线程请求加载资源
    handleLoadRequest(data.url, data.reqId)
  }
})

// 拦截 fetch 请求
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  const isMedia = url.pathname.match(/\.(png|jpg|jpeg|gif|webp|svg|mp4|webm|ogg|mp3|wav|flac|m4a|aac)(\?|$)/)
  
  if (!isMedia) return  // 非媒体请求，放行
  
  // 检查是否需要拦截
  if (!shouldIntercept(url)) return
  
  event.respondWith(handleMediaRequest(event.request))
})

// 检查是否应该拦截（白名单）
function shouldIntercept(url) {
  if (!config?.allow) return false
  const href = url.toString()
  // 检查 allow 列表（字符串数组或函数序列化后的结果）
  if (Array.isArray(config.allow)) {
    return config.allow.some(prefix => href.startsWith(prefix))
  }
  return false
}

// 处理媒体请求
async function handleMediaRequest(request) {
  const url = request.url
  const reqId = `sw-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  
  return new Promise((resolve, reject) => {
    // 发送加载请求到主线程
    messageChannel.port2.postMessage({
      type: 'pdm-load-request',
      url,
      reqId,
    })
    
    // 等待响应
    self.addEventListener('message', async (event) => {
      const data = event.data || {}
      if (data.type === 'pdm-load-response' && data.reqId === reqId) {
        if (data.error) {
          reject(new Error(data.error))
        } else {
          // 创建响应
          const response = new Response(data.blob, {
            status: 200,
            headers: {
              'Content-Type': data.mime,
              'Content-Length': String(data.size),
            },
          })
          resolve(response)
        }
      }
    }, { once: true })
    
    // 超时处理
    setTimeout(() => {
      reject(new Error('peerdrive-media: SW timeout'))
    }, 30000)
  })
}

// 安装
self.addEventListener('install', () => {
  self.skipWaiting()
})

// 激活
self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})
