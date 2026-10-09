package transport

// admission.go — Peer admission control & blocklist management (Issue #89).
//
// Responsibilities:
//   1. Peer Blocklist: Ban malicious or untrusted peer IDs from connecting
//      (inbound dropped at handshake, outbound dial blocked, active connection terminated).
//      Persisted to <storageDir>/peer_blocklist.json.
//   2. Anonymous Policy: Restrict unauthenticated peers when PSK or authentication
//      is not present (modes: "open", "share_only", "deny").
//      In "share_only" mode, anonymous peers can only query shared scope ("share"),
//      preventing full metadata enumeration ("list", "search") or data mutations.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// BlockedPeerEntry represents a banned peer record.
type BlockedPeerEntry struct {
	PeerID    string    `json:"peer_id"`
	Reason    string    `json:"reason,omitempty"`
	BlockedAt time.Time `json:"blocked_at"`
}

// PeerBlocklist manages the set of blocked peer IDs and their persistence.
type PeerBlocklist struct {
	mu      sync.RWMutex
	path    string
	blocked map[string]BlockedPeerEntry
}

// NewPeerBlocklist creates a blocklist manager, optionally persisting to storageDir/peer_blocklist.json.
// initialList is comma-separated peer IDs (e.g. from PEERDRIVE_PEER_BLOCKLIST).
func NewPeerBlocklist(storageDir string, initialList string) *PeerBlocklist {
	b := &PeerBlocklist{
		blocked: make(map[string]BlockedPeerEntry),
	}
	if storageDir != "" {
		b.path = filepath.Join(storageDir, "peer_blocklist.json")
		b.load()
	}
	if initialList != "" {
		b.mu.Lock()
		for _, id := range strings.Split(initialList, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				if _, exists := b.blocked[id]; !exists {
					b.blocked[id] = BlockedPeerEntry{
						PeerID:    id,
						Reason:    "config initial blocklist",
						BlockedAt: time.Now(),
					}
				}
			}
		}
		_ = b.saveLocked()
		b.mu.Unlock()
	}
	return b
}

func (b *PeerBlocklist) load() {
	if b.path == "" {
		return
	}
	raw, err := os.ReadFile(b.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.LogWarn("peer-blocklist: read %s failed: %v", b.path, err)
		}
		return
	}
	var fileData struct {
		Blocked []BlockedPeerEntry `json:"blocked"`
	}
	if err := json.Unmarshal(raw, &fileData); err != nil {
		log.LogWarn("peer-blocklist: parse %s failed: %v", b.path, err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, entry := range fileData.Blocked {
		if entry.PeerID != "" {
			b.blocked[entry.PeerID] = entry
		}
	}
}

func (b *PeerBlocklist) saveLocked() error {
	if b.path == "" {
		return nil
	}
	fileData := struct {
		Blocked []BlockedPeerEntry `json:"blocked"`
	}{
		Blocked: make([]BlockedPeerEntry, 0, len(b.blocked)),
	}
	for _, entry := range b.blocked {
		fileData.Blocked = append(fileData.Blocked, entry)
	}
	sort.Slice(fileData.Blocked, func(i, j int) bool {
		return fileData.Blocked[i].PeerID < fileData.Blocked[j].PeerID
	})

	raw, err := json.MarshalIndent(fileData, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

// IsBlocked returns true if peerID is currently blocked.
func (b *PeerBlocklist) IsBlocked(peerID string) bool {
	if peerID == "" {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.blocked[peerID]
	return ok
}

// Block adds peerID to the blocklist.
func (b *PeerBlocklist) Block(peerID, reason string) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return fmt.Errorf("peer_id is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked[peerID] = BlockedPeerEntry{
		PeerID:    peerID,
		Reason:    reason,
		BlockedAt: time.Now(),
	}
	return b.saveLocked()
}

// Unblock removes peerID from the blocklist.
func (b *PeerBlocklist) Unblock(peerID string) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return fmt.Errorf("peer_id is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.blocked, peerID)
	return b.saveLocked()
}

// List returns a snapshot of all blocked peers.
func (b *PeerBlocklist) List() []BlockedPeerEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BlockedPeerEntry, 0, len(b.blocked))
	for _, entry := range b.blocked {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

// IsPeerBlocked checks whether a peer ID is in the blocklist.
func (s *PeerJSService) IsPeerBlocked(peerID string) bool {
	if s.blocklist == nil {
		return false
	}
	return s.blocklist.IsBlocked(peerID)
}

// BlockPeer adds peerID to the blocklist and actively terminates any existing connection.
func (s *PeerJSService) BlockPeer(peerID, reason string) error {
	if s.blocklist == nil {
		return fmt.Errorf("blocklist not initialized")
	}
	if err := s.blocklist.Block(peerID, reason); err != nil {
		return err
	}
	s.mu.Lock()
	conn, ok := s.conns[peerID]
	if ok {
		delete(s.conns, peerID)
	}
	s.mu.Unlock()
	if ok && conn != nil {
		if peerID != "local" {
			s.notifyDisconnect(peerID)
		}
		log.LogInfo("peerjs: terminating active connection to newly blocked peer %s", peerID)
		conn.Close()
	}
	return nil
}

// UnblockPeer removes peerID from the blocklist.
func (s *PeerJSService) UnblockPeer(peerID string) error {
	if s.blocklist == nil {
		return fmt.Errorf("blocklist not initialized")
	}
	return s.blocklist.Unblock(peerID)
}

// BlockedPeers returns the list of currently blocked peers.
func (s *PeerJSService) BlockedPeers() []BlockedPeerEntry {
	if s.blocklist == nil {
		return nil
	}
	return s.blocklist.List()
}

// anonGate checks inbound frame permissions against the configured anonymous policy.
// Returns true if the frame was rejected and processing should stop.
func (s *PeerJSService) anonGate(c Session, st *connState, r dcResp) bool {
	// Local session is the operator's admin channel, never restricted.
	if isSelfSession(c) {
		return false
	}
	// If peer has authenticated via PSK, they are no longer anonymous.
	st.mu.Lock()
	authenticated := st.pskOK
	st.mu.Unlock()
	if authenticated {
		return false
	}

	policy := "open"
	if s.cfg != nil && s.cfg.PeerAnonPolicy != "" {
		policy = s.cfg.PeerAnonPolicy
	}

	switch policy {
	case "share_only":
		// Only "share" and handshake frames are allowed for anonymous peers.
		if r.Type != "share" && servedVerbs[r.Type] {
			_ = c.SendJSON(dcResp{
				Type:  "err",
				Msg:   "admission: anonymous access restricted (only share is permitted)",
				Code:  "ANON_RESTRICTED",
				ReqID: r.ReqID,
				Hash:  r.Hash,
			})
			log.LogWarn("peerjs: verb %q from anonymous peer %s rejected by anon policy", r.Type, c.ID())
			return true
		}
	case "deny":
		if servedVerbs[r.Type] {
			_ = c.SendJSON(dcResp{
				Type:  "err",
				Msg:   "admission: anonymous access denied",
				Code:  "ANON_DENIED",
				ReqID: r.ReqID,
				Hash:  r.Hash,
			})
			log.LogWarn("peerjs: verb %q from anonymous peer %s denied by anon policy", r.Type, c.ID())
			return true
		}
	}
	return false
}
