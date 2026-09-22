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

	// router 多源文件路由（source.Manager，第 3 项优化 2026-08-18）：
	// serveFile 接本地→对端→URL 模板路由；nil = 退回本地语义（openFile）。
	// 装配在 cmd/server/main.go（SetFileRouter）。接口解耦避免 import 环
	// （source 包引 transport）。
	router FileRouter

	fileIndex *FileIndexService // sha256 → 绝对路径 索引（create/upload/list/info/sync）

	// extraPeers 运行时追加的常驻对端（节点市场「加入节点」的持久化清单，
	// 由 service.NodeDirectory 提供）。与配置 PEERDRIVE_PEERJS_PEERS 同语义：
	// 每次信令重连后自动拨号，且**不受 PEERDRIVE_MAX_PEERS 预算限制**
	// （那是运营者显式加入的节点，不是发现撞见的陌生节点）。
	// nil = 无额外对端。
	extraPeers func() []string

	// shareProvider 本节点对外共享范围（share.go，M2）。由 main 注入
	// service.NodeShare.SnapshotFor；nil = 未启用共享，share 帧回空快照。
	// 用独立锁而不是在装配期裸写：main 在 Start() 之后才注入（startLoop
	// 已经在跑，发现组件可能已在 announce），裸写是 data race。
	//
	// 入参是请求者的节点 ID：share 帧走的是已建立的连接，对端 id 是已知的，
	// 所以"好友能看到 private 清单"可以实现（否则给了权限却没给目录）。
	shareMu       sync.RWMutex
	shareProvider func(peerID string) ShareSnapshot

	// shareGate 下载门禁（share.go）：按 hash 判断请求者能否取回。
	// 与 shareProvider 分开注入：清单（share 帧）与下载（req 帧）是两条路径，
	// 门禁只在 req 上生效——unlisted 的内容不出现在清单里，但 req 要放行。
	shareGate ShareGate

	// forward 转发授权规则（key 原文 → 端口白名单）与待验证质询（forward.go）。
	// 规则即凭证：运行时动态增删（端点）与配置装载（SetForwardRules）共用同一锁。
	forwardMu    sync.Mutex
	forwardRules map[string][]int
	nonceMu      sync.Mutex
	fwNonces     map[string]*fwdNonce // reqId → 质询（取出即标 used，防重放）

	// admin 管理面内部转发 handler（admin.go）：由 router.SetupRouter 注入，
	// 包装 gin engine 复用全部 controller。adminMu 保护装配期写入与并发读取
	// （serveAdmin 是连接 goroutine，装配完成后并发调用）。
	adminMu      sync.Mutex
	adminHandler AdminHandler

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
		cfg:          cfg,
		storageDir:   storageDir,
		id:           id,
		iceServers:   parseICEServers(cfg.WebRTCSTUNServer, cfg.WebRTCTURNServer),
		conns:        make(map[string]Session),
		pending:      make(map[Session]*connState),
		connecting:   make(map[string]struct{}),
		fileIndex:    NewFileIndexService(cfg.DownloadDir),
		forwardRules: make(map[string][]int),
		fwNonces:     make(map[string]*fwdNonce),
		closed:       make(chan struct{}),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// ID 返回本节点在信令网络中的 peer id。
func (s *PeerJSService) ID() string { return s.id }

