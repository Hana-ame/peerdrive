// app-transfers.js — 面板的 UI 列表渲染与传输任务处理（Issue #404 模块化拆分的第二个 part）。
// 由 scripts/build-panel.mjs 内联进 /*__PANEL_TRANSFERS_PART__*/ 占位处；与 app.js / app-shares.js 共享
// 同一个 IIFE 作用域（sessions/current/tasks/$/log 等都在那个作用域里）。
// 从 v1.0-rc 快照重建：快照把旧 app.js 拆成三个文件，但这两个 part 文件没进 zip。
// 内容逐字取自拆分前的 refactor@06d83eb app.js（渲染 + 读写动作段）。

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
        $('in-key').value = r.sig.key || DEFAULT_SIG.key
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
  var html = '<table><thead><tr><th>文件</th><th>类型</th><th>节点</th><th class="num">进度</th><th class="num">速率</th><th>状态</th><th></th></tr></thead><tbody>'
  tasks.forEach(function (t) {
    var pct = t.total > 0 ? Math.min(100, (t.received / t.total) * 100) : 0
    // 写方向完成后的 hash 就是它的内容地址——显示出来，用户才知道接下来能拿什么取回
    var addr = t.kind !== 'get' && t.hash
      ? '<div class="faint mono" style="font-size:11px" title="' + esc(t.hash) + '">' + esc(t.hash.slice(0, 16)) + '…</div>'
      : ''
    html += '<tr>' +
      '<td class="mono">' + esc(t.name || String(t.hash).slice(0, 12)) + addr + '</td>' +
      '<td class="faint">' + esc(KIND[t.kind] || t.kind || '') + '</td>' +
      '<td class="faint mono" style="font-size:11px">' + esc(t.node || t.from) + '</td>' +
      '<td class="num">' + (t.total > 0 ? fmtBytes(t.received) + ' / ' + fmtBytes(t.total) : fmtBytes(t.received)) +
      '<div class="bar"><i style="width:' + pct.toFixed(1) + '%"></i></div></td>' +
      '<td class="num faint">' + (t.state === 'running' ? esc(fmtRate(t.rate)) : '—') + '</td>' +
      '<td class="' + (t.state === 'done' ? 'ok' : t.state === 'error' || t.state === 'cancelled' ? 'err' : 'dim') + '">' +
      esc(t.state === 'done' ? '完成' : t.state === 'running' ? '进行中' : t.state === 'cancelled' ? '已取消' : t.state === 'error' ? '失败' : t.state) +
      (t.error ? '<div class="faint" style="font-size:11px">' + esc(t.error) + '</div>' : '') + '</td>' +
      '<td class="num">' + (t.state === 'running'
        ? (t.ctrl ? '<button class="tiny ghost" data-cancel="' + t.id + '">取消</button>' : '')
        : (t.state === 'done'
          ? (t.kind !== 'get' && t.hash
            ? '<button class="tiny ghost" data-verify="' + t.id + '">取回校验</button>'
            : '<span class="faint" style="font-size:11px">sha256 已校验</span>')
          : '')) + '</td>' +
      '</tr>'
  })
  box.innerHTML = html + '</tbody></table>'
  Array.prototype.forEach.call(box.querySelectorAll('[data-cancel]'), function (btn) {
    btn.addEventListener('click', function () {
      var t = tasks.find(function (x) { return x.id === btn.getAttribute('data-cancel') })
      if (t && t.ctrl) t.ctrl.abort()
    })
  })
  Array.prototype.forEach.call(box.querySelectorAll('[data-verify]'), function (btn) {
    btn.addEventListener('click', function () {
      var t = tasks.find(function (x) { return x.id === btn.getAttribute('data-verify') })
      if (t) verifyRoundTrip(t)
    })
  })
}

/**
 * renderDiscovered 渲染「自动搜索」的结果。
 *
 * 失败也要渲染：搜索失败的三种原因（信令没开 CORS / 端点不可达 / 真的没节点）
 * 在界面上长得一模一样——一个空列表。把它们拆成三句人话，用户才不用猜。
 */
