import { createContext, useContext, useState, type ReactNode } from 'react'

interface PeerdriveCtx {
  username: string
  setUsername: (u: string) => void
}

const PeerdriveContext = createContext<PeerdriveCtx | null>(null)

export function PeerdriveProvider({ children }: { children: ReactNode }) {
  const [username, setUsername] = useState(() => localStorage.getItem('pd_username') || '')
  const setAndStore = (u: string) => {
    setUsername(u)
    localStorage.setItem('pd_username', u)
  }
  return <PeerdriveContext.Provider value={{ username, setUsername: setAndStore }}>
    {children}
  </PeerdriveContext.Provider>
}

export function usePeerdrive() {
  const ctx = useContext(PeerdriveContext)
  if (!ctx) throw new Error('usePeerdrive must be inside PeerdriveProvider')
  return ctx
}
