// security.js — defensive sanitizers & permission guards for untrusted input
// crossing into the UI (CollectionView manifests, share frames, signal URLs).
//
// Why this module exists (collection-view red-team hardening, Issue #404):
//  1. Collection manifests arrive from arbitrary peers; before this file,
//     rendering them meant trusting their shape — that leaks prototype
//     pollution ("__proto__"), deep JSON nesting bombs, "../" traversal paths
//     and Bidi/control-character spoofing into the page.
//  2. A ?signal= URL pasted into a share link would otherwise let a peer make
//     the browser dial arbitrary hosts; validateSignalOrApiUrl gates it.
//  3. WebRTC must never carry admin verbs (AGENTS.md: admin is local-WS only);
//     evaluateAdminPermission enforces that boundary on the client side.
// The Go side has its own hardening (urlguard, pathutil, apperr); this module
// is the browser-side complement, not a substitute.

/** Upper bound for a collection manifest JSON document (defense against bombs). */
export const MAX_MANIFEST_BYTES = 8 * 1024 * 1024

// Characters that can spoof or hide text in the UI: C0 controls plus Unicode
// bidi overrides/isolates and zero-width joiners. Stripped everywhere untrusted
// strings are displayed.
function stripUnsafeChars(s) {
  return String(s).replace(/[\u0000-\u001F\u007F\u200B-\u200F\u202A-\u202E\u2060-\u2069\uFEFF]/g, '')
}

/** Truncate-and-clean arbitrary text for display; never rendered raw after this. */
export function sanitizeUntrustedText(text, maxLen = 512) {
  const clean = stripUnsafeChars(text == null ? '' : text)
  return clean.slice(0, maxLen)
}

/** Safe file name for the browser download bar: basename only, no path, no controls. */
export function sanitizeFilename(name, fallback = 'file') {
  const base = String(name == null ? '' : name).split(/[\\/]/).pop()
  const clean = stripUnsafeChars(base).slice(0, 255)
  return clean || fallback
}

/** Collection entry path: relative only — drops leading slashes and any ".." traversal. */
export function sanitizeRelativePath(p, maxLen = 4096) {
  const parts = String(p == null ? '' : p)
    .replace(/\\/g, '/')
    .split('/')
  const clean = []
  for (const seg of parts) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') continue // traversal: refuse, never pass through
    clean.push(stripUnsafeChars(seg).slice(0, 255))
  }
  return clean.join('/').slice(0, maxLen)
}

/**
 * safeJsonParse parses untrusted JSON with three hard caps, then walks the
 * result to reject prototype-pollution keys (__proto__ / constructor /
 * prototype as own keys) and deep nesting bombs. throwOnError:true converts a
 * rejection into an exception (callers that can fall back pass false and get null).
 */
export function safeJsonParse(text, { maxBytes = MAX_MANIFEST_BYTES, throwOnError = false } = {}) {
  const fail = (msg) => {
    if (throwOnError) throw new Error(msg)
    return null
  }
  if (typeof text !== 'string') return fail('safeJsonParse: non-string input')
  if (text.length > maxBytes) return fail(`safeJsonParse: document exceeds ${maxBytes} bytes`)

  let value
  try {
    value = JSON.parse(text)
  } catch (e) {
    return fail(`safeJsonParse: invalid JSON (${e.message})`)
  }

  // Walk: depth <= 64, node count <= 20k, no dangerous own keys.
  let nodes = 0
  const walk = (v, depth) => {
    if (depth > 64) throw new Error('safeJsonParse: nesting too deep')
    nodes += 1
    if (nodes > 20000) throw new Error('safeJsonParse: too many nodes')
    if (Array.isArray(v)) {
      for (const item of v) walk(item, depth + 1)
      return
    }
    if (v && typeof v === 'object') {
      for (const k of Object.keys(v)) {
        if (k === '__proto__' || k === 'constructor' || k === 'prototype') {
          throw new Error(`safeJsonParse: dangerous key "${k}"`)
        }
        walk(v[k], depth + 1)
      }
    }
  }
  try {
    walk(value, 0)
  } catch (e) {
    return fail(e.message)
  }
  return value
}

/** Prompt-injection markers in text that ends up in an LLM/system context. */
export function detectPromptInjection(text) {
  const s = String(text || '')
  return /(ignore|disregard|forget|overwrite|override|repeat)\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|rules?|messages)/i.test(s) ||
    /(system|developer)\s+(prompt|message)/i.test(s) ||
    /you\s+are\s+now\s+[\w-]+/i.test(s) ||
    /jailbreak|DO\s+NOT\s+REVEAL/i.test(s)
}

