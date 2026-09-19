// components.jsx — PeerImage / PeerVideo / PeerMedia 组件。
//
// 用法：
//   <PeerImage peer="node-1" url="https://cdn.example.com/a.png" />
//   <PeerVideo peer="node-1" url="https://cdn.example.com/b.mp4" controls autoPlay muted />
//   <PeerMedia peer="node-1" url="..." />   // 按 MIME 自动选 img/video
// peer/signaling 也可经 <PeerMediaProvider> 全局提供（见 context.js）。
import { usePeerMedia } from './usePeerMedia.js'

// isImageMime / isVideoMime：PeerMedia 自动分派依据。
function isImageMime(mime) {
  return typeof mime === 'string' && mime.startsWith('image/')
}
function isVideoMime(mime) {
  return typeof mime === 'string' && mime.startsWith('video/')
}

// renderState 三态渲染的公共逻辑（loading/error 可自定义 ReactNode）。
function renderState(state, { loading, error }) {
  if (state.status === 'loading') {
    return loading ?? <span className="pm-loading">loading…</span>
  }
  if (state.status === 'error') {
    return error ?? <span className="pm-error">{String(state.error?.message || state.error)}</span>
  }
  return null
}

export function PeerImage({ url, peer, signaling, alt = '', loading, error, ...imgProps }) {
  const state = usePeerMedia({ url, peer, signaling })
  if (state.status !== 'ready') return renderState(state, { loading, error })
  return <img src={state.src} alt={alt} {...imgProps} />
}

export function PeerVideo({ url, peer, signaling, controls = true, loading, error, ...videoProps }) {
  const state = usePeerMedia({ url, peer, signaling })
  if (state.status !== 'ready') return renderState(state, { loading, error })
  return <video src={state.src} controls={controls} {...videoProps} />
}

export function PeerMedia({ url, peer, signaling, loading, error, imgProps = {}, videoProps = {} }) {
  const state = usePeerMedia({ url, peer, signaling })
  if (state.status !== 'ready') return renderState(state, { loading, error })
  if (isImageMime(state.mime)) return <img src={state.src} alt="" {...imgProps} />
  if (isVideoMime(state.mime)) {
    return <video src={state.src} controls {...videoProps} />
  }
  // 非图/视频（pdf 等）：给个可下载链接兜底，不渲染媒体元素
  return (
    <a href={state.src} download target="_blank" rel="noreferrer">
      {url}
    </a>
  )
}