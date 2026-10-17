// usePeerMedia.js — loading hook: fetches URL resources from Node via peerjs, produces objectURL.
//
// Lifecycle:
//   loading → ready (src=blobUrl) | error
//   reload() reloads (auto-reloads when url changes)
//   unmount: abort in-flight requests + revokeObjectURL (prevent leaks, safe with React StrictMode double-run)
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
          // Race: resolved after abort (blob already assembled) — revoke immediately, no dangling src
          URL.revokeObjectURL(res.blobUrl)
          return
        }
        blobUrlRef.current = res.blobUrl
        setState({ status: 'ready', src: res.blobUrl, mime: res.mime, error: null })
      })
      .catch((err) => {
        if (err.name === 'AbortError') return // triggered by unmount/reload, not an error
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
