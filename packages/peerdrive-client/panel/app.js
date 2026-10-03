// app.js — common panel UI logic (plain script, not module: artifact must work under file://).
//
// Global dependencies:
//   Peer     —— peerjs on CDN
//   PeerDrive —— provided by the inline bundle in the same HTML (connectToPeer / ERR / …)
//
// Design highlights:
//   - One node per session (each with its own Peer instance + DataConnection), multiple sessions can coexist;
//   - Form state writes to URL query string, the link in the browser address bar is "the current state of this panel",
//     send it directly to someone else to reproduce (combined with ?auto=1 to connect on open);
//   - Recent connections stored in localStorage, allows privacy mode download with empty dataset.

;(function () {
  'use strict'

  var LS_KEY = 'peerdrive.panel.v1'
  // Project public signaling: node and panel defaults must match, otherwise they can't find each other
  // (two signals are each a node table, discover can't find the other).
  var DEFAULT_SIG = {
    host: 'peersignal.moonchan.xyz',
    port: 443,
    path: '/',
    key: 'pd-signal-b9447b406828e500',
    secure: true,
  }
  var MAX_RECENT = 8
  var PREVIEW_MAX_BYTES = 2 * 1024 * 1024 // preview only for small files, large ones go straight to disk
  // put() reads entire content into browser memory then pushes in 64KB chunks—beyond this amount should warn first,
  // otherwise user finds out the cost only after "large file crashed mid-transfer".
  var PUT_WARN_BYTES = 256 * 1024 * 1024
  // Most upload chunks are millisecond-level, but receiver needs to write to disk; give enough margin, don't mistake slow for dead
  var PUT_CHUNK_TIMEOUT_MS = 30 * 1000

  // Task types. Write direction (put/pull) and read direction (get) mixed in one table, must be visually distinct—
  // their failure meanings are completely different: download failure is "can't get", upload failure is "can't write".
  var KIND = { get: 'Download', put: 'Local ingest', pull: 'Network ingest' }

  var sessions = new Map() // nodeId -> {id, client, status, snapshot, error}

  // This panel's **stable identity**.
  //
  // Why must fix our own: node-side private level checks "if peer peer id is in friend list",
  // while peerjs generates a random id for every `new Peer` by default — putting a next-time-changing
  // id in the list is the same as not putting it, private degrades to "nobody can get, including
  // the person you promised". So generate once, store in localStorage: one browser = one
  // fixed visitor, name can actually be filled in.
  var myId = lsRead().myId || ''
  // Here can only use persistMyId (no render): `var $ = …` is later and unassigned, touching DOM
  // now throws "TypeError: $ is not a function" and interrupts the entire IIFE —— entire panel fails,
  // and the error message has nothing to do with "no fixed id".
  if (!myId) { myId = newPanelId(); persistMyId(myId) }

  // End-to-end self-test exit: lets scripts reuse the panel's **already established** connection, not dial again.
  // Why reuse: second handshake doesn't always succeed in some environments (CI same-host loopback), while
  // self-test needs to verify "sha256 matches manifest", not "can connect a second time".
  if (typeof window !== 'undefined') {
    window.__panel = {
      sessions: sessions,
      current: function () { return current },
      get: function (id) { return sessions.get(id) },
      // End-to-end self-test: lets scripts drive these actions without relying on DOM click details.
      connect: connect,
      discoverNow: discoverNow,
      putFiles: putFiles,
      pullUrl: pullUrl,
      // Stable identity and share link (self-test should read directly, not parse DOM)
      myId: function () { return myId },
      shareLink: shareLink,
      // Linked content identification result (self-test: 'file' / 'collection')
      linkedKind: function () { return linkedKind },
    }
  }
  var current = null // currently selected nodeId
  var tasks = [] // transfer tasks (newest first)
  // pendingHash comes from share link (?hash=<64hex>). It deliberately **doesn't** depend on manifest:
  // unlisted content by definition isn't in the manifest (see renderLinked).
  var pendingHash = ''
  // Is the linked content a **file** or **collection**.
  // Collection manifest (entries list) is itself a content-addressed JSON, hash is its
  // sha256, so collection hash can also be fetched via req—what comes back is the entry list.
  // Identification method: if manifest hits, use it; if not in manifest (unlisted), fetch and check if manifest.
  var linkedKind = '' // 'file' | 'collection'
  var linkedColl = null // {name, entries:[{path,hash}], fromList}
  var linkedFor = '' // already identified "<nodeId>:<hash>", only redo when node or link changes
  var linkedBusy = false // identifying (prevent repeated fetching every manifest refresh)
  var LINK_SNIFF_BYTES = 2 * 1024 * 1024 // sniff limit: manifest usually a few KB

  // ── Utilities ────────────────────────────────────────────────────────────
  var $ = function (id) { return document.getElementById(id) }

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (ch) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]
    })
  }

  function fmtBytes(n) {
    n = Number(n) || 0
    if (n < 1024) return n + ' B'
    var units = ['KB', 'MB', 'GB', 'TB'], i = -1
    do { n /= 1024; i++ } while (n >= 1024 && i < units.length - 1)
    return n.toFixed(n < 10 ? 1 : 0) + ' ' + units[i]
  }

  function newPanelId() {
    return 'p-' + Math.random().toString(36).slice(2, 10) + Date.now().toString(36)
  }

  function uid() {
    return 't' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
  }

  function fmtAgo(ts) {
    if (!ts) return '—'
    var d = Date.now() - Number(ts)
    if (d < 60_000) return Math.max(1, Math.round(d / 1000)) + 's ago'
    if (d < 3_600_000) return Math.round(d / 60_000) + 'm ago'
    if (d < 86_400_000) return Math.round(d / 3_600_000) + 'h ago'
    return Math.round(d / 86_400_000) + 'd ago'
  }

  var LOG_TYPES = { info: 'info', ok: 'ok', warn: 'warn', err: 'err' }
  function log(text, type) {
    var div = document.createElement('div')
    div.className = 'log-line ' + (LOG_TYPES[type] || 'info')
    div.textContent = text
    var box = $('log')
    box.insertBefore(div, box.firstChild)
    while (box.children.length > 200) box.removeChild(box.lastChild)
  }

  function copyText(s) {
    if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(s)
    return Promise.reject(new Error('Clipboard unavailable'))
  }

  // ── URL state (the address bar IS the panel state) ───────────────────────
  function sigFromForm() {
    var secure = $('in-secure').checked
    var port = $('in-port').value.trim()
    if (port === '') port = secure ? '443' : '80'
    return {
      host: $('in-host').value.trim(),
      port: Number(port),
      path: $('in-path').value.trim(),
      key: $('in-key').value.trim(),
      secure: secure,
    }
  }

  function urlOf(sig) {
    var scheme = sig.secure ? 'https' : 'http'
    var port = sig.port === (sig.secure ? 443 : 80) ? '' : ':' + sig.port
    var path = sig.path || '/'
    return scheme + '://' + sig.host + port + path + sig.key
  }

  function syncURL() {
    // Write current form state to URL query string; this link IS the current panel state.
    var node = $('in-node').value.trim()
    var psk = $('in-psk').value
    var sig = sigFromForm()
    var q = new URLSearchParams()
    if (node) q.set('node', node)
    if (psk) q.set('psk', psk)
    if (sig.host) q.set('host', sig.host)
    if (sig.port) q.set('port', String(sig.port))
    if (sig.path && sig.path !== '/') q.set('path', sig.path)
    if (sig.key) q.set('key', sig.key)
    if (!sig.secure) q.set('secure', '0')
    try {
      history.replaceState(null, '', location.pathname + '?' + q.toString())
    } catch (e) {
      /* may not be allowed under file:// */
    }
  }

  function applyURL() {
    var q = new URLSearchParams(location.search)
    // PSK never appears in URL (privacy), only in form
    if (q.get('host')) $('in-host').value = q.get('host')
    if (q.get('port')) $('in-port').value = q.get('port')
    if (q.get('path')) $('in-path').value = q.get('path')
    if (q.get('key')) $('in-key').value = q.get('key')
    if (q.get('secure') !== null) $('in-secure').checked = q.get('secure') !== '0'
    // node
    if (q.get('node')) $('in-node').value = q.get('node')
    // Pre-shared key: if present in URL, fill form then remove from URL (don't keep in history)
    var pq = q.get('psk')
    if (pq) {
      $('in-psk').value = pq
      var nq = new URLSearchParams(q)
      nq.delete('psk')
      try {
        history.replaceState(null, '', location.pathname + '?' + nq.toString())
      } catch (e) {
        /* may not be allowed under file:// */
      }
      log('Read pre-shared key from link and removed it from address bar (not left in history)', 'warn')
    }

    // hash from share link (entry point for unlisted fetching)
    var hq = (q.get('hash') || '').trim()
    if (hq) {
      if (/^[0-9a-f]{64}$/i.test(hq)) {
        pendingHash = hq
        log('Link contains a hash (' + hq.slice(0, 16) + '…), will fetch after connecting to node', 'ok')
      } else {
        log('Hash in link is not 64 hex chars, ignored: ' + hq.slice(0, 32), 'warn')
      }
    }

    $('btn-connect').addEventListener('click', connect)
    $('btn-discover').addEventListener('click', discoverNow)
    $('btn-put').addEventListener('click', putFiles)
    $('btn-pull').addEventListener('click', pullUrl)
    if ($('btn-copy-id')) $('btn-copy-id').addEventListener('click', function () {
      copyText(myId).then(function (done) {
        done ? log('Copied my node id: ' + myId + ' —— give it to the node operator, adding to friend list unlocks private content', 'ok')
          : log('Copy failed, my node id: ' + myId, 'warn')
      })
    })
    if ($('btn-new-id')) $('btn-new-id').addEventListener('click', function () {
      var old = myId
      saveMyId(newPanelId())
      log('Changed my node id: ' + old + ' → ' + myId +
        ' (after changing, old id in operator friend list is invalidated, remember to send new one)', 'warn')
    })
    $('in-url').addEventListener('keydown', function (e) { if (e.key === 'Enter') pullUrl() })
    $('in-node').addEventListener('keydown', function (e) { if (e.key === 'Enter') connect() })
    $('in-secure').addEventListener('change', function () {
      syncURL()
      if (!mixedContent()) log('Signaling uses wss, compatible with HTTPS pages', 'ok')
    })
    ;['in-host', 'in-port', 'in-path', 'in-key'].forEach(function (id) {
      $(id).addEventListener('change', syncURL)
    })

    renderRecent()
    renderTasks()
    renderMyId()
    renderLinked(sessions.get(current))
    log('Ready. This panel is a single static file, no local backend needed.')
    // Under HTTPS hosting (GitHub Pages), ws:// is always blocked, explain upfront, don't wait for unresponsive click
    if (mixedContent()) log(mixedContentHint(), 'warn')

    window.addEventListener('peerjs-ready', function (e) {
      if (e.detail && e.detail.ok) log('peerjs loaded (' + e.detail.src + ')', 'ok')
      else log(peerMissingHint(), 'err')
    })

    if (q.get('auto') === '1' && $('in-node').value.trim()) {
      // With auto=1, page may not have peerjs yet, wait for it before connecting
      if (peerReady()) connect()
      else window.addEventListener('peerjs-ready', function once(e) {
        window.removeEventListener('peerjs-ready', once)
        if (e.detail && e.detail.ok) connect()
      })
    }
  }

  // ── PeerJS CDN check ────────────────────────────────────────────────────
  function peerReady() {
    return typeof window !== 'undefined' && typeof window.Peer === 'function'
  }

  var PEER_MISSING = 'PeerJS not loaded — check network or CDN link, then refresh'

  function peerMissingHint() {
    var cdn = $('peerjs-cdn')
    var hint = PEER_MISSING
    if (cdn) hint += '\nCDN link: ' + cdn.src
    return hint
  }

  // ── Security: mixed content ───────────────────────────────────────────────
  function mixedContent() {
    return location.protocol === 'https:' && !$('in-secure').checked
  }

  function mixedContentHint() {
    return 'This page is HTTPS but signaling is set to ws (insecure). Browser will block ws:// connections (Mixed Content). Switch "secure" to on, or serve this page over http.'
  }

  // ── Session management ────────────────────────────────────────────────────
  function connect() {
    var nodeId = $('in-node').value.trim()
    if (!nodeId) {
      log('Please enter a node ID first', 'warn')
      return
    }
    if (!peerReady()) {
      log(peerMissingHint(), 'err')
      return
    }

    disconnectAll()
    setConnecting(nodeId)
    syncURL()

    var sig = sigFromForm()
    var psk = $('in-psk').value || ''
    var openT = setTimeout(function () {
      if (sessions.get(nodeId) && sessions.get(nodeId).status === 'connecting') {
        setFailed(nodeId, new Error('Connection timed out'))
      }
    }, 20000)

    window.PeerDrive.connectToPeer(window.Peer, nodeId, {
      peerOptions: {
        host: sig.host, port: sig.port, path: sig.path, key: sig.key, secure: sig.secure,
        debug: 1,
      },
      connOptions: { reliable: true, serialization: 'raw' },
      psk: psk,
      openTimeoutMs: 20000,
    }).then(function (client) {
      clearTimeout(openT)
      var s = sessions.get(nodeId)
      if (!s) return // already disconnected
      s.client = client
      s.status = 'ready'
      renderSessions()
      log('Connected to ' + nodeId, 'ok')
      shareNow()
      if (pendingHash) maybeFetchLinked(nodeId)
    }).catch(function (err) {
      clearTimeout(openT)
      setFailed(nodeId, err)
    })
  }

  function disconnectAll() {
    sessions.forEach(function (s, id) {
      if (s.client) {
        try { s.client.close() } catch (e) { /* ignore */ }
      }
    })
    sessions.clear()
    current = null
    renderSessions()
  }

  function setConnecting(nodeId) {
    var s = sessions.get(nodeId) || {}
    s.id = nodeId
    s.status = 'connecting'
    s.client = null
    sessions.set(nodeId, s)
    current = nodeId
    renderSessions()
    renderLinked(s)
  }

  function setFailed(nodeId, err) {
    var s = sessions.get(nodeId)
    if (s) {
      s.status = 'failed'
      s.error = err
      s.snapshot = null
    }
    renderSessions()
    log('Connection failed: ' + (err && err.message ? err.message : err), 'err')
  }

  function renderSessions() {
    var box = $('sessions')
    box.innerHTML = ''
    sessions.forEach(function (s, id) {
      var chip = document.createElement('div')
      var st = s.status || 'idle'
      chip.className = 'session ' + st + (id === current ? ' current' : '')
      chip.innerHTML =
        '<span class="sid">' + esc(id) + '</span>' +
        '<span class="st">' + esc(statusText(st, s)) + '</span>' +
        '<span class="kill" data-id="' + esc(id) + '" title="Disconnect">✕</span>'
      chip.querySelector('.kill').addEventListener('click', function (e) {
        e.stopPropagation()
        killSession(id)
      })
      chip.addEventListener('click', function () {
        current = id
        renderSessions()
        renderLinked(s)
      })
      box.appendChild(chip)
    })
  }

  function statusText(st, s) {
    if (st === 'connecting') return 'connecting…'
    if (st === 'ready') {
      var n = s.snapshot ? (s.snapshot.collections + s.snapshot.files) : 0
      return 'ready · ' + n + ' items'
    }
    if (st === 'failed') return 'failed'
    return 'idle'
  }

  function killSession(id) {
    var s = sessions.get(id)
    if (s && s.client) {
      try { s.client.close() } catch (e) { /* ignore */ }
    }
    sessions.delete(id)
    if (current === id) current = null
    renderSessions()
    renderLinked(sessions.get(current))
    log('Disconnected from ' + id)
  }

  // ── Manifest (share frame) ───────────────────────────────────────────────
  function shareNow() {
    var s = sessions.get(current)
    if (!s || !s.client) return
    s.snapshot = null
    renderLinked(s)
    log('Fetching manifest…')
    s.client.shares().then(function (snap) {
      s.snapshot = snap
      renderSessions()
      renderLinked(s)
      log('Manifest: ' + snap.total + ' items (' + snap.collections + ' collections, ' + snap.files + ' files)', 'ok')
      // After manifest arrives, check if share-link hash hits in manifest
      maybeFetchLinked(current)
    }).catch(function (err) {
      log('Failed to fetch manifest: ' + (err && err.message ? err.message : err), 'err')
      renderLinked(s)
    })
  }

  function renderLinked(s) {
    var box = $('linked')
    if (!box) return
    box.innerHTML = ''
    if (!s) return

    // No share link scenario
    if (!pendingHash) {
      // Empty state: show hint about how to get content
      if (s.status === 'ready') {
        box.innerHTML = '<div class="empty-hint">Connect to a node and browse its shared content below. To fetch a specific file, use a share link with ?hash= parameter.</div>'
      }
      return
    }

    // Has hash: try to match in manifest
    var snap = s.snapshot
    var matched = null
    if (snap) {
      for (var i = 0; i < snap.files.length; i++) {
        if (snap.files[i].hash === pendingHash) { matched = snap.files[i]; break }
      }
      if (!matched) {
        for (var j = 0; j < snap.collections; j++) {
          var c = snap.collections[j]
          if (c.entries && c.entries.length) {
            for (var k = 0; k < c.entries.length; k++) {
              if (c.entries[k].hash === pendingHash) { matched = c.entries[k]; break }
            }
          }
          if (matched) break
        }
      }
    }

    if (matched) {
      linkedKind = 'file'
      box.innerHTML =
        '<div class="linked-item file">' +
        '<span class="li-label">📄</span>' +
        '<span class="li-name">' + esc(matched.name || matched.path || 'unnamed') + '</span>' +
        '<span class="li-size">' + esc(fmtBytes(matched.size || 0)) + '</span>' +
        '<span class="li-actions">' +
        '<button data-act="dl" data-hash="' + esc(pendingHash) + '" data-name="' + esc(matched.name || matched.path || '') + '">⬇ Download</button>' +
        '</span></div>'
      bindLinkedButtons()
    } else if (snap) {
      // Not in manifest: may be unlisted (not in manifest). Try to fetch it.
      box.innerHTML = '<div class="linked-item sniffing"><span class="li-label">🔍</span> Checking if this is an unlisted file…</div>'
      fetchLinked(nodeId)
    } else {
      box.innerHTML = '<div class="linked-item waiting"><span class="li-label">⏳</span> Waiting for manifest to check…</div>'
    }
  }

  function maybeFetchLinked(nodeId) {
    if (!pendingHash) return
    var s = sessions.get(nodeId)
    if (!s || !s.snapshot) return
    if (linkedFor === nodeId + ':' + pendingHash) return
    if (linkedBusy) return
    linkedBusy = true
    linkedFor = nodeId + ':' + pendingHash
    fetchLinked(nodeId)
  }

  function fetchLinked(nodeId) {
    var s = sessions.get(nodeId)
    if (!s || !s.client) return
    var box = $('linked')
    box.innerHTML = '<div class="linked-item sniffing"><span class="li-label">🔍</span> Fetching to identify content…</div>'

    var task = addTask({ kind: 'get', name: 'Linked content (' + pendingHash.slice(0, 12) + '…)', hash: pendingHash })
    s.client.fetch(pendingHash, { maxBytes: LINK_SNIFF_BYTES }).then(function (bytes) {
      // Try to parse as JSON (manifest)
      var text = new TextDecoder('utf-8').decode(bytes)
      var obj = null
      try { obj = JSON.parse(text) } catch (e) { /* not JSON */ }

      if (obj && Array.isArray(obj.entries) && obj.entries.length) {
        // It's a collection manifest
        linkedKind = 'collection'
        linkedColl = {
          name: obj.name || 'Collection',
          entries: obj.entries.map(function (e) { return { path: e.path || e.name || '', hash: e.hash || '', size: e.size || 0 } }),
          fromList: false,
        }
        renderCollEntries(box)
        log('Share link is a collection manifest: ' + linkedColl.name + ' (' + linkedColl.entries.length + ' entries)', 'ok')
        finishTask(task, true)
      } else {
        // It's a regular file
        linkedKind = 'file'
        box.innerHTML =
          '<div class="linked-item file">' +
          '<span class="li-label">📄</span>' +
          '<span class="li-name">Unlisted file</span>' +
          '<span class="li-size">' + esc(fmtBytes(bytes.byteLength)) + '</span>' +
          '<span class="li-actions">' +
          '<button data-act="dl-unlisted" data-hash="' + esc(pendingHash) + '" data-bytes="' + bytes.byteLength + '">⬇ Download</button>' +
          '</span></div>'
        bindLinkedButtons()
        finishTask(task, true)
      }
    }).catch(function (err) {
      linkedKind = 'error'
      box.innerHTML =
        '<div class="linked-item error">' +
        '<span class="li-label">❌</span>' +
        '<span class="li-name">Failed to fetch: ' + esc(err && err.message ? err.message : err) + '</span>' +
        '</div>'
      finishTask(task, false, err)
    })
    linkedBusy = false
  }

  function renderCollEntries(box) {
    if (!linkedColl) return
    box.innerHTML =
      '<div class="linked-item collection">' +
      '<span class="li-label">📁</span>' +
      '<span class="li-name">' + esc(linkedColl.name) + '</span>' +
      '<span class="li-size">' + linkedColl.entries.length + ' entries</span>' +
      '</div>' +
      '<div class="coll-entries">' +
      linkedColl.entries.map(function (e, i) {
        return '<div class="coll-entry">' +
          '<span class="ce-name">' + esc(e.path) + '</span>' +
          '<span class="ce-size">' + esc(fmtBytes(e.size)) + '</span>' +
          '<button data-act="dl" data-hash="' + esc(e.hash) + '" data-name="' + esc(e.path) + '">⬇</button>' +
          '</div>'
      }).join('') +
      '</div>'
    bindLinkedButtons()
  }

  function bindLinkedButtons() {
    var btns = document.querySelectorAll('.linked-item [data-act], .coll-entry [data-act]')
    for (var i = 0; i < btns.length; i++) {
      btns[i].addEventListener('click', function (e) {
        var act = this.getAttribute('data-act')
        var hash = this.getAttribute('data-hash')
        var name = this.getAttribute('data-name') || ''
        if (act === 'dl') {
          downloadFile(hash, name)
        } else if (act === 'dl-unlisted') {
          downloadUnlisted(hash, name)
        }
      })
    }
  }

  function downloadFile(hash, name) {
    var s = sessions.get(current)
    if (!s || !s.client) return
    var task = addTask({ kind: 'get', name: name || hash.slice(0, 12), hash: hash })
    s.client.fetch(hash).then(function (bytes) {
      var blob = new Blob([bytes])
      var url = URL.createObjectURL(blob)
      var a = document.createElement('a')
      a.href = url
      a.download = name || hash.slice(0, 16) + '.bin'
      document.body.appendChild(a)
      a.click()
      a.remove()
      setTimeout(function () { URL.revokeObjectURL(url) }, 30000)
      finishTask(task, true)
      log('Downloaded: ' + (name || hash.slice(0, 12)), 'ok')
    }).catch(function (err) {
      finishTask(task, false, err)
      log('Download failed: ' + (err && err.message ? err.message : err), 'err')
    })
  }

  function downloadUnlisted(hash, name) {
    var s = sessions.get(current)
    if (!s || !s.client) return
    var task = addTask({ kind: 'get', name: 'Unlisted file (' + hash.slice(0, 12) + '…)', hash: hash })
    s.client.fetch(hash).then(function (bytes) {
      var blob = new Blob([bytes])
      var url = URL.createObjectURL(blob)
      var a = document.createElement('a')
      a.href = url
      a.download = name || hash.slice(0, 16)
      document.body.appendChild(a)
      a.click()
      a.remove()
      setTimeout(function () { URL.revokeObjectURL(url) }, 30000)
      finishTask(task, true)
      log('Downloaded unlisted file', 'ok')
    }).catch(function (err) {
      finishTask(task, false, err)
      log('Download failed: ' + (err && err.message ? err.message : err), 'err')
    })
  }

  // ── Task list ──────────────────────────────────────────────────────────
  function addTask(info) {
    var task = {
      id: uid(),
      kind: info.kind,
      name: info.name,
      hash: info.hash || '',
      status: 'running',
      progress: 0,
      total: 0,
      bytes: 0,
      started: Date.now(),
    }
    tasks.unshift(task)
    renderTasks()
    return task
  }

  function finishTask(task, ok, err) {
    task.status = ok ? 'done' : 'failed'
    if (err) task.error = err.message || String(err)
    task.ended = Date.now()
    renderTasks()
  }

  function updateTask(task, received, total) {
    task.progress = received
    task.total = total
    task.bytes = received
    renderTasks()
  }

  function renderTasks() {
    var box = $('tasks')
    if (!box) return
    box.innerHTML = ''
    for (var i = 0; i < tasks.length; i++) {
      var t = tasks[i]
      var pct = t.total > 0 ? Math.round((t.progress / t.total) * 100) : 0
      var row = document.createElement('div')
      row.className = 'task ' + t.status
      row.innerHTML =
        '<span class="task-kind">' + esc(KIND[t.kind] || t.kind) + '</span>' +
        '<span class="task-name">' + esc(t.name) + '</span>' +
        '<span class="task-prog">' + esc(fmtBytes(t.progress) + ' / ' + (t.total > 0 ? fmtBytes(t.total) : '?')) + ' (' + pct + '%)</span>' +
        '<span class="task-st">' + esc(t.status) + '</span>'
      box.appendChild(row)
    }
  }

  // ── Put (local upload) ──────────────────────────────────────────────────
  function putFiles() {
    var s = sessions.get(current)
    if (!s || !s.client) {
      log('Connect to a node first', 'warn')
      return
    }
    var input = document.createElement('input')
    input.type = 'file'
    input.multiple = true
    input.addEventListener('change', function () {
      var files = input.files
      if (!files || !files.length) return
      for (var i = 0; i < files.length; i++) {
        putOne(s.client, files[i])
      }
    })
    input.click()
  }

  function putOne(client, file) {
    if (file.size > PUT_WARN_BYTES) {
      var ok = confirm('File is ' + fmtBytes(file.size) + ', exceeding ' + fmtBytes(PUT_WARN_BYTES) + '. This will load entire file into browser memory. Continue?')
      if (!ok) return
    }
    var task = addTask({ kind: 'put', name: file.name, hash: '' })
    client.put(file, {
      name: file.name,
      onProgress: function (sent, total) {
        updateTask(task, sent, total)
      },
      timeoutMs: PUT_CHUNK_TIMEOUT_MS,
    }).then(function (res) {
      task.hash = res.hash
      finishTask(task, true)
      log('Uploaded: ' + file.name + ' → ' + res.hash.slice(0, 12) + '…', 'ok')
      shareNow()
    }).catch(function (err) {
      finishTask(task, false, err)
      log('Upload failed: ' + file.name + ' — ' + (err && err.message ? err.message : err), 'err')
    })
  }

  // ── Pull (network ingest) ───────────────────────────────────────────────
  function pullUrl() {
    var s = sessions.get(current)
    if (!s || !s.client) {
      log('Connect to a node first', 'warn')
      return
    }
    var url = $('in-url').value.trim()
    if (!url) {
      log('Please enter a URL first', 'warn')
      return
    }
    var name = url.split('/').pop() || 'pulled'
    var task = addTask({ kind: 'pull', name: name, hash: '' })
    client.pull(url, { name: name, timeoutMs: 60000 }).then(function (res) {
      task.hash = res.hash
      finishTask(task, true)
      log('Pulled: ' + url + ' → ' + res.hash.slice(0, 12) + '… (' + fmtBytes(res.size) + ')', 'ok')
      shareNow()
    }).catch(function (err) {
      finishTask(task, false, err)
      log('Pull failed: ' + (err && err.message ? err.message : err), 'err')
    })
  }

  // ── Auto-discovery ──────────────────────────────────────────────────────
  function discoverNow() {
    var sig = sigFromForm()
    log('Discovering nodes…')
    window.PeerDrive.discoverNodes(sig).then(function (nodes) {
      log('Discovered ' + nodes.length + ' nodes', 'ok')
      renderDiscover(nodes)
    }).catch(function (err) {
      log('Discovery failed: ' + (err && err.message ? err.message : err), 'err')
    })
  }

  function renderDiscover(nodes) {
    var box = $('discover')
    if (!box) return
    box.innerHTML = ''
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i]
      var row = document.createElement('div')
      row.className = 'discover-item'
      row.innerHTML =
        '<span class="di-id">' + esc(n.peerId) + '</span>' +
        '<span class="di-type">' + esc(n.nodeType || 'unknown') + '</span>' +
        '<span class="di-seen">' + esc(fmtAgo(n.lastSeen)) + '</span>' +
        '<button data-id="' + esc(n.peerId) + '">Connect</button>'
      row.querySelector('button').addEventListener('click', function (e) {
        $('in-node').value = e.target.getAttribute('data-id')
        connect()
      })
      box.appendChild(row)
    }
  }

  // ── My ID display ───────────────────────────────────────────────────────
  function renderMyId() {
    var box = $('my-id')
    if (box) box.textContent = myId
  }

  function lsRead() {
    try {
      return JSON.parse(localStorage.getItem(LS_KEY) || '{}')
    } catch (e) {
      return {}
    }
  }

  function saveMyId(id) {
    var d = lsRead()
    d.myId = id
    try {
      localStorage.setItem(LS_KEY, JSON.stringify(d))
    } catch (e) {
      /* localStorage unavailable */
    }
    myId = id
    renderMyId()
  }

  function persistMyId(id) {
    var d = lsRead()
    d.myId = id
    try {
      localStorage.setItem(LS_KEY, JSON.stringify(d))
    } catch (e) {
      /* localStorage unavailable */
    }
    myId = id
  }

  // ── Recent connections ───────────────────────────────────────────────────
  function addRecent(nodeId) {
    var d = lsRead()
    d.recent = d.recent || []
    d.recent = [nodeId].concat(d.recent.filter(function (n) { return n !== nodeId })).slice(0, MAX_RECENT)
    try {
      localStorage.setItem(LS_KEY, JSON.stringify(d))
    } catch (e) {
      /* ignore */
    }
  }

  function renderRecent() {
    var d = lsRead()
    var recent = d.recent || []
    var box = $('recent')
    if (!box) return
    box.innerHTML = ''
    for (var i = 0; i < recent.length; i++) {
      var chip = document.createElement('span')
      chip.className = 'recent-chip'
      chip.textContent = recent[i]
      chip.addEventListener('click', function () {
        $('in-node').value = chip.textContent
        connect()
      })
      box.appendChild(chip)
    }
  }

  // ── Share link generation ────────────────────────────────────────────────
  function shareLink(nodeId) {
    var hash = pendingHash
    if (!hash && sessions.get(nodeId)) {
      var s = sessions.get(nodeId)
      var snap = s.snapshot
      if (snap && snap.files.length > 0) {
        hash = snap.files[0].hash
      }
    }
    var sig = sigFromForm()
    var parts = [location.origin || '']
    parts.push(location.pathname)
    var params = []
    if (nodeId) params.push('node=' + encodeURIComponent(nodeId))
    if (hash) params.push('hash=' + encodeURIComponent(hash))
    if (params.length) parts.push('?' + params.join('&'))
    return parts.join('')
  }

  // ── Boot ─────────────────────────────────────────────────────────────────
  function boot() {
    // Initialize defaults
    $('in-host').value = DEFAULT_SIG.host
    $('in-port').value = DEFAULT_SIG.port
    $('in-path').value = DEFAULT_SIG.path
    $('in-key').value = DEFAULT_SIG.key
    $('in-secure').checked = DEFAULT_SIG.secure

    // Apply URL state
    applyURL()

    // Add to recent
    var node = $('in-node').value.trim()
    if (node) addRecent(node)

    // Render initial state
    renderSessions()
    renderTasks()
    renderMyId()
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot)
  else boot()
})()
