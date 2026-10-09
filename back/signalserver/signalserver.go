// Package signalserver is a self-hosted PeerJS signaling server (compatible with the peerjs-server protocol subset)
// + built-in room discovery (replaces public signaling + public MQTT brokers).
//
// Responsibilities:
//  1. Signaling: node registration (WS + token), OFFER/ANSWER/CANDIDATE/LEAVE forwarding by dst,
//     queuing when dst is offline (with expiry), heartbeat keep-alive, ID allocation
//  2. Discovery: nodes announce the collections they follow, `GET /discover/nodes?coll=` queries online nodes
//     ——after self-hosting, the server naturally knows all online nodes, no longer needing MQTT broadcast
//
// Protocol details aligned with peers/peerjs-server (src/services/webSocketServer, messageHandler):
//   - WS URL: /{path}peerjs?key=&id=&token=
//   - Messages {type, src, dst, payload}, server overwrites src
//   - Forward when dst is online; queue when offline (LEAVE/EXPIRE not queued)
//   - OPEN / ID-TAKEN / ERROR control messages
//   - Client sends HEARTBEAT every 5s for keep-alive
package signalserver

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hana-ame/go-peerserver/tracker"
)

type Server struct {
	key            string
	opsToken       string
	path           string
	queueTTL       time.Duration   // offline queue TTL (used for OFFER expiry)
	heartbeatTTL   time.Duration   // discovery heartbeat expiry time
	tokenWhitelist map[string]bool // allowed signaling tokens (nil/empty = unrestricted)
	// corsOrigins is the explicit CORS allow-list (nil/empty = wildcard "*", the historical behavior).
	// See WithCORSOrigins and allowCORS for why the default stays permissive.
	corsOrigins []string

	// Rate limiting (2026-10-06, N3). Three separate buckets because these
	// endpoints differ hugely in cost and blast radius — see WithRateLimit for
	// the per-bucket reasoning. All three nil = not configured, which preserves
	// the pre-2026-10-06 behavior; that keeps this package usable as a generic
	// PeerJS-compatible server for deployments that already limit at the proxy.
	announceLim *tokenBucket // POST /discover/announce — unauthenticated, writes the discovery roster
	wsLim      *tokenBucket // /peerjs upgrade — each accepted conn costs a goroutine + readLoop + a clients-table entry
	idLim      *tokenBucket // GET /peerjs/id — the panel's first call; must not be throttled by the WS bucket

	startedAt time.Time // server startup time
	msgCount  int64     // total forwarded message count (atomic access)

	mu        sync.Mutex
	clients   map[string]*client              // id → online connection
	queues    map[string][]queuedMsg          // dst → messages pending forwarding
	disc      map[string]map[string]time.Time // collection → peerId → lastSeen
	peerLinks map[string]map[string]time.Time // peerId → neighbor peerId → lastSeen (for graph)
	peerColls map[string][]string             // peerId -> collections
	peerStats map[string]*PeerStats           // peerId -> stats

	// tracker is an optional BitTorrent HTTP tracker server (BEP 12/31).
	// When set, /announce, /scrape, and /tracker/bans are served alongside
	// the signaling endpoints. See WithTracker in config.go.
	tracker *tracker.Tracker
}

// NewServer creates a signaling server.
func NewServer(key string, opts ...Option) *Server {
	if key == "" {
		key = "peerjs"
	}
	s := &Server{
		key:          key,
		path:         "",
		queueTTL:     30 * time.Second,
		heartbeatTTL: 90 * time.Second,
		startedAt:    time.Now(),
		clients:      make(map[string]*client),
		queues:       make(map[string][]queuedMsg),
		disc:         make(map[string]map[string]time.Time),
		peerLinks:    make(map[string]map[string]time.Time),
		peerColls:    make(map[string][]string),
		peerStats:    make(map[string]*PeerStats),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Offline queue limit (H3 fix): each dst caches at most maxQueuedPerDst messages.
// Pitfall: the original implementation had no limit—when dst never connects, the queue grows unbounded (each malicious client can send OFFERs to any random ID
// to exhaust server memory). When the limit is exceeded, drop the oldest (signaling messages expire anyway, so dropping old is more reasonable than dropping new).
const maxQueuedPerDst = 100

// Start launches a background sweeper: periodically cleans up expired queue entries and empty queues.
// Background: expiry cleanup was originally only done in flushQueue (when dst comes online); if dst never connects, expired messages accumulate.
func (s *Server) Start() {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			s.sweepQueues()
			s.sweepDiscovery()
		}
	}()
}

