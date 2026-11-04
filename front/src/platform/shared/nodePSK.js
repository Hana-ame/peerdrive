// nodePSK.js — 面板侧的 node PSK 凭据。
//
// 后端 HTTP 服务器是 node 自己开的（同进程同端口，面板也挂在它上面），
// 它的凭据是 `PeerPSK`，不是对等节点身份。文件字节出口
// （/f/ /sha256sum/ /download/ /ipfs/）由 internal/router/auth_middleware.go
// 的 PSKGate 把守：
//     X-PSK: <key>          或   ?psk=<key>
//     Authorization: Bearer <管理员令牌>   也放行（那是同一个操作者的另一把钥匙）
// PeerPSK 未配置则门禁关闭。
//
// 这个模块是面板那半：存/取 PSK、构造请求头、给无法带头的媒体 URL 附查询参数，
// 以及 401 时的一次性凭据对话框。
//
// 与传输层的分工：admin 帧走 lib/ws.js / transport-ws/client.js 的 readToken()
// （注册服务器签发的账号令牌），那是另一套凭据；这里只管 node 自己的 PSK。

import { STORAGE_KEY_NODE_PSK, STORAGE_KEY_AUTH_TOKEN } from './storageKeys.js'

/**
 * readNodePSK: 已保存的 node PSK。localStorage 读失败时返回空串（门禁即关）。
 */
export function readNodePSK() {
  try {
    return localStorage.getItem(STORAGE_KEY_NODE_PSK) || ''
  } catch {
    return ''
  }
}

/**
 * saveNodePSK: 持久化 node PSK。传空串等价于清除。
 */
export function saveNodePSK(value) {
  try {
    const v = typeof value === 'string' ? value.trim() : ''
    if (v) localStorage.setItem(STORAGE_KEY_NODE_PSK, v)
    else localStorage.removeItem(STORAGE_KEY_NODE_PSK)
  } catch {}
}

/**
 * buildAuthHeaders: fetch 要用的认证头。
 * PSK 走 X-PSK（后端 PSKGate 的首选呈现方式）；注册服务器的 Bearer 令牌一并带上，
 * 这样只配了管理员令牌、没配 PSK 的部署也不会被自己设的 PeerPSK 挡在门外。
 * 两者都没有时返回空对象 —— 不带头等于「门禁没开」，这是历史默认。
 */
export function buildAuthHeaders(extra) {
  const out = { ...(extra || {}) }
  const psk = readNodePSK()
  if (psk) out['X-PSK'] = psk
  try {
    let bearer = ''
    if (typeof sessionStorage !== 'undefined') {
      bearer = sessionStorage.getItem(STORAGE_KEY_AUTH_TOKEN) || ''
    }
    if (!bearer) {
      bearer = localStorage.getItem(STORAGE_KEY_AUTH_TOKEN) || ''
    }
    if (bearer) out['Authorization'] = 'Bearer ' + bearer
  } catch {}
  return out
}

/**
 * withPSKQuery: 给 URL 追加 ?psk=… 或 &psk=…（已带则原样返回）。
 * <video>/<img> 这类浏览器自己发起的请求加不了自定义请求头，
 * 只能走查询参数 —— 后端 PSKGate 明确支持这条路径。
 * 代价：PSK 会出现在同源访问日志和地址栏里；媒体流没有第三条路。
 *
 * 不用 URL 构造器：它会把相对路径（apiBase 为空时面板直接挂在同一台 node 上，
 * 构造出 `/sha256sum/…` 这种形式）absolutize 成 `http://origin/…`，改变 URL 形态。
 * 这里手工拼，保持调用方给的相对形式原样，只插入查询参数（在 #fragment 之前）。
 */
export function withPSKQuery(url) {
  const psk = readNodePSK()
  if (!psk || typeof url !== 'string' || !url) return url
  const hashIdx = url.indexOf('#')
  const path = hashIdx >= 0 ? url.slice(0, hashIdx) : url
  const hash = hashIdx >= 0 ? url.slice(hashIdx) : ''
  if (/[?&]psk=/.test(path)) return url
  return `${path}${path.includes('?') ? '&' : '?'}psk=${encodeURIComponent(psk)}${hash}`
}

/**
 * shouldPromptOnUnauthorized: 401 之后要不要弹凭据输入框。
 *
 * 没存过 PSK → 弹：这是第一次接触一台设了门禁的 node。
 * 存了 PSK 却仍然 401 → 不弹：钥匙输错了，弹框只会重复失败。
 *   直接报「去 Settings 改」，让用户看到明确指引而不是又被问一遍。
 *
 * 「一批并发下载只弹一次」的保证靠这一条：第一个成功的输入会持久化，
 * 同批其余下载看到已保存的 PSK 就跳过弹框。
 */
export function shouldPromptOnUnauthorized() {
  return !readNodePSK()
}

/**
 * requestCredential: 401 后的凭据输入框。
 *
 * 用原生 window.prompt 而不是自建弹窗：面板里没有现成的 modal 基建，
 * 而这一步是「输入一次，以后自动带」，不需要额外 UI 状态。
 * 每次调用都会真正弹框（包括已经存了 PSK 的情况 —— 允许当场纠正输错的钥匙）；
 * 要不要调用，由 shouldPromptOnUnauthorized 在调用方判定。
 */
export function requestCredential(reason) {
  if (typeof window === 'undefined' || typeof window.prompt !== 'function') {
    return Promise.resolve(null)
  }
  const answer = window.prompt(
    (reason || '访问被拒绝') +
      '\n\n请输入这台 node 的 PSK（之后会自动带上，不用再输）。取消＝本次放弃。',
    '',
  )
  if (answer === null) return Promise.resolve(null)
  const value = String(answer).trim()
  saveNodePSK(value)
  return Promise.resolve(value || null)
}
