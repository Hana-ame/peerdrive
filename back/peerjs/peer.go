package peerjs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Hana-ame/go-signalframe"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// version mimics peerjs-client's version query parameter.
const version = "1.5.4"

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
	opts       Options
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
	return Options{
		Host:         "peersignal.moonchan.xyz",
		Port:         "443",
		Secure:       true,
		Path:         "/",
		Key:          "pd-signal-1edf5e05e4a52b7351392574",
		PingInterval: 5 * time.Second,
	}
}

// NewPeer creates a PeerJS signaling client.
// If id is empty, the server assigns a random ID; if token is empty, one is randomly generated.
func NewPeer(id string, opts Options) *Peer {
	opts = normalizeOptions(opts)
	p := &Peer{
		opts:   opts,
		conns:  make(map[string]*Connection),
		closed: make(chan struct{}),
	}
	p.iceServers = opts.ICEServers // Options injects ICE servers (SetICEServers has been merged into Options)
	p.signaller = newPeerJSSignaller(id, opts, p.route)
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

// validID validates PeerJS ID rules: first and last characters must be alphanumeric; the middle may contain - _ space.
func validID(id string) bool {
	if len(id) < 1 || len(id) > 256 {
		return false
	}
	isAlnum := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	if !isAlnum(id[0]) || !isAlnum(id[len(id)-1]) {
		return false
	}
	for i := 1; i < len(id)-1; i++ {
		c := id[i]
		if !isAlnum(c) && c != '-' && c != '_' && c != ' ' {
			return false
		}
	}
	return true
}

// randomToken generates a random token (alphanumeric, mimicking peerjs util.randomToken).
func randomToken() string {
	return randHex(16)
}

// randHex generates n bytes of random hex.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// normalizeOptions fills in default values.
func normalizeOptions(opts Options) Options {
	if opts.Port == "" {
		opts.Port = "443"
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	if opts.Key == "" {
		opts.Key = "peerjs"
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = 5 * time.Second
	}
	if opts.Token == "" {
		opts.Token = randomToken()
	}
	return opts
}

// peerJSSignaller is the PeerJS public cloud implementation of Signaller.
type peerJSSignaller struct {
	id    string
	token string
	opts  Options
	route MessageHandler

	mu        sync.Mutex
	conn      *websocket.Conn
	connected bool
	closed    chan struct{}
	done      chan struct{} // H7: signaling disconnect notification (closed when readLoop exits due to network error/EOF)
	doneOnce  sync.Once

	// sender serializes writes to the signaling socket: gorilla/websocket does
	// not allow concurrent writes, so the Sender's lock guards WriteJSON.
	// Concurrent sends from multiple goroutines (heartbeat/ICE candidates/ANSWER)
	// used to be serialized by a local writeMu — moved into the shared
	// signalframe module (see signalframe package docs). The writer is attached
	// on dial and detached on Close / readLoop exit; Send maps the module's
	// ErrNotConnected back to the historical "peerjs: not connected" text.
	sender *signalframe.Sender
}

func newPeerJSSignaller(id string, opts Options, route MessageHandler) Signaller {
	if opts.Token == "" {
		opts.Token = randomToken()
	}
	return &peerJSSignaller{
		id:     id,
		token:  opts.Token,
		opts:   opts,
		route:  route,
		closed: make(chan struct{}),
		done:   make(chan struct{}),
		sender: signalframe.NewSender(nil, 15*time.Second),
	}
}

// ID returns the node ID.
func (s *peerJSSignaller) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Done returns the signaling disconnect notification (H7).
func (s *peerJSSignaller) Done() <-chan struct{} { return s.done }

// Dial registers with and connects to the signaling server. If an ID is specified, connects to WS directly; otherwise retrieves a random ID first.
func (s *peerJSSignaller) Dial(ctx context.Context) error {
	s.mu.Lock()
	if s.id == "" {
		id, err := s.retrieveID(ctx)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("peerjs: retrieve id: %w", err)
		}
		s.id = id
	}
	s.mu.Unlock()
	return s.dialWS(ctx)
}

func (s *peerJSSignaller) dialWS(ctx context.Context) error {
	scheme := "ws"
	if s.opts.Secure {
		scheme = "wss"
	}
	path := s.opts.Path
	if path[len(path)-1] != '/' {
		path += "/"
	}
	u := url.URL{
		Scheme: scheme,
		Host:   s.opts.Host + ":" + s.opts.Port,
		Path:   path + "peerjs",
		RawQuery: "key=" + url.QueryEscape(s.opts.Key) +
			"&id=" + url.QueryEscape(s.id) +
			"&token=" + url.QueryEscape(s.token) +
			"&version=" + version,
	}

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("peerjs: dial %s: %w", u.Host, err)
	}

	s.mu.Lock()
	s.conn = conn
	s.connected = true
	s.mu.Unlock()
	s.sender.SetWriter(conn)

	go s.readLoop(conn)
	go s.heartbeatLoop()
	return nil
}

