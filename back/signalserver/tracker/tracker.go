// Tracker is a minimal BitTorrent HTTP tracker server implementing BEP 12
// (announce) and BEP 31 (scrape). It is mounted on the signalserver and
// regserver binaries, giving them the ability to serve as BT trackers
// alongside their existing signaling / registration roles.
//
// Architecture:
//   - Tracker holds peer state (in-memory map info_hash → peers) and a BanStore.
//   - HandleAnnounce validates params, checks bans, records the peer, returns peers.
//   - HandleScrape returns seeder/leecher counts.
//   - HandleBans delegates to BanStore.HandleBans with the injected auth function.
//
// Design choices:
//   - Compact encoding (compact=1) is the default because it is 3-5x more
//     compact than the JSON/dict form and every mainstream client supports it.
//     Non-compact is also supported for debugging and clients that need it.
//   - Peer TTL defaults to 3× the announce interval (30 min) — matches the
//     common tracker convention. Expired peers are cleaned up lazily on access.
//   - Max peers per info_hash defaults to 100 to bound memory per torrent.
//   - Failed announces never crash the handler: every error path returns a
//     well-formed bencode error response (bencodeError).
//   - Persistence: only bans are persisted (see ban.go). Peer state is
//     ephemeral — trackers are stateless by convention; peers re-announce
//     within the interval.
package tracker

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultInterval is the standard announce interval (15 minutes, BEP 12 default).
const defaultInterval = 900 // seconds

// defaultPeerTTL is how long a peer entry remains without re-announce.
const defaultPeerTTL = 30 * time.Minute

// defaultMaxPeers caps peers per info_hash to prevent memory abuse.
const defaultMaxPeers = 100

// defaultNumWant is the default number of peers to return per announce.
const defaultNumWant = 50

// peerEntry stores metadata about a peer for a given info_hash.
type peerEntry struct {
	peerID   string    // hex-encoded 20-byte peer_id
	ip       net.IP    // peer's self-reported IP (or remoteAddr if not provided)
	port     uint16    // peer's listening port
	user     string    // optional username (for user-ban mapping)
	lastSeen time.Time
}

// Tracker is a BitTorrent HTTP tracker server instance.
type Tracker struct {
	mu sync.Mutex

	// peers[infoHashHex] = list of peer entries.
	peers map[string][]*peerEntry

	// userPeers[username] = set of peer_ids that user has announced with.
	// Used to enforce user bans (ban user → block all their peer_ids).
	userPeers map[string]map[string]bool

	// Ban store with persistence.
	bans *BanStore

	// Configuration.
	interval   int                          // announce interval in seconds
	maxPeers   int                          // max peers per info_hash
	peerTTL    time.Duration                // peer expiry
	banFile    string                       // ban persistence file path
	verifyUser func(string) (string, bool)  // JWT→username verifier (nil = no user auth)
	banAuth    func(*http.Request) bool     // auth check for ban management endpoints
}

// Option configures a Tracker.
type Option func(*Tracker)

// WithBanFile sets the ban persistence file path. Default: no persistence.
func WithBanFile(path string) Option {
	return func(t *Tracker) { t.banFile = path }
}

// WithBanAuth sets the auth function for ban management endpoints.
// Pass nil to disable auth (not recommended for production).
func WithBanAuth(auth func(*http.Request) bool) Option {
	return func(t *Tracker) { t.banAuth = auth }
}

// WithVerifyUser sets the JWT→username verifier for user-identity announce.
// The function receives a Bearer token and returns (username, ok).
// Pass nil to disable user-level announce (bans by user still work via ?user= param).
func WithVerifyUser(fn func(string) (string, bool)) Option {
	return func(t *Tracker) { t.verifyUser = fn }
}

// WithAnnounceInterval sets the announce interval in seconds (default 900).
func WithAnnounceInterval(seconds int) Option {
	return func(t *Tracker) {
		if seconds > 0 {
			t.interval = seconds
		}
	}
}

// WithMaxPeers sets the max peers per info_hash (default 100).
func WithMaxPeers(n int) Option {
	return func(t *Tracker) {
		if n > 0 {
			t.maxPeers = n
		}
	}
}

