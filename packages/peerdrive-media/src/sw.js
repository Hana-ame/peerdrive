// sw.js — Service Worker: intercepts media requests, loads via WebRTC
//
// Purpose:
//   1. Intercept fetch requests for <img>/<video>/<audio>
//   2. Fetch resources from Node via WebRTC DataChannel
//   3. Return Blob response, browser renders automatically
//
// Registration:
//   navigator.serviceWorker.register('/sw.js')

const PDM_PREFIX = '__pdm__'
const CHANNEL = 'pdm-command'

// Configuration (received from main thread via messages)
let config = null

// Establish message channel (main thread ↔ SW)
const messageChannel = new MessageChannel()
messageChannel.port1.start()

self.addEventListener('message', (event) => {
  const data = event.data || {}
  if (data.type === 'pdm-config') {
    config = data.config
  }
  if (data.type === 'pdm-load') {
    // Main thread requests resource loading
    handleLoadRequest(data.url, data.reqId)
  }
})

// Intercept fetch requests
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  const isMedia = url.pathname.match(/\.(png|jpg|jpeg|gif|webp|svg|mp4|webm|ogg|mp3|wav|flac|m4a|aac)(\?|$)/)
  
  if (!isMedia) return  // non-media request, pass through
  
  // Check if interception is needed
  if (!shouldIntercept(url)) return
  
  event.respondWith(handleMediaRequest(event.request))
})

// Check if interception should happen (whitelist)
function shouldIntercept(url) {
  if (!config?.allow) return false
  const href = url.toString()
  // Check allow list (string array or serialized function result)
  if (Array.isArray(config.allow)) {
    return config.allow.some(prefix => href.startsWith(prefix))
  }
  return false
}

// Handle media request
async function handleMediaRequest(request) {
  const url = request.url
  const reqId = `sw-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  
  return new Promise((resolve, reject) => {
    // Send load request to main thread
    messageChannel.port2.postMessage({
      type: 'pdm-load-request',
      url,
      reqId,
    })
    
    // Wait for response
    self.addEventListener('message', async (event) => {
      const data = event.data || {}
      if (data.type === 'pdm-load-response' && data.reqId === reqId) {
        if (data.error) {
          reject(new Error(data.error))
        } else {
          // Create response
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
    
    // Timeout handling
    setTimeout(() => {
      reject(new Error('peerdrive-media: SW timeout'))
    }, 30000)
  })
}

// Install
self.addEventListener('install', () => {
  self.skipWaiting()
})

// Activate
self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})
