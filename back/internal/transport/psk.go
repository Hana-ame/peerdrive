package transport

// psk.go：节点访问的预共享密钥门禁（config.PeerPSK / PEERDRIVE_PSK）。
//
// 为什么需要：网盘的面板是**公开的静态页面**（GitHub Pages / file://），任何人
// 拿到节点 id + 同一个自托管信令就能握手上来问 share、拉文件。信令只负责牵线，
// 它不做准入——所以准入只能长在节点自己这条连接上，这就是本文件做的事。
//
// 握手（刻意做成单向「出示」，不是挑战-应答）：
//
//	连接建立 → 配了 PSK 的一端立刻发 {"type":"psk-auth","psk":"<密钥>"}
//	          → 对端校验：对 → {"type":"psk-ok"}；错 → {"type":"psk-err","msg":"..."}
//
// 两个设计取舍：
//
//  1. **明文传密钥**：DataChannel 本身强制 DTLS 加密，密钥不会在链路上裸奔；
//     而且服务端本来就要持有明文才能比对，做挑战-应答只是把明文挪出信道，
//     换来的复杂度（nonce 状态、双端发起时机、HMAC 实现）不值。
//     真要防的是"陌生人连上来"，不是"信道被窃听"。
//  2. **不等 psk-ok 就开始发业务帧**：同一条 DataChannel 保序，出示方是
//     「先发 auth 再发业务」，服务端按序处理必然先看到 auth——所以不需要
//     等一个 RTT，也就不会给每次连接凭空加延迟。
//
// 语义边界（对称、两端各自独立）：
//   - 本节点没配 PSK → 开放模式，谁连上都服务（老行为，向后兼容）；
//   - 本节点配了 → 对端必须出示同样的密钥，否则所有数据 verb 回 err；
//   - 对端没配而本端配了 → 本端拒绝对端（对端拉不到我的东西），
//     但**我对对端的请求不受影响**（对端是开放的，它照常服务我）。
//     即：PSK 保护的是「配了它的那个节点」的出站内容。
//   - local（浏览器管理台 WS 会话）豁免：它走的是本机，不经过门禁。

import (
	"crypto/subtle"

	"peerdrive/internal/log"
)

// pskErrCode err 帧的错误码（消费端按 code 分支，不要匹配 msg 文案）。
const pskErrCode = "PSK_REQUIRED"

// servedVerbs 需要门禁放行的帧类型：这些是「对端要我干活」的入站 verb。
// 不在表里的（meta/data/done/err/share-resp…）是对我自己请求的应答，
// 拦掉它们等于把自己的拉取也掐了（对端开放、本端配了 PSK 时会自伤）。
var servedVerbs = map[string]bool{
	"req": true, "create": true, "upload": true, "list": true,
	"share": true, "info": true, "delete": true, "sync": true,
	"fwd-open": true, "fwd-auth": true, "fwd-data": true, "fwd-close": true,
}

// pskEnabled 本节点是否开启了密钥门禁。
func (s *PeerJSService) pskEnabled() bool {
	return s.cfg != nil && s.cfg.PeerPSK != ""
}

// pskSendAuth 连接建立时出示本节点的密钥（配了才发）。
// 必须在注册 OnMessage **之前**发，保证它一定是本端发出的第一帧——
// 顺序即语义（见文件头取舍 2）。
func (s *PeerJSService) pskSendAuth(c Session) {
	if !s.pskEnabled() || c.ID() == "local" {
		return
	}
	// 直接用 map 而不是 dcResp：dcResp 带着一堆 omitempty 字段，走它对端
	// 能解析但帧更胖；这里只需要两个字段。
	if err := c.SendJSON(map[string]string{"type": "psk-auth", "psk": s.cfg.PeerPSK}); err != nil {
		log.LogWarn("peerjs: send psk-auth to %s failed: %v", c.ID(), err)
		return
	}
	log.LogInfo("peerjs: psk-auth sent to %s", c.ID())
}

// servePskAuth 校验对端出示的密钥。
// 用常量时间比较，并且**不因失败而关闭连接**：让对端能重发正确的密钥，
// 也让它在下一次 verb 上收到明确的 err（关连接只会让它看到超时，更难排查）。
func (s *PeerJSService) servePskAuth(c Session, st *connState, got string) {
	if !s.pskEnabled() {
		return // 开放模式：对端多发了 auth，忽略（前向兼容）
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.PeerPSK)) != 1 {
		_ = c.SendJSON(dcResp{Type: "psk-err", Msg: "psk: 密钥不匹配", Code: pskErrCode})
		log.LogWarn("peerjs: psk mismatch from %s, rejected", c.ID())
		return
	}
	st.mu.Lock()
	st.pskOK = true
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "psk-ok"})
	log.LogInfo("peerjs: psk ok from %s", c.ID())
}

// pskGate 门禁判定：这个入站 verb 该不该被拦。
// 返回 true 表示已回 err、调用方必须停止处理该帧。
func (s *PeerJSService) pskGate(c Session, st *connState, r dcResp) bool {
	if !s.pskEnabled() || !servedVerbs[r.Type] || c.ID() == "local" {
		return false
	}
	st.mu.Lock()
	ok := st.pskOK
	st.mu.Unlock()
	if ok {
		return false
	}
	_ = c.SendJSON(dcResp{
		Type:  "err",
		Msg:   "psk: 本节点需要预共享密钥（先用 psk-auth 帧出示）",
		Code:  pskErrCode,
		ReqID: r.ReqID,
		Hash:  r.Hash,
	})
	log.LogWarn("peerjs: verb %q from %s rejected: psk required", r.Type, c.ID())
	return true
}

// PSKState 供状态接口自查看（GET /peerjs/node）：本节点是否开启门禁 +
// 当前有多少条连接上的对端已通过。（HTTP 层要跨包读，故导出；不参与判定。）
func (s *PeerJSService) PSKState() (enabled bool, n int) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for _, st := range s.pending {
		st.mu.Lock()
		if st.pskOK {
			n++
		}
		st.mu.Unlock()
	}
	return s.pskEnabled(), n
}
