// sw.js — Service Worker: intercepts media requests, loads via WebRTC
//
// Purpose:
//   1. Intercept fetch requests for <img>/<video>/<audio>
//   2. Forward load request to the controlling window client via Clients API
//   3. Multiplex concurrent responses by reqId without listener starvation
//   4. Return Blob response so the browser renders media transparently
//
// Registration:
//   navigator.serviceWorker.register('/sw.js')

const MEDIA_EXT_RE = /\.(png|jpg|jpeg|gif|webp|svg|mp4|webm|ogg|mp3|wav|flac|m4a|aac)(\?|$)/i
const DEFAULT_SW_TIMEOUT_MS = 30000

/**
 * createSwRequestRouter manages concurrent in-flight media requests indexed by reqId.
 *
 * Discovery background: the previous implementation registered
 * `self.addEventListener('message', ..., { once: true })` per request and posted
 * to an unattached `MessageChannel.port2`. With `{ once: true }`, any unrelated
 * message (such as `pdm-config` or another image's response) immediately removed
 * the listener and left concurrent requests hanging until the 30s timeout.
 */
export function createSwRequestRouter(timeoutMs = DEFAULT_SW_TIMEOUT_MS) {
  const pending = new Map()

  function waitForResponse(reqId, postToClient) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(reqId)
        reject(new Error('peerdrive-media: SW timeout'))
      }, timeoutMs)

      pending.set(reqId, { resolve, reject, timer })

      Promise.resolve()
        .then(() => postToClient())
        .catch((err) => {
          const entry = pending.get(reqId)
          if (entry) {
            clearTimeout(entry.timer)
            pending.delete(reqId)
            entry.reject(err)
          }
        })
    })
  }

  function handleResponseMessage(data) {
    if (!data || data.type !== 'pdm-load-response' || !data.reqId) return false
    const entry = pending.get(data.reqId)
    if (!entry) return false
    clearTimeout(entry.timer)
    pending.delete(data.reqId)
    if (data.error) {
      entry.reject(new Error(data.error))
    } else {
      entry.resolve(
        new Response(data.blob, {
          status: 200,
          headers: {
            'Content-Type': data.mime || 'application/octet-stream',
            'Content-Length': String(data.size ?? (data.blob?.size || 0)),
          },
        })
      )
    }
    return true
  }

  return {
    waitForResponse,
    handleResponseMessage,
    get pendingCount() {
      return pending.size
    },
  }
}

let config = null
const router = createSwRequestRouter(DEFAULT_SW_TIMEOUT_MS)

function shouldIntercept(url) {
  if (!config?.allow) return false
  const href = url.toString()
  if (Array.isArray(config.allow)) {
    return config.allow.some((prefix) => href.startsWith(prefix))
  }
  return false
}

async function postLoadRequestToClient(clientId, payload) {
  if (typeof self === 'undefined' || !self.clients) {
    throw new Error('peerdrive-media: ServiceWorker clients API unavailable')
  }
  if (clientId && typeof self.clients.get === 'function') {
    const target = await self.clients.get(clientId)
    if (target) {
      target.postMessage(payload)
      return
    }
  }
  const allClients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  if (!allClients || allClients.length === 0) {
    throw new Error('peerdrive-media: no active window client to handle media load')
  }
  allClients[0].postMessage(payload)
}

function handleMediaRequest(request, clientId) {
  const url = request.url
  const reqId = `sw-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  return router.waitForResponse(reqId, () =>
    postLoadRequestToClient(clientId, {
      type: 'pdm-load-request',
      url,
      reqId,
    })
  )
}

if (typeof self !== 'undefined' && typeof self.addEventListener === 'function') {
  self.addEventListener('message', (event) => {
    const data = event.data || {}
    if (data.type === 'pdm-config') {
      config = data.config
      return
    }
    router.handleResponseMessage(data)
  })

  self.addEventListener('fetch', (event) => {
    const url = new URL(event.request.url)
    if (!MEDIA_EXT_RE.test(url.pathname)) return
    if (!shouldIntercept(url)) return
    event.respondWith(handleMediaRequest(event.request, event.clientId))
  })

  if (typeof self.skipWaiting === 'function') {
    self.addEventListener('install', () => {
      self.skipWaiting()
    })
  }

  if (self.clients && typeof self.clients.claim === 'function') {
    self.addEventListener('activate', (event) => {
      event.waitUntil(self.clients.claim())
    })
  }
}