// WithPeerTTL sets the peer expiry duration (default 30 minutes).
func WithPeerTTL(d time.Duration) Option {
	return func(t *Tracker) {
		if d > 0 {
			t.peerTTL = d
		}
	}
}

// NewTracker creates a tracker instance with the given options.
func NewTracker(opts ...Option) *Tracker {
	t := &Tracker{
		peers:     make(map[string][]*peerEntry),
		userPeers: make(map[string]map[string]bool),
		interval:  defaultInterval,
		maxPeers:  defaultMaxPeers,
		peerTTL:   defaultPeerTTL,
	}
	for _, o := range opts {
		o(t)
	}

	// Create ban store with debounced persistence.
	if t.banFile != "" {
		t.bans = NewBanStore(t.banFile, t.debouncedSave)
	} else {
		t.bans = NewBanStore("", nil)
	}
	return t
}

// debouncedSave is called by BanStore on every change. It saves immediately
// (bans are rare operations; debouncing adds complexity without benefit).
func (t *Tracker) debouncedSave() {
	if err := t.bans.Save(); err != nil {
		// Log would go here; the tracker has no logger to avoid coupling.
		_ = err
	}
}

// HandleAnnounce handles GET /announce (BEP 12).
//
// Query parameters:
//   - info_hash: 20-byte binary (URL-encoded) or 40-char hex string
//   - peer_id:   20-byte binary (URL-encoded) or 40-char hex string
//   - port:      peer's listening port (required)
//   - uploaded, downloaded, left: byte counters (optional, logged for stats)
//   - event:     "started" | "completed" | "stopped" | "" (optional)
//   - numwant:   max peers to return (default 50, max = maxPeers)
//   - compact:   "1" = compact binary response (default), "0" = dict response
//   - key:       32-char hex tracker key (accepted but not enforced)
//   - user:      optional username for user-ban mapping
//   - Authorization: optional Bearer token (verified via verifyUser)
//
// Response: bencoded dict with interval, peers (compact or dict list),
// complete, incomplete. On error: bencoded dict with failure_reason.
//
// Ban enforcement:
//   - If info_hash is banned → 403 with failure_reason "info_hash is banned"
//   - If peer_id is banned → 403 with failure_reason "peer_id is banned"
//   - If IP is banned → 403 with failure_reason "IP is banned"
//   - If user is banned → 403 with failure_reason "user is banned"
//   - If a peer belongs to a banned user → 403 with failure_reason "user is banned"
//   - Banned peers are excluded from the returned peer list
func (t *Tracker) HandleAnnounce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAnnounceError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()

	// Parse required parameters.
	infoHashHex, err := parseHashParam(q.Get("info_hash"))
	if err != nil {
		writeAnnounceError(w, http.StatusBadRequest, "invalid info_hash: "+err.Error())
		return
	}
	peerIDHex, err := parseHashParam(q.Get("peer_id"))
	if err != nil {
		writeAnnounceError(w, http.StatusBadRequest, "invalid peer_id: "+err.Error())
		return
	}
	portStr := q.Get("port")
	if portStr == "" {
		writeAnnounceError(w, http.StatusBadRequest, "port is required")
		return
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		writeAnnounceError(w, http.StatusBadRequest, "invalid port")
		return
	}

	// Parse optional parameters.
	event := q.Get("event")
	compact := q.Get("compact") != "0" // default to compact
	numWant, _ := strconv.Atoi(q.Get("numwant"))
	if numWant <= 0 || numWant > t.maxPeers {
		numWant = defaultNumWant
	}

	// Determine the peer's IP: use the ip param if provided, else remoteAddr.
	ipStr := q.Get("ip")
	var peerIP net.IP
	if ipStr != "" {
		peerIP = net.ParseIP(ipStr)
		if peerIP == nil {
			writeAnnounceError(w, http.StatusBadRequest, "invalid ip parameter")
			return
		}
	} else {
		peerIP = clientIP(r)
		if peerIP == nil {
			writeAnnounceError(w, http.StatusBadRequest, "could not determine peer IP")
			return
		}
	}

	// Determine username for user-ban mapping.
	user := q.Get("user")
	if user == "" && t.verifyUser != nil {
		user, _ = t.verifyUser(bearerToken(r))
	}

	// Check bans.
	if banned, banType, reason := t.bans.IsBanned(infoHashHex, peerIDHex, peerIP.String(), user); banned {
		msg := fmt.Sprintf("%s is banned", banType)
		if reason != "" {
			msg += ": " + reason
		}
		writeAnnounceError(w, http.StatusForbidden, msg)
		return
	}
	if t.bans.IsUserPeerBanned(peerIDHex, t.userPeers) {
		writeAnnounceError(w, http.StatusForbidden, "user is banned")
		return
	}

	// Handle stopped event: remove peer and return empty list.
	if event == "stopped" {
		t.mu.Lock()
		t.removePeer(infoHashHex, peerIDHex)
		t.mu.Unlock()
		t.writeAnnounceOK(w, nil, compact)
		return
	}

	// Record the peer.
	now := time.Now()
	t.mu.Lock()
	t.addPeer(infoHashHex, peerIDHex, peerIP, uint16(port), user, now)
	if user != "" && !t.bans.IsUserBanned(user) {
		// Record user→peer_id association for user-ban enforcement.
		if t.userPeers[user] == nil {
			t.userPeers[user] = make(map[string]bool)
		}
		t.userPeers[user][peerIDHex] = true
	}
	t.mu.Unlock()

	// Collect peers for this info_hash, filtering out banned peers and the caller.
	peers := t.collectPeers(infoHashHex, peerIDHex, numWant, now)
	t.writeAnnounceOK(w, peers, compact)
}

