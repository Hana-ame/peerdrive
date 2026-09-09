// main.js — vanilla 入口（构建为 IIFE + ESM，浏览器 <script> 直接引用）。
//
// IIFE 用法：
//   <script src="peerdrive-media.iife.js"></script>
//   <script>
//     // 方式 1：手动加载
//     PeerMedia.load({ peer: 'node-1', url: 'https://.../a.png' })
//       .then(({ blobUrl, mime }) => {
//         const img = document.createElement('img')
//         img.src = blobUrl
//         document.body.append(img)
//       })
//     
//     // 方式 2：Service Worker 拦截（推荐，HTML 直接写 src）
//     PeerMedia.setupSW({ peer: 'node-1', allow: ['https://example.com/'] })
//     // 之后 <img src="https://example.com/a.png"> 自动经 WebRTC 加载
//     
//     // 方式 3：Monkey-Patch 拦截（JS 设置 src 时拦截）
//     PeerMedia.setupMP({ peer: 'node-1', allow: ['https://example.com/'] })
//     // 之后 img.src = 'https://example.com/a.png' 自动经 WebRTC 加载
//   </script>
import { client, DEFAULT_SIGNALING, registerSW, setupMP } from '../core.js'

// load 加载 URL 资源，返回 Promise<{ blob, blobUrl, mime, size }>。
// blobUrl 用完需调用者 URL.revokeObjectURL() 释放。
export function load({ url, peer, signaling = DEFAULT_SIGNALING, signal } = {}) {
  return client.load(url, { peer, signaling, signal })
}

// setupSW 注册 Service Worker，启用媒体拦截。
// 返回 Promise<{ unregister }>。
export async function setupSW({ peer, signaling = DEFAULT_SIGNALING, allow } = {}) {
  return registerSW({ peer, signaling, allow })
}

// setupMP 启用 Monkey-Patch 自动拦截。
// 返回 { teardown }。
export function setupMonkeyPatch({ peer, signaling = DEFAULT_SIGNALING, allow } = {}) {
  return setupMP({ peer, signaling, allow })
}

// mount 便捷函数：直接创建 img/video 元素并挂到容器。
// mime 自动分派（image/* → img，video/* → video，其余抛错）。
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