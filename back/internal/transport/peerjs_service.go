package transport

// peerjs_service.go: PeerJSService assembly layer (lifecycle + connection management + role
// assembly). Frame roles have been split to separate files per inbound/outbound (see conn.go
// header comments for role division):
//   - conn.go: connection shared core (frame types, connState, bindConn dispatch)
//   - inbound.go: inbound role (respond to verbs: serveFile / serve* index verbs / uploadWorker)
//   - outbound.go: outbound role (initiate verbs: FetchFromPeer / requestFile / routeResponse)
//   - ws_session.go / rtc_session.go: direction-neutral transport (Session abstraction)
//   - file_index.go: direction-neutral persistence (shared by both roles)
//
// This file retains PeerJS signaling lifecycle (Start/Close/startLoop), connection
// establishment (connectLoop dial / onIncomingConnection accept), local session binding
// (BindLocal).
// Note: the two connection establishment paths (accept/dial) only create a full-duplex
// Session; then bindConn attaches the same inbound+outbound roles — WebRTC connections
// are symmetric, no direction distinction.

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

// PeerJSService provides bidirectional file service via PeerJS public signaling +
// pion/webrtc DataChannel:
//   - Passive reception: browser or other nodes connect to this node to request sha256
//     content (inbound role, inbound.go)
//   - Active initiation: connect to other nodes to fetch files (outbound role, outbound.go),
//     inter-node interconnect
//
// Frame protocol and constraints in conn.go header comments (protocol correctness depends
// on these, do not break).
type PeerJSService struct {
	cfg        *config.Config
	storageDir string
	peer       *peerjs.Peer
	id         string

	iceServers []webrtc.ICEServer

	mu     sync.Mutex
	conns  map[string]Session // key: remote peer id / "local" (WS local session)
	closed chan struct{}

	pendingMu sync.Mutex
	pending   map[Session]*connState // connection-level request/response state (Session dynamic type is pointer, can be map key)

	// peerMu protects peer/httpDisc/discovery read/write (low-risk 2 fix):
	// startLoop writes (pointer replacement on reconnect), Close reads — previously
	// unlocked, data race during shutdown
	peerMu    sync.Mutex
	httpDisc  *HTTPDiscovery
	discovery *MQTTDiscovery

	// connecting dedup: same peerID may be triggered by multiple sources (config PEERS,
	// MQTT/HTTP discovery, passive connections) to call connectLoop — double connections
	// waste resources; conns map overwrite is a backstop but has redundant handshake
	// overhead (low-risk 3 fix)
	connectingMu sync.Mutex
	connecting   map[string]struct{}

	// router multi-source file routing (source.Manager, 3rd optimization 2026-08-18):
	// serveFile routes local→peer→URL template; nil = fall back to local semantics (openFile).
	// Assembly in cmd/server/main.go (SetFileRouter). Interface decoupling avoids import
	// cycle (source package imports transport).
	router FileRouter

	fileIndex *FileIndexService // sha256 → absolute path index (create/upload/list/info/sync)

	// extraPeers runtime-appended persistent peers (node marketplace "join node" persisted
	// list, provided by service.NodeDirectory). Same semantics as config PEERDRIVE_PEERJS_PEERS:
	// automatically dials after each signaling reconnection, and is **not limited by
	// PEERDRIVE_MAX_PEERS budget** (those are operator-explicitly added nodes, not random
	// discovered nodes).
	// nil = no extra peers.
	extraPeers func() []string

	// shareProvider this node's external sharing scope (share.go, M2). Injected by main
	// via service.NodeShare.SnapshotFor; nil = sharing not enabled, share frame returns
	// empty snapshot.
	// Uses independent lock instead of bare write during assembly: main injects after
	// Start() (startLoop already running, discovery components may already be announcing);
	// bare write is a data race.
	//
	// Input parameter is the requester's node ID: share frame goes through an established
	// connection, the peer id is known, so "friends can see private manifests" can be
	// implemented (otherwise giving permission without giving the directory).
	shareMu       sync.RWMutex
	shareProvider func(peerID string) ShareSnapshot

	// shareGate download gate (share.go): determines by hash whether requester can fetch.
	// Separated from shareProvider injection: manifest (share frame) and download (req frame)
	// are two paths; gate only applies to req — unlisted content doesn't appear in manifest
	// but req must pass through.
	shareGate ShareGate

	// forward forwarding authorization rules (plaintext key → port whitelist) and pending
	// challenges (forward.go).
	// Rules are credentials: runtime dynamic add/remove (endpoints) and config loading
	// (SetForwardRules) share the same lock.
	forwardMu    sync.Mutex
	forwardRules map[string][]int
	nonceMu      sync.Mutex
	fwNonces     map[string]*fwdNonce // reqId → challenge (marked used on retrieval, anti-replay)

	// admin management-plane internal forwarding handler (admin.go): injected by
	// router.SetupRouter, wraps gin engine to reuse all controllers. adminMu protects
	// assembly-time writes and concurrent reads (serveAdmin is a connection goroutine,
	// called concurrently after assembly).
	adminMu      sync.Mutex
	adminHandler AdminHandler

	ctx    context.Context
	cancel context.CancelFunc
}

