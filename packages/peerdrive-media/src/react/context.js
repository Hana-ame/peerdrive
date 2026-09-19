// context.js — PeerMedia 全局默认配置（peer id / 信令）。
//
// 为什么需要：多个组件共用同一 Node 端 peer 时，不必每个组件重复传
// peer/signaling——用 Provider 设一次默认值。组件 props 优先级更高。
import { createContext, createElement, useContext } from 'react'

export const PeerMediaContext = createContext({})

export function PeerMediaProvider({ peer, signaling, children }) {
  // 用 createElement 而非 JSX：本文件 .js 扩展名，vite lib 构建的 esbuild
  // 默认不解析 JSX（.jsx 才会）。保持 .js 免去消费方对 jsx 的额外处理。
  return createElement(PeerMediaContext.Provider, { value: { peer, signaling } }, children)
}

export function usePeerMediaDefaults() {
  return useContext(PeerMediaContext)
}