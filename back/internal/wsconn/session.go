package wsconn

import (
	"sync"
	"time"
)

// Tunable knobs, with the default values the original inline literals used.
// New() applies a default for every zero field, so an Options{} is
// byte-for-byte the old behavior.
type Options struct {
	// ReadLimit caps a single inbound message. 64KB data chunk + 2x headroom.
	// Without it an oversized frame from a faulty client consumed unbounded
	// memory (M5 fix).
	ReadLimit int64
	// ReadDeadline is refreshed by the pong handler; expiry means the peer
	// is gone.
	ReadDeadline time.Duration
	// WriteDeadline bounds a single write. This is the only backpressure a
	// WebSocket session has — see the backpressure note in doc.go.
	WriteDeadline time.Duration
	// PingInterval is how often heartbeatLoop writes a ping.
	PingInterval time.Duration
	// PingTimeout bounds the ping write itself, not the round trip.
	PingTimeout time.Duration
}

// defaults reproduces the historical inline constants verbatim. Keep them in
// sync with doc/REFACTOR.md's frame-protocol section if they ever change.
func defaults() Options {
	return Options{
		ReadLimit:      3 * 64 * 1024,
		ReadDeadline:   90 * time.Second,
		WriteDeadline:  15 * time.Second,
		PingInterval:   30 * time.Second,
		PingTimeout:    10 * time.Second,
	}
}

// Session is one WebSocket connection adapted to the node's frame protocol.
//
// Semantically identical to the WebRTC DataChannel sessions: the browser uses
// the same req/meta/data/done/err frames locally over WS and remotely over
// WebRTC, so the frontend needs only one codec.
//
// All methods are safe for concurrent use.
type Session struct {
	id     string
	conn   Conn
	opts   Options
	sendMu sync.Mutex // gorilla does not permit concurrent writes

	onMessage func(Frame)
	onClose   func()
	closeOnce sync.Once
}

// New wraps an already-upgraded socket and starts the read loop and the
// heartbeat. It never returns an error: a socket that cannot be wrapped has
// already failed the handshake, which is the Upgrader's concern.
func New(id string, conn Conn, opts Options) *Session {
	o := defaults()
	if opts.ReadLimit == 0 {
		opts.ReadLimit = o.ReadLimit
	}
	if opts.ReadDeadline == 0 {
		opts.ReadDeadline = o.ReadDeadline
	}
	if opts.WriteDeadline == 0 {
		opts.WriteDeadline = o.WriteDeadline
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = o.PingInterval
	}
	if opts.PingTimeout == 0 {
		opts.PingTimeout = o.PingTimeout
	}

	conn.SetReadLimit(opts.ReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(opts.ReadDeadline))
	// The pong handler renews the read deadline: the browser answers a ping
	// with a pong automatically, so a live peer refreshes the deadline by
	// protocol and no separate read timer is needed.
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(opts.ReadDeadline))
		return nil
	})

	s := &Session{id: id, conn: conn, opts: opts}
	go s.readLoop()
	go s.heartbeatLoop()
	return s
}

// heartbeatLoop keeps the connection alive and, more importantly, detects a
// dead peer. A peer that stops answering pings fails the read deadline inside
// readLoop, which exits and closes the session. If the ping write itself
// fails the connection is already gone, so exit immediately without waiting
// for the deadline.
//
// Note the loop is not cancelled on Close: it observes the closed socket
// through the ping error and exits on the next tick. That is the original
// behavior and it is deliberate — the write deadline on a dead socket fires
// quickly, and adding a separate cancellation channel would make the loop's
// exit observable through a different path.
func (s *Session) heartbeatLoop() {
	t := time.NewTicker(s.opts.PingInterval)
	defer t.Stop()
	for range t.C {
		s.sendMu.Lock()
		err := s.conn.WriteControl(OpcodePing, nil, time.Now().Add(s.opts.PingTimeout))
		s.sendMu.Unlock()
		if err != nil {
			return
		}
	}
}

// ID is the session identifier. Local WebSocket sessions register under the
// literal "local" so that multiple browser tabs can coexist without
// deduplication (see PeerJSService.dedupConn).
func (s *Session) ID() string { return s.id }

// SendJSON writes v as one text frame — a JSON control header.
func (s *Session) SendJSON(v any) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.opts.WriteDeadline))
	return s.conn.WriteJSON(v)
}

// SendFrame writes header and body as two consecutive frames (text then
// binary). The pair must be atomic — the receiver reads a text header and
// then exactly one binary body — so both writes hold the same lock and share
// one error return. A failing header write returns immediately; a failing
// body write returns after the header is already on the wire, which is the
// same behavior as the original implementation.
func (s *Session) SendFrame(header any, body []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.opts.WriteDeadline))
	if err := s.conn.WriteJSON(header); err != nil {
		return err
	}
	if len(body) > 0 {
		_ = s.conn.SetWriteDeadline(time.Now().Add(s.opts.WriteDeadline))
		return s.conn.WriteMessage(OpcodeBinary, body)
	}
	return nil
}

// OnMessage registers the inbound callback. Registered under sendMu because
// it is read from readLoop, which needs a synchronized handoff.
func (s *Session) OnMessage(f func(Frame)) {
	s.sendMu.Lock()
	s.onMessage = f
	s.sendMu.Unlock()
}

// OnClose registers the teardown callback. Fired exactly once, after the
// socket is closed.
func (s *Session) OnClose(f func()) {
	s.sendMu.Lock()
	s.onClose = f
	s.sendMu.Unlock()
}

// Close shuts the session down and fires OnClose exactly once. It is safe to
// call from any goroutine, from inside the OnMessage callback, and from
// readLoop's defer.
//
// The callback runs with sendMu released: calling SendJSON from a close
// callback would otherwise deadlock on the same mutex.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.sendMu.Lock()
		f := s.onClose
		s.onClose = nil
		s.sendMu.Unlock()
		if f != nil {
			f()
		}
	})
}

// readLoop is the inbound scheduler: it reads frames and dispatches them. Any
// read error ends the loop and therefore the session.
func (s *Session) readLoop() {
	defer s.Close()
	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		s.sendMu.Lock()
		f := s.onMessage
		s.sendMu.Unlock()
		if f != nil {
			f(MessageToFrame(mt, data))
		}
	}
}