// NewPeerJSService creates the PeerJS file service. Node ID defaults to <prefix>-<random hex>;
// once persistently online, other peers (browser or nodes) can directly connect via this ID.
func NewPeerJSService(cfg *config.Config, storageDir, peerID string) *PeerJSService {
	if peerID == "" {
		peerID = cfg.PeerPrefix + "-" + randHex8()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &PeerJSService{
		cfg:        cfg,
		storageDir: storageDir,
		id:         peerID,
		iceServers: parseICEServers(cfg.StunURL, cfg.TurnURL),
		conns:      make(map[string]Session),
		pending:    make(map[Session]*connState),
		closed:     make(chan struct{}),
		connecting: make(map[string]struct{}),
		ctx:        ctx,
		cancel:     cancel,
	}
	if cfg.MaxPeers <= 0 {
		s.cfg.MaxPeers = 8
	}
	return s
}

// Start initializes the PeerJS peer, starts connection loops and discovery.
func (s *PeerJSService) Start() error {
	peer, err := peerjs.NewPeer(peerjs.Options{
		ID:   s.id,
		Key:  s.cfg.SignalServer,
		ICE:  s.iceServers,
		Debug: s.cfg.Debug,
	})
	if err != nil {
		return err
	}
	s.peerMu.Lock()
	s.peer = peer
	s.peerMu.Unlock()

	peer.On("connection", s.onIncomingConnection)
	peer.On("error", func(err error) {
		log.LogWarn("peerjs: peer error: %v", err)
	})

	// Start discovery
	if s.cfg.DiscoverMode == "http" {
		s.httpDisc = NewHTTPDiscovery(s.cfg.SignalServer, s.id, s.collections(), s.onPeer)
		s.httpDisc.Start()
	} else {
		s.discovery = NewMQTTDiscovery(s.cfg.MQTTBroker, "peerdrive/v1", s.id, s.onPeer)
		s.discovery.Start(s.collections())
	}

	go s.startLoop()
	return nil
}

// Close shuts down the service: disconnect all connections, stop discovery, close peer.
func (s *PeerJSService) Close() error {
	s.cancel()
	<-s.closed
	s.peerMu.Lock()
	p := s.peer
	s.peerMu.Unlock()
	if p != nil {
		p.Destroy()
	}
	s.mu.Lock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = make(map[string]Session)
	s.mu.Unlock()
	log.LogInfo("peerjs: service closed")
	return nil
}

// ID returns the node's peer ID.
func (s *PeerJSService) ID() string { return s.id }

// Connections returns all connected peers (excluding local session).
func (s *PeerJSService) Connections() map[string]Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]Session)
	for id, c := range s.conns {
		if id != "local" {
			result[id] = c
		}
	}
	return result
}

