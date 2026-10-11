package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/pion/webrtc/v4"

	"peerdrive/internal/log"
	"peerdrive/pkg/hashutil"
)

// 存在房间让「任意节点都能发现任意节点」，不加限制会退化成 O(n²) 全互联
// ——所以发现触发的拨号受 PEERDRIVE_MAX_PEERS 约束（静态 PEERS 是运营者
// 显式声明，不受限，见 startLoop）。
func (s *PeerJSService) onDiscoveredPeer(peerID string) {
	if peerID == "" || peerID == s.id || s.IsPeerBlocked(peerID) {
		return
	}
	s.mu.Lock()
	_, ok := s.conns[peerID]
	s.mu.Unlock()
	if ok {
		return
	}
	if !s.tryReserveDial(peerID, true) {
		log.LogDebug("peerjs: discovery dial to %s skipped (max peers %d reached or already dialing)", peerID, s.maxPeers())
		return
	}
	go s.connectLoop(peerID)
}

// tryReserveDial atomically checks dial budget and registers peerID into s.connecting if allowed (Issue #283).
// If isDiscovery is true, both established conns and in-flight connecting dials are capped by PEERDRIVE_MAX_PEERS.
func (s *PeerJSService) tryReserveDial(peerID string, isDiscovery bool) bool {
	s.connectingMu.Lock()
	defer s.connectingMu.Unlock()
	if _, ok := s.connecting[peerID]; ok {
		return false
	}
	if isDiscovery {
		s.mu.Lock()
		n := 0
		for id := range s.conns {
			if id != "local" {
				n++
			}
		}
		s.mu.Unlock()
		if (n + len(s.connecting)) >= s.maxPeers() {
			return false
		}
	}
	s.connecting[peerID] = struct{}{}
	return true
}

// discoveryDialAllowed 是否还有发现拨号预算（已建连接 + 正在拨号中的 in-flight 连接 < PEERDRIVE_MAX_PEERS）。
// 计预算时排除 "local"：那是浏览器直连本节点的本地 WS 会话，不是对端节点。
// Issue #283：预算检查纳入 in-flight 拨号计数（s.connecting），防止瞬时突发发现穿透 MAX_PEERS。
func (s *PeerJSService) discoveryDialAllowed() bool {
	s.mu.Lock()
	n := 0
	for id := range s.conns {
		if id != "local" {
			n++
		}
	}
	s.mu.Unlock()

	s.connectingMu.Lock()
	inFlight := len(s.connecting)
	s.connectingMu.Unlock()

	return (n + inFlight) < s.maxPeers()
}

// maxPeers 互联层拨号上限（配置 PEERDRIVE_MAX_PEERS，<=0 视为不限）。
func (s *PeerJSService) maxPeers() int {
	if s.cfg.MaxPeers <= 0 {
		return 1 << 30
	}
	return s.cfg.MaxPeers
}

// discoveryMode 返回发现模式配置（PEERDRIVE_DISCOVER_MODE）的决策结果：
// (shouldHTTP, shouldMQTT, mode)。
//
// 模式语义：
//   - "off" / "peerjs"：不启动任何 HTTP/MQTT 发现，仅靠静态 PEERDRIVE_PEERJS_PEERS
//     与市场加入的对端互联。「off」是"无发现服务器部署"的推荐写法，
//     「peerjs」语义相同、写给"只用官方 peerjs 信令"的部署。
//   - "discover"：强制 HTTP 发现（DiscoverURL 非空时），忽略 MQTT。
//   - "mqtt"：强制 MQTT 发现（MQTT_ENABLE=true 时），忽略 HTTP。
//   - "auto"（默认）：DiscoverURL 非空 → HTTP；否则 MQTT_ENABLE → MQTT。
//
// 未知值回退 auto 并告警（config.Validate 启动期就拦，这里兜底防御）。
func (s *PeerJSService) discoveryMode() (shouldHTTP, shouldMQTT bool, mode string) {
	mode = s.cfg.DiscoverMode
	if mode == "" {
		mode = "auto"
	}

	auto := func() (bool, bool) {
		http := s.cfg.DiscoverURL != ""
		mqtt := !http && s.cfg.MQTTEnable
		return http, mqtt
	}

	switch mode {
	case "off", "peerjs":
		log.LogInfo("peerjs: discovery mode=%s (no HTTP/MQTT discovery; nodes connect via PEERDRIVE_PEERJS_PEERS)", mode)
		return false, false, mode
	case "discover":
		shouldHTTP = s.cfg.DiscoverURL != ""
		if !shouldHTTP {
			log.LogWarn("peerjs: discovery mode=discover but PEERDRIVE_DISCOVER_URL is empty; no discovery started")
		}
		return shouldHTTP, false, mode
	case "mqtt":
		shouldMQTT = s.cfg.MQTTEnable
		if !shouldMQTT {
			log.LogWarn("peerjs: discovery mode=mqtt but PEERDRIVE_MQTT_ENABLE is false; no discovery started")
		}
		return false, shouldMQTT, mode
	case "auto":
		shouldHTTP, shouldMQTT = auto()
		return shouldHTTP, shouldMQTT, mode
	default:
		log.LogWarn("peerjs: unknown discovery mode=%q, falling back to auto", mode)
		shouldHTTP, shouldMQTT = auto()
		return shouldHTTP, shouldMQTT, "auto"
	}
}

