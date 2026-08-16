package transport

// peerjs_service.go：PeerJSService 装配层（生命周期 + 连接管理 + 角色装配）。
// 帧角色已按 inbound/outbound 拆分到独立文件（见 conn.go 头注释的角色划分）：
//   - conn.go：连接共享核心（帧类型、connState、bindConn 分派）
//   - inbound.go：入站角色（应答 verb：serveFile / serve* 索引 verb / uploadWorker）
//   - outbound.go：出站角色（发起 verb：FetchFromPeer / requestFile / routeResponse）
//   - ws_session.go / rtc_session.go：方向中立传输（Session 抽象）
//   - file_index.go：方向中立持久化（两角色共用）
//
// 本文件保留 PeerJS 信令生命周期（Start/Close/startLoop）、连接建立
// （connectLoop 拨号 / onIncomingConnection 接受）、本地会话绑定（BindLocal）。
// 注意：连接建立的两条路径（accept/dial）都只是创建一条全双工 Session，随后
// 经 bindConn 挂上同一套 inbound+outbound 角色——WebRTC 连接对称，无方向之分。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/Hana-ame/go-peerjs"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
	hashutil "peerdrive/pkg/hashutil"
)

// PeerJSService 通过 PeerJS 公共信令 + pion/webrtc DataChannel 提供双向文件服务：
//   - 被动接收：浏览器或其它节点连接本节点请求 sha256 内容（入站角色，inbound.go）
//   - 主动发起：连接其它节点拉取文件（出站角色，outbound.go），节点间互联
//
// 帧协议与约束见 conn.go 头注释（协议正确性依赖，勿破坏）。
type PeerJSService struct {
	cfg        *config.Config
	storageDir string
	peer       *peerjs.Peer
	id         string

	iceServers []webrtc.ICEServer

	mu     sync.Mutex
	conns  map[string]Session // key: remote peer id / "local"（WS 本地会话）
	closed chan struct{}

	pendingMu sync.Mutex
	pending   map[Session]*connState // 连接级请求/响应状态（Session 动态类型为指针，可作 map key）

	// peerMu 保护 peer/httpDisc/discovery 的读写（低危 2 修复）：
	// startLoop 写（重连时替换指针），Close 读——之前无锁，关停期 data race
	peerMu    sync.Mutex
	httpDisc  *HTTPDiscovery
	discovery *MQTTDiscovery

	// connecting 去重：同一 peerID 可能被多个来源（配置 PEERS、MQTT/HTTP 发现、
	// 被动连接）触发 connectLoop——双连接浪费资源，靠 conns map 覆盖兜底但
	// 有重复握手开销（低危 3 修复）
	connectingMu sync.Mutex
	connecting   map[string]struct{}

	fileIndex *FileIndexService // sha256 → 绝对路径 索引（create/upload/list/info/sync）

	ctx    context.Context
	cancel context.CancelFunc
}

