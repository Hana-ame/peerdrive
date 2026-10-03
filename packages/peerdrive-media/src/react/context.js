// context.js — PeerMedia global default configuration (peer id / signaling).
//
// Why it's needed: when multiple components share the same Node peer,
// there's no need to pass peer/signaling repeatedly — set defaults once via Provider.
// Component props take higher precedence.
import { createContext, createElement, useContext } from 'react'

export const PeerMediaContext = createContext({})

export function PeerMediaProvider({ peer, signaling, children }) {
  // Use createElement instead of JSX: this file uses .js extension, vite lib build's esbuild
  // does not parse JSX by default (only .jsx). Keeping .js avoids extra jsx handling for consumers.
  return createElement(PeerMediaContext.Provider, { value: { peer, signaling } }, children)
}

export function usePeerMediaDefaults() {
  return useContext(PeerMediaContext)
}
