package transport

// admission_test.go — Unit tests for peer admission control & blocklist (Issue #89).

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	peerjs "github.com/Hana-ame/go-peerjs"
)

// fakeSessionForAdmission is a test double for transport.Session.
type fakeSessionForAdmission struct {
	id         string
	closed     bool
	sentFrames []map[string]any
}

func (s *fakeSessionForAdmission) ID() string { return s.id }
func (s *fakeSessionForAdmission) SendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	s.sentFrames = append(s.sentFrames, m)
	return nil
}
func (s *fakeSessionForAdmission) SendFrame(header any, body []byte) error { return nil }
func (s *fakeSessionForAdmission) OnMessage(fn func(peerjs.Frame))          {}
func (s *fakeSessionForAdmission) OnClose(fn func())                        {}
func (s *fakeSessionForAdmission) Close() {
	s.closed = true
}

// TestPeerBlocklist_AddRemoveList verifies in-memory & file persistence of blocked peers.
// 发现背景：Issue #89（节点连接准入控制：防恶意对端耗尽连接配额/黑名单阻断）。
func TestPeerBlocklist_AddRemoveList(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Initial list from config
	bl := NewPeerBlocklist(tmpDir, "peer-bad1, peer-bad2")
	assert.True(t, bl.IsBlocked("peer-bad1"))
	assert.True(t, bl.IsBlocked("peer-bad2"))
	assert.False(t, bl.IsBlocked("peer-good"))

	// 2. Dynamic block
	err := bl.Block("peer-bad3", "spamming requests")
	require.NoError(t, err)
	assert.True(t, bl.IsBlocked("peer-bad3"))

	// 3. Reload from disk
	bl2 := NewPeerBlocklist(tmpDir, "")
	assert.True(t, bl2.IsBlocked("peer-bad1"))
	assert.True(t, bl2.IsBlocked("peer-bad2"))
	assert.True(t, bl2.IsBlocked("peer-bad3"))

	list := bl2.List()
	require.Len(t, list, 3)
	assert.Equal(t, "peer-bad1", list[0].PeerID)

	// 4. Dynamic unblock
	err = bl2.Unblock("peer-bad2")
	require.NoError(t, err)
	assert.False(t, bl2.IsBlocked("peer-bad2"))

	// Reload again to verify removal persisted
	bl3 := NewPeerBlocklist(tmpDir, "")
	assert.False(t, bl3.IsBlocked("peer-bad2"))
	assert.True(t, bl3.IsBlocked("peer-bad1"))
	assert.True(t, bl3.IsBlocked("peer-bad3"))
}

// TestPeerJSService_Blocklist_DropsConnection verifies that blocked peers are rejected on connection.
// 发现背景：Issue #89。
func TestPeerJSService_Blocklist_DropsConnection(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	cfg.DownloadDir = t.TempDir()
	storageDir := t.TempDir()

	svc := NewPeerJSService(cfg, storageDir)
	defer svc.Close()

	err := svc.BlockPeer("abusive-node", "dos attack")
	require.NoError(t, err)
	assert.True(t, svc.IsPeerBlocked("abusive-node"))

	sess := &fakeSessionForAdmission{id: "abusive-node"}
	svc.bindConn(sess)

	// Session must be closed immediately
	assert.True(t, sess.closed, "connection from blocked peer must be immediately closed")

	// Must not be in active conns
	conns := svc.Connections()
	_, found := conns["abusive-node"]
	assert.False(t, found, "blocked peer must not appear in active connections")
}

// TestPeerJSService_BlockPeer_ClosesActiveConnection verifies that blocking a connected peer actively terminates session.
// 发现背景：Issue #89。
func TestPeerJSService_BlockPeer_ClosesActiveConnection(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	cfg.DownloadDir = t.TempDir()
	storageDir := t.TempDir()

	svc := NewPeerJSService(cfg, storageDir)
	defer svc.Close()

	sess := &fakeSessionForAdmission{id: "attacker-peer"}
	svc.bindConn(sess)
	assert.False(t, sess.closed)

	conns := svc.Connections()
	assert.Contains(t, conns, "attacker-peer")

	// Block the peer
	err := svc.BlockPeer("attacker-peer", "malicious activity detected")
	require.NoError(t, err)

	// Connection must be closed
	assert.True(t, sess.closed, "active session must be closed when blocked")

	// Must be evicted from active conns
	connsAfter := svc.Connections()
	assert.NotContains(t, connsAfter, "attacker-peer")
}