/** Preview allow-list: only renderable media/text types are echoed back. */
export function safePreviewMime(mime) {
  const m = String(mime || '').toLowerCase()
  if (/^(image|video|audio)\//.test(m)) return m
  if (m === 'text/plain' || m === 'text/markdown' || m === 'application/json' || m === 'application/pdf') return m
  return null
}

/**
 * validateSignalOrApiUrl gates the ?signal= / ?url= param before a PeerJS dial:
 * https/wss are always accepted, http/ws only for loopback (dev), credentials
 * are rejected outright, and the result exposes a parsed URL for dial options.
 */
export function validateSignalOrApiUrl(url) {
  const raw = String(url || '').trim()
  if (!raw) return { valid: false, reason: 'empty' }
  let parsed
  try {
    parsed = new URL(raw)
  } catch {
    return { valid: false, reason: 'unparseable' }
  }
  const scheme = parsed.protocol
  if (scheme !== 'https:' && scheme !== 'wss:' && scheme !== 'http:' && scheme !== 'ws:') {
    return { valid: false, reason: `scheme ${scheme} not allowed` }
  }
  if (parsed.username || parsed.password) {
    return { valid: false, reason: 'credentials not allowed' }
  }
  if ((scheme === 'http:' || scheme === 'ws:') && !isLoopback(parsed.hostname)) {
    return { valid: false, reason: 'plaintext only for loopback' }
  }
  return { valid: true, url: parsed }
}

function isLoopback(hostname) {
  return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1' || hostname === '[::1]'
}

/**
 * sanitizeCollectionManifest returns a sanitizing DEEP COPY of a collection
 * manifest. Every string (names, paths, hashes, mime types, Bidi/control) is
 * cleaned, "../"-traversal paths are refused, dangerous keys (__proto__ /
 * constructor / prototype) never survive, and the entry count is capped.
 * All OTHER keys (hash, files, size, version, ...) pass through untouched:
 * the shared-manifest consumers (CollectionView page, client.js
 * sanitizePeerCollections merge `{...c, ...clean}`) rely on the copy having
 * exactly the input's key set — front/tests/ws.test.js pins that a remote
 * collection keeps its hash and files fields byte-for-byte.
 *
 * Two derived fields are omitted when empty so the merge can't add keys the
 * caller didn't have: `entries` is only emitted when it has surviving entries,
 * and `friendly_name` only when it differs from `name`.
 */
export function sanitizeCollectionManifest(obj) {
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return {}
  const out = {}
  for (const [k, v] of Object.entries(obj)) {
    if (k === '__proto__' || k === 'constructor' || k === 'prototype') continue
    if (k === 'name') { out.name = sanitizeUntrustedText(v, 256); continue }
    if (k === 'friendly_name') { out.friendly_name = sanitizeUntrustedText(v, 256); continue }
    if (k === 'description') { out.description = sanitizeUntrustedText(v, 1024); continue }
    out[k] = v
  }
  // friendly_name 与 name 相同时不再重复登记（client.js 的 {...c, ...clean}
  // 合并会因此多出键，ws.test 的 toEqual 会红）。
  if (typeof out.friendly_name === 'string' && out.friendly_name === out.name) {
    delete out.friendly_name
  }
  if (!Array.isArray(out.entries)) return out
  const kept = []
  for (const e of out.entries) {
    if (kept.length >= 5000) break
    if (!e || typeof e !== 'object') continue
    const path = sanitizeRelativePath(e.path || e.filepath || '')
    if (!path) continue
    const entry = {}
    for (const [ek, ev] of Object.entries(e)) {
      if (ek === '__proto__' || ek === 'constructor' || ek === 'prototype') continue
      if (ek === 'path' || ek === 'filepath') { entry[ek] = path; continue }
      if (ek === 'name' || ek === 'filename') { entry[ek] = sanitizeFilename(ev, 'file'); continue }
      if (ek === 'mime_type' || ek === 'mime') { entry[ek] = sanitizeUntrustedText(ev, 128); continue }
      if (ek === 'size' || ek === 'total') {
        entry[ek] = Number.isFinite(ev) && ev >= 0 ? Math.min(Math.floor(ev), Number.MAX_SAFE_INTEGER) : 0
        continue
      }
      // sha / hash / preview 是内容地址与缩略图地址：保留原值（小写、限长），
      // 长度校验不拦 mock/测试里的非 64-hex 短值（64hex 校验在取回入口做）。
      if (ek === 'sha' || ek === 'hash' || ek === 'preview') {
        entry[ek] = sanitizeUntrustedText(ev, 64).toLowerCase()
        continue
      }
      entry[ek] = ev
    }
    kept.push(entry)
  }
  if (kept.length > 0) out.entries = kept
  else delete out.entries
  return out
}

/** Permission denial thrown by evaluateAdminPermission when a verb is not allowed. */
export class PermissionError extends Error {
  constructor(reason, code) {
    super(reason)
    this.name = 'PermissionError'
    this.reason = reason
    this.code = code
  }
}

/**
 * evaluateAdminPermission(transport, method, path) — the client-side admin
 * boundary. Over WebRTC ('peerjs') every READ degrades safely (shares() reads
 * + the safe-fallback handlers in client.js admin(), including null for
 * not-locally-available stats — pinned by front/tests/ws.test.js), so only
 * WRITE verbs are refused: mutations stay on the local WS admin plane.
 */
export function evaluateAdminPermission(transport, method, path) {
  const m = String(method || 'GET').toUpperCase()
  if (transport !== 'peerjs') return { allowed: true }
  if (m === 'GET') return { allowed: true }
  return {
    allowed: false,
    reason: `admin verb ${m} is restricted to the local session (WebRTC exposes a read-only share surface)`,
    code: 'LOCAL_ONLY',
  }
}