// app.js — 公共面板的 UI 逻辑（普通脚本，不是 module：产物要在 file:// 下能跑）。
//
// 依赖的全局：
//   Peer     —— CDN 上的 peerjs
//   PeerDrive —— 由同一个 HTML 里内联的 bundle 提供（connectToPeer / ERR / …）
//
// 设计要点：
//   - 一个节点一个会话（各自的 Peer 实例 + DataConnection），可以并存多个节点；
//   - 表单状态会写进 URL 查询串，浏览器地址栏里那条链接就是「这个面板现在的状态」，
//     直接发给别人即可复现（配合 ?auto=1 打开即连）；
//   - 最近连接存 localStorage，允许 dataset 为空的隐私模式下载 degrade。

;(function () {
  'use strict'

  var LS_KEY = 'peerdrive.panel.v1'
  var MAX_RECENT = 8
  var PREVIEW_MAX_BYTES = 2 * 1024 * 1024 // 预览只给小文件，大的直接存盘

  var sessions = new Map() // nodeId -> {id, client, status, snapshot, error}

  // 端到端自检用的出口：让脚本能复用面板**已经建立**的那条连接，而不是再拨一次。
  // 为什么要复用：第二次握手在部分环境（CI 的同机 loopback）并不总是成功，而
  // 自检要验的是「sha256 是否与清单一致」，不是「能不能连第二次」。
  if (typeof window !== 'undefined') {
    window.__panel = {
      sessions: sessions,
      current: function () { return current },
      get: function (id) { return sessions.get(id) },
    }
  }
  var current = null // 当前选中的 nodeId
  var tasks = [] // 传输任务（最新的在前）

  // ── 小工具 ────────────────────────────────────────────────────────────
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

  function fmtRate(bytesPerSec) {
    return fmtBytes(bytesPerSec) + '/s'
  }

  function log(msg, cls) {
    var box = $('log')
    if (!box) return
    var el = document.createElement('div')
    if (cls) el.className = cls
    el.textContent = '[' + new Date().toLocaleTimeString('zh-CN', { hour12: false }) + '] ' + msg
    box.appendChild(el)
    box.scrollTop = box.scrollHeight
  }

  function lsRead() {
    try { return JSON.parse(localStorage.getItem(LS_KEY) || '{}') } catch (e) { return {} }
  }

  function lsWrite(obj) {
    try { localStorage.setItem(LS_KEY, JSON.stringify(obj)) } catch (e) { /* 隐私模式：忽略 */ }
  }

  function params() {
    return new URLSearchParams(location.search)
  }

  function sigOfForm() {
    return {
      host: $('in-host').value.trim() || '0.peerjs.com',
      port: Number($('in-port').value.trim() || 443),
      path: $('in-path').value.trim() || '/',
      key: $('in-key').value.trim() || 'peerjs',
      secure: $('in-secure').checked,
    }
  }

  // 面板状态回写地址栏 —— 让用户能直接复制「连这个节点的面板」链接
  function syncURL() {
    var q = params()
    var sig = sigOfForm()
    q.set('host', sig.host)
    q.set('port', String(sig.port))
    q.set('path', sig.path)
    q.set('key', sig.key)
    q.set('secure', sig.secure ? '1' : '0')
    if ($('in-node').value.trim()) q.set('node', $('in-node').value.trim())
    else q.delete('node')
    try { history.replaceState(null, '', location.pathname + '?' + q.toString()) } catch (e) { /* file:// 下可能不让改 */ }
  }

  function remember(nodeId, sig) {
    var st = lsRead()
    var recent = (st.recent || []).filter(function (r) { return r.id !== nodeId })
    recent.unshift({ id: nodeId, sig: sig, at: Date.now() })
    st.recent = recent.slice(0, MAX_RECENT)
    lsWrite(st)
    renderRecent()
  }

  // ── 渲染 ──────────────────────────────────────────────────────────────
  function statusClass(st) {
    return st === 'online' ? 'on' : st === 'connecting' ? 'wait' : st === 'error' ? 'bad' : ''
  }

  function renderNodes() {
    var box = $('nodes')
    if (!sessions.size) { box.innerHTML = '<div class="empty">还没有连接任何节点。</div>'; return }
    box.innerHTML = ''
    sessions.forEach(function (s) {
      var row = document.createElement('div')
      row.className = 'node' + (s.id === current ? ' sel' : '')
      var n = s.snapshot ? s.snapshot.total : null
      row.innerHTML =
        '<span class="dot ' + statusClass(s.status) + '"></span>' +
        '<span class="nm mono" title="' + esc(s.id) + '">' + esc(s.id) + '</span>' +
        '<span class="faint" style="font-size:11px">' + (n == null ? '' : n + ' 项') + '</span>'
      row.addEventListener('click', function () { select(s.id) })

      var close = document.createElement('button')
      close.className = 'tiny ghost'
      close.textContent = '×'
      close.title = '断开'
      close.addEventListener('click', function (ev) { ev.stopPropagation(); disconnect(s.id) })
      row.appendChild(close)
      box.appendChild(row)
    })
  }

  function renderRecent() {
    var box = $('recent')
    var recent = (lsRead().recent || [])
    if (!recent.length) { box.innerHTML = '<div class="empty">暂无记录。</div>'; return }
    box.innerHTML = ''
    recent.forEach(function (r) {
      var row = document.createElement('div')
      row.className = 'node'
      row.innerHTML =
        '<span class="nm mono" title="' + esc(r.id) + '">' + esc(r.id) + '</span>' +
        '<span class="faint" style="font-size:11px">' + esc(r.sig.host || '') + '</span>'
      row.addEventListener('click', function () {
        $('in-node').value = r.id
        if (r.sig) {
          $('in-host').value = r.sig.host || ''
          $('in-port').value = r.sig.port || 443
          $('in-path').value = r.sig.path || '/'
          $('in-key').value = r.sig.key || 'peerjs'
          $('in-secure').checked = r.sig.secure !== false
        }
        connect()
      })
      box.appendChild(row)
    })
  }

  function renderTasks() {
    var box = $('tasks')
    if (!tasks.length) { box.innerHTML = '<div class="empty">没有进行中的传输。</div>'; return }
    var html = '<table><thead><tr><th>文件</th><th>来源</th><th class="num">进度</th><th class="num">速率</th><th>状态</th><th></th></tr></thead><tbody>'
    tasks.forEach(function (t) {
      var pct = t.total > 0 ? Math.min(100, (t.received / t.total) * 100) : 0
      html += '<tr>' +
        '<td class="mono">' + esc(t.name || t.hash.slice(0, 12)) + '</td>' +
        '<td class="faint mono" style="font-size:11px">' + esc(t.from) + '</td>' +
        '<td class="num">' + (t.total > 0 ? fmtBytes(t.received) + ' / ' + fmtBytes(t.total) : fmtBytes(t.received)) +
        '<div class="bar"><i style="width:' + pct.toFixed(1) + '%"></i></div></td>' +
        '<td class="num faint">' + (t.state === 'running' ? esc(fmtRate(t.rate)) : '—') + '</td>' +
        '<td class="' + (t.state === 'done' ? 'ok' : t.state === 'error' || t.state === 'cancelled' ? 'err' : 'dim') + '">' +
        esc(t.state === 'done' ? '完成' : t.state === 'running' ? '传输中' : t.state === 'cancelled' ? '已取消' : t.state === 'error' ? '失败' : t.state) +
        (t.error ? '<div class="faint" style="font-size:11px">' + esc(t.error) + '</div>' : '') + '</td>' +
        '<td class="num">' + (t.state === 'running'
          ? '<button class="tiny ghost" data-cancel="' + t.id + '">取消</button>'
          : (t.state === 'done' ? '<span class="faint" style="font-size:11px">sha256 已校验</span>' : '')) + '</td>' +
        '</tr>'
    })
    box.innerHTML = html + '</tbody></table>'
    Array.prototype.forEach.call(box.querySelectorAll('[data-cancel]'), function (btn) {
      btn.addEventListener('click', function () {
        var t = tasks.find(function (x) { return x.id === btn.getAttribute('data-cancel') })
        if (t && t.ctrl) t.ctrl.abort()
      })
    })
  }


  function updateShares() {
    var s = sessions.get(current)
    var box = $('shares')
    $('preview').innerHTML = ''
    if (!s) { box.innerHTML = '<div class="empty">先在左侧选一个节点。</div>'; return }
    if (s.status === 'connecting') { box.innerHTML = '<div class="empty">连接中…</div>'; return }
    if (s.status === 'error') { box.innerHTML = '<div class="empty err">连接失败：' + esc(s.error) + '</div>'; return }
    if (!s.snapshot) { box.innerHTML = '<div class="empty">还没有清单。</div>'; return }

    var snap = s.snapshot
    if (!snap.total) {
      box.innerHTML = '<div class="empty">该节点没有共享内容（未开启对外共享，或未声明共享目录）。</div>'
      return
    }

    var html = ''
    if (snap.files && snap.files.length) {
      html += '<h2 style="margin-top:2px">单独文件</h2><table><thead><tr><th>名称</th><th class="num">大小</th><th>hash</th><th style="width:150px"></th></tr></thead><tbody>'
      snap.files.forEach(function (f, i) {
        html += '<tr>' +
          '<td>' + esc(f.path || f.name) + '</td>' +
          '<td class="num faint">' + (f.size ? esc(fmtBytes(f.size)) : '—') + '</td>' +
          '<td class="mono faint" style="font-size:11px">' + esc(String(f.hash).slice(0, 12)) + '…</td>' +
          '<td class="num">' +
          '<button class="small" data-save="' + i + '">保存</button> ' +
          '<button class="small ghost" data-peek="' + i + '">预览</button>' +
          '</td></tr>'
      })
      html += '</tbody></table>'
    }
    if (snap.collections && snap.collections.length) {
      html += '<h2 style="margin-top:16px">合集</h2>'
      snap.collections.forEach(function (c, ci) {
        var entries = Array.isArray(c.entries) ? c.entries : []
        html += '<details><summary>' + esc(c.name || String(c.hash).slice(0, 12) + '…') + ' · ' + entries.length + ' 个条目</summary>'
        html += '<table><thead><tr><th>路径</th><th class="num">大小</th><th style="width:150px"></th></tr></thead><tbody>'
        entries.forEach(function (e, ei) {
          html += '<tr>' +
            '<td>' + esc(e.path) + '</td>' +
            '<td class="num faint">' + (e.size ? esc(fmtBytes(e.size)) : '—') + '</td>' +
            '<td class="num">' +
            '<button class="small" data-csave="' + ci + ':' + ei + '">保存</button> ' +
            '<button class="small ghost" data-cpeek="' + ci + ':' + ei + '">预览</button>' +
            '</td></tr>'
        })
        html += '</tbody></table></details>'
      })
    }
    box.innerHTML = html

    var bind = function (attr, handler) {
      Array.prototype.forEach.call(box.querySelectorAll('[' + attr + ']'), function (el) {
        el.addEventListener('click', function () { handler(el.getAttribute(attr)) })
      })
    }
    var findFile = function (i) { return snap.files[Number(i)] }
    var findEntry = function (ref) {
      var p = ref.split(':')
      return (snap.collections[Number(p[0])].entries || [])[Number(p[1])]
    }
    bind('data-save', function (v) { download(s, findFile(v)) })
    bind('data-peek', function (v) { preview(s, findFile(v)) })
    bind('data-csave', function (v) { download(s, findEntry(v)) })
    bind('data-cpeek', function (v) { preview(s, findEntry(v)) })
  }

  // ── 动作 ──────────────────────────────────────────────────────────────
  function select(id) {
    current = id
    renderNodes()
    updateShares()
  }

  function peerOptions(sig) {
    // secure 必须显式传：自托管信令常用 ws://，peerjs 默认 secure=true 会去连 wss://，
    // 表现为「连不上但不报错」——最常见的踩坑点。
    return { host: sig.host, port: sig.port, path: sig.path, key: sig.key, secure: sig.secure, debug: 1 }
  }

  // peerjs 由加载器异步注入，可能还没到位；这里给出明确状态而不是「点了没反应」
  function peerReady() { return typeof window.Peer !== 'undefined' }
  function peerMissingHint() {
    return window.__peerjsOK === false
      ? 'peerjs 加载失败（同目录副本 / jsdelivr / unpkg 都没取到）。离线环境请先 `npm run vendor:peerjs` 把 peerjs.min.js 放到本页面同目录。'
      : 'peerjs 还在加载中，稍等再点一次。'
  }

  // 本页跑在 HTTPS（GitHub Pages 强制 HTTPS）时，浏览器会把 ws:// 当「混合内容」
  // 直接拦掉，PeerJS 侧只表现为连不上、没有任何可用提示——这里提前说清楚。
  function mixedContent() {
    return location.protocol === 'https:' && !$('in-secure').checked
  }
  function mixedContentHint() {
    return '本页是 HTTPS，浏览器会按「混合内容」拦掉 ws:// 信令（本机 localhost 的 ws:// ' +
      '多数浏览器仍放行，局域网 IP 一律拦）。请改用 wss：勾选上面的 wss（或链接里 secure=1），' +
      '自托管信令用 `peersignal -tls-cert cert.pem -tls-key key.pem`，或在信令前挂 TLS 反代。'
  }

  async function connect() {
    if (!peerReady()) { log(peerMissingHint(), 'err'); return }
    var nodeId = $('in-node').value.trim()
    if (!nodeId) { log('请先填节点 peer id', 'err'); return }
    if (sessions.has(nodeId)) { select(nodeId); return }
    if (mixedContent()) { log(mixedContentHint(), 'err'); return }

    var sig = sigOfForm()
    syncURL()
    var s = { id: nodeId, client: null, status: 'connecting', snapshot: null, error: null }
    sessions.set(nodeId, s)
    current = nodeId
    renderNodes()
    updateShares()
    try {
      log('连接信令 ' + sig.host + ':' + sig.port + ' 并拨号 ' + nodeId + ' …')
      var client = await window.PeerDrive.connectToPeer(window.Peer, nodeId, {
        peerOptions: peerOptions(sig),
        idleTimeoutMs: 60 * 1000,
      })
      s.client = client
      s.status = 'online'
      // 注意用 localPeerId（本端）而不是 conn.peer（那是**对端**，第一版就错在这）
      $('my-id').textContent = client.localPeerId || '（未知）'
      $('my-id').className = 'ok'
      log('已建立 WebRTC 直连', 'ok')
      remember(nodeId, sig)
      renderNodes()
      await loadShares(s)
    } catch (e) {
      s.status = 'error'
      s.error = e.message || String(e)
      log('连接失败：' + s.error + (e.code ? '（' + e.code + '）' : ''), 'err')
      if (e.code === window.PeerDrive.ERR.TIMEOUT) {
        log('提示：对方节点离线、peer id 写错，或信令配置不同（host/port/path/key/secure）', 'warn')
      }
      renderNodes()
      updateShares()
    }
  }

  function disconnect(id) {
    var s = sessions.get(id)
    if (!s) return
    if (s.client) { try { s.client.close() } catch (e) { /* 已关闭 */ } }
    sessions.delete(id)
    if (current === id) current = sessions.size ? sessions.keys().next().value : null
    log('已断开 ' + id)
    renderNodes()
    updateShares()
  }

  async function loadShares(s) {
    if (!s || !s.client) return
    try {
      s.snapshot = await s.client.shares()
      log('清单：' + (s.snapshot.collections || []).length + ' 合集 · ' + (s.snapshot.files || []).length + ' 文件', 'ok')
      renderNodes()
      if (current === s.id) updateShares()
    } catch (e) {
      s.error = e.message || String(e)
      log('获取清单失败：' + s.error + (e.code ? '（' + e.code + '）' : ''), 'err')
      renderNodes()
      if (current === s.id) updateShares()
    }
  }

  function saveBlob(blob, name) {
    var url = URL.createObjectURL(blob)
    var a = document.createElement('a')
    a.href = url
    a.download = name || 'download'
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    a.remove()
    // 立刻 revoke 会让部分浏览器（Safari）取消下载，给足余量
    setTimeout(function () { URL.revokeObjectURL(url) }, 30000)
  }

  function addTask(t) {
    tasks.unshift(t)
    renderTasks()
    return t
  }

  async function download(s, item) {
    if (!s || !s.client || !item || !item.hash) return
    var name = String(item.name || item.path || item.hash).split('/').pop()
    var started = Date.now()
    var ctrl = new AbortController()
    var t = addTask({
      id: 't' + Date.now() + Math.random().toString(36).slice(2, 6),
      hash: item.hash, name: name, from: s.id,
      received: 0, total: -1, rate: 0, state: 'running', ctrl: ctrl, error: null,
    })
    var tick = setInterval(function () {
      if (t.state !== 'running') return
      t.rate = Math.round(t.received / Math.max(1, (Date.now() - started) / 1000))
      renderTasks()
    }, 500)
    try {
      log('拉取 ' + name + ' …')
      var bytes = await s.client.fetch(item.hash, {
        signal: ctrl.signal,
        onProgress: function (received, total) {
          t.received = received
          t.total = total > 0 ? total : t.total
          renderTasks()
        },
      })
      t.received = bytes.byteLength
      t.state = 'done'
      saveBlob(new Blob([bytes], { type: (window.PeerDrive.guessMime || function () { return '' })(name) }), name)
      log('完成：' + name + '（' + fmtBytes(bytes.byteLength) + '，sha256 已校验）', 'ok')
    } catch (e) {
      t.state = e.name === 'AbortError' || (e.code === window.PeerDrive.ERR.CANCELLED) ? 'cancelled' : 'error'
      t.error = e.message || String(e)
      log('拉取失败：' + t.error + (e.code ? '（' + e.code + '）' : ''), t.state === 'cancelled' ? 'warn' : 'err')
      if (e.code === window.PeerDrive.ERR.HASH_MISMATCH) log('内容与 hash 不符——对端数据损坏或被篡改，已丢弃', 'err')
      if (e.code === window.PeerDrive.ERR.TOO_LARGE) log('超过内存闸（默认 256MB）', 'warn')
    } finally {
      clearInterval(tick)
      renderTasks()
    }
  }

  async function preview(s, item) {
    if (!s || !s.client || !item || !item.hash) return
    var box = $('preview')
    var name = String(item.name || item.path || item.hash).split('/').pop()
    var mime = (window.PeerDrive.guessMime || function () { return '' })(name)
    box.innerHTML = '<div class="preview faint">加载预览…</div>'
    try {
      if (/^image\//.test(mime)) {
        var blob = await s.client.fetchBlob(item.hash, { name: name })
        var url = URL.createObjectURL(blob)
        box.innerHTML = '<div class="preview"><img src="' + esc(url) + '" alt="' + esc(name) + '"></div>'
      } else if (/^video\//.test(mime) || /^audio\//.test(mime)) {
        var vblob = await s.client.fetchBlob(item.hash, { name: name })
        box.innerHTML = '<div class="preview"><video src="' + esc(URL.createObjectURL(vblob)) + '" controls></video></div>'
      } else {
        var text = await s.client.fetchText(item.hash, { maxBytes: PREVIEW_MAX_BYTES })
        box.innerHTML = '<div class="preview mono"><pre>' + esc(text.slice(0, 200000)) + '</pre></div>'
      }
      log('预览 ' + name, 'ok')
    } catch (e) {
      box.innerHTML = '<div class="preview err">预览失败：' + esc(e.message || String(e)) + '</div>'
      log('预览失败：' + (e.message || String(e)), 'err')
    }
  }

  // ── 启动 ──────────────────────────────────────────────────────────────
  function boot() {
    var q = params()
    if (q.get('host')) $('in-host').value = q.get('host')
    if (q.get('port')) $('in-port').value = q.get('port')
    if (q.get('path')) $('in-path').value = q.get('path')
    if (q.get('key')) $('in-key').value = q.get('key')
    if (q.get('secure') !== null) $('in-secure').checked = q.get('secure') !== '0'
    if (q.get('node')) $('in-node').value = q.get('node')

    $('btn-connect').addEventListener('click', connect)
    $('in-node').addEventListener('keydown', function (e) { if (e.key === 'Enter') connect() })
    $('in-secure').addEventListener('change', function () {
      syncURL()
      if (!mixedContent()) log('信令走 wss，与 HTTPS 页面兼容', 'ok')
    })
    ;['in-host', 'in-port', 'in-path', 'in-key'].forEach(function (id) {
      $(id).addEventListener('change', syncURL)
    })

    renderRecent()
    renderTasks()
    log('就绪。本面板是单个静态文件，不需要本地后端。')
    // HTTPS 托管（GitHub Pages）下 ws:// 必被拦，开局就讲明白，别等用户点了没反应
    if (mixedContent()) log(mixedContentHint(), 'warn')

    window.addEventListener('peerjs-ready', function (e) {
      if (e.detail && e.detail.ok) log('peerjs 已加载（' + e.detail.src + '）', 'ok')
      else log(peerMissingHint(), 'err')
    })

    if (q.get('auto') === '1' && $('in-node').value.trim()) {
      // auto=1 时页面可能还没拿到 peerjs，等它到位再连
      if (peerReady()) connect()
      else window.addEventListener('peerjs-ready', function once(e) {
        window.removeEventListener('peerjs-ready', once)
        if (e.detail && e.detail.ok) connect()
      })
    }
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot)
  else boot()
})()