// TestPeerJSService_AnonPolicy_ShareOnly verifies that in "share_only" mode anonymous peers can only query share.
// 发现背景：Issue #89（匿名仅开放 share 禁用全量管理与检索）。
func TestPeerJSService_AnonPolicy_ShareOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	cfg.PeerAnonPolicy = "share_only"
	cfg.DownloadDir = t.TempDir()
	storageDir := t.TempDir()

	svc := NewPeerJSService(cfg, storageDir)
	defer svc.Close()

	sess := &fakeSessionForAdmission{id: "anon-node"}
	st := &connState{}

	// 1. "share" verb should be allowed
	blocked := svc.anonGate(sess, st, dcResp{Type: "share", ReqID: "r1"})
	assert.False(t, blocked, "share verb must be allowed in share_only policy")
	assert.Empty(t, sess.sentFrames)

	// 2. "list" verb should be blocked
	blocked = svc.anonGate(sess, st, dcResp{Type: "list", ReqID: "r2"})
	assert.True(t, blocked, "list verb must be rejected for anonymous peer")
	require.Len(t, sess.sentFrames, 1)
	assert.Equal(t, "err", sess.sentFrames[0]["type"])
	assert.Equal(t, "ANON_RESTRICTED", sess.sentFrames[0]["code"])

	// 3. "search" verb should be blocked
	sess.sentFrames = nil
	blocked = svc.anonGate(sess, st, dcResp{Type: "search", ReqID: "r3"})
	assert.True(t, blocked, "search verb must be rejected for anonymous peer")
	require.Len(t, sess.sentFrames, 1)
	assert.Equal(t, "ANON_RESTRICTED", sess.sentFrames[0]["code"])

	// 4. "upload" verb should be blocked
	sess.sentFrames = nil
	blocked = svc.anonGate(sess, st, dcResp{Type: "upload", ReqID: "r4"})
	assert.True(t, blocked, "upload verb must be rejected for anonymous peer")
	require.Len(t, sess.sentFrames, 1)
	assert.Equal(t, "ANON_RESTRICTED", sess.sentFrames[0]["code"])

	// 5. When authenticated (pskOK = true), all verbs are allowed
	sess.sentFrames = nil
	st.pskOK = true
	blocked = svc.anonGate(sess, st, dcResp{Type: "list", ReqID: "r5"})
	assert.False(t, blocked, "authenticated peer must not be blocked by anon policy")
	assert.Empty(t, sess.sentFrames)
}

// TestPeerJSService_AnonPolicy_Deny verifies that in "deny" mode all verbs are rejected.
// 发现背景：Issue #89。
func TestPeerJSService_AnonPolicy_Deny(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	cfg.PeerAnonPolicy = "deny"
	cfg.DownloadDir = t.TempDir()
	storageDir := t.TempDir()

	svc := NewPeerJSService(cfg, storageDir)
	defer svc.Close()

	sess := &fakeSessionForAdmission{id: "anon-node"}
	st := &connState{}

	// Both share and list are rejected
	blocked := svc.anonGate(sess, st, dcResp{Type: "share", ReqID: "r1"})
	assert.True(t, blocked)
	require.Len(t, sess.sentFrames, 1)
	assert.Equal(t, "ANON_DENIED", sess.sentFrames[0]["code"])

	sess.sentFrames = nil
	blocked = svc.anonGate(sess, st, dcResp{Type: "list", ReqID: "r2"})
	assert.True(t, blocked)
	require.Len(t, sess.sentFrames, 1)
	assert.Equal(t, "ANON_DENIED", sess.sentFrames[0]["code"])
}