function renderDiscovered(nodes, err) {
  var box = $('discovered')
  if (err) {
    var msg = err.message || String(err)
    var cors = /Access-Control-Allow-Origin/.test(msg)
    var hint = cors
      ? '这一步是<b>跨域请求</b>（本页原点是 <span class="mono">' + esc(String(location.origin)) + '</span>），' +
        '信令必须回 <span class="mono">Access-Control-Allow-Origin</span>，浏览器才会把响应交出来。' +
        '自托管信令请升到 2026-09-20 之后的 go-peerserver（已放开 CORS），或在反代层补这个头。' +
        '<b>在此之前自动发现用不了，但手动连节点完全不受影响</b>——只是在下面的「节点 peer id」里手填。'
      : '信令 REST 端点没答上来。请确认 host/port 与节点配的信令一致；' +
        '另外本页是 HTTPS 而信令是 <span class="mono">http://</span> 时，浏览器会按「混合内容」拦掉这次请求。'
    box.innerHTML = '<div class="empty err">自动搜索失败：' + esc(msg) + '</div><div class="hint">' + hint + '</div>'
    return
  }
  if (!nodes || !nodes.length) {
    box.innerHTML = '<div class="empty">信令上没有在线节点。</div>' +
      '<div class="hint">空的列表不代表信令坏了：节点要主动向信令 announce 才会出现在这里。' +
      '确认节点配了 <span class="mono">PEERDRIVE_DISCOVERY_*</span>，且它确实连着这个信令。</div>'
    return
  }
  box.innerHTML = ''
  nodes.forEach(function (n) {
    var row = document.createElement('div')
    row.className = 'node'
    var ago = n.lastSeen ? Math.max(0, Math.round(Date.now() / 1000 - Number(n.lastSeen))) : null
    row.innerHTML =
      '<span class="dot on"></span>' +
      '<span class="nm mono" title="' + esc(n.peerId) + '">' + esc(n.peerId) + '</span>' +
      '<span class="faint" style="font-size:11px">' +
      (n.nodeType ? esc(n.nodeType) + ' · ' : '') +
      (n.collections && n.collections.length ? n.collections.length + ' 集合 · ' : '') +
      (ago == null ? '' : ago + 's 前') +
      '</span>'
    row.title = '点击连接该节点'
    row.addEventListener('click', function () {
      $('in-node').value = n.peerId
      syncURL()
      connect()
    })
    box.appendChild(row)
  })
}

