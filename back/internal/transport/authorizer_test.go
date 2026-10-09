package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// tokenAuthorizer is a mock Phase 7 Authorizer that grants access if token matches expected.
type tokenAuthorizer struct {
	expectedToken string
}

func (a *tokenAuthorizer) AuthorizeDownload(session Session, hash string, token string) bool {
	if isSelfSession(session) {
		return true
	}
	return token == a.expectedToken
}

// TestAuthorizer_DefaultBehavior verifies that DefaultAuthorizer faithfully reproduces
// the pre-Phase 7 gate behavior (ShareGate + local WS session check).
// 发现背景：Phase 7 预埋 Authorizer 接口，默认必须 100% 行为等价于原 ShareGate，不得影响未接入 Phase 7 的现有节点。
func TestAuthorizer_DefaultBehavior(t *testing.T) {
	initTestDB(t)
	svc := newShareTestService(t)
	content := []byte("default-authorizer-content")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	dlDir := t.TempDir()
	svc.cfg.DownloadDir = dlDir
	svc.fileIndex = NewFileIndexService(dlDir)
	t.Cleanup(svc.fileIndex.Close)

	inRoot := filepath.Join(dlDir, "test-file.txt")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	_, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	// Configure fake share gate: this hash is private, only "friend-peer" can access.
	svc.SetShareGate(fakeShareGate{private: hash, friends: []string{"friend-peer"}})

	// 1. Stranger: denied
	stranger := &fakeSession{id: "stranger-peer"}
	svc.serveFile(stranger, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"err"}, stranger.sentTypes(), "stranger should be denied for private file")

	// 2. Friend: allowed
	friend := &fakeSession{id: "friend-peer"}
	svc.serveFile(friend, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r2"})
	require.Equal(t, []string{"meta", "data", "done"}, friend.sentFrameTypes(), "friend should be allowed")

	// 3. Local session: allowed
	self := &fakeSession{id: "local-client", local: true}
	svc.serveFile(self, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r3"})
	require.Equal(t, []string{"meta", "data", "done"}, self.sentFrameTypes(), "local session should always be allowed")
}

// TestAuthorizer_CustomAuthorizerPluginPoint verifies that SetAuthorizer allows injecting
// a custom token-based authorizer without modifying the transport serveFile dispatch logic.
// 发现背景：Phase 7 (Identity) 到来时需直接注入身份验证逻辑，验证此注入点是否有效支持 token 鉴权与重置回退。
func TestAuthorizer_CustomAuthorizerPluginPoint(t *testing.T) {
	initTestDB(t)
	svc := newShareTestService(t)
	content := []byte("custom-authorizer-content")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	dlDir := t.TempDir()
	svc.cfg.DownloadDir = dlDir
	svc.fileIndex = NewFileIndexService(dlDir)
	t.Cleanup(svc.fileIndex.Close)

	inRoot := filepath.Join(dlDir, "token-file.txt")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	_, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	// Inject custom authorizer requiring token "valid-token-123"
	svc.SetAuthorizer(&tokenAuthorizer{expectedToken: "valid-token-123"})

	// Peer without token: denied
	p1 := &fakeSession{id: "any-peer"}
	svc.serveFile(p1, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"err"}, p1.sentTypes(), "peer without token should be denied")

	// Peer with wrong token: denied
	p2 := &fakeSession{id: "any-peer"}
	svc.serveFile(p2, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r2", Token: "wrong-token"})
	require.Equal(t, []string{"err"}, p2.sentTypes(), "peer with wrong token should be denied")

	// Peer with valid token: allowed
	p3 := &fakeSession{id: "any-peer"}
	svc.serveFile(p3, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r3", Token: "valid-token-123"})
	require.Equal(t, []string{"meta", "data", "done"}, p3.sentFrameTypes(), "peer with valid token should be allowed")

	// Reset authorizer to nil: should revert to default authorizer
	svc.SetAuthorizer(nil)
	p4 := &fakeSession{id: "any-peer"}
	svc.serveFile(p4, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r4"})
	require.Equal(t, []string{"meta", "data", "done"}, p4.sentFrameTypes(), "without gate configured, default authorizer allows public content")
}

// TestShare_TokenPluginPoints verifies requester token propagation in share frames
// and responder token generation in share-resp frames.
// 发现背景：Phase 7 中 share 帧需要支持可选的 requester token 及响应的身份标识，验证此注入点可用性。
func TestShare_TokenPluginPoints(t *testing.T) {
	svc := newShareTestService(t)

	// 1. ShareProviderWithToken receives requester token
	var receivedPeerID, receivedToken string
	svc.SetShareProviderWithToken(func(peerID, token string) ShareSnapshot {
		receivedPeerID = peerID
		receivedToken = token
		if token == "secret-pass" {
			return ShareSnapshot{
				Files: []ShareFileInfo{{Hash: "hash-secret", Name: "secret.txt", Size: 42}},
			}
		}
		return ShareSnapshot{}
	})

	// Also configure responder token generator
	svc.SetShareTokenGenerator(func(peerID string) string {
		return "responder-identity-token"
	})

	sess := &fakeSession{id: "requester-node"}
	svc.serveShare(sess, dcResp{Type: "share", ReqID: "s1", Token: "secret-pass"})

	require.Equal(t, "requester-node", receivedPeerID)
	require.Equal(t, "secret-pass", receivedToken)

	frames := sess.sentFrames()
	require.Len(t, frames, 1)
	header := frames[0].header
	require.Equal(t, "share-resp", header["type"])
	require.Equal(t, "responder-identity-token", header["token"])
	require.Equal(t, float64(1), header["total"])
}

// TestCredentialFunc_PluginPoint verifies connection-level credentialFunc and service-level fallback.
// 发现背景：Phase 7 中 FetchFromPeer / OpenStreamFrom 需要支持连接级凭据附加，验证 credentialFunc 注入与回调。
func TestCredentialFunc_PluginPoint(t *testing.T) {
	svc := newShareTestService(t)

	// Mock connection
	peerConn := &fakeSession{id: "peer-target"}
	st := &connState{fetches: make(map[string]*fetchState), verbWaits: make(map[string]chan []byte)}
	svc.conns["peer-target"] = peerConn
	svc.pending[peerConn] = st

	// 1. Default when no credentialFunc is set: token is empty
	cred := svc.credentialFor(peerConn, st)
	require.Empty(t, cred)

	// 2. SetDefaultCredentialFunc provides default token
	svc.SetDefaultCredentialFunc(func(peerID string) string {
		return "default-token-for-" + peerID
	})
	credDefault := svc.credentialFor(peerConn, st)
	require.Equal(t, "default-token-for-peer-target", credDefault)

	// 3. SetConnectionCredentialFunc overrides default for this connection
	svc.SetConnectionCredentialFunc("peer-target", func() string {
		return "connection-specific-token"
	})
	credConn := svc.credentialFor(peerConn, st)
	require.Equal(t, "connection-specific-token", credConn)
}
