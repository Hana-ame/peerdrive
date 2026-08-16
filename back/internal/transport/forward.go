package transport

// forward.go：端口转发（inbound 一环）——在 PeerJS DataChannel 上承载 TCP 隧道。
//
// 背景（为什么有这层）：legacy 的 forward.go 是 libp2p 流实现（`/peerdrive/forward/1.0.0`），
// 认证仅一行明文 `KEY xxx`、无密钥交换、无端口白名单，随 libp2p 栈淘汰。转发需求
// 本身保留（把被 NAT 挡住的节点本地端口暴露给持 key 的远端，如 VPS 常驻节点的
// 127.0.0.1 服务），因此在 transport 角色体系里以新 verb 重建：
//
//	client ──fwd-open {port, reqId}──────────────────▶ server
//	client ◀──fwd-challenge {nonce, reqId}────────────  server   一次性随机数
//	client ──fwd-auth {hmac, reqId}──────────────────▶ server   HMAC-SHA256(key, nonce)
//	client ◀──fwd-ok / fwd-err──────────────────────── server
//	之后: fwd-data 头 + 二进制块双向透传（帧协议同文件传输：头文本、块二进制、原子连续）
//	      fwd-close 收尾（任一侧 EOF/主动关闭）
//
// 安全设计（对应「权限控制 + 密钥交换」要求）：
//  1. 服务端规则表只认 key 原文白名单 `key → 允许端口[]`（配置 PEERDRIVE_FORWARD_RULES，
//     运行时可用端点动态追加）。key 等价于凭证——文档注明配置文件 chmod 600。
//  2. 质询-响应：nonce 一次性（取出即标 used + 5 分钟过期），HMAC 证明持有 key，
//     key 明文永不落线（DataChannel 本身 DTLS 加密，双保险）。
//  3. 端口越权拒绝：请求端口 ∉ key 授权列表 → fwd-err，不泄露规则细节。
//  4. SSRF 防护：服务端只允许 dial 127.0.0.1（转发目标是本机端口，不许打任意内网 IP）。
//  5. 握手不占隧道槽：fwd-open/fwd-auth 只是质询状态；隧道建立才占连接级单槽
//     connState.fwd（同一连接同时只有一条活跃转发流——文件拉取/上传与之互不阻塞，
//     因为二进制块路由按「fwd-data 头声明归属」先行）。
//
// 转发块写方向不能卡消息泵：bindConn 泵内只把块投递到 fw.wCh（有界），实际
// Write 到隧道另一端在连接级 worker（uploadWorker 的 select 共用，见 conn.go 的
// binCh 同款 H5 思路）——否则对端 TCP 背压会头-of-line 冻结整条连接。

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"peerdrive/internal/log"
)

// 转发握手质询有效期与防洪水上限。
const (
	fwdNonceTTL     = 5 * time.Minute
	fwdNonceMax     = 64               // 未消费质询上限（防 fwd-open 洪水占内存）
	fwdChunkSize    = 32 * 1024        // 隧道数据块大小（转发是流，块小一点延迟低）
	fwdHandshakeTTL = 30 * time.Second // 客户端侧握手总超时（对端不回包不挂死）
)

// fwdNonce 服务端侧一次性质询（reqId → 待验证状态）。
type fwdNonce struct {
	nonce  []byte
	port   int // fwd-open 里客户端声明的目标端口
	expire time.Time
	used   bool
}

// fwdStream 连接级活跃转发隧道（单槽：同一连接同时只有一条）。
// 两端语义：
//   - 服务端（被转发方）：out = dial 127.0.0.1:port 的 TCP 连接
//   - 客户端（发起方）：out = net.Pipe 的服务端端（返回给调用方的是 client 端）
type fwdStream struct {
	reqID   string
	keyID   string // 授权 key 前 16 hex（审计日志用，不落全量）
	port    int
	out     net.Conn
	pending bool // fwd-data 头已到、期待下一个二进制块（单槽声明，见 conn.go 泵内路由）
	closed  bool
}

// fwdHandshake 客户端侧握手等待状态（连接级单槽）。
// 两个 channel 分开：challenge 先到（触发发 fwd-auth），done 终局（ok/err）。
// 坑：若共用同一 channel，OpenForward 阶段1「等 challenge」会在 challenge 到达
// 后无法区分「有 challenge 待继续」与「终局」——先 close 的 channel 会让第二次
// select 立即返回 false 终局。故 challenge/done 各自独立 close 一次。
type fwdHandshake struct {
	reqID     string
	nonce     []byte
	challenge chan struct{}
	done      chan struct{}
	ok        bool
	err       error
}

