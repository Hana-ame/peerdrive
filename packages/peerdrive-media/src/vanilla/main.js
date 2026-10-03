// main.js — vanilla entry (built as IIFE + ESM, referenced directly via browser <script>).
//
// IIFE usage:
//   <script src="peerdrive-media.iife.js"></script>
//   <script>
//     // Option 1: Manual loading
//     PeerMedia.load({ peer: 'node-1', url: 'https://.../a.png' })
//       .then(({ blobUrl, mime }) => {
//         const img = document.createElement('img')
//         img.src = blobUrl
//         document.body.append(img)
//       })
//     
//     // Option 2: Service Worker interception (recommended, write src directly in HTML)
//     PeerMedia.setupSW({ peer: 'node-1', allow: ['https://example.com/'] })
//     // After this, <img src="https://example.com/a.png"> loads automatically via WebRTC
//     
//     // Option 3: Monkey-Patch interception (intercepts when JS sets src)
//     PeerMedia.setupMP({ peer: 'node-1', allow: ['https://example.com/'] })
//     // After this, img.src = 'https://example.com/a.png' loads automatically via WebRTC
//   </script>
import { client, DEFAULT_SIGNALING, registerSW, setupMP } from '../core.js'

// load loads a URL resource, returning Promise<{ blob, blobUrl, mime, size }>.
// Callers must call URL.revokeObjectURL() to release blobUrl after use.
export function load({ url, peer, signaling = DEFAULT_SIGNALING, signal } = {}) {
  return client.load(url, { peer, signaling, signal })
}

// setupSW registers a Service Worker and enables media interception.
// Returns Promise<{ unregister }>.
export async function setupSW({ peer, signaling = DEFAULT_SIGNALING, allow } = {}) {
  return registerSW({ peer, signaling, allow })
}

// setupMP enables Monkey-Patch automatic interception.
// Returns { teardown }.
export function setupMonkeyPatch({ peer, signaling = DEFAULT_SIGNALING, allow } = {}) {
  return setupMP({ peer, signaling, allow })
}

// mount convenience function: directly creates an img/video element and attaches it to a container.
// mime dispatches automatically (image/* → img, video/* → video, otherwise throws).
export function mount({ url, peer, signaling, signal, container, props = {} } = {}) {
  const el = container || document.body
  return load({ url, peer, signaling, signal }).then(({ blobUrl, mime }) => {
    let node
    if (mime.startsWith('image/')) {
      node = document.createElement('img')
    } else if (mime.startsWith('video/')) {
      node = document.createElement('video')
      node.controls = true
    } else {
      throw new Error(`peerdrive-media: unsupported mime ${mime} for mount()`)
    }
    node.src = blobUrl
    Object.assign(node, props)
    el.appendChild(node)
    return { node, blobUrl, mime }
  })
}

export { client, DEFAULT_SIGNALING, registerSW, setupMP }

export default { load, mount, setupSW, setupMonkeyPatch, client, DEFAULT_SIGNALING }