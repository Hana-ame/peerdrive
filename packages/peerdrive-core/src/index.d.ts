// TypeScript declarations for peerdrive-core (Single Source of Truth)

export class Sha256 {
  constructor()
  update(chunk: ArrayBuffer | ArrayBufferView): this
  digest(): Uint8Array
  digestHex(): string
}
export function sha256(bytes: ArrayBuffer | ArrayBufferView): Uint8Array
export function sha256Hex(bytes: ArrayBuffer | ArrayBufferView): Promise<string>
export function sha256HexSync(bytes: ArrayBuffer | ArrayBufferView): string

export const CLIENT_CAPABILITIES: readonly string[]
export const MAX_FILE_BYTES: number
export const DEFAULT_MAX_BUFFER_BYTES: number
export const UPLOAD_CHUNK: number
export const PSK_REQUIRED: 'PSK_REQUIRED'
export const BINARY_MAGIC_V2: 'PD02'
export const BINARY_HEADER_SIZE: 32

export function nextReqId(prefix?: string): string
export function isValidHash(h: unknown): h is string
export function parseFrame(raw: unknown): Record<string, any> | null
export function makeEnvelope(event: string, reqId: string, data?: Record<string, any>, code?: number): string
export function encodeBinaryHeader32(reqId: string, offset: number, payload?: Uint8Array, flags?: number): Uint8Array
export function decodeBinaryHeader32(buf: ArrayBuffer | ArrayBufferView): {
  version: number
  flags: number
  reqId: string
  offset: number
  payload: Uint8Array
} | null
export function isBinaryFrame(data: unknown): boolean
export function toUint8Array(data: ArrayBuffer | ArrayBufferView): Uint8Array
export function concatChunks(chunks: Uint8Array[], totalLength?: number): Uint8Array
export function guessMime(filename?: string, fallback?: string): string
export function baseName(p?: string): string

export class FrameDispatcher {
  register(reqId: string, handlers: {
    onControl?: (frame: Record<string, any>) => void
    onDataHeader?: (frame: Record<string, any>) => void
    onDataChunk?: (chunk: Uint8Array, meta: { offset: number; size: number; flags?: number }) => void
    onError?: (err: Error) => void
  }): () => void
  unregister(reqId: string): void
  has(reqId: string): boolean
  handleIncoming(raw: unknown): boolean
  closeAll(reason?: Error): void
}

export interface DirectUrlOptions {
  baseUrl?: string
  peerId?: string
  filename?: string
  name?: string
  entryPath?: string
  signalUrl?: string
  mode?: 'spa' | 'http' | 'panel'
}
export function buildFileUrl(sha256: string, opts?: DirectUrlOptions): string
export function buildCollectionUrl(hash: string, opts?: DirectUrlOptions): string
export function parseDirectTarget(input: string, defaultKind?: 'file' | 'collection'): {
  kind: 'file' | 'collection' | 'unknown'
  hash: string
  peerId: string
  filename: string
  entryPath: string
  signalUrl: string
  route: string
}

export class AsyncQueueLane {
  constructor(concurrency?: number)
  enqueue<T>(fn: () => Promise<T>, opts?: { priority?: number; signal?: AbortSignal }): Promise<T>
}

export class TransferProgressHub {
  createTask(meta: Record<string, any>): Record<string, any>
  updateProgress(id: string, received: number, total?: number): void
  completeTask(id: string, extra?: Record<string, any>): void
  failTask(id: string, err: Error | string): void
  cancelTask(id: string): void
  clearCompleted(): void
  getTasks(): Record<string, any>[]
  getSnapshot(): { tasks: Record<string, any>[]; totalActive: number; totalCompleted: number; totalFailed: number }
  subscribe(fn: (snapshot: any) => void): () => void
}

export class TransportQueue {
  constructor(opts?: { progressHub?: TransferProgressHub; wsConcurrency?: number; peerjsConcurrency?: number; httpConcurrency?: number })
  runTransfer<T>(meta: Record<string, any>, worker: (ctx: { taskId: string; reportProgress: (received: number, total?: number) => void; signal?: AbortSignal }) => Promise<T>): Promise<T>
}

export const globalProgressHub: TransferProgressHub
export const globalTransportQueue: TransportQueue

export const MAX_MANIFEST_BYTES: number
export function sanitizeUntrustedText(text: unknown, maxLen?: number): string
export function sanitizeFilename(name: unknown, fallback?: string): string
export function sanitizeRelativePath(p: unknown, maxLen?: number): string
export function safeJsonParse(text: unknown, opts?: { maxBytes?: number; throwOnError?: boolean }): any
export function detectPromptInjection(text: unknown): boolean
export function safePreviewMime(mime: unknown): string | null
export function validateSignalOrApiUrl(url: unknown): { valid: boolean; url?: URL; reason?: string }
export function sanitizeCollectionManifest(obj: unknown): Record<string, any>
export class PermissionError extends Error {
  reason: string
  code: string
  constructor(reason: string, code: string)
}
export function evaluateAdminPermission(transport: string, method: string, path?: string): {
  allowed: boolean
  reason?: string
  code?: string
}