// readLoop reads server messages and dispatches them to route.
func (s *peerJSSignaller) readLoop(conn *websocket.Conn) {
	// M7: signaling messages (SDP/ICE text) are small; a 1MB limit is sufficient for legitimate payloads;
	// if the cloud signaling is compromised and sends oversized frames, no limit would cause OOM.
	conn.SetReadLimit(1 << 20)
	defer func() {
		s.mu.Lock()
		matches := s.conn == conn
		if matches {
			s.conn = nil
			s.connected = false
		}
		s.mu.Unlock()
		if matches {
			s.sender.SetWriter(nil)
		}
		_ = conn.Close()
		if matches {
			// H7: non-active close (readLoop exits due to network error/EOF, conn is still the current connection)
			// → notify the upper layer to trigger reconnect. An active Close() sets s.conn=nil before closing conn;
			// this check does not match → done is not closed, and startLoop's ctx/closed branch handles cleanup
			// (avoiding spurious reconnect loop after Close)
			s.doneOnce.Do(func() { close(s.done) })
		}
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if s.route != nil {
			_ = s.route(m)
		}
	}
}

// heartbeatLoop mimics peerjs-client, sending a HEARTBEAT every PingInterval for keep-alive.
func (s *peerJSSignaller) heartbeatLoop() {
	t := time.NewTicker(s.opts.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := s.Send(NewMessage(MsgHeartbeat, "", nil)); err != nil {
				// H7: signaling already disconnected (readLoop exited → done closed, reconnect loop takes over);
				// heartbeat spinning is meaningless, exit and wait for the next round
				return
			}
		case <-s.closed:
			return
		case <-s.done:
			return
		}
	}
}

// Send sends a message to the signaling server (the server overwrites src with the client's id).
// Serialization + write deadline + concurrent-write guard live in signalframe.Sender;
// ErrNotConnected is mapped back to the historical "peerjs: not connected" text.
func (s *peerJSSignaller) Send(m Message) error {
	if err := s.sender.Send(m); err != nil {
		if errors.Is(err, signalframe.ErrNotConnected) {
			return fmt.Errorf("peerjs: not connected")
		}
		return err
	}
	return nil
}

// OnMessage registers a signaling message callback (injected internally by the framework; users do not need to call it).
func (s *peerJSSignaller) OnMessage(h MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.route = h
}

// Close closes the signaling connection.
func (s *peerJSSignaller) Close() error {
	s.mu.Lock()
	if s.conn == nil {
		s.mu.Unlock()
		return nil
	}
	conn := s.conn
	s.conn = nil
	s.connected = false
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	s.mu.Unlock()
	s.sender.SetWriter(nil)
	return conn.Close()
}

// retrieveID obtains a server-assigned random ID via HTTP.
func (s *peerJSSignaller) retrieveID(ctx context.Context) (string, error) {
	scheme := "http"
	if s.opts.Secure {
		scheme = "https"
	}
	path := s.opts.Path
	if path[len(path)-1] != '/' {
		path += "/"
	}
	u := fmt.Sprintf("%s://%s:%s%sid?ts=%d%d&version=%s",
		scheme, s.opts.Host, s.opts.Port, path,
		time.Now().UnixMilli(), time.Now().Nanosecond()%100000, version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !validID(id) {
		return "", fmt.Errorf("server returned invalid id %q", id)
	}
	return id, nil
}
