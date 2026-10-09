package peerjs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/Hana-ame/go-peerjs/signalling"
	"github.com/pion/webrtc/v4"
)

// ConnectionHandler receives newly established WebRTC data connections (callback when passively receiving).
type ConnectionHandler func(c *Connection)

// Peer is a PeerJS signaling client: registers with the signaling server, sends/receives OFFER/ANSWER/CANDIDATE
// messages, supports actively initiating connections (Connect) and passively receiving (OnConnection).
// The data plane is encapsulated by Connection; the business layer handles Frame messages.
//
// Extensibility:
//   - Change signaling: inject a custom Signaller via NewPeerWithSignaller
//   - Change transport: Connection depends only on the DataChannel interface (see transport.go)
//   - Add messages: MessageType is an open string type; custom types can be sent directly
type Peer struct {
	signaller  Signaller
	onConn     ConnectionHandler
	iceServers []webrtc.ICEServer

	mu     sync.Mutex
	conns  map[string]*Connection
	closed chan struct{}
}

// DefaultOptions returns the project's common signaling (peersignal.moonchan.xyz) default configuration.
// Not the PeerJS public cloud: nodes and the panel must default to the same signaling, otherwise neither can find the other.
func DefaultOptions() Options {
	s := signalling.DefaultOptions()
	return Options{
		Host:         s.Host,
		Port:         s.Port,
		Secure:       s.Secure,
		Path:         s.Path,
		Key:          s.Key,
		PingInterval: s.PingInterval,
	}
}

// NewPeer creates a PeerJS signaling client.
// If id is empty, the server assigns a random ID; if token is empty, one is randomly generated.
func NewPeer(id string, opts Options) *Peer {
	opts = normalizeOptions(opts)
	p := &Peer{
		conns:  make(map[string]*Connection),
		closed: make(chan struct{}),
	}
	p.iceServers = opts.ICEServers // Options injects ICE servers (SetICEServers has been merged into Options)
	p.signaller = signalling.NewPeerJSSignaller(id, signallingFromOptions(opts), p.route)
	return p
}

// NewPeerWithSignaller creates a Peer with custom signaling (extension point).
func NewPeerWithSignaller(s Signaller) *Peer {
	p := &Peer{
		conns:  make(map[string]*Connection),
		closed: make(chan struct{}),
	}
	p.signaller = s
	s.OnMessage(p.route)
	return p
}

// OnConnection registers the passive connection callback (called when the peer initiates an OFFER).
func (p *Peer) OnConnection(h ConnectionHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onConn = h
}

// ConnectedPeers returns the currently established (open) DataConnection remote peer IDs (deduplicated, unordered).
// The signaling server's graph depends on each node reporting this list, so Peer must expose this query.
// Only counts open connections: connections in handshake have not truly been established and should not enter the graph.
func (p *Peer) ConnectedPeers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]bool)
	var out []string
	for _, c := range p.conns {
		if c.PeerID != "" && c.Open() && !seen[c.PeerID] {
			seen[c.PeerID] = true
			out = append(out, c.PeerID)
		}
	}
	return out
}

// SetICEServers sets ICE servers (STUN/TURN).
// Note: NewPeer uses Options.ICEServers; NewPeerWithSignaller (custom signaling)
// must call this method, otherwise WebRTC only has LAN host candidates.
func (p *Peer) SetICEServers(servers []webrtc.ICEServer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.iceServers = servers
}

// ID returns the peer's identifier (valid after Dial succeeds).
func (p *Peer) ID() string { return p.signaller.ID() }

// Connected returns whether signaling is connected.
func (p *Peer) Connected() bool { return p.signaller != nil && p.signaller.ID() != "" }

// Dial registers with and connects to the signaling server. ctx cancellation aborts the connection.
func (p *Peer) Dial(ctx context.Context) error {
	return p.signaller.Dial(ctx)
}

// Done returns the signaling disconnect notification (passthrough of Signaller.Done; H7 reconnect loop depends on it).
func (p *Peer) Done() <-chan struct{} { return p.signaller.Done() }

// Send sends a signaling message (extension point: custom message types).
func (p *Peer) Send(m Message) error { return p.signaller.Send(m) }

// Connect actively initiates a DataConnection with a remote peer (offerer role).
// Connection readiness is notified via conn.OnOpen; ctx is used to cancel negotiation.
func (p *Peer) Connect(ctx context.Context, dst, label string) (*Connection, error) {
	if dst == "" {
		return nil, fmt.Errorf("peerjs: empty remote id")
	}
	return p.newConnection(dst, label, true, p.iceServers, "")
}

// Close closes the signaling connection and cleans up all WebRTC connections.
func (p *Peer) Close() {
	p.mu.Lock()
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	conns := make([]*Connection, 0, len(p.conns))
	for _, c := range p.conns {
		conns = append(conns, c)
	}
	p.conns = make(map[string]*Connection)
	p.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	if p.signaller != nil {
		_ = p.signaller.Close()
	}
}

