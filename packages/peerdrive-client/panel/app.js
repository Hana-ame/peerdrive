// app.js — 公共面板的 UI 逻辑（普通脚本，不是 module：产物要在 file:// 下能跑）。
// panel/app.js — Core identity, signaling dial, share manifest & deep-link controller (<600 lines, Issue #404).
// UI list rendering and transfer task handlers are modularized in panel/app-transfers.js.
;(function () {
  'use strict'

  var LS_KEY = 'peerdrive.panel.v1'
  // 项目公共信令：节点与面板的默认值必须一致，否则互相找不到
  // （两个信令各是一张节点表，discover 也搜不到对方）。
  var DEFAULT_SIG = {
    host: 'peersignal.moonchan.xyz',
    port: 443,
    path: '/',
    key: 'pd-signal-1edf5e05e4a52b7351392574',
    secure: true,
  }
  var MAX_RECENT = 8
  var PREVIEW_MAX_BYTES = 2 * 1024 * 1024 // 预览只给小文件，大的直接存盘
  // put() 会把整个内容读进浏览器内存再按 64KB 分片推出去——超过这个量要先提醒，
  // 否则用户会在"大文件传到一半标签页崩了"之后才知道代价。
  var PUT_WARN_BYTES = 256 * 1024 * 1024
  // 入库一轮的分片绝大部分是毫秒级，但接收方要落盘；给足余量，别把慢当成死
  var PUT_CHUNK_TIMEOUT_MS = 30 * 1000

  // 任务类型。写方向（put/pull）和读方向（get）混在一张表里，必须能一眼分开——
  // 它们的失败含义完全不同：下载失败是"拿不到"，入库失败是"写不进去"。
  var KIND = { get: '下载', put: '本地入库', pull: '网络入库' }

  var sessions = new Map() // nodeId -> {id, client, status, snapshot, error}

  // 本面板的**稳定身份**。
  //
  // 为什么必须自己固定一个：节点侧的 private 级别按「对端 peer id 在不在好友
  // 名单里」判，而 peerjs 默认每次 `new Peer` 都随机生成一个 id —— 运营者在名单
  // 里填一个下次打开就变的 id 等于没填，private 会退化成"谁都拿不到，包括你
  // 答应给的那个人"。所以这里生成一次、存进 localStorage：一个浏览器 = 一个
  // 固定的来访者，名字才填得进去。
  var myId = lsRead().myId || ''
  // 这里只能用 persistMyId（不渲染）：`var $ = …` 还排在后面没赋值，此刻碰 DOM
  // 会抛 "TypeError: $ is not a function" 并中断整个 IIFE —— 面板整页失效，
  // 而错误信息跟"没有固定 id"完全看不出关系。
  if (!myId) { myId = newPanelId(); persistMyId(myId) }

  // 端到端自检用的出口：让脚本能复用面板**已经建立**的那条连接，而不是再拨一次。
  // 为什么要复用：第二次握手在部分环境（CI 的同机 loopback）并不总是成功，而
  // 自检要验的是「sha256 是否与清单一致」，不是「能不能连第二次」。
  if (typeof window !== 'undefined') {
    window.__panel = {
      sessions: sessions,
      current: function () { return current },
      get: function (id) { return sessions.get(id) },
      // 端到端自检用：让脚本能驱动这些动作，而不必依赖 DOM 点击的细节。
      connect: connect,
      discoverNow: discoverNow,
      putFiles: putFiles,
      pullUrl: pullUrl,
      // 稳定身份与分享链接（自检要能直接读到，而不是靠解析 DOM）
      myId: function () { return myId },
      shareLink: shareLink,
      // 链接带来的那条内容识别结果（自检用：'file' / 'collection'）
      linkedKind: function () { return linkedKind },
    }
  }
  var current = null // 当前选中的 nodeId
  var tasks = [] // 传输任务（最新的在前）
  // pendingHash 由分享链接（?hash=<64hex>）带来。它特意**不**依赖清单：unlisted
  // 的内容按定义就不在清单里（见 renderLinked）。
  var pendingHash = ''
  // 链接带来的那条到底是**文件**还是**合集**。
  // 合集 manifest（entries 清单）本身就是按内容寻址存的一份 JSON，hash 就是它的
  // sha256，所以合集 hash 同样能被 req 取回——取回来的就是条目清单。识别方式：
  // 清单里命中就直接用，清单里没有（unlisted）就取回来看看是不是 manifest。
  var linkedKind = '' // 'file' | 'collection'
  var linkedColl = null // {name, entries:[{path,hash}], fromList}
  var linkedFor = '' // 已识别过的 "<nodeId>:<hash>"，换节点或换链接才重来
  var linkedBusy = false // 识别中（防止清单每刷新一次就重复取回）
  var LINK_SNIFF_BYTES = 2 * 1024 * 1024 // 嗅探上限：manifest 通常几 KB

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

  // 面板 id 用 pd-panel- 前缀：运营者在节点日志/好友名单里能一眼认出"这是个面板
  // 而不是另一个节点"。字符集受 peerjs 的 id 校验约束（字母数字，分隔符只认 - 和 _）。
  function newPanelId() {
    return 'pd-panel-' + Math.random().toString(36).slice(2, 10)
  }

  // persistMyId 只落盘；saveMyId = 落盘 + 重画界面。分开是因为启动早期（$ 还没
  // 赋值）只能做前者，见顶部 myId 初始化处的注释。
  function persistMyId(id) {
    myId = id
    var st = lsRead()
    st.myId = id
    lsWrite(st)
  }

  function saveMyId(id) {
    persistMyId(id)
    renderMyId()
  }

  function renderMyId() {
    var el = $('my-id')
    if (!el) return
    el.textContent = myId
    el.className = 'ok'
  }

  // 分享链接 —— unlisted 的落地方式。
  //
  // unlisted 的语义是"不出现在共享清单里，但知道 hash 就能取回"。这句话要能被
  // 用起来，就得有个"把 hash 交给别人"的动作，否则用户只能口头传一串 64 位
  // 十六进制。链接里带 node + hash（外加当前信令参数，面板状态本来就在 URL 里），
  // 对方打开即连（auto=1）、连上就能取。
  //
  // **绝不带 psk**：密钥不能被历史记录/分享目标带走（见 pskOfForm 注释）。
  // 需要门禁的节点，收到链接的人得自己向运营者要密钥。
  function shareLink(nodeId, hash) {
    var q = params()
    q.set('node', nodeId)
    q.set('hash', hash)
    q.set('auto', '1')
    q.delete('psk')
    q.delete('token') // 信令凭据同样不带（见 tokenOfForm 注释）
    // file:// 下 location.origin 是字符串 "null"，拼出来不能用，直接用 href 去参数
    var base = String(location.href).split('#')[0].split('?')[0]
    return base + '?' + q.toString()
  }

  // 复制链接。file:// 不是 secure context，navigator.clipboard 常常不存在或被拒，
  // 所以留两级退路：剪贴板 API → execCommand → 把链接打进日志让用户自己复制。
  // 不做退路的话，"点复制没反应"在 file:// 下是常态而不是意外。
  async function copyText(text) {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text)
        return true
      }
    } catch (e) { /* 继续降级 */ }
    try {
      var ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.top = '-1000px'
      document.body.appendChild(ta)
      ta.select()
      var done = document.execCommand('copy')
      ta.remove()
      if (done) return true
    } catch (e) { /* 继续降级 */ }
    return false
  }

  async function copyShareLink(nodeId, hash, label) {
    var link = shareLink(nodeId, hash)
    if (await copyText(link)) {
      log('已复制' + (label ? '「' + label + '」的' : '') + '分享链接（不含密钥）', 'ok')
    } else {
      log('复制失败（file:// 下浏览器常不让访问剪贴板），链接如下，手动复制：', 'warn')
      log(link, 'warn')
    }
  }

  function sigOfForm() {
    return {
      host: $('in-host').value.trim() || DEFAULT_SIG.host,
      port: Number($('in-port').value.trim() || DEFAULT_SIG.port),
      path: $('in-path').value.trim() || DEFAULT_SIG.path,
      key: $('in-key').value.trim() || DEFAULT_SIG.key,
      secure: $('in-secure').checked,
    }
  }

  // 预共享密钥：**不**进地址栏、**不**进 localStorage。
  // 理由：地址栏会被历史记录/截图/分享带走，localStorage 是明文且跨会话常驻；
  // 而密钥一旦泄露，等于这个节点对所有拿到它的人开门。所以它只活在当前页面的
  // 输入框里，刷新即丢（真要长期保存请用密码管理器）。
  // 反向是允许的：链接里带 psk= 可以预填（方便一次性分享），但读完立刻从
  // URL 里抹掉，不让它在地址栏里停留。
  function pskOfForm() {
    return $('in-psk') ? $('in-psk').value : ''
  }

  // 信令 token：与 psk 同一档待遇——**不进地址栏、不进 localStorage / 最近连接**。
  // 它是信令白名单（部署侧 peersignal --tokens / PEERJS_TOKENS）的凭据，泄露
  // 等于把「能连上这台信令冒充任意节点收发消息」的钥匙发出去了（2026-08-18
  // 评审记录：白名单存在的原因就是任意 token 可冒充节点）。链接里带 token=
  // 允许预填（一次性分享），读完立刻从 URL 抹掉，与 psk 同机制。
  // 空 = 不发送该字段（见 peerOptions 里「字段必须缺席」的实测依据）。
  function tokenOfForm() {
    return ($('in-token') ? $('in-token').value : '').trim()
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
    q.delete('psk') // 密钥绝不回写地址栏（见 pskOfForm 注释）
    q.delete('token') // 信令 token 同理（见 tokenOfForm 注释）
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

  /*__PANEL_TRANSFERS_PART__*/

  /*__PANEL_SHARES_PART__*/

  function peerOptions(sig, token) {
    // secure 必须显式传：自托管信令常用 ws://，peerjs 默认 secure=true 会去连 wss://，
    // 表现为「连不上但不报错」——最常见的踩坑点。
    // id 用固定的 myId（见顶部注释）：private 级别的好友名单是按这个 id 判的。
    //
    // token 是信令白名单（peersignal --tokens / PEERJS_TOKENS）的**发送口**（审计 A-2）。
    // ⚠️ 空时必须**整个字段缺席**，不能传空串也不能传 undefined：
    //   实测 dist/peerjs.min.js 里 Peer 的默认值是 `{token: randomToken(), ...用户选项}`，
    //   显式给 `token: ''` 会**覆盖**那个随机值，WS URL 拼成 `&token=`，而信令
    //   （signalserver HandleWS）对空 token 直接回 "No id, token, or key supplied"——
    //   连**没开白名单**的信令都连不上。字段缺席才会落到 peerjs 的随机 token 老路，
    //   默认行为与改动前逐字节一致（白名单关着时服务端根本不读这个值）。
    var opts = {
      host: sig.host, port: sig.port, path: sig.path,
      key: sig.key, secure: sig.secure, debug: 1, id: myId,
    }
    if (token) opts.token = token
    return opts
  }

  // idTaken 判断「连不上」是不是因为本端 id 被占用。
  // peerjs 报的类型是 unavailable-id（文案常是 ID "xxx" is taken）。不认出来的话
  // 用户会往网络/信令方向排查，而真实原因多半是他自己另开了一个面板页。
  function idTaken(e) {
    var m = String((e && e.message) || '')
    return /unavailable-id|is taken|already/i.test(m)
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

  // pskHint 门禁提示。对端回 PSK_REQUIRED 时，用户的修复动作是「去填密钥」，
  // 而不是排查网络——不加这句，绝大多数人会以为是节点离线（典型误判）。
  function pskHint(e) {
    if (!e || e.code !== window.PeerDrive.ERR.PSK_REQUIRED) return null
    return '该节点开了预共享密钥门禁（节点侧 PEERDRIVE_PSK）：在「预共享密钥」框里填' +
      '同一个密钥再连一次。拿不到密钥就只能找节点运营者要——门禁本来就是为了' +
      '拦住没钥匙的人。'
  }

  // dial 拨一次号。单独抽出来是为了 id 冲突时能原样重试一次（见 connect）。
  // token 单独成参（不进 sig）：sig 会被 remember() 存进 localStorage，
  // 凭据不能跟着「最近连接」落盘（同 psk 的理由，见 tokenOfForm）。
  function dial(nodeId, sig, psk, token) {
    return window.PeerDrive.connectToPeer(window.Peer, nodeId, {
      peerOptions: peerOptions(sig, token),
      idleTimeoutMs: 60 * 1000,
      psk: psk, // 空串 = 不出示（对端没开门禁时完全无感）
    })
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
      var psk = pskOfForm()
      var token = tokenOfForm()
      if (token) log('信令 token 已出示（' + token.length + ' 字符，不回写地址栏）')
      var client
      try {
        client = await dial(nodeId, sig, psk, token)
      } catch (e) {
        // 本端 id 撞了（自己另开一个面板页、或 localStorage 被复制到别处）：
        // 换一个 id 重试一次。不重试的话界面上只有"信令失败"，指向完全错误的方向。
        if (!idTaken(e)) throw e
        log('我的节点 id ' + myId + ' 已被占用（多半是你自己另开了一个面板），换一个再连', 'warn')
        saveMyId(newPanelId())
        client = await dial(nodeId, sig, psk, token)
      }
      s.client = client
      s.status = 'online'
      // 我的 id 是**自己带上去**的固定 id，不从连接结果里取——连接结果里的
      // localPeerId 只是"这次实际用上的"，冲突时会和保存的那个不一致。
      renderMyId()
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
      // 白名单拒绝发生在 **HTTP 升级层**（signalserver 回 400 "Invalid token
      // provided"），WebSocket API 不会把响应体递给 JS —— 面板只能看到 bad
      // handshake 一类无差别错误。不点名 token 的话，开了白名单的信令上，
      // 用户会往「节点离线」方向排查（正是这条防线最难自查的失败形态）。
      if (/handshake|signalling|socket/i.test(String((e && e.message) || '')) && !tokenOfForm()) {
        log('若信令开了 token 白名单（peersignal --tokens / PEERJS_TOKENS）：' +
          '在「信令 token」框里填白名单里的值再连。token 留空时本面板不会发送该字段。', 'warn')
      }
      if (pskHint(e)) log(pskHint(e), 'warn')
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
      if (pskHint(e)) log(pskHint(e), 'warn')
      renderNodes()
      if (current === s.id) updateShares()
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
    // psk= 允许预填（一次性分享链接），但读完立即从地址栏抹掉
    if (q.get('psk')) {
      $('in-psk').value = q.get('psk')
      q.delete('psk')
      try { history.replaceState(null, '', location.pathname + '?' + q.toString()) } catch (e) { /* file:// 下可能不让改 */ }
      log('已从链接读入预共享密钥，并将其从地址栏移除（不留在历史记录里）', 'warn')
    }
    // token= 同理：信令白名单凭据可经链接预填，读完即从地址栏抹掉（见 tokenOfForm）
    if (q.get('token')) {
      $('in-token').value = q.get('token')
      q.delete('token')
      try { history.replaceState(null, '', location.pathname + '?' + q.toString()) } catch (e) { /* file:// 下可能不让改 */ }
      log('已从链接读入信令 token，并将其从地址栏移除（不留在历史记录里）', 'warn')
    }

    // 分享链接带来的 hash（unlisted 的取回入口）
    var hq = (q.get('hash') || '').trim()
    if (hq) {
      if (/^[0-9a-f]{64}$/i.test(hq)) {
        pendingHash = hq
        log('链接里带着一个 hash（' + hq.slice(0, 16) + '…），连上节点后即可取回', 'ok')
      } else {
        log('链接里的 hash 不是 64 位十六进制，已忽略：' + hq.slice(0, 32), 'warn')
      }
    }

    $('btn-connect').addEventListener('click', connect)
    $('btn-discover').addEventListener('click', discoverNow)
    $('btn-put').addEventListener('click', putFiles)
    $('btn-pull').addEventListener('click', pullUrl)
    if ($('btn-copy-id')) $('btn-copy-id').addEventListener('click', function () {
      copyText(myId).then(function (done) {
        done ? log('已复制我的节点 id：' + myId + ' —— 把它给节点运营者，加进好友名单就能拿到 private 内容', 'ok')
          : log('复制失败，我的节点 id：' + myId, 'warn')
      })
    })
    if ($('btn-new-id')) $('btn-new-id').addEventListener('click', function () {
      var old = myId
      saveMyId(newPanelId())
      log('已更换我的节点 id：' + old + ' → ' + myId +
        '（改了之后，运营者好友名单里的旧 id 就失效了，记得把新的给他）', 'warn')
    })
    $('in-url').addEventListener('keydown', function (e) { if (e.key === 'Enter') pullUrl() })
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
    renderMyId()
    renderLinked(sessions.get(current))
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
