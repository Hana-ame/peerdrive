// 分块并行下载 + 断点续传 + 双重校验。
//
// 设计要点：
// - 块固定 1 MiB，请求带 offset/length，对端只回这一段；
// - 同时最多 threads 条 stream 在飞，一条 stream 对应一个块，互不阻塞；
// - 每块收完对端会发一条 check（该块 sha256），先逐块验；全部到齐后再把
//   整文件 sha256 与 collection 里的值比一次（终验）。
// - 暂停只是「不再发新请求」，在飞的块照样收完；继续时只补还没完成的块，
//   所以暂停/断线都不会白下已完成的部分。
import { Sha256 } from "/assets/sha256.js";
import { KIND } from "./frames.js";

const CHUNK = 1024 * 1024;
const REQUEST_TIMEOUT = 8000;
const RETRY_LIMIT = 5;

function buildChunks(size) {
  const chunks = [];
  for (let offset = 0; offset < size; offset += CHUNK) {
    chunks.push({
      offset,
      length: Math.min(CHUNK, size - offset),
      state: "pending", // pending | inflight | done
      buffer: null,
      received: 0,
      hasher: null,
      retries: 0,
      timer: 0,
      stream: 0,
    });
  }
  return chunks;
}

export class Downloader {
  constructor({ frames, file, threads, onUpdate }) {
    this.frames = frames;
    this.file = file;
    this.threads = Math.max(1, Math.min(8, threads || 4));
    this.onUpdate = onUpdate || (() => {});

    this.chunks = buildChunks(file.size);
    this.nextStream = 1000; // 集合/统计用 1..，下载从 1000 起，互不撞号
    this.inflight = new Map(); // stream → chunkIndex
    this.streams = new Map(); // stream → chunkIndex

    this.paused = false;
    this.state = "idle"; // idle | running | paused | verifying | done | corrupt | error | disconnected
    this.verified = false;
    this.lastError = "";
    this.startedAt = 0;
    this.startedBytes = 0;
    this.startedMs = 0;
  }

  get totalChunks() {
    return this.chunks.length;
  }

  get doneChunks() {
    return this.chunks.filter((c) => c.state === "done").length;
  }

  get doneBytes() {
    let n = 0;
    for (const c of this.chunks) if (c.state === "done") n += c.length;
    return n;
  }

  start() {
    if (this.state === "done" || this.state === "corrupt") return;
    this.paused = false;
    this.state = "running";
    this.startedAt = performance.now();
    this._pump();
    this._emit();
  }

  pause() {
    if (this.state === "done" || this.state === "corrupt") return;
    this.paused = true;
    this.state = "paused";
    this._emit();
  }

  resume() {
    if (this.paused !== true && this.state !== "paused" && this.state !== "disconnected") return;
    this.paused = false;
    this.state = "running";
    this._pump();
    this._emit();
  }

  // 通道整体失效：在飞的块退回 pending，等新通道就绪后从断点继续。
  channelLost(frames) {
    for (const [stream, idx] of this.inflight) {
      const chunk = this.chunks[idx];
      chunk.state = "pending";
      clearTimeout(chunk.timer);
      this.streams.delete(stream);
    }
    this.inflight.clear();
    if (frames) this.attach(frames);
    this.paused = true;
    this.state = "disconnected";
    this._emit();
  }

  attach(frames) {
    this.frames = frames;
  }