// ──────────────────────────────────────────────
// 服务端：规则表（白名单：key → 允许端口）
// ──────────────────────────────────────────────

// SetForwardRules 全量设置转发授权规则（key 原文 → 端口白名单）。
// 等价于凭证装载：后续 serveForwardAuth 的 HMAC 验证依赖 key 原文。
// ensureForwardMaps 懒初始化规则/质询表（结构体字面量构造的实例——测试——不经过
// NewPeerJSService；防御 nil map 赋值/读 panic）。
func (s *PeerJSService) ensureForwardMaps() {
	if s.forwardRules == nil {
		s.forwardRules = map[string][]int{}
	}
	if s.fwNonces == nil {
		s.fwNonces = map[string]*fwdNonce{}
	}
}

func (s *PeerJSService) SetForwardRules(rules map[string][]int) {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	s.ensureForwardMaps()
	s.forwardRules = make(map[string][]int, len(rules))
	for k, ports := range rules {
		s.forwardRules[k] = append([]int(nil), ports...)
	}
}

// AddForwardRule 运行时追加授权规则（POST /p2p/forward/create 用，不持久化）。
func (s *PeerJSService) AddForwardRule(key string, port int) error {
	if key == "" || port <= 0 || port > 65535 {
		return errors.New("invalid key or port")
	}
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	s.ensureForwardMaps()
	s.forwardRules[key] = append(s.forwardRules[key], port)
	return nil
}

func (s *PeerJSService) forwardRulePorts(key string) ([]int, bool) {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	s.ensureForwardMaps()
	ports, ok := s.forwardRules[key]
	return ports, ok
}

// ListForwardStreams 本端活跃转发隧道（GET /p2p/forward/list 用）。
func (s *PeerJSService) ListForwardStreams() []ForwardStreamInfo {
	s.mu.Lock()
	cps := make([]Session, 0, len(s.conns))
	for _, c := range s.conns {
		cps = append(cps, c)
	}
	s.mu.Unlock()
	out := make([]ForwardStreamInfo, 0)
	for _, c := range cps {
		st := s.stateFor(c)
		st.mu.Lock()
		if st.fwd != nil && !st.fwd.closed {
			out = append(out, ForwardStreamInfo{PeerID: c.ID(), Port: st.fwd.port, KeyID: st.fwd.keyID})
		}
		st.mu.Unlock()
	}
	return out
}

// ForwardStreamInfo 活跃转发隧道快照（list 端点展示）。
type ForwardStreamInfo struct {
	PeerID string `json:"peer_id"`
	Port   int    `json:"port"`
	KeyID  string `json:"key_id"`
}

// CloseForwardStream 主动断开到指定 peer 的转发隧道（POST /p2p/forward/close 用）。
func (s *PeerJSService) CloseForwardStream(peerID string) {
	s.mu.Lock()
	c := s.conns[peerID]
	s.mu.Unlock()
	if c == nil {
		return
	}
	st := s.stateFor(c)
	st.mu.Lock()
	fw := st.fwd
	if fw != nil {
		fw.closed = true
		st.fwd = nil // 主动断开即释放单槽：调用方（close 端点）语义是「立刻可开新隧道」
	}
	st.mu.Unlock()
	if fw != nil {
		// 通知对端收尾（对端 serveForwardClose 幂等清自己的槽）
		_ = c.SendJSON(dcResp{Type: "fwd-close", ReqID: fw.reqID})
		fw.out.Close()
	}
}

// ──────────────────────────────────────────────
// 服务端：握手处理（入站角色，bindConn 分派进来）
// ──────────────────────────────────────────────

