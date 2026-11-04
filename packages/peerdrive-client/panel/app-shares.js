// app-shares.js — 分享链接（deep-link）控制器：链接带来的那条内容（file / collection）的
// 识别与取回 UI（Issue #404 模块化拆分的第三个 part）。
// 由 scripts/build-panel.mjs 内联进 /*__PANEL_SHARES_PART__*/ 占位处。
// 从 v1.0-rc 快照重建：内容逐字取自拆分前的 refactor@06d83eb app.js。

//
// unlisted 的定义就是**不出现在清单里**、但凭 hash 可取。所以拿到链接的人连上
// 节点后，清单里多半没有它——不给单独入口的话，他看到"该节点没有共享内容"
// 就走了，而东西其实一直拿得到。这条也正是"链接分享"这个语义的落点。
function renderLinked(s) {
  var box = $('linked')
  if (!box) return
  if (!pendingHash) { box.innerHTML = ''; return }
  var h = pendingHash
  var short = h.slice(0, 16) + '…'
  if (!s || s.status !== 'online') {
    box.innerHTML = '<div class="empty">链接里带着一个 hash（<span class="mono">' +
      esc(short) + '</span>），连上节点后即可取回。</div>'
    return
  }
  // 清单还没到就先别判断：此刻既不知道它是文件还是合集，判断了也不会重来
  // （linkedFor 已记过账），会把"合集链接"误判成文件。
  if (!s.snapshot) {
    box.innerHTML = '<div class="empty">正在获取对方的共享清单…</div>'
    return
  }
  var key = s.id + ':' + h
  if (linkedFor !== key) {
    if (!linkedBusy) { linkedBusy = true; resolveLinked(s, key) }
    // 占位必须**在它跑完之后再判一次**：命中清单那条路是同步走完的（里面已经
    // 自己重画过一次），无条件写占位会把刚画好的表格覆盖成"正在识别…"。
    if (linkedFor !== key) {
      box.innerHTML = '<div class="empty">正在识别链接里的内容…</div>'
      return
    }
  }
  if (linkedKind === 'collection' && linkedColl) { renderLinkedColl(s, h); return }
  var named = nameForHash(s, h)
  box.innerHTML =
    '<div class="hint">这条内容来自分享链接：<span class="mono">' + esc(short) + '</span>' +
    (named ? '（清单里叫 ' + esc(named) + '）' : '（清单里没有它 —— unlisted 就是这个意思：不列出，但知道 hash 就能取）') +
    '</div>' +
    '<div class="row">' +
    '<button class="small" id="btn-linked-get">取回</button> ' +
    '<button class="small ghost" id="btn-linked-copy">复制链接</button>' +
    '</div>'
  $('btn-linked-get').addEventListener('click', function () {
    download(s, { hash: h, name: named || h })
  })
  $('btn-linked-copy').addEventListener('click', function () {
    copyShareLink(s.id, h, named || '')
  })
}

// resolveLinked 判断链接里那条是文件还是合集（结果写进 linkedKind/linkedColl）。
//
// 为什么"取回来看"是可行且必要的：合集 manifest 就是一份按内容寻址存的 JSON，
// 而 unlisted 合集按定义不在清单里——不取就永远只能说"清单里没有它"，拿到链接
// 的人依然什么都看不到。取回失败（private / 断线）一律按文件处理：那样「取回」
// 会把服务端的真实原因显示出来，比这里猜一个原因强。
async function resolveLinked(s, key) {
  try {
    var snap = s && s.snapshot
    var cs = (snap && snap.collections) || []
    for (var i = 0; i < cs.length; i++) {
      if (cs[i].hash !== pendingHash) continue
      linkedKind = 'collection'
      linkedColl = {
        name: cs[i].name || cs[i].friendly_name || '',
        entries: (cs[i].entries || []).map(manifestEntry),
        fromList: true,
      }
      linkedFor = key
      return
    }
    var inFiles = ((snap && snap.files) || []).filter(function (f) { return f.hash === pendingHash })
    if (inFiles.length) {
      linkedKind = 'file'
      linkedColl = null
      linkedFor = key
      return
    }
    var bytes = await s.client.fetch(pendingHash, { maxBytes: LINK_SNIFF_BYTES })
    var obj = null
    try { obj = JSON.parse(new TextDecoder().decode(bytes)) } catch (e) { obj = null }
    if (obj && Array.isArray(obj.entries)) {
      linkedKind = 'collection'
      linkedColl = {
        name: obj.friendly_name || '',
        entries: obj.entries.map(manifestEntry).filter(function (e) { return e.hash }),
        fromList: false,
      }
    } else {
      linkedKind = 'file'
      linkedColl = null
    }
    linkedFor = key
  } catch (e) {
    linkedKind = 'file'
    linkedColl = null
    // 也记上账：否则清单每刷新一次就重取一次，失败时会一直转圈
    linkedFor = key
  } finally {
    linkedBusy = false
    renderLinked(s)
  }
}