  // onFrame 由 app 把落在下载 stream 上的帧转发进来。
  onFrame(f) {
    const idx = this.streams.get(f.stream);
    if (idx == null) return;
    const chunk = this.chunks[idx];

    switch (f.kind) {
      case KIND.METADATA: {
        const header = this.frames.json(f);
        chunk.length = header.length;
        chunk.buffer = new Uint8Array(header.length);
        chunk.received = 0;
        chunk.hasher = new Sha256();
        return;
      }

      case KIND.DATA: {
        if (!chunk.buffer) return;
        const bytes = f.data;
        const room = chunk.buffer.length - chunk.received;
        const take = Math.min(room, bytes.length);
        chunk.buffer.set(bytes.subarray(0, take), chunk.received);
        chunk.received += take;
        chunk.hasher.update(bytes.subarray(0, take));
        this._emit();
        return;
      }

      case KIND.CHECK: {
        const check = this.frames.json(f);
        const digest = chunk.hasher.digestHex();
        const ok = chunk.received === check.length && digest === check.digest;
        this._finishStream(f.stream, idx, ok);
        return;
      }

      case KIND.ERROR: {
        const err = this.frames.json(f);
        this.lastError = err.message || "节点报错";
        this._finishStream(f.stream, idx, false);
        return;
      }

      default:
        return;
    }
  }

  _finishStream(stream, idx, ok) {
    const chunk = this.chunks[idx];
    clearTimeout(chunk.timer);
    this.inflight.delete(stream);
    this.streams.delete(stream);

    if (ok) {
      chunk.state = "done";
      this.lastError = "";
      this._pump();
      this._emit();
      return;
    }

    chunk.retries += 1;
    if (chunk.retries > RETRY_LIMIT) {
      chunk.state = "failed";
      this.state = "error";
      this._emit();
      return;
    }
    chunk.state = "pending";
    this._pump();
    this._emit();
  }

  _pump() {
    if (this.paused || this.state === "error" || this.state === "done" || this.state === "corrupt") return;

    for (const chunk of this.chunks) {
      if (this.inflight.size >= this.threads) break;
      if (chunk.state !== "pending") continue;
      this._request(chunk);
    }

    if (this.inflight.size === 0 && this.chunks.every((c) => c.state === "done")) {
      this._verify();
    }
    if (this.inflight.size === 0 && this.chunks.some((c) => c.state === "failed")) {
      this.state = "error";
      this._emit();
    }
  }

  _request(chunk) {
    chunk.state = "inflight";
    chunk.stream = this.nextStream++;
    this.streams.set(chunk.stream, this.chunks.indexOf(chunk));
    this.inflight.set(chunk.stream, this.chunks.indexOf(chunk));

    const sent = this.frames.sendJSON(KIND.CONTROL, chunk.stream, {
      action: "file",
      sha256: this.file.sha256,
      offset: chunk.offset,
      length: chunk.length,
    });
    if (!sent) {
      // 通道已关：退回 pending，等 channelLost() 统一处理。
      chunk.state = "pending";
      this.streams.delete(chunk.stream);
      this.inflight.delete(chunk.stream);
      this.paused = true;
      this.state = "disconnected";
      return;
    }

    chunk.timer = setTimeout(() => {
      if (!this.streams.has(chunk.stream)) return;
      this.lastError = `块 ${chunk.offset} 超时`;
      this._finishStream(chunk.stream, this.chunks.indexOf(chunk), false);
    }, REQUEST_TIMEOUT);
  }

  _verify() {
    this.state = "verifying";
    this._emit();
    const h = new Sha256();
    for (const chunk of this.chunks) h.update(chunk.buffer);
    const digest = h.digestHex();
    this.verified = digest === this.file.sha256;
    this.state = this.verified ? "done" : "corrupt";
    this.lastError = this.verified ? "" : `整文件 sha256 不符：得到 ${digest}`;
    this._emit();
  }

  snapshot() {
    const elapsed = Math.max(1, performance.now() - this.startedAt);
    return {
      state: this.state,
      verified: this.verified,
      error: this.lastError,
      sha256: this.file.sha256,
      name: this.file.filename,
      size: this.file.size,
      threads: this.threads,
      totalChunks: this.totalChunks,
      doneChunks: this.doneChunks,
      doneBytes: this.doneBytes,
      inflight: this.inflight.size,
      bytesPerSec: this.doneBytes / (elapsed / 1000),
    };
  }

  _emit() {
    this.onUpdate(this.snapshot());
  }
}