func (s *Server) sweepQueues() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for dst, q := range s.queues {
		kept := q[:0]
		for _, qm := range q {
			if qm.expire.After(now) && s.clients[dst] == nil {
				kept = append(kept, qm)
			}
		}
		if len(kept) == 0 {
			delete(s.queues, dst)
		} else {
			s.queues[dst] = kept
		}
	}
}

// sweepDiscovery periodically cleans up discovery nodes with expired heartbeats, and synchronously cleans up graph/metadata to prevent memory and graph residue over long runs.
func (s *Server) sweepDiscovery() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-s.heartbeatTTL)

	active := make(map[string]bool)
	for coll, peers := range s.disc {
		for id, last := range peers {
			if last.Before(cutoff) {
				delete(peers, id)
				continue
			}
			active[id] = true
		}
		if len(peers) == 0 {
			delete(s.disc, coll)
		}
	}

	// Clean up graph origins of nodes that are no longer active
	for id := range s.peerLinks {
		if !active[id] {
			delete(s.peerLinks, id)
		}
	}
	// Clean up edges pointing to nodes that are no longer active
	for _, links := range s.peerLinks {
		for nid := range links {
			if !active[nid] {
				delete(links, nid)
			}
		}
	}
	// Clean up stats/collections of nodes that are no longer active
	for id := range s.peerStats {
		if !active[id] {
			delete(s.peerStats, id)
		}
	}
	for id := range s.peerColls {
		if !active[id] {
			delete(s.peerColls, id)
		}
	}
}

// route routes messages: forward if dst is online, queue if offline (except LEAVE/EXPIRE).
//
// Two fixes aligned with peers/peerjs-server (src/messageHandler/handlers/transmission):
//
//  1. send failures are no longer silently dropped. The old implementation `_ = dst.send(m)`: when the target socket is half-open but still
//     hasn't been removed from the clients table yet (peer crashed, NAT mapping expired, connection half-open without FIN),
//     OFFER/ANSWER/CANDIDATE messages are swallowed, leaving the initiator stuck waiting for handshake forever. Here we remove the dead connection
//     and send a supplementary LEAVE to the initiator so it stops retrying.
//  2. Release s.mu before send. WriteJSON inside send has a 10s write timeout; a slow/dead client
//     would hold the lock for 10s and freeze the entire signaling server's routing. So we unlock before writing.
func (s *Server) route(m Message) {
	s.mu.Lock()
	dst := s.clients[m.Dst]
	if dst != nil {
		s.mu.Unlock() // unlock before writing, to avoid blocking on s.mu held by a slow client
		if err := dst.send(m); err == nil {
			atomic.AddInt64(&s.msgCount, 1)
			return
		}
		s.handleDeadDst(dst, m)
		return
	}
	s.mu.Unlock()

	if m.Type == "LEAVE" || m.Type == "EXPIRE" || m.Dst == "" {
		return
	}
	// Enqueue: replay after the target comes online (OFFER/ANSWER/CANDIDATE)
	// H3: unbounded queue → OOM. When exceeding maxQueuedPerDst, drop the oldest.
	s.mu.Lock()
	defer s.mu.Unlock()
	q := append(s.queues[m.Dst], queuedMsg{msg: m, expire: time.Now().Add(s.queueTTL)})
	if len(q) > maxQueuedPerDst {
		q = q[len(q)-maxQueuedPerDst:]
	}
	s.queues[m.Dst] = q
	atomic.AddInt64(&s.msgCount, 1)
}