// NewPeerJSService 创建 PeerJS 文件服务。节点 ID 默认 <prefix>-<随机hex>，
// 常驻在线后其他 peer（浏览器或节点）可通过该 ID 直连。
func NewPeerJSService(cfg *config.Config, storageDir string) *PeerJSService {
	id := cfg.PeerJSID
	if id == "" {
		id = "peerdrive-" + randHex8()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &PeerJSService{
		cfg:        cfg,
		storageDir: storageDir,
		id:         id,
		iceServers: parseICEServers(cfg.WebRTCSTUNServer, cfg.WebRTCTURNServer),
		conns:      make(map[string]Session),
		pending:    make(map[Session]*connState),
		connecting: make(map[string]struct{}),
		fileIndex:  NewFileIndexService(cfg.DownloadDir),
		closed:     make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// ID 返回本节点在信令网络中的 peer id。
func (s *PeerJSService) ID() string { return s.id }

// Start 注册到信令服务器；断线自动重连。异步，不阻塞调用方。
func (s *PeerJSService) Start() {
	go s.startLoop()
}

// Close 关闭信令连接并释放所有 WebRTC 连接。
func (s *PeerJSService) Close() {
	s.cancel()
	s.mu.Lock()
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	conns := make([]Session, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.conns = make(map[string]Session)
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	s.peerMu.Lock()
	peer := s.peer
	httpDisc := s.httpDisc
	s.peer = nil
	s.httpDisc = nil
	s.discovery = nil
	s.peerMu.Unlock()
	if peer != nil {
		peer.Close()
	}
	if httpDisc != nil {
		httpDisc.Stop()
	}
}

func (s *PeerJSService) startLoop() {
	backoff := 2 * time.Second
	for {
		if s.ctx.Err() != nil {
			return
		}
		opts := peerjs.DefaultOptions()
		opts.Host = s.cfg.PeerJSHost
		opts.Port = s.cfg.PeerJSPort
		opts.Secure = s.cfg.PeerJSSecure
		opts.Key = s.cfg.PeerJSKey
		if opts.Host == "" {
			opts.Host = "0.peerjs.com"
		}
		if opts.Port == "" {
			opts.Port = "443"
		}
		opts.ICEServers = s.iceServers
		p := peerjs.NewPeer(s.id, opts)
		p.OnConnection(s.onIncomingConnection)
		s.peerMu.Lock()
		s.peer = p
		s.peerMu.Unlock()
		if err := p.Dial(s.ctx); err != nil {
			log.LogWarn("peerjs: connect failed (retry in %s): %v", backoff, err)
			select {
			case <-time.After(backoff):
			case <-s.ctx.Done():
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second
		log.LogInfo("peerjs: connected, id=%s host=%s", p.ID(), opts.Host)

		// 连接配置的对端节点
		peers := strings.Split(s.cfg.PeerJSPeers, ",")
		for _, pid := range peers {
			pid = strings.TrimSpace(pid)
			if pid == "" || pid == s.id {
				continue
			}
			go s.connectLoop(pid)
		}

		// 房间发现（多路并取）：自托管信令服务器的发现 API 优先，否则 MQTT
		if s.httpDisc == nil && s.cfg.DiscoverURL != "" {
			cols := s.collectionHashes()
			s.httpDisc = NewHTTPDiscovery(s.cfg.DiscoverURL, s.id, cols, s.onDiscoveredPeer)
			s.httpDisc.Start()
			log.LogInfo("peerjs: http discovery enabled url=%s collections=%d", s.cfg.DiscoverURL, len(cols))
		} else if s.discovery == nil && s.cfg.MQTTEnable {
			cols := s.collectionHashes()
			s.discovery = NewMQTTDiscovery(s.cfg.MQTTBroker, s.cfg.MQTTTopicPref,
				"pd-node-"+s.id, s.onDiscoveredPeer)
			s.discovery.Start(cols)
			s.discovery.Announce(s.id, cols)
			log.LogInfo("peerjs: mqtt discovery enabled broker=%s collections=%d", s.cfg.MQTTBroker, len(cols))
		}

		select {
		case <-s.ctx.Done():
			p.Close()
			return
		case <-s.closed:
			p.Close()
			return
		case <-p.Done():
			// H7 修复：信令 WS 意外断开（公共云掉线/代理抖动常见）——之前
			// readLoop 静默退出后 startLoop 只 select ctx/closed 两个永不触发的
			// 信号，节点永久失聪直到重启（心跳空转、connectLoop 永远退避打转）。
			// 现在 Signaller.Done() 在断线时关闭，触发整轮重连（复用 backoff）。
			log.LogWarn("peerjs: signalling connection lost, reconnecting in %s", backoff)
			p.Close()
			select {
			case <-time.After(backoff):
			case <-s.ctx.Done():
				return
			case <-s.closed:
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
		}
	}
}

// BindLocal 注册本地 WebSocket 会话（浏览器直连本节点，帧协议与远端
// DataChannel 完全一致）。本地会话以 "local" 注册，FetchFromPeer("local", ...)
// 即可复用同一拉取路径（出站角色，见 outbound.go）。
func (s *PeerJSService) BindLocal(sess Session) {
	s.bindConn(sess)
	log.LogInfo("peerjs: local session bound (id=%s)", sess.ID())
}

// onDiscoveredPeer MQTT 发现回调：对端节点在线，发起互联（已连接则跳过）。
func (s *PeerJSService) onDiscoveredPeer(peerID string) {
	if peerID == "" || peerID == s.id {
		return
	}
	s.mu.Lock()
	_, ok := s.conns[peerID]
	s.mu.Unlock()
	if ok {
		return
	}
	go s.connectLoop(peerID)
}

// collectionHashes 返回本节点关注的集合 hash 分片（配置 + 本地存储）。
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
	if _, ok := s.connecting[peerID]; ok {
		s.connectingMu.Unlock()
		return
	}
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
		opened := make(chan struct{})
		conn.OnOpen(func(c *peerjs.Connection) {
			s.bindConn(newRTCSession(c))
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
// 此时 DataChannel 尚未 open，过早注册会让 FetchFromPeer 拿到未就绪连接
// （发现背景：双向发现时双端同时发起连接，B 侧 answerer 连接未 open 即被使用，
// 报 "connection not open"）。
func (s *PeerJSService) onIncomingConnection(c *peerjs.Connection) {
	c.OnOpen(func(c *peerjs.Connection) {
		s.bindConn(newRTCSession(c))
		log.LogInfo("peerjs: incoming connection from %s (conn=%s)", c.PeerID, c.ID)
	})
}

// Connections 返回当前活跃的节点连接（按远端 peer id）。
func (s *PeerJSService) Connections() map[string]Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Session, len(s.conns))
	for k, v := range s.conns {
		out[k] = v
	}
	return out
}

// FileIndex 暴露本地文件索引（source 体系的 LocalSource 装配用：
// 统一文件管理需要复用同一份 file_index 的路径决策与元数据）。
func (s *PeerJSService) FileIndex() *FileIndexService { return s.fileIndex }

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

func parseICEServers(stun, turn string) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	if stun != "" {
		out = append(out, webrtc.ICEServer{URLs: []string{stun}})
	}
	if turn != "" {
		urls := strings.Split(turn, ",")
		out = append(out, webrtc.ICEServer{URLs: urls})
	}
	return out
}