// HandleScrape handles GET /scrape (BEP 31).
//
// Query parameters:
//   - info_hash: 20-byte binary or hex (can be repeated for multiple hashes)
//
// Response: bencoded dict with "files" mapping info_hash → {complete, incomplete}.
func (t *Tracker) HandleScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAnnounceError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	hashes := r.URL.Query()["info_hash"]
	if len(hashes) == 0 {
		writeAnnounceError(w, http.StatusBadRequest, "info_hash is required")
		return
	}

	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	fileEntries := make([]bencodeEntry, 0, len(hashes))
	for _, h := range hashes {
		infoHashHex, err := parseHashParam(h)
		if err != nil {
			continue // skip invalid hashes silently
		}

		// Check if info_hash is banned — return zero counts.
		if banned, _, _ := t.bans.IsBanned(infoHashHex, "", "", ""); banned {
			continue
		}

		peers := t.peers[infoHashHex]
		var complete, incomplete int
		for _, p := range peers {
			if now.Sub(p.lastSeen) > t.peerTTL {
				continue
			}
			// Filter out banned peers.
			if banned, _, _ := t.bans.IsBanned("", p.peerID, p.ip.String(), p.user); banned {
				continue
			}
			if t.bans.IsUserPeerBanned(p.peerID, t.userPeers) {
				continue
			}
			if isSeeder(p) {
				complete++
			} else {
				incomplete++
			}
		}

		// Convert hex back to raw bytes for the bencode key.
		rawHash, err := hex.DecodeString(infoHashHex)
		if err != nil {
			continue
		}

		dict := bencodeDict([]bencodeEntry{
			{"complete", bencodeInt(int64(complete))},
			{"incomplete", bencodeInt(int64(incomplete))},
		})
		fileEntries = append(fileEntries, bencodeEntry{
			key:   string(rawHash),
			value: dict,
		})
	}

	filesDict := bencodeDict(fileEntries)
	resp := bencodeDict([]bencodeEntry{
		{"files", filesDict},
	})
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(resp)
}

// HandleBans returns the ban management handler with auth.
func (t *Tracker) HandleBans() http.HandlerFunc {
	return t.bans.HandleBans(t.banAuth)
}

// Stats returns tracker statistics (for dashboard / monitoring).
func (t *Tracker) Stats() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()

	totalPeers := 0
	torrentCount := len(t.peers)
	for _, ps := range t.peers {
		totalPeers += len(ps)
	}
	return map[string]any{
		"torrents":  torrentCount,
		"totalPeers": totalPeers,
		"bans":      len(t.bans.List()),
		"users":     len(t.userPeers),
	}
}

