package signalling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Hana-ame/go-signalframe"
	"github.com/gorilla/websocket"
)

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

// NewPeerJSSignaller creates a PeerJS-protocol Signaller.
//
// id takes precedence over any ID in opts: an empty id makes Dial retrieve one
// from the server over HTTP, a non-empty id dials the socket directly. route
// receives every frame read from the socket; pass nil to discard them.
func NewPeerJSSignaller(id string, opts Options, route MessageHandler) Signaller {
	// NormalizeOptions is idempotent: NewPeer has already normalized the
	// options, so calling it again only guards direct construction.
	opts = NormalizeOptions(opts)
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
			if err := s.Send(signalframe.NewMessage(signalframe.MsgHeartbeat, "", nil)); err != nil {
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
	if !ValidID(id) {
		return "", fmt.Errorf("server returned invalid id %q", id)
	}
	return id, nil
}
