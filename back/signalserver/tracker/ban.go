// Ban management for the BitTorrent tracker.
//
// Ban types:
//   - info_hash: block an entire torrent (all peers for that info_hash)
//   - peer_id: block a specific peer by its 20-byte peer_id
//   - ip: block a source IP address
//   - user: block a registered user (maps to all peer_ids that user has announced with)
//
// Persistence: bans are stored as a JSON file (default: tracker_bans.json).
// The file is loaded on startup and saved (debounced) on every change.
// JSON was chosen over a database because (1) the ban list is small and
// human-readable, (2) both signalserver (no DB) and regserver (SQLite with
// an unrelated schema) can share the same format, and (3) operators can
// inspect/backup the file without DB tooling.
//
// User-ban mapping: the tracker is fundamentally an anonymous peer model.
// "Ban user" works by optionally accepting a `user` query parameter or a
// Bearer JWT on announce. When present, the tracker records user→peer_id
// associations. Banning a user blocks all known peer_ids for that user AND
// rejects any future announce carrying that user identity. The VerifyUser
// callback (injected at construction) bridges to the regserver's JWT system.
package tracker

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
)

// BanType enumerates the scoping dimension of a ban.
type BanType string

const (
	BanInfoHash BanType = "info_hash" // block an entire torrent by info_hash hex
	BanPeerID   BanType = "peer_id"   // block a specific peer_id (40-char hex)
	BanIP       BanType = "ip"        // block a source IP address
	BanUser     BanType = "user"      // block a registered user (regserver username)
)

// Ban is a single ban entry. Target is a hex-encoded info_hash, hex-encoded
// peer_id, IP address string, or username depending on Type.
type Ban struct {
	Type    BanType `json:"type"`
	Target  string  `json:"target"`
	Reason  string  `json:"reason,omitempty"`
	Created time.Time `json:"created"`
}

// BanStore holds the live ban set and its persistence backend.
type BanStore struct {
	mu   sync.RWMutex
	bans []Ban

	// Indexes for O(1) lookup on announce.
	byInfoHash map[string]bool // hex info_hash → banned
	byPeerID   map[string]bool // hex peer_id → banned
	byIP       map[string]bool // IP string → banned
	byUser     map[string]bool // username → banned

	file     string // persistence path; empty = no persistence
	onChange func() // debounced save trigger
}

// NewBanStore creates a ban store, optionally loading from a JSON file.
// If file is non-empty, bans are loaded from it; if the file does not exist
// yet, the store starts empty and will be created on the first ban.
func NewBanStore(file string, onChange func()) *BanStore {
	bs := &BanStore{
		bans:       make([]Ban, 0),
		byInfoHash: make(map[string]bool),
		byPeerID:   make(map[string]bool),
		byIP:       make(map[string]bool),
		byUser:     make(map[string]bool),
		file:       file,
		onChange:   onChange,
	}
	if file != "" {
		bs.load()
	}
	return bs
}

// IsBanned checks whether the given info_hash, peer_id, IP, or user is banned.
// The order of checks matters for the banUser case: a user ban must also
// catch peer_ids that the banned user previously announced.
func (bs *BanStore) IsBanned(infoHashHex, peerIDHex, ip, user string) (bool, BanType, string) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	if infoHashHex != "" && bs.byInfoHash[infoHashHex] {
		for _, b := range bs.bans {
			if b.Type == BanInfoHash && b.Target == infoHashHex {
				return true, BanInfoHash, b.Reason
			}
		}
		return true, BanInfoHash, ""
	}
	if peerIDHex != "" && bs.byPeerID[peerIDHex] {
		for _, b := range bs.bans {
			if b.Type == BanPeerID && b.Target == peerIDHex {
				return true, BanPeerID, b.Reason
			}
		}
		return true, BanPeerID, ""
	}
	if ip != "" && bs.byIP[ip] {
		for _, b := range bs.bans {
			if b.Type == BanIP && b.Target == ip {
				return true, BanIP, b.Reason
			}
		}
		return true, BanIP, ""
	}
	if user != "" && bs.byUser[user] {
		for _, b := range bs.bans {
			if b.Type == BanUser && b.Target == user {
				return true, BanUser, b.Reason
			}
		}
		return true, BanUser, ""
	}
	return false, "", ""
}

// IsUserBanned checks whether a username is banned.
func (bs *BanStore) IsUserBanned(user string) bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	return bs.byUser[user]
}

// IsUserPeerBanned checks whether a peer_id belongs to a banned user.
// This catches peers that announced with a user identity that was later banned.
func (bs *BanStore) IsUserPeerBanned(peerIDHex string, userPeers map[string]map[string]bool) bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	for user := range bs.byUser {
		if peers, ok := userPeers[user]; ok && peers[peerIDHex] {
			return true
		}
	}
	return false
}

