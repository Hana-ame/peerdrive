// url.js — canonical self-contained URL generation/parsing (invariant #2 SSOT).
//
// Two URL dialects, both uniquely address a SHA256 file or a Collection:
//   SPA  mode  ->  <base>#/f/<sha256>?peer=<peerId>&name=<filename>  (hash routes)
//   HTTP mode  ->  <base>/f/<sha256>[/<filename>]                    (backend direct)
//                 <base>/c/<hash>[/<entryPath>]                      (collection manifest/entry)
//
// parseDirectTarget accepts any of: a bare 64-hex hash (+kind hint), a SPA hash
// route, a legacy /api/v1/... route, the public panel link (?node=&hash=), so
// collection/file deep links can be pasted anywhere and still resolve to the
// right content. The backend /f/ /c/ handlers are a known gap on the mainline
// (excised 2026-10-17) — this module stays as the contract for when they land.

import { sanitizeRelativePath } from './security.js'

// Named URL_HASH_RE (not HASH_RE): build-panel.mjs inlines every core file into
// one flat IIFE scope, and protocol.js already owns a top-level HASH_RE.
const URL_HASH_RE = /\b[0-9a-f]{64}\b/

function baseNoSlash(baseUrl) {
  return String(baseUrl || '').replace(/\/+$/, '')
}

function queryString(params) {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
  }
  const s = qs.toString()
  return s ? `?${s}` : ''
}

// buildFileUrl hash param is trusted (a 64-hex sha256 from file_index); only the
// query values are untrusted user data, so only those go through URLSearchParams
// (spaces become '+' in the SPA link, matching the pinned core tests).
export function buildFileUrl(hash, { baseUrl = '', peerId = '', filename = '', mode = 'spa' } = {}) {
  const base = baseNoSlash(baseUrl)
  if (mode === 'http') {
    return filename ? `${base}/f/${hash}/${filename}` : `${base}/f/${hash}`
  }
  return `${base}#/f/${hash}${queryString({ peer: peerId, name: filename })}`
}

export function buildCollectionUrl(hash, { baseUrl = '', peerId = '', name = '', entryPath = '', mode = 'spa' } = {}) {
  const base = baseNoSlash(baseUrl)
  if (mode === 'http') {
    return entryPath ? `${base}/c/${hash}/${entryPath}` : `${base}/c/${hash}`
  }
  return `${base}#/c/${hash}${queryString({ peer: peerId, name })}`
}

/**
 * parseDirectTarget resolves any pasted target to {kind, route, hash, peerId?,
 * filename?, entryPath?}. The bare-hash form needs a kind hint ('file' | 'collection');
 * every URL form carries enough information to infer kind from the path shape.
 */
export function parseDirectTarget(input, hint = '') {
  const str = String(input || '').trim()
  const qIdx = str.indexOf('?')
  const pathPart = qIdx >= 0 ? str.slice(0, qIdx) : str
  const query = qIdx >= 0 ? new URLSearchParams(str.slice(qIdx + 1)) : new URLSearchParams()

  const hash = (str.match(URL_HASH_RE) || [])[0] || ''

  // Normalize to a route-ish string: drop scheme/host, keep the fragment path.
  let route = pathPart
  const fragIdx = route.indexOf('#')
  if (fragIdx >= 0) route = route.slice(fragIdx + 1)
  route = route.replace(/^https?:\/\/[^/]+/, '').replace(/^\/+/, '')

  let kind = hint
  if (/^f\//.test(route)) kind = 'file'
  else if (/^c\//.test(route)) kind = 'collection'
  else if (/\/(f|c)\/[0-9a-f]{64}(\/|$)/.test(pathPart) && !kind) {
    // legacy /api/v1/f/... or /api/v1/c/... forms
    kind = /\/f\/[0-9a-f]{64}/.test(pathPart) ? 'file' : 'collection'
  } else if ((/panel/.test(pathPart)) && query.get('hash') && !kind) {
    kind = 'file' // public panel deep link carries ?node=&hash=
  }
  kind = kind === 'collection' ? 'collection' : kind === 'file' ? 'file' : ''

  const peerId = query.get('peer') || query.get('node') || ''
  const filename = query.get('name') || ''

  let entryPath = ''
  if (kind === 'collection') {
    // Both the SPA form (c/<hash>/<rest>) and the legacy /api/v1/c/<hash>/<rest>
    // form carry the entry path right after the collection hash.
    const m = route.match(/(?:^|\/)c\/[0-9a-f]{64}\/(.+)$/)
    if (m && m[1]) {
      const normalizedSlashes = m[1].replace(/%2f/gi, '/').replace(/%5c/gi, '/')
      const decodedSegs = normalizedSlashes.split('/').map((seg) => {
        try {
          return decodeURIComponent(seg)
        } catch {
          return seg
        }
      })
      entryPath = sanitizeRelativePath(decodedSegs.join('/'))
    }
  }

  const out = { kind }
  if (hash) out.hash = hash
  if (kind === 'file' && hash) out.route = `/f/${hash}`
  if (kind === 'collection' && hash) out.route = `/c/${hash}`
  if (peerId) out.peerId = peerId
  if (filename) out.filename = filename
  if (entryPath) out.entryPath = entryPath
  return out
}