// route dispatches signaling messages.
// OFFER/ANSWER/CANDIDATE are all "connection-type" messages: ANSWER/CANDIDATE route to existing connections by connectionId,
// OFFER creates a new connection (answerer).
func (p *Peer) route(m Message) error {
	switch m.Type {
	case MsgOffer:
		p.handleOffer(m)
		return nil
	case MsgHeartbeat:
		// Server heartbeat: the client's active ping (heartbeatLoop) already keeps it alive; no response needed
		return nil
	case MsgLeave:
		p.handleLeave(m)
		return nil
	case MsgAnswer, MsgCandidate:
		connID := payloadConnectionID(m)
		if connID == "" {
			return nil
		}
		p.mu.Lock()
		conn := p.conns[connID]
		p.mu.Unlock()
		if conn != nil {
			conn.handleMessage(m)
			return nil
		}
	case MsgExpire:
		// EXPIRE: OFFER expired in the signaling server's queue (peer did not come online in time).
		// Close the connection so the upper-layer connectLoop reconnects (otherwise it would wait for OnOpen forever).
		p.mu.Lock()
		connID := payloadConnectionID(m)
		conn := p.conns[connID]
		p.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
	case MsgError, MsgIDTaken:
		// Low-severity 7 fix: ID taken / server error was previously silently ignored — two nodes with the same ID
		// would both lose contact with no trace. Log for debugging (same ID is a configuration error; reconnect cannot resolve it)
		var payload struct {
			Msg string `json:"msg"`
		}
		_ = json.Unmarshal(m.Payload, &payload)
		if m.Type == MsgIDTaken {
			log.Printf("peerjs: ID-TAKEN: id %q already in use by another peer", m.Src)
		} else {
			log.Printf("peerjs: signaling error: %s", payload.Msg)
		}
	}
	return nil
}

// handleOffer handles a peer-initiated connection: creates an answerer Connection and replies with an ANSWER.
func (p *Peer) handleOffer(m Message) {
	var payload OfferPayload
	if err := json.Unmarshal(m.Payload, &payload); err != nil {
		return
	}
	if payload.Type != ConnData || payload.SDP == nil {
		return
	}

	p.mu.Lock()
	old := p.conns[payload.ConnectionID]
	servers := p.iceServers
	onConn := p.onConn
	p.mu.Unlock()

	if old != nil {
		// Must do a full Close (closeOnce is idempotent): just closing pc would leak — the old connection remains in
		// the conns map, done never closes (connectLoop blocks forever), onClose does not fire
		// (upper-layer pending requests hang until timeout).
		// Note: must be called after p.mu is unlocked — Close→forgetConnection needs the same lock;
		// calling while holding the lock would deadlock (Go mutex is not reentrant).
		old.Close()
	}

	conn, err := p.newConnection(m.Src, payload.Label, false, servers, payload.ConnectionID)
	if err != nil {
		return
	}
	// newConnection has already registered conn; set the remote SDP before replying with ANSWER.
	if err := conn.handleOffer(payload.SDP); err != nil {
		conn.Close()
		return
	}
	if onConn != nil {
		onConn(conn)
	}
}

func (p *Peer) handleLeave(m Message) {
	// Collect then unlock before Close: Close→forgetConnection needs p.mu; calling while holding the lock deadlocks.
	p.mu.Lock()
	var toClose []*Connection
	for _, c := range p.conns {
		if c.PeerID == m.Src {
			toClose = append(toClose, c)
		}
	}
	p.mu.Unlock()
	for _, c := range toClose {
		c.Close()
	}
}

// registerConnection registers a WebRTC connection.
func (p *Peer) registerConnection(c *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns[c.ID] = c
}

// forgetConnection deregisters a WebRTC connection.
func (p *Peer) forgetConnection(connectionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, connectionID)
}

// payloadConnectionID extracts connectionId from the message payload.
func payloadConnectionID(m Message) string {
	var probe struct {
		ConnectionID string `json:"connectionId"`
	}
	if len(m.Payload) == 0 {
		return ""
	}
	if err := json.Unmarshal(m.Payload, &probe); err != nil {
		return ""
	}
	return probe.ConnectionID
}

// signallingFromOptions maps a peer-level Options to the transport-level one.
// ICEServers is dropped: it is a WebRTC-layer concern (pion) that the signalling
// package deliberately does not import.
func signallingFromOptions(o Options) signalling.Options {
	return signalling.Options{
		Host:         o.Host,
		Port:         o.Port,
		Secure:       o.Secure,
		Path:         o.Path,
		Key:          o.Key,
		Token:        o.Token,
		PingInterval: o.PingInterval,
	}
}

// normalizeOptions fills in default values. The defaults themselves live in the
// signalling package (single source of truth); the result is copied back so the
// stored options match what the transport actually dials with.
func normalizeOptions(opts Options) Options {
	s := signalling.NormalizeOptions(signallingFromOptions(opts))
	opts.Host, opts.Port, opts.Secure = s.Host, s.Port, s.Secure
	opts.Path, opts.Key, opts.PingInterval = s.Path, s.Key, s.PingInterval
	opts.Token = s.Token
	return opts
}
