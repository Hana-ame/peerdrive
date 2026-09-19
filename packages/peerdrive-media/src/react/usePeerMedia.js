// usePeerMedia.js — 加载钩子：经 peerjs 从 Node 端拉取 URL 资源，产出 objectURL。
//
// 生命周期：
//   loading → ready（src=blobUrl）| error
//   reload() 重新加载（url 变化时自动重载）
//   卸载：abort 在途请求 + revokeObjectURL（防泄漏，React StrictMode 双跑安全）
import { useEffect, useRef, useState } from 'react'
import { client } from '../core.js'
import { usePeerMediaDefaults } from './context.js'

export function usePeerMedia({ url, peer, signaling } = {}) {
  const defaults = usePeerMediaDefaults()
  const effectivePeer = peer || defaults.peer
  const effectiveSignaling = signaling || defaults.signaling

  const [state, setState] = useState({ status: 'idle', src: null, mime: null, error: null })
  const [tick, setTick] = useState(0)
  const blobUrlRef = useRef(null)

  useEffect(() => {
    if (!effectivePeer || !url) {
      setState({ status: 'idle', src: null, mime: null, error: null })
      return undefined
    }
    const ac = new AbortController()
    setState({ status: 'loading', src: null, mime: null, error: null })

    client
      .load(url, { peer: effectivePeer, signaling: effectiveSignaling, signal: ac.signal })
      .then((res) => {
        if (ac.signal.aborted) {
          // 竞态：abort 后 resolve（blob 已拼完）——立即 revoke，不留悬空 src
          URL.revokeObjectURL(res.blobUrl)
          return
        }
        blobUrlRef.current = res.blobUrl
        setState({ status: 'ready', src: res.blobUrl, mime: res.mime, error: null })
      })
      .catch((err) => {
        if (err.name === 'AbortError') return // 卸载/重载触发，非错误
        setState({ status: 'error', src: null, mime: null, error: err })
      })

    return () => {
      ac.abort()
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current)
        blobUrlRef.current = null
      }
    }
  }, [url, effectivePeer, effectiveSignaling, tick])

  return { ...state, reload: () => setTick((t) => t + 1) }
}