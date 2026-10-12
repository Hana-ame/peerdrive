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
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/Hana-ame/go-peerjs"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
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
	shareMu                sync.RWMutex
	shareProvider          func(peerID string) ShareSnapshot
	shareProviderWithToken ShareProviderWithToken
	shareTokenGenerator    func(peerID string) string

	// shareGate 下载门禁（gate.go）：按 hash 判断请求者能否取回。
	// 与 shareProvider 分开注入：清单（share 帧）与下载（req 帧）是两条路径，
	// 门禁只在 req 上生效——unlisted 的内容不出现在清单里，但 req 要放行。
	shareGate ShareGate

	// authorizer 身份与门禁验证器（gate.go，Phase 7 预埋插件点）。
	// nil 时由 defaultAuthorizer 代理 shareGate + isSelfSession。
	authorizer        Authorizer
	defaultAuthorizer *DefaultAuthorizer

	// defaultCredentialFunc 节点级出站请求身份凭据提供者（Phase 7 预埋）。
	defaultCredentialFunc func(peerID string) string

	// forward 转发授权规则（key 原文 → 端口白名单）与待验证质询（forward.go）。
	// 规则即凭证：运行时动态增删（端点）与配置装载（SetForwardRules）共用同一锁。
	forwardMu    sync.Mutex
	forwardRules map[string][]int
	nonceMu      sync.Mutex
	fwNonces     map[string]*fwdNonce // reqId → 质询（取出即标 used，防重放）

	// admin 管理面内部转发 handler（admin.go）：由 router.Engine 注入，
	// 包装 gin engine 复用全部 controller。adminMu 保护装配期写入与并发读取
	// （serveAdmin 是连接 goroutine，装配完成后并发调用）。
	adminMu      sync.Mutex
	adminHandler AdminHandler

	// blocklist 节点黑名单与准入管理（admission.go）。
	blocklist *PeerBlocklist

	// Capability negotiation (Issue #213).
	capsMu       sync.RWMutex
	localCaps    []string
	requiredCaps []string

	// Disconnect hooks (Issue #214): notified when an active peer connection is cleaned up.
	disconnectMu    sync.Mutex
	disconnectHooks []func(peerID string)

	// Remote control authorizer (Issue #234): validates remote admin execution.
	remoteAuthMu     sync.RWMutex
	remoteAuthorizer func(peerID, token, method, path string) bool

	// Display manager (Issue #243): tracks controlled screens and channel sync.
	displayMgr *DisplayManager

	// Stream manager (Issue #244): P2P live stream chunk broadcast and subscriptions.
	streamMgr *StreamManager

	// QoS Concurrency & Rate Limiter (Issue #269).
	qos *QoSGuard

	ctx    context.Context
	cancel context.CancelFunc
}

