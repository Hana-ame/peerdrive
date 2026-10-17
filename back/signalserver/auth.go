// Package signalserver — token parsing and ops authentication.
//
// Two independent token systems:
//
//   - Signaling token whitelist (WithTokenWhitelist): gates WebSocket registration.
//     Empty = unrestricted (peerjs protocol default).
//   - Ops token (WithOpsToken): gates /status, /status/key, /discover/leave.
//     Empty = denied (ops surface must default closed).
//
// The two are deliberately separate: securing ops must never break node connectivity.
package signalserver

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// tokenFromRequest reads the signaling token from the query string or the
// `Authorization: Bearer` header.
//
// Two shapes accepted on purpose: the WebSocket upgrade can only carry a query param
// (see HandleWS), while curl/dashboard scripts naturally send a header.
func tokenFromRequest(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// opsTokenOK reports whether this request may use ops-facing REST endpoints (/status and
// the full roster). Policy (2026-10-04):
//
//   - A token must be presented (query `?token=` or `Authorization: Bearer …`).
//   - When a token whitelist is configured, the presented token must be on it.
//   - When **no** whitelist is configured, the request is rejected (2026-10-06 fix:
//     it used to pass any non-empty token, which made the check decorative — see
//     the opsTokenOK body). Note this is deliberately NOT peerjs's "empty whitelist =
//     unrestricted": that one is protocol behaviour, this one is an ops surface.
//     This is deliberate: default deployments have no token to present, and hard-failing
//     would break every existing /status dashboard. The real fix is operators turning the
//     whitelist on (`-tokens`); this keeps that the single switch.
//
// An empty token never passes — that is what closes the "any web page reads the roster"
// hole for the default deployment, because a browser cannot conjure a token.
func (s *Server) opsTokenOK(r *http.Request) bool {
	// Ops credentials are independent of the signaling token whitelist.
	// An unconfigured ops token means "no ops access at all" — default closed.
	if s.opsToken == "" {
		return false
	}
	t := tokenFromRequest(r)
	if t == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(t), []byte(s.opsToken)) == 1 {
		return true
	}
	// ⚠️ 这里原来是「白名单为空 → 任何非空 token 都放行」。
	// 2026-10-06 实测：线上没配 -tokens，于是
	//   curl 'https://peersignal.moonchan.xyz/status?token=totally-made-up'
	// 返回 200 + 信令 key + 全网节点名册。等于「随便编一个 token 就能读」。
	//
	// 语义上「没配白名单」应该意味着**没有可用的 ops 身份**，而不是
	// 「不设防」——后者把一个鉴权字段变成了摆设：任何人都能构造出非空 token。
	// 空 → 一律拒绝，与 HandleWS 里 tokenWhitelist 的空=不限制**刻意不同**
	// （那个空是 peerjs 协议行为，不能改；这里是运维面，必须默认关闭）。
	return false
}

// HandleOpsKey GET /status/key → 只回信令 key，且要求完整 ops 鉴权。
//
// 从 /status 里拆出来的原因见 HandleStatus 的注释：/status 是同源面板轮询用的，
// 面板不需要知道 key；而「核对部署配置」需要，所以给它一个单独的、默认无凭据
// 就进不来的端点。不设 CORS 头 —— 运维从命令行查，不该有网页能读。
func (s *Server) HandleOpsKey(w http.ResponseWriter, r *http.Request) {
	if !s.opsTokenOK(r) {
		http.Error(w, "ops token required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// 仍是共享凭据，所以不回显到日志、也不加缓存。
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"key": s.key})
}
