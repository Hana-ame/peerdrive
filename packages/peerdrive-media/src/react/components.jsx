// components.jsx — PeerImage / PeerVideo / PeerMedia components.
//
// Usage:
//   <PeerImage peer="node-1" url="https://cdn.example.com/a.png" />
//   <PeerVideo peer="node-1" url="https://cdn.example.com/b.mp4" controls autoPlay muted />
//   <PeerMedia peer="node-1" url="..." />   // auto-selects img/video by MIME
// peer/signaling can also be provided globally via <PeerMediaProvider> (see context.js).
import { usePeerMedia } from './usePeerMedia.js'

// isImageMime / isVideoMime: basis for PeerMedia auto-dispatch.
function isImageMime(mime) {
  return typeof mime === 'string' && mime.startsWith('image/')
}
function isVideoMime(mime) {
  return typeof mime === 'string' && mime.startsWith('video/')
}

// renderState: shared logic for three-state rendering (loading/error can be custom ReactNode).
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
  // Non-image/video (pdf etc.): provide a downloadable link as fallback, don't render media element
  return (
    <a href={state.src} download target="_blank" rel="noreferrer">
      {url}
    </a>
  )
}