// manifestEntry 把 manifest 的条目统一成 {path, hash}：新格式是 providers[]，
// 老格式直接给 hash（panel 侧两种都要认，链接可能是老节点生成的）。
function manifestEntry(e) {
  var h = ''
  if (e) h = e.hash || ((e.providers || [])[0] || {}).value || ''
  return { path: (e && e.path) || '', hash: String(h || '') }
}

// renderLinkedColl 把链接带来的合集单独列在清单上方（条目可逐条保存/预览/再分享）。
function renderLinkedColl(s, h) {
  var box = $('linked')
  var entries = linkedColl.entries || []
  var title = linkedColl.name || (h.slice(0, 12) + '…')
  var html = '<div class="hint">这条链接指向一个<b>合集</b>：' + esc(title) +
    '（' + entries.length + ' 个条目）'
  if (!linkedColl.fromList) {
    html += ' —— 它不在对方的共享清单里，是凭 hash 取到的（unlisted 就是这个意思：不列出，但知道 hash 就能取）'
  }
  html += '</div>'
  if (!entries.length) {
    html += '<div class="empty">这个合集没有条目。</div>'
  } else {
    html += '<table><thead><tr><th>路径</th><th style="width:210px"></th></tr></thead><tbody>'
    entries.forEach(function (e, i) {
      html += '<tr><td>' + esc(e.path || e.hash) + '</td>' +
        '<td class="num">' +
        '<button class="small" data-lsave="' + i + '">保存</button> ' +
        '<button class="small ghost" data-lpeek="' + i + '">预览</button> ' +
        '<button class="small ghost" data-llink="' + i + '">链接</button>' +
        '</td></tr>'
    })
    html += '</tbody></table>'
  }
  html += '<div class="row">' +
    '<button class="small ghost" id="btn-linked-copy">复制链接</button>' +
    '</div>' +
    '<div class="hint faint">面板只能逐条存到浏览器；要整包存进自己的节点，用管理台的「保存整个合集」（教程第五章）。</div>'
  box.innerHTML = html

  var at = function (i) { return (linkedColl.entries || [])[i] }
  var bindL = function (attr, fn) {
    Array.prototype.forEach.call(box.querySelectorAll('[' + attr + ']'), function (el) {
      el.addEventListener('click', function () { fn(Number(el.getAttribute(attr))) })
    })
  }
  bindL('data-lsave', function (i) { download(s, at(i)) })
  bindL('data-lpeek', function (i) { preview(s, at(i)) })
  bindL('data-llink', function (i) {
    var e = at(i)
    copyShareLink(s.id, e.hash, String(e.path || ''))
  })
  $('btn-linked-copy').addEventListener('click', function () { copyShareLink(s.id, h, title) })
}

// nameForHash 在已拿到的清单里找 hash 对应的名字（只为显示，找不到不算错）。
function nameForHash(s, h) {
  var snap = s && s.snapshot
  if (!snap) return ''
  var f = (snap.files || []).filter(function (x) { return x.hash === h })[0]
  if (f) return String(f.path || f.name || '')
  var cs = snap.collections || []
  for (var i = 0; i < cs.length; i++) {
    var e = (cs[i].entries || []).filter(function (x) { return x.hash === h })[0]
    if (e) return String(e.path || '')
  }
  return ''
}