// serveForwardOpen 响应 fwd-open：发一次性质询。不建隧道、不验证 key（验证
// 在 fwd-auth 拿着 nonce 才算——HTTP 式「先拿质询再算应答」，防嗅探复用）。
func (s *PeerJSService) serveForwardOpen(c Session, st *connState, r dcResp) {
	if r.Port <= 0 || r.Port > 65535 {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "invalid port", ReqID: r.ReqID})
		return
	}
	// 规则表空 = 本节点未开放任何转发（等价旧版 ForwardEnable=false）
	s.forwardMu.Lock()
	s.ensureForwardMaps()
	empty := len(s.forwardRules) == 0
	s.forwardMu.Unlock()
	if empty {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward disabled", ReqID: r.ReqID})
		return
	}
	now := time.Now()
	s.nonceMu.Lock()
	s.ensureForwardMaps()
	// 防洪水：未消费质询超上限直接拒绝（陈旧的一并清理）
	if len(s.fwNonces) >= fwdNonceMax {
		for k, n := range s.fwNonces {
			if now.Sub(n.expire) > 0 {
				delete(s.fwNonces, k)
			}
		}
		if len(s.fwNonces) >= fwdNonceMax {
			s.nonceMu.Unlock()
			_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "too many challenges", ReqID: r.ReqID})
			return
		}
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		s.nonceMu.Unlock()
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "internal", ReqID: r.ReqID})
		return
	}
	s.fwNonces[r.ReqID] = &fwdNonce{nonce: nonce, port: r.Port, expire: now.Add(fwdNonceTTL)}
	s.nonceMu.Unlock()
	_ = c.SendJSON(dcResp{Type: "fwd-challenge", Nonce: hex.EncodeToString(nonce), ReqID: r.ReqID})
}

// serveForwardAuth 验证 HMAC（证书持有证明）→ 按 key 白名单建隧道。
func (s *PeerJSService) serveForwardAuth(c Session, st *connState, r dcResp) {
	// 取出即标 used（防重放：同一 nonce 二次提交必失败）
	s.nonceMu.Lock()
	n := s.fwNonces[r.ReqID]
	if n != nil {
		n.used = true
		delete(s.fwNonces, r.ReqID)
	}
	s.nonceMu.Unlock()
	if n == nil || n.used == false { // used==false 不可能（上面已标 true），仅防御
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "no challenge", ReqID: r.ReqID})
		return
	}
	if time.Now().After(n.expire) {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "challenge expired", ReqID: r.ReqID})
		return
	}
	// 遍历规则表验证 HMAC（key 原文只在服务端内存，客户端只持有 key）
	var matchedKey string
	s.forwardMu.Lock()
	for key := range s.forwardRules {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(n.nonce)
		if hmac.Equal([]byte(r.Hmac), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			matchedKey = key
			break
		}
	}
	if matchedKey == "" {
		s.forwardMu.Unlock()
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "unauthorized", ReqID: r.ReqID})
		return
	}
	ports := append([]int(nil), s.forwardRules[matchedKey]...)
	s.forwardMu.Unlock()
	// 端口越权：key 合法但端口不在授权列表 → 拒绝（不泄露规则）
	allowed := false
	for _, p := range ports {
		if p == n.port {
			allowed = true
			break
		}
	}
	if !allowed {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "port not authorized", ReqID: r.ReqID})
		return
	}
	st.mu.Lock()
	if st.fwd != nil && !st.fwd.closed {
		st.mu.Unlock()
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "tunnel already active", ReqID: r.ReqID})
		return
	}
	st.mu.Unlock()
	// SSRF 防护：只 dial 本机 loopback（转发目标是本节点端口）
	tcpConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", n.port))
	if err != nil {
		log.LogWarn("peerjs: forward dial 127.0.0.1:%d failed: %v", n.port, err)
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "dial failed", ReqID: r.ReqID})
		return
	}
	fw := &fwdStream{
		reqID: r.ReqID,
		keyID: matchedKey[:min(16, len(matchedKey))],
		port:  n.port,
		out:   tcpConn,
	}
	st.mu.Lock()
	st.fwd = fw
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "fwd-ok", ReqID: r.ReqID})
	log.LogInfo("peerjs: forward tunnel established key=%s 127.0.0.1:%d", fw.keyID, n.port)
	go s.forwardPump(c, st, fw)
}

// serveForwardClose 对端主动关闭隧道：关 out 释放读侧，清槽（幂等）。
func (s *PeerJSService) serveForwardClose(c Session, st *connState, r dcResp) {
	st.mu.Lock()
	fw := st.fwd
	if fw != nil && (r.ReqID == "" || fw.reqID == r.ReqID) {
		fw.closed = true
		st.fwd = nil
	}
	st.mu.Unlock()
	if fw != nil {
		fw.out.Close()
	}
}