// Add adds a ban, creating indexes. Returns false if the ban already exists.
func (bs *BanStore) Add(b Ban) bool {
	bs.mu.Lock()

	// Check for duplicate
	for _, existing := range bs.bans {
		if existing.Type == b.Type && existing.Target == b.Target {
			bs.mu.Unlock()
			return false
		}
	}

	if b.Created.IsZero() {
		b.Created = time.Now()
	}
	bs.bans = append(bs.bans, b)
	bs.indexBan(b)
	bs.mu.Unlock()

	if bs.onChange != nil {
		bs.onChange()
	}
	return true
}

// Remove removes a ban by type and target. Returns false if not found.
func (bs *BanStore) Remove(btype BanType, target string) bool {
	bs.mu.Lock()

	for i, b := range bs.bans {
		if b.Type == btype && b.Target == target {
			bs.bans = append(bs.bans[:i], bs.bans[i+1:]...)
			bs.unindexBan(b)
			bs.mu.Unlock()
			if bs.onChange != nil {
				bs.onChange()
			}
			return true
		}
	}
	bs.mu.Unlock()
	return false
}

// List returns a copy of all bans.
func (bs *BanStore) List() []Ban {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	out := make([]Ban, len(bs.bans))
	copy(out, bs.bans)
	return out
}

func (bs *BanStore) indexBan(b Ban) {
	switch b.Type {
	case BanInfoHash:
		bs.byInfoHash[b.Target] = true
	case BanPeerID:
		bs.byPeerID[b.Target] = true
	case BanIP:
		bs.byIP[b.Target] = true
	case BanUser:
		bs.byUser[b.Target] = true
	}
}

func (bs *BanStore) unindexBan(b Ban) {
	switch b.Type {
	case BanInfoHash:
		delete(bs.byInfoHash, b.Target)
	case BanPeerID:
		delete(bs.byPeerID, b.Target)
	case BanIP:
		delete(bs.byIP, b.Target)
	case BanUser:
		delete(bs.byUser, b.Target)
	}
}

// load reads bans from the JSON persistence file.
func (bs *BanStore) load() {
	data, err := os.ReadFile(bs.file)
	if err != nil {
		if os.IsNotExist(err) {
			return // first run, no bans yet
		}
		return // corrupt file; start empty rather than fail to start
	}
	var fileData struct {
		Version int   `json:"version"`
		Bans    []Ban `json:"bans"`
	}
	if err := json.Unmarshal(data, &fileData); err != nil {
		return // corrupt file; start empty
	}
	for _, b := range fileData.Bans {
		bs.indexBan(b)
	}
	bs.bans = append(bs.bans, fileData.Bans...)
}

// Save persists the current ban set to the JSON file.
func (bs *BanStore) Save() error {
	bs.mu.RLock()
	data := struct {
		Version int   `json:"version"`
		Bans    []Ban `json:"bans"`
	}{
		Version: 1,
		Bans:    make([]Ban, len(bs.bans)),
	}
	copy(data.Bans, bs.bans)
	bs.mu.RUnlock()

	// Write to temp file then rename for atomicity.
	tmp := bs.file + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(data); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, bs.file)
}

// SaveJSON writes the ban store to a JSON string (used by Save and tests).
func (bs *BanStore) SaveJSON() (string, error) {
	bs.mu.RLock()
	data := struct {
		Version int   `json:"version"`
		Bans    []Ban `json:"bans"`
	}{
		Version: 1,
		Bans:    make([]Ban, len(bs.bans)),
	}
	copy(data.Bans, bs.bans)
	bs.mu.RUnlock()

	b, err := json.MarshalIndent(data, "", "  ")
	return string(b), err
}

// HandleBans manages ban entries via HTTP. Auth is checked via the injected
// banAuth function (ops token on signalserver, JWT on regserver).
//
// Routes (mounted by the caller):
//   - GET    /tracker/bans                    — list all bans
//   - POST   /tracker/bans                    — add a ban (JSON body)
//   - DELETE /tracker/bans?type=&target=      — remove a ban
func (bs *BanStore) HandleBans(auth func(*http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth != nil && !auth(r) {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"bans": bs.List()})
		case http.MethodPost:
			var req struct {
				Type   BanType `json:"type"`
				Target string  `json:"target"`
				Reason string  `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if req.Target == "" {
				writeJSONErr(w, http.StatusBadRequest, "target is required")
				return
			}
			b := Ban{Type: req.Type, Target: req.Target, Reason: req.Reason, Created: time.Now()}
			if !bs.Add(b) {
				writeJSONErr(w, http.StatusConflict, "ban already exists")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ban": b})
		case http.MethodDelete:
			q := r.URL.Query()
			btype := BanType(q.Get("type"))
			target := q.Get("target")
			if target == "" {
				writeJSONErr(w, http.StatusBadRequest, "target query param is required")
				return
			}
			if !bs.Remove(btype, target) {
				writeJSONErr(w, http.StatusNotFound, "ban not found")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeJSONErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONErr writes a JSON error response.
func writeJSONErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
