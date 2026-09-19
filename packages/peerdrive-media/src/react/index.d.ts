// index.d.ts — React 入口类型声明（精简手写，覆盖主要 API）。
import type { ReactNode, CSSProperties, ImgHTMLAttributes, VideoHTMLAttributes } from 'react'

export interface SignalingOptions {
  host?: string
  port?: number | string
  secure?: boolean
  key?: string
  path?: string
}

export interface LoadResult {
  blob: Blob
  /** objectURL，用完需 URL.revokeObjectURL() */
  blobUrl: string
  mime: string
  size: number
}

export interface LoadOptions {
  peer: string
  signaling?: SignalingOptions
  signal?: AbortSignal
}

export class PeerMediaClient {
  load(url: string, options?: LoadOptions): Promise<LoadResult>
  dispose(peer: string, signaling?: SignalingOptions): void
}

export const client: PeerMediaClient
export const DEFAULT_SIGNALING: Required<SignalingOptions>

export interface PeerMediaProviderProps {
  peer?: string
  signaling?: SignalingOptions
  children?: ReactNode
}
export function PeerMediaProvider(props: PeerMediaProviderProps): ReactNode

export interface PeerMediaState {
  status: 'idle' | 'loading' | 'ready' | 'error'
  src: string | null
  mime: string | null
  error: Error | null
  reload: () => void
}

export interface UsePeerMediaOptions {
  url?: string
  peer?: string
  signaling?: SignalingOptions
}
export function usePeerMedia(options?: UsePeerMediaOptions): PeerMediaState

export interface PeerMediaBaseProps {
  url?: string
  peer?: string
  signaling?: SignalingOptions
  loading?: ReactNode
  error?: ReactNode
}
export interface PeerImageProps extends PeerMediaBaseProps, ImgHTMLAttributes<HTMLImageElement> {
  alt?: string
}
export interface PeerVideoProps extends PeerMediaBaseProps, VideoHTMLAttributes<HTMLVideoElement> {
  controls?: boolean
}
export function PeerImage(props: PeerImageProps): ReactNode
export function PeerVideo(props: PeerVideoProps): ReactNode
export function PeerMedia(props: PeerMediaBaseProps & {
  imgProps?: ImgHTMLAttributes<HTMLImageElement>
  videoProps?: VideoHTMLAttributes<HTMLVideoElement>
}): ReactNode