// currentPeer 返回当前信令 peer（受 peerMu 保护，供 HTTPDiscovery 等并发回调安全读取）。
func (s *PeerJSService) currentPeer() *peerjs.Peer {
	s.peerMu.Lock()
	defer s.peerMu.Unlock()
	return s.peer
}

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
			opts.Host = config.DefaultSignalHost
		}
		if opts.Port == "" {
			opts.Port = config.DefaultSignalPort
		}
		if opts.Key == "" {
			opts.Key = config.DefaultSignalKey
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
		// 连接「市场里加入」的常驻对端（与静态 PEERS 同等地位，见 extraPeers 注释）
		if s.extraPeers != nil {
			for _, pid := range s.extraPeers() {
				pid = strings.TrimSpace(pid)
				if pid == "" || pid == s.id {
					continue
				}
				go s.connectLoop(pid)
			}
		}

		// 房间发现（多路并取）：自托管信令服务器的发现 API 优先，否则 MQTT。
		// peerMu 保护 httpDisc/discovery 的读写（低危 2 修复的补齐）：Close
		// 并发置 nil，无锁快照是 data race（-race 集成测试连跑暴露，2026-08-18）。
		// 重建前查 ctx：Close 已 cancel 时不再启动新发现（防清理竞态泄漏
		// goroutine——Close 置 nil 后本循环仍可能走到这里）。
		s.peerMu.Lock()
		httpDisc := s.httpDisc
		disc := s.discovery
		s.peerMu.Unlock()
		if s.ctx.Err() != nil {
			return
		}
		if httpDisc == nil && s.cfg.DiscoverURL != "" {
			cols := s.discoveryRooms()
			httpDisc = NewHTTPDiscovery(s.cfg.DiscoverURL, s.id, cols, s.onDiscoveredPeer, func() []string {
				if p := s.currentPeer(); p != nil {
					return p.ConnectedPeers()
				}
				return nil
			})
			// 共享摘要随 announce 上报（只报数量，见 HTTPDiscovery.shareInfo 注释）。
			// 无条件注册：shareLoadInfo 每次调用都重读 provider，晚注入也生效。
			httpDisc.SetShareInfo(s.shareLoadInfo)
			httpDisc.Start()
			s.peerMu.Lock()
			s.httpDisc = httpDisc
			s.peerMu.Unlock()
			log.LogInfo("peerjs: http discovery enabled url=%s collections=%d", s.cfg.DiscoverURL, len(cols))
		} else if disc == nil && s.cfg.MQTTEnable {
			cols := s.collectionHashes()
			disc = NewMQTTDiscovery(s.cfg.MQTTBroker, s.cfg.MQTTTopicPref,
				"pd-node-"+s.id, s.onDiscoveredPeer)
			disc.Start(cols)
			disc.Announce(s.id, cols)
			s.peerMu.Lock()
			s.discovery = disc
			s.peerMu.Unlock()
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

// onDiscoveredPeer 发现回调：对端节点在线，发起互联（已连接则跳过）。
// 发现来源（内容分片房间 / 存在房间）在此合并，唯一区别是拨号预算：
// 存在房间让「任意节点都能发现任意节点」，不加限制会退化成 O(n²) 全互联
// ——所以发现触发的拨号受 PEERDRIVE_MAX_PEERS 约束（静态 PEERS 是运营者
// 显式声明，不受限，见 startLoop）。
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
	if !s.discoveryDialAllowed() {
		log.LogDebug("peerjs: discovery dial to %s skipped (max peers %d reached)", peerID, s.maxPeers())
		return
	}
	go s.connectLoop(peerID)
}

// discoveryDialAllowed 是否还有发现拨号预算（对端节点数 < PEERDRIVE_MAX_PEERS）。
// 计预算时排除 "local"：那是浏览器直连本节点的本地 WS 会话，不是对端节点。
func (s *PeerJSService) discoveryDialAllowed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id := range s.conns {
		if id != "local" {
			n++
		}
	}
	return n < s.maxPeers()
}

// maxPeers 互联层拨号上限（配置 PEERDRIVE_MAX_PEERS，<=0 视为不限）。
func (s *PeerJSService) maxPeers() int {
	if s.cfg.MaxPeers <= 0 {
		return 1 << 30
	}
	return s.cfg.MaxPeers
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
// 注意：这里**不含**本地存储里已有的合集——把本地合集 hash 广播出去等于公开
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

// ConnectedPeerIDs 当前已直连对端的 id 集合（市场/我的节点页的"直连"状态）。
// 与 Connections 的区别：只回 id、不拷 Session（避免调用方持有连接引用），
// 且排除 "local"（浏览器本地 WS 会话不是对端节点）。
func (s *PeerJSService) ConnectedPeerIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.conns))
	for id := range s.conns {
		if id == "local" {
			continue
		}
		out[id] = true
	}
	return out
}

// SetExtraPeers 注入运行时追加的常驻对端（节点市场「加入节点」清单）。
// 语义见 extraPeers 字段注释：重连后自动拨号，不受发现拨号预算限制。
func (s *PeerJSService) SetExtraPeers(fn func() []string) { s.extraPeers = fn }

// EnsureConnection 幂等拨号：已连接/正在连接则无事发生。
// 供「加入节点」即时生效用——不等下一次发现轮询（最长 10s）+ 拨号，
// 用户点"加入"后界面上的"直连"状态要尽快点亮。
// 复用 connectLoop 的 connecting 去重（同一 peerID 不会开两条连接）。
func (s *PeerJSService) EnsureConnection(peerID string) {
	if peerID == "" || peerID == s.id {
		return
	}
	s.mu.Lock()
	_, connected := s.conns[peerID]
	s.mu.Unlock()
	if connected {
		return
	}
	go s.connectLoop(peerID)
}

// FileIndex 暴露本地文件索引（source 体系的 LocalSource 装配用：
// 统一文件管理需要复用同一份 file_index 的路径决策与元数据）。
func (s *PeerJSService) FileIndex() *FileIndexService { return s.fileIndex }

// SetFileRouter 装配多源文件路由（source.Manager，第 3 项优化 2026-08-18）：
// serveFile 由此路由「本地 → 对端 → URL 模板」。nil 可清除（退回本地语义）。
func (s *PeerJSService) SetFileRouter(r FileRouter) { s.router = r }

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