function updateShares() {
  var s = sessions.get(current)
  var box = $('shares')
  $('preview').innerHTML = ''
  renderLinked(s)
  if (!s) { box.innerHTML = '<div class="empty">先在左侧选一个节点。</div>'; return }
  if (s.status === 'connecting') { box.innerHTML = '<div class="empty">连接中…</div>'; return }
  if (s.status === 'error') { box.innerHTML = '<div class="empty err">连接失败：' + esc(s.error) + '</div>'; return }
  if (!s.snapshot) { box.innerHTML = '<div class="empty">还没有清单。</div>'; return }

  var snap = s.snapshot
  if (!snap.total) {
    // 空清单有两种成因，现象一模一样，修复动作却相反，所以必须在这里点名：
    //   1) 节点开了 PSK 门禁而本页面没填密钥 —— share 帧被 pskGate 挡住，返回空清单；
    //   2) 节点真的没开对外共享 / 没声明共享目录。
    // 原来只写「未开启对外共享」，会让第 1 种情况的用户去改节点配置（改不对），
    // 而正确动作只是在上方「预共享密钥」框里填一次。
    // 为什么门禁不会回 PSK_REQUIRED：psk.go 的 servedVerbs 挡的是「对端让我做事」的动词，
    // share 的**响应帧**不在名单里，所以这里只能表现为「拿不到内容」而不是「报错」。
    var pskFilled = !!(pskOfForm() && pskOfForm().trim())
    box.innerHTML = '<div class="empty">该节点没有共享内容。'
      + (pskFilled
          ? '<br><br>你填了密钥但仍拿不到清单：确认密钥与运营者给的一致，'
            + '且该节点确实声明了共享目录。'
          : '<br><br>两种可能：① 节点开了预共享密钥门禁——在「预共享密钥」框里填同一个密钥再连一次；'
            + '② 节点确实没有对外共享内容。')
      + '</div>'
    return
  }

  var html = ''
  if (snap.files && snap.files.length) {
    html += '<h2 style="margin-top:2px">单独文件</h2><table><thead><tr><th>名称</th><th class="num">大小</th><th>hash</th><th style="width:210px"></th></tr></thead><tbody>'
    snap.files.forEach(function (f, i) {
      html += '<tr>' +
        '<td>' + esc(f.path || f.name) + '</td>' +
        '<td class="num faint">' + (f.size ? esc(fmtBytes(f.size)) : '—') + '</td>' +
        '<td class="mono faint" style="font-size:11px">' + esc(String(f.hash).slice(0, 12)) + '…</td>' +
        '<td class="num">' +
        '<button class="small" data-save="' + i + '">保存</button> ' +
        '<button class="small ghost" data-peek="' + i + '">预览</button> ' +
        '<button class="small ghost" data-link="' + i + '" title="生成一条分享链接：不在清单里也能凭 hash 取回（unlisted）">链接</button>' +
        '</td></tr>'
    })
    html += '</tbody></table>'
  }
  if (snap.collections && snap.collections.length) {
    html += '<h2 style="margin-top:16px">合集</h2>'
    snap.collections.forEach(function (c, ci) {
      var entries = Array.isArray(c.entries) ? c.entries : []
      // 整包一条链接：合集 manifest 本身能凭 hash 取回，所以对方不在清单里也能拿到
      html += '<details><summary>' + esc(c.name || String(c.hash).slice(0, 12) + '…') + ' · ' + entries.length + ' 个条目' +
        ' <button class="small ghost" data-coll-link="' + ci + '" title="生成这个合集的分享链接（整包一条）">链接</button></summary>'
      html += '<table><thead><tr><th>路径</th><th class="num">大小</th><th style="width:210px"></th></tr></thead><tbody>'
      entries.forEach(function (e, ei) {
        html += '<tr>' +
          '<td>' + esc(e.path) + '</td>' +
          '<td class="num faint">' + (e.size ? esc(fmtBytes(e.size)) : '—') + '</td>' +
          '<td class="num">' +
          '<button class="small" data-csave="' + ci + ':' + ei + '">保存</button> ' +
          '<button class="small ghost" data-cpeek="' + ci + ':' + ei + '">预览</button> ' +
          '<button class="small ghost" data-clink="' + ci + ':' + ei + '" title="生成一条分享链接：不在清单里也能凭 hash 取回（unlisted）">链接</button>' +
          '</td></tr>'
      })
      html += '</tbody></table></details>'
    })
  }
  box.innerHTML = html

  // stop=true 用于"按钮嵌在 <summary> 里"的情形：不拦住的话点按钮会顺手把
  // details 展开/收起（合集标题行就是这种）。
  var bind = function (attr, handler, stop) {
    Array.prototype.forEach.call(box.querySelectorAll('[' + attr + ']'), function (el) {
      el.addEventListener('click', function (e) {
        if (stop) { e.preventDefault(); e.stopPropagation() }
        handler(el.getAttribute(attr))
      })
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
  bind('data-link', function (v) {
    var f = findFile(v)
    copyShareLink(s.id, f.hash, String(f.path || f.name || ''))
  })
  bind('data-clink', function (v) {
    var e = findEntry(v)
    copyShareLink(s.id, e.hash, String(e.path || ''))
  })
  bind('data-coll-link', function (v) {
    var c = snap.collections[Number(v)]
    copyShareLink(s.id, c.hash, String(c.name || c.hash))
  }, true)
}

function select(id) {
  current = id
  renderNodes()
  updateShares()
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
    hash: item.hash, name: name, from: s.id, node: s.id, kind: 'get',
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

async function discoverNow() {
  var sig = sigOfForm()
  var btn = $('btn-discover')
  btn.disabled = true
  $('discovered').innerHTML = '<div class="empty">正在向 ' + esc(sig.host + ':' + sig.port) + ' 询问…</div>'
  log('向信令询问在线节点：' + sig.host + ':' + sig.port)
  try {
    var nodes = await window.PeerDrive.discoverNodes(sig, { timeoutMs: 8000 })
    renderDiscovered(nodes, null)
    if (nodes.length) {
      log('在线节点 ' + nodes.length + ' 个：' + nodes.map(function (n) { return n.peerId }).join('、'), 'ok')
    } else {
      log('信令上没有在线节点（空列表≠信令坏了：节点要主动 announce 才会出现）', 'warn')
    }
  } catch (e) {
    renderDiscovered([], e)
    log('自动搜索失败：' + (e.message || e) + (e.code ? '（' + e.code + '）' : ''), 'err')
  } finally {
    btn.disabled = false
  }
}

function activeNode() {
  var s = sessions.get(current)
  if (!s) { log('先在左栏选中一个节点', 'err'); return null }
  if (s.status !== 'online' || !s.client) {
    log('节点 ' + s.id + ' 还不是在线状态（' + s.status + '）', 'err')
    return null
  }
  return s
}

function newTask(s, name, kind, total) {
  return addTask({
    id: 't' + Date.now() + Math.random().toString(36).slice(2, 6),
    hash: '', name: name, from: s.id, node: s.id, kind: kind,
    received: 0, total: total || 0, rate: 0, state: 'running', ctrl: new AbortController(), error: null,
  })
}

function tickRate(t, started) {
  var h = setInterval(function () {
    if (t.state !== 'running') return
    t.rate = Math.round(t.received / Math.max(1, (Date.now() - started) / 1000))
    renderTasks()
  }, 500)
  return function () { clearInterval(h) }
}

function settleWrite(t, e, what) {
  t.state = (e && (e.name === 'AbortError' || e.code === window.PeerDrive.ERR.CANCELLED)) ? 'cancelled' : 'error'
  t.error = (e && e.message) || String(e)
  log(what + '失败：' + t.error + (e && e.code ? '（' + e.code + '）' : ''), t.state === 'cancelled' ? 'warn' : 'err')
  if (!e || !e.code) return
  if (pskHint(e)) log(pskHint(e), 'warn')
  if (e.code === window.PeerDrive.ERR.TIMEOUT) {
    log('节点在分片超时窗口内没回应。常见原因：它此刻正忙（磁盘/别的在传），或这条连接已经半死——重连一次最快。', 'warn')
  }
}

async function putFiles() {
  var s = activeNode()
  if (!s) return
  var files = Array.prototype.slice.call($('in-file').files || [])
  if (!files.length) { log('先选至少一个本地文件', 'err'); return }
  // 串行执行：一条 DataChannel 上同时只能有一个 upload 流（服务端按连接级槽位
  // 管理），并行会在第二份的第一帧就收到 "already in progress"。
  for (var i = 0; i < files.length; i++) await putOne(s, files[i])
  await loadShares(s)
}

async function putOne(s, file) {
  var name = file.name || ('upload-' + Date.now())
  var size = file.size || 0
  var started = Date.now()
  var t = newTask(s, name, 'put', size)
  var stop = tickRate(t, started)
  try {
    if (size > PUT_WARN_BYTES) {
      log(name + ' 有 ' + fmtBytes(size) + '：put 会先把整个内容读进浏览器内存，超大文件建议先本地切片', 'warn')
    }
    log('入库 ' + name + '（' + fmtBytes(size) + '）…')
    var res = await s.client.put(file, {
      name: name,
      signal: t.ctrl.signal,
      chunkTimeoutMs: PUT_CHUNK_TIMEOUT_MS,
      onProgress: function (sent, total) {
        t.received = sent
        if (total > 0) t.total = total
        renderTasks()
      },
    })
    t.hash = res.hash
    t.received = res.size
    t.total = res.size
    t.state = 'done'
    log('入库完成：' + res.name + ' → sha256 ' + res.hash + '（' + fmtBytes(res.size) + '）', 'ok')
    log('这条内容之后在任何地方都能用这个 hash 取回；要立刻验证，点任务里的「取回校验」。', 'ok')
  } catch (e) {
    settleWrite(t, e, '入库 ' + name)
  } finally {
    stop()
    renderTasks()
  }
}

async function pullUrl() {
  var s = activeNode()
  if (!s) return
  var url = ($('in-url').value || '').trim()
  if (!url) { log('先填一个 URL', 'err'); return }
  if (!/^https?:\/\//i.test(url)) {
    log('URL 必须是 http:// 或 https:// 开头——节点侧的 SSRF 防护也只放行这两种协议', 'err')
    return
  }
  var want = ($('in-url-name').value || '').trim()
  var started = Date.now()
  var t = newTask(s, want || url.split('/').pop() || url, 'pull', 0)
  var stop = tickRate(t, started)
  try {
    log('让节点抓取 ' + url + ' …')
    var res = await s.client.pull(url, { name: want })
    t.hash = res.hash
    t.received = res.size
    t.total = res.size
    t.state = 'done'
    log('网络入库完成：' + res.name + ' → sha256 ' + res.hash + '（' + fmtBytes(res.size) + '）', 'ok')
  } catch (e) {
    settleWrite(t, e, '抓取 ' + url)
    // pull 的绝大多数 err 是节点主动拒绝（SSRF/超限/404）——那是节点在保护自己，
    // 不是故障。把这句话说清楚，省得用户反复重试同一个注定失败的地址。
    if (e && e.code === window.PeerDrive.ERR.PEER) {
      log('这是节点的策略性拒绝（不是网络故障）：换一个公网可访问的地址再来。', 'warn')
    }
  } finally {
    stop()
    renderTasks()
  }
}

async function verifyRoundTrip(t) {
  var s = sessions.get(t.node)
  if (!s || !s.client) { log('该节点已断开，无法取回校验', 'err'); return }
  try {
    log('取回校验 ' + t.hash.slice(0, 12) + '…')
    var bytes = await s.client.fetch(t.hash)
    var hex = await window.PeerDrive.sha256Hex(bytes)
    hex === t.hash
      ? log('取回校验通过：拉回 ' + fmtBytes(bytes.byteLength) + '，sha256 与入库返回值一致', 'ok')
      : log('取回校验失败：期望 ' + t.hash + '，实得 ' + hex, 'err')
  } catch (e) {
    log('取回校验失败：' + (e.message || e) + (e.code ? '（' + e.code + '）' : ''), 'err')
    if (pskHint(e)) log(pskHint(e), 'warn')
  }
}
