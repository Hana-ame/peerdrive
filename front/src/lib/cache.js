// cache.js — lightweight LRU cache for immutable content-addressed data.
//
// Discovery context (2026-10, issue #74):
// Content-addressed data (sha256 keyed manifests, immutable preview thumbnails)
// never changes once written. Caching them avoids redundant network downloads
// across navigation and duplicate references.
//
// Features:
// - LRU eviction with max count and optional byte budget.
// - Cleanup callback on eviction (e.g. URL.revokeObjectURL).

export class LRUCache {
  constructor({ max = 32, maxBytes = 0, onEvict } = {}) {
    this.max = max;
    this.maxBytes = maxBytes;
    this.onEvict = onEvict;
    this.map = new Map();
    this.currentBytes = 0;
  }

  get(key) {
    if (!this.map.has(key)) return undefined;
    const entry = this.map.get(key);
    // Refresh position to most recently used
    this.map.delete(key);
    this.map.set(key, entry);
    return entry.value;
  }

  has(key) {
    return this.map.has(key);
  }

  set(key, value, size = 0) {
    if (this.map.has(key)) {
      const old = this.map.get(key);
      this.currentBytes -= old.size;
      this.map.delete(key);
      if (this.onEvict && old.value !== value) {
        this.onEvict(key, old.value);
      }
    }

    this.map.set(key, { value, size });
    this.currentBytes += size;

    this.prune();
  }

  delete(key) {
    if (!this.map.has(key)) return false;
    const entry = this.map.get(key);
    this.currentBytes -= entry.size;
    this.map.delete(key);
    if (this.onEvict) {
      this.onEvict(key, entry.value);
    }
    return true;
  }

  clear() {
    if (this.onEvict) {
      for (const [k, entry] of this.map.entries()) {
        this.onEvict(k, entry.value);
      }
    }
    this.map.clear();
    this.currentBytes = 0;
  }

  prune() {
    // Evict oldest entries when exceeding max count or byte limit
    while (this.map.size > this.max || (this.maxBytes > 0 && this.currentBytes > this.maxBytes && this.map.size > 0)) {
      const oldestKey = this.map.keys().next().value;
      if (oldestKey === undefined) break;
      this.delete(oldestKey);
    }
  }
}

// Module-level singleton caches for immutable content-addressed data
// 1. Manifest cache: keyed by collection sha (max 32 manifests)
export const manifestCache = new LRUCache({
  max: 32,
});

// 2. Preview cache: keyed by preview sha (max 64 thumbnails, 32MB budget, automatically revokes blob URLs)
export const previewBlobCache = new LRUCache({
  max: 64,
  maxBytes: 32 * 1024 * 1024,
  onEvict: (_sha, url) => {
    if (typeof url === 'string' && url.startsWith('blob:')) {
      try {
        URL.revokeObjectURL(url);
      } catch {
        // ignore revoke error
      }
    }
  },
});

// pLimit: limits concurrency of async tasks to avoid flooding WebSocket / network
export function pLimit(concurrency = 4) {
  let active = 0;
  const queue = [];

  const run = (fn, resolve, reject) => {
    active++;
    Promise.resolve()
      .then(fn)
      .then(
        (val) => {
          active--;
          if (queue.length > 0) {
            const next = queue.shift();
            run(next.fn, next.resolve, next.reject);
          }
          resolve(val);
        },
        (err) => {
          active--;
          if (queue.length > 0) {
            const next = queue.shift();
            run(next.fn, next.resolve, next.reject);
          }
          reject(err);
        }
      );
  };

  return function (fn) {
    return new Promise((resolve, reject) => {
      if (active < concurrency) {
        run(fn, resolve, reject);
      } else {
        queue.push({ fn, resolve, reject });
      }
    });
  };
}

export const previewLimit = pLimit(4);