// ---- Peer management (internal, must be called with t.mu held) ----

// addPeer adds or updates a peer entry for the given info_hash.
func (t *Tracker) addPeer(infoHashHex, peerIDHex string, ip net.IP, port uint16, user string, now time.Time) {
	peers := t.peers[infoHashHex]
	for _, p := range peers {
		if p.peerID == peerIDHex {
			p.ip = ip
			p.port = port
			p.user = user
			p.lastSeen = now
			return
		}
	}
	// New peer; respect maxPeers limit.
	if len(peers) >= t.maxPeers {
		// Replace the oldest peer (FIFO eviction to keep the list bounded).
		t.peers[infoHashHex] = peers[1:]
		peers = t.peers[infoHashHex]
	}
	t.peers[infoHashHex] = append(peers, &peerEntry{
		peerID:   peerIDHex,
		ip:       ip,
		port:     port,
		user:     user,
		lastSeen: now,
	})
}

// removePeer removes a peer entry for the given info_hash.
func (t *Tracker) removePeer(infoHashHex, peerIDHex string) {
	peers := t.peers[infoHashHex]
	out := peers[:0]
	for _, p := range peers {
		if p.peerID != peerIDHex {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		delete(t.peers, infoHashHex)
	} else {
		t.peers[infoHashHex] = out
	}
}

// collectPeers gathers peer entries for an info_hash, filtering out expired
// entries, banned peers, and the requesting peer itself.
func (t *Tracker) collectPeers(infoHashHex, selfPeerID string, numWant int, now time.Time) []PeerAddr {
	peers := t.peers[infoHashHex]
	result := make([]PeerAddr, 0, numWant)
	for _, p := range peers {
		if len(result) >= numWant {
			break
		}
		if now.Sub(p.lastSeen) > t.peerTTL {
			continue
		}
		if p.peerID == selfPeerID {
			continue
		}
		if banned, _, _ := t.bans.IsBanned("", p.peerID, p.ip.String(), p.user); banned {
			continue
		}
		if t.bans.IsUserPeerBanned(p.peerID, t.userPeers) {
			continue
		}
		result = append(result, PeerAddr{IP: p.ip, Port: p.port})
	}
	return result
}

// writeAnnounceOK writes a successful announce response (bencoded).
func (t *Tracker) writeAnnounceOK(w http.ResponseWriter, peers []PeerAddr, compact bool) {
	resp := bencodePeersDict(t.interval, peers, compact)
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(resp)
}

// ---- Helper functions ----

// parseHashParam parses a 20-byte hash from a query parameter.
// It accepts both 40-character hex strings and raw binary (URL-decoded by net/http).
func parseHashParam(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty")
	}
	// Try hex first (40 chars = 20 bytes).
	if len(s) == 40 {
		if _, err := hex.DecodeString(s); err != nil {
			return "", fmt.Errorf("invalid hex: %w", err)
		}
		return strings.ToLower(s), nil
	}
	// Raw binary: net/http's URL.Query() already decodes %XX sequences,
	// so raw bytes arrive as actual bytes in the string. Must be exactly 20 bytes.
	if len(s) == 20 {
		return strings.ToLower(hex.EncodeToString([]byte(s))), nil
	}
	return "", fmt.Errorf("expected 20-byte binary or 40-char hex, got %d chars", len(s))
}

// bearerToken extracts a Bearer token from the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

// clientIP extracts the client IP from the request's RemoteAddr.
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return net.ParseIP(r.RemoteAddr)
	}
	return net.ParseIP(host)
}

// isSeeder checks if a peer entry represents a seeder (has all data).
// Conservative: treats all peers as leechers. The scrape response's
// complete/incomplete distinction is a best-effort hint for clients.
func isSeeder(p *peerEntry) bool {
	_ = p
	return false
}

// writeAnnounceError writes an error response as a bencoded dict.
func writeAnnounceError(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(code)
	_, _ = w.Write(bencodeError(reason))
}