// discoveryRooms 返回 announce/查询用的房间列表 = 配置声明的内容分片房间
// + （可选）节点级存在房间。仅 HTTP 发现使用：公共 MQTT broker 上开全局
// 存在房间等于向公网广播本节点，不做。
func (s *PeerJSService) discoveryRooms() []string {
	rooms := s.collectionHashes()
	if !s.cfg.DiscoverPresence {
		return rooms
	}
	// 去重：理论上运营者可以把存在房间 hash 写进 PEERDRIVE_MQTT_COLLECTIONS
	// （不可能猜中，但重复房间名会让 announce 出现无意义重复项）。
	for _, r := range rooms {
		if r == PresenceRoom {
			return rooms
		}
	}
	return append(rooms, PresenceRoom)
}

// collectionHashes 返回本节点声明关注的内容分片房间（仅配置 PEERDRIVE_MQTT_COLLECTIONS）。
// 注意：这里不含本地存储里已有的合集——把本地合集 hash 广播出去等于公开
// 「本节点持有这些内容」，受限/私有合集更会直接泄露房间名。本地合集进房间
// 需要按可见性过滤（只广播 public），留给「文件范围管理」阶段。
func (s *PeerJSService) collectionHashes() []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range strings.Split(s.cfg.MQTTCollections, ",") {
		h = strings.TrimSpace(h)
		if hashutil.IsStrictSHA256(h) && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// connectLoop 保持与远端节点的连接（断开自动重连），直到服务关闭。
// 为什么不用一次性 Connect：对端可能尚未上线（OFFER 入队过期 → EXPIRE），
// 或 ICE 协商失败（failed → Close），必须循环重试直到真正 OnOpen。
// 注意 opened 用一次性 close 而不是带缓冲 channel，防止 OnOpen 重复触发时泄漏。
// 低危 3 修复：connecting 去重——同一 peerID 可能被配置 PEERS 与发现回调
// 同时触发，双 connectLoop 会开两条重复连接（之前靠 conns map 覆盖兜底）。
func (s *PeerJSService) connectLoop(peerID string) {
	s.connectingMu.Lock()
	s.connecting[peerID] = struct{}{}
	s.connectingMu.Unlock()
	defer func() {
		s.connectingMu.Lock()
		delete(s.connecting, peerID)
		s.connectingMu.Unlock()
	}()

	backoff := 2 * time.Second
	for {
		if s.ctx.Err() != nil {
			return
		}
		if s.IsPeerBlocked(peerID) {
			log.LogWarn("peerjs: connectLoop to blocked peer %s cancelled", peerID)
			return
		}
		s.peerMu.Lock()
		peer := s.peer
		s.peerMu.Unlock()
		if peer == nil {
			return
		}
		conn, err := peer.Connect(s.ctx, peerID, "peerdrive")
		if err != nil {
			log.LogWarn("peerjs: connect to %s failed (retry in %s): %v", peerID, backoff, err)
			if !sleepCtx(s.ctx, backoff) {
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}

		sess := newRTCSession(conn)

		// Dedup check before st creation (avoids st leak on dedup).
		s.mu.Lock()
		old := s.conns[sess.ID()]
		s.mu.Unlock()
		keepOld := old != nil && old != sess && sess.ID() != "local" &&
			sessionRank(old) < sessionRank(sess)
		if keepOld {
			log.LogInfo("peerjs: dedup connection to %s, closing newer", sess.ID())
			conn.Close()
			continue
		}

		// Create st BEFORE OnOpen to prevent pre-bindConn frame drops.
		st := &connState{
			fetches:   make(map[string]*fetchState),
			verbWaits: make(map[string]chan []byte),
			binCh:     make(chan binaryChunk, 16),
			binDone:   make(chan struct{}),
			fwdCh:     make(chan fwdChunk, 64),
		}

		// Register OnMessage BEFORE OnOpen — closes the race window where the
		// peer's psk-auth arrives before bindConn and is silently dropped.
		sess.OnMessage(func(msg peerjs.Frame) { s.dispatchFrame(sess, st, msg) })
		sess.OnClose(func() { s.cleanupConn(sess, st) })

		opened := make(chan struct{})
		conn.OnOpen(func(c *peerjs.Connection) {
			s.bindConnPrepared(sess, st, old)
			select {
			case <-opened:
			default:
				close(opened)
			}
		})
		select {
		case <-opened:
			backoff = 2 * time.Second
			log.LogInfo("peerjs: connected to %s (conn=%s)", peerID, conn.ID)
			// 等待连接关闭（EXPIRE / ICE 失败 / 对端断开），随后重连
			select {
			case <-conn.Done():
			case <-s.ctx.Done():
				return
			}
		case <-time.After(30 * time.Second):
			log.LogWarn("peerjs: connect to %s timed out", peerID)
			conn.Close()
		}
		if !sleepCtx(s.ctx, backoff) {
			return
		}
	}
}

// onIncomingConnection 被动连接（浏览器或其它节点发起）就绪后绑定消息处理。
// 注意：必须等 OnOpen 再 bindConn——answerer 侧回调在 handleOffer 时立即触发，
// 此时 DataChannel 尚未 open，过早注册会让 FetchFromPeer 拿到未就绪连接。
//
// 2026-10-09 竞态修复：st 与 OnMessage/OnClose 的创建前移至 OnOpen 之前。
func (s *PeerJSService) onIncomingConnection(c *peerjs.Connection) {
	if s.IsPeerBlocked(c.PeerID) {
		log.LogWarn("peerjs: incoming connection from blocked peer %s rejected", c.PeerID)
		c.Close()
		return
	}

	sess := newRTCSession(c)

	// Dedup check before st creation (avoids st leak on dedup).
	s.mu.Lock()
	old := s.conns[sess.ID()]
	s.mu.Unlock()
	keepOld := old != nil && old != sess && sess.ID() != "local" &&
		sessionRank(old) < sessionRank(sess)
	if keepOld {
		log.LogInfo("peerjs: dedup connection to %s, closing newer", sess.ID())
		c.Close()
		return
	}

	// Create st BEFORE OnOpen to prevent pre-bindConn frame drops.
	st := &connState{
		fetches:   make(map[string]*fetchState),
		verbWaits: make(map[string]chan []byte),
		binCh:     make(chan binaryChunk, 16),
		binDone:   make(chan struct{}),
		fwdCh:     make(chan fwdChunk, 64),
	}

	// Register OnMessage BEFORE OnOpen — closes the race window where the
	// peer's psk-auth arrives before bindConn and is silently dropped.
	sess.OnMessage(func(msg peerjs.Frame) { s.dispatchFrame(sess, st, msg) })
	sess.OnClose(func() { s.cleanupConn(sess, st) })

	c.OnOpen(func(c *peerjs.Connection) {
		if s.IsPeerBlocked(c.PeerID) {
			log.LogWarn("peerjs: connection opened from blocked peer %s rejected", c.PeerID)
			c.Close()
			return
		}
		s.bindConnPrepared(sess, st, old)
		log.LogInfo("peerjs: incoming connection from %s (conn=%s)", c.PeerID, c.ID)
	})
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func randHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// parseICEServers builds the ICE server list handed to pion/webrtc (node side) and
// mirrored to browsers via GET /p2p/webrtc/info.
func parseICEServers(stun, turn, turnUser, turnPass string) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	if stun != "" {
		out = append(out, webrtc.ICEServer{URLs: []string{stun}})
	}
	if turn != "" {
		urls := strings.Split(turn, ",")
		srv := webrtc.ICEServer{URLs: urls}
		if turnUser != "" || turnPass != "" {
			srv.Username = turnUser
			srv.Credential = turnPass
			srv.CredentialType = webrtc.ICECredentialTypePassword
		}
		out = append(out, srv)
	}
	return out
}