// forwardPump 隧道数据泵：读隧道另一端（TCP/pipe）→ SendFrame(fwd-data)。
// 天然处理 EOF：Read 返回错误即通知对端 fwd-close 并清理。
func (s *PeerJSService) forwardPump(c Session, st *connState, fw *fwdStream) {
	buf := make([]byte, fwdChunkSize)
	for {
		n, err := fw.out.Read(buf)
		if n > 0 {
			if serr := c.SendFrame(dcResp{Type: "fwd-data", ReqID: fw.reqID}, buf[:n]); serr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	fw.out.Close()
	// 通知对端收尾（可能对端已先发 fwd-close——幂等，对端清槽判 reqID）
	_ = c.SendJSON(dcResp{Type: "fwd-close", ReqID: fw.reqID})
	// 清本端槽（服务端侧：隧道结束后允许同连接再开新隧道）
	st.mu.Lock()
	if st.fwd == fw {
		st.fwd = nil
	}
	st.mu.Unlock()
}

// routeForwardResponse 客户端侧握手响应路由（fwd-challenge/fwd-ok/fwd-err）。
// challenge 不 close(done)——OpenForward 等 done 一次性收尾。
func (s *PeerJSService) routeForwardResponse(st *connState, r dcResp) {
	st.mu.Lock()
	defer st.mu.Unlock()
	hs := st.fwdHs
	if hs == nil || hs.reqID != r.ReqID {
		return
	}
	switch r.Type {
	case "fwd-challenge":
		nonce, err := hex.DecodeString(r.Nonce)
		if err != nil {
			hs.err = errors.New("bad challenge")
			close(hs.done)
			return
		}
		hs.nonce = nonce
		close(hs.challenge)
	case "fwd-ok":
		hs.ok = true
		close(hs.done)
	case "fwd-err":
		hs.err = errors.New(r.Msg)
		close(hs.done)
	}
}

// ──────────────────────────────────────────────
// 客户端：OpenForward（出站角色）
// ──────────────────────────────────────────────

// OpenForward 向 peerID 建立转发隧道，返回 net.Conn（调用方把它当 TCP 连接用）。
// port=0 时由服务端按 key 规则唯一端口决定（多端口规则须显式指定，否则 fwd-err）。
// 每次握手重新拿一次 nonce（质询式：key 明文本地使用，落线只传 HMAC）。
func (s *PeerJSService) OpenForward(ctx context.Context, peerID, key string, port int) (net.Conn, error) {
	s.mu.Lock()
	c := s.conns[peerID]
	s.mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("peerjs: no connection to %s", peerID)
	}
	st := s.stateFor(c)
	st.mu.Lock()
	if st.fwd != nil && !st.fwd.closed {
		st.mu.Unlock()
		return nil, errors.New("peerjs: forward tunnel already active on this connection")
	}
	if st.fwdHs != nil {
		st.mu.Unlock()
		return nil, errors.New("peerjs: forward handshake already in progress")
	}
	hs := &fwdHandshake{reqID: randHex8(), challenge: make(chan struct{}), done: make(chan struct{})}
	st.fwdHs = hs
	st.mu.Unlock()

	// 清握手状态（无论成败），防重复握手占单槽
	defer func() {
		st.mu.Lock()
		st.fwdHs = nil
		st.mu.Unlock()
	}()

	if err := c.SendJSON(dcResp{Type: "fwd-open", Port: port, ReqID: hs.reqID}); err != nil {
		return nil, err
	}
	// 阶段1：等 challenge（5s，服务端不回即放弃）
	deadline := time.NewTimer(fwdHandshakeTTL)
	defer deadline.Stop()
	select {
	case <-hs.challenge:
	case <-deadline.C:
		return nil, errors.New("peerjs: forward handshake timeout (no challenge)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if hs.err != nil || hs.nonce == nil {
		return nil, hs.err
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(hs.nonce)
	if err := c.SendJSON(dcResp{Type: "fwd-auth", Hmac: hex.EncodeToString(mac.Sum(nil)), ReqID: hs.reqID}); err != nil {
		return nil, err
	}
	// 阶段2：等 ok/err
	select {
	case <-hs.done:
	case <-deadline.C:
		return nil, errors.New("peerjs: forward handshake timeout (no verdict)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if hs.err != nil {
		return nil, hs.err
	}
	if !hs.ok {
		return nil, errors.New("peerjs: forward handshake rejected")
	}
	// 建管道：调用方拿 client 端，pump 读 server 端（数据双向透传）
	client, server := net.Pipe()
	fw := &fwdStream{
		reqID: hs.reqID,
		keyID: key[:min(16, len(key))],
		port:  port,
		out:   server,
	}
	st.mu.Lock()
	st.fwd = fw
	st.mu.Unlock()
	go s.forwardPump(c, st, fw)
	log.LogInfo("peerjs: forward tunnel to %s established", peerID)
	return client, nil
}
