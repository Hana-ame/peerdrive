// contracts.d.ts — Shared cross-end data contracts between Go backend, peerdrive-core, and React UI
// (see doc/FRONTEND-LINT-TS-EVALUATION.md §3.3).

export interface Provider {
  type: 'sha256' | 'url' | 'ech' | 'ipfs' | 'bt' | string;
  value: string;
  mime_type?: string;
  size?: number;
}

export interface ShareEntry {
  path: string;
  name?: string;
  sha?: string;
  hash?: string;
  size: number;
  mime?: string;
  mime_type?: string;
  preview?: string;
}

export interface ShareCollection {
  hash: string;
  name: string;
  friendly_name?: string;
  description?: string;
  size?: number;
  tags?: string[];
  entries?: ShareEntry[];
}

export interface ShareSnapshot {
  collections: ShareCollection[];
  files: ShareEntry[];
  dirs: string[];
  total: number;
}

export interface TransferTask {
  id: string;
  kind?: 'upload' | 'download' | 'pull';
  transport: 'peerjs' | 'ws' | 'http' | 'node-pull';
  peerId?: string;
  peer?: string;
  hash?: string;
  name: string;
  path?: string;
  received: number;
  total: number;
  pct?: number | null;
  rateBps?: number;
  status: 'queued' | 'running' | 'resuming' | 'done' | 'failed' | 'cancelled';
  error?: string;
  startedAt?: number | string;
  endedAt?: number | string;
  saved_to?: string;
}

export interface Aria2Status {
  enabled: boolean;
  connected: boolean;
  version?: string;
  rpc_url?: string;
}
