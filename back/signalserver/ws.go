// Package signalserver — WebSocket transport layer.
//
// This file owns everything that touches the gorilla/websocket connection:
// the upgrade handshake, the per-connection read loop, the client write
// wrapper, and the random-ID generator that the panel calls before connecting.
// Message routing (route / handleDeadDst / flushQueue / removeClient) stays
// in signalserver.go because it mutates the Server's shared state under s.mu.
package signalserver

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Hana-ame/go-signalframe"
	"github.com/gorilla/websocket"
)

// client is an online signaling connection.
type client struct {
	id     string
	token  string
	conn   *websocket.Conn
	sender *signalframe.Sender // serializes writes; owns the write deadline (10s)
	last   time.Time           // last heartbeat
	done   chan struct{}       // closed when the connection is torn down; signals pingTicker to exit
}

// queuedMsg is an offline queue entry (with expiry time set at enqueue time).
type queuedMsg struct {
	msg    Message
	expire time.Time
}

// Message is a signaling message (consistent with the peerjs client protocol).
// Alias of signalframe.Message — the single wire-format definition shared with
// the peerjs client (back/peerjs). Previously this package kept its own copy;
// any drift between the two endpoints would silently break OFFER/ANSWER relay.
type Message = signalframe.Message

// HandleID GET /{path}{key}/id → random id (peerjs API compatible).
func (s *Server) HandleID(w http.ResponseWriter, r *http.Request) {
	if s.handleCORS(w, r) {
		return
	}
	// Checked after handleCORS so a CORS preflight is never charged for a bucket
	// token — preflights carry no work and blocking them would break the panel
	// with a CORS error rather than a readable 429.
	if !s.idLim.allow(clientIP(r)) {
		rateLimited(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, randomID())
}

// HandleWS handles signaling WebSocket upgrade and the message loop.
// Path is shaped like /{path}peerjs?key=&id=&token= ({path} is provided when mounted via gin routing).
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	// Rate limit first (2026-10-06, N3). Placed before the key/token checks and
	// before the upgrade because the upgrade is the expensive part: it commits a
	// goroutine, a readLoop and a clients-table entry. Note this also means a
	// client with a *wrong key* still consumes a token — which is the point:
	// key-guessing is exactly the abuse this bounds.
	// (Rejected by rate limit, the caller gets an HTTP 429 rather than a WS
	// close frame; that is deliberate — before the upgrade nothing has been
	// written, so a real status code is still possible and is far more debuggable
	// than a silent disconnect.)
	if !s.wsLim.allow(clientIP(r)) {
		rateLimited(w)
		return
	}
	q := r.URL.Query()
	id, token, key := q.Get("id"), q.Get("token"), q.Get("key")
	if id == "" || token == "" || key == "" {
		s.wsError(w, "No id, token, or key supplied to websocket server")
		return
	}
	if key != s.key {
		s.wsError(w, "Invalid key provided")
		return
	}
	// Token whitelist: empty list = unrestricted (default); if non-empty, token must be on the list.
	// See the pitfall note in WithTokenWhitelist (any token can impersonate a node to receive signaling).
	if len(s.tokenWhitelist) > 0 && !s.tokenWhitelist[token] {
		s.wsError(w, "Invalid token provided")
		return
	}
	// 2026-10-06 security fix (audit A-12): validate the registered id before accepting
	// the connection. The server overwrites message Src with cl.id (readLoop:476), so an
	// unvalidated id that passes here will be used as the source identity for every
	// message. Before this check, an attacker could register with ?id=local and have the
	// server forward messages with Src="local", which the transport side (admin.go,
	// psk.go) used to treat as the local management session.
	if !validPeerID(id) {
		s.wsError(w, "Invalid id provided")
		return
	}

	upgrader := websocket.Upgrader{
		// Red-team R2: was hard-coded to true, which left the signalling socket
		// outside the WithCORSOrigins allow-list that the REST endpoints honor.
		// Empty allow-list still accepts everything, so nothing changes unless an
		// operator opts in.
		CheckOrigin: s.originAllowed,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// Read limit: signaling messages are small (SDP/ICE payloads), 40KB is sufficient; prevents malicious clients from stuffing huge payloads.
	// Also set a 60s read timeout as a fallback—readLoop refreshes it via HEARTBEAT, so disconnected clients don't hold resources.
	conn.SetReadLimit(40 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// RFC 6455 Ping/Pong (2026-10-14, P0-1 refactor): the server now sends
	// protocol-level pings proactively (pingTicker below), and refreshes the
	// read deadline on any incoming pong. This makes keep-alive a WS protocol
	// property instead of an application-layer text-frame dance (HEARTBEAT),
	// so clients that don't implement the peerjs heartbeat loop still stay
	// alive as long as their stack answers pings — which every conforming
	// WebSocket implementation (browsers, `ws`, gorilla) does at the protocol
	// layer.
	//
	// HEARTBEAT text frames are still accepted by readLoop below and still
	// refresh the deadline, so old peerjs clients keep working unchanged.
	// The two mechanisms are complementary: protocol-level ping/pong is the
	// primary keep-alive, and any JSON message (including HEARTBEAT) is a
	// secondary keep-alive signal.
	//
	// PingHandler is deliberately NOT overridden: gorilla/websocket's default
	// auto-pongs incoming pings (RFC 6455 §5.5.2 requires a Pong reply), and
	// that path doesn't need a deadline refresh because a client that sends
	// pings is by definition alive and will also auto-pong our proactive pings.
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	s.mu.Lock()
	// ID occupancy: if token matches, reuse the connection; otherwise reject
	if existing, ok := s.clients[id]; ok {
		if existing.token != token {
			s.mu.Unlock()
			_ = conn.WriteJSON(Message{Type: "ID-TAKEN", Payload: raw(`{"msg":"ID is taken"}`)})
			_ = conn.Close()
			return
		}
		existing.closeConn()
	}
	cl := &client{
		id:     id,
		token:  token,
		conn:   conn,
		sender: signalframe.NewSender(conn, 10 * time.Second),
		last:   time.Now(),
		done:   make(chan struct{}),
	}
	s.clients[id] = cl
	s.mu.Unlock()

	_ = cl.send(Message{Type: "OPEN"})
	s.flushQueue(cl)

	// Start pingTicker in the background: proactively send WS Ping control
	// frames so a live client auto-pongs and keeps the read deadline fresh
	// (see PongHandler above). The loop exits when cl.done is closed, which
	// happens in closeConn (called by readLoop's defer on any read error,
	// or by the takeover path above when a new connection reuses the id).
	go s.pingTicker(cl)
	go s.readLoop(cl)
}

// readLoop reads client messages and routes them.
func (s *Server) readLoop(cl *client) {
	defer func() {
		s.removeClient(cl)
		cl.closeConn()
	}()
	for {
		var m Message
		if err := cl.conn.ReadJSON(&m); err != nil {
			return
		}
		m.Src = cl.id // server overwrites src
		s.mu.Lock()
		cl.last = time.Now()
		// Receiving a message counts as active; extend the read timeout (paired with SetReadDeadline in HandleWS)
		_ = cl.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		s.mu.Unlock()
		s.route(m)
	}
}

func (cl *client) send(m Message) error {
	// Serialization + write deadline moved into signalframe.Sender: gorilla does
	// not allow concurrent writes, and a stuck socket must not hold the relay
	// for longer than the deadline (same 10s as before the move).
	return cl.sender.Send(m)
}

// pingTicker proactively sends WS Ping control frames on a fixed interval
// (RFC 6455, §5.5.2). A conforming WebSocket client (browsers, `ws`, gorilla)
// auto-pongs at the protocol layer, and the PongHandler set in HandleWS
// refreshes the read deadline on each pong. If the client stops responding
// (crash, NAT mapping expired, half-open connection), the read deadline is
// never refreshed and ReadJSON in readLoop times out, causing the connection
// to be torn down — the server no longer needs the peerjs-style application
// HEARTBEAT text frame to detect liveness.
//
// The interval is read from s.pingInterval (set via WithPingInterval, default
// 20s). Making it a per-Server field (not a global) eliminates data races
// when multiple tests run in parallel with different intervals.
//
// The goroutine exits cleanly when cl.done is closed (see closeConn), so
// there is no goroutine leak across the takeover path (a new connection
// reusing the same id triggers closeConn on the old one).
func (s *Server) pingTicker(cl *client) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cl.done:
			return
		case t := <-ticker.C:
			if err := cl.conn.WriteControl(websocket.PingMessage, nil, t.Add(10*time.Second)); err != nil {
				// Write failure: the socket is dead. closeConn has already
				// been called (or will be called by readLoop's defer on the
				// next ReadJSON error); nothing to do here.
				return
			}
		}
	}
}

func (cl *client) closeConn() {
	// Close done first so pingTicker exits promptly without waiting for the
	// next ping tick (up to 20s otherwise); sender.Close() is idempotent
	// (see signalframe.Sender.Close), so the two close paths from readLoop's
	// defer and the takeover path in HandleWS are safe.
	select {
	case <-cl.done:
		// already closed
	default:
		close(cl.done)
	}
	_ = cl.sender.Close()
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// randomID generates an alphanumeric id conforming to PeerJS rules.
func randomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[b[i]%byte(len(chars))]
	}
	return string(b)
}

// wsError handles upgrade failure (HTTP layer).
func (s *Server) wsError(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}