// handleDeadDst handles a target that's "in the clients table but failed to send": remove the entry, close the connection,
// broadcast LEAVE.
func (s *Server) handleDeadDst(dst *client, m Message) {
	s.mu.Lock()
	if cur, ok := s.clients[m.Dst]; !ok || cur != dst {
		s.mu.Unlock()
		return // already removed/replaced by another flow, leave cleanup to that process
	}
	delete(s.clients, m.Dst)
	var victims []*client
	for _, c := range s.clients {
		if c != dst {
			victims = append(victims, c)
		}
	}
	// Clean up the dead connection's residue in discovery/graph/metadata (removeClient won't come back to clean up since the client was already removed)
	for coll, peers := range s.disc {
		delete(peers, m.Dst)
		if len(peers) == 0 {
			delete(s.disc, coll)
		}
	}
	delete(s.peerLinks, m.Dst)
	for _, links := range s.peerLinks {
		delete(links, m.Dst)
	}
	delete(s.peerStats, m.Dst)
	delete(s.peerColls, m.Dst)
	s.mu.Unlock()

	dst.closeConn()
	leave := Message{Type: "LEAVE", Src: m.Dst}
	for _, c := range victims {
		_ = c.send(leave)
	}
	if m.Src == "" || m.Src == m.Dst {
		return
	}
	s.mu.Lock()
	src := s.clients[m.Src]
	s.mu.Unlock()
	if src != nil {
		_ = src.send(Message{Type: "LEAVE", Src: m.Dst, Dst: m.Src})
	}
}

// flushQueue replays the offline queue after a client comes online (with expiry cleanup).
func (s *Server) flushQueue(cl *client) {
	s.mu.Lock()
	q := s.queues[cl.id]
	delete(s.queues, cl.id)
	s.mu.Unlock()
	now := time.Now()
	for _, qm := range q {
		if qm.expire.After(now) {
			_ = cl.send(qm.msg)
		}
	}
}

// removeClient handles disconnect cleanup: notify other nodes with LEAVE, delete discovery records.
func (s *Server) removeClient(cl *client) {
	s.mu.Lock()
	if s.clients[cl.id] != cl {
		s.mu.Unlock()
		return
	}
	delete(s.clients, cl.id)
	leave := Message{Type: "LEAVE", Src: cl.id}
	var victims []*client
	for _, c := range s.clients {
		victims = append(victims, c)
	}
	for coll, peers := range s.disc {
		delete(peers, cl.id)
		if len(peers) == 0 {
			delete(s.disc, coll)
		}
	}
	delete(s.peerLinks, cl.id)
	for _, links := range s.peerLinks {
		delete(links, cl.id)
	}
	delete(s.peerStats, cl.id)
	delete(s.peerColls, cl.id)
	s.mu.Unlock()
	for _, c := range victims {
		_ = c.send(leave)
	}
}

// reservedPeerIDs are id strings that must never be registered by a signaling client
// or announced to the discovery table. They are reserved for internal use (e.g. the
// local WS management session in back/internal/transport/ws_session.go uses id "local").
// Audit A-9/A-12 (2026-10-06): before this check, an attacker could register with
// ?id=local on the signaling WS, and the server would overwrite message Src with that
// id (readLoop:476), letting a spoofed "local" id pass the admin/PSK gate.
var reservedPeerIDs = map[string]bool{
	"local": true, // WSSession management channel id (ws_session.go:81)
}

// maxPeerIDLen caps the length of a peer id to prevent memory abuse via extremely long
// self-reported identifiers (announced ids become dictionary keys in disc/peerStats/etc.).
const maxPeerIDLen = 128

// validPeerID reports whether id is usable as a signaling registration or discovery
// peer id. It rejects empty, too-long, and reserved ids.
func validPeerID(id string) bool {
	if id == "" {
		return false
	}
	if len(id) > maxPeerIDLen {
		return false
	}
	if reservedPeerIDs[id] {
		return false
	}
	return true
}