// PeerCount returns the number of connected peers.
func (s *PeerJSService) PeerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns) - 1 // exclude local session
}

// stateFor retrieves the connection state for a session.
func (s *PeerJSService) stateFor(c Session) *connState {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.pending[c]
}

// onIncomingConnection handles an incoming connection from a peer.
func (s *PeerJSService) onIncomingConnection(c *peerjs.Connection) {
	session := newRTCSession(c)
	s.mu.Lock()
	s.conns[session.ID()] = session
	s.mu.Unlock()
	s.bindConn(session)
}

// connectLoop periodically attempts to connect to configured peers.
func (s *PeerJSService) connectLoop() {
	// Initial connection attempt
	s.connectToAll()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.connectToAll()
		case <-s.ctx.Done():
			return
		}
	}
}

// connectToAll dials all configured + discovered peers.
func (s *PeerJSService) connectToAll() {
	// Build peer list: configured peers + extra peers + discovered peers
	s.connectingMu.Lock()
	defer s.connectingMu.Unlock()
	peers := map[string]bool{}
	for _, p := range strings.Split(s.cfg.Peers, ",") {
		p = strings.TrimSpace(p)
		if p != "" && p != s.id {
			peers[p] = true
		}
	}
	if s.extraPeers != nil {
		for _, p := range s.extraPeers() {
			if p != "" && p != s.id {
				peers[p] = true
			}
		}
	}
	for pid := range peers {
		if pid == s.id {
			continue
		}
		if _, connected := s.conns[pid]; connected {
			continue
		}
		if _, connecting := s.connecting[pid]; connecting {
			continue
		}
		s.connecting[pid] = struct{}{}
		go func(pid string) {
			defer func() {
				s.connectingMu.Lock()
				delete(s.connecting, pid)
				s.connectingMu.Unlock()
			}()
			s.connectToPeer(pid)
		}(pid)
	}
}

// connectToPeer dials a single peer.
func (s *PeerJSService) connectToPeer(pid string) {
	s.peerMu.Lock()
	p := s.peer
	s.peerMu.Unlock()
	if p == nil {
		return
	}
	conn, err := p.Connect(pid)
	if err != nil {
		log.LogDebug("peerjs: connect to %s failed: %v", pid, err)
		return
	}
	session := newRTCSession(conn)
	s.mu.Lock()
	// Check if already connected (race condition guard)
	if _, exists := s.conns[session.ID()]; exists {
		s.mu.Unlock()
		return
	}
	s.conns[session.ID()] = session
	s.mu.Unlock()
	s.bindConn(session)
	log.LogInfo("peerjs: connected to %s", pid)
}

// onPeer discovery callback: initiates connection to a newly discovered peer.
func (s *PeerJSService) onPeer(pid string) {
	if pid == s.id {
		return
	}
	s.connectToPeer(pid)
}

// collections returns the list of collections this node is subscribed to.
func (s *PeerJSService) collections() []string {
	var colls []string
	if s.cfg.Collections != "" {
		for _, c := range strings.Split(s.cfg.Collections, ",") {
			c = strings.TrimSpace(c)
			if c != "" && hashutil.IsStrictSHA256(c) {
				colls = append(colls, c)
			}
		}
	}
	return colls
}

// BindLocal binds a local WebSocket session (browser management).
func (s *PeerJSService) BindLocal(session Session) {
	s.mu.Lock()
	s.conns["local"] = session
	s.mu.Unlock()
	s.bindConn(session)
}

// FetchFromPeer fetches a file from a specific peer.
func (s *PeerJSService) FetchFromPeer(peerID, hash string, offset, size int64) (io.ReadCloser, error) {
	return s.OpenStream(peerID, hash, offset, size)
}

// FileIndex returns the file index service.
func (s *PeerJSService) FileIndex() *FileIndexService { return s.fileIndex }

// SetFileRouter assembles multi-source file routing (source.Manager, 3rd optimization
// 2026-08-18): serveFile routes through this for "local → peer → URL template". nil
// clears (falls back to local semantics).
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
