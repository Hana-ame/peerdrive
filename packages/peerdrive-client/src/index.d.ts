// TypeScript declarations for peerdrive-client
export * from '../../peerdrive-core/src/index.js'

export interface DataConnectionLike {
  on(event: string, cb: (...args: any[]) => void): void
  send(data: any): void
  open?: boolean
  close?(): void
  peer?: string
}

export class PeerDriveError extends Error {
  code: string
  constructor(code: string, message: string)
}

export interface PeerDriveClientOptions {
  requestTimeoutMs?: number
  idleTimeoutMs?: number
  maxBufferBytes?: number
  psk?: string
  token?: string
}

export class PeerDriveClient {
  constructor(conn: DataConnectionLike, opts?: PeerDriveClientOptions)
  ready(): Promise<void>
  shares(): Promise<{
    collections: Array<Record<string, any>>
    files: Array<Record<string, any>>
    dirs: string[]
    total: number
  }>
  stream(hash: string, opts?: { offset?: number; size?: number; signal?: AbortSignal; onProgress?: (p: { received: number; total: number }) => void }): AsyncGenerator<Uint8Array>
  fetchBytes(hash: string, opts?: Record<string, any>): Promise<Uint8Array>
  fetchBlob(hash: string, opts?: Record<string, any>): Promise<Blob>
  fetchText(hash: string, opts?: Record<string, any>): Promise<string>
  put(data: Uint8Array | ArrayBuffer | Blob | string, opts?: { name?: string; signal?: AbortSignal; onProgress?: (p: { sent: number; total: number }) => void }): Promise<{ hash: string; size: number; path?: string }>
  pull(url: string, opts?: { filename?: string; signal?: AbortSignal }): Promise<{ hash: string; size: number; name?: string; path?: string }>
  close(): void
}