// NewPeerJSService 创建 PeerJS 文件服务。节点 ID 默认 <prefix>-<随机hex>，
// 常驻在线后其他 peer（浏览器或节点）可通过该 ID 直连。
func NewPeerJSService(cfg *config.Config, storageDir string) *PeerJSService {
	id := ""
	if cfg != nil {
		id = cfg.PeerJSID
	}
	if id == "" {
		id = "peerdrive-" + randHexN(16) // 128 bits: the signalling plane trusts this as identity
	}
	ctx, cancel := context.WithCancel(context.Background())
	var stun, turn, turnU, turnP, bl string
	if cfg != nil {
		stun = cfg.WebRTCSTUNServer
		turn = cfg.WebRTCTURNServer
		turnU = cfg.WebRTCTURNUsername
		turnP = cfg.WebRTCTURNPassword
		bl = cfg.PeerBlocklist
	}
	downloadDir := ""
	if cfg != nil {
		downloadDir = cfg.DownloadDir
	}
	svc := &PeerJSService{
		cfg:          cfg,
		storageDir:   storageDir,
		id:           id,
		iceServers:   parseICEServers(stun, turn, turnU, turnP),
		conns:        make(map[string]Session),
		pending:      make(map[Session]*connState),
		connecting:   make(map[string]struct{}),
		fileIndex:    NewFileIndexService(downloadDir),
		blocklist:    NewPeerBlocklist(storageDir, bl),
		forwardRules: make(map[string][]int),
		fwNonces:     make(map[string]*fwdNonce),
		displayMgr:   NewDisplayManager(),
		streamMgr:    NewStreamManager(),
		closed:       make(chan struct{}),
		ctx:          ctx,
		cancel:       cancel,
	}
	svc.localCaps = svc.computeLocalCaps()
	maxStreams := int64(8)
	if cfg != nil && cfg.MaxConcurrentStreams > 0 {
		maxStreams = int64(cfg.MaxConcurrentStreams)
	}
	uploadSpeed := int64(0)
	if cfg != nil && cfg.MaxUploadSpeed > 0 {
		uploadSpeed = cfg.MaxUploadSpeed
	}
	svc.qos = NewQoSGuard(maxStreams, uploadSpeed)
	svc.defaultAuthorizer = NewDefaultAuthorizer(svc.currentShareGate)
	return svc
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
		if c.ID() != "local" {
			s.notifyDisconnect(c.ID())
		}
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
		// 数据面 XOR 混淆（back/peerjs/xor.go）：默认关 = 现状明文；开启时
		// 与对端节点互操作要求两端同 key（浏览器 peerjs 客户端不参与 XOR）。
		opts.XOREnable = s.cfg.PeerJSXOREnable
		opts.XORKey = s.cfg.PeerJSXORKey
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
			if s.tryReserveDial(pid, false) {
				go s.connectLoop(pid)
			}
		}
		// 连接「市场里加入」的常驻对端（与静态 PEERS 同等地位，见 extraPeers 注释）
		if s.extraPeers != nil {
			for _, pid := range s.extraPeers() {
				pid = strings.TrimSpace(pid)
				if pid == "" || pid == s.id {
					continue
				}
				if s.tryReserveDial(pid, false) {
					go s.connectLoop(pid)
				}
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
		// 发现模式选择（PEERDRIVE_DISCOVER_MODE）：
		//   auto（默认）   DiscoverURL 非空→HTTP 发现，否则 MQTT_ENABLE→MQTT
		//   peerjs / off   完全不启动 HTTP/MQTT 发现，只靠静态 PEERDRIVE_PEERJS_PEERS
		//                   与市场加入的对端互联——"只用官方 0.peerjs.com 信令、不向
		//                   任何自托管发现 API 上报"正是靠这个模式（信令可选）。
		//   discover       强制 HTTP 发现（要求 DiscoverURL 非空；忽略 MQTT）
		//   mqtt           强制 MQTT 发现（要求 MQTT_ENABLE=true；忽略 HTTP）
		// 为什么做成模式而不是沿用旧的"URL 非空 / MQTTEnable"两个布尔：旧行为没有
		// "显式关掉发现"的通道——默认 DiscoverURL 非空（config.go DefaultDiscoverURL）
		// 会让 auto 模式一直上报到自托管服务器。config.Validate 已在启动期校验模式值。
		shouldHTTP, shouldMQTT, mode := s.discoveryMode()

		// HTTP 发现（仅当模式允许且未启动过）。
		if shouldHTTP && httpDisc == nil {
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
			log.LogInfo("peerjs: http discovery enabled (mode=%s) url=%s collections=%d", mode, s.cfg.DiscoverURL, len(cols))
		}

		// MQTT 发现（仅当模式允许且未启动过）。
		if shouldMQTT && disc == nil {
			cols := s.collectionHashes()
			disc = NewMQTTDiscovery(s.cfg.MQTTBroker, s.cfg.MQTTTopicPref,
				"pd-node-"+s.id, s.onDiscoveredPeer)
			disc.Start(cols)
			disc.Announce(s.id, cols)
			s.peerMu.Lock()
			s.discovery = disc
			s.peerMu.Unlock()
			log.LogInfo("peerjs: mqtt discovery enabled (mode=%s) broker=%s collections=%d", mode, s.cfg.MQTTBroker, len(cols))
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
	s.bindConnInternal(sess, true)
	log.LogInfo("peerjs: local session bound (id=%s)", sess.ID())
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

// HasConnection reports whether a connection currently exists for peerID.
func (s *PeerJSService) HasConnection(peerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[peerID] != nil
}

// MaxPeers returns the configured maximum peer connections (PEERDRIVE_MAX_PEERS).
func (s *PeerJSService) MaxPeers() int {
	if s == nil {
		return 8
	}
	return s.maxPeers()
}

// OnPeerDisconnect registers a callback invoked when an active peer connection is cleaned up (Issue #214).
func (s *PeerJSService) OnPeerDisconnect(fn func(peerID string)) {
	if fn == nil {
		return
	}
	s.disconnectMu.Lock()
	defer s.disconnectMu.Unlock()
	s.disconnectHooks = append(s.disconnectHooks, fn)
}

// notifyDisconnect invokes all registered peer disconnect hooks.
func (s *PeerJSService) notifyDisconnect(peerID string) {
	s.disconnectMu.Lock()
	hooks := append([]func(peerID string){}, s.disconnectHooks...)
	s.disconnectMu.Unlock()
	for _, h := range hooks {
		h(peerID)
	}
}

// SetExtraPeers 注入运行时追加的常驻对端（节点市场「加入节点」清单）。
// 语义见 extraPeers 字段注释：重连后自动拨号，不受发现拨号预算限制。
func (s *PeerJSService) SetExtraPeers(fn func() []string) { s.extraPeers = fn }

// SetRemoteControlAuthorizer sets a custom callback to validate remote control commands (Issue #234).
func (s *PeerJSService) SetRemoteControlAuthorizer(fn func(peerID, token, method, path string) bool) {
	s.remoteAuthMu.Lock()
	defer s.remoteAuthMu.Unlock()
	s.remoteAuthorizer = fn
}

// EnsureConnection 幂等拨号：已连接/正在连接则无事发生。
// 供「加入节点」即时生效用——不等下一次发现轮询（最长 10s）+ 拨号，
// 用户点"加入"后界面上的"直连"状态要尽快点亮。
// 复用 connectLoop 的 connecting 去重（同一 peerID 不会开两条连接）。
func (s *PeerJSService) EnsureConnection(peerID string) {
	if peerID == "" || peerID == s.id || s.IsPeerBlocked(peerID) {
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

// Search 搜本节点的文件索引（controller.fileIndexSearcher 接口的实现之一）。
//
// 只是一层转发：本地搜索不需要 PeerJS 连接、不需要信令，纯 SQLite 查询。
// 之所以挂在 PeerJSService 上而不是让 controller 直接拿 FileIndex()，是为了让
// controller 依赖一个**窄接口**（Search / RequestSearch / ConnectedPeerIDs 三个方法），
// 单测能塞假实现，不必为测一个 handler 起一整个 PeerJSService。
func (s *PeerJSService) Search(q SearchQuery) (*SearchPage, error) {
	return s.fileIndex.Search(q)
}

// SetFileRouter 装配多源文件路由（source.Manager，第 3 项优化 2026-08-18）：
// serveFile 由此路由「本地 → 对端 → URL 模板」。nil 可清除（退回本地语义）。
func (s *PeerJSService) SetFileRouter(r FileRouter) { s.router = r }
